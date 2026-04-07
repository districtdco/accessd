package config

import (
	"encoding/base64"
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
	App         AppConfig
	Auth        AuthConfig
	Credentials CredentialsConfig
	Sessions    SessionsConfig
	SSHProxy    SSHProxyConfig
	PGProxy     PGProxyConfig
	MySQLProxy  MySQLProxyConfig
	MSSQLProxy  MSSQLProxyConfig
	RedisProxy  RedisProxyConfig
	DB          DBConfig
}

type AppConfig struct {
	Name               string
	Env                string
	HTTPAddr           string
	CORSAllowedOrigins []string
	ShutdownTimeout    time.Duration
	AllowUnsafeMode    bool
	Version            VersionInfo
	Migrations         MigrationConfig
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
	SessionSameSite   string
	DevAdminUsername  string
	DevAdminPassword  string
	DevAdminEmail     string
	DevAdminName      string
	ProviderMode      string
	LDAP              LDAPConfig
}

type LDAPConfig struct {
	URL                  string
	Host                 string
	Port                 int
	BaseDN               string
	BindDN               string
	BindPassword         string
	UserSearchFilter     string
	UsernameAttribute    string
	DisplayNameAttribute string
	EmailAttribute       string
	UseTLS               bool
	StartTLS             bool
	InsecureSkipVerify   bool
	GroupSearchBaseDN    string
	GroupSearchFilter    string
	GroupNameAttribute   string
	GroupRoleMappingRaw  string
}

type CredentialsConfig struct {
	MasterKey string
	KeyID     string
}

type SessionsConfig struct {
	LaunchTokenSecret string
	LaunchTokenTTL    time.Duration
	ConnectorSecret   string // HMAC key for signing connector launch payloads
}

type SSHProxyConfig struct {
	ListenAddr             string
	PublicHost             string
	PublicPort             int
	Username               string
	HostKeyPath            string
	UpstreamHostKeyMode    string
	UpstreamKnownHostsPath string
	IdleTimeout            time.Duration
	MaxSessionAge          time.Duration
}

type PGProxyConfig struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	QueryLogQueue  int
	QueryMaxBytes  int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type MySQLProxyConfig struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	QueryLogQueue  int
	QueryMaxBytes  int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type MSSQLProxyConfig struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
	QueryLogQueue  int
	QueryMaxBytes  int
	IdleTimeout    time.Duration
	MaxSessionAge  time.Duration
}

type RedisProxyConfig struct {
	BindHost        string
	PublicHost      string
	ConnectTimeout  time.Duration
	CommandLogQueue int
	ArgMaxLen       int
	IdleTimeout     time.Duration
	MaxSessionAge   time.Duration
}

