package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jackc/pgx/v5"
)

const (
	defaultLDAPUserSearchFilter  = "(&(objectClass=user)({{username_attr}}={{username}}))"
	defaultLDAPSyncSearchFilter  = "(objectClass=user)"
	defaultLDAPGroupSearchFilter = "(&(objectClass=group)(member={{user_dn}}))"
)

type LDAPSettings struct {
	ProviderMode           string
	Enabled                bool
	Host                   string
	Port                   int
	URL                    string
	BaseDN                 string
	BindDN                 string
	BindPassword           string
	UserSearchFilter       string
	SyncUserFilter         string
	UsernameAttribute      string
	DisplayNameAttribute   string
	EmailAttribute         string
	GroupSearchBaseDN      string
	GroupSearchFilter      string
	GroupNameAttribute     string
	GroupRoleMapping       string
	UseTLS                 bool
	StartTLS               bool
	InsecureSkipVerify     bool
	DeactivateMissingUsers bool
	UpdatedBy              string
	UpdatedAt              time.Time
	HasBindPassword        bool
}

type LDAPSyncSummary struct {
	Discovered  int      `json:"discovered"`
	Created     int      `json:"created"`
	Updated     int      `json:"updated"`
	Reactivated int      `json:"reactivated"`
	Unchanged   int      `json:"unchanged"`
	Deactivated int      `json:"deactivated"`
	Samples     []string `json:"samples,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type LDAPSyncRun struct {
	ID          int64
	StartedAt   time.Time
	CompletedAt *time.Time
	Status      string
	TriggeredBy string
	Summary     LDAPSyncSummary
	Error       string
}

type LDAPTestResult struct {
	Connected  bool   `json:"connected"`
	BindOK     bool   `json:"bind_ok"`
	Message    string `json:"message"`
	Server     string `json:"server"`
	SearchBase string `json:"search_base"`
}

type ldapUserEntry struct {
	Username    string
	DisplayName string
	Email       string
}

func (s *Service) defaultLDAPSettings() LDAPSettings {
	return LDAPSettings{
		ProviderMode:           strings.TrimSpace(s.authCfg.ProviderMode),
		Enabled:                strings.TrimSpace(s.authCfg.ProviderMode) != "local",
		Host:                   strings.TrimSpace(s.authCfg.LDAP.Host),
		Port:                   s.authCfg.LDAP.Port,
		URL:                    strings.TrimSpace(s.authCfg.LDAP.URL),
		BaseDN:                 strings.TrimSpace(s.authCfg.LDAP.BaseDN),
		BindDN:                 strings.TrimSpace(s.authCfg.LDAP.BindDN),
		BindPassword:           strings.TrimSpace(s.authCfg.LDAP.BindPassword),
		UserSearchFilter:       strings.TrimSpace(s.authCfg.LDAP.UserSearchFilter),
		SyncUserFilter:         defaultLDAPSyncSearchFilter,
		UsernameAttribute:      strings.TrimSpace(s.authCfg.LDAP.UsernameAttribute),
		DisplayNameAttribute:   strings.TrimSpace(s.authCfg.LDAP.DisplayNameAttribute),
		EmailAttribute:         strings.TrimSpace(s.authCfg.LDAP.EmailAttribute),
		GroupSearchBaseDN:      strings.TrimSpace(s.authCfg.LDAP.GroupSearchBaseDN),
		GroupSearchFilter:      strings.TrimSpace(s.authCfg.LDAP.GroupSearchFilter),
		GroupNameAttribute:     strings.TrimSpace(s.authCfg.LDAP.GroupNameAttribute),
		GroupRoleMapping:       strings.TrimSpace(s.authCfg.LDAP.GroupRoleMappingRaw),
		UseTLS:                 s.authCfg.LDAP.UseTLS,
		StartTLS:               s.authCfg.LDAP.StartTLS,
		InsecureSkipVerify:     s.authCfg.LDAP.InsecureSkipVerify,
		DeactivateMissingUsers: true,
	}
}

func (s *Service) normalizeLDAPSettings(in LDAPSettings, keepExistingPassword bool) (LDAPSettings, error) {
	out := in
	out.ProviderMode = strings.ToLower(strings.TrimSpace(out.ProviderMode))
	if out.ProviderMode == "" {
		out.ProviderMode = "local"
	}
	switch out.ProviderMode {
	case "local", "ldap", "hybrid":
	default:
		return LDAPSettings{}, fmt.Errorf("provider_mode must be one of local, ldap, hybrid")
	}
	out.Host = strings.TrimSpace(out.Host)
	out.URL = strings.TrimSpace(out.URL)
	out.BaseDN = strings.TrimSpace(out.BaseDN)
	out.BindDN = strings.TrimSpace(out.BindDN)
	out.BindPassword = strings.TrimSpace(out.BindPassword)
	out.UserSearchFilter = strings.TrimSpace(out.UserSearchFilter)
	out.SyncUserFilter = strings.TrimSpace(out.SyncUserFilter)
	out.UsernameAttribute = strings.TrimSpace(out.UsernameAttribute)
	out.DisplayNameAttribute = strings.TrimSpace(out.DisplayNameAttribute)
	out.EmailAttribute = strings.TrimSpace(out.EmailAttribute)
	out.GroupSearchBaseDN = strings.TrimSpace(out.GroupSearchBaseDN)
	out.GroupSearchFilter = strings.TrimSpace(out.GroupSearchFilter)
	out.GroupNameAttribute = strings.TrimSpace(out.GroupNameAttribute)
	out.GroupRoleMapping = strings.TrimSpace(out.GroupRoleMapping)

	if out.Port <= 0 || out.Port > 65535 {
		return LDAPSettings{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if out.Enabled {
		if out.BaseDN == "" {
			return LDAPSettings{}, fmt.Errorf("base_dn is required when ldap is enabled")
		}
		if out.URL == "" && out.Host == "" {
			return LDAPSettings{}, fmt.Errorf("either ldap url or host is required")
		}
	}
	if out.UserSearchFilter == "" {
		out.UserSearchFilter = defaultLDAPUserSearchFilter
	}
	if out.SyncUserFilter == "" {
		out.SyncUserFilter = defaultLDAPSyncSearchFilter
	}
	if out.UsernameAttribute == "" {
		out.UsernameAttribute = "sAMAccountName"
	}
	if out.DisplayNameAttribute == "" {
		out.DisplayNameAttribute = "displayName"
	}
	if out.EmailAttribute == "" {
		out.EmailAttribute = "mail"
	}
	if out.GroupSearchFilter == "" {
		out.GroupSearchFilter = defaultLDAPGroupSearchFilter
	}
	if out.GroupNameAttribute == "" {
		out.GroupNameAttribute = "cn"
	}
	if keepExistingPassword {
		out.BindPassword = ""
	}
	return out, nil
}

func (s *Service) GetLDAPSettings(ctx context.Context) (LDAPSettings, error) {
	defaults := s.defaultLDAPSettings()
	const q = `
SELECT
	provider_mode,
	enabled,
	host,
	port,
	url,
	base_dn,
	bind_dn,
	bind_password,
	user_search_filter,
	sync_user_filter,
	username_attribute,
	display_name_attribute,
	email_attribute,
	group_search_base_dn,
	group_search_filter,
	group_name_attribute,
	group_role_mapping,
	use_tls,
	start_tls,
	insecure_skip_verify,
	deactivate_missing_users,
	COALESCE(updated_by::text, ''),
	updated_at
FROM ldap_settings
WHERE id = 1
LIMIT 1;`
	var row LDAPSettings
	err := s.pool.QueryRow(ctx, q).Scan(
		&row.ProviderMode,
		&row.Enabled,
		&row.Host,
		&row.Port,
		&row.URL,
		&row.BaseDN,
		&row.BindDN,
		&row.BindPassword,
		&row.UserSearchFilter,
		&row.SyncUserFilter,
		&row.UsernameAttribute,
		&row.DisplayNameAttribute,
		&row.EmailAttribute,
		&row.GroupSearchBaseDN,
		&row.GroupSearchFilter,
		&row.GroupNameAttribute,
		&row.GroupRoleMapping,
		&row.UseTLS,
		&row.StartTLS,
		&row.InsecureSkipVerify,
		&row.DeactivateMissingUsers,
		&row.UpdatedBy,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			defaults.HasBindPassword = defaults.BindPassword != ""
			defaults.BindPassword = ""
			return defaults, nil
		}
		return LDAPSettings{}, fmt.Errorf("get ldap settings: %w", err)
	}
	row.HasBindPassword = row.BindPassword != ""
	row.BindPassword = ""
	return row, nil
}

func (s *Service) UpsertLDAPSettings(ctx context.Context, actorUserID string, input LDAPSettings, keepExistingPassword bool) (LDAPSettings, error) {
	normalized, err := s.normalizeLDAPSettings(input, keepExistingPassword)
	if err != nil {
		return LDAPSettings{}, err
	}

	passwordExpr := "$9"
	passwordArg := normalized.BindPassword
	if keepExistingPassword {
		passwordExpr = "COALESCE(NULLIF(ldap_settings.bind_password, ''), '')"
		passwordArg = ""
	}

	q := `
INSERT INTO ldap_settings (
	id,
	provider_mode,
	enabled,
	host,
	port,
	url,
	base_dn,
	bind_dn,
	bind_password,
	user_search_filter,
	sync_user_filter,
	username_attribute,
	display_name_attribute,
	email_attribute,
	group_search_base_dn,
	group_search_filter,
	group_name_attribute,
	group_role_mapping,
	use_tls,
	start_tls,
	insecure_skip_verify,
	deactivate_missing_users,
	updated_by,
	updated_at
)
VALUES (
	1,
	$1,
	$2,
	$3,
	$4,
	$5,
	$6,
	$7,
	$8,
	$9,
	$10,
	$11,
	$12,
	$13,
	$14,
	$15,
	$16,
	$17,
	$18,
	$19,
	$20,
	$21,
	NULLIF($22, '')::uuid,
	NOW()
)
ON CONFLICT (id) DO UPDATE SET
	provider_mode = EXCLUDED.provider_mode,
	enabled = EXCLUDED.enabled,
	host = EXCLUDED.host,
	port = EXCLUDED.port,
	url = EXCLUDED.url,
	base_dn = EXCLUDED.base_dn,
	bind_dn = EXCLUDED.bind_dn,
	bind_password = ` + passwordExpr + `,
	user_search_filter = EXCLUDED.user_search_filter,
	sync_user_filter = EXCLUDED.sync_user_filter,
	username_attribute = EXCLUDED.username_attribute,
	display_name_attribute = EXCLUDED.display_name_attribute,
	email_attribute = EXCLUDED.email_attribute,
	group_search_base_dn = EXCLUDED.group_search_base_dn,
	group_search_filter = EXCLUDED.group_search_filter,
	group_name_attribute = EXCLUDED.group_name_attribute,
	group_role_mapping = EXCLUDED.group_role_mapping,
	use_tls = EXCLUDED.use_tls,
	start_tls = EXCLUDED.start_tls,
	insecure_skip_verify = EXCLUDED.insecure_skip_verify,
	deactivate_missing_users = EXCLUDED.deactivate_missing_users,
	updated_by = NULLIF(EXCLUDED.updated_by::text, '')::uuid,
	updated_at = NOW();`
	if _, err := s.pool.Exec(ctx, q,
		normalized.ProviderMode,
		normalized.Enabled,
		normalized.Host,
		normalized.Port,
		normalized.URL,
		normalized.BaseDN,
		normalized.BindDN,
		passwordArg,
		normalized.UserSearchFilter,
		normalized.SyncUserFilter,
		normalized.UsernameAttribute,
		normalized.DisplayNameAttribute,
		normalized.EmailAttribute,
		normalized.GroupSearchBaseDN,
		normalized.GroupSearchFilter,
		normalized.GroupNameAttribute,
		normalized.GroupRoleMapping,
		normalized.UseTLS,
		normalized.StartTLS,
		normalized.InsecureSkipVerify,
		normalized.DeactivateMissingUsers,
		strings.TrimSpace(actorUserID),
	); err != nil {
		return LDAPSettings{}, fmt.Errorf("save ldap settings: %w", err)
	}

	return s.GetLDAPSettings(ctx)
}

func (s *Service) resolvedLDAPSettingsForOps(ctx context.Context) (LDAPSettings, error) {
	defaults := s.defaultLDAPSettings()
	const q = `
SELECT
	provider_mode,
	enabled,
	host,
	port,
	url,
	base_dn,
	bind_dn,
	bind_password,
	user_search_filter,
	sync_user_filter,
	username_attribute,
	display_name_attribute,
	email_attribute,
	group_search_base_dn,
	group_search_filter,
	group_name_attribute,
	group_role_mapping,
	use_tls,
	start_tls,
	insecure_skip_verify,
	deactivate_missing_users,
	COALESCE(updated_by::text, ''),
	updated_at
FROM ldap_settings
WHERE id = 1
LIMIT 1;`
	var row LDAPSettings
	err := s.pool.QueryRow(ctx, q).Scan(
		&row.ProviderMode,
		&row.Enabled,
		&row.Host,
		&row.Port,
		&row.URL,
		&row.BaseDN,
		&row.BindDN,
		&row.BindPassword,
		&row.UserSearchFilter,
		&row.SyncUserFilter,
		&row.UsernameAttribute,
		&row.DisplayNameAttribute,
		&row.EmailAttribute,
		&row.GroupSearchBaseDN,
		&row.GroupSearchFilter,
		&row.GroupNameAttribute,
		&row.GroupRoleMapping,
		&row.UseTLS,
		&row.StartTLS,
		&row.InsecureSkipVerify,
		&row.DeactivateMissingUsers,
		&row.UpdatedBy,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return defaults, nil
		}
		return LDAPSettings{}, fmt.Errorf("load ldap settings: %w", err)
	}
	return row, nil
}

func (s *Service) TestLDAPConnection(ctx context.Context, in LDAPSettings) (LDAPTestResult, error) {
	settings, err := s.normalizeLDAPSettings(in, false)
	if err != nil {
		return LDAPTestResult{}, err
	}
	conn, server, err := dialLDAP(settings)
	if err != nil {
		return LDAPTestResult{Connected: false, BindOK: false, Message: err.Error(), Server: server, SearchBase: settings.BaseDN}, nil
	}
	defer conn.Close()

	if err := bindLDAPServiceAccount(conn, settings); err != nil {
		return LDAPTestResult{Connected: true, BindOK: false, Message: err.Error(), Server: server, SearchBase: settings.BaseDN}, nil
	}

	return LDAPTestResult{
		Connected:  true,
		BindOK:     true,
		Message:    "LDAP connectivity and bind succeeded",
		Server:     server,
		SearchBase: settings.BaseDN,
	}, nil
}

func (s *Service) TriggerLDAPSync(ctx context.Context, actorUserID string) (LDAPSyncRun, error) {
	settings, err := s.resolvedLDAPSettingsForOps(ctx)
	if err != nil {
		return LDAPSyncRun{}, err
	}
	if !settings.Enabled {
		return LDAPSyncRun{}, fmt.Errorf("ldap is disabled in admin settings")
	}
	if strings.TrimSpace(settings.BaseDN) == "" {
		return LDAPSyncRun{}, fmt.Errorf("base_dn is required before sync")
	}

	runID, startedAt, err := s.createLDAPSyncRun(ctx, actorUserID)
	if err != nil {
		return LDAPSyncRun{}, err
	}

	summary, syncErr := s.syncLDAPUsers(ctx, settings)
	if syncErr != nil {
		if failErr := s.finishLDAPSyncRun(ctx, runID, "failed", summary, syncErr.Error()); failErr != nil {
			return LDAPSyncRun{}, fmt.Errorf("ldap sync failed (%v) and failed to mark run (%v)", syncErr, failErr)
		}
		return LDAPSyncRun{}, syncErr
	}
	if err := s.finishLDAPSyncRun(ctx, runID, "success", summary, ""); err != nil {
		return LDAPSyncRun{}, err
	}
	completedAt := time.Now().UTC()
	return LDAPSyncRun{
		ID:          runID,
		StartedAt:   startedAt,
		CompletedAt: &completedAt,
		Status:      "success",
		TriggeredBy: strings.TrimSpace(actorUserID),
		Summary:     summary,
	}, nil
}

func (s *Service) ListLDAPSyncRuns(ctx context.Context, limit int) ([]LDAPSyncRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	const q = `
SELECT id, started_at, completed_at, status, COALESCE(triggered_by::text, ''), summary, COALESCE(error, '')
FROM ldap_sync_runs
ORDER BY started_at DESC, id DESC
LIMIT $1;`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list ldap sync runs: %w", err)
	}
	defer rows.Close()

	items := make([]LDAPSyncRun, 0)
	for rows.Next() {
		var row LDAPSyncRun
		var summaryBlob []byte
		if err := rows.Scan(&row.ID, &row.StartedAt, &row.CompletedAt, &row.Status, &row.TriggeredBy, &summaryBlob, &row.Error); err != nil {
			return nil, fmt.Errorf("scan ldap sync run: %w", err)
		}
		if len(summaryBlob) > 0 {
			_ = json.Unmarshal(summaryBlob, &row.Summary)
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ldap sync runs: %w", err)
	}
	return items, nil
}

func (s *Service) createLDAPSyncRun(ctx context.Context, actorUserID string) (int64, time.Time, error) {
	const q = `
INSERT INTO ldap_sync_runs (status, triggered_by)
VALUES ('running', NULLIF($1, '')::uuid)
RETURNING id, started_at;`
	var id int64
	var started time.Time
	if err := s.pool.QueryRow(ctx, q, strings.TrimSpace(actorUserID)).Scan(&id, &started); err != nil {
		return 0, time.Time{}, fmt.Errorf("create ldap sync run: %w", err)
	}
	return id, started, nil
}

func (s *Service) finishLDAPSyncRun(ctx context.Context, runID int64, status string, summary LDAPSyncSummary, errText string) error {
	blob, _ := json.Marshal(summary)
	const q = `
UPDATE ldap_sync_runs
SET completed_at = NOW(),
	status = $2,
	summary = $3::jsonb,
	error = NULLIF($4, '')
WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, q, runID, status, blob, strings.TrimSpace(errText)); err != nil {
		return fmt.Errorf("finish ldap sync run: %w", err)
	}
	return nil
}

