package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/districtd/pam/connector/internal/auth"
	"github.com/districtd/pam/connector/internal/config"
	"github.com/districtd/pam/connector/internal/launch"
)

var (
	version = "0.1.0-dev"
	commit  = "dev"
	builtAt = "unknown"
)

func main() {
	if len(os.Args) > 1 && strings.EqualFold(strings.TrimSpace(os.Args[1]), "bridge-shell") {
		if err := launch.RunShellBridgeCommand(context.Background(), os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "AccessD shell launch failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.Load()
	localVerifier := auth.NewVerifier(cfg.ConnectorSecret)
	remoteVerifier := auth.NewRemoteVerifier(cfg.BackendVerifyURL, cfg.BackendVerifyTimeout)
	var verifier connectorTokenVerifier
	switch {
	case localVerifier != nil:
		verifier = localVerifier
		log.Printf("connector token verification enabled (mode=local-hmac)")
	case remoteVerifier != nil:
		verifier = remoteVerifier
		log.Printf("connector token verification enabled (mode=backend-online verify_url=%s)", cfg.BackendVerifyURL)
	}
	if verifier == nil && !cfg.AllowInsecureNoToken {
		log.Fatal("connector token verification requires ACCESSD_CONNECTOR_SECRET or ACCESSD_CONNECTOR_BACKEND_VERIFY_URL (set ACCESSD_CONNECTOR_ALLOW_INSECURE_NO_TOKEN=true only for temporary local development)")
	}
	if verifier == nil {
		log.Printf("WARNING: connector token verification disabled by ACCESSD_CONNECTOR_ALLOW_INSECURE_NO_TOKEN=true")
	}
	launcher := launch.Launcher{
		DBeaverTempTTL: cfg.DBeaverTempTTL,
		Resolver:       cfg.Resolver,
	}
	if removed, err := launch.CleanupStaleDBeaverTemp(cfg.DBeaverTempTTL); err != nil {
		log.Printf("stale dbeaver temp cleanup skipped: %v", err)
	} else if removed > 0 {
		log.Printf("stale dbeaver temp cleanup removed %d directories", removed)
	}
	if cfg.AllowRemote {
		log.Printf("WARNING: ACCESSD_CONNECTOR_ALLOW_REMOTE=true exposes connector launch endpoints beyond localhost")
	}
	if cfg.AllowAnyOrigin {
		log.Printf("WARNING: ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN=true allows any browser origin to call connector endpoints")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
		})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service":  "accessd-connector",
			"version":  version,
			"commit":   commit,
			"built_at": builtAt,
		})
	})
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, _ *http.Request) {
		missing := make([]string, 0, 1)
		if verifier == nil && !cfg.AllowInsecureNoToken {
			missing = append(missing, "ACCESSD_CONNECTOR_SECRET or ACCESSD_CONNECTOR_BACKEND_VERIFY_URL")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service":  "accessd-connector",
			"version":  version,
			"commit":   commit,
			"built_at": builtAt,
			"runtime": map[string]any{
				"addr":                 cfg.Addr,
				"allow_remote":         cfg.AllowRemote,
				"allow_any_origin":     cfg.AllowAnyOrigin,
				"allow_insecure_token": cfg.AllowInsecureNoToken,
				"allowed_origins":      cfg.AllowedOrigins,
				"backend_verify_url":   cfg.BackendVerifyURL,
			},
			"requirements": map[string]any{
				"ok":                       len(missing) == 0,
				"missing_env":              missing,
				"connector_secret_present": strings.TrimSpace(cfg.ConnectorSecret) != "",
				"backend_verify_url_set":   strings.TrimSpace(cfg.BackendVerifyURL) != "",
				"notes": []string{
					"Connector runtime settings are operator-machine specific.",
					"Set ACCESSD_CONNECTOR_ALLOWED_ORIGIN to the UI origin.",
				},
			},
		})
	})

	mux.HandleFunc("POST /launch/shell", func(w http.ResponseWriter, r *http.Request) {
		var req launch.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		if err := req.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := verifyConnectorToken(verifier, req.ConnectorToken, req.SessionID); err != nil {
			log.Printf("launch/shell rejected: %v session_id=%s", err, req.SessionID)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing connector token"})
			return
		}
		log.Printf("launch/shell accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchShell(r.Context(), req)
		if err != nil {
			writeLaunchError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":       "launched",
			"session_id":   req.SessionID,
			"instructions": "shell launched with automatic token authentication",
			"diagnostics":  diag,
		})
	})
	mux.HandleFunc("POST /launch/dbeaver", func(w http.ResponseWriter, r *http.Request) {
		var req launch.DBeaverRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		if err := req.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := verifyConnectorToken(verifier, req.ConnectorToken, req.SessionID); err != nil {
			log.Printf("launch/dbeaver rejected: %v session_id=%s", err, req.SessionID)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing connector token"})
			return
		}
		log.Printf("launch/dbeaver accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchDBeaver(r.Context(), req)
		if err != nil {
			writeLaunchError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":       "launched",
			"session_id":   req.SessionID,
			"instructions": "DBeaver launch requested; local temp launch metadata will be cleaned automatically",
			"diagnostics":  diag,
		})
	})
	mux.HandleFunc("POST /launch/redis", func(w http.ResponseWriter, r *http.Request) {
		var req launch.RedisRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		if err := req.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := verifyConnectorToken(verifier, req.ConnectorToken, req.SessionID); err != nil {
			log.Printf("launch/redis rejected: %v session_id=%s", err, req.SessionID)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing connector token"})
			return
		}
		log.Printf("launch/redis accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchRedisCLI(r.Context(), req)
		if err != nil {
			writeLaunchError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":       "launched",
			"session_id":   req.SessionID,
			"instructions": "redis-cli launched in a local terminal through managed connector flow",
			"diagnostics":  diag,
		})
	})
	mux.HandleFunc("POST /launch/sftp", func(w http.ResponseWriter, r *http.Request) {
		var req launch.SFTPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		if err := req.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := verifyConnectorToken(verifier, req.ConnectorToken, req.SessionID); err != nil {
			log.Printf("launch/sftp rejected: %v session_id=%s", err, req.SessionID)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing connector token"})
			return
		}
		log.Printf("launch/sftp accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchSFTPClient(r.Context(), req)
		if err != nil {
			writeLaunchError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":       "launched",
			"session_id":   req.SessionID,
			"instructions": "SFTP client launch requested through managed connector flow",
			"diagnostics":  diag,
		})
	})

	handler := withCORS(cfg.AllowedOrigins, cfg.AllowAnyOrigin, mux)
	handler = withLocalhostOnly(cfg.AllowRemote, handler)
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           withLogging(handler),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("accessd-connector listening on %s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		log.Printf("%s %s status=%d duration=%s", r.Method, r.URL.Path, rr.status, time.Since(start))
	})
}

