package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/districtd/pam/connector/internal/auth"
	"github.com/districtd/pam/connector/internal/config"
	"github.com/districtd/pam/connector/internal/discovery"
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

	if len(os.Args) > 1 && strings.EqualFold(strings.TrimSpace(os.Args[1]), "ensure-local-tls") {
		certFile, keyFile := tlsPathsFromEnv()
		if err := ensureLocalTLSFiles(certFile, keyFile); err != nil {
			fmt.Fprintf(os.Stderr, "failed to prepare local TLS cert: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s\n", localTrustCertPath(certFile))
		return
	}

	cfg := config.Load()
	if len(os.Args) > 1 {
		arg := strings.TrimSpace(os.Args[1])
		if isProtocolAutostartArg(arg) {
			if connectorReachable(cfg) {
				return
			}
			if err := spawnBackgroundServe(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to start AccessD connector in background: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}
	localVerifier := auth.NewVerifier(cfg.ConnectorSecret)
	remoteVerifier := auth.NewRemoteVerifier(cfg.BackendVerifyURL, cfg.BackendVerifyTimeout, auth.RemoteVerifierOptions{
		CACertFile:         cfg.BackendCACertFile,
		InsecureSkipVerify: cfg.BackendVerifyInsecure,
	})
	var verifier connectorTokenVerifier
	switch {
	case localVerifier != nil && remoteVerifier != nil:
		verifier = fallbackConnectorTokenVerifier{verifiers: []connectorTokenVerifier{localVerifier, remoteVerifier}}
		log.Printf("connector token verification enabled (mode=local-hmac+backend-fallback verify_url=%s)", cfg.BackendVerifyURL)
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
	launchTracker := newLaunchTracker(10 * time.Minute)
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
		diagnostics, issues := buildHealthDiagnostics(cfg, localVerifier, remoteVerifier)
		readiness := "ready"
		if len(issues) > 0 {
			readiness = "degraded"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"readiness": readiness,
			"issues":    issues,
			"checks":    diagnostics,
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
				"addr":                    cfg.Addr,
				"enable_tls":              cfg.EnableTLS,
				"tls_cert_file":           cfg.TLSCertFile,
				"allow_remote":            cfg.AllowRemote,
				"allow_any_origin":        cfg.AllowAnyOrigin,
				"allow_insecure_token":    cfg.AllowInsecureNoToken,
				"allowed_origins":         cfg.AllowedOrigins,
				"backend_verify_url":      cfg.BackendVerifyURL,
				"backend_ca_cert_file":    cfg.BackendCACertFile,
				"backend_verify_insecure": cfg.BackendVerifyInsecure,
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
			writeConnectorTokenError(w, err)
			return
		}
		launchKey := "shell:" + strings.TrimSpace(req.SessionID)
		if !launchTracker.TryStart(launchKey) {
			log.Printf("launch/shell duplicate ignored session_id=%s", req.SessionID)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":       "already_launched",
				"session_id":   req.SessionID,
				"instructions": "shell launch already in progress or completed for this session",
			})
			return
		}
		log.Printf("launch/shell accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchShell(r.Context(), req)
		if err != nil {
			launchTracker.FinishFailure(launchKey)
			writeLaunchError(w, err)
			return
		}
		launchTracker.FinishSuccess(launchKey)
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
			writeConnectorTokenError(w, err)
			return
		}
		launchKey := "dbeaver:" + strings.TrimSpace(req.SessionID)
		if !launchTracker.TryStart(launchKey) {
			log.Printf("launch/dbeaver duplicate ignored session_id=%s", req.SessionID)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":       "already_launched",
				"session_id":   req.SessionID,
				"instructions": "dbeaver launch already in progress or completed for this session",
			})
			return
		}
		log.Printf("launch/dbeaver accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchDBeaver(r.Context(), req)
		if err != nil {
			launchTracker.FinishFailure(launchKey)
			writeLaunchError(w, err)
			return
		}
		launchTracker.FinishSuccess(launchKey)
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
			writeConnectorTokenError(w, err)
			return
		}
		launchKey := "redis:" + strings.TrimSpace(req.SessionID)
		if !launchTracker.TryStart(launchKey) {
			log.Printf("launch/redis duplicate ignored session_id=%s", req.SessionID)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":       "already_launched",
				"session_id":   req.SessionID,
				"instructions": "redis launch already in progress or completed for this session",
			})
			return
		}
		log.Printf("launch/redis accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchRedisCLI(r.Context(), req)
		if err != nil {
			launchTracker.FinishFailure(launchKey)
			writeLaunchError(w, err)
			return
		}
		launchTracker.FinishSuccess(launchKey)
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
			writeConnectorTokenError(w, err)
			return
		}
		launchKey := "sftp:" + strings.TrimSpace(req.SessionID)
		if !launchTracker.TryStart(launchKey) {
			log.Printf("launch/sftp duplicate ignored session_id=%s", req.SessionID)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":       "already_launched",
				"session_id":   req.SessionID,
				"instructions": "sftp launch already in progress or completed for this session",
			})
			return
		}
		log.Printf("launch/sftp accepted session_id=%s", req.SessionID)
		diag, err := launcher.LaunchSFTPClient(r.Context(), req)
		if err != nil {
			launchTracker.FinishFailure(launchKey)
			writeLaunchError(w, err)
			return
		}
		launchTracker.FinishSuccess(launchKey)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":       "launched",
			"session_id":   req.SessionID,
			"instructions": "SFTP client launch requested through managed connector flow",
			"diagnostics":  diag,
		})
	})

	handler := withCORS(cfg.AllowedOrigins, cfg.AllowAnyOrigin, mux)
	handler = withLocalhostOnly(cfg.AllowRemote, handler)
	handler = withRecovery(handler)
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           withLogging(handler),
		ReadHeaderTimeout: 5 * time.Second,
		// Keep loopback connector transport on HTTP/1.1 for maximum browser
		// compatibility with local/self-signed TLS interception environments.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}

	if cfg.EnableTLS {
		if err := ensureLocalTLSFiles(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
			log.Fatalf("prepare local tls cert: %v", err)
		}
		log.Printf("accessd-connector listening on https://%s", cfg.Addr)
		if err := server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
		return
	}

	log.Printf("accessd-connector listening on http://%s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func tlsPathsFromEnv() (certFile, keyFile string) {
	certFile = strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_TLS_CERT_FILE"))
	keyFile = strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_TLS_KEY_FILE"))
	if certFile != "" && keyFile != "" {
		return certFile, keyFile
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return certFile, keyFile
	}
	if certFile == "" {
		certFile = home + "/.accessd-connector/tls/localhost.crt"
	}
	if keyFile == "" {
		keyFile = home + "/.accessd-connector/tls/localhost.key"
	}
	return certFile, keyFile
}

