package redisproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/connutil"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/sessions"
)

type Config struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	LogQueue       int
	ArgMaxLen      int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type SessionRegistration struct {
	SessionID string
	UserID    string
	AssetID   string
	AssetName string

	UpstreamHost string
	UpstreamPort int

	UseTLS                bool
	InsecureSkipVerifyTLS bool
	Database              int

	ClientAuthToken string
	RequestID       string
}

type commandLogEvent struct {
	SessionID  string
	UserID     string
	AssetID    string
	RequestID  string
	Command    string
	Args       []string
	Dangerous  bool
	EventTime  time.Time
	RemoteAddr string
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

	logCh chan commandLogEvent
	wg    sync.WaitGroup
}

func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.BindHost) == "" {
		return nil, fmt.Errorf("redis proxy bind host is required")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return nil, fmt.Errorf("redis proxy public host is required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.LogQueue <= 0 {
		cfg.LogQueue = 1024
	}
	if cfg.ArgMaxLen <= 0 {
		cfg.ArgMaxLen = 128
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
		logger:      logger.With("component", "redis_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		listeners:   map[string]net.Listener{},
		active:      map[net.Conn]struct{}{},
		logCh:       make(chan commandLogEvent, cfg.LogQueue),
	}
	s.wg.Add(1)
	go s.logWorker()
	return s, nil
}

func (s *Service) RegisterSession(reg SessionRegistration) (string, int, error) {
	reg.SessionID = strings.TrimSpace(reg.SessionID)
	reg.UserID = strings.TrimSpace(reg.UserID)
	reg.AssetID = strings.TrimSpace(reg.AssetID)
	reg.AssetName = strings.TrimSpace(reg.AssetName)
	reg.UpstreamHost = strings.TrimSpace(reg.UpstreamHost)
	reg.ClientAuthToken = strings.TrimSpace(reg.ClientAuthToken)
	reg.RequestID = strings.TrimSpace(reg.RequestID)

	if reg.SessionID == "" || reg.UserID == "" || reg.AssetID == "" {
		return "", 0, fmt.Errorf("session_id, user_id, and asset_id are required")
	}
	if reg.UpstreamHost == "" || reg.UpstreamPort <= 0 || reg.UpstreamPort > 65535 {
		return "", 0, fmt.Errorf("upstream host and port are required")
	}
	if reg.Database < 0 {
		reg.Database = 0
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("redis proxy is shutting down")
	}
	if existing, ok := s.listeners[reg.SessionID]; ok {
		_ = existing.Close()
		delete(s.listeners, reg.SessionID)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindHost, "0"))
	if err != nil {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("listen redis proxy: %w", err)
	}
	s.listeners[reg.SessionID] = ln
	s.mu.Unlock()

	port := ln.Addr().(*net.TCPAddr).Port
	s.logger.Info("redis proxy session listener registered", "session_id", reg.SessionID, "request_id", reg.RequestID, "addr", ln.Addr().String())

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
			s.logger.Warn("redis proxy max session duration reached; closing listener", "session_id", reg.SessionID, "request_id", reg.RequestID)
			_ = ln.Close()
		})
		defer timer.Stop()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.logger.Warn("redis proxy accept failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
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
			s.logger.Info("redis proxy client connected", "session_id", reg.SessionID, "request_id", reg.RequestID, "remote_addr", c.RemoteAddr().String())
			if err := s.handleSessionConn(reg, c); err != nil {
				s.logger.Warn("redis proxy session failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
			} else {
				s.logger.Info("redis proxy session ended", "session_id", reg.SessionID, "request_id", reg.RequestID)
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
		Action:    "redis",
		Protocol:  sessions.ProtocolRedis,
		AssetType: assets.TypeRedis,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	ctx := context.Background()
	if err := s.sessionsSvc.MarkProxyConnected(ctx, lctx, client.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record redis proxy connected", "session_id", reg.SessionID, "error", err)
	}

	if err := s.runProxyFlow(reg, client); err != nil {
		_ = s.sessionsSvc.MarkFailed(ctx, lctx, "redis_proxy_failed")
		return err
	}

	if err := s.sessionsSvc.MarkEnded(ctx, lctx, "redis_client_disconnected"); err != nil {
		s.logger.Warn("failed to mark redis session ended", "session_id", reg.SessionID, "error", err)
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
		Action:    "redis",
		Protocol:  sessions.ProtocolRedis,
		AssetType: assets.TypeRedis,
		Host:      reg.UpstreamHost,
		Port:      reg.UpstreamPort,
	}

	upstream, err := s.connectUpstream(reg)
	if err != nil {
		s.logger.Warn("redis upstream connect failed", "session_id", reg.SessionID, "asset_id", reg.AssetID, "error", err)
		return fmt.Errorf("connect upstream: %w", err)
	}
	defer upstream.Close()
	s.logger.Info("redis upstream connected", "session_id", reg.SessionID, "asset_id", reg.AssetID)

	cred, err := s.credSvc.ResolveForAsset(context.Background(), reg.AssetID, credentials.TypePassword)
	if err != nil {
		return fmt.Errorf("resolve redis credential: %w", err)
	}
	lctx.UpstreamUsername = strings.TrimSpace(cred.Username)
	if err := s.sessionsSvc.RecordCredentialUsage(context.Background(), lctx, credentials.TypePassword, "proxy_upstream_auth", reg.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}

	if err := authenticateAndSelectUpstream(upstream, cred.Username, cred.Secret, reg.Database); err != nil {
		s.logger.Warn("redis upstream auth/select failed", "session_id", reg.SessionID, "asset_id", reg.AssetID, "error", err)
		return fmt.Errorf("upstream auth/select: %w", err)
	}

	if err := s.sessionsSvc.MarkUpstreamConnected(context.Background(), sessions.LaunchContext{
		SessionID:        reg.SessionID,
		UserID:           reg.UserID,
		AssetID:          reg.AssetID,
		AssetName:        reg.AssetName,
		RequestID:        reg.RequestID,
		Action:           "redis",
		Protocol:         sessions.ProtocolRedis,
		AssetType:        assets.TypeRedis,
		Host:             reg.UpstreamHost,
		Port:             reg.UpstreamPort,
		UpstreamUsername: strings.TrimSpace(cred.Username),
	}); err != nil {
		s.logger.Warn("failed to record redis upstream connected", "session_id", reg.SessionID, "error", err)
	}

	clientReader := bufio.NewReader(client)
	upstreamReader := bufio.NewReader(upstream)
	clientAuthenticated := strings.TrimSpace(reg.ClientAuthToken) == ""

	for {
		rawReq, cmd, args, err := readRESPCommand(clientReader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			s.logger.Warn("redis command parse failed", "session_id", reg.SessionID, "error", err)
			return fmt.Errorf("read client command: %w", err)
		}

		if cmd == "" {
			if _, wErr := upstream.Write(rawReq); wErr != nil {
				return wErr
			}
			rawResp, rErr := readRESPFrameRaw(upstreamReader)
			if rErr != nil {
				return rErr
			}
			if _, wErr := client.Write(rawResp); wErr != nil {
				return wErr
			}
			continue
		}

		if !clientAuthenticated {
			if cmd != "AUTH" {
				s.logger.Warn("redis client attempted command before auth", "session_id", reg.SessionID, "command", cmd)
				if _, wErr := client.Write([]byte("-NOAUTH Authentication required by PAM proxy\r\n")); wErr != nil {
					return wErr
				}
				continue
			}
			token := extractClientAuthToken(args)
			if token == "" {
				s.logger.Warn("redis proxy auth payload missing token", "session_id", reg.SessionID)
				if _, wErr := client.Write([]byte("-ERR invalid AUTH payload\r\n")); wErr != nil {
					return wErr
				}
				continue
			}
			if !s.validateClientToken(reg, token) {
				s.logger.Warn("redis proxy auth failed", "session_id", reg.SessionID)
				if _, wErr := client.Write([]byte("-ERR invalid PAM launch token\r\n")); wErr != nil {
					return wErr
				}
				continue
			}
			clientAuthenticated = true
			if _, wErr := client.Write([]byte("+OK\r\n")); wErr != nil {
				return wErr
			}
			continue
		}

		if cmd == "AUTH" {
			if _, wErr := client.Write([]byte("+OK\r\n")); wErr != nil {
				return wErr
			}
			continue
		}

		s.enqueueCommandLog(commandLogEvent{
			SessionID:  reg.SessionID,
			UserID:     reg.UserID,
			AssetID:    reg.AssetID,
			RequestID:  reg.RequestID,
			Command:    cmd,
			Args:       args,
			Dangerous:  isDangerousCommand(cmd),
			EventTime:  time.Now().UTC(),
			RemoteAddr: client.RemoteAddr().String(),
		})

		if _, wErr := upstream.Write(rawReq); wErr != nil {
			return wErr
		}
		rawResp, rErr := readRESPFrameRaw(upstreamReader)
		if rErr != nil {
			return rErr
		}
		if _, wErr := client.Write(rawResp); wErr != nil {
			return wErr
		}
	}
}

func (s *Service) validateClientToken(reg SessionRegistration, token string) bool {
	if strings.TrimSpace(reg.ClientAuthToken) == "" {
		return false
	}
	if strings.TrimSpace(token) != strings.TrimSpace(reg.ClientAuthToken) {
		return false
	}
	lctx, err := s.sessionsSvc.ResolveLaunchToken(context.Background(), token)
	if err != nil {
		return false
	}
	return launchContextMatchesRedisSession(lctx, reg)
}

func launchContextMatchesRedisSession(lctx sessions.LaunchContext, reg SessionRegistration) bool {
	return lctx.SessionID == reg.SessionID &&
		lctx.UserID == reg.UserID &&
		lctx.AssetID == reg.AssetID &&
		lctx.Action == "redis" &&
		lctx.Protocol == sessions.ProtocolRedis &&
		lctx.AssetType == assets.TypeRedis
}

func (s *Service) connectUpstream(reg SessionRegistration) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)), s.cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	if !reg.UseTLS {
		return connutil.WrapIdleTimeout(raw, s.cfg.IdleTimeout), nil
	}

	tlsCfg := &tls.Config{ // #nosec G402 - runtime controlled via config
		MinVersion: tls.VersionTLS12,
		ServerName: reg.UpstreamHost,
	}
	if reg.InsecureSkipVerifyTLS {
		tlsCfg.InsecureSkipVerify = true
	}
	tlsConn := tls.Client(raw, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("redis upstream tls handshake: %w", err)
	}
	return connutil.WrapIdleTimeout(tlsConn, s.cfg.IdleTimeout), nil
}

