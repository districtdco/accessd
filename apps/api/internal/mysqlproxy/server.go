package mysqlproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
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

	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/connutil"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/sessions"
)

// MySQL command bytes.
const (
	comQuit             byte = 0x01
	comInitDB           byte = 0x02
	comQuery            byte = 0x03
	comFieldList        byte = 0x04
	comStmtPrepare      byte = 0x16
	comStmtExecute      byte = 0x17
	comStmtSendLongData byte = 0x18
	comStmtClose        byte = 0x19
)

// MySQL packet markers.
const (
	iOK       byte = 0x00
	iERR      byte = 0xff
	iEOF      byte = 0xfe
	iLocalInf byte = 0xfb
)

// MySQL capability flags.
const (
	capLongPassword     uint32 = 1 << 0
	capFoundRows        uint32 = 1 << 1
	capLongFlag         uint32 = 1 << 2
	capConnectWithDB    uint32 = 1 << 3
	capProtocol41       uint32 = 1 << 9
	capSSL              uint32 = 1 << 11
	capTransactions     uint32 = 1 << 13
	capSecureConnection uint32 = 1 << 15
	capPluginAuth       uint32 = 1 << 19

	// SERVER_MORE_RESULTS_EXISTS in status flags.
	statusMoreResults uint16 = 0x0008
)

// Auth plugin names.
const (
	authNativePassword = "mysql_native_password"
	authCachingSha2    = "caching_sha2_password"
)

// Max MySQL packet payload (2^24 - 1).
const maxPacketSize = 1<<24 - 1

// Config for the MySQL proxy.
type Config struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	QueryLogQueue  int
	QueryMaxBytes  int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

// SessionRegistration binds a MySQL proxy listener to a PAM session.
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
	ProtocolType string // "simple" or "prepared"
	Prepared     bool
}

// preparedStmtCache tracks MySQL prepared statements by server-assigned ID.
type preparedStmtCache struct {
	mu    sync.Mutex
	stmts map[uint32]string
}

func newPreparedStmtCache() *preparedStmtCache {
	return &preparedStmtCache{stmts: make(map[uint32]string)}
}

func (c *preparedStmtCache) Store(id uint32, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stmts[id] = query
}

func (c *preparedStmtCache) Lookup(id uint32) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q, ok := c.stmts[id]
	return q, ok
}

func (c *preparedStmtCache) Delete(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.stmts, id)
}

// Service provides a session-scoped MySQL proxy.
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

// New creates a new MySQL proxy service. It generates a self-signed TLS certificate
// for client connections and starts an async query log worker.
func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.BindHost) == "" {
		return nil, fmt.Errorf("mysql proxy bind host is required")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return nil, fmt.Errorf("mysql proxy public host is required")
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
		logger:      logger.With("component", "mysql_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		listeners:   map[string]net.Listener{},
		active:      map[net.Conn]struct{}{},
		queryLogCh:  make(chan queryLogEvent, cfg.QueryLogQueue),
	}
	tlsCfg, err := buildProxyTLSConfig(cfg.PublicHost)
	if err != nil {
		return nil, fmt.Errorf("build mysql proxy tls config: %w", err)
	}
	s.tlsConfig = tlsCfg

	s.wg.Add(1)
	go s.queryLogWorker()
	return s, nil
}

