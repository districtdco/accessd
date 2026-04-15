package mongoproxy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/districtdco/accessd/api/internal/assets"
	"github.com/districtdco/accessd/api/internal/connutil"
	"github.com/districtdco/accessd/api/internal/credentials"
	"github.com/districtdco/accessd/api/internal/sessions"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/pbkdf2"
)

type Config struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type SessionRegistration struct {
	SessionID string
	UserID    string
	AssetID   string
	AssetName string

	Engine   string
	Database string
	SSLMode  string

	UpstreamHost string
	UpstreamPort int

	RequestID string
}

type Service struct {
	cfg         Config
	logger      *slog.Logger
	sessionsSvc *sessions.Service
	credSvc     *credentials.Service

	mu        sync.Mutex
	listeners map[string]net.Listener
	active    map[net.Conn]struct{}
	closed    bool
	wg        sync.WaitGroup
}

func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.BindHost) == "" {
		return nil, fmt.Errorf("mongo proxy bind host is required")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return nil, fmt.Errorf("mongo proxy public host is required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.MaxSessionAge <= 0 {
		cfg.MaxSessionAge = 8 * time.Hour
	}
	if sessionsSvc == nil {
		return nil, fmt.Errorf("sessions service is required")
	}
	if credSvc == nil {
		return nil, fmt.Errorf("credentials service is required")
	}

	return &Service{
		cfg:         cfg,
		logger:      logger.With("component", "mongo_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		listeners:   map[string]net.Listener{},
		active:      map[net.Conn]struct{}{},
	}, nil
}

func (s *Service) RegisterSession(reg SessionRegistration) (string, int, error) {
	reg.SessionID = strings.TrimSpace(reg.SessionID)
	reg.UserID = strings.TrimSpace(reg.UserID)
	reg.AssetID = strings.TrimSpace(reg.AssetID)
	reg.AssetName = strings.TrimSpace(reg.AssetName)
	reg.Engine = normalizeEngine(reg.Engine)
	reg.Database = strings.TrimSpace(reg.Database)
	reg.SSLMode = strings.TrimSpace(reg.SSLMode)
	reg.UpstreamHost = strings.TrimSpace(reg.UpstreamHost)
	reg.RequestID = strings.TrimSpace(reg.RequestID)

	if reg.SessionID == "" || reg.UserID == "" || reg.AssetID == "" {
		return "", 0, fmt.Errorf("session_id, user_id, and asset_id are required")
	}
	if reg.UpstreamHost == "" || reg.UpstreamPort <= 0 || reg.UpstreamPort > 65535 {
		return "", 0, fmt.Errorf("upstream host and port are required")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("mongo proxy is shutting down")
	}
	if existing, ok := s.listeners[reg.SessionID]; ok {
		_ = existing.Close()
		delete(s.listeners, reg.SessionID)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindHost, "0"))
	if err != nil {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("listen mongo proxy: %w", err)
	}
	s.listeners[reg.SessionID] = ln
	s.mu.Unlock()

	port := ln.Addr().(*net.TCPAddr).Port
	s.logger.Info("mongo proxy session listener registered", "session_id", reg.SessionID, "request_id", reg.RequestID, "addr", ln.Addr().String())
	s.wg.Add(1)
	go s.serveSession(reg, ln)
	return s.cfg.PublicHost, port, nil
}

func (s *Service) serveSession(reg SessionRegistration, ln net.Listener) {
	defer s.wg.Done()
	defer s.unregister(reg.SessionID)
	defer ln.Close()

	var connWG sync.WaitGroup
	defer connWG.Wait()

	if s.cfg.MaxSessionAge > 0 {
		timer := time.AfterFunc(s.cfg.MaxSessionAge, func() {
			s.logger.Warn("mongo proxy max session duration reached; closing listener", "session_id", reg.SessionID, "request_id", reg.RequestID)
			_ = ln.Close()
		})
		defer timer.Stop()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.logger.Warn("mongo proxy accept failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
			}
			return
		}
		conn = connutil.WrapIdleTimeout(conn, s.cfg.IdleTimeout)
		s.trackConn(conn)
		connWG.Add(1)
		go func(c net.Conn) {
			defer connWG.Done()
			defer c.Close()
			defer s.untrackConn(c)
			s.logger.Info("mongo proxy client connected", "session_id", reg.SessionID, "request_id", reg.RequestID, "remote_addr", c.RemoteAddr().String())
			if err := s.handleSessionConn(reg, c); err != nil {
				s.logger.Warn("mongo proxy session failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
			} else {
				s.logger.Info("mongo proxy session ended", "session_id", reg.SessionID, "request_id", reg.RequestID)
			}
		}(conn)
	}
}

