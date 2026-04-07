package pgproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/connutil"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/sessions"
)

const (
	protocolVersion3 = 196608
	sslRequestCode   = 80877103
)

type Config struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	QueryLogQueue  int
	QueryMaxBytes  int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type SessionRegistration struct {
	SessionID string
	UserID    string
	AssetID   string

	Engine   string
	Database string
	SSLMode  string

	UpstreamHost string
	UpstreamPort int
	Username     string
	RequestID    string
}

type queryLogEvent struct {
	SessionID    string
	UserID       string
	AssetID      string
	Query        string
	EventTime    time.Time
	ProtocolType string // "simple" or "extended"
	Prepared     bool
}

// preparedStmtCache tracks named prepared statements per connection.
type preparedStmtCache struct {
	mu    sync.Mutex
	stmts map[string]string // statement name → SQL query
}

func newPreparedStmtCache() *preparedStmtCache {
	return &preparedStmtCache{stmts: make(map[string]string)}
}

func (c *preparedStmtCache) Store(name, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stmts[name] = query
}

func (c *preparedStmtCache) Lookup(name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q, ok := c.stmts[name]
	return q, ok
}

func (c *preparedStmtCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.stmts, name)
}

type Service struct {
	cfg         Config
	logger      *slog.Logger
	sessionsSvc *sessions.Service
	credSvc     *credentials.Service
	tlsConfig   *tls.Config

	mu        sync.Mutex
	listeners map[string]net.Listener
	active    map[net.Conn]struct{}
	closed    bool

	queryLogCh chan queryLogEvent
	wg         sync.WaitGroup
}