func (s *Service) syncLDAPUsers(ctx context.Context, settings LDAPSettings) (LDAPSyncSummary, error) {
	conn, _, err := dialLDAP(settings)
	if err != nil {
		return LDAPSyncSummary{}, fmt.Errorf("connect ldap: %w", err)
	}
	defer conn.Close()
	if err := bindLDAPServiceAccount(conn, settings); err != nil {
		return LDAPSyncSummary{}, err
	}

	entries, err := searchLDAPUsers(conn, settings)
	if err != nil {
		return LDAPSyncSummary{}, err
	}
	if len(entries) == 0 {
		return LDAPSyncSummary{Warnings: []string{"LDAP sync returned zero users; no local users were modified"}}, fmt.Errorf("ldap sync returned zero users")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LDAPSyncSummary{}, fmt.Errorf("begin ldap sync tx: %w", err)
	}
	defer tx.Rollback(ctx)

	names := make([]string, 0, len(entries))
	summary := LDAPSyncSummary{Discovered: len(entries), Samples: make([]string, 0, 20)}
	for _, entry := range entries {
		names = append(names, entry.Username)
		change, err := s.upsertLDAPUserTx(ctx, tx, entry)
		if err != nil {
			return summary, err
		}
		switch change {
		case "created":
			summary.Created++
		case "reactivated":
			summary.Reactivated++
		case "updated":
			summary.Updated++
		default:
			summary.Unchanged++
		}
		if len(summary.Samples) < 20 {
			summary.Samples = append(summary.Samples, entry.Username)
		}
	}

	if settings.DeactivateMissingUsers {
		deactivated, err := s.deactivateMissingLDAPUsersTx(ctx, tx, names)
		if err != nil {
			return summary, err
		}
		summary.Deactivated = deactivated
	}

	if err := tx.Commit(ctx); err != nil {
		return summary, fmt.Errorf("commit ldap sync tx: %w", err)
	}

	if err := s.writeLDAPSyncAuditEvent(ctx, summary); err != nil {
		s.logger.Warn("failed to write ldap sync audit event", "error", err)
	}
	return summary, nil
}