func (s *Service) handleSessionConn(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID: reg.SessionID,
		UserID:    reg.UserID,
		AssetID:   reg.AssetID,
		AssetName: reg.AssetName,
		RequestID: reg.RequestID,
		Action:    "dbeaver",
		Protocol:  sessions.ProtocolDB,
		AssetType: assets.TypeDatabase,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	ctx := context.Background()
	if err := s.sessionsSvc.MarkProxyConnected(ctx, lctx, client.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record mongo proxy connected", "session_id", reg.SessionID, "error", err)
	}
	if err := s.runProxyFlow(reg, client); err != nil {
		_ = s.sessionsSvc.MarkFailed(ctx, lctx, "db_proxy_failed")
		return err
	}
	if err := s.sessionsSvc.MarkEnded(ctx, lctx, "db_client_disconnected"); err != nil {
		s.logger.Warn("failed to mark mongo session ended", "session_id", reg.SessionID, "error", err)
	}
	return nil
}

func (s *Service) runProxyFlow(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID: reg.SessionID,
		UserID:    reg.UserID,
		AssetID:   reg.AssetID,
		AssetName: reg.AssetName,
		RequestID: reg.RequestID,
		Action:    "dbeaver",
		Protocol:  sessions.ProtocolDB,
		AssetType: assets.TypeDatabase,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	upstream, err := s.connectUpstream(reg)
	if err != nil {
		s.logger.Warn("mongo upstream connect failed", "session_id", reg.SessionID, "asset_id", reg.AssetID, "error", err)
		return fmt.Errorf("connect upstream: %w", err)
	}
	defer upstream.Close()

	upstreamCred, err := s.credSvc.ResolveForAsset(context.Background(), reg.AssetID, credentials.TypeDBPassword)
	if err != nil {
		return fmt.Errorf("resolve mongo credential: %w", err)
	}
	lctx.UpstreamUsername = strings.TrimSpace(upstreamCred.Username)
	if err := s.sessionsSvc.RecordCredentialUsage(context.Background(), lctx, credentials.TypeDBPassword, "proxy_upstream_auth", reg.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}

	if err := authenticateUpstreamMongo(upstream, strings.TrimSpace(upstreamCred.Username), upstreamCred.Secret, authDatabase(reg.Database)); err != nil {
		s.logger.Warn("mongo upstream auth failed", "session_id", reg.SessionID, "asset_id", reg.AssetID, "error", err)
		return fmt.Errorf("upstream auth: %w", err)
	}

	if err := s.sessionsSvc.MarkUpstreamConnected(context.Background(), lctx); err != nil {
		s.logger.Warn("failed to record mongo upstream connected", "session_id", reg.SessionID, "error", err)
	}

	return proxyBidirectional(client, upstream)
}

func (s *Service) connectUpstream(reg SessionRegistration) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)), s.cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	return connutil.WrapIdleTimeout(raw, s.cfg.IdleTimeout), nil
}

func proxyBidirectional(client, upstream net.Conn) error {
	errCh := make(chan error, 2)

	go func() {
		_, err := io.Copy(upstream, client)
		_ = closeWrite(upstream)
		errCh <- normalizeProxyErr(err)
	}()
	go func() {
		_, err := io.Copy(client, upstream)
		_ = closeWrite(client)
		errCh <- normalizeProxyErr(err)
	}()

	first := <-errCh
	second := <-errCh
	if first != nil {
		return first
	}
	return second
}

