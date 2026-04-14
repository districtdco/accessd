package sessions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/districtdco/accessd/api/internal/assets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ProtocolSSH   = "ssh"
	ProtocolSFTP  = "sftp"
	ProtocolDB    = "database"
	ProtocolRedis = "redis"

	StatusPending   = "pending"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

const (
	EventLaunchCreated    = "launch_created"
	EventConnectorReq     = "connector_launch_requested"
	EventConnectorSuccess = "connector_launch_succeeded"
	EventConnectorFailed  = "connector_launch_failed"
	EventDBQuery          = "db_query"
	EventRedisCommand     = "redis_command"
	EventFileOperation    = "file_operation"
	EventProxyConnected   = "proxy_connected"
	EventUpstreamConn     = "upstream_connected"
	EventShellStarted     = "shell_started"
	EventDataIn           = "data_in"
	EventDataOut          = "data_out"
	EventTerminalResize   = "terminal_resize"
	EventSessionEnded     = "session_ended"
	EventSessionFailed    = "session_failed"
	auditEventSessionOpen = "session_start"
	auditEventSessionDone = "session_end"
)

var materializationEventsByAction = map[string][]string{
	"shell":   {EventProxyConnected},
	"dbeaver": {EventProxyConnected, EventDBQuery},
	"sftp":    {EventProxyConnected, EventFileOperation},
	"redis":   {EventProxyConnected, EventRedisCommand},
}

var (
	ErrUnauthorizedLaunch = errors.New("unauthorized launch token")
	ErrLaunchExpired      = errors.New("launch token expired")
	ErrSessionNotFound    = errors.New("session not found")
	ErrAuditEventNotFound = errors.New("audit event not found")
)

type Config struct {
	LaunchTokenSecret  []byte
	LaunchTokenTTL     time.Duration
	ConnectorSecret    []byte // HMAC key for signing connector launch payloads
	MaterializeTimeout time.Duration
	ProxyHost          string
	ProxyPort          int
	ProxyUsername      string
}

type Service struct {
	pool            *pgxpool.Pool
	logger          *slog.Logger
	cfg             Config
	tokens          *TokenSigner
	connectorTokens *TokenSigner // Signs connector launch payloads (may be nil if no secret configured)
	bootstrapTokens *ConnectorBootstrapSigner
}

type CreateLaunchInput struct {
	UserID      string
	AssetID     string
	Action      string
	Protocol    string
	RequestID   string
	ClientIP    string
	UserAgent   string
	RequestedAt time.Time
}

type LaunchResult struct {
	SessionID      string
	Action         string
	AssetType      string
	LaunchType     string
	ExpiresAt      time.Time
	ConnectorToken string // HMAC-signed token the connector must verify before launching
	Shell          *ShellLaunchPayload
	SFTP           *SFTPLaunchPayload
	DBeaver        *DBeaverLaunchPayload
	Redis          *RedisLaunchPayload
}

type ShellLaunchPayload struct {
	ProxyHost        string
	ProxyPort        int
	ProxyUsername    string
	UpstreamUsername string
	TargetAssetName  string
	TargetHost       string
	Token            string
}

type SFTPLaunchPayload struct {
	Host             string
	Port             int
	ProxyUsername    string
	UpstreamUsername string
	TargetAssetName  string
	TargetHost       string
	Password         string
	Path             string
}

type DBeaverLaunchPayload struct {
	Engine           string
	Host             string
	Port             int
	Database         string
	Username         string
	UpstreamUsername string
	TargetAssetName  string
	TargetHost       string
	Password         string
	SSLMode          string
}

type RedisLaunchPayload struct {
	Host                  string
	Port                  int
	Username              string
	Password              string
	Database              int
	UseTLS                bool
	InsecureSkipVerifyTLS bool
}

type LaunchContext struct {
	SessionID        string
	UserID           string
	AssetID          string
	AssetName        string
	RequestID        string
	Action           string
	Protocol         string
	AssetType        string
	Host             string
	Port             int
	UpstreamUsername string
	Status           string
	ExpiresAt        time.Time
}

type SessionListFilter struct {
	UserID    string
	AssetID   string
	Status    string
	Action    string
	AssetType string
	From      *time.Time
	To        *time.Time
	Limit     int
}

type SessionSummary struct {
	SessionID       string
	UserID          string
	Username        string
	AssetID         string
	AssetName       string
	AssetType       string
	Action          string
	LaunchType      string
	Status          string
	StartedAt       *time.Time
	EndedAt         *time.Time
	CreatedAt       time.Time
	DurationSeconds *int64
}

type SessionDetail struct {
	SessionID       string
	UserID          string
	Username        string
	AssetID         string
	AssetName       string
	AssetType       string
	Protocol        string
	Action          string
	LaunchType      string
	Status          string
	LaunchedVia     string
	StartedAt       *time.Time
	EndedAt         *time.Time
	CreatedAt       time.Time
	DurationSeconds *int64
}

type SessionLifecycleSummary struct {
	Started            bool
	Ended              bool
	Failed             bool
	ShellStarted       bool
	ConnectorRequested bool
	ConnectorSucceeded bool
	ConnectorFailed    bool
	EventCount         int64
	FirstEventAt       *time.Time
	LastEventAt        *time.Time
}

type SessionEvent struct {
	ID          int64
	EventType   string
	EventTime   time.Time
	ActorUserID *string
	ActorUser   *string
	Payload     map[string]any
}

type ReplayChunk struct {
	EventID   int64
	EventTime time.Time
	EventType string
	Direction string
	Stream    string
	Size      int
	Text      string
	Encoded   string
	OffsetSec float64
	DelaySec  float64
	Cols      int
	Rows      int
	Asciicast []any
}

type AdminSummary struct {
	WindowDays      int
	RecentSessions  int64
	ActiveSessions  int64
	FailedSessions  int64
	ShellLaunches   int64
	DBeaverLaunches int64
	ByAction        []ActionCount
}

type ActionCount struct {
	Action string
	Count  int64
}

type AuditItem struct {
	ID          int64
	EventTime   time.Time
	EventType   string
	Action      string
	Outcome     string
	ActorUserID *string
	ActorUser   *string
	AssetID     *string
	AssetName   *string
	AssetType   *string
	SessionID   *string
	Metadata    map[string]any
	Session     *AuditSessionSummary
}

type AuditSessionSummary struct {
	ID        string
	Action    string
	Status    string
	CreatedAt time.Time
}

type AuditListFilter struct {
	EventType string
	UserID    string
	AssetID   string
	SessionID string
	Action    string
	From      *time.Time
	To        *time.Time
	Limit     int
}

