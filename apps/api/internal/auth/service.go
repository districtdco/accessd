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
	pool        *pgxpool.Pool
	logger      *slog.Logger
	cfg         config.AuthConfig
	provider    Provider
	rateLimiter *LoginRateLimiter
}

func NewService(pool *pgxpool.Pool, cfg config.AuthConfig, logger *slog.Logger) (*Service, error) {
	provider, err := NewProvider(pool, cfg, logger)
	if err != nil {
		logger.Warn("auth provider init failed; falling back to local provider until runtime config is available", "error", err)
		provider = NewLocalProvider(pool, logger)
	}

	svc := &Service{
		pool:        pool,
		cfg:         cfg,
		logger:      logger.With("component", "auth"),
		provider:    provider,
		rateLimiter: NewLoginRateLimiter(5*time.Minute, 10),
	}

	// Start periodic cleanup of expired rate limit entries.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			svc.rateLimiter.Cleanup()
		}
	}()

	return svc, nil
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
SELECT id, COALESCE(password_hash, ''), COALESCE(auth_provider, '')
FROM users
WHERE username = $1
LIMIT 1;`

		var existingHash string
		var existingProvider string
		if scanErr := s.pool.QueryRow(ctx, existingQuery, s.cfg.DevAdminUsername).Scan(&userID, &existingHash, &existingProvider); scanErr != nil {
			return fmt.Errorf("find dev admin user: %w", scanErr)
		}

		// Keep bootstrap-admin local password in sync with ACCESSD_DEV_ADMIN_PASSWORD
		// when this account is local (or missing provider metadata).
		if existingHash == "" || existingProvider == "" || strings.EqualFold(existingProvider, "local") {
			needsUpdate := existingHash == ""
			if !needsUpdate {
				needsUpdate = bcrypt.CompareHashAndPassword([]byte(existingHash), []byte(s.cfg.DevAdminPassword)) != nil
			}
			if !needsUpdate {
				goto assignRole
			}
			const updatePasswordSQL = `
UPDATE users
SET password_hash = $2, auth_provider = 'local', updated_at = NOW()
WHERE id = $1;`
			if _, execErr := s.pool.Exec(ctx, updatePasswordSQL, userID, string(passwordHash)); execErr != nil {
				return fmt.Errorf("set missing dev admin password hash: %w", execErr)
			}
			s.logger.Info("updated bootstrap admin password", "username", s.cfg.DevAdminUsername)
		} else {
			s.logger.Warn(
				"bootstrap admin exists with non-local provider; leaving password unchanged",
				"username", s.cfg.DevAdminUsername,
				"auth_provider", existingProvider,
			)
		}
	} else {
		s.logger.Info("seeded development admin user", "username", s.cfg.DevAdminUsername)
	}

assignRole:
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

func (s *Service) GetUserByUsername(ctx context.Context, username string) (User, error) {
	const query = `
