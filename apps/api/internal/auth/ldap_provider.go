package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/districtd/pam/api/internal/config"
	"github.com/go-ldap/ldap/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Samba AD-compatible default: uses objectClass=user + username attribute match.
	defaultLDAPUserSearchFilter  = "(&(objectClass=user)({{username_attr}}={{username}}))"
	defaultLDAPGroupSearchFilter = "(&(objectClass=group)(member={{user_dn}}))"
)

type ldapAuthFailureKind string

const (
	ldapFailureUserNotFound      ldapAuthFailureKind = "user_not_found"
	ldapFailureInvalidPassword   ldapAuthFailureKind = "invalid_password"
	ldapFailureBindSearchConfig  ldapAuthFailureKind = "bind_or_search_config_issue"
	ldapFailureTLSOrConnectivity ldapAuthFailureKind = "tls_or_connectivity_issue"
)

type ldapAuthError struct {
	kind ldapAuthFailureKind
	err  error
}

func (e *ldapAuthError) Error() string {
	switch e.kind {
	case ldapFailureUserNotFound:
		return "ldap authentication failed: user not found"
	case ldapFailureInvalidPassword:
		return "ldap authentication failed: invalid password"
	case ldapFailureBindSearchConfig:
		return "ldap authentication failed: bind/search configuration issue"
	case ldapFailureTLSOrConnectivity:
		return "ldap authentication failed: tls/connectivity issue"
	default:
		return "ldap authentication failed"
	}
}

func (e *ldapAuthError) Unwrap() error {
	if e.kind == ldapFailureUserNotFound || e.kind == ldapFailureInvalidPassword {
		return ErrInvalidCredentials
	}
	return e.err
}

type LDAPProvider struct {
	pool         *pgxpool.Pool
	cfg          config.LDAPConfig
	logger       *slog.Logger
	groupRoleMap map[string][]string
}

func NewLDAPProvider(pool *pgxpool.Pool, cfg config.LDAPConfig, logger *slog.Logger) (*LDAPProvider, error) {
	groupRoleMap, err := parseGroupRoleMapping(cfg.GroupRoleMappingRaw)
	if err != nil {
		return nil, err
	}

	return &LDAPProvider{
		pool:         pool,
		cfg:          cfg,
		logger:       logger.With("auth_provider", "ldap"),
		groupRoleMap: groupRoleMap,
	}, nil
}

func (p *LDAPProvider) Name() string {
	return "ldap"
}

func (p *LDAPProvider) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return User{}, ErrInvalidCredentials
	}

	conn, err := p.dial()
	if err != nil {
		p.logAuthFailure(ldapFailureTLSOrConnectivity, username, err)
		return User{}, &ldapAuthError{kind: ldapFailureTLSOrConnectivity, err: fmt.Errorf("connect to ldap server: %w", err)}
	}
	defer conn.Close()

	if bindErr := p.bindServiceAccount(conn); bindErr != nil {
		p.logAuthFailure(ldapFailureBindSearchConfig, username, bindErr)
		return User{}, &ldapAuthError{kind: ldapFailureBindSearchConfig, err: bindErr}
	}

	profile, err := p.lookupUser(ctx, conn, username)
	if err != nil {
		return User{}, err
	}

	if err := conn.Bind(profile.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			p.logAuthFailure(ldapFailureInvalidPassword, username, err)
			return User{}, &ldapAuthError{kind: ldapFailureInvalidPassword, err: err}
		}
		p.logAuthFailure(ldapFailureBindSearchConfig, username, err)
		return User{}, &ldapAuthError{kind: ldapFailureBindSearchConfig, err: fmt.Errorf("bind ldap user: %w", err)}
	}

	// Rebind as service account to keep group lookups predictable across directory ACLs.
	if p.cfg.BindDN != "" {
		if err := conn.Bind(p.cfg.BindDN, p.cfg.BindPassword); err != nil {
			p.logAuthFailure(ldapFailureBindSearchConfig, username, err)
			return User{}, &ldapAuthError{kind: ldapFailureBindSearchConfig, err: fmt.Errorf("re-bind ldap service account: %w", err)}
		}
	}

	mappedRoles, err := p.resolveMappedRoles(ctx, conn, profile)
	if err != nil {
		return User{}, err
	}

	return p.syncLocalUser(ctx, profile, mappedRoles)
}

