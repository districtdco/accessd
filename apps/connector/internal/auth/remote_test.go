package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteVerifier_TLSUnknownAuthorityByDefault(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"valid": true,
			"claims": ConnectorClaims{
				SessionID: "session-1",
			},
		})
	}))
	defer server.Close()

	verifier := NewRemoteVerifier(server.URL, 2*time.Second, RemoteVerifierOptions{})
	_, err := verifier.Verify("token")
	if err == nil {
		t.Fatalf("expected TLS verification failure for unknown authority")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("expected certificate verification error, got %v", err)
	}
}

func TestRemoteVerifier_AcceptsCustomCACertFile_DER(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"valid": true,
			"claims": ConnectorClaims{
				SessionID: "session-1",
				UserID:    "user-1",
			},
		})
	}))
	defer server.Close()

	if server.Certificate() == nil {
		t.Fatalf("expected tls server certificate")
	}

	certFile := filepath.Join(t.TempDir(), "accessd-test.cer")
	if err := os.WriteFile(certFile, server.Certificate().Raw, 0o644); err != nil {
		t.Fatalf("write cert file: %v", err)
	}

	verifier := NewRemoteVerifier(server.URL, 2*time.Second, RemoteVerifierOptions{
		CACertFile: certFile,
	})
	claims, err := verifier.Verify("token")
	if err != nil {
		t.Fatalf("expected verify success with custom CA cert, got %v", err)
	}
	if claims.SessionID != "session-1" {
		t.Fatalf("expected session-1 claims, got %q", claims.SessionID)
	}
}

func TestRemoteVerifier_InvalidCACertFileReported(t *testing.T) {
	verifier := NewRemoteVerifier("https://accessd.example.internal/api/connector/token/verify", 2*time.Second, RemoteVerifierOptions{
		CACertFile: filepath.Join(t.TempDir(), "missing.cer"),
	})
	_, err := verifier.Verify("token")
	if err == nil {
		t.Fatalf("expected verify error for unreadable CA cert file")
	}
	if !strings.Contains(err.Error(), "read backend ca cert file") {
		t.Fatalf("expected backend CA cert read error, got %v", err)
	}
}