SELECT id, username, COALESCE(email, ''), COALESCE(display_name, ''), created_at
FROM users
WHERE username = $1
LIMIT 1;`

	var user User
	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(username)).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.DisplayName,
		&user.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("get user by username: %w", err)
	}

	roles, err := s.loadRoles(ctx, user.ID)
	if err != nil {
		return User{}, fmt.Errorf("load user roles: %w", err)
	}
	user.Roles = roles

	return user, nil
}

// LoginRequest carries metadata about the login attempt for audit purposes.
type LoginRequest struct {
	Username  string
	Password  string
	SourceIP  string
	UserAgent string
}

// ChangePasswordRequest carries metadata and credentials for self-service password change.
type ChangePasswordRequest struct {
	UserID          string
	CurrentPassword string
	NewPassword     string
	SourceIP        string
	UserAgent       string
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, *http.Cookie, error) {
	return s.LoginWithContext(ctx, LoginRequest{Username: username, Password: password})
}

func (s *Service) LoginWithContext(ctx context.Context, req LoginRequest) (LoginResult, *http.Cookie, error) {
	username := strings.TrimSpace(req.Username)
	rateLimitKey := "user:" + strings.ToLower(username)

	if s.rateLimiter.IsBlocked(rateLimitKey) {
		s.logger.Warn("login rate limited", "username", username, "source_ip", req.SourceIP)
		s.recordLoginAudit(ctx, AuditLoginFailedRateLimited, username, nil, req.SourceIP, req.UserAgent, "rate_limited", "rate_limiter")
		return LoginResult{}, nil, ErrRateLimited
	}

	provider := s.resolveProviderForLogin(ctx, username)
	user, err := provider.Authenticate(ctx, username, req.Password)
	if err != nil {
		s.rateLimiter.RecordFailure(rateLimitKey)
		auditType, reason := classifyLoginFailure(err)
		s.logger.Warn("login failed",
			"username", username,
			"provider", provider.Name(),
			"reason", reason,
			"source_ip", req.SourceIP,
		)
		s.recordLoginAudit(ctx, auditType, username, nil, req.SourceIP, req.UserAgent, reason, provider.Name())
		if errors.Is(err, ErrInvalidCredentials) {
			return LoginResult{}, nil, ErrInvalidCredentials
		}
		return LoginResult{}, nil, fmt.Errorf("authenticate with provider %s: %w", provider.Name(), err)
	}

	s.rateLimiter.Reset(rateLimitKey)

	sessionToken, err := newSessionToken()
	if err != nil {
		return LoginResult{}, nil, fmt.Errorf("generate session token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(s.cfg.SessionTTL)
	if err := s.storeSession(ctx, user.ID, sessionToken, expiresAt); err != nil {
		return LoginResult{}, nil, err
	}

	s.logger.Info("login success",
		"username", username,
		"user_id", user.ID,
		"provider", provider.Name(),
		"source_ip", req.SourceIP,
	)
	s.recordLoginAudit(ctx, AuditLoginSuccess, username, &user.ID, req.SourceIP, req.UserAgent, "success", provider.Name())

	cookie := &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SessionSecure,
		SameSite: parseSameSite(s.cfg.SessionSameSite),
		Expires:  expiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	}

	return LoginResult{User: user}, cookie, nil
}

func classifyLoginFailure(err error) (string, string) {
	var ldapErr *ldapAuthError
	if errors.As(err, &ldapErr) {
		switch ldapErr.kind {
		case ldapFailureUserNotFound:
			return AuditLoginFailedUserNotFound, "user_not_found"
		case ldapFailureInvalidPassword:
			return AuditLoginFailedInvalidPass, "invalid_password"
		case ldapFailureTLSOrConnectivity:
			return AuditLoginFailedLDAPError, "ldap_tls_or_connectivity"
		case ldapFailureBindSearchConfig:
			return AuditLoginFailedLDAPError, "ldap_bind_or_search_config"
		}
	}
	if errors.Is(err, ErrInvalidCredentials) {
		return AuditLoginFailed, "invalid_credentials"
	}
	return AuditLoginFailed, "provider_error"
}

func (s *Service) recordLoginAudit(ctx context.Context, eventType, username string, userID *string, sourceIP, userAgent, outcome, providerName string) {
	meta := map[string]any{
		"username": username,
		"provider": providerName,
	}
	if err := writeAuditEvent(ctx, s.pool, AuditEvent{
		EventType:   eventType,
		Action:      "login",
		Outcome:     outcome,
		ActorUserID: userID,
		SourceIP:    sourceIP,
		UserAgent:   userAgent,
		Metadata:    meta,
	}); err != nil {
		s.logger.Warn("failed to write login audit event", "event_type", eventType, "username", username, "error", err)
	}
}

func (s *Service) resolveProviderForLogin(ctx context.Context, username string) Provider {
	resolvedCfg, err := resolveAuthConfigFromAdmin(ctx, s.pool, s.cfg)
	if err != nil {
		s.logger.Warn("failed to resolve auth config from admin settings; using fallback provider", "error", err)
		return s.provider
	}

	localProvider := NewLocalProvider(s.pool, s.logger)
	mode := strings.ToLower(strings.TrimSpace(resolvedCfg.ProviderMode))
	if mode == "" {
		mode = "local"
	}

	mappedProvider, mapped, lookupErr := s.lookupMappedAuthProvider(ctx, username)
	if lookupErr != nil {
		s.logger.Warn("failed to lookup mapped auth provider; using mode defaults", "username", username, "error", lookupErr)
	}

	// Username mapping in users table decides provider when known:
	// - local mapping always wins to keep local admin/IT break-glass login reliable
	// - ldap mapping uses ldap
	if mapped {
		switch mappedProvider {
		case "local":
			return localProvider
		case "ldap":
			ldapProvider, ldapErr := NewLDAPProvider(s.pool, resolvedCfg.LDAP, s.logger)
			if ldapErr != nil {
				s.logger.Warn("failed to build ldap provider for mapped ldap user; using local provider", "username", username, "error", ldapErr)
				return localProvider
			}
			return ldapProvider
		default:
			return localProvider
		}
	}

	// Unknown user: follow configured mode behavior.
	switch mode {
	case "ldap":
		ldapProvider, ldapErr := NewLDAPProvider(s.pool, resolvedCfg.LDAP, s.logger)
		if ldapErr != nil {
			s.logger.Warn("failed to build ldap provider; using local provider", "error", ldapErr)
			return localProvider
		}
		return ldapProvider
	case "hybrid":
		ldapProvider, ldapErr := NewLDAPProvider(s.pool, resolvedCfg.LDAP, s.logger)
		if ldapErr != nil {
			s.logger.Warn("failed to build ldap provider; using local provider", "error", ldapErr)
			return localProvider
		}
		return &FallbackProvider{
			primary:  ldapProvider,
			fallback: localProvider,
			logger:   s.logger.With("auth_provider", "hybrid"),
		}
	default:
		return localProvider
	}
}

func (s *Service) lookupMappedAuthProvider(ctx context.Context, username string) (string, bool, error) {
	const q = `
