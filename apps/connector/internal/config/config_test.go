package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveDefaultBackendVerifyURL_SkipsPlaceholderAndLoopback(t *testing.T) {
	got := deriveDefaultBackendVerifyURL([]string{
		"https://accessd.example.internal",
		"http://127.0.0.1:5173",
		"https://localhost:5173",
	})
	if got != "" {
		t.Fatalf("expected empty backend verify url for placeholder/loopback origins, got %q", got)
	}
}

func TestDeriveDefaultBackendVerifyURL_UsesFirstValidOrigin(t *testing.T) {
	got := deriveDefaultBackendVerifyURL([]string{
		"https://127.0.0.1:5173",
		"https://accessd.acme.internal",
	})
	want := "https://accessd.acme.internal/api/connector/token/verify"
	if got != want {
		t.Fatalf("deriveDefaultBackendVerifyURL() = %q, want %q", got, want)
	}
}

func TestDeriveOriginFromURL(t *testing.T) {
	got := deriveOriginFromURL("https://accessd.example.internal/api/connector/token/verify")
	want := "https://accessd.example.internal"
	if got != want {
		t.Fatalf("deriveOriginFromURL() = %q, want %q", got, want)
	}
}

func TestAppendUniqueOrigin_DeduplicatesCaseInsensitive(t *testing.T) {
	got := appendUniqueOrigin([]string{"https://accessd.acme.internal"}, "HTTPS://ACCESSD.ACME.INTERNAL")
	if len(got) != 1 {
		t.Fatalf("expected deduplicated origin list, got %#v", got)
	}
}

func TestDeriveDefaultBackendCACertFile_FindsHostCert(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	certsDir := filepath.Join(home, ".accessd-connector", "certs")
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		t.Fatalf("mkdir certs dir: %v", err)
	}
	want := filepath.Join(certsDir, "accessd-accessd.example.internal.cer")
	if err := os.WriteFile(want, []byte("test-cert"), 0o644); err != nil {
		t.Fatalf("write cert file: %v", err)
	}

	got := deriveDefaultBackendCACertFile("https://accessd.example.internal/api/connector/token/verify")
	if got != want {
		t.Fatalf("deriveDefaultBackendCACertFile() = %q, want %q", got, want)
	}
}

func TestDeriveDefaultBackendCACertFile_SkipsLoopback(t *testing.T) {
	if got := deriveDefaultBackendCACertFile("https://127.0.0.1/api/connector/token/verify"); got != "" {
		t.Fatalf("expected empty CA cert file for loopback host, got %q", got)
	}
}
