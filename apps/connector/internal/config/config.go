// Package config manages connector runtime configuration.
package config

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/districtd/pam/connector/internal/discovery"
)

const (
	defaultAddr           = "127.0.0.1:9494"
	defaultAllowedOrigins = "http://127.0.0.1:3000,http://localhost:3000"
	defaultDBeaverTempTTL = 15 * time.Minute
)

type Config struct {
	Addr            string
	AllowedOrigins  []string
	AllowAnyOrigin  bool
	AllowRemote     bool
	ConnectorSecret string // Shared HMAC secret for verifying backend-signed launch payloads
	DBeaverTempTTL  time.Duration

	// Resolver handles cross-platform application discovery with strict
	// priority: ENV → config file → auto-detect → actionable error.
	Resolver *discovery.Resolver
}

func Load() Config {
	cfg := Config{
		Addr:            strings.TrimSpace(os.Getenv("PAM_CONNECTOR_ADDR")),
		AllowAnyOrigin:  parseBoolEnv("PAM_CONNECTOR_ALLOW_ANY_ORIGIN", false),
		AllowRemote:     parseBoolEnv("PAM_CONNECTOR_ALLOW_REMOTE", false),
		ConnectorSecret: strings.TrimSpace(os.Getenv("PAM_CONNECTOR_SECRET")),
		DBeaverTempTTL:  parseDurationEnv("PAM_CONNECTOR_DBEAVER_TEMP_TTL", defaultDBeaverTempTTL),
	}
	rawOrigins := strings.TrimSpace(os.Getenv("PAM_CONNECTOR_ALLOWED_ORIGIN"))
	if rawOrigins == "" {
		rawOrigins = defaultAllowedOrigins
	}
	cfg.AllowedOrigins = parseCSV(rawOrigins)

	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
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
