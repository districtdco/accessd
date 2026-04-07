package launch

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type ShellBridgeArgs struct {
	Host      string
	Port      int
	Username  string
	SessionID string
	AssetName string
	TokenFile string
}

func RunShellBridgeCommand(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("bridge-shell", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	args := ShellBridgeArgs{}
	fs.StringVar(&args.Host, "host", "", "ssh host")
	fs.IntVar(&args.Port, "port", 0, "ssh port")
	fs.StringVar(&args.Username, "username", "", "ssh username")
	fs.StringVar(&args.SessionID, "session-id", "", "pam session id")
	fs.StringVar(&args.AssetName, "asset-name", "", "pam asset name")
	fs.StringVar(&args.TokenFile, "token-file", "", "path to launch token file")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	return runShellBridge(ctx, args)
}

func runShellBridge(ctx context.Context, args ShellBridgeArgs) error {
	if strings.TrimSpace(args.Host) == "" || args.Port <= 0 || strings.TrimSpace(args.Username) == "" || strings.TrimSpace(args.TokenFile) == "" {
		return fmt.Errorf("missing required bridge-shell arguments")
	}
	fmt.Fprintf(os.Stderr, "pam: connecting to %s:%d as %s (session %s, asset %s)\n",
		args.Host, args.Port, args.Username, args.SessionID, args.AssetName)

	blob, err := os.ReadFile(args.TokenFile)
	if err != nil {
		return fmt.Errorf("read launch token: %w", err)
	}
	_ = os.Remove(args.TokenFile)
	token := strings.TrimSpace(string(blob))
	if token == "" {
		return fmt.Errorf("launch token is empty")
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("shell bridge requires an interactive terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("enable terminal raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
	}()

	endpoint := net.JoinHostPort(args.Host, strconv.Itoa(args.Port))
	expectedHostKey, err := fetchSSHHostKey(ctx, args.Host, args.Port)
	if err != nil {
		return fmt.Errorf("resolve pam ssh proxy host key: %w", err)
	}
	sshCfg := &ssh.ClientConfig{
		User: args.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(token),
			ssh.KeyboardInteractive(func(_ string, _ string, _ []string, _ []bool) ([]string, error) {
				return []string{token}, nil
			}),
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if !bytes.Equal(key.Marshal(), expectedHostKey.Marshal()) {
				return fmt.Errorf("proxy host key mismatch")
			}
			return nil
		},
	}
	fmt.Fprintf(os.Stderr, "pam: dialing ssh proxy %s\n", endpoint)
	client, err := dialSSHWithContext(ctx, endpoint, sshCfg)
	if err != nil {
		return fmt.Errorf("connect to pam ssh proxy %s: %w", endpoint, err)
	}
	defer client.Close()
	fmt.Fprintf(os.Stderr, "pam: authenticated, opening session channel\n")

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open ssh session (proxy may have failed to reach upstream target): %w", err)
	}
	defer session.Close()

	width, height, sizeErr := term.GetSize(fd)
	if sizeErr != nil || width <= 0 || height <= 0 {
		width, height = 120, 32
	}
	termType := strings.TrimSpace(os.Getenv("TERM"))
	if termType == "" {
		termType = "xterm-256color"
	}
	if err := session.RequestPty(termType, height, width, ssh.TerminalModes{}); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if err := session.Shell(); err != nil {
		return fmt.Errorf("start remote shell: %w", err)
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			w, h, e := term.GetSize(fd)
			if e == nil && w > 0 && h > 0 {
				_ = session.WindowChange(h, w)
			}
		}
	}()
	winch <- syscall.SIGWINCH

	return session.Wait()
}

func dialSSHWithContext(ctx context.Context, endpoint string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, endpoint, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}
