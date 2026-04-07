package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/districtd/pam/api/internal/requestctx"
)

func TestWithRequestLogging_GeneratesRequestID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var seen string
	handler := withRequestLogging(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	gotHeader := rec.Header().Get("X-Request-Id")
	if gotHeader == "" {
		t.Fatalf("missing X-Request-Id header")
	}
	if seen == "" {
		t.Fatalf("request id missing from context")
	}
	if seen != gotHeader {
		t.Fatalf("context request id %q does not match header %q", seen, gotHeader)
	}
}

func TestWithRequestLogging_UsesIncomingRequestID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	const incoming = "req-12345"
	var seen string
	handler := withRequestLogging(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestctx.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("X-Request-Id", incoming)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seen != incoming {
		t.Fatalf("context request id = %q, want %q", seen, incoming)
	}
	if rec.Header().Get("X-Request-Id") != incoming {
		t.Fatalf("response request id = %q, want %q", rec.Header().Get("X-Request-Id"), incoming)
	}
}

func TestWithCORS_AllowedOrigin(t *testing.T) {
	handler := withCORS([]string{"http://localhost:3000"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin header = %q, want %q", got, "http://localhost:3000")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow-credentials header = %q, want true", got)
	}
}

func TestWithCORS_Preflight(t *testing.T) {
	handler := withCORS([]string{"http://localhost:3000"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/sessions/my", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("missing Access-Control-Allow-Methods header")
	}
}
