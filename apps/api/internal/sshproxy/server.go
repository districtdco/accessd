package sshproxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
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
}

type Server struct {
	cfg         Config
	logger      *slog.Logger
	sessionsSvc *sessions.Service
	credSvc     *credentials.Service

	listener net.Listener
	wg       sync.WaitGroup

	hostKeyCallback ssh.HostKeyCallback
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

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
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
					"host":       result.launch.Host,
					"port":       strconv.Itoa(result.launch.Port),
				},
			}, nil
		},
		KeyboardInteractiveCallback: func(conn ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := challenge(
				conn.User(),
				"Enter launch token for this shell session.",
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
					"host":       result.launch.Host,
					"port":       strconv.Itoa(result.launch.Port),
				},
			}, nil
		},
	}
	config.AddHostKey(hostSigner)
	return config, nil
}

func (s *Server) authenticate(conn ssh.ConnMetadata, token string) (authResult, error) {
	if conn.User() != s.cfg.Username {
		return authResult{}, fmt.Errorf("invalid proxy username")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lctx, err := s.sessionsSvc.ResolveLaunchToken(ctx, token)
	if err != nil {
		return authResult{}, err
	}
	s.logger.Info(
		"ssh proxy authentication succeeded",
		"session_id", lctx.SessionID,
		"user_id", lctx.UserID,
		"asset_id", lctx.AssetID,
		"remote_addr", conn.RemoteAddr().String(),
	)
	return authResult{launch: lctx}, nil
}

func (s *Server) handleConn(rawConn net.Conn, cfg *ssh.ServerConfig) {
	defer rawConn.Close()

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
			s.logger.Warn("failed to mark session failed", "session_id", launch.SessionID, "reason", reason, "error", err)
		}
	}
	finalizeEnded := func(reason string) {
		if finalized {
			return
		}
		finalized = true
		if err := s.sessionsSvc.MarkEnded(ctx, launch, reason); err != nil {
			s.logger.Warn("failed to record session end", "session_id", launch.SessionID, "error", err)
		}
	}
	defer func() {
		if !finalized {
			finalizeFailed("proxy_disconnected")
		}
	}()

	if err := s.sessionsSvc.MarkProxyConnected(ctx, launch, serverConn.RemoteAddr().String()); err != nil {
		s.logger.Warn("failed to record proxy_connected", "session_id", launch.SessionID, "error", err)
	}

	upstreamClient, upstreamSession, err := s.connectUpstream(ctx, launch)
	if err != nil {
		finalizeFailed("upstream_connect_failed")
		s.logger.Error("upstream ssh connection failed", "session_id", launch.SessionID, "error", err)
		return
	}
	defer upstreamClient.Close()
	defer upstreamSession.Close()

	if err := s.sessionsSvc.MarkUpstreamConnected(ctx, launch); err != nil {
		s.logger.Warn("failed to record upstream_connected", "session_id", launch.SessionID, "error", err)
	}

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels are supported")
			s.logger.Warn("rejected non-session channel", "session_id", launch.SessionID, "channel_type", newChannel.ChannelType())
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			finalizeFailed("session_channel_accept_failed")
			s.logger.Warn("failed to accept session channel", "session_id", launch.SessionID, "error", err)
			return
		}

		if err := s.bridgeSession(ctx, launch, channel, requests, upstreamSession); err != nil {
			reason := bridgeFailureReason(err)
			finalizeFailed(reason)
			s.logger.Warn("ssh bridge failed", "session_id", launch.SessionID, "reason", reason, "error", err)
			return
		}
		finalizeEnded("client_disconnected")
		s.logger.Info("ssh session bridge completed", "session_id", launch.SessionID)

		return
	}

	finalizeFailed("no_session_channel")
}

func (s *Server) connectUpstream(ctx context.Context, launch sessions.LaunchContext) (*ssh.Client, *ssh.Session, error) {
	cred, err := s.credSvc.ResolveForAsset(ctx, launch.AssetID, credentials.TypePassword)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve password credential: %w", err)
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
	s.logger.Info("connecting to upstream ssh", "session_id", launch.SessionID, "endpoint", endpoint)
	client, err := ssh.Dial("tcp", endpoint, clientCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dial upstream ssh: %w", err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("open upstream session: %w", err)
	}
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
			ok, reqErr := handleSessionRequest(req, upstreamSession)
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
	go s.copyWithEvents(ctx, launch, upstreamIn, clientCh, sessions.EventDataIn, "stdin", true, copyErrs)
	go s.copyWithEvents(ctx, launch, clientCh, upstreamOut, sessions.EventDataOut, "stdout", false, copyErrs)
	go s.copyWithEvents(ctx, launch, clientCh.Stderr(), upstreamErr, sessions.EventDataOut, "stderr", false, copyErrs)

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

func bridgeFailureReason(err error) string {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "shell_request_timeout"):
		return "shell_request_timeout"
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
			if recErr := s.sessionsSvc.RecordDataEvent(ctx, launch.SessionID, eventType, stream, chunk); recErr != nil {
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

func handleSessionRequest(req *ssh.Request, upstreamSession *ssh.Session) (bool, error) {
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
		Host:      get("host"),
		Port:      port,
		Protocol:  sessions.ProtocolSSH,
		Action:    "shell",
	}, nil
}