func (s *Service) writeLDAPSyncAuditEvent(ctx context.Context, summary LDAPSyncSummary) error {
	payload, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	const q = `
INSERT INTO audit_events (event_type, action, outcome, metadata)
VALUES ('admin_action', 'ldap_sync', 'success', $1::jsonb);`
	_, err = s.pool.Exec(ctx, q, payload)
	return err
}

func (s *Service) upsertLDAPUserTx(ctx context.Context, tx pgx.Tx, entry ldapUserEntry) (string, error) {
	const selectQ = `
SELECT id, is_active, COALESCE(email, ''), COALESCE(display_name, ''), COALESCE(auth_provider, '')
FROM users
WHERE username = $1
LIMIT 1;`
	var id string
	var isActive bool
	var email string
	var displayName string
	var provider string
	err := tx.QueryRow(ctx, selectQ, entry.Username).Scan(&id, &isActive, &email, &displayName, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		const insertQ = `
INSERT INTO users (username, email, display_name, is_active, auth_provider, updated_at)
VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), TRUE, 'ldap', NOW())
RETURNING id;`
		if err := tx.QueryRow(ctx, insertQ, entry.Username, entry.Email, entry.DisplayName).Scan(&id); err != nil {
			return "", fmt.Errorf("insert ldap user %s: %w", entry.Username, err)
		}
		if err := s.ensureUserRoleTx(ctx, tx, id, "user"); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup local user %s: %w", entry.Username, err)
	}

	changed := !isActive || provider != "ldap" || email != entry.Email || displayName != entry.DisplayName
	if changed {
		const updateQ = `
UPDATE users
SET email = NULLIF($2, ''),
	display_name = NULLIF($3, ''),
	is_active = TRUE,
	auth_provider = 'ldap',
	updated_at = NOW()
WHERE id = $1;`
		if _, err := tx.Exec(ctx, updateQ, id, entry.Email, entry.DisplayName); err != nil {
			return "", fmt.Errorf("update ldap user %s: %w", entry.Username, err)
		}
		if !isActive {
			return "reactivated", nil
		}
		return "updated", nil
	}

	return "unchanged", nil
}

