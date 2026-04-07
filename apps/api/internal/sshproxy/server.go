package sshproxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/districtd/pam/api/internal/connutil"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/sessions"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Config struct {
	ListenAddr             string
	Username               string
	HostKeyPath            string
	UpstreamHostKeyMode    string
	UpstreamKnownHostsPath string
	IdleTimeout            time.Duration
	MaxSessionAge          time.Duration
}

type Server struct {
	cfg         Config
	logger      *slog.Logger
	sessionsSvc *sessions.Service
	credSvc     *credentials.Service

	listener net.Listener
	wg       sync.WaitGroup

	hostKeyCallback ssh.HostKeyCallback
	mu              sync.Mutex
	active          map[net.Conn]struct{}
}

func New(cfg Config, sessionsSvc *sessions.Service, credSvc *credentials.Service, logger *slog.Logger) (*Server, error) {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return nil, fmt.Errorf("ssh proxy listen address is required")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("ssh proxy username is required")
	}
	if strings.TrimSpace(cfg.HostKeyPath) == "" {
		return nil, fmt.Errorf("ssh proxy host key path is required")
	}
	if strings.TrimSpace(cfg.UpstreamKnownHostsPath) == "" {
		return nil, fmt.Errorf("ssh proxy upstream known hosts path is required")
	}
	if sessionsSvc == nil {
		return nil, fmt.Errorf("sessions service is required")
	}
	if credSvc == nil {
		return nil, fmt.Errorf("credentials service is required")
	}

	s := &Server{
		cfg:         cfg,
		logger:      logger.With("component", "ssh_proxy"),
		sessionsSvc: sessionsSvc,
		credSvc:     credSvc,
		active:      map[net.Conn]struct{}{},
	}
	if s.cfg.IdleTimeout <= 0 {
		s.cfg.IdleTimeout = 5 * time.Minute
	}
	if s.cfg.MaxSessionAge <= 0 {
		s.cfg.MaxSessionAge = 8 * time.Hour
	}
	callback, err := s.buildUpstreamHostKeyCallback()
	if err != nil {
		return nil, err
	}
	s.hostKeyCallback = callback
	return s, nil
}

func (s *Server) ListenAndServe() error {
	serverConfig, err := s.newSSHServerConfig()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen ssh proxy: %w", err)
	}
	s.listener = listener
	s.logger.Info(
		"ssh proxy listening",
		"addr", s.cfg.ListenAddr,
		"proxy_host_key_path", s.cfg.HostKeyPath,
		"upstream_hostkey_mode", s.cfg.UpstreamHostKeyMode,
		"upstream_known_hosts_path", s.cfg.UpstreamKnownHostsPath,
	)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept ssh connection: %w", err)
		}

		conn = connutil.WrapIdleTimeout(conn, s.cfg.IdleTimeout)
		s.trackConn(conn)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrackConn(conn)
			s.handleConn(conn, serverConfig)
		}()
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.listener != nil {
		_ = s.listener.Close()
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

type authResult struct {
	launch sessions.LaunchContext
}

func (s *Server) newSSHServerConfig() (*ssh.ServerConfig, error) {
	hostSigner, err := s.loadOrCreateProxyHostSigner()
	if err != nil {
		return nil, err
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			result, err := s.authenticate(conn, string(password))
			if err != nil {
				return nil, err
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"session_id": result.launch.SessionID,
					"user_id":    result.launch.UserID,
					"asset_id":   result.launch.AssetID,
					"request_id": result.launch.RequestID,
					"host":       result.launch.Host,
					"port":       strconv.Itoa(result.launch.Port),
					"protocol":   result.launch.Protocol,
					"action":     result.launch.Action,
				},
			}, nil
		},
		KeyboardInteractiveCallback: func(conn ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := challenge(
				conn.User(),
				"Enter launch token for this PAM session.",
				[]string{"Launch token:"},
				[]bool{false},
			)
			if err != nil {
				return nil, err
			}
			if len(answers) != 1 {
				return nil, fmt.Errorf("missing launch token")
			}
			result, err := s.authenticate(conn, answers[0])
			if err != nil {
				return nil, err
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"session_id": result.launch.SessionID,
					"user_id":    result.launch.UserID,
					"asset_id":   result.launch.AssetID,
					"request_id": result.launch.RequestID,
					"host":       result.launch.Host,
					"port":       strconv.Itoa(result.launch.Port),
					"protocol":   result.launch.Protocol,
					"action":     result.launch.Action,
				},
			}, nil
		},
	}
	config.AddHostKey(hostSigner)
	return config, nil
}

