package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/districtd/pam/api/internal/access"
	"github.com/districtd/pam/api/internal/admin"
	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/audit"
	"github.com/districtd/pam/api/internal/auth"
	"github.com/districtd/pam/api/internal/config"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/handlers"
	"github.com/districtd/pam/api/internal/httpserver"
	"github.com/districtd/pam/api/internal/migrate"
	"github.com/districtd/pam/api/internal/mssqlproxy"
	"github.com/districtd/pam/api/internal/mysqlproxy"
	"github.com/districtd/pam/api/internal/pgproxy"
	"github.com/districtd/pam/api/internal/redisproxy"
	"github.com/districtd/pam/api/internal/sessions"
	"github.com/districtd/pam/api/internal/sshproxy"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	Config     config.Config
	Logger     *slog.Logger
	DB         *pgxpool.Pool
	Server     *http.Server
	Migrations *migrate.Runner

	AuthService        *auth.Service
	AssetsService      *assets.Service
	AccessService      *access.Service
	AdminService       *admin.Service
	CredentialsService *credentials.Service
	SessionsService    *sessions.Service
	AuditService       *audit.Service
	SSHProxyServer     *sshproxy.Server
	PGProxyServer      *pgproxy.Service
	MySQLProxyServer   *mysqlproxy.Service
	MSSQLProxyServer   *mssqlproxy.Service
	RedisProxyServer   *redisproxy.Service
}

