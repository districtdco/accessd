package launch

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func fetchSSHHostKey(ctx context.Context, host string, port int) (ssh.PublicKey, error) {
	endpoint := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
	var (
		captured ssh.PublicKey
		once     sync.Once
	)
	cfg := &ssh.ClientConfig{
		User:            "pam",
		Auth:            []ssh.AuthMethod{ssh.Password("invalid")},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error { once.Do(func() { captured = key }); return nil },
	}
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	defer rawConn.Close()
	_, _, _, err = ssh.NewClientConn(rawConn, endpoint, cfg)
	if captured == nil {
		if err == nil {
			return nil, fmt.Errorf("ssh handshake completed without host key capture")
		}
		return nil, fmt.Errorf("ssh handshake failed before host key capture: %w", err)
	}
	return captured, nil
}

func ensureFileZillaKnownHost(host string, port int, key ssh.PublicKey) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cleanHost := strings.TrimSpace(host)
	addrWithPort := net.JoinHostPort(cleanHost, strconv.Itoa(port))
	pattern := addrWithPort
	if port != 22 {
		pattern = "[" + cleanHost + "]:" + strconv.Itoa(port)
	}
	line := knownhosts.Line([]string{pattern}, key)

	paths := []string{
		filepath.Join(home, ".config", "filezilla", "known_hosts"),
		filepath.Join(home, ".filezilla", "known_hosts"),
		filepath.Join(home, "Library", "Application Support", "FileZilla", "known_hosts"),
		filepath.Join(home, "Library", "Preferences", "FileZilla", "known_hosts"),
	}
	var firstErr error
	for _, p := range paths {
		if err := upsertKnownHostLine(p, pattern, line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return nil
	}
	if firstErr != nil {
		return firstErr
	}
	return fmt.Errorf("failed to write FileZilla known_hosts")
}

func upsertKnownHostLine(path, pattern, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing := ""
	if blob, err := os.ReadFile(path); err == nil {
		existing = string(blob)
	} else if !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(existing, "\n")
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, current := range lines {
		trimmed := strings.TrimSpace(current)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, pattern+" ") {
			if !replaced {
				out = append(out, line)
				replaced = true
			}
			continue
		}
		out = append(out, trimmed)
	}
	if !replaced {
		out = append(out, line)
	}
	content := strings.Join(out, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

func winSCPHostKeyParam(key ssh.PublicKey) string {
	if key == nil {
		return ""
	}
	alg := strings.TrimSpace(key.Type())
	bits := publicKeyBits(key)
	md5fp := strings.TrimPrefix(ssh.FingerprintLegacyMD5(key), "MD5:")
	if bits <= 0 || alg == "" || md5fp == "" {
		return ""
	}
	return fmt.Sprintf("%s %d %s", alg, bits, md5fp)
}

func publicKeyBits(key ssh.PublicKey) int {
	cryptoKey, ok := key.(ssh.CryptoPublicKey)
	if !ok {
		return 0
	}
	switch k := cryptoKey.CryptoPublicKey().(type) {
	case *rsa.PublicKey:
		return k.N.BitLen()
	case *ecdsa.PublicKey:
		return k.Params().BitSize
	case ed25519.PublicKey:
		return len(k) * 8
	default:
		return 0
	}
}
