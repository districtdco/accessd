package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound  = errors.New("user not found")
	ErrRoleNotFound  = errors.New("role not found")
	ErrGroupNotFound = errors.New("group not found")
	ErrAssetNotFound = errors.New("asset not found")
)

type Service struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

type UserSummary struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	AuthProvider string
	IsActive     bool
	Roles        []string
}

type UserDetail struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	AuthProvider string
	IsActive     bool
	Roles        []string
	Groups       []GroupSummary
}

type Role struct {
	ID          string
	Name        string
	Description string
}

type GroupSummary struct {
	ID          string
	Name        string
	Description string
	MemberCount int
}

type GroupMember struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
}

type AssetSummary struct {
	ID              string
	Name            string
	Type            string
	Host            string
	Port            int
	GrantCount      int
	CredentialCount int
}

type GrantRecord struct {
	SubjectType string
	SubjectID   string
	SubjectName string
	AssetID     string
	AssetName   string
	Action      string
	Effect      string
	CreatedAt   time.Time
}

type EffectiveAccess struct {
	AssetID   string
	AssetName string
	Actions   []EffectiveAction
}

type EffectiveAction struct {
	Action  string
	Sources []string
}

type CredentialAuditInput struct {
	ActorUserID    string
	AssetID        string
	CredentialType string
	Username       string
	MetadataKeys   []string
	SourceIP       string
	UserAgent      string
}

type effectiveAccessRow struct {
	assetID   string
	assetName string
	action    string
	source    string
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, logger: logger.With("component", "admin")}
}

func (s *Service) RecordCredentialUpsertAudit(ctx context.Context, input CredentialAuditInput) error {
	actorUserID := strings.TrimSpace(input.ActorUserID)
	assetID := strings.TrimSpace(input.AssetID)
	credentialType := strings.TrimSpace(input.CredentialType)
	if actorUserID == "" || assetID == "" || credentialType == "" {
		return fmt.Errorf("actor user id, asset id, and credential type are required")
	}

	metadata := map[string]any{
		"credential_type": credentialType,
		"username_set":    strings.TrimSpace(input.Username) != "",
		"metadata_keys":   input.MetadataKeys,
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}

	const query = `
INSERT INTO audit_events (
    actor_user_id,
    asset_id,
    event_type,
    action,
    outcome,
    source_ip,
    user_agent,
    metadata
)
VALUES ($1, $2, 'admin_action', 'credential_upsert', 'success', NULLIF($3, '')::inet, NULLIF($4, ''), $5::jsonb);`
	if _, err := s.pool.Exec(ctx, query, actorUserID, assetID, strings.TrimSpace(input.SourceIP), strings.TrimSpace(input.UserAgent), payload); err != nil {
		return fmt.Errorf("insert admin credential audit event: %w", err)
	}
	return nil
}

func (s *Service) ListUsers(ctx context.Context) ([]UserSummary, error) {
	const query = `
SELECT
	u.id,
	u.username,
	COALESCE(u.email, ''),
	COALESCE(u.display_name, ''),
	COALESCE(u.auth_provider, 'local'),
	u.is_active,
	COALESCE(
		ARRAY(
			SELECT r.name
			FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = u.id
			ORDER BY r.name
		),
		ARRAY[]::TEXT[]
	) AS roles
FROM users u
ORDER BY u.username ASC;`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]UserSummary, 0)
	for rows.Next() {
		var user UserSummary
		if scanErr := rows.Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.DisplayName,
			&user.AuthProvider,
			&user.IsActive,
			&user.Roles,
		); scanErr != nil {
			return nil, fmt.Errorf("scan user: %w", scanErr)
		}
		users = append(users, user)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate users: %w", rowsErr)
	}

	return users, nil
}

