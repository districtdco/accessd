package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/districtd/pam/api/internal/app"
	"github.com/districtd/pam/api/internal/config"
	"github.com/districtd/pam/api/internal/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

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
	if err := a.RunMigrations(ctx); err != nil {
		return err
	}
	if err := a.Bootstrap(ctx); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		a.Logger.Info("http server listening", "addr", a.Config.App.HTTPAddr)
		if err := a.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	if err := <-errCh; err != nil {
		return err
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
