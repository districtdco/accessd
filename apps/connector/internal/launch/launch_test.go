package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeCommandArg_DBeaverPassword(t *testing.T) {
	arg := "driver=sqlserver|host=db.local|user=sa|password=supersecret|database=master"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "password=<redacted>") {
		t.Fatalf("expected redacted password marker, got %q", got)
	}
}

func TestSanitizeCommandArg_SFTPURLPassword(t *testing.T) {
	arg := "sftp://alice:ultrasecret@example.com:22/home/alice"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "ultrasecret") {
		t.Fatalf("expected sftp password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "alice:<redacted>@") {
		t.Fatalf("expected redacted sftp password marker, got %q", got)
	}
}

func TestResolveBinaryPath_OverrideMissing(t *testing.T) {
	_, fromOverride, err := resolveBinaryPath("/tmp/does-not-exist-pam-launcher", nil)
	if err == nil {
		t.Fatalf("expected error for missing override path")
	}
	if !fromOverride {
		t.Fatalf("expected fromOverride=true for explicit override path")
	}
}

func TestResolveBinaryPath_FallbackPath(t *testing.T) {
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "pam-test-bin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}

	got, fromOverride, err := resolveBinaryPath("", []string{"/tmp/definitely-missing", binPath})
	if err != nil {
		t.Fatalf("expected fallback resolution to succeed: %v", err)
	}
	if fromOverride {
		t.Fatalf("expected fromOverride=false when using fallback search")
	}
	if got != binPath {
		t.Fatalf("expected resolved path %q, got %q", binPath, got)
	}
}

func TestPrepareSSHAskpassFiles(t *testing.T) {
	askpassPath, tokenPath, err := prepareSSHAskpassFiles("super-secret-token")
	if err != nil {
		t.Fatalf("prepare askpass files: %v", err)
	}
	defer os.Remove(askpassPath)
	defer os.Remove(tokenPath)

	tokenBlob, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if !strings.Contains(string(tokenBlob), "super-secret-token") {
		t.Fatalf("token file should contain token")
	}

	askpassBlob, err := os.ReadFile(askpassPath)
	if err != nil {
		t.Fatalf("read askpass file: %v", err)
	}
	if strings.Contains(string(askpassBlob), "super-secret-token") {
		t.Fatalf("askpass helper must not embed token directly")
	}
	if !strings.Contains(string(askpassBlob), tokenPath) {
		t.Fatalf("askpass helper should read token file path")
	}
}

func TestShellCommand_DoesNotLeakToken(t *testing.T) {
	req := Request{
		SessionID: "session-1",
		AssetName: "db1",
		Launch: Shell{
			ProxyHost: "127.0.0.1",
			ProxyPort: 2222,
			Username:  "pam",
			Token:     "very-secret-launch-token",
		},
	}
	command, err := shellCommand(req)
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	if strings.Contains(command, req.Launch.Token) {
		t.Fatalf("shell command must not contain raw token")
	}
}

func TestSanitizeCommandArgs_PuttyPasswordRedacted(t *testing.T) {
	args := []string{"putty", "-ssh", "pam@127.0.0.1", "-P", "2222", "-pw", "supersecret"}
	got := sanitizeCommandArgs(args)
	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected putty password to be redacted, got %q", got)
	}
}
