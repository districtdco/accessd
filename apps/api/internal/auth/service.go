package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/districtd/pam/api/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultRoleAdmin    = "admin"
	defaultRoleOperator = "operator"
	defaultRoleAuditor  = "auditor"
	defaultRoleUser     = "user"
)

type Service struct {
	pool     *pgxpool.Pool
	logger   *slog.Logger
	cfg      config.AuthConfig
	provider Provider
}

func NewService(pool *pgxpool.Pool, cfg config.AuthConfig, logger *slog.Logger) *Service {
	return &Service{
		pool:     pool,
		cfg:      cfg,
		logger:   logger.With("component", "auth"),
		provider: NewLocalProvider(pool, logger),
	}
}

func (s *Service) Bootstrap(ctx context.Context) error {
	if err := s.ensureDefaultRoles(ctx); err != nil {
		return err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.DevAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash dev admin password: %w", err)
	}

	const userUpsert = `
INSERT INTO users (username, email, display_name, is_active, auth_provider, password_hash)
VALUES ($1, $2, $3, TRUE, 'local', $4)
ON CONFLICT (username) DO NOTHING
RETURNING id;`

	var userID string
	err = s.pool.QueryRow(ctx, userUpsert,
		s.cfg.DevAdminUsername,
		nullIfEmpty(s.cfg.DevAdminEmail),
		nullIfEmpty(s.cfg.DevAdminName),
		string(passwordHash),
	).Scan(&userID)

	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("seed dev admin user: %w", err)
		}

		const existingQuery = `
SELECT id, COALESCE(password_hash, '')
FROM users
WHERE username = $1
LIMIT 1;`

		var existingHash string
		if scanErr := s.pool.QueryRow(ctx, existingQuery, s.cfg.DevAdminUsername).Scan(&userID, &existingHash); scanErr != nil {
			return fmt.Errorf("find dev admin user: %w", scanErr)
		}

		if existingHash == "" {
			const updatePasswordSQL = `
UPDATE users
SET password_hash = $2, auth_provider = 'local', updated_at = NOW()
WHERE id = $1;`
			if _, execErr := s.pool.Exec(ctx, updatePasswordSQL, userID, string(passwordHash)); execErr != nil {
				return fmt.Errorf("set missing dev admin password hash: %w", execErr)
			}
		}
	} else {
		s.logger.Info("seeded development admin user", "username", s.cfg.DevAdminUsername)
	}

	const assignAdminRole = `
INSERT INTO user_roles (user_id, role_id)
SELECT $1, r.id
FROM roles r
WHERE r.name = $2
ON CONFLICT (user_id, role_id) DO NOTHING;`
	if _, err := s.pool.Exec(ctx, assignAdminRole, userID, defaultRoleAdmin); err != nil {
		return fmt.Errorf("assign admin role: %w", err)
	}

	return nil
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, *http.Cookie, error) {
	user, err := s.provider.Authenticate(ctx, username, password)
	if err != nil {
		if err == ErrInvalidCredentials {
			return LoginResult{}, nil, ErrInvalidCredentials
		}
		return LoginResult{}, nil, fmt.Errorf("authenticate with provider %s: %w", s.provider.Name(), err)
	}

	sessionToken, err := newSessionToken()
	if err != nil {
		return LoginResult{}, nil, fmt.Errorf("generate session token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(s.cfg.SessionTTL)
	if err := s.storeSession(ctx, user.ID, sessionToken, expiresAt); err != nil {
		return LoginResult{}, nil, err
	}

	cookie := &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SessionSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	}

	return LoginResult{User: user}, cookie, nil
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	tokenHash := hashSessionToken(sessionToken)
	const query = `
UPDATE auth_sessions
SET revoked_at = NOW()
WHERE session_token_hash = $1 AND revoked_at IS NULL;`

	if _, err := s.pool.Exec(ctx, query, tokenHash[:]); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	return nil
}

func (s *Service) ResolveCurrentUser(ctx context.Context, sessionToken string) (CurrentUser, error) {
	token := strings.TrimSpace(sessionToken)
	if token == "" {
		return CurrentUser{}, ErrUnauthorized
	}

	tokenHash := hashSessionToken(token)
	const query = `
SELECT u.id, u.username, COALESCE(u.email, ''), COALESCE(u.display_name, '')
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.session_token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > NOW()
  AND u.is_active = TRUE
LIMIT 1;`

	var user CurrentUser
	if err := s.pool.QueryRow(ctx, query, tokenHash[:]).Scan(&user.ID, &user.Username, &user.Email, &user.DisplayName); err != nil {
		return CurrentUser{}, ErrUnauthorized
	}

	roles, err := s.loadRoles(ctx, user.ID)
	if err != nil {
		return CurrentUser{}, fmt.Errorf("load current user roles: %w", err)
	}
	user.Roles = roles

	const touchSessionSQL = `
UPDATE auth_sessions
SET last_seen_at = NOW()
WHERE session_token_hash = $1;`
	if _, err := s.pool.Exec(ctx, touchSessionSQL, tokenHash[:]); err != nil {
		s.logger.Warn("failed to touch auth session", "user_id", user.ID, "error", err)
	}

	return user, nil
}

func (s *Service) ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SessionSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

func (s *Service) SessionCookieName() string {
	return s.cfg.SessionCookieName
}

func (s *Service) Authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.SessionCookieName)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "missing session")
			return
		}

		user, err := s.ResolveCurrentUser(r.Context(), cookie.Value)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r.WithContext(WithCurrentUser(r.Context(), user)))
	})
}

func (s *Service) RequireRoles(next http.Handler, allowedRoles ...string) http.Handler {
	return s.Authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentUser, ok := CurrentUserFromContext(r.Context())
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		for _, role := range allowedRoles {
			if currentUser.HasRole(role) {
				next.ServeHTTP(w, r)
				return
			}
		}

		writeAuthError(w, http.StatusForbidden, "forbidden")
	}))
}

func (s *Service) ensureDefaultRoles(ctx context.Context) error {
	roles := []struct {
		name        string
		description string
	}{
		{name: defaultRoleAdmin, description: "Full administrative access"},
		{name: defaultRoleOperator, description: "Operational access for managed workflows"},
		{name: defaultRoleAuditor, description: "Read-only audit and session review access"},
		{name: defaultRoleUser, description: "Standard user access"},
	}

	const upsertSQL = `
INSERT INTO roles (name, description)
VALUES ($1, $2)
ON CONFLICT (name) DO UPDATE
SET description = EXCLUDED.description,
    updated_at = NOW();`

	for _, role := range roles {
		if _, err := s.pool.Exec(ctx, upsertSQL, role.name, role.description); err != nil {
			return fmt.Errorf("upsert role %s: %w", role.name, err)
		}
	}

	return nil
}

func (s *Service) storeSession(ctx context.Context, userID, sessionToken string, expiresAt time.Time) error {
	tokenHash := hashSessionToken(sessionToken)
	const insertSQL = `
INSERT INTO auth_sessions (user_id, session_token_hash, expires_at, last_seen_at)
VALUES ($1, $2, $3, NOW());`

	if _, err := s.pool.Exec(ctx, insertSQL, userID, tokenHash[:], expiresAt); err != nil {
		return fmt.Errorf("store auth session: %w", err)
	}

	return nil
}

func (s *Service) loadRoles(ctx context.Context, userID string) ([]string, error) {
	const query = `
SELECT r.name
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.name;`

	rows, err := s.pool.Query(ctx, query, userID)
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

func newSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashSessionToken(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

func nullIfEmpty(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
