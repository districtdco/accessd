package access

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ActionShell   = "shell"
	ActionSFTP    = "sftp"
	ActionDBeaver = "dbeaver"
	ActionRedis   = "redis"
)

type AccessPoint struct {
	AssetID        string
	AssetName      string
	AssetType      string
	Host           string
	Port           int
	MetadataJSON   json.RawMessage
	AllowedActions []string
}

type Service struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, logger: logger.With("component", "access")}
}

func (s *Service) GrantUserAction(ctx context.Context, userID, assetID, action string, createdBy *string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(assetID) == "" {
		return fmt.Errorf("user id and asset id are required")
	}
	if !isSupportedAction(action) {
		return fmt.Errorf("unsupported action: %s", action)
	}

	const query = `
INSERT INTO access_grants (subject_type, subject_id, asset_id, action, effect, created_by)
VALUES ('user', $1, $2, $3, 'allow', $4)
ON CONFLICT (subject_type, subject_id, asset_id, action) DO UPDATE
SET effect = 'allow',
    created_by = EXCLUDED.created_by,
    updated_at = NOW();`

	if _, err := s.pool.Exec(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(assetID), action, createdBy); err != nil {
		return fmt.Errorf("grant user action: %w", err)
	}

	return nil
}

func (s *Service) AllowedAssetsForUser(ctx context.Context, userID string) ([]AccessPoint, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("user id is required")
	}

	const query = `
SELECT
    a.id,
    a.name,
    a.asset_type,
    a.host,
    a.port,
    a.metadata_json,
    ARRAY_AGG(DISTINCT ag.action ORDER BY ag.action) AS actions
FROM assets a
JOIN access_grants ag ON ag.asset_id = a.id
WHERE ag.effect = 'allow'
  AND (
        (ag.subject_type = 'user' AND ag.subject_id = $1)
        OR
        (
            ag.subject_type = 'group'
            AND ag.subject_id IN (
                SELECT ug.group_id
                FROM user_groups ug
                WHERE ug.user_id = $1
            )
        )
    )
GROUP BY a.id, a.name, a.asset_type, a.host, a.port, a.metadata_json
ORDER BY a.name ASC;`

	rows, err := s.pool.Query(ctx, query, strings.TrimSpace(userID))
	if err != nil {
		return nil, fmt.Errorf("query allowed assets: %w", err)
	}
	defer rows.Close()

	points := make([]AccessPoint, 0)
	for rows.Next() {
		var point AccessPoint
		if scanErr := rows.Scan(
			&point.AssetID,
			&point.AssetName,
			&point.AssetType,
			&point.Host,
			&point.Port,
			&point.MetadataJSON,
			&point.AllowedActions,
		); scanErr != nil {
			return nil, fmt.Errorf("scan allowed asset: %w", scanErr)
		}
		points = append(points, point)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate allowed assets: %w", rowsErr)
	}

	return points, nil
}

func (s *Service) CanUserPerform(ctx context.Context, userID, assetID, action string) (bool, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(assetID) == "" {
		return false, fmt.Errorf("user id and asset id are required")
	}
	if !isSupportedAction(action) {
		return false, fmt.Errorf("unsupported action: %s", action)
	}

	const query = `
SELECT EXISTS (
    SELECT 1
    FROM access_grants ag
    WHERE ag.asset_id = $2
      AND ag.action = $3
      AND ag.effect = 'allow'
      AND (
            (ag.subject_type = 'user' AND ag.subject_id = $1)
            OR
            (
                ag.subject_type = 'group'
                AND ag.subject_id IN (
                    SELECT ug.group_id
                    FROM user_groups ug
                    WHERE ug.user_id = $1
                )
            )
          )
);`

	var allowed bool
	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(assetID), action).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check access grant: %w", err)
	}

	return allowed, nil
}

func isSupportedAction(action string) bool {
	switch action {
	case ActionShell, ActionSFTP, ActionDBeaver, ActionRedis:
		return true
	default:
		return false
	}
}