func normalizeProxyErr(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func closeWrite(conn net.Conn) error {
	type closeWriter interface {
		CloseWrite() error
	}
	cw, ok := conn.(closeWriter)
	if !ok {
		return nil
	}
	return cw.CloseWrite()
}

func normalizeEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mongo", "mongodb":
		return "mongo"
	default:
		return strings.TrimSpace(engine)
	}
}

func authDatabase(db string) string {
	clean := strings.TrimSpace(db)
	if clean == "" {
		return "admin"
	}
	return clean
}

func authenticateUpstreamMongo(conn net.Conn, username, password, authDB string) error {
	nonce, err := randomNonce()
	if err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	userEscaped := escapeSCRAMUsername(username)
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", userEscaped, nonce)
	clientFirst := "n,," + clientFirstBare

	startDoc := bson.D{
		{Key: "saslStart", Value: 1},
		{Key: "mechanism", Value: "SCRAM-SHA-256"},
		{Key: "payload", Value: primitive.Binary{Subtype: 0x00, Data: []byte(clientFirst)}},
		{Key: "autoAuthorize", Value: 1},
		{Key: "$db", Value: authDB},
	}
	startResp, err := mongoRunCommand(conn, startDoc)
	if err != nil {
		return fmt.Errorf("saslStart: %w", err)
	}
	convID, serverFirst, err := parseSASLResponse(startResp)
	if err != nil {
		return fmt.Errorf("parse saslStart response: %w", err)
	}

	serverFirstFields, err := parseSCRAMFields(serverFirst)
	if err != nil {
		return fmt.Errorf("parse server-first-message: %w", err)
	}
	serverNonce := strings.TrimSpace(serverFirstFields["r"])
	if serverNonce == "" || !strings.HasPrefix(serverNonce, nonce) {
		return fmt.Errorf("invalid server nonce")
	}
	saltB64 := strings.TrimSpace(serverFirstFields["s"])
	iterationsRaw := strings.TrimSpace(serverFirstFields["i"])
	if saltB64 == "" || iterationsRaw == "" {
		return fmt.Errorf("server-first-message missing salt/iterations")
	}
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return fmt.Errorf("decode salt: %w", err)
	}
	iterations, err := strconv.Atoi(iterationsRaw)
	if err != nil || iterations <= 0 {
		return fmt.Errorf("invalid iterations")
	}

	clientFinalNoProof := "c=biws,r=" + serverNonce
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalNoProof
	saltedPassword := pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	clientSignature := hmacSHA256(storedKey[:], []byte(authMessage))
	clientProof := xorBytes(clientKey, clientSignature)
	serverKey := hmacSHA256(saltedPassword, []byte("Server Key"))
	expectedServerSignature := hmacSHA256(serverKey, []byte(authMessage))

	clientFinal := clientFinalNoProof + ",p=" + base64.StdEncoding.EncodeToString(clientProof)
	continueDoc := bson.D{
		{Key: "saslContinue", Value: 1},
		{Key: "conversationId", Value: convID},
		{Key: "payload", Value: primitive.Binary{Subtype: 0x00, Data: []byte(clientFinal)}},
		{Key: "$db", Value: authDB},
	}
	continueResp, err := mongoRunCommand(conn, continueDoc)
	if err != nil {
		return fmt.Errorf("saslContinue: %w", err)
	}
	_, serverFinal, err := parseSASLResponse(continueResp)
	if err != nil {
		return fmt.Errorf("parse saslContinue response: %w", err)
	}
	serverFinalFields, err := parseSCRAMFields(serverFinal)
	if err != nil {
		return fmt.Errorf("parse server-final-message: %w", err)
	}
	serverSignatureB64 := strings.TrimSpace(serverFinalFields["v"])
	if serverSignatureB64 == "" {
		return fmt.Errorf("server-final-message missing verifier")
	}
	serverSignature, err := base64.StdEncoding.DecodeString(serverSignatureB64)
	if err != nil {
		return fmt.Errorf("decode server verifier: %w", err)
	}
	if !hmac.Equal(serverSignature, expectedServerSignature) {
		return fmt.Errorf("server verifier mismatch")
	}

	return nil
}