func (s *Service) GetUserDetail(ctx context.Context, userID string) (UserDetail, error) {
	const userQuery = `
SELECT
	u.id,
	u.username,
	COALESCE(u.email, ''),
	COALESCE(u.display_name, ''),
	COALESCE(u.auth_provider, 'local'),
	u.is_active,
	COALESCE(
		ARRAY(
			SELECT r.name
			FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = u.id
			ORDER BY r.name
		),
		ARRAY[]::TEXT[]
	) AS roles
FROM users u
WHERE u.id = $1
LIMIT 1;`

	var user UserDetail
	if err := s.pool.QueryRow(ctx, userQuery, strings.TrimSpace(userID)).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.DisplayName,
		&user.AuthProvider,
		&user.IsActive,
		&user.Roles,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserDetail{}, ErrUserNotFound
		}
		return UserDetail{}, fmt.Errorf("get user detail: %w", err)
	}

	const groupsQuery = `
SELECT g.id, g.name, COALESCE(g.description, '')
FROM user_groups ug
JOIN groups g ON g.id = ug.group_id
WHERE ug.user_id = $1
ORDER BY g.name ASC;`

	rows, err := s.pool.Query(ctx, groupsQuery, user.ID)
	if err != nil {
		return UserDetail{}, fmt.Errorf("list user groups: %w", err)
	}
	defer rows.Close()

	groups := make([]GroupSummary, 0)
	for rows.Next() {
		var group GroupSummary
		if scanErr := rows.Scan(&group.ID, &group.Name, &group.Description); scanErr != nil {
			return UserDetail{}, fmt.Errorf("scan user group: %w", scanErr)
		}
		groups = append(groups, group)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return UserDetail{}, fmt.Errorf("iterate user groups: %w", rowsErr)
	}
	user.Groups = groups

	return user, nil
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	const query = `
SELECT id, name, COALESCE(description, '')
FROM roles
ORDER BY name ASC;`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	roles := make([]Role, 0)
	for rows.Next() {
		var role Role
		if scanErr := rows.Scan(&role.ID, &role.Name, &role.Description); scanErr != nil {
			return nil, fmt.Errorf("scan role: %w", scanErr)
		}
		roles = append(roles, role)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate roles: %w", rowsErr)
	}

	return roles, nil
}

func (s *Service) AssignRoleToUser(ctx context.Context, userID, roleName string) error {
	uid := strings.TrimSpace(userID)
	role := strings.TrimSpace(roleName)
	if uid == "" || role == "" {
		return fmt.Errorf("user id and role name are required")
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	roleID, err := s.roleIDByName(ctx, role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoleNotFound
		}
		return err
	}

	const query = `
INSERT INTO user_roles (user_id, role_id)
VALUES ($1, $2)
ON CONFLICT (user_id, role_id) DO NOTHING;`

	if _, err := s.pool.Exec(ctx, query, uid, roleID); err != nil {
		return fmt.Errorf("assign role to user: %w", err)
	}
	return nil
}

func (s *Service) RemoveRoleFromUser(ctx context.Context, userID, roleName string) error {
	uid := strings.TrimSpace(userID)
	role := strings.TrimSpace(roleName)
	if uid == "" || role == "" {
		return fmt.Errorf("user id and role name are required")
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	roleID, err := s.roleIDByName(ctx, role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoleNotFound
		}
		return err
	}

	const query = `
DELETE FROM user_roles
WHERE user_id = $1 AND role_id = $2;`

	if _, err := s.pool.Exec(ctx, query, uid, roleID); err != nil {
		return fmt.Errorf("remove role from user: %w", err)
	}
	return nil
}

func (s *Service) ListGroups(ctx context.Context) ([]GroupSummary, error) {
	const query = `
SELECT
	g.id,
	g.name,
	COALESCE(g.description, ''),
	COUNT(ug.user_id)::INTEGER AS member_count
FROM groups g
LEFT JOIN user_groups ug ON ug.group_id = g.id
GROUP BY g.id, g.name, g.description
ORDER BY g.name ASC;`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	groups := make([]GroupSummary, 0)
	for rows.Next() {
		var group GroupSummary
		if scanErr := rows.Scan(&group.ID, &group.Name, &group.Description, &group.MemberCount); scanErr != nil {
			return nil, fmt.Errorf("scan group: %w", scanErr)
		}
		groups = append(groups, group)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate groups: %w", rowsErr)
	}

	return groups, nil
}

func (s *Service) ListGroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	gid := strings.TrimSpace(groupID)
	if gid == "" {
		return nil, fmt.Errorf("group id is required")
	}

	exists, err := s.groupExists(ctx, gid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrGroupNotFound
	}

	const query = `
SELECT u.id, u.username, COALESCE(u.email, ''), COALESCE(u.display_name, '')
FROM user_groups ug
JOIN users u ON u.id = ug.user_id
WHERE ug.group_id = $1
ORDER BY u.username ASC;`

	rows, err := s.pool.Query(ctx, query, gid)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()

	members := make([]GroupMember, 0)
	for rows.Next() {
		var member GroupMember
		if scanErr := rows.Scan(&member.ID, &member.Username, &member.Email, &member.DisplayName); scanErr != nil {
			return nil, fmt.Errorf("scan group member: %w", scanErr)
		}
		members = append(members, member)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate group members: %w", rowsErr)
	}

	return members, nil
}