func isProtocolAutostartArg(arg string) bool {
	if arg == "" {
		return false
	}
	trimmed := strings.TrimSpace(arg)
	if strings.EqualFold(trimmed, "start") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(trimmed), "accessd-connector://")
}

func connectorReachable(cfg config.Config) bool {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = "127.0.0.1:9494"
	}
	if cfg.EnableTLS {
		httpsClient := &http.Client{
			Timeout: 1200 * time.Millisecond,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // loopback readiness probe only
			},
		}
		if resp, err := httpsClient.Get("https://" + addr + "/version"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return true
			}
		}
	}
	httpClient := &http.Client{Timeout: 1200 * time.Millisecond}
	if resp, err := httpClient.Get("http://" + addr + "/version"); err == nil {
		_ = resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 500
	}
	return false
}

func spawnBackgroundServe() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, "serve")
	cmd.Env = os.Environ()
	return cmd.Start()
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		log.Printf("%s %s status=%d duration=%s", r.Method, r.URL.Path, rr.status, time.Since(start))
	})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered method=%s path=%s panic=%v stack=%s", r.Method, r.URL.Path, rec, string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "internal connector error",
					"code":  "internal_error",
					"hint":  "check connector logs for panic details",
				})
			}
		}()
		next.ServeHTTP(w, r)
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
				if originMatches(requestOrigin, allowed) {
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

func originMatches(requestOrigin, allowedOrigin string) bool {
	req, reqOK := canonicalOrigin(requestOrigin)
	allow, allowOK := canonicalOrigin(allowedOrigin)
	if reqOK && allowOK {
		return req == allow
	}
	return strings.TrimSpace(requestOrigin) == strings.TrimSpace(allowedOrigin)
}

func canonicalOrigin(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return "", false
	}
	port := strings.TrimSpace(u.Port())
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + host + ":" + port, true
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

func buildHealthDiagnostics(cfg config.Config, localVerifier *auth.Verifier, remoteVerifier *auth.RemoteVerifier) (map[string]any, []string) {
	issues := make([]string, 0, 8)
	mode := "disabled"
	switch {
	case localVerifier != nil && remoteVerifier != nil:
		mode = "local_hmac+backend_online"
	case localVerifier != nil:
		mode = "local_hmac"
	case remoteVerifier != nil:
		mode = "backend_online"
	}

	backendURL := strings.TrimSpace(cfg.BackendVerifyURL)
	backend := map[string]any{
		"url": backendURL,
	}
	if strings.TrimSpace(backendURL) != "" {
		host, resolveOK, resolveErr := resolveBackendVerifyHost(backendURL)
		backend["host"] = host
		backend["host_resolves"] = resolveOK
		if resolveErr != "" {
			backend["resolve_error"] = resolveErr
		}
		if isPlaceholderHost(host) {
			backend["placeholder"] = true
			issues = append(issues, "backend verify URL still points to placeholder host")
		}
		if !resolveOK {
			issues = append(issues, "backend verify host does not resolve")
		}
	}

	if localVerifier == nil && remoteVerifier == nil && !cfg.AllowInsecureNoToken {
		issues = append(issues, "connector token verification is not configured")
	}

	appChecks := map[string]any{
		"putty":     appHealth(cfg.Resolver, discovery.AppPuTTY, []string{"shell"}),
		"filezilla": appHealth(cfg.Resolver, discovery.AppFileZilla, []string{"sftp"}),
		"winscp":    appHealth(cfg.Resolver, discovery.AppWinSCP, []string{"sftp"}),
		"dbeaver":   appHealth(cfg.Resolver, discovery.AppDBeaver, []string{"dbeaver"}),
		"redis_cli": appHealth(cfg.Resolver, discovery.AppRedisCLI, []string{"redis"}),
	}
	if runtime.GOOS == "windows" {
		if putty, ok := appChecks["putty"].(map[string]any); ok {
			available, _ := putty["available"].(bool)
			if !available {
				issues = append(issues, "PuTTY not detected for Windows shell launch")
			}
		}
	}

	checks := map[string]any{
		"token_verification": map[string]any{
			"mode":                       mode,
			"allow_insecure_no_token":    cfg.AllowInsecureNoToken,
			"connector_secret_present":   localVerifier != nil,
			"backend_verify_url_present": strings.TrimSpace(backendURL) != "",
			"backend_ca_cert_file":       strings.TrimSpace(cfg.BackendCACertFile),
			"backend_verify_insecure":    cfg.BackendVerifyInsecure,
			"backend":                    backend,
		},
		"apps": appChecks,
	}
	if path := strings.TrimSpace(cfg.BackendCACertFile); path != "" {
		if _, err := os.Stat(path); err != nil {
			issues = append(issues, "backend CA cert file not readable")
		}
	}
	if cfg.BackendVerifyInsecure {
		issues = append(issues, "backend token verification TLS is running in insecure mode")
	}
	return checks, issues
}

func appHealth(resolver *discovery.Resolver, app discovery.AppName, requiredFor []string) map[string]any {
	result := map[string]any{
		"app":          string(app),
		"required_for": requiredFor,
		"available":    false,
	}
	if resolver == nil {
		result["error"] = "resolver unavailable"
		return result
	}
	resolution, err := resolver.ResolveApp(app)
	if err != nil {
		result["error"] = err.Error()
		var derr *discovery.DiscoveryError
		if errors.As(err, &derr) {
			if strings.TrimSpace(derr.Hint) != "" {
				result["hint"] = derr.Hint
			}
			if strings.TrimSpace(derr.Source) != "" {
				result["source"] = derr.Source
			}
		}
		return result
	}
	result["available"] = true
	result["path"] = resolution.Path
	result["source"] = resolution.Source
	return result
}

func resolveBackendVerifyHost(verifyURL string) (host string, ok bool, errText string) {
	parsed, err := url.Parse(strings.TrimSpace(verifyURL))
	if err != nil {
		return "", false, fmt.Sprintf("invalid verify URL: %v", err)
	}
	host = strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false, "missing host in verify URL"
	}
	if strings.EqualFold(host, "localhost") {
		return host, true, ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, true, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		return host, false, err.Error()
	}
	return host, true, ""
}