func (s *Service) ensureUserRoleTx(ctx context.Context, tx pgx.Tx, userID, roleName string) error {
	const q = `
INSERT INTO user_roles (user_id, role_id)
SELECT $1, id
FROM roles
WHERE name = $2
ON CONFLICT (user_id, role_id) DO NOTHING;`
	if _, err := tx.Exec(ctx, q, userID, roleName); err != nil {
		return fmt.Errorf("assign default role: %w", err)
	}
	return nil
}

func (s *Service) deactivateMissingLDAPUsersTx(ctx context.Context, tx pgx.Tx, activeLDAPUsernames []string) (int, error) {
	if len(activeLDAPUsernames) == 0 {
		return 0, nil
	}
	const q = `
WITH inactive AS (
	UPDATE users
	SET is_active = FALSE,
		updated_at = NOW()
	WHERE auth_provider = 'ldap'
		AND is_active = TRUE
		AND NOT (username = ANY($1::text[]))
	RETURNING id
)
SELECT COUNT(*) FROM inactive;`
	var count int
	if err := tx.QueryRow(ctx, q, activeLDAPUsernames).Scan(&count); err != nil {
		return 0, fmt.Errorf("deactivate missing ldap users: %w", err)
	}
	return count, nil
}

func dialLDAP(settings LDAPSettings) (*ldap.Conn, string, error) {
	address := strings.TrimSpace(settings.URL)
	if address == "" {
		scheme := "ldap"
		if settings.UseTLS {
			scheme = "ldaps"
		}
		address = fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: settings.InsecureSkipVerify,
	}

	options := []ldap.DialOpt{}
	if strings.HasPrefix(strings.ToLower(address), "ldaps://") {
		options = append(options, ldap.DialWithTLSConfig(tlsConfig))
	}
	conn, err := ldap.DialURL(address, options...)
	if err != nil {
		return nil, address, err
	}
	conn.SetTimeout(10 * time.Second)
	if settings.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, address, err
		}
	}
	return conn, address, nil
}