SELECT COALESCE(auth_provider, 'local')
FROM users
WHERE lower(username) = lower($1)
ORDER BY username
LIMIT 1;`
	var provider string
	err := s.pool.QueryRow(ctx, q, strings.TrimSpace(username)).Scan(&provider)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("query user auth provider: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(provider)), true, nil
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

func (s *Service) ChangePassword(ctx context.Context, req ChangePasswordRequest) error {
	userID := strings.TrimSpace(req.UserID)
	currentPassword := strings.TrimSpace(req.CurrentPassword)
	newPassword := strings.TrimSpace(req.NewPassword)

	if userID == "" {
		return ErrUnauthorized
	}
	if len(newPassword) < 8 {
		return ErrInvalidNewPassword
	}

	const query = `
SELECT username, COALESCE(auth_provider, 'local'), COALESCE(password_hash, '')
FROM users
WHERE id = $1
LIMIT 1;`

	var username string
	var authProvider string
	var passwordHash string
	if err := s.pool.QueryRow(ctx, query, userID).Scan(&username, &authProvider, &passwordHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("load user for password change: %w", err)
	}

	if authProvider != "local" {
		s.recordPasswordChangeAudit(ctx, userID, username, req.SourceIP, req.UserAgent, "failed_non_local_user")
		return ErrPasswordChangeNotAllowed
	}

	if passwordHash == "" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(currentPassword)) != nil {
		s.recordPasswordChangeAudit(ctx, userID, username, req.SourceIP, req.UserAgent, "failed_invalid_current_password")
		return ErrInvalidCurrentPassword
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	const updateSQL = `
UPDATE users
SET password_hash = $2,
    updated_at = NOW()
WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, updateSQL, userID, string(newHash)); err != nil {
		return fmt.Errorf("update user password hash: %w", err)
	}

	s.recordPasswordChangeAudit(ctx, userID, username, req.SourceIP, req.UserAgent, "success")
	return nil
}

func (s *Service) recordPasswordChangeAudit(ctx context.Context, userID, username, sourceIP, userAgent, outcome string) {
	meta := map[string]any{
		"username": username,
		"provider": "local",
	}
	eventType := AuditPasswordChangeSuccess
	if outcome != "success" {
		eventType = AuditPasswordChangeFailed
	}
	if err := writeAuditEvent(ctx, s.pool, AuditEvent{
		EventType:   eventType,
		Action:      "password_change",
		Outcome:     outcome,
		ActorUserID: &userID,
		SourceIP:    sourceIP,
		UserAgent:   userAgent,
		Metadata:    meta,
	}); err != nil {
		s.logger.Warn("failed to write password change audit event", "user_id", userID, "error", err)
	}
}

func (s *Service) ResolveCurrentUser(ctx context.Context, sessionToken string) (CurrentUser, error) {
	token := strings.TrimSpace(sessionToken)
	if token == "" {
		return CurrentUser{}, ErrUnauthorized
	}

	tokenHash := hashSessionToken(token)
	const query = `
SELECT u.id, u.username, COALESCE(u.email, ''), COALESCE(u.display_name, ''), COALESCE(u.auth_provider, 'local')
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.session_token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > NOW()
  AND u.is_active = TRUE
LIMIT 1;`

	var user CurrentUser
	if err := s.pool.QueryRow(ctx, query, tokenHash[:]).Scan(&user.ID, &user.Username, &user.Email, &user.DisplayName, &user.AuthProvider); err != nil {
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
		SameSite: parseSameSite(s.cfg.SessionSameSite),
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
	const revokeSQL = `
UPDATE auth_sessions
SET revoked_at = NOW()
WHERE user_id = $1
  AND revoked_at IS NULL
  AND expires_at > NOW();`
	const insertSQL = `
INSERT INTO auth_sessions (user_id, session_token_hash, expires_at, last_seen_at)
VALUES ($1, $2, $3, NOW());`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin auth session tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, revokeSQL, userID); err != nil {
		return fmt.Errorf("revoke prior auth sessions: %w", err)
	}

	if _, err := tx.Exec(ctx, insertSQL, userID, tokenHash[:], expiresAt); err != nil {
		return fmt.Errorf("store auth session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit auth session tx: %w", err)
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

func parseSameSite(raw string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
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