func isPlaceholderHost(host string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(host))
	return trimmed == "accessd.example.internal" || strings.HasSuffix(trimmed, ".example.internal")
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

type connectorTokenError struct {
	message string
	code    string
	hint    string
	details string
}

func (e *connectorTokenError) Error() string {
	if e == nil {
		return "invalid connector token"
	}
	return e.message
}

type fallbackConnectorTokenVerifier struct {
	verifiers []connectorTokenVerifier
}

type launchTracker struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]launchTrackerEntry
}

type launchTrackerEntry struct {
	startedAt  time.Time
	inProgress bool
	completed  bool
}

func newLaunchTracker(ttl time.Duration) *launchTracker {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &launchTracker{
		ttl:     ttl,
		entries: map[string]launchTrackerEntry{},
	}
}

func (t *launchTracker) TryStart(key string) bool {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	if existing, ok := t.entries[key]; ok {
		if existing.inProgress || existing.completed {
			return false
		}
	}
	t.entries[key] = launchTrackerEntry{
		startedAt:  now,
		inProgress: true,
		completed:  false,
	}
	return true
}

func (t *launchTracker) FinishSuccess(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[key]
	if !ok {
		return
	}
	entry.startedAt = time.Now()
	entry.inProgress = false
	entry.completed = true
	t.entries[key] = entry
}