func authenticateAndSelectUpstream(conn net.Conn, username, password string, database int) error {
	reader := bufio.NewReader(conn)
	if strings.TrimSpace(password) != "" {
		authCmd := buildRESPCommand(authCommandArgs(username, password))
		if _, err := conn.Write(authCmd); err != nil {
			return err
		}
		authResp, err := readRESPFrameRaw(reader)
		if err != nil {
			return err
		}
		if isRESPError(authResp) {
			return fmt.Errorf("upstream auth failed: %s", firstRESPLineText(authResp))
		}
	}
	if database > 0 {
		selectCmd := buildRESPCommand([]string{"SELECT", strconv.Itoa(database)})
		if _, err := conn.Write(selectCmd); err != nil {
			return err
		}
		selectResp, err := readRESPFrameRaw(reader)
		if err != nil {
			return err
		}
		if isRESPError(selectResp) {
			return fmt.Errorf("upstream select failed: %s", firstRESPLineText(selectResp))
		}
	}
	return nil
}

func authCommandArgs(username, password string) []string {
	if strings.TrimSpace(username) != "" {
		return []string{"AUTH", username, password}
	}
	return []string{"AUTH", password}
}

func buildRESPCommand(args []string) []byte {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

func readRESPCommand(r *bufio.Reader) ([]byte, string, []string, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, "", nil, err
	}
	if prefix != '*' {
		raw, frameErr := readRESPFrameFromPrefix(r, prefix)
		return raw, "", nil, frameErr
	}

	countLine, err := readLineCRLF(r)
	if err != nil {
		return nil, "", nil, err
	}
	countRaw := bytes.TrimSuffix(countLine, []byte("\r\n"))
	count, err := strconv.Atoi(string(countRaw))
	if err != nil || count <= 0 {
		raw := append([]byte{'*'}, countLine...)
		return raw, "", nil, nil
	}

	raw := make([]byte, 0, 64)
	raw = append(raw, '*')
	raw = append(raw, countLine...)
	items := make([]string, 0, count)

	for i := 0; i < count; i++ {
		p, pErr := r.ReadByte()
		if pErr != nil {
			return nil, "", nil, pErr
		}
		itemRaw, value, itemErr := readRESPValueForCommand(r, p)
		if itemErr != nil {
			return nil, "", nil, itemErr
		}
		raw = append(raw, itemRaw...)
		items = append(items, value)
	}

	cmd := strings.ToUpper(strings.TrimSpace(items[0]))
	args := make([]string, 0, len(items)-1)
	for _, v := range items[1:] {
		args = append(args, strings.TrimSpace(v))
	}
	return raw, cmd, args, nil
}