func (s *Server) authenticate(conn ssh.ConnMetadata, token string) (authResult, error) {
	remoteAddr := ""
	if conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}
	s.logger.Debug("ssh proxy authentication attempt",
		"remote_addr", remoteAddr, "proxy_user", conn.User(),
		"expected_user", s.cfg.Username, "token_len", len(token))
	if conn.User() != s.cfg.Username {
		s.logger.Warn("ssh proxy authentication failed",
			"reason", "invalid_proxy_username",
			"remote_addr", remoteAddr,
			"proxy_user", conn.User(),
			"expected_user", s.cfg.Username)
		return authResult{}, fmt.Errorf("invalid proxy username")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lctx, err := s.sessionsSvc.ResolveLaunchToken(ctx, token)
	if err != nil {
		reason := "launch_token_invalid"
		if errors.Is(err, sessions.ErrLaunchExpired) {
			reason = "launch_token_expired"
		} else if errors.Is(err, sessions.ErrUnauthorizedLaunch) {
			reason = "launch_token_unauthorized"
		}
		s.logger.Warn("ssh proxy authentication failed",
			"reason", reason,
			"remote_addr", remoteAddr,
			"proxy_user", conn.User(),
			"token_len", len(token),
			"error", err)
		return authResult{}, err
	}
	s.logger.Info(
		"ssh proxy authentication succeeded",
		"session_id", lctx.SessionID,
		"request_id", lctx.RequestID,
		"user_id", lctx.UserID,
		"asset_id", lctx.AssetID,
		"action", lctx.Action,
		"protocol", lctx.Protocol,
		"upstream_host", lctx.Host,
		"upstream_port", lctx.Port,
		"remote_addr", remoteAddr,
	)
	return authResult{launch: lctx}, nil
}