// RegisterSession creates a TCP listener for the given session. It
// returns the public host and port the client should connect to.
func (s *Service) RegisterSession(reg SessionRegistration) (string, int, error) {
	reg.SessionID = strings.TrimSpace(reg.SessionID)
	reg.UserID = strings.TrimSpace(reg.UserID)
	reg.AssetID = strings.TrimSpace(reg.AssetID)
	reg.AssetName = strings.TrimSpace(reg.AssetName)
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
		reg.Engine = "mysql"
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("mysql proxy is shutting down")
	}
	if existing, ok := s.listeners[reg.SessionID]; ok {
		_ = existing.Close()
		delete(s.listeners, reg.SessionID)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindHost, "0"))
	if err != nil {
		s.mu.Unlock()
		return "", 0, fmt.Errorf("listen mysql proxy: %w", err)
	}
	s.listeners[reg.SessionID] = ln
	s.mu.Unlock()

	port := ln.Addr().(*net.TCPAddr).Port
	s.logger.Info("mysql proxy session listener registered", "session_id", reg.SessionID, "request_id", reg.RequestID, "addr", ln.Addr().String())

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
			s.logger.Warn("mysql proxy max session duration reached; closing listener", "session_id", reg.SessionID, "request_id", reg.RequestID)
			_ = ln.Close()
		})
		defer timer.Stop()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.logger.Warn("mysql proxy accept failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
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
				s.logger.Warn("mysql proxy session failed", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
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
	if err := s.sessionsSvc.MarkProxyConnected(ctx, lctx, client.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record mysql proxy connected", "session_id", reg.SessionID, "error", err)
	}

	if err := s.runProxyFlow(reg, client); err != nil {
		_ = s.sessionsSvc.MarkFailed(ctx, lctx, "db_proxy_failed")
		return err
	}

	if err := s.sessionsSvc.MarkEnded(ctx, lctx, "db_client_disconnected"); err != nil {
		s.logger.Warn("failed to mark mysql session ended", "session_id", reg.SessionID, "error", err)
	}
	return nil
}

// runProxyFlow connects to upstream, authenticates, negotiates with the client,
// then enters the command-forwarding loop.
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

	upstream, err := s.connectAndAuthUpstream(reg)
	if err != nil {
		return fmt.Errorf("upstream: %w", err)
	}
	defer upstream.Close()

	s.logger.Info("mysql upstream connected", "session_id", reg.SessionID,
		"upstream", net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)))

	clientConn, err := s.negotiateClient(client)
	if err != nil {
		return fmt.Errorf("client negotiate: %w", err)
	}

	s.logger.Info("mysql client authenticated", "session_id", reg.SessionID)
	if err := s.sessionsSvc.RecordCredentialUsage(context.Background(), lctx, credentials.TypeDBPassword, "proxy_upstream_auth", reg.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", reg.SessionID, "request_id", reg.RequestID, "error", err)
	}
	return s.forwardCommands(reg, clientConn, upstream)
}

// ---------------------------------------------------------------------------
// Upstream connection and authentication
// ---------------------------------------------------------------------------

// connectAndAuthUpstream dials the real MySQL server and completes the full
// handshake/auth exchange using PAM-managed credentials.
func (s *Service) connectAndAuthUpstream(reg SessionRegistration) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp",
		net.JoinHostPort(reg.UpstreamHost, strconv.Itoa(reg.UpstreamPort)),
		s.cfg.ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	raw = connutil.WrapIdleTimeout(raw, s.cfg.IdleTimeout)

	payload, _, err := readPacket(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("read handshake: %w", err)
	}
	hs, err := parseHandshakeV10(payload)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("parse handshake: %w", err)
	}

	cred, err := s.credSvc.ResolveForAsset(context.Background(), reg.AssetID, credentials.TypeDBPassword)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("resolve credential: %w", err)
	}

	sslCfg := classifySSLMode(reg.SSLMode)
	conn := net.Conn(raw)
	var seq uint8 = 1
	useSSL := false

	if sslCfg.AttemptTLS && hs.Capabilities&capSSL != 0 {
		sslReq := buildSSLRequest()
		if err := writePacket(conn, sslReq, seq); err != nil {
			conn.Close()
			return nil, fmt.Errorf("send ssl request: %w", err)
		}
		seq++

		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: reg.UpstreamHost,
		}
		if !sslCfg.VerifyCert {
			tlsCfg.InsecureSkipVerify = true
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("upstream tls handshake: %w", err)
		}
		conn = connutil.WrapIdleTimeout(tlsConn, s.cfg.IdleTimeout)
		useSSL = true
	} else if sslCfg.RequireTLS {
		conn.Close()
		return nil, fmt.Errorf("upstream does not support ssl (ssl_mode=%s)", reg.SSLMode)
	}

	authPlugin := hs.AuthPlugin
	if authPlugin == "" {
		authPlugin = authNativePassword
	}
	authResp := computeAuthResponse(authPlugin, cred.Secret, hs.AuthData)

	hsResp := buildHandshakeResponse(hs.Capabilities, reg.Username, reg.Database, authPlugin, authResp, useSSL)
	if err := writePacket(conn, hsResp, seq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send auth: %w", err)
	}
	seq++

	if err := handleAuthResult(conn, &seq, authPlugin, cred.Secret, hs.AuthData, s.logger); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// handleAuthResult reads auth responses from the upstream MySQL server, handling