func (t *launchTracker) FinishFailure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

func (t *launchTracker) pruneLocked(now time.Time) {
	for key, entry := range t.entries {
		if now.Sub(entry.startedAt) > t.ttl {
			delete(t.entries, key)
		}
	}
}

func (f fallbackConnectorTokenVerifier) Verify(token string) (auth.ConnectorClaims, error) {
	var errs []string
	for _, verifier := range f.verifiers {
		if verifier == nil {
			continue
		}
		claims, err := verifier.Verify(token)
		if err == nil {
			return claims, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return auth.ConnectorClaims{}, fmt.Errorf("connector token verification is not configured")
	}
	return auth.ConnectorClaims{}, fmt.Errorf("connector token verification failed: %s", strings.Join(errs, "; "))
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
		return &connectorTokenError{
			message: "missing connector token",
			code:    "connector_token_missing",
			hint:    "ensure connector_token from /sessions/launch is forwarded unchanged to the connector",
			details: "launch request did not include connector_token",
		}
	}
	claims, err := v.Verify(token)
	if err != nil {
		return classifyConnectorTokenVerifyError(err)
	}
	if strings.TrimSpace(claims.SessionID) != strings.TrimSpace(sessionID) {
		return &connectorTokenError{
			message: "connector token does not match requested session",
			code:    "connector_token_session_mismatch",
			hint:    "request a fresh launch and ensure session_id and connector_token belong to the same launch response",
			details: fmt.Sprintf("token sid=%q request sid=%q", strings.TrimSpace(claims.SessionID), strings.TrimSpace(sessionID)),
		}
	}
	return nil
}

func classifyConnectorTokenVerifyError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "expired"):
		return &connectorTokenError{
			message: "connector token expired",
			code:    "connector_token_expired",
			hint:    "start a new launch from AccessD and retry immediately",
			details: msg,
		}
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "malformed"):
		return &connectorTokenError{
			message: "connector token verification failed",
			code:    "connector_token_invalid",
			hint:    "verify ACCESSD_CONNECTOR_SECRET matches backend ACCESSD_CONNECTOR_SECRET, or set ACCESSD_CONNECTOR_BACKEND_VERIFY_URL",
			details: msg,
		}
	default:
		return &connectorTokenError{
			message: "connector token verification unavailable",
			code:    "connector_token_verify_unavailable",
			hint:    "check connector backend verification connectivity and local secret configuration",
			details: msg,
		}
	}
}

func writeConnectorTokenError(w http.ResponseWriter, err error) {
	var tokenErr *connectorTokenError
	if errors.As(err, &tokenErr) {
		body := map[string]string{
			"error": tokenErr.message,
			"code":  tokenErr.code,
			"hint":  tokenErr.hint,
		}
		if strings.TrimSpace(tokenErr.details) != "" {
			body["details"] = tokenErr.details
		}
		writeJSON(w, http.StatusForbidden, body)
		return
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing connector token"})
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