func (p *LDAPProvider) bindServiceAccount(conn *ldap.Conn) error {
	if p.cfg.BindDN == "" {
		return nil
	}
	if err := conn.Bind(p.cfg.BindDN, p.cfg.BindPassword); err != nil {
		return fmt.Errorf("bind ldap service account: %w", err)
	}
	return nil
}

func (p *LDAPProvider) dial() (*ldap.Conn, error) {
	address := strings.TrimSpace(p.cfg.URL)
	if address == "" {
		scheme := "ldap"
		if p.cfg.UseTLS {
			scheme = "ldaps"
		}
		address = fmt.Sprintf("%s://%s:%d", scheme, p.cfg.Host, p.cfg.Port)
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: p.cfg.InsecureSkipVerify,
	}

	options := []ldap.DialOpt{}
	if strings.HasPrefix(strings.ToLower(address), "ldaps://") {
		options = append(options, ldap.DialWithTLSConfig(tlsConfig))
	}
	conn, err := ldap.DialURL(address, options...)
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(10 * time.Second)

	if p.cfg.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, err
		}
	}

	return conn, nil
}

func (p *LDAPProvider) lookupUser(ctx context.Context, conn *ldap.Conn, username string) (ldapUserProfile, error) {
	attrs := []string{}
	attrs = appendAttribute(attrs, p.cfg.UsernameAttribute)
	attrs = appendAttribute(attrs, p.cfg.DisplayNameAttribute, "displayName", "cn", "name")
	attrs = appendAttribute(attrs, p.cfg.EmailAttribute, "mail", "userPrincipalName")

	filter := p.renderFilter(
		p.cfg.UserSearchFilter,
		username,
		"",
		defaultLDAPUserSearchFilter,
	)
	search := ldap.NewSearchRequest(
		p.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,
		10,
		false,
		filter,
		attrs,
		nil,
	)

	result, err := conn.Search(search)
	if err != nil {
		kind := ldapFailureBindSearchConfig
		if isLDAPConnectivityError(err) {
			kind = ldapFailureTLSOrConnectivity
		}
		p.logAuthFailure(kind, username, err)
		return ldapUserProfile{}, &ldapAuthError{kind: kind, err: fmt.Errorf("ldap user search failed: %w", err)}
	}
	if len(result.Entries) == 0 {
		p.logAuthFailure(ldapFailureUserNotFound, username, nil)
		return ldapUserProfile{}, &ldapAuthError{kind: ldapFailureUserNotFound, err: ErrInvalidCredentials}
	}
	if len(result.Entries) > 1 {
		err := fmt.Errorf("ldap user search returned %d entries for username %q", len(result.Entries), username)
		p.logAuthFailure(ldapFailureBindSearchConfig, username, err)
		return ldapUserProfile{}, &ldapAuthError{kind: ldapFailureBindSearchConfig, err: err}
	}

	entry := result.Entries[0]
	profile := ldapUserProfile{
		DN:       entry.DN,
		Username: strings.TrimSpace(entry.GetAttributeValue(p.cfg.UsernameAttribute)),
		DisplayName: firstNonEmptyAttributeValue(entry,
			p.cfg.DisplayNameAttribute,
			"displayName",
			"cn",
			"name",
		),
		Email: firstNonEmptyAttributeValue(entry,
			p.cfg.EmailAttribute,
			"mail",
			"userPrincipalName",
		),
	}
	if profile.Username == "" {
		profile.Username = username
	}

	if err := ctx.Err(); err != nil {
		return ldapUserProfile{}, err
	}
	return profile, nil
}