func Load() (Config, error) {
	if err := loadConfigFileIntoEnv(strings.TrimSpace(os.Getenv("PAM_CONFIG_FILE"))); err != nil {
		return Config{}, err
	}

	cfg := Config{}

	cfg.App.Name = getEnv("PAM_APP_NAME", "pam-api")
	cfg.App.Env = getEnv("PAM_ENV", "development")
	cfg.App.HTTPAddr = getEnv("PAM_HTTP_ADDR", defaultHTTPAddr)
	corsDefaults := ""
	if strings.ToLower(cfg.App.Env) == "development" {
		corsDefaults = "http://localhost:3000,http://127.0.0.1:3000"
	}
	cfg.App.CORSAllowedOrigins = splitCSV(getEnv("PAM_CORS_ALLOWED_ORIGINS", corsDefaults))
	cfg.App.ShutdownTimeout = getDurationEnv("PAM_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	cfg.App.AllowUnsafeMode = getBoolEnv("PAM_ALLOW_UNSAFE_MODE", false)
	sessionSecureDefault := strings.ToLower(cfg.App.Env) != "development"
	sessionSameSiteDefault := "lax"
	if sessionSecureDefault {
		sessionSameSiteDefault = "strict"
	}
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
		SessionSecure:     getBoolEnv("PAM_AUTH_COOKIE_SECURE", sessionSecureDefault),
		SessionSameSite:   strings.ToLower(getEnv("PAM_AUTH_COOKIE_SAMESITE", sessionSameSiteDefault)),
		DevAdminUsername:  getEnv("PAM_DEV_ADMIN_USERNAME", "admin"),
		DevAdminPassword:  getEnv("PAM_DEV_ADMIN_PASSWORD", "admin123"),
		DevAdminEmail:     getEnv("PAM_DEV_ADMIN_EMAIL", "admin@pam.local"),
		DevAdminName:      getEnv("PAM_DEV_ADMIN_NAME", "PAM Administrator"),
		ProviderMode:      strings.ToLower(getEnv("PAM_AUTH_PROVIDER_MODE", "local")),
		LDAP: LDAPConfig{
			URL:                  strings.TrimSpace(os.Getenv("PAM_LDAP_URL")),
			Host:                 getEnv("PAM_LDAP_HOST", "127.0.0.1"),
			Port:                 getIntEnv("PAM_LDAP_PORT", 389),
			BaseDN:               strings.TrimSpace(os.Getenv("PAM_LDAP_BASE_DN")),
			BindDN:               strings.TrimSpace(os.Getenv("PAM_LDAP_BIND_DN")),
			BindPassword:         strings.TrimSpace(os.Getenv("PAM_LDAP_BIND_PASSWORD")),
			UserSearchFilter:     getEnv("PAM_LDAP_USER_FILTER", "(&(objectClass=user)({{username_attr}}={{username}}))"),
			UsernameAttribute:    getEnv("PAM_LDAP_USERNAME_ATTR", "sAMAccountName"),
			DisplayNameAttribute: getEnv("PAM_LDAP_DISPLAY_NAME_ATTR", "displayName"),
			EmailAttribute:       getEnv("PAM_LDAP_EMAIL_ATTR", "mail"),
			UseTLS:               getBoolEnv("PAM_LDAP_USE_TLS", false),
			StartTLS:             getBoolEnv("PAM_LDAP_STARTTLS", false),
			InsecureSkipVerify:   getBoolEnv("PAM_LDAP_INSECURE_SKIP_VERIFY", false),
			GroupSearchBaseDN:    strings.TrimSpace(os.Getenv("PAM_LDAP_GROUP_BASE_DN")),
			GroupSearchFilter:    getEnv("PAM_LDAP_GROUP_FILTER", "(&(objectClass=group)(member={{user_dn}}))"),
			GroupNameAttribute:   getEnv("PAM_LDAP_GROUP_NAME_ATTR", "cn"),
			GroupRoleMappingRaw:  strings.TrimSpace(os.Getenv("PAM_LDAP_GROUP_ROLE_MAPPING")),
		},
	}
	cfg.Credentials = CredentialsConfig{
		MasterKey: strings.TrimSpace(os.Getenv("PAM_VAULT_KEY")),
		KeyID:     getEnv("PAM_VAULT_KEY_ID", "v1"),
	}
	cfg.Sessions = SessionsConfig{
		LaunchTokenSecret: strings.TrimSpace(os.Getenv("PAM_LAUNCH_TOKEN_SECRET")),
		LaunchTokenTTL:    getDurationEnv("PAM_LAUNCH_TOKEN_TTL", 2*time.Minute),
		ConnectorSecret:   strings.TrimSpace(os.Getenv("PAM_CONNECTOR_SECRET")),
	}
	cfg.SSHProxy = SSHProxyConfig{
		ListenAddr:             getEnv("PAM_SSH_PROXY_ADDR", ":2222"),
		PublicHost:             getEnv("PAM_SSH_PROXY_PUBLIC_HOST", "127.0.0.1"),
		PublicPort:             getIntEnv("PAM_SSH_PROXY_PUBLIC_PORT", 2222),
		Username:               getEnv("PAM_SSH_PROXY_USERNAME", "pam"),
		HostKeyPath:            getEnv("PAM_SSH_PROXY_HOST_KEY_PATH", ".pam_ssh_proxy_host_key"),
		UpstreamHostKeyMode:    getEnv("PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE", "known-hosts"),
		UpstreamKnownHostsPath: getEnv("PAM_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH", ".pam_upstream_known_hosts"),
		IdleTimeout:            getDurationEnv("PAM_SSH_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:          getDurationEnv("PAM_SSH_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.PGProxy = PGProxyConfig{
		BindHost:       getEnv("PAM_PG_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("PAM_PG_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("PAM_PG_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("PAM_PG_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("PAM_PG_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("PAM_PG_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("PAM_PG_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.MySQLProxy = MySQLProxyConfig{
		BindHost:       getEnv("PAM_MYSQL_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("PAM_MYSQL_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("PAM_MYSQL_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("PAM_MYSQL_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("PAM_MYSQL_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("PAM_MYSQL_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("PAM_MYSQL_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.MSSQLProxy = MSSQLProxyConfig{
		BindHost:       getEnv("PAM_MSSQL_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("PAM_MSSQL_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("PAM_MSSQL_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("PAM_MSSQL_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("PAM_MSSQL_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("PAM_MSSQL_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("PAM_MSSQL_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.RedisProxy = RedisProxyConfig{
		BindHost:        getEnv("PAM_REDIS_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:      getEnv("PAM_REDIS_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout:  getDurationEnv("PAM_REDIS_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		CommandLogQueue: getIntEnv("PAM_REDIS_PROXY_COMMAND_LOG_QUEUE", 1024),
		ArgMaxLen:       getIntEnv("PAM_REDIS_PROXY_ARG_MAX_LEN", 128),
		IdleTimeout:     getDurationEnv("PAM_REDIS_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:   getDurationEnv("PAM_REDIS_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
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
	switch cfg.Auth.SessionSameSite {
	case "lax", "strict", "none":
	default:
		return Config{}, fmt.Errorf("PAM_AUTH_COOKIE_SAMESITE must be one of: lax, strict, none")
	}
	if cfg.Auth.SessionSameSite == "none" && !cfg.Auth.SessionSecure {
		return Config{}, fmt.Errorf("PAM_AUTH_COOKIE_SAMESITE=none requires PAM_AUTH_COOKIE_SECURE=true")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminUsername) == "" {
		return Config{}, fmt.Errorf("PAM_DEV_ADMIN_USERNAME cannot be empty")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminPassword) == "" {
		return Config{}, fmt.Errorf("PAM_DEV_ADMIN_PASSWORD cannot be empty")
	}
	switch cfg.Auth.ProviderMode {
	case "local", "ldap", "hybrid":
	default:
		return Config{}, fmt.Errorf("PAM_AUTH_PROVIDER_MODE must be one of: local, ldap, hybrid")
	}
	if cfg.Auth.ProviderMode != "local" {
		if strings.TrimSpace(cfg.Auth.LDAP.BaseDN) == "" {
			return Config{}, fmt.Errorf("PAM_LDAP_BASE_DN is required when PAM_AUTH_PROVIDER_MODE is ldap or hybrid")
		}
		if strings.TrimSpace(cfg.Auth.LDAP.UsernameAttribute) == "" {
			return Config{}, fmt.Errorf("PAM_LDAP_USERNAME_ATTR cannot be empty")
		}
		if strings.TrimSpace(cfg.Auth.LDAP.UserSearchFilter) == "" {
			return Config{}, fmt.Errorf("PAM_LDAP_USER_FILTER cannot be empty")
		}
		if cfg.Auth.LDAP.Port <= 0 || cfg.Auth.LDAP.Port > 65535 {
			return Config{}, fmt.Errorf("PAM_LDAP_PORT must be between 1 and 65535")
		}
		if cfg.Auth.LDAP.UseTLS && cfg.Auth.LDAP.StartTLS {
			return Config{}, fmt.Errorf("PAM_LDAP_USE_TLS and PAM_LDAP_STARTTLS cannot both be true")
		}
	}
	if cfg.Credentials.MasterKey == "" {
		return Config{}, fmt.Errorf("PAM_VAULT_KEY is required")
	}
	if cfg.Sessions.LaunchTokenSecret == "" {
		return Config{}, fmt.Errorf("PAM_LAUNCH_TOKEN_SECRET is required")
	}
	if cfg.Sessions.LaunchTokenTTL <= 0 {
		return Config{}, fmt.Errorf("PAM_LAUNCH_TOKEN_TTL must be > 0")
	}
	if strings.TrimSpace(cfg.SSHProxy.ListenAddr) == "" {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_ADDR cannot be empty")
	}
	if strings.TrimSpace(cfg.SSHProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.SSHProxy.PublicPort <= 0 || cfg.SSHProxy.PublicPort > 65535 {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_PUBLIC_PORT must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.SSHProxy.Username) == "" {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_USERNAME cannot be empty")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SSHProxy.UpstreamHostKeyMode)) {
	case "accept-new", "known-hosts", "insecure":
	default:
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE must be one of: accept-new, known-hosts, insecure")
	}
	if strings.TrimSpace(cfg.SSHProxy.HostKeyPath) == "" {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_HOST_KEY_PATH cannot be empty")
	}
	if strings.TrimSpace(cfg.SSHProxy.UpstreamKnownHostsPath) == "" {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH cannot be empty")
	}
	if cfg.SSHProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.SSHProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("PAM_SSH_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.PGProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.PGProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.PGProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.PGProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.PGProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.PGProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.PGProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("PAM_PG_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.MySQLProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.MySQLProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.MySQLProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.MySQLProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.MySQLProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.MySQLProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.MySQLProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("PAM_MYSQL_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.MSSQLProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.MSSQLProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.MSSQLProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.MSSQLProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.MSSQLProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.MSSQLProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.MSSQLProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("PAM_MSSQL_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.RedisProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.RedisProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.RedisProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.RedisProxy.CommandLogQueue <= 0 {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_COMMAND_LOG_QUEUE must be > 0")
	}
	if cfg.RedisProxy.ArgMaxLen <= 0 {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_ARG_MAX_LEN must be > 0")
	}
	if cfg.RedisProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.RedisProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("PAM_REDIS_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.ToLower(strings.TrimSpace(cfg.App.Env)) != "development" && !cfg.App.AllowUnsafeMode {
		if !cfg.Auth.SessionSecure {
			return Config{}, fmt.Errorf("PAM_AUTH_COOKIE_SECURE must be true outside development (or set PAM_ALLOW_UNSAFE_MODE=true)")
		}
		if cfg.Auth.SessionSameSite == "none" {
			return Config{}, fmt.Errorf("PAM_AUTH_COOKIE_SAMESITE=none is blocked outside development unless PAM_ALLOW_UNSAFE_MODE=true")
		}
		if cfg.Auth.LDAP.InsecureSkipVerify {
			return Config{}, fmt.Errorf("PAM_LDAP_INSECURE_SKIP_VERIFY=true is blocked outside development unless PAM_ALLOW_UNSAFE_MODE=true")
		}
		switch strings.ToLower(strings.TrimSpace(cfg.SSHProxy.UpstreamHostKeyMode)) {
		case "insecure", "accept-new":
			return Config{}, fmt.Errorf("PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE=%s is blocked outside development unless PAM_ALLOW_UNSAFE_MODE=true", cfg.SSHProxy.UpstreamHostKeyMode)
		}
		if !looksLikeBase64Encoded32ByteKey(cfg.Credentials.MasterKey) {
			return Config{}, fmt.Errorf("PAM_VAULT_KEY must be base64-encoded 32 bytes outside development (or set PAM_ALLOW_UNSAFE_MODE=true)")
		}
	}

	return cfg, nil
}

func looksLikeBase64Encoded32ByteKey(raw string) bool {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return len(decoded) == 32
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

func splitCSV(raw string) []string {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func loadConfigFileIntoEnv(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read PAM_CONFIG_FILE %q: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return fmt.Errorf("invalid line %d in PAM_CONFIG_FILE %q: expected KEY=VALUE", i+1, path)
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return fmt.Errorf("invalid line %d in PAM_CONFIG_FILE %q: empty key", i+1, path)
		}
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env from PAM_CONFIG_FILE %q for key %q: %w", path, key, err)
		}
	}
	return nil
}