func (s *Service) ListGroupGrants(ctx context.Context, groupID string) ([]GrantRecord, error) {
	gid := strings.TrimSpace(groupID)
	if gid == "" {
		return nil, fmt.Errorf("group id is required")
	}

	exists, err := s.groupExists(ctx, gid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrGroupNotFound
	}

	const query = `
SELECT
	ag.subject_type,
	ag.subject_id::TEXT,
	g.name,
	a.id,
	a.name,
	ag.action,
	ag.effect,
	ag.created_at
FROM access_grants ag
JOIN assets a ON a.id = ag.asset_id
JOIN groups g ON g.id = ag.subject_id
WHERE ag.subject_type = 'group'
  AND ag.subject_id = $1
ORDER BY a.name ASC, ag.action ASC;`

	rows, err := s.pool.Query(ctx, query, gid)
	if err != nil {
		return nil, fmt.Errorf("list group grants: %w", err)
	}
	defer rows.Close()

	grants := make([]GrantRecord, 0)
	for rows.Next() {
		var grant GrantRecord
		if scanErr := rows.Scan(
			&grant.SubjectType,
			&grant.SubjectID,
			&grant.SubjectName,
			&grant.AssetID,
			&grant.AssetName,
			&grant.Action,
			&grant.Effect,
			&grant.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan group grant: %w", scanErr)
		}
		grants = append(grants, grant)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate group grants: %w", rowsErr)
	}

	return grants, nil
}

func (s *Service) ListAssets(ctx context.Context) ([]AssetSummary, error) {
	const query = `
SELECT
	a.id,
	a.name,
	a.asset_type,
	a.host,
	a.port,
	COUNT(DISTINCT ag.id)::INTEGER AS grant_count,
	COUNT(DISTINCT c.id)::INTEGER AS credential_count
FROM assets a
LEFT JOIN access_grants ag ON ag.asset_id = a.id
LEFT JOIN credentials c ON c.asset_id = a.id
WHERE COALESCE(a.status, 'active') <> 'deleted'
GROUP BY a.id, a.name, a.asset_type, a.host, a.port
ORDER BY a.name ASC;`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	items := make([]AssetSummary, 0)
	for rows.Next() {
		var item AssetSummary
		if scanErr := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Type,
			&item.Host,
			&item.Port,
			&item.GrantCount,
			&item.CredentialCount,
		); scanErr != nil {
			return nil, fmt.Errorf("scan asset: %w", scanErr)
		}
		items = append(items, item)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate assets: %w", rowsErr)
	}

	return items, nil
}

func (s *Service) ListAssetGrants(ctx context.Context, assetID string) ([]GrantRecord, error) {
	aid := strings.TrimSpace(assetID)
	if aid == "" {
		return nil, fmt.Errorf("asset id is required")
	}

	exists, err := s.assetExists(ctx, aid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrAssetNotFound
	}

	const query = `
SELECT
	ag.subject_type,
	ag.subject_id::TEXT,
	COALESCE(u.username, g.name, ag.subject_id::TEXT) AS subject_name,
	a.id,
	a.name,
	ag.action,
	ag.effect,
	ag.created_at
FROM access_grants ag
JOIN assets a ON a.id = ag.asset_id
LEFT JOIN users u ON ag.subject_type = 'user' AND u.id = ag.subject_id
LEFT JOIN groups g ON ag.subject_type = 'group' AND g.id = ag.subject_id
WHERE ag.asset_id = $1
ORDER BY ag.subject_type ASC, subject_name ASC, ag.action ASC;`

	rows, err := s.pool.Query(ctx, query, aid)
	if err != nil {
		return nil, fmt.Errorf("list asset grants: %w", err)
	}
	defer rows.Close()

	grants := make([]GrantRecord, 0)
	for rows.Next() {
		var grant GrantRecord
		if scanErr := rows.Scan(
			&grant.SubjectType,
			&grant.SubjectID,
			&grant.SubjectName,
			&grant.AssetID,
			&grant.AssetName,
			&grant.Action,
			&grant.Effect,
			&grant.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan asset grant: %w", scanErr)
		}
		grants = append(grants, grant)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate asset grants: %w", rowsErr)
	}

	return grants, nil
}