func (s *Server) handleConn(rawConn net.Conn, cfg *ssh.ServerConfig) {
	defer rawConn.Close()
	if s.cfg.MaxSessionAge > 0 {
		timer := time.AfterFunc(s.cfg.MaxSessionAge, func() {
			s.logger.Warn("ssh proxy max session duration reached; closing connection", "remote_addr", rawConn.RemoteAddr().String())
			_ = rawConn.Close()
		})
		defer timer.Stop()
	}

	serverConn, chans, reqs, err := ssh.NewServerConn(rawConn, cfg)
	if err != nil {
		s.logger.Warn("ssh handshake failed", "remote_addr", rawConn.RemoteAddr().String(), "error", err)
		return
	}
	defer serverConn.Close()

	launch, err := launchFromPermissions(serverConn.Permissions)
	if err != nil {
		s.logger.Warn("missing launch context on authenticated connection", "error", err)
		return
	}

	ctx := context.Background()
	finalized := false
	finalizeFailed := func(reason string) {
		if finalized {
			return
		}
		finalized = true
		if err := s.sessionsSvc.MarkFailed(ctx, launch, reason); err != nil {
			s.logger.Warn("failed to mark session failed", "session_id", launch.SessionID, "request_id", launch.RequestID, "reason", reason, "error", err)
		}
	}
	finalizeEnded := func(reason string) {
		if finalized {
			return
		}
		finalized = true
		if err := s.sessionsSvc.MarkEnded(ctx, launch, reason); err != nil {
			s.logger.Warn("failed to record session end", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
		}
	}
	defer func() {
		if !finalized {
			finalizeFailed("proxy_disconnected")
		}
	}()

	if err := s.sessionsSvc.MarkProxyConnected(ctx, launch, serverConn.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record proxy_connected", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
	}

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels are supported")
			s.logger.Warn("rejected non-session channel", "session_id", launch.SessionID, "request_id", launch.RequestID, "channel_type", newChannel.ChannelType())
			continue
		}

		// Connect upstream AFTER client opens session channel so that failures
		// produce a proper SSH channel rejection instead of an abrupt close.
		s.logger.Info("client session channel requested, connecting upstream",
			"session_id", launch.SessionID, "request_id", launch.RequestID,
			"upstream_host", launch.Host, "upstream_port", launch.Port, "action", launch.Action)

		upstreamClient, upstreamSession, err := s.connectUpstream(ctx, launch)
		if err != nil {
			rejectMsg := "upstream connection failed: " + err.Error()
			_ = newChannel.Reject(ssh.ConnectionFailed, rejectMsg)
			finalizeFailed("upstream_connect_failed")
			s.logger.Error("upstream ssh connection failed", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
			return
		}

		if err := s.sessionsSvc.MarkUpstreamConnected(ctx, launch); err != nil {
			s.logger.Warn("failed to record upstream_connected", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			_ = upstreamSession.Close()
			_ = upstreamClient.Close()
			finalizeFailed("session_channel_accept_failed")
			s.logger.Warn("failed to accept session channel", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
			return
		}

		var bridgeErr error
		switch launch.Action {
		case "sftp":
			bridgeErr = s.bridgeSFTPSession(ctx, launch, channel, requests, upstreamSession)
		default:
			bridgeErr = s.bridgeSession(ctx, launch, channel, requests, upstreamSession)
		}

		_ = upstreamSession.Close()
		_ = upstreamClient.Close()

		if bridgeErr != nil {
			reason := bridgeFailureReason(bridgeErr)
			finalizeFailed(reason)
			s.logger.Warn("ssh bridge failed", "session_id", launch.SessionID, "request_id", launch.RequestID, "reason", reason, "error", bridgeErr, "action", launch.Action)
			return
		}
		finalizeEnded("client_disconnected")
		s.logger.Info("ssh session bridge completed", "session_id", launch.SessionID, "request_id", launch.RequestID, "action", launch.Action)

		return
	}

	finalizeFailed("no_session_channel")
}

func (s *Server) connectUpstream(ctx context.Context, launch sessions.LaunchContext) (*ssh.Client, *ssh.Session, error) {
	cred, err := s.credSvc.ResolveForAsset(ctx, launch.AssetID, credentials.TypePassword)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve password credential: %w", err)
	}
	if err := s.sessionsSvc.RecordCredentialUsage(ctx, launch, credentials.TypePassword, "proxy_upstream_auth", launch.RequestID); err != nil {
		s.logger.Warn("failed to write credential usage audit", "session_id", launch.SessionID, "request_id", launch.RequestID, "error", err)
	}
	if strings.TrimSpace(cred.Username) == "" {
		return nil, nil, fmt.Errorf("credential username is required for ssh")
	}

	clientCfg := &ssh.ClientConfig{
		User: cred.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cred.Secret),
		},
		HostKeyCallback: s.hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	endpoint := net.JoinHostPort(launch.Host, strconv.Itoa(launch.Port))
	s.logger.Info("connecting to upstream ssh",
		"session_id", launch.SessionID, "request_id", launch.RequestID,
		"endpoint", endpoint, "upstream_user", cred.Username, "action", launch.Action)
	client, err := ssh.Dial("tcp", endpoint, clientCfg)
	if err != nil {
		s.logger.Error("upstream ssh dial failed",
			"session_id", launch.SessionID, "request_id", launch.RequestID,
			"endpoint", endpoint, "error", err)
		return nil, nil, fmt.Errorf("dial upstream ssh %s: %w", endpoint, err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		s.logger.Error("upstream ssh session open failed",
			"session_id", launch.SessionID, "request_id", launch.RequestID,
			"endpoint", endpoint, "error", err)
		return nil, nil, fmt.Errorf("open upstream session: %w", err)
	}
	s.logger.Info("upstream ssh connected",
		"session_id", launch.SessionID, "request_id", launch.RequestID,
		"endpoint", endpoint)
	return client, session, nil
}

func (s *Server) bridgeSession(
	ctx context.Context,
	launch sessions.LaunchContext,
	clientCh ssh.Channel,
	requests <-chan *ssh.Request,
	upstreamSession *ssh.Session,
) error {
	defer clientCh.Close()
	recorder := newShellRecorder()

	upstreamIn, err := upstreamSession.StdinPipe()
	if err != nil {
		return fmt.Errorf("open upstream stdin: %w", err)
	}
	upstreamOut, err := upstreamSession.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open upstream stdout: %w", err)
	}
	upstreamErr, err := upstreamSession.StderrPipe()
	if err != nil {
		return fmt.Errorf("open upstream stderr: %w", err)
	}

	shellStarted := make(chan struct{}, 1)
	reqErrs := make(chan error, 1)
	go func() {
		for req := range requests {
			ok, reqErr := s.handleSessionRequest(ctx, launch, recorder, req, upstreamSession)
			if req.WantReply {
				_ = req.Reply(ok, nil)
			}
			if reqErr != nil {
				reqErrs <- reqErr
				return
			}
			if req.Type == "shell" {
				select {
				case shellStarted <- struct{}{}:
				default:
				}
			}
		}
	}()

	select {
	case <-shellStarted:
		if err := s.sessionsSvc.MarkShellStarted(ctx, launch); err != nil {
			s.logger.Warn("failed to mark shell start", "session_id", launch.SessionID, "error", err)
		}
	case reqErr := <-reqErrs:
		return fmt.Errorf("session_request_failed: %w", reqErr)
	case <-time.After(10 * time.Second):
		return fmt.Errorf("shell_request_timeout")
	}

	copyErrs := make(chan error, 3)
	go s.copyWithEvents(ctx, launch, recorder, upstreamIn, clientCh, sessions.EventDataIn, "stdin", true, copyErrs)
	go s.copyWithEvents(ctx, launch, recorder, clientCh, upstreamOut, sessions.EventDataOut, "stdout", false, copyErrs)
	go s.copyWithEvents(ctx, launch, recorder, clientCh.Stderr(), upstreamErr, sessions.EventDataOut, "stderr", false, copyErrs)

	waitErr := upstreamSession.Wait()

	// Wait for copy goroutines to settle after session end.
	for i := 0; i < 3; i++ {
		if copyErr := <-copyErrs; copyErr != nil {
			s.logger.Warn("stream copy ended with error", "session_id", launch.SessionID, "error", copyErr)
		}
	}

	if waitErr != nil {
		if exitErr, ok := waitErr.(*ssh.ExitError); ok {
			status := exitErr.ExitStatus()
			_, _ = clientCh.SendRequest("exit-status", false, ssh.Marshal(struct {
				Status uint32
			}{Status: uint32(status)}))
		} else {
			return fmt.Errorf("upstream_wait_failed: %w", waitErr)
		}
	} else {
		_, _ = clientCh.SendRequest("exit-status", false, ssh.Marshal(struct {
			Status uint32
		}{Status: 0}))
	}

	return nil
}

