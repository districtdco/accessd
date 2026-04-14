// Package config manages connector runtime configuration.
package config

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/districtd/pam/connector/internal/discovery"
)

const (
	defaultAddr           = "127.0.0.1:9494"
	defaultAllowedOrigins = "http://127.0.0.1:3000,http://localhost:3000,http://127.0.0.1:5173,http://localhost:5173,https://127.0.0.1:5173,https://localhost:5173"
	defaultDBeaverTempTTL = 15 * time.Minute
)

type Config struct {
	Addr                  string
	EnableTLS             bool
	TLSCertFile           string
	TLSKeyFile            string
	AllowedOrigins        []string
	AllowAnyOrigin        bool
	AllowRemote           bool
	AllowInsecureNoToken  bool
	ConnectorSecret       string // Shared HMAC secret for verifying backend-signed launch payloads
	BackendVerifyURL      string // API endpoint for online connector-token verification
	BackendVerifyTimeout  time.Duration
	BackendCACertFile     string
	BackendVerifyInsecure bool
	DBeaverTempTTL        time.Duration

	// Resolver handles cross-platform application discovery with strict
	// priority: ENV → config file → auto-detect → actionable error.
	Resolver *discovery.Resolver
}

func Load() Config {
	if path, created, err := discovery.EnsureDefaultConfigFile(); err != nil {
		log.Printf("WARNING: failed to prepare default discovery config file: %v", err)
	} else if created {
		log.Printf("discovery: created default config file at %s", path)
	}

	cfg := Config{
		Addr:                  strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_ADDR")),
		EnableTLS:             parseBoolEnv("ACCESSD_CONNECTOR_ENABLE_TLS", true),
		TLSCertFile:           strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_TLS_CERT_FILE")),
		TLSKeyFile:            strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_TLS_KEY_FILE")),
		AllowAnyOrigin:        parseBoolEnv("ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN", false),
		AllowRemote:           parseBoolEnv("ACCESSD_CONNECTOR_ALLOW_REMOTE", false),
		AllowInsecureNoToken:  parseBoolEnv("ACCESSD_CONNECTOR_ALLOW_INSECURE_NO_TOKEN", false),
		ConnectorSecret:       strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_SECRET")),
		BackendVerifyURL:      strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_BACKEND_VERIFY_URL")),
		BackendVerifyTimeout:  parseDurationEnv("ACCESSD_CONNECTOR_BACKEND_VERIFY_TIMEOUT", 5*time.Second),
		BackendCACertFile:     strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_BACKEND_CA_CERT_FILE")),
		BackendVerifyInsecure: parseBoolEnv("ACCESSD_CONNECTOR_BACKEND_VERIFY_INSECURE", false),
		DBeaverTempTTL:        parseDurationEnv("ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL", defaultDBeaverTempTTL),
	}
	rawOrigins := strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_ALLOWED_ORIGIN"))
	if rawOrigins == "" {
		rawOrigins = defaultAllowedOrigins
	}
	cfg.AllowedOrigins = parseCSV(rawOrigins)
	if hasPlaceholderOrigin(cfg.AllowedOrigins) {
		// Placeholder origin is a bootstrap default; allow runtime origin checks
		// to proceed instead of hard-failing CORS on real deployments.
		cfg.AllowAnyOrigin = true
	}
	if cfg.BackendVerifyURL == "" {
		cfg.BackendVerifyURL = deriveDefaultBackendVerifyURL(cfg.AllowedOrigins)
	}
	if cfg.BackendCACertFile == "" {
		cfg.BackendCACertFile = deriveDefaultBackendCACertFile(cfg.BackendVerifyURL)
	}
	if backendOrigin := deriveOriginFromURL(cfg.BackendVerifyURL); backendOrigin != "" {
		cfg.AllowedOrigins = appendUniqueOrigin(cfg.AllowedOrigins, backendOrigin)
	}

	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if cfg.TLSCertFile == "" {
				cfg.TLSCertFile = home + "/.accessd-connector/tls/localhost.crt"
			}
			if cfg.TLSKeyFile == "" {
				cfg.TLSKeyFile = home + "/.accessd-connector/tls/localhost.key"
			}
		}
	}
	if cfg.DBeaverTempTTL <= 0 {
		cfg.DBeaverTempTTL = defaultDBeaverTempTTL
	}

	// Load optional discovery config file for app path and terminal overrides.
	discoveryCfg, err := discovery.LoadConfig()
	if err != nil {
		log.Printf("WARNING: failed to load discovery config: %v", err)
	}
	cfg.Resolver = discovery.NewResolver(discoveryCfg)

	return cfg
}

func deriveDefaultBackendVerifyURL(origins []string) string {
	for _, origin := range origins {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || parsed == nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		if host == "" {
			continue
		}
		if isUnusableBackendVerifyHost(host) {
			continue
		}
		base := strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
		if parsed.Scheme == "" {
			continue
		}
		return base + "/api/connector/token/verify"
	}
	return ""
}

func isUnusableBackendVerifyHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return true
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if host == "accessd.example.internal" || strings.HasSuffix(host, ".example.internal") {
		return true
	}
	return false
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func hasPlaceholderOrigin(origins []string) bool {
	for _, origin := range origins {
		trimmed := strings.TrimSpace(strings.ToLower(origin))
		if trimmed == "https://accessd.example.internal" || trimmed == "http://accessd.example.internal" {
			return true
		}
	}
	return false
}

func deriveOriginFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.TrimSpace(parsed.Host)
	if host == "" {
		return ""
	}
	return strings.TrimRight(scheme+"://"+host, "/")
}

func appendUniqueOrigin(origins []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return origins
	}
	for _, existing := range origins {
		if strings.EqualFold(strings.TrimSpace(existing), candidate) {
			return origins
		}
	}
	return append(origins, candidate)
}

func deriveDefaultBackendCACertFile(verifyURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(verifyURL))
	if err != nil || parsed == nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	base := filepath.Join(home, ".accessd-connector", "certs")
	for _, candidate := range []string{
		filepath.Join(base, "accessd-"+host+".cer"),
		filepath.Join(base, "accessd-"+host+".crt"),
	} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	return ""
}