func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.BindHost) == "" {
		return nil, fmt.Errorf("pg proxy bind host is required")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return nil, fmt.Errorf("pg proxy public host is required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.QueryLogQueue <= 0 {
		cfg.QueryLogQueue = 1024
	}
	if cfg.QueryMaxBytes <= 0 {
		cfg.QueryMaxBytes = 16 * 1024
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

	s := &Service{
		cfg:         cfg,
		logger:      logger.With("component", "pg_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		listeners:   map[string]net.Listener{},
		active:      map[net.Conn]struct{}{},
		queryLogCh:  make(chan queryLogEvent, cfg.QueryLogQueue),
	}
	tlsCfg, err := buildProxyTLSConfig(cfg.PublicHost)
	if err != nil {
		return nil, fmt.Errorf("build pg proxy tls config: %w", err)
	}
	s.tlsConfig = tlsCfg

	s.wg.Add(1)
	go s.queryLogWorker()
	return s, nil
}

func (s *Service) RegisterSession(reg SessionRegistration) (string, int, error) {
	reg.SessionID = strings.TrimSpace(reg.SessionID)
	reg.UserID = strings.TrimSpace(reg.UserID)
	reg.AssetID = strings.TrimSpace(reg.AssetID)
	reg.Engine = strings.TrimSpace(reg.Engine)
	reg.Database = strings.TrimSpace(reg.Database)
	reg.SSLMode = strings.TrimSpace(reg.SSLMode)
	reg.UpstreamHost = strings.TrimSpace(reg.UpstreamHost)
	reg.Username = strings.TrimSpace(reg.Username)
	reg.RequestID = strings.TrimSpace(reg.RequestID)

	if reg.SessionID == "" || reg.UserID == "" || reg.AssetID == "" {
		return "", 0, fmt.Errorf("session_id, user_id, and asset_id are required")
	}
	if reg.UpstreamHost == "" || reg.UpstreamPort <= 0 || reg.UpstreamPort > 65535 {
		return "", 0, fmt.Errorf("upstream host and port are required")
	}
	if reg.Username == "" {
		return "", 0, fmt.Errorf("upstream username is required")
	}
	if reg.Engine == "" {
		reg.Engine = "postgres"
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("pg proxy is shutting down")
	}
	if existing, ok := s.listeners[reg.SessionID]; ok {
		_ = existing.Close()
		delete(s.listeners, reg.SessionID)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindHost, "0"))
	if err != nil {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("listen pg proxy: %w", err)
	}
	s.listeners[reg.SessionID] = ln
	s.mu.Unlock()

	port := ln.Addr().(*net.TCPAddr).Port
	s.logger.Info("pg proxy session listener registered", "session_id", reg.SessionID, "request_id", reg.RequestID, "addr", ln.Addr().String())

	s.wg.Add(1)
	go s.serveSession(reg, ln)
	return s.cfg.PublicHost, port, nil
}

func (s *Service) serveSession(reg SessionRegistration, ln net.Listener) {
	defer s.wg.Done()
	defer s.unregister(reg.SessionID)
	defer ln.Close()

	conn, err := ln.Accept()
	if err != nil {
		if !errors.Is(err, net.ErrClosed) {
			s.logger.Warn("pg proxy accept failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
		}
		return
	}
	conn = connutil.WrapIdleTimeout(conn, s.cfg.IdleTimeout)
	s.trackConn(conn)
	defer conn.Close()
	defer s.untrackConn(conn)

	if s.cfg.MaxSessionAge > 0 {
		timer := time.AfterFunc(s.cfg.MaxSessionAge, func() {
			s.logger.Warn("pg proxy max session duration reached; closing connection", "session_id", reg.SessionID, "request_id", reg.RequestID)
			_ = conn.Close()
		})
		defer timer.Stop()
	}

	if err := s.handleSessionConn(reg, conn); err != nil {
		s.logger.Warn("pg proxy session failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}
}

func (s *Service) handleSessionConn(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID: reg.SessionID,
		UserID:    reg.UserID,
		AssetID:   reg.AssetID,
		Action:    "dbeaver",
		Protocol:  sessions.ProtocolDB,
		AssetType: assets.TypeDatabase,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	ctx := context.Background()
	if err := s.sessionsSvc.MarkProxyConnected(ctx, lctx, client.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record db proxy connected", "session_id", reg.SessionID, "error", err)
	}

	if err := s.runProxyFlow(reg, client); err != nil {
		_ = s.sessionsSvc.MarkFailed(ctx, lctx, "db_proxy_failed")
		return err
	}

	if err := s.sessionsSvc.MarkEnded(ctx, lctx, "db_client_disconnected"); err != nil {
		s.logger.Warn("failed to mark db session ended", "session_id", reg.SessionID, "error", err)
	}
	return nil
}

func (s *Service) runProxyFlow(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID: reg.SessionID,
		UserID:    reg.UserID,
		AssetID:   reg.AssetID,
		Action:    "dbeaver",
		Protocol:  sessions.ProtocolDB,
		AssetType: assets.TypeDatabase,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	clientConn, startup, err := s.negotiateClientConn(client)
	if err != nil {
		return fmt.Errorf("read startup: %w", err)
	}

	upstream, err := s.connectUpstream(reg)
	if err != nil {
		return fmt.Errorf("connect upstream: %w", err)
	}
	defer upstream.Close()

	cred, err := s.credSvc.ResolveForAsset(context.Background(), reg.AssetID, credentials.TypeDBPassword)
	if err != nil {
		return fmt.Errorf("resolve db credential: %w", err)
	}
	if err := s.sessionsSvc.RecordCredentialUsage(context.Background(), lctx, credentials.TypeDBPassword, "proxy_upstream_auth", reg.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}

	database := reg.Database
	if database == "" {
		database = strings.TrimSpace(startup.Params["database"])
	}
	startupParams := map[string]string{}
	for k, v := range startup.Params {
		startupParams[k] = v
	}
	startupParams["user"] = reg.Username
	if database != "" {
		startupParams["database"] = database
	}

	if err := writeStartupPacket(upstream, startupParams); err != nil {
		return fmt.Errorf("forward startup: %w", err)
	}

	startupMsgs, err := authenticateUpstream(upstream, reg.Username, cred.Secret)
	if err != nil {
		return fmt.Errorf("upstream auth: %w", err)
	}

	if err := writeBackendMessage(clientConn, 'R', int32ToBytes(0)); err != nil {
		return fmt.Errorf("write client auth ok: %w", err)
	}
	for _, msg := range startupMsgs {
		if err := writeBackendMessage(clientConn, msg.Type, msg.Payload); err != nil {
			return fmt.Errorf("write startup msg: %w", err)
		}
	}

	backendErrCh := make(chan error, 1)
	go func() {
		_, cpErr := io.Copy(clientConn, upstream)
		backendErrCh <- cpErr
	}()

	frontendErrCh := make(chan error, 1)
	go func() {
		frontendErrCh <- s.forwardClientMessages(reg, clientConn, upstream)
	}()

	select {
	case err := <-frontendErrCh:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	case err := <-backendErrCh:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
}

func (s *Service) negotiateClientConn(raw net.Conn) (net.Conn, startupMessage, error) {
	payload, err := readStartupPacket(raw)
	if err != nil {
		return nil, startupMessage{}, err
	}
	if len(payload) < 4 {
		return nil, startupMessage{}, fmt.Errorf("invalid startup payload")
	}

	code := binary.BigEndian.Uint32(payload[:4])
	switch code {
	case sslRequestCode:
		if s.tlsConfig == nil {
			if _, err := raw.Write([]byte{'N'}); err != nil {
				return nil, startupMessage{}, err
			}
			startup, err := readClientStartup(raw)
			if err != nil {
				return nil, startupMessage{}, err
			}
			return raw, startup, nil
		}
		if _, err := raw.Write([]byte{'S'}); err != nil {
			return nil, startupMessage{}, err
		}
		tlsConn := tls.Server(raw, s.tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return nil, startupMessage{}, fmt.Errorf("client tls handshake failed: %w", err)
		}
		startup, err := readClientStartup(tlsConn)
		if err != nil {
			return nil, startupMessage{}, err
		}
		return tlsConn, startup, nil
	case protocolVersion3:
		return raw, startupMessage{Protocol: code, Params: parseStartupParams(payload[4:])}, nil
	default:
		return nil, startupMessage{}, fmt.Errorf("unsupported startup code: %d", code)
	}
}

func (s *Service) connectUpstream(reg SessionRegistration) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)), s.cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}

	sslCfg := classifySSLMode(reg.SSLMode)
	if !sslCfg.AttemptTLS {
		return connutil.WrapIdleTimeout(raw, s.cfg.IdleTimeout), nil
	}

	if err := writeSSLRequest(raw); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("send upstream ssl request: %w", err)
	}
	reply := make([]byte, 1)
	if _, err := io.ReadFull(raw, reply); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("read upstream ssl reply: %w", err)
	}
	if reply[0] != 'S' {
		if sslCfg.RequireTLS {
			_ = raw.Close()
			return nil, fmt.Errorf("upstream refused tls (ssl_mode=%s)", strings.TrimSpace(reg.SSLMode))
		}
		return connutil.WrapIdleTimeout(raw, s.cfg.IdleTimeout), nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: reg.UpstreamHost,
	}
	if !sslCfg.VerifyCert {
		tlsCfg.InsecureSkipVerify = true
	}
	tlsConn := tls.Client(raw, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("upstream tls handshake failed: %w", err)
	}
	return connutil.WrapIdleTimeout(tlsConn, s.cfg.IdleTimeout), nil
}