// auth-switch, caching_sha2 fast/full paths, etc.
func handleAuthResult(conn net.Conn, seq *uint8, authPlugin, password string, authData []byte, logger *slog.Logger) error {
	for {
		payload, respSeq, err := readPacket(conn)
		if err != nil {
			return fmt.Errorf("read auth response: %w", err)
		}
		*seq = respSeq + 1

		if len(payload) == 0 {
			return fmt.Errorf("empty auth response")
		}

		switch payload[0] {
		case iOK:
			return nil
		case iERR:
			return fmt.Errorf("upstream auth failed: %s", parseErrPacket(payload))
		case iEOF:
			// Auth switch request.
			newPlugin, newData := parseAuthSwitch(payload)
			logger.Debug("mysql auth switch", "from", authPlugin, "to", newPlugin)
			authPlugin = newPlugin
			authData = newData
			authResp := computeAuthResponse(newPlugin, password, newData)
			if err := writePacket(conn, authResp, *seq); err != nil {
				return fmt.Errorf("send auth switch response: %w", err)
			}
			*seq++
		case 0x01:
			// Extra auth data (caching_sha2_password).
			if authPlugin == authCachingSha2 && len(payload) >= 2 {
				switch payload[1] {
				case 0x03:
					// Fast auth success — wait for the final OK.
					continue
				case 0x04:
					// Full auth required — send cleartext over TLS.
					if _, ok := conn.(*tls.Conn); !ok {
						return fmt.Errorf("caching_sha2_password full auth requires TLS to upstream; set ssl_mode=require on the asset")
					}
					cleartext := append([]byte(password), 0)
					if err := writePacket(conn, cleartext, *seq); err != nil {
						return fmt.Errorf("send cleartext password: %w", err)
					}
					*seq++
					continue
				}
			}
			return fmt.Errorf("unexpected auth extra data: %x", payload)
		default:
			return fmt.Errorf("unexpected auth response: 0x%02x", payload[0])
		}
	}
}

// ---------------------------------------------------------------------------
// Client negotiation
// ---------------------------------------------------------------------------

// negotiateClient sends a synthetic MySQL handshake to the DBeaver client,
// optionally upgrades to TLS, reads the client auth (ignoring the password),
// and responds with OK.
func (s *Service) negotiateClient(client net.Conn) (net.Conn, error) {
	salt := make([]byte, 20)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	hsPayload := buildServerHandshake(salt, s.tlsConfig != nil)
	if err := writePacket(client, hsPayload, 0); err != nil {
		return nil, fmt.Errorf("send handshake: %w", err)
	}

	payload, seq, err := readPacket(client)
	if err != nil {
		return nil, fmt.Errorf("read client auth: %w", err)
	}
	if len(payload) < 4 {
		return nil, fmt.Errorf("client auth too short: %d bytes", len(payload))
	}

	capFlags := binary.LittleEndian.Uint32(payload[:4])
	conn := net.Conn(client)

	// A 32-byte packet with CLIENT_SSL is an SSL request.
	if capFlags&capSSL != 0 && len(payload) == 32 {
		if s.tlsConfig == nil {
			return nil, fmt.Errorf("client requested SSL but proxy has no TLS config")
		}
		tlsConn := tls.Server(client, s.tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("client tls handshake: %w", err)
		}
		conn = tlsConn
		// Re-read the full auth response over TLS.
		payload, seq, err = readPacket(conn)
		if err != nil {
			return nil, fmt.Errorf("read client auth after tls: %w", err)
		}
	}

	// Proxy handles upstream auth; always accept the client.
	okPayload := buildOKPacket(0, 0, 0x0002, 0)
	if err := writePacket(conn, okPayload, seq+1); err != nil {
		return nil, fmt.Errorf("send ok: %w", err)
	}
	return conn, nil
}

// ---------------------------------------------------------------------------
// Command forwarding
// ---------------------------------------------------------------------------