const (
	sftpPacketInit     byte = 1
	sftpPacketVersion  byte = 2
	sftpPacketOpen     byte = 3
	sftpPacketClose    byte = 4
	sftpPacketRead     byte = 5
	sftpPacketWrite    byte = 6
	sftpPacketLstat    byte = 7
	sftpPacketFstat    byte = 8
	sftpPacketSetstat  byte = 9
	sftpPacketOpendir  byte = 11
	sftpPacketReaddir  byte = 12
	sftpPacketRemove   byte = 13
	sftpPacketMkdir    byte = 14
	sftpPacketRmdir    byte = 15
	sftpPacketRealpath byte = 16
	sftpPacketStat     byte = 17
	sftpPacketRename   byte = 18

	sftpPacketStatus byte = 101
	sftpPacketHandle byte = 102
	sftpPacketData   byte = 103
	sftpPacketName   byte = 104
)

type sftpPendingRequest struct {
	Operation string
	Path      string
	PathTo    string
	Handle    string
	Size      int64
}

type sftpFileOperation struct {
	Operation string
	Path      string
	PathTo    string
	Size      int64
}

type sftpRelayState struct {
	mu      sync.Mutex
	pending map[uint32]sftpPendingRequest
	handles map[string]string
}

func newSFTPRelayState() *sftpRelayState {
	return &sftpRelayState{
		pending: map[uint32]sftpPendingRequest{},
		handles: map[string]string{},
	}
}

func (s *Server) bridgeSFTPSession(
	ctx context.Context,
	launch sessions.LaunchContext,
	clientCh ssh.Channel,
	requests <-chan *ssh.Request,
	upstreamSession *ssh.Session,
) error {
	defer clientCh.Close()

	upstreamIn, err := upstreamSession.StdinPipe()
	if err != nil {
		return fmt.Errorf("open upstream stdin: %w", err)
	}
	upstreamOut, err := upstreamSession.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open upstream stdout: %w", err)
	}
	upstreamErr, err := upstreamSession.StderrPipe()
	if err != nil {
		return fmt.Errorf("open upstream stderr: %w", err)
	}

	subsystemStarted := make(chan struct{}, 1)
	reqErrs := make(chan error, 1)
	go func() {
		for req := range requests {
			ok, started, reqErr := handleSFTPRequest(req, upstreamSession)
			if req.WantReply {
				_ = req.Reply(ok, nil)
			}
			if reqErr != nil {
				reqErrs <- reqErr
				return
			}
			if started {
				select {
				case subsystemStarted <- struct{}{}:
				default:
				}
			}
		}
	}()

	select {
	case <-subsystemStarted:
	case reqErr := <-reqErrs:
		return fmt.Errorf("session_request_failed: %w", reqErr)
	case <-time.After(10 * time.Second):
		return fmt.Errorf("sftp_subsystem_timeout")
	}

	state := newSFTPRelayState()
	copyErrs := make(chan error, 3)

	go s.copySFTPClientToUpstream(ctx, launch, upstreamIn, clientCh, state, copyErrs)
	go s.copySFTPUpstreamToClient(ctx, launch, clientCh, upstreamOut, state, copyErrs)
	go s.copySFTPRaw(clientCh.Stderr(), upstreamErr, copyErrs)

	waitErr := upstreamSession.Wait()
	for i := 0; i < 3; i++ {
		if copyErr := <-copyErrs; copyErr != nil {
			s.logger.Warn("sftp stream copy ended with error", "session_id", launch.SessionID, "error", copyErr)
		}
	}
	if waitErr != nil {
		if exitErr, ok := waitErr.(*ssh.ExitError); ok {
			status := exitErr.ExitStatus()
			_, _ = clientCh.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: uint32(status)}))
		} else {
			return fmt.Errorf("upstream_wait_failed: %w", waitErr)
		}
	} else {
		_, _ = clientCh.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
	}
	return nil
}

