package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/districtd/pam/api/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func OpenPool(ctx context.Context, cfg config.DBConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open db pool: %w", err)
	}

	logger.Info("database pool initialized", "max_conns", cfg.MaxConns, "min_conns", cfg.MinConns)
	return pool, nil
}

func VerifyConnection(ctx context.Context, pool *pgxpool.Pool) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(verifyCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	return nil
}
