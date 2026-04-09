package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	TypeLinuxVM  = "linux_vm"
	TypeDatabase = "database"
	TypeRedis    = "redis"
)

var ErrAssetNotFound = errors.New("asset not found")

type Asset struct {
	ID           string
	Name         string
	Type         string
	Host         string
	Port         int
	MetadataJSON json.RawMessage
	CreatedAt    time.Time
}

type CreateInput struct {
	Name         string
	Type         string
	Host         string
	Port         int
	MetadataJSON json.RawMessage
	CreatedBy    *string
}

type Service struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, logger: logger.With("component", "assets")}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Asset, error) {
	if err := validateInput(input); err != nil {
		return Asset{}, err
	}

	metadata := normalizedMetadata(input.MetadataJSON)

	const query = `
INSERT INTO assets (name, asset_type, host, port, metadata_json, created_by)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
RETURNING id, name, asset_type, host, port, metadata_json, created_at;`

	var asset Asset
	if err := s.pool.QueryRow(ctx, query,
		strings.TrimSpace(input.Name),
		input.Type,
		strings.TrimSpace(input.Host),
		input.Port,
		[]byte(metadata),
		input.CreatedBy,
	).Scan(&asset.ID, &asset.Name, &asset.Type, &asset.Host, &asset.Port, &asset.MetadataJSON, &asset.CreatedAt); err != nil {
		return Asset{}, fmt.Errorf("create asset: %w", err)
	}

	return asset, nil
}

func (s *Service) Upsert(ctx context.Context, input CreateInput) (Asset, error) {
	if err := validateInput(input); err != nil {
		return Asset{}, err
	}

	metadata := normalizedMetadata(input.MetadataJSON)

	const query = `
INSERT INTO assets (name, asset_type, host, port, metadata_json, created_by)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
ON CONFLICT (name, host, port) DO UPDATE
SET asset_type = EXCLUDED.asset_type,
    metadata_json = EXCLUDED.metadata_json,
    updated_at = NOW()
RETURNING id, name, asset_type, host, port, metadata_json, created_at;`

	var asset Asset
	if err := s.pool.QueryRow(ctx, query,
		strings.TrimSpace(input.Name),
		input.Type,
		strings.TrimSpace(input.Host),
		input.Port,
		[]byte(metadata),
		input.CreatedBy,
	).Scan(&asset.ID, &asset.Name, &asset.Type, &asset.Host, &asset.Port, &asset.MetadataJSON, &asset.CreatedAt); err != nil {
		return Asset{}, fmt.Errorf("upsert asset: %w", err)
	}

	return asset, nil
}

func (s *Service) GetByID(ctx context.Context, id string) (Asset, error) {
	const query = `
SELECT id, name, asset_type, host, port, metadata_json, created_at
FROM assets
WHERE id = $1
  AND COALESCE(status, 'active') <> 'deleted'
LIMIT 1;`

	var asset Asset
	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(id)).Scan(
		&asset.ID,
		&asset.Name,
		&asset.Type,
		&asset.Host,
		&asset.Port,
		&asset.MetadataJSON,
		&asset.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, ErrAssetNotFound
		}
		return Asset{}, fmt.Errorf("get asset by id: %w", err)
	}

	return asset, nil
}

func (s *Service) Update(ctx context.Context, assetID string, input CreateInput) (Asset, error) {
	id := strings.TrimSpace(assetID)
	if id == "" {
		return Asset{}, fmt.Errorf("asset id is required")
	}
	if err := validateInput(input); err != nil {
		return Asset{}, err
	}

	metadata := normalizedMetadata(input.MetadataJSON)

	const query = `
UPDATE assets
SET name = $2,
    asset_type = $3,
    host = $4,
    port = $5,
    metadata_json = $6::jsonb,
    updated_at = NOW()
WHERE id = $1
  AND COALESCE(status, 'active') <> 'deleted'
RETURNING id, name, asset_type, host, port, metadata_json, created_at;`

	var asset Asset
	if err := s.pool.QueryRow(ctx, query,
		id,
		strings.TrimSpace(input.Name),
		input.Type,
		strings.TrimSpace(input.Host),
		input.Port,
		[]byte(metadata),
	).Scan(&asset.ID, &asset.Name, &asset.Type, &asset.Host, &asset.Port, &asset.MetadataJSON, &asset.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, ErrAssetNotFound
		}
		return Asset{}, fmt.Errorf("update asset: %w", err)
	}

	return asset, nil
}

func (s *Service) List(ctx context.Context) ([]Asset, error) {
	const query = `
SELECT id, name, asset_type, host, port, metadata_json, created_at
FROM assets
WHERE COALESCE(status, 'active') <> 'deleted'
ORDER BY name ASC;`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	assets := make([]Asset, 0)
	for rows.Next() {
		var asset Asset
		if scanErr := rows.Scan(&asset.ID, &asset.Name, &asset.Type, &asset.Host, &asset.Port, &asset.MetadataJSON, &asset.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan asset: %w", scanErr)
		}
		assets = append(assets, asset)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate assets: %w", rowsErr)
	}

	return assets, nil
}

func validateInput(input CreateInput) error {
	name := strings.TrimSpace(input.Name)
	host := strings.TrimSpace(input.Host)
	if name == "" {
		return fmt.Errorf("asset name is required")
	}
	if host == "" {
		return fmt.Errorf("asset host is required")
	}
	if input.Port <= 0 || input.Port > 65535 {
		return fmt.Errorf("asset port is invalid")
	}
	if !isSupportedType(input.Type) {
		return fmt.Errorf("unsupported asset type: %s", input.Type)
	}
	if len(input.MetadataJSON) > 0 && !json.Valid(input.MetadataJSON) {
		return fmt.Errorf("asset metadata_json must be valid json")
	}
	return nil
}

func isSupportedType(assetType string) bool {
	switch assetType {
	case TypeLinuxVM, TypeDatabase, TypeRedis:
		return true
	default:
		return false
	}
}

func normalizedMetadata(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