func handleSFTPRequest(req *ssh.Request, upstreamSession *ssh.Session) (ok bool, started bool, err error) {
	switch req.Type {
	case "subsystem":
		var payload struct {
			Name string
		}
		if unmarshalErr := ssh.Unmarshal(req.Payload, &payload); unmarshalErr != nil {
			return false, false, fmt.Errorf("invalid subsystem payload: %w", unmarshalErr)
		}
		if strings.TrimSpace(payload.Name) != "sftp" {
			return false, false, nil
		}
		if subErr := upstreamSession.RequestSubsystem("sftp"); subErr != nil {
			return false, false, fmt.Errorf("request upstream sftp subsystem: %w", subErr)
		}
		return true, true, nil
	case "env":
		// Accept and ignore env for compatibility.
		return true, false, nil
	default:
		return false, false, nil
	}
}

func (s *Server) copySFTPRaw(dst io.Writer, src io.Reader, done chan<- error) {
	_, err := io.Copy(dst, src)
	if errors.Is(err, io.EOF) {
		done <- nil
		return
	}
	done <- err
}

func (s *Server) copySFTPClientToUpstream(
	ctx context.Context,
	launch sessions.LaunchContext,
	dst io.WriteCloser,
	src io.Reader,
	state *sftpRelayState,
	done chan<- error,
) {
	defer func() {
		_ = dst.Close()
	}()
	parser := newSFTPPacketParser(func(payload []byte) {
		s.handleSFTPClientPacket(ctx, launch, payload, state)
	})
	done <- copyWithParser(dst, src, parser)
}

func (s *Server) copySFTPUpstreamToClient(
	ctx context.Context,
	launch sessions.LaunchContext,
	dst io.Writer,
	src io.Reader,
	state *sftpRelayState,
	done chan<- error,
) {
	parser := newSFTPPacketParser(func(payload []byte) {
		s.handleSFTPServerPacket(ctx, launch, payload, state)
	})
	done <- copyWithParser(dst, src, parser)
}