func randomNonce() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func escapeSCRAMUsername(username string) string {
	u := strings.ReplaceAll(username, "=", "=3D")
	u = strings.ReplaceAll(u, ",", "=2C")
	return u
}

func parseSCRAMFields(msg string) (map[string]string, error) {
	parts := strings.Split(strings.TrimSpace(msg), ",")
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid scram field %q", p)
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out, nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func parseSASLResponse(doc bson.Raw) (int32, string, error) {
	var parsed struct {
		ConversationID int32            `bson:"conversationId"`
		Payload        primitive.Binary `bson:"payload"`
	}
	if err := bson.Unmarshal(doc, &parsed); err != nil {
		return 0, "", err
	}
	return parsed.ConversationID, string(parsed.Payload.Data), nil
}

func mongoRunCommand(conn net.Conn, doc bson.D) (bson.Raw, error) {
	reqID := int32(time.Now().UnixNano())
	bodyDoc, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	msg := buildMongoOpMsg(reqID, bodyDoc)
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}
	return readMongoCommandReply(conn)
}

func buildMongoOpMsg(reqID int32, doc []byte) []byte {
	const headerLen = 16
	const opMsgCode = 2013
	bodyLen := 4 + 1 + len(doc) // flags + section kind + doc
	msgLen := headerLen + bodyLen
	buf := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(msgLen))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(reqID))
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], opMsgCode)
	binary.LittleEndian.PutUint32(buf[16:20], 0) // flags
	buf[20] = 0                                  // section kind 0
	copy(buf[21:], doc)
	return buf
}

func readMongoCommandReply(conn net.Conn) (bson.Raw, error) {
	header := make([]byte, 16)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	msgLen := int(binary.LittleEndian.Uint32(header[0:4]))
	if msgLen < 21 {
		return nil, fmt.Errorf("invalid mongo message length %d", msgLen)
	}
	opCode := int32(binary.LittleEndian.Uint32(header[12:16]))
	if opCode != 2013 {
		return nil, fmt.Errorf("unsupported mongo opcode %d", opCode)
	}
	body := make([]byte, msgLen-16)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	if len(body) < 5 {
		return nil, fmt.Errorf("invalid mongo response body")
	}
	kind := body[4]
	if kind != 0 {
		return nil, fmt.Errorf("unsupported mongo response section %d", kind)
	}
	doc := bson.Raw(body[5:])
	var okDoc struct {
		OK float64 `bson:"ok"`
	}
	if err := bson.Unmarshal(doc, &okDoc); err != nil {
		return nil, err
	}
	if okDoc.OK != 1 {
		var errDoc struct {
			CodeName string `bson:"codeName"`
			ErrMsg   string `bson:"errmsg"`
		}
		_ = bson.Unmarshal(doc, &errDoc)
		if strings.TrimSpace(errDoc.ErrMsg) != "" {
			return nil, fmt.Errorf("mongo command failed: %s (%s)", errDoc.ErrMsg, errDoc.CodeName)
		}
		return nil, fmt.Errorf("mongo command failed")
	}
	return doc, nil
}

func (s *Service) unregister(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ln, ok := s.listeners[sessionID]; ok {
		_ = ln.Close()
		delete(s.listeners, sessionID)
	}
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listeners := make([]net.Listener, 0, len(s.listeners))
	for _, ln := range s.listeners {
		listeners = append(listeners, ln)
	}
	s.listeners = map[string]net.Listener{}
	s.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.Close()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()

	select {
	case <-ctx.Done():
		s.closeActiveConns()
		<-done
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Service) trackConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn != nil {
		s.active[conn] = struct{}{}
	}
}

func (s *Service) untrackConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, conn)
}

func (s *Service) closeActiveConns() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.active))
	for conn := range s.active {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}