func (s *Service) ListUserDirectGrants(ctx context.Context, userID string) ([]GrantRecord, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, fmt.Errorf("user id is required")
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	const query = `
SELECT
	ag.subject_type,
	ag.subject_id::TEXT,
	u.username,
	a.id,
	a.name,
	ag.action,
	ag.effect,
	ag.created_at
FROM access_grants ag
JOIN users u ON u.id = ag.subject_id
JOIN assets a ON a.id = ag.asset_id
WHERE ag.subject_type = 'user'
  AND ag.subject_id = $1
ORDER BY a.name ASC, ag.action ASC;`

	rows, err := s.pool.Query(ctx, query, uid)
	if err != nil {
		return nil, fmt.Errorf("list user grants: %w", err)
	}
	defer rows.Close()

	grants := make([]GrantRecord, 0)
	for rows.Next() {
		var grant GrantRecord
		if scanErr := rows.Scan(
			&grant.SubjectType,
			&grant.SubjectID,
			&grant.SubjectName,
			&grant.AssetID,
			&grant.AssetName,
			&grant.Action,
			&grant.Effect,
			&grant.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan user grant: %w", scanErr)
		}
		grants = append(grants, grant)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate user grants: %w", rowsErr)
	}

	return grants, nil
}

func (s *Service) GrantUserAllow(ctx context.Context, userID, assetID, action string, createdBy *string) error {
	uid := strings.TrimSpace(userID)
	aid := strings.TrimSpace(assetID)
	act := strings.TrimSpace(action)
	if uid == "" || aid == "" || act == "" {
		return fmt.Errorf("user id, asset id, and action are required")
	}
	if !isSupportedAction(act) {
		return fmt.Errorf("unsupported action: %s", act)
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	exists, err = s.assetExists(ctx, aid)
	if err != nil {
		return err
	}
	if !exists {
		return ErrAssetNotFound
	}
	assetType, err := s.assetTypeByID(ctx, aid)
	if err != nil {
		return err
	}
	if !isActionAllowedForAssetType(act, assetType) {
		return fmt.Errorf("action %q is not allowed for asset type %q", act, assetType)
	}

	const query = `
INSERT INTO access_grants (subject_type, subject_id, asset_id, action, effect, created_by)
VALUES ('user', $1, $2, $3, 'allow', $4)
ON CONFLICT (subject_type, subject_id, asset_id, action) DO UPDATE
SET effect = 'allow',
    created_by = EXCLUDED.created_by,
    updated_at = NOW();`

	if _, err := s.pool.Exec(ctx, query, uid, aid, act, createdBy); err != nil {
		return fmt.Errorf("grant user allow access: %w", err)
	}
	return nil
}

func (s *Service) RevokeUserAllow(ctx context.Context, userID, assetID, action string) error {
	uid := strings.TrimSpace(userID)
	aid := strings.TrimSpace(assetID)
	act := strings.TrimSpace(action)
	if uid == "" || aid == "" || act == "" {
		return fmt.Errorf("user id, asset id, and action are required")
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	const query = `
DELETE FROM access_grants
WHERE subject_type = 'user'
  AND subject_id = $1
  AND asset_id = $2
  AND action = $3;`

	if _, err := s.pool.Exec(ctx, query, uid, aid, act); err != nil {
		return fmt.Errorf("revoke user allow access: %w", err)
	}
	return nil
}

func (s *Service) UserEffectiveAccess(ctx context.Context, userID string) ([]EffectiveAccess, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, fmt.Errorf("user id is required")
	}

	exists, err := s.userExists(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	const query = `
SELECT
	a.id,
	a.name,
	ag.action,
	'direct' AS source
FROM access_grants ag
JOIN assets a ON a.id = ag.asset_id
WHERE ag.effect = 'allow'
  AND ag.subject_type = 'user'
  AND ag.subject_id = $1
UNION ALL
SELECT
	a.id,
	a.name,
	ag.action,
	'group:' || g.name AS source
FROM access_grants ag
JOIN assets a ON a.id = ag.asset_id
JOIN groups g ON g.id = ag.subject_id
JOIN user_groups ug ON ug.group_id = g.id
WHERE ag.effect = 'allow'
  AND ag.subject_type = 'group'
  AND ug.user_id = $1
ORDER BY 2 ASC, 3 ASC, 4 ASC;`

	rows, err := s.pool.Query(ctx, query, uid)
	if err != nil {
		return nil, fmt.Errorf("resolve user effective access: %w", err)
	}
	defer rows.Close()

	flat := make([]effectiveAccessRow, 0)
	for rows.Next() {
		var row effectiveAccessRow
		if scanErr := rows.Scan(&row.assetID, &row.assetName, &row.action, &row.source); scanErr != nil {
			return nil, fmt.Errorf("scan effective access: %w", scanErr)
		}
		flat = append(flat, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate effective access: %w", rowsErr)
	}

	return aggregateEffectiveAccess(flat), nil
}

func aggregateEffectiveAccess(rows []effectiveAccessRow) []EffectiveAccess {
	type actionState struct {
		sourceSet map[string]struct{}
	}

	type assetState struct {
		assetID   string
		assetName string
		actions   map[string]*actionState
	}

	assets := map[string]*assetState{}
	assetOrder := make([]string, 0)
	for _, row := range rows {
		state, ok := assets[row.assetID]
		if !ok {
			state = &assetState{
				assetID:   row.assetID,
				assetName: row.assetName,
				actions:   map[string]*actionState{},
			}
			assets[row.assetID] = state
			assetOrder = append(assetOrder, row.assetID)
		}

		action, ok := state.actions[row.action]
		if !ok {
			action = &actionState{sourceSet: map[string]struct{}{}}
			state.actions[row.action] = action
		}
		action.sourceSet[row.source] = struct{}{}
	}

	items := make([]EffectiveAccess, 0, len(assetOrder))
	for _, assetID := range assetOrder {
		state := assets[assetID]
		actions := make([]EffectiveAction, 0, len(state.actions))
		actionNames := make([]string, 0, len(state.actions))
		for action := range state.actions {
			actionNames = append(actionNames, action)
		}
		sort.Strings(actionNames)

		for _, actionName := range actionNames {
			actionState := state.actions[actionName]
			sources := make([]string, 0, len(actionState.sourceSet))
			for source := range actionState.sourceSet {
				sources = append(sources, source)
			}
			sort.Strings(sources)
			actions = append(actions, EffectiveAction{Action: actionName, Sources: sources})
		}

		items = append(items, EffectiveAccess{
			AssetID:   state.assetID,
			AssetName: state.assetName,
			Actions:   actions,
		})
	}

	return items
}

func isSupportedAction(action string) bool {
	switch action {
	case "shell", "sftp", "dbeaver", "redis":
		return true
	default:
		return false
	}
}

func isActionAllowedForAssetType(action, assetType string) bool {
	switch strings.TrimSpace(assetType) {
	case "linux_vm":
		return action == "shell" || action == "sftp"
	case "database":
		return action == "dbeaver"
	case "redis":
		return action == "redis"
	default:
		return false
	}
}

func (s *Service) userExists(ctx context.Context, userID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1);`
	var exists bool
	if err := s.pool.QueryRow(ctx, query, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check user exists: %w", err)
	}
	return exists, nil
}

func (s *Service) groupExists(ctx context.Context, groupID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1);`
	var exists bool
	if err := s.pool.QueryRow(ctx, query, groupID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check group exists: %w", err)
	}
	return exists, nil
}

func (s *Service) assetExists(ctx context.Context, assetID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM assets WHERE id = $1 AND COALESCE(status, 'active') <> 'deleted');`
	var exists bool
	if err := s.pool.QueryRow(ctx, query, assetID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check asset exists: %w", err)
	}
	return exists, nil
}

func (s *Service) assetTypeByID(ctx context.Context, assetID string) (string, error) {
	const query = `SELECT asset_type FROM assets WHERE id = $1 LIMIT 1;`
	var assetType string
	if err := s.pool.QueryRow(ctx, query, assetID).Scan(&assetType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrAssetNotFound
		}
		return "", fmt.Errorf("fetch asset type: %w", err)
	}
	return strings.TrimSpace(assetType), nil
}

func (s *Service) roleIDByName(ctx context.Context, roleName string) (string, error) {
	const query = `SELECT id FROM roles WHERE name = $1 LIMIT 1;`
	var roleID string
	if err := s.pool.QueryRow(ctx, query, roleName).Scan(&roleID); err != nil {
		return "", err
	}
	return roleID, nil
}

// CreateUserInput holds fields for creating a new local user.
type CreateUserInput struct {
	Username    string
	Password    string
	Email       string
	DisplayName string
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (UserSummary, error) {
	username := strings.TrimSpace(input.Username)
	password := strings.TrimSpace(input.Password)
	email := strings.TrimSpace(input.Email)
	displayName := strings.TrimSpace(input.DisplayName)

	if username == "" {
		return UserSummary{}, fmt.Errorf("username is required")
	}
	if len(username) < 2 || len(username) > 64 {
		return UserSummary{}, fmt.Errorf("username must be between 2 and 64 characters")
	}
	if password == "" {
		return UserSummary{}, fmt.Errorf("password is required")
	}
	if len(password) < 8 {
		return UserSummary{}, fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := bcryptHash(password)
	if err != nil {
		return UserSummary{}, fmt.Errorf("hash password: %w", err)
	}

	const query = `
INSERT INTO users (username, password_hash, email, display_name, is_active, auth_provider)
VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), true, 'local')
RETURNING id;`

	var userID string
	if err := s.pool.QueryRow(ctx, query, username, hash, email, displayName).Scan(&userID); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return UserSummary{}, fmt.Errorf("username %q already exists", username)
		}
		return UserSummary{}, fmt.Errorf("create user: %w", err)
	}

	return UserSummary{
		ID:           userID,
		Username:     username,
		Email:        email,
		DisplayName:  displayName,
		AuthProvider: "local",
		IsActive:     true,
		Roles:        []string{},
	}, nil
}

// UpdateUserInput holds fields for updating an existing user.
type UpdateUserInput struct {
	Email       *string
	DisplayName *string
}

func (s *Service) UpdateUser(ctx context.Context, userID string, input UpdateUserInput) error {
	trimmedID := strings.TrimSpace(userID)
	if trimmedID == "" {
		return fmt.Errorf("user id is required")
	}
	exists, err := s.userExists(ctx, trimmedID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	const query = `