func copyWithParser(dst io.Writer, src io.Reader, parser *sftpPacketParser) error {
	buf := make([]byte, 16*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, wErr := dst.Write(chunk); wErr != nil {
				return wErr
			}
			parser.Feed(chunk)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type sftpPacketParser struct {
	buf      []byte
	onPacket func([]byte)
}

func newSFTPPacketParser(onPacket func([]byte)) *sftpPacketParser {
	return &sftpPacketParser{onPacket: onPacket}
}

func (p *sftpPacketParser) Feed(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	p.buf = append(p.buf, chunk...)
	for {
		if len(p.buf) < 4 {
			return
		}
		packetLen := int(binary.BigEndian.Uint32(p.buf[:4]))
		total := 4 + packetLen
		if packetLen <= 0 || total > 16*1024*1024 {
			p.buf = nil
			return
		}
		if len(p.buf) < total {
			return
		}
		payload := make([]byte, packetLen)
		copy(payload, p.buf[4:total])
		p.onPacket(payload)
		p.buf = p.buf[total:]
	}
}

func (s *Server) handleSFTPClientPacket(ctx context.Context, launch sessions.LaunchContext, payload []byte, state *sftpRelayState) {
	for _, op := range parseSFTPClientPacket(payload, state) {
		s.recordFileOperation(ctx, launch, op)
	}
}

func parseSFTPClientPacket(payload []byte, state *sftpRelayState) []sftpFileOperation {
	ops := make([]sftpFileOperation, 0, 2)
	if len(payload) < 1 {
		return ops
	}
	packetType := payload[0]
	if packetType == sftpPacketInit {
		return ops
	}
	if len(payload) < 5 {
		return ops
	}
	reqID := binary.BigEndian.Uint32(payload[1:5])
	body := payload[5:]

	switch packetType {
	case sftpPacketOpen:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		state.storePending(reqID, sftpPendingRequest{Operation: "open", Path: path})
	case sftpPacketOpendir:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		state.storePending(reqID, sftpPendingRequest{Operation: "opendir", Path: path})
		ops = append(ops, sftpFileOperation{Operation: "list", Path: path, Size: 0})
	case sftpPacketRead:
		handle, offset, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		if offset+12 > len(body) {
			return ops
		}
		size := int64(binary.BigEndian.Uint32(body[offset+8 : offset+12]))
		path := state.lookupPathByHandle(handle)
		state.storePending(reqID, sftpPendingRequest{Operation: "download_read", Path: path, Handle: handle, Size: size})
	case sftpPacketWrite:
		handle, offset, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		if offset+8 > len(body) {
			return ops
		}
		data, _, ok := readSFTPString(body, offset+8)
		if !ok {
			return ops
		}
		path := state.lookupPathByHandle(handle)
		ops = append(ops, sftpFileOperation{
			Operation: "upload_write",
			Path:      path,
			Size:      int64(len(data)),
		})
	case sftpPacketRemove:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		ops = append(ops, sftpFileOperation{Operation: "delete", Path: path})
	case sftpPacketRename:
		oldPath, next, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		newPath, _, ok := readSFTPString(body, next)
		if !ok {
			return ops
		}
		ops = append(ops, sftpFileOperation{Operation: "rename", Path: oldPath, PathTo: newPath})
	case sftpPacketMkdir:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		ops = append(ops, sftpFileOperation{Operation: "mkdir", Path: path})
	case sftpPacketRmdir:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		ops = append(ops, sftpFileOperation{Operation: "rmdir", Path: path})
	case sftpPacketStat, sftpPacketLstat, sftpPacketSetstat, sftpPacketRealpath:
		path, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		ops = append(ops, sftpFileOperation{Operation: "stat", Path: path})
	case sftpPacketReaddir:
		handle, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		path := state.lookupPathByHandle(handle)
		if path != "" {
			ops = append(ops, sftpFileOperation{Operation: "list", Path: path})
		}
	case sftpPacketClose:
		handle, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		state.deleteHandle(handle)
	}
	return ops
}

func (s *Server) handleSFTPServerPacket(ctx context.Context, launch sessions.LaunchContext, payload []byte, state *sftpRelayState) {
	for _, op := range parseSFTPServerPacket(payload, state) {
		s.recordFileOperation(ctx, launch, op)
	}
}

func parseSFTPServerPacket(payload []byte, state *sftpRelayState) []sftpFileOperation {
	ops := make([]sftpFileOperation, 0, 1)
	if len(payload) < 1 {
		return ops
	}
	packetType := payload[0]
	if packetType == sftpPacketVersion || len(payload) < 5 {
		return ops
	}
	reqID := binary.BigEndian.Uint32(payload[1:5])
	body := payload[5:]

	switch packetType {
	case sftpPacketHandle:
		handleRaw, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		pending, ok := state.popPending(reqID)
		if !ok {
			return ops
		}
		if pending.Operation == "open" || pending.Operation == "opendir" {
			state.mapHandleToPath(handleRaw, pending.Path)
		}
	case sftpPacketData:
		data, _, ok := readSFTPString(body, 0)
		if !ok {
			return ops
		}
		pending, ok := state.popPending(reqID)
		if !ok || pending.Operation != "download_read" {
			return ops
		}
		path := pending.Path
		if path == "" && pending.Handle != "" {
			path = state.lookupPathByHandle(pending.Handle)
		}
		ops = append(ops, sftpFileOperation{
			Operation: "download_read",
			Path:      path,
			Size:      int64(len(data)),
		})
	case sftpPacketStatus:
		// Drop pending read/open bookkeeping on status replies.
		state.popPending(reqID)
	case sftpPacketName:
		// READDIR result; clear pending bookkeeping if set.
		state.popPending(reqID)
	}
	return ops
}

func (s *Server) recordFileOperation(ctx context.Context, launch sessions.LaunchContext, op sftpFileOperation) {
	if strings.TrimSpace(op.Operation) == "" {
		return
	}
	payload := buildFileOperationPayload(launch, op, time.Now().UTC())
	actor := launch.UserID
	if err := s.sessionsSvc.WriteEvent(ctx, launch.SessionID, sessions.EventFileOperation, &actor, payload); err != nil {
		s.logger.Warn("failed to write file operation event", "session_id", launch.SessionID, "operation", op.Operation, "error", err)
	}
}

func buildFileOperationPayload(launch sessions.LaunchContext, op sftpFileOperation, when time.Time) map[string]any {
	payload := map[string]any{
		"session_id": launch.SessionID,
		"user_id":    launch.UserID,
		"asset_id":   launch.AssetID,
		"operation":  op.Operation,
		"path":       strings.TrimSpace(op.Path),
		"event_time": when.UTC().Format(time.RFC3339Nano),
		"request_id": strings.TrimSpace(launch.RequestID),
	}
	if strings.TrimSpace(op.PathTo) != "" {
		payload["path_to"] = strings.TrimSpace(op.PathTo)
	}
	if op.Size > 0 {
		payload["size"] = op.Size
	}
	return payload
}

func readSFTPString(payload []byte, offset int) (string, int, bool) {
	if offset+4 > len(payload) {
		return "", 0, false
	}
	ln := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
	start := offset + 4
	end := start + ln
	if ln < 0 || end > len(payload) {
		return "", 0, false
	}
	return string(payload[start:end]), end, true
}

func (s *sftpRelayState) storePending(id uint32, req sftpPendingRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[id] = req
}

func (s *sftpRelayState) popPending(id uint32) (sftpPendingRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	return req, ok
}

func (s *sftpRelayState) mapHandleToPath(handle, path string) {
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(path) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handles[handle] = path
}

func (s *sftpRelayState) lookupPathByHandle(handle string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handles[handle]
}

func (s *sftpRelayState) deleteHandle(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.handles, handle)
}

func bridgeFailureReason(err error) string {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "shell_request_timeout"):
		return "shell_request_timeout"
	case strings.Contains(text, "sftp_subsystem_timeout"):
		return "sftp_subsystem_timeout"
	case strings.Contains(text, "session_request_failed"):
		return "session_request_failed"
	case strings.Contains(text, "upstream_wait_failed"):
		return "upstream_wait_failed"
	default:
		return "bridge_failed"
	}
}

