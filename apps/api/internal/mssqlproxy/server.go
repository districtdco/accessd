package mssqlproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/districtdco/accessd/api/internal/assets"
	"github.com/districtdco/accessd/api/internal/connutil"
	"github.com/districtdco/accessd/api/internal/credentials"
	"github.com/districtdco/accessd/api/internal/sessions"
)

const (
	tdsPacketSQLBatch  byte = 0x01
	tdsPacketRPC       byte = 0x03
	tdsPacketResponse  byte = 0x04
	tdsPacketAttention byte = 0x06
	tdsPacketLogin7    byte = 0x10
	tdsPacketPreLogin  byte = 0x12

	tdsStatusEOM byte = 0x01
)

const (
	preloginOptVersion    byte = 0x00
	preloginOptEncryption byte = 0x01
	preloginOptMARS       byte = 0x04
	preloginOptTerminator byte = 0xff
)

const (
	tdsEncryptOff     byte = 0x00
	tdsEncryptOn      byte = 0x01
	tdsEncryptNotSup  byte = 0x02
	tdsEncryptRequire byte = 0x03
)

const login7FixedLen = 94

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
	AssetName string

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
	RequestID    string
	Engine       string
	Query        string
	EventTime    time.Time
	ProtocolType string
	Prepared     bool
}

type connectionPreparedState struct {
	mu                sync.Mutex
	lastSQLTemplate   string
	lastPreparedQuery string
}

func (s *connectionPreparedState) SetTemplate(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.TrimSpace(query)
	if q == "" {
		return
	}
	s.lastSQLTemplate = q
	s.lastPreparedQuery = q
}

func (s *connectionPreparedState) LastTemplate() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.lastPreparedQuery) != "" {
		return s.lastPreparedQuery
	}
	return s.lastSQLTemplate
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

	queryLogCh chan queryLogEvent
	wg         sync.WaitGroup
}

func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.BindHost) == "" {
		return nil, fmt.Errorf("mssql proxy bind host is required")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return nil, fmt.Errorf("mssql proxy public host is required")
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
		logger:      logger.With("component", "mssql_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		listeners:   map[string]net.Listener{},
		active:      map[net.Conn]struct{}{},
		queryLogCh:  make(chan queryLogEvent, cfg.QueryLogQueue),
	}
	s.wg.Add(1)
	go s.queryLogWorker()
	return s, nil
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

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("mssql proxy is shutting down")
	}
	if existing, ok := s.listeners[reg.SessionID]; ok {
		_ = existing.Close()
		delete(s.listeners, reg.SessionID)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindHost, "0"))
	if err != nil {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("listen mssql proxy: %w", err)
	}
	s.listeners[reg.SessionID] = ln
	s.mu.Unlock()

	port := ln.Addr().(*net.TCPAddr).Port
	s.logger.Info("mssql proxy session listener registered", "session_id", reg.SessionID, "request_id", reg.RequestID, "addr", ln.Addr().String())

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
			s.logger.Warn("mssql proxy max session duration reached; closing listener", "session_id", reg.SessionID, "request_id", reg.RequestID)
			_ = ln.Close()
		})
		defer timer.Stop()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.logger.Warn("mssql proxy accept failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
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
			if err := s.handleSessionConn(reg, c); err != nil {
				s.logger.Warn("mssql proxy session failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
			}
		}(conn)
	}
}

