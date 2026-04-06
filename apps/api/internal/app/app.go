package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/districtd/pam/api/internal/access"
	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/audit"
	"github.com/districtd/pam/api/internal/auth"
	"github.com/districtd/pam/api/internal/config"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/handlers"
	"github.com/districtd/pam/api/internal/httpserver"
	"github.com/districtd/pam/api/internal/migrate"
	"github.com/districtd/pam/api/internal/sessions"
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
	CredentialsService *credentials.Service
	SessionsService    *sessions.Service
	AuditService       *audit.Service
}

func New(_ context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) (*App, error) {
	migrations := migrate.NewRunner(pool, cfg.App.Migrations, logger)

	authService := auth.NewService(pool, cfg.Auth, logger)
	assetsService := assets.NewService(logger)
	accessService := access.NewService(logger)
	credentialsService := credentials.NewService(logger)
	sessionsService := sessions.NewService(logger)
	auditService := audit.NewService(logger)

	router := httpserver.NewRouter(httpserver.RouteHandlers{
		Health:  handlers.NewHealthHandler(pool),
		Version: handlers.NewVersionHandler(cfg.App.Version),
		Auth:    handlers.NewAuthHandler(authService),
		AuthSvc: authService,
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
		CredentialsService: credentialsService,
		SessionsService:    sessionsService,
		AuditService:       auditService,
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