func (s *Service) forwardClientMessages(reg SessionRegistration, client io.Reader, upstream io.Writer) error {
	cache := newPreparedStmtCache()
	for {
		msgType, payload, err := readFrontendMessage(client)
		if err != nil {
			return err
		}
		switch msgType {
		case 'Q': // Simple query
			if query := parseQueryPayload(payload); query != "" {
				s.enqueueQueryLog(queryLogEvent{
					SessionID:    reg.SessionID,
					UserID:       reg.UserID,
					AssetID:      reg.AssetID,
					Query:        truncate(query, s.cfg.QueryMaxBytes),
					EventTime:    time.Now().UTC(),
					ProtocolType: "simple",
					Prepared:     false,
				})
			}
		case 'P': // Parse (extended protocol)
			name, query := parseParseMessage(payload)
			if query != "" {
				cache.Store(name, query)
				s.logger.Debug("prepared statement cached", "session_id", reg.SessionID, "stmt", name, "query_len", len(query))
			}
		case 'B': // Bind — no logging needed, just forward
		case 'E': // Execute (extended protocol)
			stmtName := parseExecuteMessage(payload)
			query, ok := cache.Lookup(stmtName)
			if ok && query != "" {
				s.enqueueQueryLog(queryLogEvent{
					SessionID:    reg.SessionID,
					UserID:       reg.UserID,
					AssetID:      reg.AssetID,
					Query:        truncate(query, s.cfg.QueryMaxBytes),
					EventTime:    time.Now().UTC(),
					ProtocolType: "extended",
					Prepared:     stmtName != "",
				})
			} else if !ok {
				s.logger.Warn("execute references unknown prepared statement", "session_id", reg.SessionID, "stmt", stmtName)
			}
		case 'C': // Close prepared statement or portal
			if target, name := parseCloseMessage(payload); target == 'S' {
				cache.Delete(name)
				s.logger.Debug("prepared statement closed", "session_id", reg.SessionID, "stmt", name)
			}
		case 'D', 'S', 'H': // Describe, Sync, Flush — forward only
		default:
			if msgType != 'X' && msgType != 'd' && msgType != 'c' && msgType != 'f' {
				s.logger.Debug("forwarding unknown frontend message type", "session_id", reg.SessionID, "type", string(msgType))
			}
		}
		if err := writeFrontendMessage(upstream, msgType, payload); err != nil {
			return err
		}
		if msgType == 'X' {
			return nil
		}
	}
}