func (s *Service) handleSessionConn(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID:        reg.SessionID,
		UserID:           reg.UserID,
		AssetID:          reg.AssetID,
		AssetName:        reg.AssetName,
		RequestID:        reg.RequestID,
		Action:           "dbeaver",
		Protocol:         sessions.ProtocolDB,
		AssetType:        assets.TypeDatabase,
		Host:             reg.UpstreamHost,
		Port:             reg.UpstreamPort,
		UpstreamUsername: reg.Username,
	}

	ctx := context.Background()
	s.logger.Info("mssql client connected", "session_id", reg.SessionID, "remote_addr", client.RemoteAddr().String())
	if err := s.sessionsSvc.MarkProxyConnected(ctx, lctx, client.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record mssql proxy connected", "session_id", reg.SessionID, "error", err)
	}

	if err := s.runProxyFlow(reg, client); err != nil {
		_ = s.sessionsSvc.MarkFailed(ctx, lctx, "db_proxy_failed")
		return err
	}

	if err := s.sessionsSvc.MarkEnded(ctx, lctx, "db_client_disconnected"); err != nil {
		s.logger.Warn("failed to mark mssql session ended", "session_id", reg.SessionID, "error", err)
	}
	return nil
}

func (s *Service) runProxyFlow(reg SessionRegistration, client net.Conn) error {
	lctx := sessions.LaunchContext{
		SessionID:        reg.SessionID,
		UserID:           reg.UserID,
		AssetID:          reg.AssetID,
		AssetName:        reg.AssetName,
		RequestID:        reg.RequestID,
		Action:           "dbeaver",
		Protocol:         sessions.ProtocolDB,
		AssetType:        assets.TypeDatabase,
		Host:             reg.UpstreamHost,
		Port:             reg.UpstreamPort,
		UpstreamUsername: reg.Username,
	}

	clientPrelogin, err := readTDSMessage(client)
	if err != nil {
		return fmt.Errorf("read client prelogin: %w", err)
	}
	if clientPrelogin.PacketType != tdsPacketPreLogin {
		return fmt.Errorf("unexpected first client packet type: 0x%02x", clientPrelogin.PacketType)
	}

	clientEncrypt, hasClientEncrypt := parsePreloginEncryption(clientPrelogin.Payload)
	if hasClientEncrypt && clientEncrypt == tdsEncryptRequire {
		s.logger.Warn("mssql client requires tls, unsupported in this proxy slice", "session_id", reg.SessionID)
		return fmt.Errorf("client requested required tls; mssql proxy currently supports non-tls client connections only")
	}

	// First pass: explicit plaintext client-proxy path for practical query visibility.
	// TDS pre-login responses are emitted as server response packets.
	// Returning ENCRYPT_OFF keeps plaintext mode explicit for this proxy slice.
	proxyPreloginResp := buildPreloginMessage(tdsEncryptOff)
	if err := writeTDSPayload(client, tdsPacketResponse, proxyPreloginResp); err != nil {
		return fmt.Errorf("write prelogin response: %w", err)
	}

	clientLogin, err := readTDSMessage(client)
	if err != nil {
		return fmt.Errorf("read client login7: %w", err)
	}
	if clientLogin.PacketType != tdsPacketLogin7 {
		return fmt.Errorf("expected login7 packet, got 0x%02x", clientLogin.PacketType)
	}

	cred, err := s.credSvc.ResolveForAsset(context.Background(), reg.AssetID, credentials.TypeDBPassword)
	if err != nil {
		return fmt.Errorf("resolve db credential: %w", err)
	}
	if err := s.sessionsSvc.RecordCredentialUsage(context.Background(), lctx, credentials.TypeDBPassword, "proxy_upstream_auth", reg.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}

	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)), s.cfg.ConnectTimeout)
	if err != nil {
		return fmt.Errorf("dial upstream: %w", err)
	}
	upstream = connutil.WrapIdleTimeout(upstream, s.cfg.IdleTimeout)
	defer upstream.Close()
	s.logger.Info("mssql upstream connected", "session_id", reg.SessionID, "upstream", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)))
	if err := s.sessionsSvc.MarkUpstreamConnected(context.Background(), sessions.LaunchContext{
		SessionID:        reg.SessionID,
		UserID:           reg.UserID,
		AssetID:          reg.AssetID,
		AssetName:        reg.AssetName,
		RequestID:        reg.RequestID,
		Action:           "dbeaver",
		Protocol:         sessions.ProtocolDB,
		AssetType:        assets.TypeDatabase,
		Host:             reg.UpstreamHost,
		Port:             reg.UpstreamPort,
		UpstreamUsername: reg.Username,
	}); err != nil {
		s.logger.Warn("failed to record mssql upstream connected", "session_id", reg.SessionID, "error", err)
	}

	upstreamEnc, err := s.negotiateUpstreamPrelogin(reg, upstream)
	if err != nil {
		s.logger.Warn("mssql upstream prelogin failed", "session_id", reg.SessionID, "error", err)
		return err
	}
	if upstreamEnc == tdsEncryptOn || upstreamEnc == tdsEncryptRequire {
		return fmt.Errorf("upstream requested tls encryption; mssql proxy tls tunnel mode is not yet implemented")
	}

	database := strings.TrimSpace(reg.Database)
	if database == "" {
		database = parseLogin7Database(clientLogin.Payload)
	}
	loginPayload, err := rewriteLogin7WithManagedCreds(clientLogin.Payload, reg.Username, cred.Secret, database)
	if err != nil {
		s.logger.Warn("mssql login7 rewrite failed", "session_id", reg.SessionID, "error", err)
		return fmt.Errorf("rewrite login7: %w", err)
	}
	if err := writeTDSPayload(upstream, tdsPacketLogin7, loginPayload); err != nil {
		return fmt.Errorf("write upstream login7: %w", err)
	}

	authErr, err := relayLoginResponse(upstream, client)
	if err != nil {
		s.logger.Warn("mssql auth negotiation failed", "session_id", reg.SessionID, "error", err)
		return fmt.Errorf("relay login response: %w", err)
	}
	if strings.TrimSpace(authErr) != "" {
		s.logger.Warn("mssql upstream login failed", "session_id", reg.SessionID)
		return fmt.Errorf("upstream login failed")
	}

	state := &connectionPreparedState{}
	backendErrCh := make(chan error, 1)
	go func() {
		_, cpErr := io.Copy(client, upstream)
		backendErrCh <- cpErr
	}()

	frontendErrCh := make(chan error, 1)
	go func() {
		frontendErrCh <- s.forwardClientMessages(reg, client, upstream, state)
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

func (s *Service) negotiateUpstreamPrelogin(reg SessionRegistration, upstream net.Conn) (byte, error) {
	sslCfg := classifySSLMode(reg.SSLMode)
	if sslCfg.RequireTLS {
		return 0, fmt.Errorf("ssl_mode=%s requires tls, which is not yet implemented for mssql proxy", reg.SSLMode)
	}

	encReq := tdsEncryptOff
	if sslCfg.AttemptTLS {
		// Explicitly request non-encrypted upstream login in this first pass.
		encReq = tdsEncryptOff
	}
	if err := writeTDSPayload(upstream, tdsPacketPreLogin, buildPreloginMessage(encReq)); err != nil {
		return 0, fmt.Errorf("write upstream prelogin: %w", err)
	}

	resp, err := readTDSMessage(upstream)
	if err != nil {
		return 0, fmt.Errorf("read upstream prelogin response: %w", err)
	}
	// Real SQL Server instances commonly answer pre-login with response packets.
	// Accept both to stay interoperable across server builds/proxies.
	if resp.PacketType != tdsPacketPreLogin && resp.PacketType != tdsPacketResponse {
		return 0, fmt.Errorf("unexpected upstream prelogin response type: 0x%02x", resp.PacketType)
	}
	enc, _ := parsePreloginEncryption(resp.Payload)
	return enc, nil
}

func (s *Service) forwardClientMessages(reg SessionRegistration, client io.Reader, upstream io.Writer, state *connectionPreparedState) error {
	for {
		msg, err := readTDSMessage(client)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}

		s.handleQueryCapture(reg, msg.PacketType, msg.Payload, state)

		for _, packet := range msg.Packets {
			if _, err := upstream.Write(packet); err != nil {
				return err
			}
		}
		if msg.PacketType == tdsPacketAttention {
			s.logger.Debug("forwarded mssql attention packet", "session_id", reg.SessionID)
		}
	}
}

