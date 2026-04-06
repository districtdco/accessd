package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/districtd/pam/api/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    config.MigrationConfig
}

type StatusRow struct {
	Version string
	Name    string
	Applied bool
}

type migrationFile struct {
	Version string
	Name    string
	Path    string
}

func NewRunner(pool *pgxpool.Pool, cfg config.MigrationConfig, logger *slog.Logger) *Runner {
	return &Runner{pool: pool, cfg: cfg, logger: logger}
}

func (r *Runner) Up(ctx context.Context) error {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	files, err := loadMigrationFiles(r.cfg.Dir)
	if err != nil {
		return err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, file := range files {
		if applied[file.Version] {
			continue
		}

		sqlBytes, readErr := os.ReadFile(file.Path)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", file.Path, readErr)
		}

		tx, beginErr := r.pool.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return fmt.Errorf("begin migration tx for version %s: %w", file.Version, beginErr)
		}

		startedAt := time.Now().UTC()
		r.logger.Info("applying migration", "version", file.Version, "name", file.Name)

		if _, execErr := tx.Exec(ctx, string(sqlBytes)); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", file.Path, execErr)
		}

		insertSQL := fmt.Sprintf("INSERT INTO %s (version, name, applied_at) VALUES ($1, $2, $3)", r.cfg.Table)
		if _, execErr := tx.Exec(ctx, insertSQL, file.Version, file.Name, startedAt); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration version %s: %w", file.Version, execErr)
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit migration version %s: %w", file.Version, commitErr)
		}

		r.logger.Info("migration applied", "version", file.Version, "name", file.Name)
	}

	return nil
}

func (r *Runner) Status(ctx context.Context) ([]StatusRow, error) {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	files, err := loadMigrationFiles(r.cfg.Dir)
	if err != nil {
		return nil, err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]StatusRow, 0, len(files))
	for _, file := range files {
		rows = append(rows, StatusRow{
			Version: file.Version,
			Name:    file.Name,
			Applied: applied[file.Version],
		})
	}

	return rows, nil
}

func (r *Runner) ensureMigrationsTable(ctx context.Context) error {
	query := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	version TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL
);`, r.cfg.Table)

	if _, err := r.pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	return nil
}

func (r *Runner) appliedVersions(ctx context.Context) (map[string]bool, error) {
	query := fmt.Sprintf("SELECT version FROM %s", r.cfg.Table)
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if scanErr := rows.Scan(&version); scanErr != nil {
			return nil, fmt.Errorf("scan applied migration version: %w", scanErr)
		}
		applied[version] = true
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", rowsErr)
	}

	return applied, nil
}

func loadMigrationFiles(dir string) ([]migrationFile, error) {
	resolvedDir, err := resolveMigrationDir(dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	files := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}

		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration filename: %s", name)
		}

		if _, convErr := strconv.Atoi(parts[0]); convErr != nil {
			return nil, fmt.Errorf("invalid migration version in filename %s: %w", name, convErr)
		}

		logicalName := strings.TrimSuffix(parts[1], ".up.sql")
		files = append(files, migrationFile{
			Version: parts[0],
			Name:    logicalName,
			Path:    filepath.Join(resolvedDir, name),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Version < files[j].Version
	})

	return files, nil
}

func resolveMigrationDir(primary string) (string, error) {
	candidates := []string{primary, "./migrations", "apps/api/migrations"}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}

		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect migrations dir %s: %w", candidate, err)
		}
	}

	return "", fmt.Errorf("migrations directory not found (tried: %s)", strings.Join(candidates, ", "))
}
