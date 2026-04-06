package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr           = ":8080"
	defaultShutdownTimeout    = 15 * time.Second
	defaultMigrationsTable    = "schema_migrations"
	defaultMigrationsFilePath = "migrations"
)

type Config struct {
	App  AppConfig
	Auth AuthConfig
	DB   DBConfig
}

type AppConfig struct {
	Name            string
	Env             string
	HTTPAddr        string
	ShutdownTimeout time.Duration
	Version         VersionInfo
	Migrations      MigrationConfig
}

type VersionInfo struct {
	Service string
	Version string
	Commit  string
	BuiltAt string
}

type MigrationConfig struct {
	Dir   string
	Table string
}

type DBConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type AuthConfig struct {
	SessionCookieName string
	SessionTTL        time.Duration
	SessionSecure     bool
	DevAdminUsername  string
	DevAdminPassword  string
	DevAdminEmail     string
	DevAdminName      string
}

func Load() (Config, error) {
	cfg := Config{}

	cfg.App.Name = getEnv("PAM_APP_NAME", "pam-api")
	cfg.App.Env = getEnv("PAM_ENV", "development")
	cfg.App.HTTPAddr = getEnv("PAM_HTTP_ADDR", defaultHTTPAddr)
	cfg.App.ShutdownTimeout = getDurationEnv("PAM_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	cfg.App.Migrations = MigrationConfig{
		Dir:   getEnv("PAM_MIGRATIONS_DIR", defaultMigrationsFilePath),
		Table: getEnv("PAM_MIGRATIONS_TABLE", defaultMigrationsTable),
	}
	cfg.App.Version = VersionInfo{
		Service: cfg.App.Name,
		Version: getEnv("PAM_VERSION", "0.1.0-dev"),
		Commit:  getEnv("PAM_COMMIT", "dev"),
		BuiltAt: getEnv("PAM_BUILT_AT", "unknown"),
	}
	cfg.Auth = AuthConfig{
		SessionCookieName: getEnv("PAM_AUTH_COOKIE_NAME", "pam_session"),
		SessionTTL:        getDurationEnv("PAM_AUTH_SESSION_TTL", 12*time.Hour),
		SessionSecure:     getBoolEnv("PAM_AUTH_COOKIE_SECURE", false),
		DevAdminUsername:  getEnv("PAM_DEV_ADMIN_USERNAME", "admin"),
		DevAdminPassword:  getEnv("PAM_DEV_ADMIN_PASSWORD", "admin123"),
		DevAdminEmail:     getEnv("PAM_DEV_ADMIN_EMAIL", "admin@pam.local"),
		DevAdminName:      getEnv("PAM_DEV_ADMIN_NAME", "PAM Administrator"),
	}

	cfg.DB.URL = strings.TrimSpace(os.Getenv("PAM_DB_URL"))
	cfg.DB.MaxConns = int32(getIntEnv("PAM_DB_MAX_CONNS", 10))
	cfg.DB.MinConns = int32(getIntEnv("PAM_DB_MIN_CONNS", 1))
	cfg.DB.MaxConnLifetime = getDurationEnv("PAM_DB_MAX_CONN_LIFETIME", time.Hour)
	cfg.DB.MaxConnIdleTime = getDurationEnv("PAM_DB_MAX_CONN_IDLE_TIME", 15*time.Minute)

	if cfg.DB.URL == "" {
		return Config{}, fmt.Errorf("PAM_DB_URL is required")
	}

	if cfg.DB.MinConns < 0 {
		return Config{}, fmt.Errorf("PAM_DB_MIN_CONNS must be >= 0")
	}

	if cfg.DB.MaxConns < 1 {
		return Config{}, fmt.Errorf("PAM_DB_MAX_CONNS must be >= 1")
	}

	if cfg.DB.MinConns > cfg.DB.MaxConns {
		return Config{}, fmt.Errorf("PAM_DB_MIN_CONNS cannot be greater than PAM_DB_MAX_CONNS")
	}

	if cfg.Auth.SessionTTL <= 0 {
		return Config{}, fmt.Errorf("PAM_AUTH_SESSION_TTL must be > 0")
	}

	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" {
		return Config{}, fmt.Errorf("PAM_AUTH_COOKIE_NAME cannot be empty")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminUsername) == "" {
		return Config{}, fmt.Errorf("PAM_DEV_ADMIN_USERNAME cannot be empty")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminPassword) == "" {
		return Config{}, fmt.Errorf("PAM_DEV_ADMIN_PASSWORD cannot be empty")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getIntEnv(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}

	return parsed
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}

	return parsed
}

func getBoolEnv(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
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