func (s *Service) handleQueryCapture(reg SessionRegistration, packetType byte, payload []byte, state *connectionPreparedState) {
	switch packetType {
	case tdsPacketSQLBatch:
		query := extractSQLBatchQuery(payload)
		if query == "" {
			return
		}
		s.enqueueQueryLog(queryLogEvent{
			SessionID:    reg.SessionID,
			UserID:       reg.UserID,
			AssetID:      reg.AssetID,
			RequestID:    reg.RequestID,
			Engine:       normalizeEngine(reg.Engine),
			Query:        truncate(query, s.cfg.QueryMaxBytes),
			EventTime:    time.Now().UTC(),
			ProtocolType: "sql_batch",
			Prepared:     false,
		})
	case tdsPacketRPC:
		procName, sqlText := parseRPCQuery(payload)
		procLower := strings.ToLower(strings.TrimSpace(procName))
		prepared := false
		query := strings.TrimSpace(sqlText)

		switch procLower {
		case "sp_prepare", "sp_prepexec", "sp_executesql":
			prepared = true
			if query != "" {
				state.SetTemplate(query)
			} else {
				query = state.LastTemplate()
			}
		case "sp_execute", "sp_cursorprepexec", "sp_cursorexecute":
			prepared = true
			if query == "" {
				query = state.LastTemplate()
			}
		default:
			if query != "" && strings.Contains(strings.ToLower(query), "@p") {
				prepared = true
			}
		}

		if query == "" {
			if procLower != "" {
				s.logger.Debug("mssql rpc query template not derivable", "session_id", reg.SessionID, "proc", procLower)
			}
			return
		}

		s.enqueueQueryLog(queryLogEvent{
			SessionID:    reg.SessionID,
			UserID:       reg.UserID,
			AssetID:      reg.AssetID,
			RequestID:    reg.RequestID,
			Engine:       normalizeEngine(reg.Engine),
			Query:        truncate(query, s.cfg.QueryMaxBytes),
			EventTime:    time.Now().UTC(),
			ProtocolType: "rpc",
			Prepared:     prepared,
		})
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
			"engine":        evt.Engine,
			"event_time":    evt.EventTime.Format(time.RFC3339Nano),
			"query":         evt.Query,
			"protocol_type": evt.ProtocolType,
			"prepared":      evt.Prepared,
			"request_id":    strings.TrimSpace(evt.RequestID),
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

type tdsMessage struct {
	PacketType byte
	Payload    []byte
	Packets    [][]byte
}

func readTDSMessage(r io.Reader) (tdsMessage, error) {
	head := make([]byte, 8)
	out := tdsMessage{}
	for {
		if _, err := io.ReadFull(r, head); err != nil {
			return tdsMessage{}, err
		}
		packetType := head[0]
		status := head[1]
		length := int(binary.BigEndian.Uint16(head[2:4]))
		if length < 8 {
			return tdsMessage{}, fmt.Errorf("invalid tds packet length: %d", length)
		}
		payload := make([]byte, length-8)
		if _, err := io.ReadFull(r, payload); err != nil {
			return tdsMessage{}, err
		}

		raw := make([]byte, 0, length)
		raw = append(raw, head...)
		raw = append(raw, payload...)

		if out.PacketType == 0 {
			out.PacketType = packetType
		}
		out.Packets = append(out.Packets, raw)
		out.Payload = append(out.Payload, payload...)
		if status&tdsStatusEOM != 0 {
			break
		}
	}
	return out, nil
}

func writeTDSPayload(w io.Writer, packetType byte, payload []byte) error {
	const maxPayload = 4096
	if len(payload) == 0 {
		header := make([]byte, 8)
		header[0] = packetType
		header[1] = tdsStatusEOM
		binary.BigEndian.PutUint16(header[2:4], 8)
		_, err := w.Write(header)
		return err
	}

	packetID := byte(1)
	remaining := payload
	for len(remaining) > 0 {
		take := len(remaining)
		if take > maxPayload {
			take = maxPayload
		}
		chunk := remaining[:take]
		remaining = remaining[take:]

		header := make([]byte, 8)
		header[0] = packetType
		if len(remaining) == 0 {
			header[1] = tdsStatusEOM
		}
		binary.BigEndian.PutUint16(header[2:4], uint16(len(chunk)+8))
		header[6] = packetID
		packetID++

		if _, err := w.Write(header); err != nil {
			return err
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

func buildPreloginMessage(encryption byte) []byte {
	version := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	mars := []byte{0x00}
	enc := []byte{encryption}

	tableLen := 5*3 + 1
	data := make([]byte, 0, len(version)+len(enc)+len(mars))
	buf := make([]byte, 0, tableLen+len(version)+len(enc)+len(mars))

	entry := func(token byte, d []byte) {
		off := uint16(tableLen + len(data))
		ln := uint16(len(d))
		buf = append(buf, token)
		tmp := make([]byte, 4)
		binary.BigEndian.PutUint16(tmp[0:2], off)
		binary.BigEndian.PutUint16(tmp[2:4], ln)
		buf = append(buf, tmp...)
		data = append(data, d...)
	}
	entry(preloginOptVersion, version)
	entry(preloginOptEncryption, enc)
	entry(preloginOptMARS, mars)
	buf = append(buf, preloginOptTerminator)
	buf = append(buf, data...)
	return buf
}

func parsePreloginEncryption(payload []byte) (byte, bool) {
	pos := 0
	for pos < len(payload) {
		tok := payload[pos]
		if tok == preloginOptTerminator {
			return 0, false
		}
		if pos+5 > len(payload) {
			return 0, false
		}
		offset := int(binary.BigEndian.Uint16(payload[pos+1 : pos+3]))
		length := int(binary.BigEndian.Uint16(payload[pos+3 : pos+5]))
		if offset < 0 || length <= 0 || offset+length > len(payload) {
			return 0, false
		}
		if tok == preloginOptEncryption {
			return payload[offset], true
		}
		pos += 5
	}
	return 0, false
}

func relayLoginResponse(upstream io.Reader, client io.Writer) (string, error) {
	var firstErr string
	for {
		msg, err := readTDSMessage(upstream)
		if err != nil {
			return firstErr, err
		}
		for _, pkt := range msg.Packets {
			if _, err := client.Write(pkt); err != nil {
				return firstErr, err
			}
		}

		if candidate := parseFirstErrorToken(msg.Payload); candidate != "" && firstErr == "" {
			firstErr = candidate
		}
		if hasLoginAckToken(msg.Payload) {
			return "", nil
		}
		if hasDoneToken(msg.Payload) && firstErr != "" {
			return firstErr, nil
		}
	}
}

func hasLoginAckToken(payload []byte) bool {
	return bytes.Contains(payload, []byte{0xad})
}

func hasDoneToken(payload []byte) bool {
	return bytes.Contains(payload, []byte{0xfd}) || bytes.Contains(payload, []byte{0xfe}) || bytes.Contains(payload, []byte{0xff})
}

func parseFirstErrorToken(payload []byte) string {
	idx := bytes.IndexByte(payload, 0xaa)
	if idx < 0 || idx+9 > len(payload) {
		return ""
	}
	msgLen := int(binary.LittleEndian.Uint16(payload[idx+7 : idx+9]))
	start := idx + 9
	end := start + msgLen*2
	if msgLen <= 0 || end > len(payload) {
		return ""
	}
	msg := decodeUTF16LE(payload[start:end])
	return strings.TrimSpace(msg)
}

func rewriteLogin7WithManagedCreds(payload []byte, username, password, database string) ([]byte, error) {
	if len(payload) < login7FixedLen {
		return nil, fmt.Errorf("login7 payload too short")
	}
	out := make([]byte, login7FixedLen)
	copy(out, payload[:login7FixedLen])

	// Force SQL auth mode (disable integrated security bit in OptionFlags2).
	out[25] &^= 0x80

	fields := parseLogin7Fields(payload)
	if fields == nil {
		return nil, fmt.Errorf("invalid login7 field offsets")
	}

	fields[1].raw = encodeUTF16LE(username)
	fields[1].chars = len([]rune(username))
	fields[2].raw = obfuscateTDSPassword(password)
	fields[2].chars = len([]rune(password))
	if strings.TrimSpace(database) != "" {
		fields[8].raw = encodeUTF16LE(database)
		fields[8].chars = len([]rune(database))
	}
	// Clear SSPI payload for SQL authentication path.
	fields[9].raw = nil
	fields[9].chars = 0

	varBuf := make([]byte, 0, len(payload))
	appendField := func(f *loginField) {
		off := login7FixedLen + len(varBuf)
		binary.LittleEndian.PutUint16(out[f.offsetPos:f.offsetPos+2], uint16(off))
		binary.LittleEndian.PutUint16(out[f.offsetPos+2:f.offsetPos+4], uint16(f.chars))
		varBuf = append(varBuf, f.raw...)
	}
	for i := range fields {
		appendField(&fields[i])
	}

	full := append(out, varBuf...)
	binary.LittleEndian.PutUint32(full[0:4], uint32(len(full)))
	return full, nil
}

type loginField struct {
	offsetPos int
	chars     int
	raw       []byte
	isSSPI    bool
}

func parseLogin7Fields(payload []byte) []loginField {
	if len(payload) < login7FixedLen {
		return nil
	}
	positions := []struct {
		pos    int
		isSSPI bool
	}{
		{36, false}, // HostName
		{40, false}, // UserName
		{44, false}, // Password
		{48, false}, // AppName
		{52, false}, // ServerName
		{56, false}, // Unused
		{60, false}, // CltIntName
		{64, false}, // Language
		{68, false}, // Database
		{78, true},  // SSPI (length in bytes)
		{82, false}, // AtchDBFile
		{86, false}, // ChangePassword
	}
	fields := make([]loginField, 0, len(positions))
	for _, p := range positions {
		off := int(binary.LittleEndian.Uint16(payload[p.pos : p.pos+2]))
		ln := int(binary.LittleEndian.Uint16(payload[p.pos+2 : p.pos+4]))
		byteLen := ln * 2
		if p.isSSPI {
			byteLen = ln
		}
		if off < 0 || off > len(payload) || off+byteLen > len(payload) {
			return nil
		}
		raw := make([]byte, 0)
		if byteLen > 0 {
			raw = append(raw, payload[off:off+byteLen]...)
		}
		fields = append(fields, loginField{offsetPos: p.pos, chars: ln, raw: raw, isSSPI: p.isSSPI})
	}
	return fields
}

func parseLogin7Database(payload []byte) string {
	if len(payload) < login7FixedLen {
		return ""
	}
	off := int(binary.LittleEndian.Uint16(payload[68:70]))
	ln := int(binary.LittleEndian.Uint16(payload[70:72]))
	if ln <= 0 {
		return ""
	}
	byteLen := ln * 2
	if off < 0 || off+byteLen > len(payload) {
		return ""
	}
	return strings.TrimSpace(decodeUTF16LE(payload[off : off+byteLen]))
}

func obfuscateTDSPassword(password string) []byte {
	raw := encodeUTF16LE(password)
	out := make([]byte, len(raw))
	for i, b := range raw {
		rot := (b << 4) | (b >> 4)
		out[i] = rot ^ 0xA5
	}
	return out
}

func encodeUTF16LE(v string) []byte {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	runes := utf16.Encode([]rune(v))
	buf := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		tmp := make([]byte, 2)
		binary.LittleEndian.PutUint16(tmp, r)
		buf = append(buf, tmp...)
	}
	return buf
}

func decodeUTF16LE(raw []byte) string {
	if len(raw) < 2 {
		return ""
	}
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u16 = append(u16, binary.LittleEndian.Uint16(raw[i:i+2]))
	}
	runes := utf16.Decode(u16)
	return strings.TrimSpace(string(runes))
}

func extractSQLBatchQuery(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	queryData := payload
	if len(payload) >= 4 {
		headersLen := int(binary.LittleEndian.Uint32(payload[:4]))
		if headersLen >= 4 && headersLen <= len(payload) {
			queryData = payload[headersLen:]
		}
	}
	q := strings.TrimSpace(decodeUTF16LE(queryData))
	if !utf8.ValidString(q) {
		return ""
	}
	return q
}

func parseRPCQuery(payload []byte) (procName string, sqlText string) {
	if len(payload) < 2 {
		return "", ""
	}
	pos := 0
	nameLen := int(binary.LittleEndian.Uint16(payload[pos : pos+2]))
	pos += 2

	if nameLen == 0xffff {
		if len(payload) < pos+4 {
			return "", ""
		}
		procID := binary.LittleEndian.Uint16(payload[pos : pos+2])
		pos += 2
		_ = binary.LittleEndian.Uint16(payload[pos : pos+2]) // option flags
		pos += 2
		procName = rpcProcIDName(procID)
	} else {
		nameBytes := nameLen * 2
		if len(payload) < pos+nameBytes+2 {
			return "", ""
		}
		procName = strings.TrimSpace(decodeUTF16LE(payload[pos : pos+nameBytes]))
		pos += nameBytes
		_ = binary.LittleEndian.Uint16(payload[pos : pos+2]) // option flags
		pos += 2
	}

	if pos >= len(payload) {
		return procName, ""
	}
	return procName, extractLikelySQLFromRPCPayload(payload[pos:])
}

func rpcProcIDName(id uint16) string {
	switch id {
	case 10:
		return "sp_executesql"
	default:
		return fmt.Sprintf("proc_id_%d", id)
	}
}

func extractLikelySQLFromRPCPayload(payload []byte) string {
	candidates := extractASCIIUTF16Runs(payload)
	if len(candidates) == 0 {
		return ""
	}
	best := ""
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if !containsSQLKeyword(trimmed) {
			continue
		}
		if len(trimmed) > len(best) {
			best = trimmed
		}
	}
	return best
}

func extractASCIIUTF16Runs(payload []byte) []string {
	runs := make([]string, 0, 4)
	var b strings.Builder
	flush := func() {
		if b.Len() >= 12 {
			runs = append(runs, b.String())
		}
		b.Reset()
	}
	for i := 0; i+1 < len(payload); i += 2 {
		c := binary.LittleEndian.Uint16(payload[i : i+2])
		if (c >= 32 && c <= 126) || c == '\n' || c == '\r' || c == '\t' {
			b.WriteRune(rune(c))
			continue
		}
		flush()
	}
	flush()
	return runs
}

func containsSQLKeyword(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	if lower == "" {
		return false
	}
	keywords := []string{"select ", "insert ", "update ", "delete ", "merge ", "exec ", "with ", "create ", "alter ", "drop "}
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
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
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "disable":
		return sslModeConfig{}
	case "allow", "prefer", "preferred", "", "optional":
		return sslModeConfig{AttemptTLS: true}
	case "require", "required", "true", "encrypt":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true}
	case "verify-ca", "verify-full", "verify_ca", "verify_identity":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true, VerifyCert: true}
	default:
		return sslModeConfig{AttemptTLS: true}
	}
}

func normalizeEngine(engine string) string {
	normalized := strings.ToLower(strings.TrimSpace(engine))
	switch normalized {
	case "sqlserver", "mssql", "sql_server":
		return "mssql"
	case "":
		return "mssql"
	default:
		return normalized
	}
}

// buildProxyTLSConfig is reserved for a future client<->proxy TLS mode where
// TDS-wrapped TLS handshakes are supported end-to-end.
func buildProxyTLSConfig(publicHost string) (*tls.Config, error) {
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject: pkix.Name{
			CommonName: "accessd-mssql-proxy",
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
