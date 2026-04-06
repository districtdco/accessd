package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type LocalProvider struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewLocalProvider(pool *pgxpool.Pool, logger *slog.Logger) *LocalProvider {
	return &LocalProvider{pool: pool, logger: logger.With("auth_provider", "local")}
}

func (p *LocalProvider) Name() string {
	return "local"
}

func (p *LocalProvider) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || strings.TrimSpace(password) == "" {
		return User{}, ErrInvalidCredentials
	}

	const query = `
SELECT id, username, COALESCE(email, ''), COALESCE(display_name, ''), COALESCE(password_hash, ''), created_at
FROM users
WHERE username = $1 AND is_active = TRUE AND auth_provider = 'local'
LIMIT 1;`

	var user User
	var passwordHash string
	if err := p.pool.QueryRow(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.DisplayName,
		&passwordHash,
		&user.CreatedAt,
	); err != nil {
		return User{}, ErrInvalidCredentials
	}

	if passwordHash == "" {
		return User{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}

	roles, err := p.loadRoles(ctx, user.ID)
	if err != nil {
		return User{}, fmt.Errorf("load roles: %w", err)
	}
	user.Roles = roles

	const updateLoginSQL = `UPDATE users SET last_login_at = $2, updated_at = $2 WHERE id = $1;`
	now := time.Now().UTC()
	if _, err := p.pool.Exec(ctx, updateLoginSQL, user.ID, now); err != nil {
		p.logger.Warn("failed to update last_login_at", "user_id", user.ID, "error", err)
	}

	return user, nil
}

func (p *LocalProvider) loadRoles(ctx context.Context, userID string) ([]string, error) {
	const query = `
SELECT r.name
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.name;`

	rows, err := p.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := make([]string, 0, 4)
	for rows.Next() {
		var role string
		if scanErr := rows.Scan(&role); scanErr != nil {
			return nil, scanErr
		}
		roles = append(roles, role)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}

	return roles, nil
}