func (s *Service) enqueueQueryLog(evt queryLogEvent) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case s.queryLogCh <- evt:
	default:
		s.logger.Warn("dropping db_query event due to full queue", "session_id", evt.SessionID)
	}
}

func (s *Service) queryLogWorker() {
	defer s.wg.Done()
	for evt := range s.queryLogCh {
		actor := evt.UserID
		if err := s.sessionsSvc.WriteEvent(context.Background(), evt.SessionID, sessions.EventDBQuery, &actor, map[string]any{
			"asset_id":      evt.AssetID,
			"event_time":    evt.EventTime.Format(time.RFC3339Nano),
			"query":         evt.Query,
			"protocol_type": evt.ProtocolType,
			"prepared":      evt.Prepared,
		}); err != nil {
			s.logger.Warn("failed to write db_query event", "session_id", evt.SessionID, "error", err)
		}
	}
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
	close(s.queryLogCh)
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

type startupMessage struct {
	Protocol uint32
	Params   map[string]string
}

type backendMessage struct {
	Type    byte
	Payload []byte
}

func readClientStartup(rw io.ReadWriter) (startupMessage, error) {
	for {
		payload, err := readStartupPacket(rw)
		if err != nil {
			return startupMessage{}, err
		}
		if len(payload) < 4 {
			return startupMessage{}, fmt.Errorf("invalid startup payload")
		}

		code := binary.BigEndian.Uint32(payload[:4])
		switch code {
		case sslRequestCode:
			if _, err := rw.Write([]byte{'N'}); err != nil {
				return startupMessage{}, err
			}
			continue
		case protocolVersion3:
			params := parseStartupParams(payload[4:])
			return startupMessage{Protocol: code, Params: params}, nil
		default:
			return startupMessage{}, fmt.Errorf("unsupported startup code: %d", code)
		}
	}
}

func writeSSLRequest(w io.Writer) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[:4], 8)
	binary.BigEndian.PutUint32(buf[4:], sslRequestCode)
	_, err := w.Write(buf)
	return err
}