func NewService(pool *pgxpool.Pool, cfg Config, logger *slog.Logger) (*Service, error) {
	if pool == nil {
		return nil, fmt.Errorf("db pool is required")
	}
	if len(cfg.LaunchTokenSecret) == 0 {
		return nil, fmt.Errorf("launch token secret is required")
	}
	if cfg.LaunchTokenTTL <= 0 {
		return nil, fmt.Errorf("launch token ttl must be > 0")
	}
	if cfg.MaterializeTimeout <= 0 {
		cfg.MaterializeTimeout = 45 * time.Second
	}
	if strings.TrimSpace(cfg.ProxyHost) == "" {
		return nil, fmt.Errorf("proxy host is required")
	}
	if cfg.ProxyPort <= 0 || cfg.ProxyPort > 65535 {
		return nil, fmt.Errorf("proxy port is invalid")
	}
	if strings.TrimSpace(cfg.ProxyUsername) == "" {
		return nil, fmt.Errorf("proxy username is required")
	}

	svc := &Service{
		pool:            pool,
		cfg:             cfg,
		tokens:          NewTokenSigner(cfg.LaunchTokenSecret),
		bootstrapTokens: NewConnectorBootstrapSigner(cfg.LaunchTokenSecret),
		logger:          logger.With("component", "sessions"),
	}
	if len(cfg.ConnectorSecret) > 0 {
		svc.connectorTokens = NewTokenSigner(cfg.ConnectorSecret)
	}
	return svc, nil
}

func (s *Service) CreateLaunch(ctx context.Context, input CreateLaunchInput) (LaunchResult, error) {
	now := input.RequestedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	userID := strings.TrimSpace(input.UserID)
	assetID := strings.TrimSpace(input.AssetID)
	action := strings.TrimSpace(input.Action)
	protocol := strings.TrimSpace(input.Protocol)
	clientIP := normalizeIP(input.ClientIP)
	userAgent := strings.TrimSpace(input.UserAgent)
	requestID := strings.TrimSpace(input.RequestID)
	if userID == "" || assetID == "" || action == "" || protocol == "" {
		return LaunchResult{}, fmt.Errorf("user_id, asset_id, action, and protocol are required")
	}

	const insertSession = `
INSERT INTO sessions (user_id, asset_id, protocol, status, launched_via, reason, client_ip)
VALUES ($1, $2, $3, $4, 'api', $5, NULLIF($6, '')::inet)
RETURNING id;`

	var sessionID string
	reason := "action:" + action
	if err := s.pool.QueryRow(ctx, insertSession, userID, assetID, protocol, StatusPending, reason, clientIP).Scan(&sessionID); err != nil {
		return LaunchResult{}, fmt.Errorf("create session: %w", err)
	}

	expiresAt := now.Add(s.cfg.LaunchTokenTTL).UTC()

	if err := s.WriteEvent(ctx, sessionID, EventLaunchCreated, &userID, map[string]any{
		"action":      action,
		"protocol":    protocol,
		"expires_at":  expiresAt.Format(time.RFC3339Nano),
		"client_ip":   clientIP,
		"user_agent":  userAgent,
		"request_id":  requestID,
		"created_via": "api",
	}); err != nil {
		return LaunchResult{}, err
	}

	result := LaunchResult{
		SessionID: sessionID,
		Action:    action,
		ExpiresAt: expiresAt,
	}

	// Sign a connector token so the local connector can verify this launch is backend-authorized.
	if s.connectorTokens != nil {
		ct, ctErr := s.connectorTokens.Sign(LaunchTokenClaims{
			SessionID: sessionID,
			UserID:    userID,
			AssetID:   assetID,
			Action:    action,
			RequestID: requestID,
			ExpiresAt: expiresAt.Unix(),
		})
		if ctErr != nil {
			return LaunchResult{}, fmt.Errorf("sign connector token: %w", ctErr)
		}
		result.ConnectorToken = ct
	}

	if protocol == ProtocolSSH && action == "shell" {
		token, err := s.tokens.Sign(LaunchTokenClaims{
			SessionID: sessionID,
			UserID:    userID,
			AssetID:   assetID,
			Action:    action,
			RequestID: requestID,
			ExpiresAt: expiresAt.Unix(),
		})
		if err != nil {
			return LaunchResult{}, fmt.Errorf("sign launch token: %w", err)
		}
		result.LaunchType = "shell"
		result.AssetType = assets.TypeLinuxVM
		result.Shell = &ShellLaunchPayload{
			ProxyHost:        s.cfg.ProxyHost,
			ProxyPort:        s.cfg.ProxyPort,
			ProxyUsername:    s.cfg.ProxyUsername,
			UpstreamUsername: s.cfg.ProxyUsername,
			TargetHost:       s.cfg.ProxyHost,
			Token:            token,
		}
	}
	if protocol == ProtocolSFTP && action == "sftp" {
		token, err := s.tokens.Sign(LaunchTokenClaims{
			SessionID: sessionID,
			UserID:    userID,
			AssetID:   assetID,
			Action:    action,
			RequestID: requestID,
			ExpiresAt: expiresAt.Unix(),
		})
		if err != nil {
			return LaunchResult{}, fmt.Errorf("sign launch token: %w", err)
		}
		result.LaunchType = "sftp"
		result.AssetType = assets.TypeLinuxVM
		result.SFTP = &SFTPLaunchPayload{
			Host:             s.cfg.ProxyHost,
			Port:             s.cfg.ProxyPort,
			ProxyUsername:    s.cfg.ProxyUsername,
			UpstreamUsername: s.cfg.ProxyUsername,
			TargetHost:       s.cfg.ProxyHost,
			Password:         token,
		}
	}
	if protocol == ProtocolRedis && action == "redis" {
		token, err := s.tokens.Sign(LaunchTokenClaims{
			SessionID: sessionID,
			UserID:    userID,
			AssetID:   assetID,
			Action:    action,
			RequestID: requestID,
			ExpiresAt: expiresAt.Unix(),
		})
		if err != nil {
			return LaunchResult{}, fmt.Errorf("sign launch token: %w", err)
		}
		result.LaunchType = "redis"
		result.AssetType = assets.TypeRedis
		result.Redis = &RedisLaunchPayload{
			Password: token,
		}
	}

	return result, nil
}

func (s *Service) ConnectorTokenEnabled() bool {
	return s.connectorTokens != nil
}

// VerifyConnectorToken validates a connector launch token and returns claims.
func (s *Service) VerifyConnectorToken(token string) (LaunchTokenClaims, error) {
	if s.connectorTokens == nil {
		return LaunchTokenClaims{}, fmt.Errorf("connector token verification is not configured")
	}
	claims, err := s.connectorTokens.Verify(strings.TrimSpace(token))
	if err != nil {
		if errors.Is(err, ErrLaunchExpired) {
			return LaunchTokenClaims{}, err
		}
		return LaunchTokenClaims{}, ErrUnauthorizedLaunch
	}
	return claims, nil
}

func (s *Service) IssueConnectorBootstrapToken(userID, origin string, ttl time.Duration) (string, ConnectorBootstrapClaims, error) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return "", ConnectorBootstrapClaims{}, fmt.Errorf("origin is required")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	claims := ConnectorBootstrapClaims{
		Origin:        origin,
		BackendVerify: origin + "/api/connector/token/verify",
		IssuedForUser: strings.TrimSpace(userID),
		ExpiresAt:     time.Now().Add(ttl).Unix(),
	}
	token, err := s.bootstrapTokens.Sign(claims)
	if err != nil {
		return "", ConnectorBootstrapClaims{}, err
	}
	return token, claims, nil
}