func readRESPValueForCommand(r *bufio.Reader, prefix byte) ([]byte, string, error) {
	switch prefix {
	case '$':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, "", err
		}
		raw := make([]byte, 0, 16)
		raw = append(raw, '$')
		raw = append(raw, line...)
		lenRaw := bytes.TrimSuffix(line, []byte("\r\n"))
		n, err := strconv.Atoi(string(lenRaw))
		if err != nil {
			return raw, "", nil
		}
		if n < 0 {
			return raw, "", nil
		}
		payload := make([]byte, n+2)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, "", err
		}
		raw = append(raw, payload...)
		return raw, string(payload[:n]), nil
	case '+', ':':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, "", err
		}
		raw := make([]byte, 0, 1+len(line))
		raw = append(raw, prefix)
		raw = append(raw, line...)
		value := string(bytes.TrimSuffix(line, []byte("\r\n")))
		return raw, value, nil
	default:
		raw, err := readRESPFrameFromPrefix(r, prefix)
		if err != nil {
			return nil, "", err
		}
		return raw, "", nil
	}
}

func readRESPFrameRaw(r *bufio.Reader) ([]byte, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	return readRESPFrameFromPrefix(r, prefix)
}

func readRESPFrameFromPrefix(r *bufio.Reader, prefix byte) ([]byte, error) {
	raw := []byte{prefix}
	switch prefix {
	case '+', '-', ':', ',', '#', '_', '(':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		return raw, nil
	case '$', '!', '=':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		nRaw := bytes.TrimSuffix(line, []byte("\r\n"))
		n, err := strconv.Atoi(string(nRaw))
		if err != nil || n < 0 {
			return raw, nil
		}
		payload := make([]byte, n+2)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
		raw = append(raw, payload...)
		return raw, nil
	case '*', '~', '>':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		nRaw := bytes.TrimSuffix(line, []byte("\r\n"))
		n, err := strconv.Atoi(string(nRaw))
		if err != nil || n < 0 {
			return raw, nil
		}
		for i := 0; i < n; i++ {
			child, err := readRESPFrameRaw(r)
			if err != nil {
				return nil, err
			}
			raw = append(raw, child...)
		}
		return raw, nil
	case '%':
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		nRaw := bytes.TrimSuffix(line, []byte("\r\n"))
		n, err := strconv.Atoi(string(nRaw))
		if err != nil || n < 0 {
			return raw, nil
		}
		for i := 0; i < n*2; i++ {
			child, err := readRESPFrameRaw(r)
			if err != nil {
				return nil, err
			}
			raw = append(raw, child...)
		}
		return raw, nil
	default:
		line, err := readLineCRLF(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		return raw, nil
	}
}

func readLineCRLF(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return line, nil
}

func isRESPError(raw []byte) bool {
	return len(raw) > 0 && raw[0] == '-'
}

func firstRESPLineText(raw []byte) string {
	idx := bytes.Index(raw, []byte("\r\n"))
	if idx < 0 {
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(string(raw[:idx]))
}

func extractClientAuthToken(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if len(args) == 1 {
		return strings.TrimSpace(args[0])
	}
	return strings.TrimSpace(args[len(args)-1])
}

func isDangerousCommand(cmd string) bool {
	switch strings.ToUpper(strings.TrimSpace(cmd)) {
	case "FLUSHDB", "FLUSHALL", "DEL", "CONFIG", "KEYS", "SET", "EXPIRE", "EVAL", "EVALSHA":
		return true
	default:
		return false
	}
}

func summarizeArgs(cmd string, args []string, maxLen int) []string {
	up := strings.ToUpper(strings.TrimSpace(cmd))
	out := make([]string, 0, len(args))
	for i, arg := range args {
		clean := truncateArg(arg, maxLen)
		switch up {
		case "AUTH":
			clean = "<redacted>"
		case "SET":
			if i == 1 {
				clean = "<redacted_value>"
			}
		case "MSET", "HMSET":
			if i%2 == 1 {
				clean = "<redacted_value>"
			}
		case "CONFIG":
			if len(args) >= 3 && strings.EqualFold(args[0], "set") && i >= 2 {
				clean = "<redacted_value>"
			}
		case "EVAL":
			if i == 0 {
				clean = "<redacted_script>"
			}
		case "ACL":
			if len(args) >= 2 && strings.EqualFold(args[0], "setuser") && i >= 2 {
				clean = "<redacted>"
			}
		}
		out = append(out, clean)
		if len(out) >= 10 {
			out = append(out, "<truncated>")
			break
		}
	}
	return out
}

func truncateArg(v string, maxLen int) string {
	trimmed := strings.TrimSpace(v)
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "..."
}

func (s *Service) enqueueCommandLog(evt commandLogEvent) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case s.logCh <- evt:
	default:
		s.logger.Warn("dropping redis_command event due to full queue", "session_id", evt.SessionID)
	}
}

func (s *Service) logWorker() {
	defer s.wg.Done()
	for evt := range s.logCh {
		actor := evt.UserID
		payload := map[string]any{
			"session_id":   evt.SessionID,
			"user_id":      evt.UserID,
			"asset_id":     evt.AssetID,
			"command":      evt.Command,
			"args_summary": summarizeArgs(evt.Command, evt.Args, s.cfg.ArgMaxLen),
			"dangerous":    evt.Dangerous,
			"event_time":   evt.EventTime.Format(time.RFC3339Nano),
			"remote_addr":  evt.RemoteAddr,
			"request_id":   strings.TrimSpace(evt.RequestID),
		}
		if err := s.sessionsSvc.WriteEvent(context.Background(), evt.SessionID, sessions.EventRedisCommand, &actor, payload); err != nil {
			s.logger.Warn("failed to write redis_command event", "session_id", evt.SessionID, "error", err)
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
	close(s.logCh)
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