func (p *LDAPProvider) resolveMappedRoles(ctx context.Context, conn *ldap.Conn, profile ldapUserProfile) ([]string, error) {
	if len(p.groupRoleMap) == 0 {
		return nil, nil
	}

	baseDN := strings.TrimSpace(p.cfg.GroupSearchBaseDN)
	if baseDN == "" {
		baseDN = p.cfg.BaseDN
	}
	groupAttr := strings.TrimSpace(p.cfg.GroupNameAttribute)
	if groupAttr == "" {
		groupAttr = "cn"
	}

	filter := p.renderFilter(p.cfg.GroupSearchFilter, profile.Username, profile.DN, defaultLDAPGroupSearchFilter)
	search := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,
		10,
		false,
		filter,
		[]string{groupAttr},
		nil,
	)

	result, err := conn.Search(search)
	if err != nil {
		kind := ldapFailureBindSearchConfig
		if isLDAPConnectivityError(err) {
			kind = ldapFailureTLSOrConnectivity
		}
		p.logAuthFailure(kind, profile.Username, err)
		return nil, &ldapAuthError{kind: kind, err: fmt.Errorf("ldap group search failed: %w", err)}
	}

	roleSet := make(map[string]struct{})
	for _, entry := range result.Entries {
		for _, mappingKey := range groupMappingKeys(entry, groupAttr) {
			for _, role := range p.groupRoleMap[mappingKey] {
				roleSet[role] = struct{}{}
			}
		}
	}

	roles := make([]string, 0, len(roleSet))
	for role := range roleSet {
		roles = append(roles, role)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return roles, nil
}

func (p *LDAPProvider) syncLocalUser(ctx context.Context, profile ldapUserProfile, mappedRoles []string) (User, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	user := User{
		Username:    profile.Username,
		Email:       profile.Email,
		DisplayName: profile.DisplayName,
	}

	const existingUserQuery = `
SELECT id, created_at
FROM users
WHERE username = $1
LIMIT 1;`

	queryErr := tx.QueryRow(ctx, existingUserQuery, profile.Username).Scan(&user.ID, &user.CreatedAt)
	switch {
	case queryErr == nil:
		const updateUserSQL = `
UPDATE users
SET email = $2,
    display_name = $3,
    auth_provider = 'ldap',
    is_active = TRUE,
    last_login_at = $4,
    updated_at = $4
WHERE id = $1;`
		if _, err := tx.Exec(ctx, updateUserSQL, user.ID, nullIfEmpty(profile.Email), nullIfEmpty(profile.DisplayName), now); err != nil {
			return User{}, fmt.Errorf("update ldap user: %w", err)
		}
	case errors.Is(queryErr, pgx.ErrNoRows):
		const insertUserSQL = `
INSERT INTO users (username, email, display_name, is_active, auth_provider, last_login_at)
VALUES ($1, $2, $3, TRUE, 'ldap', $4)
RETURNING id, created_at;`
		if err := tx.QueryRow(ctx, insertUserSQL, profile.Username, nullIfEmpty(profile.Email), nullIfEmpty(profile.DisplayName), now).
			Scan(&user.ID, &user.CreatedAt); err != nil {
			return User{}, fmt.Errorf("create local user for ldap identity: %w", err)
		}
		if err := p.ensureRoleAssignment(ctx, tx, user.ID, defaultRoleUser); err != nil {
			return User{}, err
		}
	default:
		return User{}, fmt.Errorf("lookup local user: %w", queryErr)
	}

	for _, role := range mappedRoles {
		if err := p.ensureRoleAssignment(ctx, tx, user.ID, role); err != nil {
			return User{}, err
		}
	}

	roles, err := p.loadRolesTx(ctx, tx, user.ID)
	if err != nil {
		return User{}, err
	}
	user.Roles = roles

	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit ldap user sync transaction: %w", err)
	}

	return user, nil
}

func (p *LDAPProvider) ensureRoleAssignment(ctx context.Context, tx pgx.Tx, userID, roleName string) error {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return nil
	}

	const roleExistsSQL = `SELECT EXISTS(SELECT 1 FROM roles WHERE name = $1);`
	var roleExists bool
	if err := tx.QueryRow(ctx, roleExistsSQL, roleName).Scan(&roleExists); err != nil {
		return fmt.Errorf("check role %s exists: %w", roleName, err)
	}
	if !roleExists {
		p.logger.Warn("ldap mapped role not assigned because role does not exist", "role", roleName, "user_id", userID)
		return nil
	}

	const assignRoleSQL = `
INSERT INTO user_roles (user_id, role_id)
SELECT $1, r.id
FROM roles r
WHERE r.name = $2
ON CONFLICT (user_id, role_id) DO NOTHING;`

	if _, err := tx.Exec(ctx, assignRoleSQL, userID, roleName); err != nil {
		return fmt.Errorf("assign role %s: %w", roleName, err)
	}
	return nil
}