func (s *Service) VerifyConnectorBootstrapToken(token string) (ConnectorBootstrapClaims, error) {
	if s.bootstrapTokens == nil {
		return ConnectorBootstrapClaims{}, fmt.Errorf("bootstrap token verification is not configured")
	}
	claims, err := s.bootstrapTokens.Verify(strings.TrimSpace(token))
	if err != nil {
		if errors.Is(err, ErrLaunchExpired) {
			return ConnectorBootstrapClaims{}, err
		}
		return ConnectorBootstrapClaims{}, ErrUnauthorizedLaunch
	}
	return claims, nil
}

func (s *Service) ResolveLaunchToken(ctx context.Context, token string) (LaunchContext, error) {
	claims, err := s.tokens.Verify(strings.TrimSpace(token))
	if err != nil {
		if errors.Is(err, ErrLaunchExpired) {
			return LaunchContext{}, err
		}
		return LaunchContext{}, ErrUnauthorizedLaunch
	}

	const query = `
SELECT
    s.id,
    s.user_id,
    s.asset_id,
    a.name,
    s.protocol,
    s.status,
    COALESCE(s.reason, ''),
    a.asset_type,
    a.host,
    a.port
FROM sessions s
JOIN assets a ON a.id = s.asset_id
WHERE s.id = $1
LIMIT 1;`

	var (
		lctx   LaunchContext
		reason string
	)
	if err := s.pool.QueryRow(ctx, query, claims.SessionID).Scan(
		&lctx.SessionID,
		&lctx.UserID,
		&lctx.AssetID,
		&lctx.AssetName,
		&lctx.Protocol,
		&lctx.Status,
		&reason,
		&lctx.AssetType,
		&lctx.Host,
		&lctx.Port,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LaunchContext{}, ErrUnauthorizedLaunch
		}
		return LaunchContext{}, fmt.Errorf("resolve launch session: %w", err)
	}

	lctx.Action = strings.TrimPrefix(reason, "action:")
	lctx.RequestID = strings.TrimSpace(claims.RequestID)
	lctx.ExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()

	if lctx.UserID != claims.UserID || lctx.AssetID != claims.AssetID || lctx.Action != claims.Action {
		return LaunchContext{}, ErrUnauthorizedLaunch
	}
	isShellLaunch := lctx.Protocol == ProtocolSSH && claims.Action == "shell"
	isSFTPLaunch := lctx.Protocol == ProtocolSFTP && claims.Action == "sftp"
	isRedisLaunch := lctx.Protocol == ProtocolRedis && claims.Action == "redis"
	if !isShellLaunch && !isSFTPLaunch && !isRedisLaunch {
		return LaunchContext{}, ErrUnauthorizedLaunch
	}
	if isRedisLaunch {
		if lctx.AssetType != assets.TypeRedis {
			return LaunchContext{}, ErrUnauthorizedLaunch
		}
	} else if lctx.AssetType != assets.TypeLinuxVM {
		return LaunchContext{}, ErrUnauthorizedLaunch
	}
	if isRedisLaunch {
		if lctx.Status != StatusPending && lctx.Status != StatusActive {
			return LaunchContext{}, ErrUnauthorizedLaunch
		}
	} else if lctx.Status != StatusPending {
		return LaunchContext{}, ErrUnauthorizedLaunch
	}

	return lctx, nil
}

func (s *Service) AttachDBeaverPayload(result LaunchResult, payload DBeaverLaunchPayload) LaunchResult {
	result.LaunchType = "dbeaver"
	result.AssetType = assets.TypeDatabase
	result.DBeaver = &payload
	return result
}

func (s *Service) AttachShellPayload(result LaunchResult, payload ShellLaunchPayload) LaunchResult {
	result.LaunchType = "shell"
	result.AssetType = assets.TypeLinuxVM
	result.Shell = &payload
	return result
}

func (s *Service) AttachSFTPPayload(result LaunchResult, payload SFTPLaunchPayload) LaunchResult {
	result.LaunchType = "sftp"
	result.AssetType = assets.TypeLinuxVM
	result.SFTP = &payload
	return result
}

func (s *Service) AttachRedisPayload(result LaunchResult, payload RedisLaunchPayload) LaunchResult {
	result.LaunchType = "redis"
	result.AssetType = assets.TypeRedis
	result.Redis = &payload
	return result
}

func (s *Service) RecordConnectorLaunchEvent(
	ctx context.Context,
	sessionID, userID, eventType string,
	payload map[string]any,
) error {
	if eventType != EventConnectorReq && eventType != EventConnectorSuccess && eventType != EventConnectorFailed {
		return fmt.Errorf("unsupported connector event type: %s", eventType)
	}

	lctx, err := s.GetSessionContextForUser(ctx, sessionID, userID)
	if err != nil {
		return err
	}

	if err := s.WriteEvent(ctx, lctx.SessionID, eventType, &lctx.UserID, payload); err != nil {
		return err
	}
	return nil
}