func readStartupPacket(r io.Reader) ([]byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, err
	}
	totalLen := int(binary.BigEndian.Uint32(lenBuf))
	if totalLen < 8 {
		return nil, fmt.Errorf("invalid startup packet length: %d", totalLen)
	}
	payload := make([]byte, totalLen-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func parseStartupParams(raw []byte) map[string]string {
	params := map[string]string{}
	parts := strings.Split(string(raw), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		k := strings.TrimSpace(parts[i])
		v := strings.TrimSpace(parts[i+1])
		if k == "" {
			break
		}
		params[k] = v
	}
	return params
}

func writeStartupPacket(w io.Writer, params map[string]string) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.TrimSpace(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	payload := make([]byte, 0, 256)
	proto := make([]byte, 4)
	binary.BigEndian.PutUint32(proto, protocolVersion3)
	payload = append(payload, proto...)

	for _, k := range keys {
		v := strings.TrimSpace(params[k])
		if v == "" {
			continue
		}
		payload = append(payload, []byte(k)...)
		payload = append(payload, 0)
		payload = append(payload, []byte(v)...)
		payload = append(payload, 0)
	}
	payload = append(payload, 0)

	totalLen := make([]byte, 4)
	binary.BigEndian.PutUint32(totalLen, uint32(len(payload)+4))
	if _, err := w.Write(totalLen); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func authenticateUpstream(conn io.ReadWriter, username, password string) ([]backendMessage, error) {
	startupMessages := make([]backendMessage, 0, 8)
	for {
		msgType, payload, err := readBackendMessage(conn)
		if err != nil {
			return nil, err
		}
		switch msgType {
		case 'R':
			if len(payload) < 4 {
				return nil, fmt.Errorf("invalid authentication payload")
			}
			authType := binary.BigEndian.Uint32(payload[:4])
			switch authType {
			case 0:
				// AuthenticationOk from upstream; we'll return our own to the client.
			case 3:
				if err := writeFrontendMessage(conn, 'p', []byte(password+"\x00")); err != nil {
					return nil, err
				}
			case 5:
				if len(payload) < 8 {
					return nil, fmt.Errorf("invalid md5 auth payload")
				}
				hash := postgresMD5Password(password, username, payload[4:8])
				if err := writeFrontendMessage(conn, 'p', []byte(hash+"\x00")); err != nil {
					return nil, err
				}
			case 10: // AuthenticationSASL
				if err := handleSCRAMAuth(conn, payload[4:], username, password); err != nil {
					return nil, fmt.Errorf("scram auth: %w", err)
				}
			default:
				return nil, fmt.Errorf("unsupported upstream auth method: %d", authType)
			}
		case 'E':
			return nil, fmt.Errorf("upstream error: %s", parseErrorResponse(payload))
		default:
			startupMessages = append(startupMessages, backendMessage{Type: msgType, Payload: payload})
			if msgType == 'Z' {
				return startupMessages, nil
			}
		}
	}
}

func readFrontendMessage(r io.Reader) (byte, []byte, error) {
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, typeBuf); err != nil {
		return 0, nil, err
	}
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return 0, nil, err
	}
	totalLen := int(binary.BigEndian.Uint32(lenBuf))
	if totalLen < 4 {
		return 0, nil, fmt.Errorf("invalid frontend message length: %d", totalLen)
	}
	payload := make([]byte, totalLen-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typeBuf[0], payload, nil
}

func writeFrontendMessage(w io.Writer, msgType byte, payload []byte) error {
	if _, err := w.Write([]byte{msgType}); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)+4))
	if _, err := w.Write(lenBuf); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func readBackendMessage(r io.Reader) (byte, []byte, error) {
	return readFrontendMessage(r)
}

func writeBackendMessage(w io.Writer, msgType byte, payload []byte) error {
	return writeFrontendMessage(w, msgType, payload)
}

func int32ToBytes(v uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return buf
}

func parseQueryPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	query := string(payload)
	query = strings.TrimSuffix(query, "\x00")
	return strings.TrimSpace(query)
}

func parseErrorResponse(payload []byte) string {
	if len(payload) == 0 {
		return "unknown upstream error"
	}
	parts := strings.Split(string(payload), "\x00")
	msgs := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if part[0] == 'M' {
			msgs = append(msgs, part[1:])
		}
	}
	if len(msgs) == 0 {
		return "unknown upstream error"
	}
	return strings.Join(msgs, "; ")
}

// parseParseMessage extracts the prepared statement name and SQL query from
// a Parse ('P') message payload. Format: name\0 query\0 int16 param-count [int32 oids...]
func parseParseMessage(payload []byte) (name, query string) {
	idx := bytes.IndexByte(payload, 0)
	if idx < 0 {
		return "", ""
	}
	name = string(payload[:idx])
	rest := payload[idx+1:]
	idx2 := bytes.IndexByte(rest, 0)
	if idx2 < 0 {
		return name, string(rest)
	}
	return name, strings.TrimSpace(string(rest[:idx2]))
}

// parseExecuteMessage extracts the portal name from an Execute ('E') message.
// Format: portal\0 int32-max-rows
func parseExecuteMessage(payload []byte) string {
	idx := bytes.IndexByte(payload, 0)
	if idx < 0 {
		return ""
	}
	return string(payload[:idx])
}

// parseCloseMessage extracts the target type ('S' for statement, 'P' for portal)
// and name from a Close ('C') message. Format: byte-type name\0
func parseCloseMessage(payload []byte) (target byte, name string) {
	if len(payload) < 2 {
		return 0, ""
	}
	target = payload[0]
	rest := payload[1:]
	idx := bytes.IndexByte(rest, 0)
	if idx < 0 {
		return target, string(rest)
	}
	return target, string(rest[:idx])
}

// --- SCRAM-SHA-256 authentication ---