UPDATE users
SET email = COALESCE(NULLIF($2, ''), email),
    display_name = COALESCE(NULLIF($3, ''), display_name)
WHERE id = $1;`

	emailVal := ""
	if input.Email != nil {
		emailVal = strings.TrimSpace(*input.Email)
	}
	displayVal := ""
	if input.DisplayName != nil {
		displayVal = strings.TrimSpace(*input.DisplayName)
	}

	if _, err := s.pool.Exec(ctx, query, trimmedID, emailVal, displayVal); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

func (s *Service) SetUserActive(ctx context.Context, userID string, active bool) error {
	trimmedID := strings.TrimSpace(userID)
	if trimmedID == "" {
		return fmt.Errorf("user id is required")
	}
	exists, err := s.userExists(ctx, trimmedID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	const query = `UPDATE users SET is_active = $2 WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, query, trimmedID, active); err != nil {
		return fmt.Errorf("set user active: %w", err)
	}
	return nil
}

func (s *Service) ResetUserPassword(ctx context.Context, userID, newPassword string) error {
	trimmedID := strings.TrimSpace(userID)
	password := strings.TrimSpace(newPassword)
	if trimmedID == "" {
		return fmt.Errorf("user id is required")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	exists, err := s.userExists(ctx, trimmedID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	hash, err := bcryptHash(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	const query = `UPDATE users SET password_hash = $2 WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, query, trimmedID, hash); err != nil {
		return fmt.Errorf("reset user password: %w", err)
	}
	return nil
}

func (s *Service) DeleteAsset(ctx context.Context, assetID string) error {
	trimmedID := strings.TrimSpace(assetID)
	if trimmedID == "" {
		return fmt.Errorf("asset id is required")
	}
	exists, err := s.assetExists(ctx, trimmedID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrAssetNotFound
	}

	// Preserve historical references (sessions/audit) by archiving the asset
	// instead of hard-deleting it. We still remove active access + credentials.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM access_grants WHERE asset_id = $1`, trimmedID); err != nil {
		return fmt.Errorf("delete asset grants: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM credentials WHERE asset_id = $1`, trimmedID); err != nil {
		return fmt.Errorf("delete asset credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM asset_protocols WHERE asset_id = $1`, trimmedID); err != nil {
		return fmt.Errorf("delete asset protocols: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE assets
SET status = 'deleted',
    name = CASE
      WHEN name LIKE '% [deleted-%]' THEN name
      ELSE name || ' [deleted-' || SUBSTRING(id::text, 1, 8) || ']'
    END,
    updated_at = NOW()
WHERE id = $1`, trimmedID); err != nil {
		return fmt.Errorf("archive asset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit archive asset: %w", err)
	}
	return nil
}

func bcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