func (s *Server) copyWithEvents(
	ctx context.Context,
	launch sessions.LaunchContext,
	recorder *shellRecorder,
	dst io.Writer,
	src io.Reader,
	eventType, stream string,
	closeDst bool,
	done chan<- error,
) {
	var result error
	defer func() {
		if closer, ok := dst.(io.Closer); ok && closeDst {
			_ = closer.Close()
		}
		done <- result
	}()

	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if _, werr := dst.Write(chunk); werr != nil {
				result = werr
				return
			}
			offset := 0.0
			if recorder != nil {
				offset = recorder.elapsed()
			}
			if recErr := s.sessionsSvc.RecordDataEvent(ctx, launch.SessionID, eventType, stream, chunk, offset); recErr != nil {
				s.logger.Warn("failed to record data event", "session_id", launch.SessionID, "event_type", eventType, "error", recErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return
			}
			result = err
			return
		}
	}
}

type shellRecorder struct {
	startedAt time.Time
}

func newShellRecorder() *shellRecorder {
	return &shellRecorder{startedAt: time.Now().UTC()}
}

func (r *shellRecorder) elapsed() float64 {
	if r == nil || r.startedAt.IsZero() {
		return 0
	}
	seconds := time.Since(r.startedAt).Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (s *Server) loadOrCreateProxyHostSigner() (ssh.Signer, error) {
	path := strings.TrimSpace(s.cfg.HostKeyPath)
	keyBytes, err := os.ReadFile(path)
	if err == nil {
		signer, parseErr := ssh.ParsePrivateKey(keyBytes)
		if parseErr != nil {
			return nil, fmt.Errorf("parse proxy host key %s: %w", path, parseErr)
		}
		return signer, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read proxy host key %s: %w", path, err)
	}

	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return nil, fmt.Errorf("create proxy host key directory: %w", mkErr)
	}

	privateKey, genErr := rsa.GenerateKey(rand.Reader, 2048)
	if genErr != nil {
		return nil, fmt.Errorf("generate proxy host key: %w", genErr)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	if writeErr := os.WriteFile(path, keyPEM, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write proxy host key: %w", writeErr)
	}
	s.logger.Info("generated persistent ssh proxy host key", "path", path)
	return ssh.NewSignerFromKey(privateKey)
}

func (s *Server) buildUpstreamHostKeyCallback() (ssh.HostKeyCallback, error) {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.UpstreamHostKeyMode))
	switch mode {
	case "insecure":
		s.logger.Warn("upstream host key mode is insecure; use only for local development")
		return ssh.InsecureIgnoreHostKey(), nil
	case "known-hosts", "accept-new":
	default:
		return nil, fmt.Errorf("unsupported upstream host key mode: %s", mode)
	}

	path := strings.TrimSpace(s.cfg.UpstreamKnownHostsPath)
	if err := ensureKnownHostsFile(path); err != nil {
		return nil, err
	}

	baseCallback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("build known_hosts callback: %w", err)
	}
	if mode == "known-hosts" {
		return baseCallback, nil
	}

	var mu sync.Mutex
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := baseCallback(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) > 0 {
			// Host exists but key mismatched.
			return err
		}
		line := knownhosts.Line([]string{knownHostsAddrString(hostname, remote)}, key)
		mu.Lock()
		defer mu.Unlock()
		f, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return fmt.Errorf("append known_hosts: %w", openErr)
		}
		defer f.Close()
		if _, writeErr := fmt.Fprintln(f, line); writeErr != nil {
			return fmt.Errorf("write known_hosts entry: %w", writeErr)
		}
		s.logger.Info("accepted new upstream host key", "known_hosts_path", path, "host", hostname)
		return nil
	}, nil
}

func ensureKnownHostsFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("known_hosts path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create known_hosts directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return fmt.Errorf("ensure known_hosts file: %w", err)
	}
	defer f.Close()
	return nil
}

func knownHostsAddrString(hostname string, remote net.Addr) string {
	trimmed := strings.TrimSpace(hostname)
	if strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]:") {
		return trimmed
	}
	if host, port, err := net.SplitHostPort(strings.TrimSpace(remote.String())); err == nil {
		return net.JoinHostPort(host, port)
	}
	return trimmed
}

func (s *Server) handleSessionRequest(
	ctx context.Context,
	launch sessions.LaunchContext,
	recorder *shellRecorder,
	req *ssh.Request,
	upstreamSession *ssh.Session,
) (bool, error) {
	switch req.Type {
	case "pty-req":
		var payload struct {
			Term            string
			Columns         uint32
			Rows            uint32
			PixelWidth      uint32
			PixelHeight     uint32
			EncodedTerminal []byte `ssh:"rest"`
		}
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			return false, fmt.Errorf("invalid pty request payload: %w", err)
		}
		modes := parseTerminalModes(payload.EncodedTerminal)
		if err := upstreamSession.RequestPty(payload.Term, int(payload.Rows), int(payload.Columns), modes); err != nil {
			return false, fmt.Errorf("request upstream pty: %w", err)
		}
		if recErr := s.sessionsSvc.RecordTerminalResizeEvent(ctx, launch.SessionID, int(payload.Columns), int(payload.Rows), recorder.elapsed()); recErr != nil {
			s.logger.Warn("failed to record terminal resize", "session_id", launch.SessionID, "error", recErr)
		}
		return true, nil
	case "window-change":
		var payload struct {
			Columns     uint32
			Rows        uint32
			PixelWidth  uint32
			PixelHeight uint32
		}
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			return false, fmt.Errorf("invalid window change payload: %w", err)
		}
		if err := upstreamSession.WindowChange(int(payload.Rows), int(payload.Columns)); err != nil {
			return false, fmt.Errorf("forward window change: %w", err)
		}
		if recErr := s.sessionsSvc.RecordTerminalResizeEvent(ctx, launch.SessionID, int(payload.Columns), int(payload.Rows), recorder.elapsed()); recErr != nil {
			s.logger.Warn("failed to record terminal resize", "session_id", launch.SessionID, "error", recErr)
		}
		return true, nil
	case "shell":
		if err := upstreamSession.Shell(); err != nil {
			return false, fmt.Errorf("start upstream shell: %w", err)
		}
		return true, nil
	default:
		// Keep first pass strict and explicit: unsupported requests are rejected.
		return false, nil
	}
}

func parseTerminalModes(encoded []byte) ssh.TerminalModes {
	modes := ssh.TerminalModes{}
	for i := 0; i < len(encoded); {
		opcode := encoded[i]
		i++
		if opcode == 0 {
			break
		}
		if i+4 > len(encoded) {
			break
		}
		value := uint32(encoded[i])<<24 | uint32(encoded[i+1])<<16 | uint32(encoded[i+2])<<8 | uint32(encoded[i+3])
		modes[opcode] = value
		i += 4
	}
	return modes
}

func launchFromPermissions(perms *ssh.Permissions) (sessions.LaunchContext, error) {
	if perms == nil || perms.Extensions == nil {
		return sessions.LaunchContext{}, fmt.Errorf("missing ssh permissions")
	}
	get := func(key string) string {
		return strings.TrimSpace(perms.Extensions[key])
	}
	port, err := strconv.Atoi(get("port"))
	if err != nil {
		return sessions.LaunchContext{}, fmt.Errorf("invalid launch port")
	}
	return sessions.LaunchContext{
		SessionID: get("session_id"),
		UserID:    get("user_id"),
		AssetID:   get("asset_id"),
		RequestID: get("request_id"),
		Host:      get("host"),
		Port:      port,
		Protocol:  defaultIfEmpty(get("protocol"), sessions.ProtocolSSH),
		Action:    defaultIfEmpty(get("action"), "shell"),
	}, nil
}

func defaultIfEmpty(v, fallback string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func (s *Server) trackConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn != nil {
		s.active[conn] = struct{}{}
	}
}

func (s *Server) untrackConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, conn)
}

func (s *Server) closeActiveConns() {
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