func (s *Service) FailUnmaterializedConnectorLaunches(ctx context.Context, timeout time.Duration, limit int) (int, error) {
	if timeout <= 0 {
		timeout = s.cfg.MaterializeTimeout
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	cutoff := time.Now().UTC().Add(-timeout)

	const query = `
SELECT
	s.id,
	s.user_id,
	s.asset_id,
	a.name,
	s.protocol,
	COALESCE(s.reason, ''),
	a.asset_type,
	a.host,
	a.port
FROM sessions s
JOIN assets a ON a.id = s.asset_id
JOIN (
	SELECT session_id, MAX(event_time) AS succeeded_at
	FROM session_events
	WHERE event_type = $1
	GROUP BY session_id
) success_evt ON success_evt.session_id = s.id
WHERE s.status = $2
  AND success_evt.succeeded_at <= $3
ORDER BY success_evt.succeeded_at ASC
LIMIT $4;`

	rows, err := s.pool.Query(ctx, query, EventConnectorSuccess, StatusPending, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("query stale pending launches: %w", err)
	}
	defer rows.Close()

	marked := 0
	for rows.Next() {
		var (
			lctx   LaunchContext
			reason string
		)
		if scanErr := rows.Scan(
			&lctx.SessionID,
			&lctx.UserID,
			&lctx.AssetID,
			&lctx.AssetName,
			&lctx.Protocol,
			&reason,
			&lctx.AssetType,
			&lctx.Host,
			&lctx.Port,
		); scanErr != nil {
			return marked, fmt.Errorf("scan stale pending launch: %w", scanErr)
		}
		lctx.Action = strings.TrimPrefix(reason, "action:")
		materialized, matErr := s.launchMaterialized(ctx, lctx.SessionID, lctx.Action)
		if matErr != nil {
			return marked, fmt.Errorf("check launch materialization: %w", matErr)
		}
		if materialized {
			continue
		}

		const updatePending = `
UPDATE sessions
SET status = $2,
	ended_at = NOW()
WHERE id = $1
  AND status = $3;`
		tag, execErr := s.pool.Exec(ctx, updatePending, lctx.SessionID, StatusFailed, StatusPending)
		if execErr != nil {
			return marked, fmt.Errorf("mark stale launch failed: %w", execErr)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		expected := expectedMaterialization(lctx.Action)
		failPayload := map[string]any{
			"reason":                   "launch_materialization_timeout",
			"expected_materialization": expected,
		}
		if err := s.WriteEvent(ctx, lctx.SessionID, EventSessionFailed, &lctx.UserID, failPayload); err != nil {
			return marked, err
		}
		if err := s.writeAudit(ctx, lctx, auditEventSessionDone, sessionEndAuditAction(lctx.Action), "failed", failPayload); err != nil {
			return marked, err
		}
		marked++
	}
	if err := rows.Err(); err != nil {
		return marked, fmt.Errorf("iterate stale pending launches: %w", err)
	}
	return marked, nil
}

func (s *Service) launchMaterialized(ctx context.Context, sessionID, action string) (bool, error) {
	events := materializationEventsByAction[strings.TrimSpace(action)]
	if len(events) == 0 {
		events = []string{EventProxyConnected}
	}
	const query = `
SELECT EXISTS (
	SELECT 1
	FROM session_events
	WHERE session_id = $1
	  AND event_type = ANY($2::text[])
)`
	var exists bool
	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(sessionID), events).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func expectedMaterialization(action string) string {
	switch strings.TrimSpace(action) {
	case "shell":
		return EventProxyConnected
	case "dbeaver":
		return EventProxyConnected + " or " + EventDBQuery
	case "sftp":
		return EventProxyConnected + " or " + EventFileOperation
	case "redis":
		return EventProxyConnected + " or " + EventRedisCommand
	default:
		return EventProxyConnected
	}
}

func (s *Service) GetSessionContextForUser(ctx context.Context, sessionID, userID string) (LaunchContext, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(userID) == "" {
		return LaunchContext{}, fmt.Errorf("session id and user id are required")
	}

	const query = `
SELECT
    s.id,
    s.user_id,
    s.asset_id,
    a.name,
    s.protocol,
    s.status,
    COALESCE(s.reason, ''),
    a.asset_type,
    a.host,
    a.port
FROM sessions s
JOIN assets a ON a.id = s.asset_id
WHERE s.id = $1
  AND s.user_id = $2
LIMIT 1;`

	var (
		lctx   LaunchContext
		reason string
	)

	if err := s.pool.QueryRow(ctx, query, strings.TrimSpace(sessionID), strings.TrimSpace(userID)).Scan(
		&lctx.SessionID,
		&lctx.UserID,
		&lctx.AssetID,
		&lctx.AssetName,
		&lctx.Protocol,
		&lctx.Status,
		&reason,
		&lctx.AssetType,
		&lctx.Host,
		&lctx.Port,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LaunchContext{}, ErrUnauthorizedLaunch
		}
		return LaunchContext{}, fmt.Errorf("get session context: %w", err)
	}

	lctx.Action = strings.TrimPrefix(reason, "action:")
	return lctx, nil
}

func (s *Service) ListForUser(ctx context.Context, userID string, filter SessionListFilter) ([]SessionSummary, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, fmt.Errorf("user id is required")
	}
	filter.UserID = uid
	return s.listSessions(ctx, filter)
}

func (s *Service) ListForAdmin(ctx context.Context, filter SessionListFilter) ([]SessionSummary, error) {
	return s.listSessions(ctx, filter)
}

func (s *Service) GetDetail(ctx context.Context, sessionID string) (SessionDetail, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return SessionDetail{}, fmt.Errorf("session id is required")
	}

	const query = `
SELECT
    s.id,
    u.id,
    u.username,
    a.id,
    a.name,
    a.asset_type,
    s.protocol,
    COALESCE(s.reason, ''),
    s.status,
    s.launched_via,
    s.started_at,
    s.ended_at,
    s.created_at,
    CASE
        WHEN s.started_at IS NULL THEN NULL
        WHEN s.ended_at IS NOT NULL THEN GREATEST(EXTRACT(EPOCH FROM (s.ended_at - s.started_at)), 0)::BIGINT
        WHEN s.status = 'active' THEN GREATEST(EXTRACT(EPOCH FROM (NOW() - s.started_at)), 0)::BIGINT
        ELSE NULL
    END AS duration_seconds
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN assets a ON a.id = s.asset_id
WHERE s.id = $1
LIMIT 1;`

	var (
		item     SessionDetail
		reason   string
		started  pgtype.Timestamptz
		ended    pgtype.Timestamptz
		duration pgtype.Int8
	)

	if err := s.pool.QueryRow(ctx, query, sid).Scan(
		&item.SessionID,
		&item.UserID,
		&item.Username,
		&item.AssetID,
		&item.AssetName,
		&item.AssetType,
		&item.Protocol,
		&reason,
		&item.Status,
		&item.LaunchedVia,
		&started,
		&ended,
		&item.CreatedAt,
		&duration,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionDetail{}, ErrSessionNotFound
		}
		return SessionDetail{}, fmt.Errorf("get session detail: %w", err)
	}

	item.Action = actionFromReason(reason, item.Protocol)
	item.LaunchType = launchTypeForAction(item.Action)
	item.StartedAt = nullableTime(started)
	item.EndedAt = nullableTime(ended)
	item.DurationSeconds = nullableInt64(duration)

	return item, nil
}

func (s *Service) GetLifecycleSummary(ctx context.Context, sessionID string) (SessionLifecycleSummary, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return SessionLifecycleSummary{}, fmt.Errorf("session id is required")
	}

	const query = `
SELECT
	COUNT(*)::BIGINT AS event_count,
	COALESCE(BOOL_OR(event_type = 'shell_started'), FALSE) AS shell_started,
	COALESCE(BOOL_OR(event_type = 'connector_launch_requested'), FALSE) AS connector_requested,
	COALESCE(BOOL_OR(event_type = 'connector_launch_succeeded'), FALSE) AS connector_succeeded,
	COALESCE(BOOL_OR(event_type = 'connector_launch_failed'), FALSE) AS connector_failed,
	COALESCE(BOOL_OR(event_type = 'session_ended'), FALSE) AS ended,
	COALESCE(BOOL_OR(event_type = 'session_failed'), FALSE) AS failed,
	MIN(event_time) AS first_event_at,
	MAX(event_time) AS last_event_at
FROM session_events
WHERE session_id = $1;`

	var (
		summary SessionLifecycleSummary
		first   pgtype.Timestamptz
		last    pgtype.Timestamptz
	)
	if err := s.pool.QueryRow(ctx, query, sid).Scan(
		&summary.EventCount,
		&summary.ShellStarted,
		&summary.ConnectorRequested,
		&summary.ConnectorSucceeded,
		&summary.ConnectorFailed,
		&summary.Ended,
		&summary.Failed,
		&first,
		&last,
	); err != nil {
		return SessionLifecycleSummary{}, fmt.Errorf("get lifecycle summary: %w", err)
	}

	summary.FirstEventAt = nullableTime(first)
	summary.LastEventAt = nullableTime(last)
	return summary, nil
}