func bindLDAPServiceAccount(conn *ldap.Conn, settings LDAPSettings) error {
	if settings.BindDN == "" {
		return nil
	}
	if err := conn.Bind(settings.BindDN, settings.BindPassword); err != nil {
		return fmt.Errorf("bind ldap service account: %w", err)
	}
	return nil
}

func searchLDAPUsers(conn *ldap.Conn, settings LDAPSettings) ([]ldapUserEntry, error) {
	attrs := []string{settings.UsernameAttribute}
	attrs = appendAttribute(attrs, settings.DisplayNameAttribute, "displayName", "cn", "name")
	attrs = appendAttribute(attrs, settings.EmailAttribute, "mail", "userPrincipalName")
	filter := strings.TrimSpace(settings.SyncUserFilter)
	if filter == "" {
		filter = defaultLDAPSyncSearchFilter
	}
	search := ldap.NewSearchRequest(
		settings.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,
		15,
		false,
		filter,
		attrs,
		nil,
	)
	result, err := conn.Search(search)
	if err != nil {
		return nil, fmt.Errorf("search ldap users: %w", err)
	}

	seen := map[string]struct{}{}
	users := make([]ldapUserEntry, 0, len(result.Entries))
	for _, entry := range result.Entries {
		username := strings.TrimSpace(entry.GetAttributeValue(settings.UsernameAttribute))
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		users = append(users, ldapUserEntry{
			Username: username,
			DisplayName: firstNonEmptyAttributeValue(entry,
				settings.DisplayNameAttribute,
				"displayName",
				"cn",
				"name",
			),
			Email: firstNonEmptyAttributeValue(entry,
				settings.EmailAttribute,
				"mail",
				"userPrincipalName",
			),
		})
	}
	sort.Slice(users, func(i, j int) bool {
		return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
	})
	return users, nil
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

func NormalizeRemoteIP(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		trimmed = host
	}
	return strings.TrimSpace(trimmed)
}
