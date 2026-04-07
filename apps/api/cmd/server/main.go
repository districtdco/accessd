package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/districtd/pam/api/internal/app"
	"github.com/districtd/pam/api/internal/config"
	"github.com/districtd/pam/api/internal/db"
)

var (
	version = "0.1.0-dev"
	commit  = "dev"
	builtAt = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))
	setVersionEnvDefaults()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Info("starting pam-api",
		"env", cfg.App.Env,
		"http_addr", cfg.App.HTTPAddr,
		"version", cfg.App.Version.Version,
		"commit", cfg.App.Version.Commit,
		"built_at", cfg.App.Version.BuiltAt,
		"auth_provider", cfg.Auth.ProviderMode,
		"session_ttl", cfg.Auth.SessionTTL.String(),
		"session_secure", cfg.Auth.SessionSecure,
		"session_samesite", cfg.Auth.SessionSameSite,
		"ssh_proxy_addr", cfg.SSHProxy.ListenAddr,
		"ssh_proxy_public", fmt.Sprintf("%s:%d", cfg.SSHProxy.PublicHost, cfg.SSHProxy.PublicPort),
		"ssh_proxy_idle_timeout", cfg.SSHProxy.IdleTimeout.String(),
		"ssh_proxy_max_session_duration", cfg.SSHProxy.MaxSessionAge.String(),
		"pg_proxy_public_host", cfg.PGProxy.PublicHost,
		"pg_proxy_idle_timeout", cfg.PGProxy.IdleTimeout.String(),
		"pg_proxy_max_session_duration", cfg.PGProxy.MaxSessionAge.String(),
		"mysql_proxy_public_host", cfg.MySQLProxy.PublicHost,
		"mysql_proxy_idle_timeout", cfg.MySQLProxy.IdleTimeout.String(),
		"mysql_proxy_max_session_duration", cfg.MySQLProxy.MaxSessionAge.String(),
		"mssql_proxy_public_host", cfg.MSSQLProxy.PublicHost,
		"mssql_proxy_idle_timeout", cfg.MSSQLProxy.IdleTimeout.String(),
		"mssql_proxy_max_session_duration", cfg.MSSQLProxy.MaxSessionAge.String(),
		"redis_proxy_public_host", cfg.RedisProxy.PublicHost,
		"redis_proxy_idle_timeout", cfg.RedisProxy.IdleTimeout.String(),
		"redis_proxy_max_session_duration", cfg.RedisProxy.MaxSessionAge.String(),
		"ssh_host_key_mode", cfg.SSHProxy.UpstreamHostKeyMode,
		"connector_trust", cfg.Sessions.ConnectorSecret != "",
		"unsafe_mode", cfg.App.AllowUnsafeMode,
	)

	ctx := context.Background()
	pool, err := db.OpenPool(ctx, cfg.DB, logger)
	if err != nil {
		logger.Error("initialize database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.VerifyConnection(ctx, pool); err != nil {
		logger.Error("verify database connectivity", "error", err)
		os.Exit(1)
	}

	a, err := app.New(ctx, cfg, logger, pool)
	if err != nil {
		logger.Error("initialize app", "error", err)
		os.Exit(1)
	}

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"server"}
	}

	switch args[0] {
	case "server":
		if err := runServer(a); err != nil {
			logger.Error("server stopped with error", "error", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(ctx, a, args[1:]); err != nil {
			logger.Error("migration command failed", "error", err)
			os.Exit(1)
		}
	case "bootstrap":
		if err := runBootstrap(ctx, a); err != nil {
			logger.Error("bootstrap command failed", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("unknown command", "command", args[0])
		printUsage()
		os.Exit(1)
	}
}

func runServer(a *app.App) error {
	ctx := context.Background()
	a.Logger.Info("startup phase begin", "phase", "migrations")
	if err := a.RunMigrations(ctx); err != nil {
		a.Logger.Error("startup phase failed", "phase", "migrations", "error", err)
		return err
	}
	a.Logger.Info("startup phase complete", "phase", "migrations")
	a.Logger.Info("startup phase begin", "phase", "bootstrap")
	if err := a.Bootstrap(ctx); err != nil {
		a.Logger.Error("startup phase failed", "phase", "bootstrap", "error", err)
		return err
	}
	a.Logger.Info("startup phase complete", "phase", "bootstrap")

	errCh := make(chan error, 2)
	go func() {
		a.Logger.Info("http server listening", "addr", a.Config.App.HTTPAddr)
		if err := a.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	go func() {
		if err := a.SSHProxyServer.ListenAndServe(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.App.ShutdownTimeout)
	defer cancel()

	if err := a.Server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	if err := a.SSHProxyServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("ssh proxy shutdown failed: %w", err)
	}
	if err := a.PGProxyServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("pg proxy shutdown failed: %w", err)
	}
	if err := a.MySQLProxyServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("mysql proxy shutdown failed: %w", err)
	}
	if err := a.MSSQLProxyServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("mssql proxy shutdown failed: %w", err)
	}
	if err := a.RedisProxyServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("redis proxy shutdown failed: %w", err)
	}

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			return err
		}
	}

	a.Logger.Info("server stopped", "timeout", a.Config.App.ShutdownTimeout.String())
	return nil
}

func runBootstrap(ctx context.Context, a *app.App) error {
	if err := a.RunMigrations(ctx); err != nil {
		return err
	}
	if err := a.Bootstrap(ctx); err != nil {
		return err
	}
	a.Logger.Info("bootstrap complete")
	return nil
}

func runMigrate(ctx context.Context, a *app.App, args []string) error {
	if len(args) == 0 {
		printUsage()
		return fmt.Errorf("missing migrate subcommand")
	}

	switch args[0] {
	case "up":
		if err := a.RunMigrations(ctx); err != nil {
			return err
		}
		a.Logger.Info("migrations are up to date")
		return nil
	case "status":
		rows, err := a.MigrationStatus(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("%-10s %-35s %s\n", "VERSION", "NAME", "APPLIED")
		for _, row := range rows {
			applied := "no"
			if row.Applied {
				applied = "yes"
			}
			fmt.Printf("%-10s %-35s %s\n", row.Version, row.Name, applied)
		}
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown migrate subcommand: %s", args[0])
	}
}

func printUsage() {
	fmt.Println("usage:")
	fmt.Println("  pam-api server")
	fmt.Println("  pam-api migrate up")
	fmt.Println("  pam-api migrate status")
	fmt.Println("  pam-api bootstrap")
	fmt.Println()
	fmt.Println("default command is: server")
}

func setVersionEnvDefaults() {
	if strings.TrimSpace(os.Getenv("PAM_VERSION")) == "" {
		_ = os.Setenv("PAM_VERSION", version)
	}
	if strings.TrimSpace(os.Getenv("PAM_COMMIT")) == "" {
		_ = os.Setenv("PAM_COMMIT", commit)
	}
	if strings.TrimSpace(os.Getenv("PAM_BUILT_AT")) == "" {
		_ = os.Setenv("PAM_BUILT_AT", builtAt)
	}
}