func (s *Service) ListEvents(ctx context.Context, sessionID string, afterID int64, limit int) ([]SessionEvent, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, fmt.Errorf("session id is required")
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if afterID < 0 {
		return nil, fmt.Errorf("after_id must be >= 0")
	}

	var afterPtr *int64
	if afterID > 0 {
		afterPtr = &afterID
	}

	const query = `
SELECT
    se.id,
    se.event_type,
    se.event_time,
    se.actor_user_id::TEXT,
    au.username,
    se.payload
FROM session_events se
LEFT JOIN users au ON au.id = se.actor_user_id
WHERE se.session_id = $1
  AND ($2::BIGINT IS NULL OR se.id > $2)
ORDER BY se.id ASC
LIMIT $3;`

	rows, err := s.pool.Query(ctx, query, sid, afterPtr, limit)
	if err != nil {
		return nil, fmt.Errorf("list session events: %w", err)
	}
	defer rows.Close()

	items := make([]SessionEvent, 0)
	for rows.Next() {
		var (
			item        SessionEvent
			payloadRaw  []byte
			actorUserID pgtype.Text
			actorName   pgtype.Text
		)
		if scanErr := rows.Scan(
			&item.ID,
			&item.EventType,
			&item.EventTime,
			&actorUserID,
			&actorName,
			&payloadRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("scan session event: %w", scanErr)
		}

		if actorUserID.Valid {
			value := actorUserID.String
			item.ActorUserID = &value
		}
		if actorName.Valid {
			value := actorName.String
			item.ActorUser = &value
		}

		item.Payload = map[string]any{}
		if len(payloadRaw) > 0 {
			if decodeErr := json.Unmarshal(payloadRaw, &item.Payload); decodeErr != nil {
				return nil, fmt.Errorf("decode session event payload: %w", decodeErr)
			}
		}
		items = append(items, item)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate session events: %w", rowsErr)
	}

	return items, nil
}

