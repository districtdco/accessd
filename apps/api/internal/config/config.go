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
	Connector   ConnectorDistributionConfig
	Auth        AuthConfig
	Credentials CredentialsConfig
	Sessions    SessionsConfig
	SSHProxy    SSHProxyConfig
	PGProxy     PGProxyConfig
	MySQLProxy  MySQLProxyConfig
	MSSQLProxy  MSSQLProxyConfig
	MongoProxy  MongoProxyConfig
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

type ConnectorDistributionConfig struct {
	LatestVersion  string
	MinimumVersion string
	ReleaseChannel string
	DownloadBase   string
	DownloadRoot   string
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
	CACertFile           string
	CACertPEM            string
	UserSearchFilter     string
	UsernameAttribute    string
	DisplayNameAttribute string
	SurnameAttribute     string
	EmailAttribute       string
	SSHKeyAttribute      string
	AvatarAttribute      string
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
	LaunchTokenSecret   string
	LaunchTokenTTL      time.Duration
	ConnectorSecret     string // HMAC key for signing connector launch payloads
	MaterializeTimeout  time.Duration
	LaunchSweepInterval time.Duration
}

type SSHProxyConfig struct {
	ListenAddr             string
	PublicHost             string
	PublicPort             int
	Username               string
	HostKeyPath            string
	UpstreamHostKeyMode    string
	UpstreamKnownHostsPath string
	UpstreamAutoRepair     bool
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

type MongoProxyConfig struct {
	BindHost       string
	PublicHost     string
	ConnectTimeout time.Duration
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
	// 1. Resolve config file path from ACCESSD_CONFIG_FILE.
	configFile := strings.TrimSpace(os.Getenv("ACCESSD_CONFIG_FILE"))
	if err := loadConfigFileIntoEnv(configFile); err != nil {
		return Config{}, err
	}

	// 2. Read all configuration from ACCESSD_* env vars.
	cfg := Config{}

	cfg.App.Name = getEnv("ACCESSD_APP_NAME", "accessd")
	cfg.App.Env = getEnv("ACCESSD_ENV", "development")
	cfg.App.HTTPAddr = getEnv("ACCESSD_HTTP_ADDR", defaultHTTPAddr)
	corsDefaults := ""
	if strings.ToLower(cfg.App.Env) == "development" {
		corsDefaults = "http://localhost:3000,http://127.0.0.1:3000"
	}
	cfg.App.CORSAllowedOrigins = splitCSV(getEnv("ACCESSD_CORS_ALLOWED_ORIGINS", corsDefaults))
	cfg.App.ShutdownTimeout = getDurationEnv("ACCESSD_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	cfg.App.AllowUnsafeMode = getBoolEnv("ACCESSD_ALLOW_UNSAFE_MODE", false)
	sessionSecureDefault := strings.ToLower(cfg.App.Env) != "development"
	sessionSameSiteDefault := "lax"
	if sessionSecureDefault {
		sessionSameSiteDefault = "strict"
	}
	cfg.App.Migrations = MigrationConfig{
		Dir:   getEnv("ACCESSD_MIGRATIONS_DIR", defaultMigrationsFilePath),
		Table: getEnv("ACCESSD_MIGRATIONS_TABLE", defaultMigrationsTable),
	}
	cfg.App.Version = VersionInfo{
		Service: cfg.App.Name,
		Version: getEnv("ACCESSD_VERSION", "0.1.0-dev"),
		Commit:  getEnv("ACCESSD_COMMIT", "dev"),
		BuiltAt: getEnv("ACCESSD_BUILT_AT", "unknown"),
	}
	cfg.Connector = ConnectorDistributionConfig{
		LatestVersion:  getEnv("ACCESSD_CONNECTOR_LATEST_VERSION", cfg.App.Version.Version),
		MinimumVersion: getEnv("ACCESSD_CONNECTOR_MIN_VERSION", cfg.App.Version.Version),
		ReleaseChannel: getEnv("ACCESSD_CONNECTOR_RELEASE_CHANNEL", "stable"),
		DownloadBase:   strings.TrimRight(getEnv("ACCESSD_CONNECTOR_RELEASES_BASE_URL", "https://accessd.example.internal/downloads/connectors"), "/"),
		DownloadRoot:   strings.TrimRight(getEnv("ACCESSD_CONNECTOR_RELEASES_FS_ROOT", "/var/www/accessd-downloads/connectors"), "/"),
	}
	cfg.Auth = AuthConfig{
		SessionCookieName: getEnv("ACCESSD_AUTH_COOKIE_NAME", "accessd_session"),
		SessionTTL:        getDurationEnv("ACCESSD_AUTH_SESSION_TTL", 12*time.Hour),
		SessionSecure:     getBoolEnv("ACCESSD_AUTH_COOKIE_SECURE", sessionSecureDefault),
		SessionSameSite:   strings.ToLower(getEnv("ACCESSD_AUTH_COOKIE_SAMESITE", sessionSameSiteDefault)),
		DevAdminUsername:  getEnv("ACCESSD_DEV_ADMIN_USERNAME", "admin"),
		DevAdminPassword:  getEnv("ACCESSD_DEV_ADMIN_PASSWORD", "admin123"),
		DevAdminEmail:     getEnv("ACCESSD_DEV_ADMIN_EMAIL", "admin@accessd.local"),
		DevAdminName:      getEnv("ACCESSD_DEV_ADMIN_NAME", "AccessD Administrator"),
		ProviderMode:      "local",
		LDAP: LDAPConfig{
			URL:                  "",
			Host:                 "",
			Port:                 389,
			BaseDN:               "",
			BindDN:               "",
			BindPassword:         "",
			CACertFile:           "",
			CACertPEM:            "",
			UserSearchFilter:     "(&(objectClass=user)({{username_attr}}={{username}}))",
			UsernameAttribute:    "sAMAccountName",
			DisplayNameAttribute: "displayName",
			SurnameAttribute:     "sn",
			EmailAttribute:       "mail",
			SSHKeyAttribute:      "SshPublicKey",
			AvatarAttribute:      "jpegPhoto",
			UseTLS:               false,
			StartTLS:             false,
			InsecureSkipVerify:   false,
			GroupSearchBaseDN:    "",
			GroupSearchFilter:    "(&(objectClass=group)(member={{user_dn}}))",
			GroupNameAttribute:   "cn",
			GroupRoleMappingRaw:  "",
		},
	}
	cfg.Credentials = CredentialsConfig{
		MasterKey: strings.TrimSpace(os.Getenv("ACCESSD_VAULT_KEY")),
		KeyID:     getEnv("ACCESSD_VAULT_KEY_ID", "v1"),
	}
	cfg.Sessions = SessionsConfig{
		LaunchTokenSecret:   strings.TrimSpace(os.Getenv("ACCESSD_LAUNCH_TOKEN_SECRET")),
		LaunchTokenTTL:      getDurationEnv("ACCESSD_LAUNCH_TOKEN_TTL", 2*time.Minute),
		ConnectorSecret:     strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_SECRET")),
		MaterializeTimeout:  getDurationEnv("ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT", 45*time.Second),
		LaunchSweepInterval: getDurationEnv("ACCESSD_LAUNCH_SWEEP_INTERVAL", 15*time.Second),
	}
	hostKeyPath := strings.TrimSpace(os.Getenv("ACCESSD_SSH_PROXY_HOST_KEY_PATH"))
	if hostKeyPath == "" {
		hostKeyPath = ".accessd_ssh_proxy_host_key"
	}
	upstreamKnownHostsPath := strings.TrimSpace(os.Getenv("ACCESSD_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH"))
	if upstreamKnownHostsPath == "" {
		upstreamKnownHostsPath = ".accessd_upstream_known_hosts"
	}
	cfg.SSHProxy = SSHProxyConfig{
		ListenAddr:             getEnv("ACCESSD_SSH_PROXY_ADDR", ":2222"),
		PublicHost:             getEnv("ACCESSD_SSH_PROXY_PUBLIC_HOST", "127.0.0.1"),
		PublicPort:             getIntEnv("ACCESSD_SSH_PROXY_PUBLIC_PORT", 2222),
		Username:               getEnv("ACCESSD_SSH_PROXY_USERNAME", "accessd"),
		HostKeyPath:            hostKeyPath,
		UpstreamHostKeyMode:    getEnv("ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE", "accept-new"),
		UpstreamKnownHostsPath: upstreamKnownHostsPath,
		UpstreamAutoRepair:     getBoolEnv("ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_AUTO_REPAIR", true),
		IdleTimeout:            getDurationEnv("ACCESSD_SSH_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:          getDurationEnv("ACCESSD_SSH_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.PGProxy = PGProxyConfig{
		BindHost:       getEnv("ACCESSD_PG_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("ACCESSD_PG_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("ACCESSD_PG_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("ACCESSD_PG_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("ACCESSD_PG_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("ACCESSD_PG_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("ACCESSD_PG_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.MySQLProxy = MySQLProxyConfig{
		BindHost:       getEnv("ACCESSD_MYSQL_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("ACCESSD_MYSQL_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("ACCESSD_MYSQL_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("ACCESSD_MYSQL_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("ACCESSD_MYSQL_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("ACCESSD_MYSQL_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("ACCESSD_MYSQL_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.MSSQLProxy = MSSQLProxyConfig{
		BindHost:       getEnv("ACCESSD_MSSQL_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("ACCESSD_MSSQL_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("ACCESSD_MSSQL_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		QueryLogQueue:  getIntEnv("ACCESSD_MSSQL_PROXY_QUERY_LOG_QUEUE", 1024),
		QueryMaxBytes:  getIntEnv("ACCESSD_MSSQL_PROXY_QUERY_MAX_BYTES", 16384),
		IdleTimeout:    getDurationEnv("ACCESSD_MSSQL_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("ACCESSD_MSSQL_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.MongoProxy = MongoProxyConfig{
		BindHost:       getEnv("ACCESSD_MONGO_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:     getEnv("ACCESSD_MONGO_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout: getDurationEnv("ACCESSD_MONGO_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		IdleTimeout:    getDurationEnv("ACCESSD_MONGO_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:  getDurationEnv("ACCESSD_MONGO_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}
	cfg.RedisProxy = RedisProxyConfig{
		BindHost:        getEnv("ACCESSD_REDIS_PROXY_BIND_HOST", "127.0.0.1"),
		PublicHost:      getEnv("ACCESSD_REDIS_PROXY_PUBLIC_HOST", "127.0.0.1"),
		ConnectTimeout:  getDurationEnv("ACCESSD_REDIS_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		CommandLogQueue: getIntEnv("ACCESSD_REDIS_PROXY_COMMAND_LOG_QUEUE", 1024),
		ArgMaxLen:       getIntEnv("ACCESSD_REDIS_PROXY_ARG_MAX_LEN", 128),
		IdleTimeout:     getDurationEnv("ACCESSD_REDIS_PROXY_IDLE_TIMEOUT", 5*time.Minute),
		MaxSessionAge:   getDurationEnv("ACCESSD_REDIS_PROXY_MAX_SESSION_DURATION", 8*time.Hour),
	}

	cfg.DB.URL = strings.TrimSpace(os.Getenv("ACCESSD_DB_URL"))
	cfg.DB.MaxConns = int32(getIntEnv("ACCESSD_DB_MAX_CONNS", 10))
	cfg.DB.MinConns = int32(getIntEnv("ACCESSD_DB_MIN_CONNS", 1))
	cfg.DB.MaxConnLifetime = getDurationEnv("ACCESSD_DB_MAX_CONN_LIFETIME", time.Hour)
	cfg.DB.MaxConnIdleTime = getDurationEnv("ACCESSD_DB_MAX_CONN_IDLE_TIME", 15*time.Minute)

	if cfg.DB.URL == "" {
		return Config{}, fmt.Errorf("ACCESSD_DB_URL is required")
	}

	if cfg.DB.MinConns < 0 {
		return Config{}, fmt.Errorf("ACCESSD_DB_MIN_CONNS must be >= 0")
	}

	if cfg.DB.MaxConns < 1 {
		return Config{}, fmt.Errorf("ACCESSD_DB_MAX_CONNS must be >= 1")
	}

	if cfg.DB.MinConns > cfg.DB.MaxConns {
		return Config{}, fmt.Errorf("ACCESSD_DB_MIN_CONNS cannot be greater than ACCESSD_DB_MAX_CONNS")
	}

	if cfg.Auth.SessionTTL <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_AUTH_SESSION_TTL must be > 0")
	}

	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" {
		return Config{}, fmt.Errorf("ACCESSD_AUTH_COOKIE_NAME cannot be empty")
	}
	switch cfg.Auth.SessionSameSite {
	case "lax", "strict", "none":
	default:
		return Config{}, fmt.Errorf("ACCESSD_AUTH_COOKIE_SAMESITE must be one of: lax, strict, none")
	}
	if cfg.Auth.SessionSameSite == "none" && !cfg.Auth.SessionSecure {
		return Config{}, fmt.Errorf("ACCESSD_AUTH_COOKIE_SAMESITE=none requires ACCESSD_AUTH_COOKIE_SECURE=true")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminUsername) == "" {
		return Config{}, fmt.Errorf("ACCESSD_DEV_ADMIN_USERNAME cannot be empty")
	}

	if strings.TrimSpace(cfg.Auth.DevAdminPassword) == "" {
		return Config{}, fmt.Errorf("ACCESSD_DEV_ADMIN_PASSWORD cannot be empty")
	}
	if cfg.Credentials.MasterKey == "" {
		return Config{}, fmt.Errorf("ACCESSD_VAULT_KEY is required")
	}
	if cfg.Sessions.LaunchTokenSecret == "" {
		return Config{}, fmt.Errorf("ACCESSD_LAUNCH_TOKEN_SECRET is required")
	}
	if cfg.Sessions.LaunchTokenTTL <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_LAUNCH_TOKEN_TTL must be > 0")
	}
	if cfg.Sessions.MaterializeTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT must be > 0")
	}
	if cfg.Sessions.LaunchSweepInterval <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_LAUNCH_SWEEP_INTERVAL must be > 0")
	}
	if strings.TrimSpace(cfg.Connector.LatestVersion) == "" {
		return Config{}, fmt.Errorf("ACCESSD_CONNECTOR_LATEST_VERSION cannot be empty")
	}
	if strings.TrimSpace(cfg.Connector.MinimumVersion) == "" {
		return Config{}, fmt.Errorf("ACCESSD_CONNECTOR_MIN_VERSION cannot be empty")
	}
	if strings.TrimSpace(cfg.Connector.DownloadBase) == "" {
		return Config{}, fmt.Errorf("ACCESSD_CONNECTOR_RELEASES_BASE_URL cannot be empty")
	}
	if strings.TrimSpace(cfg.Connector.DownloadRoot) == "" {
		return Config{}, fmt.Errorf("ACCESSD_CONNECTOR_RELEASES_FS_ROOT cannot be empty")
	}
	if strings.TrimSpace(cfg.SSHProxy.ListenAddr) == "" {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_ADDR cannot be empty")
	}
	if strings.TrimSpace(cfg.SSHProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.SSHProxy.PublicPort <= 0 || cfg.SSHProxy.PublicPort > 65535 {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_PUBLIC_PORT must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.SSHProxy.Username) == "" {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_USERNAME cannot be empty")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SSHProxy.UpstreamHostKeyMode)) {
	case "accept-new", "known-hosts", "insecure":
	default:
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE must be one of: accept-new, known-hosts, insecure")
	}
	if strings.TrimSpace(cfg.SSHProxy.HostKeyPath) == "" {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_HOST_KEY_PATH cannot be empty")
	}
	if strings.TrimSpace(cfg.SSHProxy.UpstreamKnownHostsPath) == "" {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH cannot be empty")
	}
	if cfg.SSHProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.SSHProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.PGProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.PGProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.PGProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.PGProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.PGProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.PGProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.PGProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_PG_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.MySQLProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.MySQLProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.MySQLProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.MySQLProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.MySQLProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.MySQLProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.MySQLProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MYSQL_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.MSSQLProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.MSSQLProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.MSSQLProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.MSSQLProxy.QueryLogQueue <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_QUERY_LOG_QUEUE must be > 0")
	}
	if cfg.MSSQLProxy.QueryMaxBytes <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_QUERY_MAX_BYTES must be > 0")
	}
	if cfg.MSSQLProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.MSSQLProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MSSQL_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.MongoProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MONGO_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.MongoProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_MONGO_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.MongoProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MONGO_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.MongoProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MONGO_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.MongoProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_MONGO_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.TrimSpace(cfg.RedisProxy.BindHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_BIND_HOST cannot be empty")
	}
	if strings.TrimSpace(cfg.RedisProxy.PublicHost) == "" {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_PUBLIC_HOST cannot be empty")
	}
	if cfg.RedisProxy.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.RedisProxy.CommandLogQueue <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_COMMAND_LOG_QUEUE must be > 0")
	}
	if cfg.RedisProxy.ArgMaxLen <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_ARG_MAX_LEN must be > 0")
	}
	if cfg.RedisProxy.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_IDLE_TIMEOUT must be > 0")
	}
	if cfg.RedisProxy.MaxSessionAge <= 0 {
		return Config{}, fmt.Errorf("ACCESSD_REDIS_PROXY_MAX_SESSION_DURATION must be > 0")
	}
	if strings.ToLower(strings.TrimSpace(cfg.App.Env)) != "development" && !cfg.App.AllowUnsafeMode {
		if !cfg.Auth.SessionSecure {
			return Config{}, fmt.Errorf("ACCESSD_AUTH_COOKIE_SECURE must be true outside development (or set ACCESSD_ALLOW_UNSAFE_MODE=true)")
		}
		if cfg.Auth.SessionSameSite == "none" {
			return Config{}, fmt.Errorf("ACCESSD_AUTH_COOKIE_SAMESITE=none is blocked outside development unless ACCESSD_ALLOW_UNSAFE_MODE=true")
		}
		if strings.EqualFold(strings.TrimSpace(cfg.SSHProxy.UpstreamHostKeyMode), "insecure") {
			return Config{}, fmt.Errorf("ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE=%s is blocked outside development unless ACCESSD_ALLOW_UNSAFE_MODE=true", cfg.SSHProxy.UpstreamHostKeyMode)
		}
		if !looksLikeBase64Encoded32ByteKey(cfg.Credentials.MasterKey) {
			return Config{}, fmt.Errorf("ACCESSD_VAULT_KEY must be base64-encoded 32 bytes outside development (or set ACCESSD_ALLOW_UNSAFE_MODE=true)")
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
		return fmt.Errorf("read ACCESSD_CONFIG_FILE %q: %w", path, err)
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
			return fmt.Errorf("invalid line %d in ACCESSD_CONFIG_FILE %q: expected KEY=VALUE", i+1, path)
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return fmt.Errorf("invalid line %d in ACCESSD_CONFIG_FILE %q: empty key", i+1, path)
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
			return fmt.Errorf("set env from ACCESSD_CONFIG_FILE %q for key %q: %w", path, key, err)
		}
	}
	return nil
}
