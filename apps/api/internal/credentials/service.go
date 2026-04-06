package credentials

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
	TypePassword   = "password"
	TypeSSHKey     = "ssh_key"
	TypeDBPassword = "db_password"
)

var ErrCredentialNotFound = errors.New("credential not found")

type StoredCredential struct {
	ID        string
	AssetID   string
	Type      string
	Username  string
	Algorithm string
	KeyID     string
	CreatedAt time.Time
}

type CredentialMetadata struct {
	ID            string
	AssetID       string
	Type          string
	Username      string
	Algorithm     string
	KeyID         string
	Metadata      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastRotatedAt *time.Time
}

type CreateInput struct {
	AssetID   string
	Type      string
	Username  string
	Secret    string
	Metadata  json.RawMessage
	CreatedBy *string
}

type ResolvedCredential struct {
	AssetID  string
	Type     string
	Username string
	Secret   string
}

type Service struct {
	pool   *pgxpool.Pool
	cipher *Cipher
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, cipher *Cipher, logger *slog.Logger) *Service {
	return &Service{pool: pool, cipher: cipher, logger: logger.With("component", "credentials")}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (StoredCredential, error) {
	if err := validateCreateInput(input); err != nil {
		return StoredCredential{}, err
	}

	aad := []byte(fmt.Sprintf("asset:%s:type:%s", strings.TrimSpace(input.AssetID), input.Type))
	nonce, encrypted, err := s.cipher.Encrypt([]byte(input.Secret), aad)
	if err != nil {
		return StoredCredential{}, fmt.Errorf("encrypt secret: %w", err)
	}

	metadata := normalizeMetadata(input.Metadata)

	const query = `
INSERT INTO credentials (
    asset_id,
    credential_type,
    algorithm,
    key_id,
    nonce,
    encrypted_data,
    aad,
    metadata,
    username,
    created_by,
    last_rotated_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, NOW(), NOW())
RETURNING id, asset_id, credential_type, COALESCE(username, ''), algorithm, key_id, created_at;`

	var stored StoredCredential
	if err := s.pool.QueryRow(ctx, query,
		strings.TrimSpace(input.AssetID),
		input.Type,
		algorithmAES256GCM,
		s.cipher.KeyID(),
		nonce,
		encrypted,
		aad,
		[]byte(metadata),
		strings.TrimSpace(input.Username),
		input.CreatedBy,
	).Scan(&stored.ID, &stored.AssetID, &stored.Type, &stored.Username, &stored.Algorithm, &stored.KeyID, &stored.CreatedAt); err != nil {
		return StoredCredential{}, fmt.Errorf("create credential: %w", err)
	}

	return stored, nil
}

func (s *Service) Upsert(ctx context.Context, input CreateInput) (StoredCredential, error) {
	if err := validateCreateInput(input); err != nil {
		return StoredCredential{}, err
	}

	aad := []byte(fmt.Sprintf("asset:%s:type:%s", strings.TrimSpace(input.AssetID), input.Type))
	nonce, encrypted, err := s.cipher.Encrypt([]byte(input.Secret), aad)
	if err != nil {
		return StoredCredential{}, fmt.Errorf("encrypt secret: %w", err)
	}

	metadata := normalizeMetadata(input.Metadata)

	const query = `
INSERT INTO credentials (
    asset_id,
    credential_type,
    algorithm,
    key_id,
    nonce,
    encrypted_data,
    aad,
    metadata,
    username,
    created_by,
    last_rotated_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, NOW(), NOW())
ON CONFLICT (asset_id, credential_type) DO UPDATE
SET algorithm = EXCLUDED.algorithm,
    key_id = EXCLUDED.key_id,
    nonce = EXCLUDED.nonce,
    encrypted_data = EXCLUDED.encrypted_data,
    aad = EXCLUDED.aad,
    metadata = EXCLUDED.metadata,
    username = EXCLUDED.username,
    created_by = EXCLUDED.created_by,
    last_rotated_at = NOW(),
    updated_at = NOW()
RETURNING id, asset_id, credential_type, COALESCE(username, ''), algorithm, key_id, created_at;`

	var stored StoredCredential
	if err := s.pool.QueryRow(ctx, query,
		strings.TrimSpace(input.AssetID),
		input.Type,
		algorithmAES256GCM,
		s.cipher.KeyID(),
		nonce,
		encrypted,
		aad,
		[]byte(metadata),
		strings.TrimSpace(input.Username),
		input.CreatedBy,
	).Scan(&stored.ID, &stored.AssetID, &stored.Type, &stored.Username, &stored.Algorithm, &stored.KeyID, &stored.CreatedAt); err != nil {
		return StoredCredential{}, fmt.Errorf("upsert credential: %w", err)
	}

	return stored, nil
}

func (s *Service) ResolveForAsset(ctx context.Context, assetID, credentialType string) (ResolvedCredential, error) {
	if strings.TrimSpace(assetID) == "" {
		return ResolvedCredential{}, fmt.Errorf("asset id is required")
	}
	if !isSupportedType(credentialType) {
		return ResolvedCredential{}, fmt.Errorf("unsupported credential type: %s", credentialType)
	}

	const query = `
SELECT asset_id, credential_type, COALESCE(username, ''), nonce, encrypted_data, aad
FROM credentials
WHERE asset_id = $1
  AND credential_type = $2
LIMIT 1;`

	var (
		resolved  ResolvedCredential
		nonce     []byte
		encrypted []byte
		aad       []byte
	)

	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(assetID), credentialType).Scan(
		&resolved.AssetID,
		&resolved.Type,
		&resolved.Username,
		&nonce,
		&encrypted,
		&aad,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResolvedCredential{}, ErrCredentialNotFound
		}
		return ResolvedCredential{}, fmt.Errorf("resolve credential: %w", err)
	}

	secret, err := s.cipher.Decrypt(nonce, encrypted, aad)
	if err != nil {
		return ResolvedCredential{}, err
	}

	resolved.Secret = string(secret)
	return resolved, nil
}