func (s *Service) ListReplayChunks(ctx context.Context, sessionID string, afterID int64, limit int) ([]ReplayChunk, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if afterID < 0 {
		return nil, fmt.Errorf("after_id must be >= 0")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	var afterPtr *int64
	if afterID > 0 {
		afterPtr = &afterID
	}

	const query = `
SELECT
	id,
	event_type,
	event_time,
	payload
FROM session_events
WHERE session_id = $1
  AND event_type IN ('data_in', 'data_out', 'terminal_resize')
  AND ($2::BIGINT IS NULL OR id > $2)
ORDER BY id ASC
LIMIT $3;`

	rows, err := s.pool.Query(ctx, query, sid, afterPtr, limit)
	if err != nil {
		return nil, fmt.Errorf("list replay chunks: %w", err)
	}
	defer rows.Close()

	chunks := make([]ReplayChunk, 0)
	for rows.Next() {
		var (
			eventID   int64
			eventType string
			eventTime time.Time
			payload   map[string]any
		)
		if scanErr := rows.Scan(&eventID, &eventType, &eventTime, &payload); scanErr != nil {
			return nil, fmt.Errorf("scan replay chunk: %w", scanErr)
		}

		chunk, ok := replayChunkFromPayload(eventID, eventType, eventTime, payload)
		if !ok {
			continue
		}
		chunks = append(chunks, chunk)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate replay chunks: %w", rowsErr)
	}
	return normalizeReplayChunks(chunks), nil
}

func (s *Service) GetAdminSummary(ctx context.Context, windowDays int) (AdminSummary, error) {
	if windowDays <= 0 {
		windowDays = 7
	}
	if windowDays > 90 {
		windowDays = 90
	}

	windowStart := time.Now().UTC().AddDate(0, 0, -windowDays)
	summary := AdminSummary{
		WindowDays: windowDays,
		ByAction:   make([]ActionCount, 0),
	}

	const countsQuery = `
SELECT
	COUNT(*)::BIGINT AS recent_sessions,
	COUNT(*) FILTER (WHERE status = 'failed')::BIGINT AS failed_sessions,
	COUNT(*) FILTER (WHERE reason = 'action:shell')::BIGINT AS shell_launches,
	COUNT(*) FILTER (WHERE reason = 'action:dbeaver')::BIGINT AS dbeaver_launches
FROM sessions
WHERE created_at >= $1;`
	if err := s.pool.QueryRow(ctx, countsQuery, windowStart).Scan(
		&summary.RecentSessions,
		&summary.FailedSessions,
		&summary.ShellLaunches,
		&summary.DBeaverLaunches,
	); err != nil {
		return AdminSummary{}, fmt.Errorf("get admin summary counts: %w", err)
	}

	const activeQuery = `
SELECT COUNT(*)::BIGINT
FROM sessions
WHERE status = 'active';`
	if err := s.pool.QueryRow(ctx, activeQuery).Scan(&summary.ActiveSessions); err != nil {
		return AdminSummary{}, fmt.Errorf("get admin summary active sessions: %w", err)
	}

	const byActionQuery = `
SELECT
	CASE
		WHEN reason LIKE 'action:%' THEN SUBSTRING(reason FROM 8)
		ELSE protocol
	END AS action,
	COUNT(*)::BIGINT AS count
FROM sessions
WHERE created_at >= $1
GROUP BY 1
ORDER BY count DESC, action ASC;`

	rows, err := s.pool.Query(ctx, byActionQuery, windowStart)
	if err != nil {
		return AdminSummary{}, fmt.Errorf("get admin summary actions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var row ActionCount
		if scanErr := rows.Scan(&row.Action, &row.Count); scanErr != nil {
			return AdminSummary{}, fmt.Errorf("scan admin summary action row: %w", scanErr)
		}
		summary.ByAction = append(summary.ByAction, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return AdminSummary{}, fmt.Errorf("iterate admin summary actions: %w", rowsErr)
	}

	return summary, nil
}

func (s *Service) ListActiveForAdmin(ctx context.Context, limit int) ([]SessionSummary, error) {
	return s.listSessions(ctx, SessionListFilter{
		Status: StatusActive,
		Limit:  limit,
	})
}

func (s *Service) ListRecentAudit(ctx context.Context, limit int) ([]AuditItem, error) {
	return s.ListAuditEvents(ctx, AuditListFilter{Limit: limit})
}

func (s *Service) ListAuditEvents(ctx context.Context, filter AuditListFilter) ([]AuditItem, error) {
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return nil, fmt.Errorf("from must be before to")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	whereParts := make([]string, 0, 8)
	args := make([]any, 0, 10)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if eventType := strings.TrimSpace(filter.EventType); eventType != "" {
		whereParts = append(whereParts, "ae.event_type = "+addArg(eventType))
	}
	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		whereParts = append(whereParts, "ae.actor_user_id = "+addArg(userID))
	}
	if assetID := strings.TrimSpace(filter.AssetID); assetID != "" {
		whereParts = append(whereParts, "ae.asset_id = "+addArg(assetID))
	}
	if sessionID := strings.TrimSpace(filter.SessionID); sessionID != "" {
		whereParts = append(whereParts, "ae.session_id = "+addArg(sessionID))
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		whereParts = append(whereParts, "ae.action = "+addArg(action))
	}
	if filter.From != nil {
		whereParts = append(whereParts, "ae.event_time >= "+addArg(filter.From.UTC()))
	}
	if filter.To != nil {
		whereParts = append(whereParts, "ae.event_time <= "+addArg(filter.To.UTC()))
	}

	query := `
SELECT
	ae.id,
	ae.event_time,
	ae.event_type,
	COALESCE(ae.action, ''),
	COALESCE(ae.outcome, ''),
	ae.metadata,
	ae.actor_user_id::TEXT,
	u.username,
	ae.asset_id::TEXT,
	a.name,
	a.asset_type,
	ae.session_id::TEXT,
	s.status,
	COALESCE(s.reason, ''),
	s.created_at
FROM audit_events ae
LEFT JOIN users u ON u.id = ae.actor_user_id
LEFT JOIN assets a ON a.id = ae.asset_id
LEFT JOIN sessions s ON s.id = ae.session_id`

	if len(whereParts) > 0 {
		query += "\nWHERE " + strings.Join(whereParts, "\n  AND ")
	}
	query += "\nORDER BY ae.event_time DESC, ae.id DESC\nLIMIT " + addArg(limit) + ";"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()

	items := make([]AuditItem, 0)
	for rows.Next() {
		item, scanErr := scanAuditItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate audit events: %w", rowsErr)
	}
	return items, nil
}

func (s *Service) GetAuditEventByID(ctx context.Context, eventID int64) (AuditItem, error) {
	if eventID <= 0 {
		return AuditItem{}, fmt.Errorf("event id must be > 0")
	}

	const query = `
SELECT
	ae.id,
	ae.event_time,
	ae.event_type,
	COALESCE(ae.action, ''),
	COALESCE(ae.outcome, ''),
	ae.metadata,
	ae.actor_user_id::TEXT,
	u.username,
	ae.asset_id::TEXT,
	a.name,
	a.asset_type,
	ae.session_id::TEXT,
	s.status,
	COALESCE(s.reason, ''),
	s.created_at
FROM audit_events ae
LEFT JOIN users u ON u.id = ae.actor_user_id
LEFT JOIN assets a ON a.id = ae.asset_id
LEFT JOIN sessions s ON s.id = ae.session_id
WHERE ae.id = $1
LIMIT 1;`

	rows, err := s.pool.Query(ctx, query, eventID)
	if err != nil {
		return AuditItem{}, fmt.Errorf("get audit event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return AuditItem{}, ErrAuditEventNotFound
	}
	item, scanErr := scanAuditItem(rows)
	if scanErr != nil {
		return AuditItem{}, scanErr
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return AuditItem{}, fmt.Errorf("iterate audit event: %w", rowsErr)
	}
	return item, nil
}

func scanAuditItem(rows pgx.Rows) (AuditItem, error) {
	var (
		item          AuditItem
		metadataRaw   []byte
		actorUserID   pgtype.Text
		actorUser     pgtype.Text
		assetID       pgtype.Text
		assetName     pgtype.Text
		assetType     pgtype.Text
		sessionID     pgtype.Text
		sessionStatus pgtype.Text
		sessionReason pgtype.Text
		sessionAt     pgtype.Timestamptz
	)
	if scanErr := rows.Scan(
		&item.ID,
		&item.EventTime,
		&item.EventType,
		&item.Action,
		&item.Outcome,
		&metadataRaw,
		&actorUserID,
		&actorUser,
		&assetID,
		&assetName,
		&assetType,
		&sessionID,
		&sessionStatus,
		&sessionReason,
		&sessionAt,
	); scanErr != nil {
		return AuditItem{}, fmt.Errorf("scan audit item: %w", scanErr)
	}

	item.Metadata = map[string]any{}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &item.Metadata); err != nil {
			return AuditItem{}, fmt.Errorf("decode audit metadata: %w", err)
		}
	}
	if actorUserID.Valid {
		value := actorUserID.String
		item.ActorUserID = &value
	}
	if actorUser.Valid {
		value := actorUser.String
		item.ActorUser = &value
	}
	if assetID.Valid {
		value := assetID.String
		item.AssetID = &value
	}
	if assetName.Valid {
		value := assetName.String
		item.AssetName = &value
	}
	if assetType.Valid {
		value := assetType.String
		item.AssetType = &value
	}
	if sessionID.Valid {
		value := sessionID.String
		item.SessionID = &value
	}
	if sessionID.Valid && sessionStatus.Valid && sessionAt.Valid {
		item.Session = &AuditSessionSummary{
			ID:        sessionID.String,
			Action:    actionFromReason(sessionReason.String, ""),
			Status:    sessionStatus.String,
			CreatedAt: sessionAt.Time.UTC(),
		}
	}
	return item, nil
}

func (s *Service) listSessions(ctx context.Context, filter SessionListFilter) ([]SessionSummary, error) {
	status := strings.TrimSpace(filter.Status)
	if status != "" && !isSupportedStatus(status) {
		return nil, fmt.Errorf("unsupported status: %s", status)
	}

	action := strings.TrimSpace(filter.Action)
	if action != "" && !isSupportedAction(action) {
		return nil, fmt.Errorf("unsupported action: %s", action)
	}

	assetType := strings.TrimSpace(filter.AssetType)
	if assetType != "" && !isSupportedAssetType(assetType) {
		return nil, fmt.Errorf("unsupported asset type: %s", assetType)
	}

	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return nil, fmt.Errorf("from must be before to")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	whereParts := make([]string, 0, 8)
	args := make([]any, 0, 10)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		whereParts = append(whereParts, "s.user_id = "+addArg(userID))
	}
	if assetID := strings.TrimSpace(filter.AssetID); assetID != "" {
		whereParts = append(whereParts, "s.asset_id = "+addArg(assetID))
	}
	if status != "" {
		whereParts = append(whereParts, "s.status = "+addArg(status))
	}
	if action != "" {
		whereParts = append(whereParts, "s.reason = "+addArg("action:"+action))
	}
	if assetType != "" {
		whereParts = append(whereParts, "a.asset_type = "+addArg(assetType))
	}
	if filter.From != nil {
		whereParts = append(whereParts, "s.created_at >= "+addArg(filter.From.UTC()))
	}
	if filter.To != nil {
		whereParts = append(whereParts, "s.created_at <= "+addArg(filter.To.UTC()))
	}

	query := `
SELECT
    s.id,
    u.id,
    u.username,
    a.id,
    a.name,
    a.asset_type,
    CASE
        WHEN s.reason LIKE 'action:%' THEN SUBSTRING(s.reason FROM 8)
        ELSE s.protocol
    END AS action,
    s.status,
    s.started_at,
    s.ended_at,
    s.created_at,
    CASE
        WHEN s.started_at IS NULL THEN NULL
        WHEN s.ended_at IS NOT NULL THEN GREATEST(EXTRACT(EPOCH FROM (s.ended_at - s.started_at)), 0)::BIGINT
        WHEN s.status = 'active' THEN GREATEST(EXTRACT(EPOCH FROM (NOW() - s.started_at)), 0)::BIGINT
        ELSE NULL
    END AS duration_seconds
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN assets a ON a.id = s.asset_id`

	if len(whereParts) > 0 {
		query += "\nWHERE " + strings.Join(whereParts, "\n  AND ")
	}

	query += "\nORDER BY s.created_at DESC\nLIMIT " + addArg(limit) + ";"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	items := make([]SessionSummary, 0)
	for rows.Next() {
		var (
			item     SessionSummary
			started  pgtype.Timestamptz
			ended    pgtype.Timestamptz
			duration pgtype.Int8
		)
		if scanErr := rows.Scan(
			&item.SessionID,
			&item.UserID,
			&item.Username,
			&item.AssetID,
			&item.AssetName,
			&item.AssetType,
			&item.Action,
			&item.Status,
			&started,
			&ended,
			&item.CreatedAt,
			&duration,
		); scanErr != nil {
			return nil, fmt.Errorf("scan session summary: %w", scanErr)
		}
		item.LaunchType = launchTypeForAction(item.Action)
		item.StartedAt = nullableTime(started)
		item.EndedAt = nullableTime(ended)
		item.DurationSeconds = nullableInt64(duration)
		items = append(items, item)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate sessions: %w", rowsErr)
	}

	return items, nil
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time.UTC()
	return &v
}

func nullableInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func launchTypeForAction(action string) string {
	switch action {
	case "shell":
		return "shell"
	case "dbeaver":
		return "dbeaver"
	case "sftp":
		return "sftp"
	default:
		return action
	}
}

func actionFromReason(reason, fallback string) string {
	if strings.HasPrefix(reason, "action:") {
		action := strings.TrimPrefix(reason, "action:")
		action = strings.TrimSpace(action)
		if action != "" {
			return action
		}
	}
	return strings.TrimSpace(fallback)
}

func isSupportedStatus(status string) bool {
	switch status {
	case "pending", "active", "completed", "failed", "terminated", "expired":
		return true
	default:
		return false
	}
}

func isSupportedAction(action string) bool {
	switch action {
	case "shell", "sftp", "dbeaver", "redis":
		return true
	default:
		return false
	}
}

func isSupportedAssetType(assetType string) bool {
	switch assetType {
	case assets.TypeLinuxVM, assets.TypeDatabase, assets.TypeRedis:
		return true
	default:
		return false
	}
}

func (s *Service) WriteEvent(ctx context.Context, sessionID, eventType string, actorUserID *string, payload map[string]any) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(eventType) == "" {
		return fmt.Errorf("session id and event type are required")
	}
	blob, err := json.Marshal(payloadOrEmpty(payload))
	if err != nil {
		return fmt.Errorf("marshal session event payload: %w", err)
	}

	const query = `
INSERT INTO session_events (session_id, event_type, actor_user_id, payload)
VALUES ($1, $2, $3, $4::jsonb);`
	if _, err := s.pool.Exec(ctx, query, strings.TrimSpace(sessionID), strings.TrimSpace(eventType), actorUserID, blob); err != nil {
		return fmt.Errorf("insert session event: %w", err)
	}
	return nil
}

func (s *Service) RecordDataEvent(ctx context.Context, sessionID, eventType, stream string, chunk []byte, offsetSec float64) error {
	if len(chunk) == 0 {
		return nil
	}
	offset := 0.0
	if offsetSec > 0 {
		offset = offsetSec
	}
	castType := "o"
	if strings.TrimSpace(eventType) == EventDataIn {
		castType = "i"
	}
	return s.WriteEvent(ctx, sessionID, eventType, nil, map[string]any{
		"stream":           stream,
		"encoding":         "base64",
		"size":             len(chunk),
		"data":             base64.StdEncoding.EncodeToString(chunk),
		"recording_format": "asciicast-v2-like",
		"offset_sec":       offset,
		"event":            []any{offset, castType, string(chunk)},
	})
}

func (s *Service) RecordTerminalResizeEvent(ctx context.Context, sessionID string, cols, rows int, offsetSec float64) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	if cols <= 0 || rows <= 0 {
		return nil
	}
	offset := 0.0
	if offsetSec > 0 {
		offset = offsetSec
	}
	return s.WriteEvent(ctx, sessionID, EventTerminalResize, nil, map[string]any{
		"recording_format": "asciicast-v2-like",
		"offset_sec":       offset,
		"cols":             cols,
		"rows":             rows,
		"event":            []any{offset, "r", fmt.Sprintf("%dx%d", cols, rows)},
	})
}

func (s *Service) RecordCredentialUsage(
	ctx context.Context,
	lctx LaunchContext,
	credentialType, usageStage, requestID string,
) error {
	stage := strings.TrimSpace(usageStage)
	if stage == "" {
		stage = "unknown"
	}
	ctype := strings.TrimSpace(credentialType)
	if ctype == "" {
		ctype = "unknown"
	}
	// Use protocol-aware action for credential usage
	action := CredentialUsageAction(ctype, stage, lctx.Protocol, lctx.Action)
	return s.writeAudit(ctx, lctx, "credential_usage", action, "success", map[string]any{
		"session_id":      lctx.SessionID,
		"user_id":         lctx.UserID,
		"asset_id":        lctx.AssetID,
		"credential_type": ctype,
		"usage_stage":     stage,
		"request_id":      strings.TrimSpace(requestID),
		"recorded_at":     time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Service) MarkProxyConnected(ctx context.Context, lctx LaunchContext, remoteAddr string) error {
	const query = `
UPDATE sessions
SET status = $2,
    started_at = COALESCE(started_at, NOW())
WHERE id = $1
  AND status = $3;`
	if _, err := s.pool.Exec(ctx, query, lctx.SessionID, StatusActive, StatusPending); err != nil {
		return fmt.Errorf("mark proxy connected: %w", err)
	}

	ip := normalizeIP(remoteAddr)
	payload := map[string]any{
		"remote_addr": remoteAddr,
		"remote_ip":   ip,
		"request_id":  strings.TrimSpace(lctx.RequestID),
	}
	for k, v := range auditIdentityMetadata(lctx) {
		payload[k] = v
	}
	if err := s.WriteEvent(ctx, lctx.SessionID, EventProxyConnected, &lctx.UserID, payload); err != nil {
		return err
	}
	return nil
}

func (s *Service) MarkUpstreamConnected(ctx context.Context, lctx LaunchContext) error {
	payload := map[string]any{
		"upstream_host": lctx.Host,
		"upstream_port": lctx.Port,
		"request_id":    strings.TrimSpace(lctx.RequestID),
	}
	for k, v := range auditIdentityMetadata(lctx) {
		payload[k] = v
	}
	return s.WriteEvent(ctx, lctx.SessionID, EventUpstreamConn, &lctx.UserID, payload)
}

func (s *Service) MarkShellStarted(ctx context.Context, lctx LaunchContext) error {
	const query = `
UPDATE sessions
SET status = $2,
    started_at = COALESCE(started_at, NOW())
WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, query, lctx.SessionID, StatusActive); err != nil {
		return fmt.Errorf("mark session active: %w", err)
	}
	payload := map[string]any{
		"protocol":   lctx.Protocol,
		"action":     lctx.Action,
		"request_id": strings.TrimSpace(lctx.RequestID),
	}
	for k, v := range auditIdentityMetadata(lctx) {
		payload[k] = v
	}
	if err := s.WriteEvent(ctx, lctx.SessionID, EventShellStarted, &lctx.UserID, payload); err != nil {
		return err
	}
	// Use protocol-aware action for session start
	action := ProtocolAwareAction(lctx.Action, lctx.Protocol, lctx.Action)
	return s.writeAudit(ctx, lctx, auditEventSessionOpen, action, "success", nil)
}

func (s *Service) MarkEnded(ctx context.Context, lctx LaunchContext, reason string) error {
	const query = `
UPDATE sessions
SET status = $2,
    started_at = COALESCE(started_at, NOW()),
    ended_at = NOW()
WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, query, lctx.SessionID, StatusCompleted); err != nil {
		return fmt.Errorf("mark session completed: %w", err)
	}
	payload := map[string]any{
		"reason":     strings.TrimSpace(reason),
		"request_id": strings.TrimSpace(lctx.RequestID),
	}
	for k, v := range auditIdentityMetadata(lctx) {
		payload[k] = v
	}
	if err := s.WriteEvent(ctx, lctx.SessionID, EventSessionEnded, &lctx.UserID, payload); err != nil {
		return err
	}
	return s.writeAudit(ctx, lctx, auditEventSessionDone, sessionEndAuditAction(lctx.Action), "success", map[string]any{
		"reason": strings.TrimSpace(reason),
	})
}

func (s *Service) MarkFailed(ctx context.Context, lctx LaunchContext, reason string) error {
	const query = `
UPDATE sessions
SET status = $2,
    ended_at = NOW()
WHERE id = $1;`
	if _, err := s.pool.Exec(ctx, query, lctx.SessionID, StatusFailed); err != nil {
		return fmt.Errorf("mark session failed: %w", err)
	}
	payload := map[string]any{
		"reason":     strings.TrimSpace(reason),
		"request_id": strings.TrimSpace(lctx.RequestID),
	}
	for k, v := range auditIdentityMetadata(lctx) {
		payload[k] = v
	}
	if err := s.WriteEvent(ctx, lctx.SessionID, EventSessionFailed, &lctx.UserID, payload); err != nil {
		return err
	}
	return s.writeAudit(ctx, lctx, auditEventSessionDone, sessionEndAuditAction(lctx.Action), "failed", map[string]any{
		"reason": strings.TrimSpace(reason),
	})
}

func sessionEndAuditAction(action string) string {
	trimmed := strings.TrimSpace(action)
	if trimmed == "" {
		return "session_end"
	}
	return trimmed + "_end"
}

func replayChunkFromPayload(eventID int64, eventType string, eventTime time.Time, payload map[string]any) (ReplayChunk, bool) {
	switch eventType {
	case EventDataIn, EventDataOut:
		return replayDataChunkFromPayload(eventID, eventType, eventTime, payload)
	case EventTerminalResize:
		return replayResizeChunkFromPayload(eventID, eventTime, payload)
	default:
		return ReplayChunk{}, false
	}
}

func replayDataChunkFromPayload(eventID int64, eventType string, eventTime time.Time, payload map[string]any) (ReplayChunk, bool) {
	direction := "out"
	typeName := "output"
	if eventType == EventDataIn {
		direction = "in"
		typeName = "input"
	}

	data, _ := payload["data"].(string)
	if strings.TrimSpace(data) == "" {
		return ReplayChunk{}, false
	}

	size := intFromAny(payload["size"])
	stream, _ := payload["stream"].(string)
	offset := floatFromAny(payload["offset_sec"])

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return ReplayChunk{}, false
	}
	if !utf8.Valid(decoded) {
		return ReplayChunk{}, false
	}
	text := string(decoded)
	castCode := "o"
	if direction == "in" {
		castCode = "i"
	}
	return ReplayChunk{
		EventID:   eventID,
		EventTime: eventTime,
		EventType: typeName,
		Direction: direction,
		Stream:    stream,
		Size:      size,
		Text:      text,
		Encoded:   data,
		OffsetSec: offset,
		Asciicast: []any{offset, castCode, text},
	}, true
}