// handleSCRAMAuth performs SCRAM-SHA-256 authentication with the upstream server.
func handleSCRAMAuth(conn io.ReadWriter, mechanisms []byte, username, password string) error {
	// Parse mechanisms list (null-separated, double-null terminated).
	var hasSHA256 bool
	for _, mech := range strings.Split(strings.TrimRight(string(mechanisms), "\x00"), "\x00") {
		if mech == "SCRAM-SHA-256" {
			hasSHA256 = true
			break
		}
	}
	if !hasSHA256 {
		return fmt.Errorf("server does not support SCRAM-SHA-256 (offered: %q)", string(mechanisms))
	}

	// Generate client nonce.
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	clientNonce := base64.StdEncoding.EncodeToString(nonceBytes)

	// Client-first-message-bare: n=<username>,r=<nonce>
	// We use n=* (no channel binding), authzid omitted.
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", scramSaslName(username), clientNonce)
	clientFirstMsg := "n,," + clientFirstBare

	// Send SASLInitialResponse: mechanism\0 int32-len client-first-message
	resp := buildSASLInitialResponse("SCRAM-SHA-256", []byte(clientFirstMsg))
	if err := writeFrontendMessage(conn, 'p', resp); err != nil {
		return fmt.Errorf("send sasl initial response: %w", err)
	}

	// Read AuthenticationSASLContinue (type=11)
	msgType, payload, err := readBackendMessage(conn)
	if err != nil {
		return fmt.Errorf("read sasl continue: %w", err)
	}
	if msgType == 'E' {
		return fmt.Errorf("upstream error during SCRAM: %s", parseErrorResponse(payload))
	}
	if msgType != 'R' || len(payload) < 4 {
		return fmt.Errorf("unexpected message during SCRAM continue: type=%c", msgType)
	}
	if binary.BigEndian.Uint32(payload[:4]) != 11 {
		return fmt.Errorf("expected AuthenticationSASLContinue(11), got %d", binary.BigEndian.Uint32(payload[:4]))
	}
	serverFirstMsg := string(payload[4:])

	// Parse server-first-message.
	serverNonce, salt, iterations, err := parseServerFirst(serverFirstMsg)
	if err != nil {
		return fmt.Errorf("parse server-first: %w", err)
	}
	if !strings.HasPrefix(serverNonce, clientNonce) {
		return fmt.Errorf("server nonce does not start with client nonce")
	}

	// Derive keys.
	saltedPassword := scramHi([]byte(scramSaslPrep(password)), salt, iterations)
	clientKey := scramHMAC(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	serverKey := scramHMAC(saltedPassword, []byte("Server Key"))

	// Build client-final-message-without-proof.
	channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
	clientFinalWithoutProof := fmt.Sprintf("c=%s,r=%s", channelBinding, serverNonce)

	// AuthMessage = client-first-bare + "," + server-first-message + "," + client-final-without-proof
	authMessage := clientFirstBare + "," + serverFirstMsg + "," + clientFinalWithoutProof

	// ClientSignature = HMAC(StoredKey, AuthMessage)
	clientSig := scramHMAC(storedKey[:], []byte(authMessage))
	// ClientProof = ClientKey XOR ClientSignature
	clientProof := make([]byte, len(clientKey))
	for i := range clientKey {
		clientProof[i] = clientKey[i] ^ clientSig[i]
	}
	// ServerSignature = HMAC(ServerKey, AuthMessage)
	expectedServerSig := scramHMAC(serverKey, []byte(authMessage))

	// Send client-final-message.
	clientFinalMsg := clientFinalWithoutProof + ",p=" + base64.StdEncoding.EncodeToString(clientProof)
	if err := writeFrontendMessage(conn, 'p', []byte(clientFinalMsg)); err != nil {
		return fmt.Errorf("send sasl response: %w", err)
	}

	// Read AuthenticationSASLFinal (type=12)
	msgType, payload, err = readBackendMessage(conn)
	if err != nil {
		return fmt.Errorf("read sasl final: %w", err)
	}
	if msgType == 'E' {
		return fmt.Errorf("upstream error during SCRAM final: %s", parseErrorResponse(payload))
	}
	if msgType != 'R' || len(payload) < 4 {
		return fmt.Errorf("unexpected message during SCRAM final: type=%c", msgType)
	}
	if binary.BigEndian.Uint32(payload[:4]) != 12 {
		return fmt.Errorf("expected AuthenticationSASLFinal(12), got %d", binary.BigEndian.Uint32(payload[:4]))
	}

	// Verify server signature.
	serverFinalMsg := string(payload[4:])
	if !strings.HasPrefix(serverFinalMsg, "v=") {
		return fmt.Errorf("invalid server-final-message: %q", serverFinalMsg)
	}
	serverSigB64 := serverFinalMsg[2:]
	serverSig, err := base64.StdEncoding.DecodeString(serverSigB64)
	if err != nil {
		return fmt.Errorf("decode server signature: %w", err)
	}
	if !hmac.Equal(serverSig, expectedServerSig) {
		return fmt.Errorf("server signature mismatch")
	}

	// Read AuthenticationOk (type=0).
	msgType, payload, err = readBackendMessage(conn)
	if err != nil {
		return fmt.Errorf("read auth ok after SCRAM: %w", err)
	}
	if msgType == 'E' {
		return fmt.Errorf("upstream error after SCRAM: %s", parseErrorResponse(payload))
	}
	if msgType != 'R' || len(payload) < 4 || binary.BigEndian.Uint32(payload[:4]) != 0 {
		return fmt.Errorf("expected AuthenticationOk after SCRAM, got type=%c auth=%d", msgType, binary.BigEndian.Uint32(payload[:4]))
	}
	return nil
}

func buildSASLInitialResponse(mechanism string, data []byte) []byte {
	// mechanism\0 + int32(len(data)) + data
	buf := make([]byte, 0, len(mechanism)+1+4+len(data))
	buf = append(buf, []byte(mechanism)...)
	buf = append(buf, 0)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	buf = append(buf, lenBuf...)
	buf = append(buf, data...)
	return buf
}

func parseServerFirst(msg string) (nonce string, salt []byte, iterations int, err error) {
	for _, attr := range strings.Split(msg, ",") {
		if strings.HasPrefix(attr, "r=") {
			nonce = attr[2:]
		} else if strings.HasPrefix(attr, "s=") {
			salt, err = base64.StdEncoding.DecodeString(attr[2:])
			if err != nil {
				return "", nil, 0, fmt.Errorf("decode salt: %w", err)
			}
		} else if strings.HasPrefix(attr, "i=") {
			iterations, err = strconv.Atoi(attr[2:])
			if err != nil {
				return "", nil, 0, fmt.Errorf("parse iterations: %w", err)
			}
		}
	}
	if nonce == "" || salt == nil || iterations == 0 {
		return "", nil, 0, fmt.Errorf("incomplete server-first-message: %q", msg)
	}
	return nonce, salt, iterations, nil
}

// scramHi implements the SCRAM Hi() function (PBKDF2 with HMAC-SHA-256).
func scramHi(password, salt []byte, iterations int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	result := make([]byte, len(u))
	copy(result, u)
	for i := 1; i < iterations; i++ {
		mac = hmac.New(sha256.New, password)
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}

func scramHMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// scramSaslName escapes '=' and ',' in username per RFC 5802.
func scramSaslName(s string) string {
	s = strings.ReplaceAll(s, "=", "=3D")
	s = strings.ReplaceAll(s, ",", "=2C")
	return s
}

// scramSaslPrep is a minimal SASLprep — for ASCII passwords this is identity.
func scramSaslPrep(s string) string {
	return s
}

func postgresMD5Password(password, username string, salt []byte) string {
	first := md5.Sum([]byte(password + username))
	firstHex := hex.EncodeToString(first[:])
	secondInput := make([]byte, 0, len(firstHex)+len(salt))
	secondInput = append(secondInput, []byte(firstHex)...)
	secondInput = append(secondInput, salt...)
	second := md5.Sum(secondInput)
	return "md5" + hex.EncodeToString(second[:])
}

func truncate(v string, max int) string {
	if max <= 0 || len(v) <= max {
		return v
	}
	return v[:max]
}

type sslModeConfig struct {
	AttemptTLS bool
	RequireTLS bool
	VerifyCert bool
}

func classifySSLMode(raw string) sslModeConfig {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "disable":
		return sslModeConfig{}
	case "allow", "prefer", "":
		return sslModeConfig{AttemptTLS: true}
	case "require":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true}
	case "verify-ca", "verify-full":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true, VerifyCert: true}
	default:
		return sslModeConfig{AttemptTLS: true}
	}
}

func buildProxyTLSConfig(publicHost string) (*tls.Config, error) {
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject: pkix.Name{
			CommonName: "pam-pg-proxy",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	if host := strings.TrimSpace(publicHost); host != "" {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	template.IPAddresses = append(template.IPAddresses, net.ParseIP("127.0.0.1"))

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}