func (s *Service) ListMetadataForAsset(ctx context.Context, assetID string) ([]CredentialMetadata, error) {
	aid := strings.TrimSpace(assetID)
	if aid == "" {
		return nil, fmt.Errorf("asset id is required")
	}

	const query = `
SELECT
	id,
	asset_id,
	credential_type,
	COALESCE(username, ''),
	algorithm,
	key_id,
	metadata,
	created_at,
	updated_at,
	last_rotated_at
FROM credentials
WHERE asset_id = $1
ORDER BY credential_type ASC;`

	rows, err := s.pool.Query(ctx, query, aid)
	if err != nil {
		return nil, fmt.Errorf("list credential metadata: %w", err)
	}
	defer rows.Close()

	items := make([]CredentialMetadata, 0)
	for rows.Next() {
		var (
			item          CredentialMetadata
			lastRotatedAt *time.Time
		)
		if scanErr := rows.Scan(
			&item.ID,
			&item.AssetID,
			&item.Type,
			&item.Username,
			&item.Algorithm,
			&item.KeyID,
			&item.Metadata,
			&item.CreatedAt,
			&item.UpdatedAt,
			&lastRotatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan credential metadata: %w", scanErr)
		}
		item.LastRotatedAt = lastRotatedAt
		items = append(items, item)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate credential metadata: %w", rowsErr)
	}

	return items, nil
}

func validateCreateInput(input CreateInput) error {
	if strings.TrimSpace(input.AssetID) == "" {
		return fmt.Errorf("asset id is required")
	}
	if strings.TrimSpace(input.Secret) == "" {
		return fmt.Errorf("secret is required")
	}
	if !isSupportedType(input.Type) {
		return fmt.Errorf("unsupported credential type: %s", input.Type)
	}
	if len(input.Metadata) > 0 && !json.Valid(input.Metadata) {
		return fmt.Errorf("metadata must be valid json")
	}
	return nil
}

func normalizeMetadata(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func isSupportedType(t string) bool {
	switch t {
	case TypePassword, TypeSSHKey, TypeDBPassword:
		return true
	default:
		return false
	}
}