func replayResizeChunkFromPayload(eventID int64, eventTime time.Time, payload map[string]any) (ReplayChunk, bool) {
	cols := intFromAny(payload["cols"])
	rows := intFromAny(payload["rows"])
	if cols <= 0 || rows <= 0 {
		return ReplayChunk{}, false
	}
	offset := floatFromAny(payload["offset_sec"])
	return ReplayChunk{
		EventID:   eventID,
		EventTime: eventTime,
		EventType: "resize",
		OffsetSec: offset,
		Cols:      cols,
		Rows:      rows,
		Asciicast: []any{offset, "r", fmt.Sprintf("%dx%d", cols, rows)},
	}, true
}

func normalizeReplayChunks(chunks []ReplayChunk) []ReplayChunk {
	if len(chunks) == 0 {
		return chunks
	}
	base := chunks[0].EventTime
	prev := 0.0
	for i := range chunks {
		offset := chunks[i].OffsetSec
		if offset <= 0 {
			offset = chunks[i].EventTime.Sub(base).Seconds()
		}
		if offset < 0 {
			offset = 0
		}
		chunks[i].OffsetSec = offset
		delay := offset - prev
		if i == 0 || delay < 0 {
			delay = 0
		}
		chunks[i].DelaySec = delay
		prev = offset

		// Normalize asciicast-like tuple to use normalized offset.
		switch chunks[i].EventType {
		case "input":
			chunks[i].Asciicast = []any{offset, "i", chunks[i].Text}
		case "output":
			chunks[i].Asciicast = []any{offset, "o", chunks[i].Text}
		case "resize":
			chunks[i].Asciicast = []any{offset, "r", fmt.Sprintf("%dx%d", chunks[i].Cols, chunks[i].Rows)}
		}
	}
	return chunks
}

func intFromAny(v any) int {
	switch typed := v.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func floatFromAny(v any) float64 {
	switch typed := v.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func (s *Service) writeAudit(
	ctx context.Context,
	lctx LaunchContext,
	eventType, action, outcome string,
	metadata map[string]any,
) error {
	const query = `
INSERT INTO audit_events (
    actor_user_id,
    asset_id,
    session_id,
    event_type,
    action,
    outcome,
    metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb);`
	meta := payloadOrEmpty(metadata)
	if _, ok := meta["request_id"]; !ok {
		meta["request_id"] = strings.TrimSpace(lctx.RequestID)
	}
	// Enrich with protocol-aware context so the UI can display
	// "Shell upstream auth" / "SFTP upstream auth" etc. instead of
	// generic "proxy_upstream_auth".
	if _, ok := meta["protocol_action"]; !ok {
		meta["protocol_action"] = UpstreamAuthAction(lctx.Protocol, lctx.Action)
	}
	if _, ok := meta["session_type"]; !ok && lctx.Protocol != "" {
		meta["session_type"] = lctx.Protocol
	}
	if _, ok := meta["launch_type"]; !ok && lctx.Action != "" {
		meta["launch_type"] = lctx.Action
	}
	for k, v := range auditIdentityMetadata(lctx) {
		if _, ok := meta[k]; !ok {
			meta[k] = v
		}
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}
	if _, err := s.pool.Exec(ctx, query, lctx.UserID, lctx.AssetID, lctx.SessionID, eventType, action, outcome, blob); err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func auditIdentityMetadata(lctx LaunchContext) map[string]any {
	meta := map[string]any{}
	if v := strings.TrimSpace(lctx.AssetName); v != "" {
		meta["target_asset_name"] = v
	}
	if v := strings.TrimSpace(lctx.Host); v != "" {
		meta["target_host"] = v
	}
	if v := strings.TrimSpace(lctx.UpstreamUsername); v != "" {
		meta["upstream_username"] = v
	}
	if v := strings.TrimSpace(lctx.Protocol); v != "" {
		meta["protocol"] = v
	}
	return meta
}

func payloadOrEmpty(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func normalizeIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		trimmed = host
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return ""
	}
	return ip.String()
}