func withCORS(allowedOrigins []string, allowAnyOrigin bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestOrigin := strings.TrimSpace(r.Header.Get("Origin"))
		allowOrigin := ""
		if allowAnyOrigin {
			allowOrigin = "*"
		} else if isLoopbackOrigin(requestOrigin) {
			// Always allow loopback browser origins (localhost/127.0.0.1).
			// Connector is already loopback-bound by default, so this keeps
			// local dev ports (e.g. :5173) and local HTTPS origins seamless.
			allowOrigin = requestOrigin
		} else if requestOrigin != "" {
			for _, allowed := range allowedOrigins {
				if requestOrigin == allowed {
					allowOrigin = requestOrigin
					break
				}
			}
		}
		if allowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if requestOrigin != "" && !allowAnyOrigin {
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if requestOrigin != "" && allowOrigin == "" {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func withLocalhostOnly(allowRemote bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowRemote {
			next.ServeHTTP(w, r)
			return
		}
		if !isLoopbackRequest(r.RemoteAddr) {
			http.Error(w, "connector only accepts local requests; set ACCESSD_CONNECTOR_ALLOW_REMOTE=true to override", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRequest(remoteAddr string) bool {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return false
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		trimmed = host
	}
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		fmt.Printf("failed to encode json: %v\n", err)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

type connectorTokenVerifier interface {
	Verify(token string) (auth.ConnectorClaims, error)
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// verifyConnectorToken checks the connector token if a verifier is configured.
// Verifier can be local-HMAC or backend-online. When verifier is nil, verification
// is skipped (explicit insecure mode only).
func verifyConnectorToken(v connectorTokenVerifier, token, sessionID string) error {
	if v == nil {
		return nil // verification disabled
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("missing connector_token")
	}
	claims, err := v.Verify(token)
	if err != nil {
		return err
	}
	if claims.SessionID != sessionID {
		return fmt.Errorf("connector token session_id mismatch")
	}
	return nil
}

func writeLaunchError(w http.ResponseWriter, err error) {
	var launchErr *launch.LaunchError
	if errors.As(err, &launchErr) {
		body := map[string]string{
			"error": launchErr.Message,
			"code":  launchErr.Code,
			"hint":  launchErr.Hint,
		}
		if strings.TrimSpace(launchErr.Details) != "" {
			body["details"] = launchErr.Details
		}
		writeJSON(w, http.StatusInternalServerError, body)
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