// forwardCommands is the main proxy loop. It reads one command at a time from
// the client, forwards it to the upstream server, then relays the full response
// back. This synchronous request-response model matches the MySQL protocol and
// avoids concurrency between the two directions.
func (s *Service) forwardCommands(reg SessionRegistration, client io.ReadWriter, upstream io.ReadWriter) error {
	cache := newPreparedStmtCache()

	for {
		payload, seq, err := readPacket(client)
		if err != nil {
			return err
		}
		if len(payload) == 0 {
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			continue
		}

		cmd := payload[0]

		switch cmd {
		case comQuit:
			_ = writePacket(upstream, payload, seq)
			return nil

		case comQuery:
			query := strings.TrimRight(string(payload[1:]), "\x00")
			if q := strings.TrimSpace(query); q != "" {
				s.enqueueQueryLog(queryLogEvent{
					SessionID:    reg.SessionID,
					UserID:       reg.UserID,
					AssetID:      reg.AssetID,
					RequestID:    reg.RequestID,
					Engine:       reg.Engine,
					Query:        truncate(q, s.cfg.QueryMaxBytes),
					EventTime:    time.Now().UTC(),
					ProtocolType: "simple",
				})
			}
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			if err := relayResponse(upstream, client); err != nil {
				return err
			}

		case comStmtPrepare:
			query := strings.TrimSpace(strings.TrimRight(string(payload[1:]), "\x00"))
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			if err := s.relayPrepareResponse(reg, upstream, client, cache, query); err != nil {
				return err
			}

		case comStmtExecute:
			if len(payload) >= 5 {
				stmtID := binary.LittleEndian.Uint32(payload[1:5])
				if q, ok := cache.Lookup(stmtID); ok {
					s.enqueueQueryLog(queryLogEvent{
						SessionID:    reg.SessionID,
						UserID:       reg.UserID,
						AssetID:      reg.AssetID,
						RequestID:    reg.RequestID,
						Engine:       reg.Engine,
						Query:        truncate(q, s.cfg.QueryMaxBytes),
						EventTime:    time.Now().UTC(),
						ProtocolType: "prepared",
						Prepared:     true,
					})
				} else {
					s.logger.Warn("execute references unknown statement",
						"session_id", reg.SessionID, "stmt_id", stmtID)
				}
			}
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			if err := relayResponse(upstream, client); err != nil {
				return err
			}

		case comStmtClose:
			if len(payload) >= 5 {
				stmtID := binary.LittleEndian.Uint32(payload[1:5])
				cache.Delete(stmtID)
				s.logger.Debug("prepared statement closed",
					"session_id", reg.SessionID, "stmt_id", stmtID)
			}
			// COM_STMT_CLOSE has no server response.
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}

		case comStmtSendLongData:
			// COM_STMT_SEND_LONG_DATA has no server response.
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}

		case comFieldList:
			// Deprecated but DBeaver may use it. Response: column defs until EOF/ERR.
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			if err := relayFieldListResponse(upstream, client); err != nil {
				return err
			}

		default:
			if err := writePacket(upstream, payload, seq); err != nil {
				return err
			}
			if err := relayResponse(upstream, client); err != nil {
				return err
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Response relay
// ---------------------------------------------------------------------------

// relayResponse reads a full MySQL response from upstream and writes it to client.
// It handles OK, ERR, result sets, LOCAL INFILE, and multi-result sets.
func relayResponse(upstream, client io.ReadWriter) error {
	for {
		more, err := relaySingleResponse(upstream, client)
		if err != nil || !more {
			return err
		}
	}
}

func relaySingleResponse(upstream, client io.ReadWriter) (moreResults bool, _ error) {
	payload, seq, err := readPacket(upstream)
	if err != nil {
		return false, err
	}
	if err := writePacket(client, payload, seq); err != nil {
		return false, err
	}
	if len(payload) == 0 {
		return false, nil
	}

	switch payload[0] {
	case iOK:
		return hasMoreResultsOK(payload), nil
	case iERR:
		return false, nil
	case iLocalInf:
		return relayLocalInFile(upstream, client)
	default:
		// EOF-as-first-packet with small payload is unusual; treat as terminal.
		if payload[0] == iEOF && len(payload) < 9 {
			return false, nil
		}
		// Result set.
		columnCount, _, err := readLenEncInt(payload)
		if err != nil {
			return false, fmt.Errorf("parse column count: %w", err)
		}
		if columnCount == 0 {
			return false, nil
		}
		for i := uint64(0); i < columnCount; i++ {
			if err := relayOnePacket(upstream, client); err != nil {
				return false, err
			}
		}
		// EOF after column definitions.
		if err := relayOnePacket(upstream, client); err != nil {
			return false, err
		}
		// Rows until EOF/ERR.
		return relayRows(upstream, client)
	}
}

func relayRows(upstream io.Reader, client io.Writer) (moreResults bool, _ error) {
	for {
		payload, seq, err := readPacket(upstream)
		if err != nil {
			return false, err
		}
		if err := writePacket(client, payload, seq); err != nil {
			return false, err
		}
		if isEOFPacket(payload) {
			return hasMoreResultsEOF(payload), nil
		}
		if isERRPacket(payload) {
			return false, nil
		}
	}
}

func relayLocalInFile(upstream, client io.ReadWriter) (moreResults bool, _ error) {
	// Client sends file data in packets, ending with an empty packet.
	for {
		data, dseq, err := readPacket(client)
		if err != nil {
			return false, fmt.Errorf("read local infile data: %w", err)
		}
		if err := writePacket(upstream, data, dseq); err != nil {
			return false, fmt.Errorf("forward local infile data: %w", err)
		}
		if len(data) == 0 {
			break
		}
	}
	// Server responds with OK or ERR.
	resp, rseq, err := readPacket(upstream)
	if err != nil {
		return false, err
	}
	if err := writePacket(client, resp, rseq); err != nil {
		return false, err
	}
	if len(resp) > 0 && resp[0] == iOK {
		return hasMoreResultsOK(resp), nil
	}
	return false, nil
}

func (s *Service) relayPrepareResponse(reg SessionRegistration, upstream io.Reader, client io.Writer, cache *preparedStmtCache, sql string) error {
	payload, seq, err := readPacket(upstream)
	if err != nil {
		return err
	}
	if err := writePacket(client, payload, seq); err != nil {
		return err
	}
	if len(payload) == 0 || payload[0] == iERR {
		return nil
	}
	if payload[0] != iOK || len(payload) < 12 {
		return fmt.Errorf("unexpected prepare response: 0x%02x len=%d", payload[0], len(payload))
	}

	// COM_STMT_PREPARE_OK: [OK(1) stmt_id(4) num_columns(2) num_params(2) reserved(1) warnings(2)]
	stmtID := binary.LittleEndian.Uint32(payload[1:5])
	numColumns := binary.LittleEndian.Uint16(payload[5:7])
	numParams := binary.LittleEndian.Uint16(payload[7:9])

	if sql != "" {
		cache.Store(stmtID, sql)
		s.logger.Debug("prepared statement cached",
			"session_id", reg.SessionID, "stmt_id", stmtID, "query_len", len(sql))
	}

	// Param definitions + EOF.
	if numParams > 0 {
		for i := uint16(0); i < numParams; i++ {
			if err := relayOnePacket(upstream, client); err != nil {
				return err
			}
		}
		if err := relayOnePacket(upstream, client); err != nil {
			return err
		}
	}
	// Column definitions + EOF.
	if numColumns > 0 {
		for i := uint16(0); i < numColumns; i++ {
			if err := relayOnePacket(upstream, client); err != nil {
				return err
			}
		}
		if err := relayOnePacket(upstream, client); err != nil {
			return err
		}
	}
	return nil
}

func relayFieldListResponse(upstream io.Reader, client io.Writer) error {
	for {
		payload, seq, err := readPacket(upstream)
		if err != nil {
			return err
		}
		if err := writePacket(client, payload, seq); err != nil {
			return err
		}
		if isEOFPacket(payload) || isERRPacket(payload) {
			return nil
		}
	}
}

func relayOnePacket(src io.Reader, dst io.Writer) error {
	payload, seq, err := readPacket(src)
	if err != nil {
		return err
	}
	return writePacket(dst, payload, seq)
}

// ---------------------------------------------------------------------------
// Query logging
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func (s *Service) unregister(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ln, ok := s.listeners[sessionID]; ok {
		_ = ln.Close()
		delete(s.listeners, sessionID)
	}
}

// Shutdown gracefully shuts down all active session listeners and the query log worker.
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

// ---------------------------------------------------------------------------
// MySQL wire protocol helpers
// ---------------------------------------------------------------------------

// readPacket reads a single MySQL packet (4-byte header + payload).
func readPacket(r io.Reader) ([]byte, uint8, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, 0, err
	}
	length := uint32(header[0]) | uint32(header[1])<<8 | uint32(header[2])<<16
	seq := header[3]
	if length == 0 {
		return nil, seq, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, err
	}
	return payload, seq, nil
}

// writePacket writes a MySQL packet with the 4-byte header.
func writePacket(w io.Writer, payload []byte, seq uint8) error {
	header := make([]byte, 4)
	pLen := len(payload)
	header[0] = byte(pLen)
	header[1] = byte(pLen >> 8)
	header[2] = byte(pLen >> 16)
	header[3] = seq
	if _, err := w.Write(header); err != nil {
		return err
	}
	if pLen > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handshake parsing / building
// ---------------------------------------------------------------------------

type handshakeV10 struct {
	ProtocolVersion byte
	ServerVersion   string
	ConnectionID    uint32
	AuthData        []byte // 20-byte salt/nonce
	Capabilities    uint32
	CharacterSet    byte
	StatusFlags     uint16
	AuthPlugin      string
}

func parseHandshakeV10(payload []byte) (handshakeV10, error) {
	if len(payload) < 4 {
		return handshakeV10{}, fmt.Errorf("handshake too short")
	}
	hs := handshakeV10{}
	pos := 0

	hs.ProtocolVersion = payload[pos]
	if hs.ProtocolVersion != 10 {
		return hs, fmt.Errorf("unsupported mysql protocol version: %d", hs.ProtocolVersion)
	}
	pos++

	nullPos := bytes.IndexByte(payload[pos:], 0)
	if nullPos < 0 {
		return hs, fmt.Errorf("malformed server version")
	}
	hs.ServerVersion = string(payload[pos : pos+nullPos])
	pos += nullPos + 1

	if pos+4 > len(payload) {
		return hs, fmt.Errorf("short handshake: connection id")
	}
	hs.ConnectionID = binary.LittleEndian.Uint32(payload[pos : pos+4])
	pos += 4

	if pos+8 > len(payload) {
		return hs, fmt.Errorf("short handshake: auth data part 1")
	}
	authData1 := make([]byte, 8)
	copy(authData1, payload[pos:pos+8])
	pos += 8

	// Filler byte.
	if pos >= len(payload) {
		hs.AuthData = authData1
		return hs, nil
	}
	pos++

	if pos+2 > len(payload) {
		hs.AuthData = authData1
		return hs, nil
	}
	capLow := binary.LittleEndian.Uint16(payload[pos : pos+2])
	pos += 2

	if pos >= len(payload) {
		hs.Capabilities = uint32(capLow)
		hs.AuthData = authData1
		return hs, nil
	}
	hs.CharacterSet = payload[pos]
	pos++

	if pos+2 > len(payload) {
		hs.Capabilities = uint32(capLow)
		hs.AuthData = authData1
		return hs, nil
	}
	hs.StatusFlags = binary.LittleEndian.Uint16(payload[pos : pos+2])
	pos += 2

	if pos+2 > len(payload) {
		hs.Capabilities = uint32(capLow)
		hs.AuthData = authData1
		return hs, nil
	}
	capHigh := binary.LittleEndian.Uint16(payload[pos : pos+2])
	hs.Capabilities = uint32(capLow) | uint32(capHigh)<<16
	pos += 2

	var authDataLen byte
	if pos < len(payload) {
		authDataLen = payload[pos]
		pos++
	}

	// Reserved 10 bytes.
	if pos+10 > len(payload) {
		hs.AuthData = authData1
		return hs, nil
	}
	pos += 10

	// Auth data part 2.
	if hs.Capabilities&capSecureConnection != 0 {
		part2Len := int(authDataLen) - 8
		if part2Len < 13 {
			part2Len = 13
		}
		if pos+part2Len > len(payload) {
			part2Len = len(payload) - pos
		}
		authData2 := payload[pos : pos+part2Len]
		pos += part2Len
		// Trim trailing null.
		if len(authData2) > 0 && authData2[len(authData2)-1] == 0 {
			authData2 = authData2[:len(authData2)-1]
		}
		hs.AuthData = make([]byte, len(authData1)+len(authData2))
		copy(hs.AuthData, authData1)
		copy(hs.AuthData[8:], authData2)
	} else {
		hs.AuthData = authData1
	}

	// Auth plugin name.
	if hs.Capabilities&capPluginAuth != 0 && pos < len(payload) {
		np := bytes.IndexByte(payload[pos:], 0)
		if np >= 0 {
			hs.AuthPlugin = string(payload[pos : pos+np])
		} else {
			hs.AuthPlugin = string(payload[pos:])
		}
	}

	return hs, nil
}

// buildServerHandshake creates the HandshakeV10 packet the proxy sends to clients.
func buildServerHandshake(salt []byte, supportSSL bool) []byte {
	var buf bytes.Buffer

	buf.WriteByte(10) // protocol version
	buf.WriteString("5.7.99-pam-mysql-proxy")
	buf.WriteByte(0) // null-terminated

	// Connection ID (4 bytes, arbitrary).
	connID := make([]byte, 4)
	_, _ = rand.Read(connID)
	buf.Write(connID)

	// Auth data part 1 (8 bytes).
	buf.Write(salt[:8])
	buf.WriteByte(0) // filler

	// Capability flags.
	caps := capLongPassword | capFoundRows | capLongFlag | capConnectWithDB |
		capProtocol41 | capTransactions | capSecureConnection | capPluginAuth
	if supportSSL {
		caps |= capSSL
	}
	binary.Write(&buf, binary.LittleEndian, uint16(caps&0xffff))

	buf.WriteByte(45)                                         // character set: utf8mb4_general_ci
	binary.Write(&buf, binary.LittleEndian, uint16(0x0002))   // status: SERVER_STATUS_AUTOCOMMIT
	binary.Write(&buf, binary.LittleEndian, uint16(caps>>16)) // upper capability flags

	buf.WriteByte(21)           // auth data length (8 + 13)
	buf.Write(make([]byte, 10)) // reserved

	// Auth data part 2 (12 bytes + null).
	buf.Write(salt[8:20])
	buf.WriteByte(0)

	// Auth plugin name.
	buf.WriteString(authNativePassword)
	buf.WriteByte(0)

	return buf.Bytes()
}

// buildHandshakeResponse creates the HandshakeResponse41 that the proxy sends
// to the upstream MySQL server.
func buildHandshakeResponse(serverCaps uint32, username, database, authPlugin string, authResponse []byte, useSSL bool) []byte {
	caps := capLongPassword | capFoundRows | capLongFlag | capProtocol41 |
		capTransactions | capSecureConnection | capPluginAuth
	if database != "" {
		caps |= capConnectWithDB
	}
	if useSSL {
		caps |= capSSL
	}
	// Only request capabilities the server supports (except our basics).
	caps &= serverCaps | capProtocol41 | capSecureConnection | capPluginAuth
	if database != "" {
		caps |= capConnectWithDB
	}
	if useSSL {
		caps |= capSSL
	}

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, caps)
	binary.Write(&buf, binary.LittleEndian, uint32(maxPacketSize))
	buf.WriteByte(45)           // character set: utf8mb4
	buf.Write(make([]byte, 23)) // reserved

	buf.WriteString(username)
	buf.WriteByte(0)

	// Auth response: length-encoded.
	buf.WriteByte(byte(len(authResponse)))
	buf.Write(authResponse)

	if database != "" {
		buf.WriteString(database)
		buf.WriteByte(0)
	}

	buf.WriteString(authPlugin)
	buf.WriteByte(0)

	return buf.Bytes()
}

// buildSSLRequest creates the 32-byte SSL request packet.
func buildSSLRequest() []byte {
	caps := capLongPassword | capFoundRows | capLongFlag | capConnectWithDB |
		capProtocol41 | capSSL | capTransactions | capSecureConnection | capPluginAuth

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, caps)
	binary.Write(&buf, binary.LittleEndian, uint32(maxPacketSize))
	buf.WriteByte(45)           // character set
	buf.Write(make([]byte, 23)) // reserved
	return buf.Bytes()
}