func New(_ context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) (*App, error) {
	migrations := migrate.NewRunner(pool, cfg.App.Migrations, logger)

	authService, err := auth.NewService(pool, cfg.Auth, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize auth service: %w", err)
	}
	assetsService := assets.NewService(pool, logger)
	accessService := access.NewService(pool, logger)
	adminService := admin.NewService(pool, logger)
	cipher, err := credentials.NewCipher(cfg.Credentials.MasterKey, cfg.Credentials.KeyID)
	if err != nil {
		return nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	credentialsService := credentials.NewService(pool, cipher, logger)
	sessionsService, err := sessions.NewService(pool, sessions.Config{
		LaunchTokenSecret: []byte(cfg.Sessions.LaunchTokenSecret),
		LaunchTokenTTL:    cfg.Sessions.LaunchTokenTTL,
		ConnectorSecret:   []byte(cfg.Sessions.ConnectorSecret),
		ProxyHost:         cfg.SSHProxy.PublicHost,
		ProxyPort:         cfg.SSHProxy.PublicPort,
		ProxyUsername:     cfg.SSHProxy.Username,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize sessions service: %w", err)
	}
	auditService := audit.NewService(logger)
	sshProxyServer, err := sshproxy.New(sshproxy.Config{
		ListenAddr:             cfg.SSHProxy.ListenAddr,
		Username:               cfg.SSHProxy.Username,
		HostKeyPath:            cfg.SSHProxy.HostKeyPath,
		UpstreamHostKeyMode:    cfg.SSHProxy.UpstreamHostKeyMode,
		UpstreamKnownHostsPath: cfg.SSHProxy.UpstreamKnownHostsPath,
		IdleTimeout:            cfg.SSHProxy.IdleTimeout,
		MaxSessionAge:          cfg.SSHProxy.MaxSessionAge,
	}, sessionsService, credentialsService, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize ssh proxy server: %w", err)
	}
	pgProxyServer, err := pgproxy.New(pgproxy.Config{
		BindHost:       cfg.PGProxy.BindHost,
		PublicHost:     cfg.PGProxy.PublicHost,
		ConnectTimeout: cfg.PGProxy.ConnectTimeout,
		QueryLogQueue:  cfg.PGProxy.QueryLogQueue,
		QueryMaxBytes:  cfg.PGProxy.QueryMaxBytes,
		IdleTimeout:    cfg.PGProxy.IdleTimeout,
		MaxSessionAge:  cfg.PGProxy.MaxSessionAge,
	}, sessionsService, credentialsService, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize pg proxy server: %w", err)
	}
	mysqlProxyServer, err := mysqlproxy.New(mysqlproxy.Config{
		BindHost:       cfg.MySQLProxy.BindHost,
		PublicHost:     cfg.MySQLProxy.PublicHost,
		ConnectTimeout: cfg.MySQLProxy.ConnectTimeout,
		QueryLogQueue:  cfg.MySQLProxy.QueryLogQueue,
		QueryMaxBytes:  cfg.MySQLProxy.QueryMaxBytes,
		IdleTimeout:    cfg.MySQLProxy.IdleTimeout,
		MaxSessionAge:  cfg.MySQLProxy.MaxSessionAge,
	}, sessionsService, credentialsService, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize mysql proxy server: %w", err)
	}
	mssqlProxyServer, err := mssqlproxy.New(mssqlproxy.Config{
		BindHost:       cfg.MSSQLProxy.BindHost,
		PublicHost:     cfg.MSSQLProxy.PublicHost,
		ConnectTimeout: cfg.MSSQLProxy.ConnectTimeout,
		QueryLogQueue:  cfg.MSSQLProxy.QueryLogQueue,
		QueryMaxBytes:  cfg.MSSQLProxy.QueryMaxBytes,
		IdleTimeout:    cfg.MSSQLProxy.IdleTimeout,
		MaxSessionAge:  cfg.MSSQLProxy.MaxSessionAge,
	}, sessionsService, credentialsService, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize mssql proxy server: %w", err)
	}
	redisProxyServer, err := redisproxy.New(redisproxy.Config{
		BindHost:       cfg.RedisProxy.BindHost,
		PublicHost:     cfg.RedisProxy.PublicHost,
		ConnectTimeout: cfg.RedisProxy.ConnectTimeout,
		LogQueue:       cfg.RedisProxy.CommandLogQueue,
		ArgMaxLen:      cfg.RedisProxy.ArgMaxLen,
		IdleTimeout:    cfg.RedisProxy.IdleTimeout,
		MaxSessionAge:  cfg.RedisProxy.MaxSessionAge,
	}, sessionsService, credentialsService, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize redis proxy server: %w", err)
	}

	router := httpserver.NewRouter(httpserver.RouteHandlers{
		Health:   handlers.NewHealthHandler(pool),
		Version:  handlers.NewVersionHandler(cfg.App.Version),
		Auth:     handlers.NewAuthHandler(authService),
		Access:   handlers.NewAccessHandler(accessService),
		Sessions: handlers.NewSessionsHandler(assetsService, accessService, credentialsService, sessionsService, pgProxyServer, mysqlProxyServer, mssqlProxyServer, redisProxyServer),
		Admin:    handlers.NewAdminHandler(adminService, assetsService, credentialsService),
		AuthSvc:  authService,
	})

	server := httpserver.New(httpserver.Config{Addr: cfg.App.HTTPAddr}, logger, router)

	return &App{
		Config:             cfg,
		Logger:             logger,
		DB:                 pool,
		Server:             server,
		Migrations:         migrations,
		AuthService:        authService,
		AssetsService:      assetsService,
		AccessService:      accessService,
		AdminService:       adminService,
		CredentialsService: credentialsService,
		SessionsService:    sessionsService,
		AuditService:       auditService,
		SSHProxyServer:     sshProxyServer,
		PGProxyServer:      pgProxyServer,
		MySQLProxyServer:   mysqlProxyServer,
		MSSQLProxyServer:   mssqlProxyServer,
		RedisProxyServer:   redisProxyServer,
	}, nil
}

func (a *App) RunMigrations(ctx context.Context) error {
	a.Logger.Info("running migrations")
	if err := a.Migrations.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

func (a *App) Bootstrap(ctx context.Context) error {
	a.Logger.Info("running auth/bootstrap")
	if err := a.AuthService.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap auth: %w", err)
	}
	if a.Config.App.Env == "development" {
		a.Logger.Info("running dev access bootstrap")
		if err := a.bootstrapDevAccess(ctx); err != nil {
			return fmt.Errorf("bootstrap dev access: %w", err)
		}
	}
	return nil
}

func (a *App) MigrationStatus(ctx context.Context) ([]migrate.StatusRow, error) {
	return a.Migrations.Status(ctx)
}

func (a *App) Close() {
	if a.DB != nil {
		a.DB.Close()
	}
}

func (a *App) bootstrapDevAccess(ctx context.Context) error {
	adminUser, err := a.AuthService.GetUserByUsername(ctx, a.Config.Auth.DevAdminUsername)
	if err != nil {
		return err
	}

	createdBy := &adminUser.ID
	seedAssets := []struct {
		name      string
		assetType string
		host      string
		port      int
		metadata  string
		username  string
		secret    string
		credType  string
		actions   []string
	}{
		{
			name:      "linux-app-01",
			assetType: assets.TypeLinuxVM,
			host:      "10.0.10.11",
			port:      22,
			metadata:  `{"env":"dev","os":"ubuntu-22.04","team":"platform"}`,
			username:  "ubuntu",
			secret:    "pam-dev-linux-app-01",
			credType:  credentials.TypePassword,
			actions:   []string{access.ActionShell, access.ActionSFTP},
		},
		{
			name:      "linux-batch-01",
			assetType: assets.TypeLinuxVM,
			host:      "10.0.10.12",
			port:      22,
			metadata:  `{"env":"dev","os":"debian-12","team":"data"}`,
			username:  "ec2-user",
			secret:    "pam-dev-linux-batch-01",
			credType:  credentials.TypePassword,
			actions:   []string{access.ActionShell, access.ActionSFTP},
		},
		{
			name:      "linux-ops-01",
			assetType: assets.TypeLinuxVM,
			host:      "10.0.10.13",
			port:      22,
			metadata:  `{"env":"dev","os":"rocky-9","team":"ops"}`,
			username:  "opsadmin",
			secret:    "pam-dev-linux-ops-01",
			credType:  credentials.TypePassword,
			actions:   []string{access.ActionShell, access.ActionSFTP},
		},
		{
			name:      "postgres-app",
			assetType: assets.TypeDatabase,
			host:      "10.0.20.21",
			port:      5432,
			metadata:  `{"engine":"postgres","database":"app","env":"dev"}`,
			username:  "app_user",
			secret:    "pam-dev-db-password",
			credType:  credentials.TypeDBPassword,
			actions:   []string{access.ActionDBeaver},
		},
		{
			name:      "mysql-app",
			assetType: assets.TypeDatabase,
			host:      "10.0.20.22",
			port:      3306,
			metadata:  `{"engine":"mysql","database":"appdb","env":"dev"}`,
			username:  "app_user",
			secret:    "pam-dev-mysql-password",
			credType:  credentials.TypeDBPassword,
			actions:   []string{access.ActionDBeaver},
		},
		{
			name:      "mssql-app",
			assetType: assets.TypeDatabase,
			host:      "10.0.20.23",
			port:      1433,
			metadata:  `{"engine":"mssql","database":"appdb","ssl_mode":"disable","env":"dev"}`,
			username:  "app_user",
			secret:    "pam-dev-mssql-password",
			credType:  credentials.TypeDBPassword,
			actions:   []string{access.ActionDBeaver},
		},
		{
			name:      "redis-cache",
			assetType: assets.TypeRedis,
			host:      "10.0.30.31",
			port:      6379,
			metadata:  `{"engine":"redis","env":"dev"}`,
			username:  "default",
			secret:    "pam-dev-redis-password",
			credType:  credentials.TypePassword,
			actions:   []string{access.ActionRedis},
		},
	}

	for _, seed := range seedAssets {
		metadata := json.RawMessage(seed.metadata)
		asset, err := a.AssetsService.Upsert(ctx, assets.CreateInput{
			Name:         seed.name,
			Type:         seed.assetType,
			Host:         seed.host,
			Port:         seed.port,
			MetadataJSON: metadata,
			CreatedBy:    createdBy,
		})
		if err != nil {
			return err
		}

		if _, err := a.CredentialsService.Upsert(ctx, credentials.CreateInput{
			AssetID:   asset.ID,
			Type:      seed.credType,
			Username:  seed.username,
			Secret:    seed.secret,
			Metadata:  metadata,
			CreatedBy: createdBy,
		}); err != nil {
			return err
		}

		for _, action := range seed.actions {
			if err := a.AccessService.GrantUserAction(ctx, adminUser.ID, asset.ID, action, createdBy); err != nil {
				return err
			}
		}
	}

	return nil
}