func (p *LDAPProvider) loadRolesTx(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	const query = `
SELECT r.name
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.name;`

	rows, err := tx.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()

	roles := make([]string, 0, 4)
	for rows.Next() {
		var role string
		if scanErr := rows.Scan(&role); scanErr != nil {
			return nil, fmt.Errorf("scan role: %w", scanErr)
		}
		roles = append(roles, role)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate roles: %w", rowsErr)
	}

	return roles, nil
}

func (p *LDAPProvider) renderFilter(template, username, userDN, defaultFilter string) string {
	filter := strings.TrimSpace(template)
	if filter == "" {
		filter = strings.TrimSpace(defaultFilter)
	}
	replacer := strings.NewReplacer(
		"{{username}}", ldap.EscapeFilter(username),
		"{{username_attr}}", strings.TrimSpace(p.cfg.UsernameAttribute),
		"{{user_dn}}", ldap.EscapeFilter(userDN),
	)
	return replacer.Replace(filter)
}

func parseGroupRoleMapping(raw string) (map[string][]string, error) {
	result := make(map[string][]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result, nil
	}

	entries := strings.Split(raw, ",")
	for _, entry := range entries {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid PAM_LDAP_GROUP_ROLE_MAPPING entry %q (expected group=role1|role2)", entry)
		}
		groupName := strings.ToLower(strings.TrimSpace(parts[0]))
		if groupName == "" {
			return nil, fmt.Errorf("invalid PAM_LDAP_GROUP_ROLE_MAPPING entry %q (empty group)", entry)
		}
		roles := strings.Split(parts[1], "|")
		cleanRoles := make([]string, 0, len(roles))
		for _, role := range roles {
			trimmed := strings.TrimSpace(role)
			if trimmed != "" {
				cleanRoles = append(cleanRoles, trimmed)
			}
		}
		if len(cleanRoles) == 0 {
			return nil, fmt.Errorf("invalid PAM_LDAP_GROUP_ROLE_MAPPING entry %q (no roles)", entry)
		}
		result[groupName] = appendUnique(result[groupName], cleanRoles...)
	}

	return result, nil
}

func firstNonEmptyAttributeValue(entry *ldap.Entry, candidates ...string) string {
	for _, candidate := range candidates {
		attr := strings.TrimSpace(candidate)
		if attr == "" {
			continue
		}
		value := strings.TrimSpace(entry.GetAttributeValue(attr))
		if value != "" {
			return value
		}
	}
	return ""
}

func groupMappingKeys(entry *ldap.Entry, groupNameAttribute string) []string {
	keys := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)

	add := func(value string) {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	add(entry.DN)
	add(entry.GetAttributeValue(groupNameAttribute))
	return keys
}

func appendUnique(existing []string, newValues ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, role := range existing {
		seen[role] = struct{}{}
	}
	for _, role := range newValues {
		if _, exists := seen[role]; exists {
			continue
		}
		existing = append(existing, role)
		seen[role] = struct{}{}
	}
	return existing
}

func appendAttribute(existing []string, candidates ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, value := range existing {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		existing = append(existing, value)
		seen[key] = struct{}{}
	}
	return existing
}

func isLDAPConnectivityError(err error) bool {
	var ldapErr *ldap.Error
	if !errors.As(err, &ldapErr) {
		return false
	}
	switch ldapErr.ResultCode {
	case ldap.ErrorNetwork:
		return true
	default:
		return false
	}
}

func (p *LDAPProvider) logAuthFailure(kind ldapAuthFailureKind, username string, err error) {
	safeUser := strings.TrimSpace(username)
	baseDN := strings.TrimSpace(p.cfg.BaseDN)
	host := strings.TrimSpace(p.cfg.Host)
	if host == "" {
		host = strings.TrimSpace(p.cfg.URL)
	}
	if err == nil {
		p.logger.Warn("ldap authentication failure", "reason", string(kind), "username", safeUser, "base_dn", baseDN, "server", host)
		return
	}
	p.logger.Warn("ldap authentication failure", "reason", string(kind), "username", safeUser, "base_dn", baseDN, "server", host, "error", err)
}

type ldapUserProfile struct {
	DN          string
	Username    string
	DisplayName string
	Email       string
}