// buildOKPacket creates a MySQL OK packet.
func buildOKPacket(affectedRows, lastInsertID uint64, statusFlags, warnings uint16) []byte {
	var buf bytes.Buffer
	buf.WriteByte(iOK)
	writeLenEncInt(&buf, affectedRows)
	writeLenEncInt(&buf, lastInsertID)
	binary.Write(&buf, binary.LittleEndian, statusFlags)
	binary.Write(&buf, binary.LittleEndian, warnings)
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Auth helpers
// ---------------------------------------------------------------------------

func computeAuthResponse(plugin, password string, salt []byte) []byte {
	if password == "" {
		return nil
	}
	switch plugin {
	case authCachingSha2:
		return cachingSha2Auth(password, salt)
	default:
		return nativePasswordAuth(password, salt)
	}
}

// nativePasswordAuth implements mysql_native_password:
// SHA1(password) XOR SHA1(salt + SHA1(SHA1(password)))
func nativePasswordAuth(password string, salt []byte) []byte {
	hash1 := sha1.Sum([]byte(password))
	hash2 := sha1.Sum(hash1[:])

	h := sha1.New()
	h.Write(salt)
	h.Write(hash2[:])
	scramble := h.Sum(nil)

	for i := range scramble {
		scramble[i] ^= hash1[i]
	}
	return scramble
}

// cachingSha2Auth implements the caching_sha2_password scramble:
// SHA256(password) XOR SHA256(SHA256(SHA256(password)) + salt)
func cachingSha2Auth(password string, salt []byte) []byte {
	hash1 := sha256.Sum256([]byte(password))
	hash2 := sha256.Sum256(hash1[:])

	h := sha256.New()
	h.Write(hash2[:])
	h.Write(salt)
	hash3 := h.Sum(nil)

	result := make([]byte, len(hash1))
	for i := range result {
		result[i] = hash1[i] ^ hash3[i]
	}
	return result
}

func parseErrPacket(payload []byte) string {
	if len(payload) < 3 {
		return "unknown error"
	}
	errCode := binary.LittleEndian.Uint16(payload[1:3])
	pos := 3
	if pos < len(payload) && payload[pos] == '#' {
		pos += 1 + 5 // skip '#' + 5-char SQL state
	}
	msg := ""
	if pos < len(payload) {
		msg = string(payload[pos:])
	}
	if msg == "" {
		return fmt.Sprintf("error code %d", errCode)
	}
	return fmt.Sprintf("error %d: %s", errCode, strings.TrimSpace(msg))
}

func parseAuthSwitch(payload []byte) (plugin string, authData []byte) {
	if len(payload) < 2 || payload[0] != iEOF {
		return "", nil
	}
	rest := payload[1:]
	nullPos := bytes.IndexByte(rest, 0)
	if nullPos < 0 {
		return string(rest), nil
	}
	plugin = string(rest[:nullPos])
	authData = rest[nullPos+1:]
	if len(authData) > 0 && authData[len(authData)-1] == 0 {
		authData = authData[:len(authData)-1]
	}
	return plugin, authData
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

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
	case "allow", "prefer", "preferred", "":
		return sslModeConfig{AttemptTLS: true}
	case "require", "required":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true}
	case "verify-ca", "verify-full", "verify_ca", "verify_identity":
		return sslModeConfig{AttemptTLS: true, RequireTLS: true, VerifyCert: true}
	default:
		return sslModeConfig{AttemptTLS: true}
	}
}

func isEOFPacket(payload []byte) bool {
	return len(payload) > 0 && payload[0] == iEOF && len(payload) < 9
}

func isERRPacket(payload []byte) bool {
	return len(payload) > 0 && payload[0] == iERR
}

func hasMoreResultsOK(payload []byte) bool {
	if len(payload) < 2 {
		return false
	}
	pos := 1
	_, n, err := readLenEncInt(payload[pos:])
	if err != nil {
		return false
	}
	pos += n
	_, n, err = readLenEncInt(payload[pos:])
	if err != nil {
		return false
	}
	pos += n
	if pos+2 > len(payload) {
		return false
	}
	status := binary.LittleEndian.Uint16(payload[pos : pos+2])
	return status&statusMoreResults != 0
}

func hasMoreResultsEOF(payload []byte) bool {
	if len(payload) < 5 {
		return false
	}
	status := binary.LittleEndian.Uint16(payload[3:5])
	return status&statusMoreResults != 0
}

// readLenEncInt reads a MySQL length-encoded integer. Returns value, bytes consumed, error.
func readLenEncInt(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, fmt.Errorf("empty data for lenenc int")
	}
	switch {
	case data[0] < 0xfb:
		return uint64(data[0]), 1, nil
	case data[0] == 0xfc:
		if len(data) < 3 {
			return 0, 0, fmt.Errorf("short lenenc int (2-byte)")
		}
		return uint64(binary.LittleEndian.Uint16(data[1:3])), 3, nil
	case data[0] == 0xfd:
		if len(data) < 4 {
			return 0, 0, fmt.Errorf("short lenenc int (3-byte)")
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4, nil
	case data[0] == 0xfe:
		if len(data) < 9 {
			return 0, 0, fmt.Errorf("short lenenc int (8-byte)")
		}
		return binary.LittleEndian.Uint64(data[1:9]), 9, nil
	default:
		return 0, 0, fmt.Errorf("invalid lenenc int prefix: 0x%02x", data[0])
	}
}

func writeLenEncInt(buf *bytes.Buffer, v uint64) {
	switch {
	case v < 251:
		buf.WriteByte(byte(v))
	case v < 1<<16:
		buf.WriteByte(0xfc)
		binary.Write(buf, binary.LittleEndian, uint16(v))
	case v < 1<<24:
		buf.WriteByte(0xfd)
		buf.WriteByte(byte(v))
		buf.WriteByte(byte(v >> 8))
		buf.WriteByte(byte(v >> 16))
	default:
		buf.WriteByte(0xfe)
		binary.Write(buf, binary.LittleEndian, v)
	}
}

// ---------------------------------------------------------------------------
// TLS certificate generation
// ---------------------------------------------------------------------------

func buildProxyTLSConfig(publicHost string) (*tls.Config, error) {
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject: pkix.Name{
			CommonName: "pam-mysql-proxy",
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
