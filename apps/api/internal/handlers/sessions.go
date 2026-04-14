package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/districtdco/accessd/api/internal/access"
	"github.com/districtdco/accessd/api/internal/assets"
	"github.com/districtdco/accessd/api/internal/auth"
	"github.com/districtdco/accessd/api/internal/credentials"
	"github.com/districtdco/accessd/api/internal/mssqlproxy"
	"github.com/districtdco/accessd/api/internal/mysqlproxy"
	"github.com/districtdco/accessd/api/internal/pgproxy"
	"github.com/districtdco/accessd/api/internal/redisproxy"
	"github.com/districtdco/accessd/api/internal/requestctx"
	"github.com/districtdco/accessd/api/internal/sessions"
)

type SessionsHandler struct {
	assetsService      *assets.Service
	accessService      *access.Service
	credentialsService *credentials.Service
	sessionsService    *sessions.Service
	pgProxyService     *pgproxy.Service
	mysqlProxyService  *mysqlproxy.Service
	mssqlProxyService  *mssqlproxy.Service
	redisProxyService  *redisproxy.Service
}

type launchRequest struct {
	AssetID string `json:"asset_id"`
	Action  string `json:"action"`
}

type connectorTokenVerifyRequest struct {
	ConnectorToken string `json:"connector_token"`
	SessionID      string `json:"session_id,omitempty"`
}

type connectorBootstrapIssueRequest struct {
	Origin string `json:"origin,omitempty"`
}

type connectorBootstrapVerifyRequest struct {
	Token string `json:"token"`
}

type sessionEventRequest struct {
	EventType string         `json:"event_type"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type sessionsListResponse struct {
	Items []sessionSummaryResponse `json:"items"`
}

type sessionDetailResponse struct {
	SessionID       string                  `json:"session_id"`
	User            sessionSummaryUser      `json:"user"`
	Asset           sessionSummaryAsset     `json:"asset"`
	Action          string                  `json:"action"`
	LaunchType      string                  `json:"launch_type"`
	Protocol        string                  `json:"protocol"`
	Status          string                  `json:"status"`
	LaunchedVia     string                  `json:"launched_via"`
	StartedAt       string                  `json:"started_at,omitempty"`
	EndedAt         string                  `json:"ended_at,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	DurationSeconds *int64                  `json:"duration_seconds,omitempty"`
	Lifecycle       sessionLifecycleSummary `json:"lifecycle"`
}

type sessionEventsResponse struct {
	Items       []sessionEventResponse `json:"items"`
	NextAfterID int64                  `json:"next_after_id,omitempty"`
}

type sessionEventResponse struct {
	ID         int64                   `json:"id"`
	EventType  string                  `json:"event_type"`
	EventTime  string                  `json:"event_time"`
	ActorUser  *sessionEventUser       `json:"actor_user,omitempty"`
	Payload    map[string]any          `json:"payload"`
	Transcript *sessionTranscriptChunk `json:"transcript,omitempty"`
}

type sessionReplayResponse struct {
	SessionID   string               `json:"session_id"`
	Supported   bool                 `json:"supported"`
	Approximate bool                 `json:"approximate"`
	Items       []sessionReplayChunk `json:"items"`
	NextAfterID int64                `json:"next_after_id,omitempty"`
}

type sessionReplayChunk struct {
	EventID   int64   `json:"event_id"`
	EventTime string  `json:"event_time"`
	EventType string  `json:"event_type"`
	Direction string  `json:"direction,omitempty"`
	Stream    string  `json:"stream,omitempty"`
	Size      int     `json:"size,omitempty"`
	Text      string  `json:"text,omitempty"`
	OffsetSec float64 `json:"offset_sec"`
	DelaySec  float64 `json:"delay_sec"`
	Cols      int     `json:"cols,omitempty"`
	Rows      int     `json:"rows,omitempty"`
	Asciicast []any   `json:"asciicast,omitempty"`
}

type sessionEventUser struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
}

type adminSummaryResponse struct {
	WindowDays  int                   `json:"window_days"`
	GeneratedAt string                `json:"generated_at"`
	Metrics     adminSummaryMetrics   `json:"metrics"`
	ByAction    []adminActionCountRow `json:"by_action"`
}

type adminSummaryMetrics struct {
	RecentSessions  int64 `json:"recent_sessions"`
	ActiveSessions  int64 `json:"active_sessions"`
	FailedSessions  int64 `json:"failed_sessions"`
	ShellLaunches   int64 `json:"shell_launches"`
	DBeaverLaunches int64 `json:"dbeaver_launches"`
}

type adminActionCountRow struct {
	Action string `json:"action"`
	Count  int64  `json:"count"`
}

type adminAuditRecentResponse struct {
	Items []adminAuditItemResponse `json:"items"`
}

type adminAuditEventsResponse struct {
	Items []adminAuditItemResponse `json:"items"`
}

type adminAuditEventDetailResponse struct {
	Item adminAuditItemResponse `json:"item"`
}

type adminAuditItemResponse struct {
	ID        int64              `json:"id"`
	EventTime string             `json:"event_time"`
	EventType string             `json:"event_type"`
	Action    string             `json:"action,omitempty"`
	Outcome   string             `json:"outcome,omitempty"`
	ActorUser *sessionEventUser  `json:"actor_user,omitempty"`
	Asset     *adminAuditAsset   `json:"asset,omitempty"`
	Session   *adminAuditSession `json:"session,omitempty"`
	SessionID string             `json:"session_id,omitempty"`
	Metadata  map[string]any     `json:"metadata"`
}

type adminAuditAsset struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	AssetType string `json:"asset_type,omitempty"`
}

type adminAuditSession struct {
	ID        string `json:"id"`
	Action    string `json:"action,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type sessionSummaryResponse struct {
	SessionID       string                  `json:"session_id"`
	User            sessionSummaryUser      `json:"user"`
	Asset           sessionSummaryAsset     `json:"asset"`
	Action          string                  `json:"action"`
	LaunchType      string                  `json:"launch_type"`
	Status          string                  `json:"status"`
	StartedAt       string                  `json:"started_at,omitempty"`
	EndedAt         string                  `json:"ended_at,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	DurationSeconds *int64                  `json:"duration_seconds,omitempty"`
	Lifecycle       sessionLifecycleSummary `json:"lifecycle"`
}

type sessionSummaryUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type sessionSummaryAsset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AssetType string `json:"asset_type"`
}

type sessionLifecycleSummary struct {
	Started            bool   `json:"started"`
	Ended              bool   `json:"ended"`
	Failed             bool   `json:"failed"`
	ShellStarted       bool   `json:"shell_started"`
	ConnectorRequested bool   `json:"connector_requested"`
	ConnectorSucceeded bool   `json:"connector_succeeded"`
	ConnectorFailed    bool   `json:"connector_failed"`
	EventCount         int64  `json:"event_count"`
	FirstEventAt       string `json:"first_event_at,omitempty"`
	LastEventAt        string `json:"last_event_at,omitempty"`
}

type sessionTranscriptChunk struct {
	Direction string `json:"direction"`
	Stream    string `json:"stream,omitempty"`
	Size      int    `json:"size,omitempty"`
	Text      string `json:"text,omitempty"`
}

type launchResponse struct {
	SessionID      string             `json:"session_id"`
	LaunchType     string             `json:"launch_type"`
	AssetName      string             `json:"asset_name,omitempty"`
	AssetHost      string             `json:"asset_host,omitempty"`
	ConnectorToken string             `json:"connector_token,omitempty"`
	Launch         launchPayloadUnion `json:"launch"`
}

type launchPayloadUnion struct {
	ProxyHost        string `json:"proxy_host,omitempty"`
	ProxyPort        int    `json:"proxy_port,omitempty"`
	Username         string `json:"username,omitempty"`
	ProxyUsername    string `json:"proxy_username,omitempty"`
	UpstreamUsername string `json:"upstream_username,omitempty"`
	TargetAssetName  string `json:"target_asset_name,omitempty"`
	TargetHost       string `json:"target_host,omitempty"`
	Token            string `json:"token,omitempty"`
	Path             string `json:"path,omitempty"`

	Engine   string `json:"engine,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Password string `json:"password,omitempty"`
	SSLMode  string `json:"ssl_mode,omitempty"`

	RedisHost                  string `json:"redis_host,omitempty"`
	RedisPort                  int    `json:"redis_port,omitempty"`
	RedisUsername              string `json:"redis_username,omitempty"`
	RedisPassword              string `json:"redis_password,omitempty"`
	RedisDatabase              int    `json:"redis_database,omitempty"`
	RedisTLS                   bool   `json:"redis_tls,omitempty"`
	RedisInsecureSkipVerifyTLS bool   `json:"redis_insecure_skip_verify_tls,omitempty"`

	ExpiresAt string `json:"expires_at"`
}

type dbAssetMetadata struct {
	Engine   string `json:"engine"`
	Database string `json:"database"`
	SSLMode  string `json:"ssl_mode"`
}

type redisAssetMetadata struct {
	Database              int  `json:"database"`
	TLS                   bool `json:"tls"`
	InsecureSkipVerifyTLS bool `json:"insecure_skip_verify_tls"`
}

type sftpAssetMetadata struct {
	Path string `json:"path"`
}

func NewSessionsHandler(
	assetsService *assets.Service,
	accessService *access.Service,
	credentialsService *credentials.Service,
	sessionsService *sessions.Service,
	pgProxyService *pgproxy.Service,
	mysqlProxyService *mysqlproxy.Service,
	mssqlProxyService *mssqlproxy.Service,
	redisProxyService *redisproxy.Service,
) *SessionsHandler {
	return &SessionsHandler{
		assetsService:      assetsService,
		accessService:      accessService,
		credentialsService: credentialsService,
		sessionsService:    sessionsService,
		pgProxyService:     pgProxyService,
		mysqlProxyService:  mysqlProxyService,
		mssqlProxyService:  mssqlProxyService,
		redisProxyService:  redisProxyService,
	}
}

func (h *SessionsHandler) Launch(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if isReadOnlyAuditor(currentUser) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var req launchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	req.AssetID = strings.TrimSpace(req.AssetID)
	req.Action = strings.TrimSpace(req.Action)
	if req.AssetID == "" || req.Action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset_id and action are required"})
		return
	}

	asset, err := h.assetsService.GetByID(r.Context(), req.AssetID)
	if err != nil {
		if err == assets.ErrAssetNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve asset"})
		return
	}

	protocol, err := protocolForLaunch(asset.Type, req.Action)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	allowed, err := h.accessService.CanUserPerform(r.Context(), currentUser.ID, req.AssetID, req.Action)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check access"})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	launch, err := h.sessionsService.CreateLaunch(r.Context(), sessions.CreateLaunchInput{
		UserID:    currentUser.ID,
		AssetID:   req.AssetID,
		Action:    req.Action,
		Protocol:  protocol,
		RequestID: requestctx.FromContext(r.Context()),
		ClientIP:  r.RemoteAddr,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create launch session"})
		return
	}

	if asset.Type == assets.TypeDatabase && req.Action == access.ActionDBeaver {
		cred, err := h.credentialsService.ResolveForAsset(r.Context(), asset.ID, credentials.TypeDBPassword)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve database credential"})
			return
		}
		meta, err := parseDBMetadata(asset.MetadataJSON)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "database asset metadata is invalid"})
			return
		}
		lctx := sessions.LaunchContext{
			SessionID: launch.SessionID,
			UserID:    currentUser.ID,
			AssetID:   asset.ID,
			Action:    req.Action,
			Protocol:  sessions.ProtocolDB,
			AssetType: assets.TypeDatabase,
			Host:      asset.Host,
			Port:      asset.Port,
		}
		if auditErr := h.sessionsService.RecordCredentialUsage(r.Context(), lctx, credentials.TypeDBPassword, "launch_prepare", requestctx.FromContext(r.Context())); auditErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record credential usage audit"})
			return
		}

		var proxyHost string
		var proxyPort int
		clientUsername := "accessd"
		clientPassword := dbeaverClientPassword(meta.Engine, launch.ConnectorToken, launch.SessionID)

		switch meta.Engine {
		case "mysql", "mariadb":
			if h.mysqlProxyService == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mysql proxy is not configured"})
				return
			}
			proxyHost, proxyPort, err = h.mysqlProxyService.RegisterSession(mysqlproxy.SessionRegistration{
				SessionID:    launch.SessionID,
				UserID:       currentUser.ID,
				AssetID:      asset.ID,
				AssetName:    strings.TrimSpace(asset.Name),
				Engine:       meta.Engine,
				Database:     meta.Database,
				SSLMode:      meta.SSLMode,
				UpstreamHost: asset.Host,
				UpstreamPort: asset.Port,
				Username:     strings.TrimSpace(cred.Username),
				RequestID:    requestctx.FromContext(r.Context()),
			})
		case "mssql", "sqlserver", "sql_server":
			if h.mssqlProxyService == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mssql proxy is not configured"})
				return
			}
			proxyHost, proxyPort, err = h.mssqlProxyService.RegisterSession(mssqlproxy.SessionRegistration{
				SessionID:    launch.SessionID,
				UserID:       currentUser.ID,
				AssetID:      asset.ID,
				AssetName:    strings.TrimSpace(asset.Name),
				Engine:       meta.Engine,
				Database:     meta.Database,
				SSLMode:      meta.SSLMode,
				UpstreamHost: asset.Host,
				UpstreamPort: asset.Port,
				Username:     strings.TrimSpace(cred.Username),
				RequestID:    requestctx.FromContext(r.Context()),
			})
		case "mongo", "mongodb":
			// Secure mode: never hand upstream Mongo credentials to the client.
			// A managed Mongo proxy path is required before Mongo launches are enabled.
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "mongo launch requires managed proxy support and is not available yet"})
			return
		default:
			if h.pgProxyService == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database proxy is not configured"})
				return
			}
			proxyHost, proxyPort, err = h.pgProxyService.RegisterSession(pgproxy.SessionRegistration{
				SessionID:    launch.SessionID,
				UserID:       currentUser.ID,
				AssetID:      asset.ID,
				AssetName:    strings.TrimSpace(asset.Name),
				Engine:       meta.Engine,
				Database:     meta.Database,
				SSLMode:      meta.SSLMode,
				UpstreamHost: asset.Host,
				UpstreamPort: asset.Port,
				Username:     strings.TrimSpace(cred.Username),
				RequestID:    requestctx.FromContext(r.Context()),
			})
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to allocate database proxy endpoint"})
			return
		}
		launch = h.sessionsService.AttachDBeaverPayload(launch, sessions.DBeaverLaunchPayload{
			Engine:           meta.Engine,
			Host:             proxyHost,
			Port:             proxyPort,
			Database:         meta.Database,
			Username:         clientUsername,
			UpstreamUsername: strings.TrimSpace(cred.Username),
			TargetAssetName:  strings.TrimSpace(asset.Name),
			TargetHost:       strings.TrimSpace(asset.Host),
			Password:         clientPassword,
			SSLMode:          meta.SSLMode,
		})
	}
	if asset.Type == assets.TypeLinuxVM && (req.Action == access.ActionShell || req.Action == access.ActionSFTP) {
		cred, err := h.credentialsService.ResolveForAsset(r.Context(), asset.ID, credentials.TypePassword)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve linux credential"})
			return
		}
		upstreamUsername := strings.TrimSpace(cred.Username)
		if req.Action == access.ActionShell {
			if launch.Shell == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to allocate shell proxy endpoint"})
				return
			}
			launch = h.sessionsService.AttachShellPayload(launch, sessions.ShellLaunchPayload{
				ProxyHost:        launch.Shell.ProxyHost,
				ProxyPort:        launch.Shell.ProxyPort,
				ProxyUsername:    launch.Shell.ProxyUsername,
				UpstreamUsername: upstreamUsername,
				TargetAssetName:  strings.TrimSpace(asset.Name),
				TargetHost:       strings.TrimSpace(asset.Host),
				Token:            launch.Shell.Token,
			})
		}
		if req.Action == access.ActionSFTP {
			if launch.SFTP == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to allocate sftp proxy endpoint"})
				return
			}
			launch = h.sessionsService.AttachSFTPPayload(launch, sessions.SFTPLaunchPayload{
				Host:             launch.SFTP.Host,
				Port:             launch.SFTP.Port,
				ProxyUsername:    launch.SFTP.ProxyUsername,
				UpstreamUsername: upstreamUsername,
				TargetAssetName:  strings.TrimSpace(asset.Name),
				TargetHost:       strings.TrimSpace(asset.Host),
				Password:         launch.SFTP.Password,
				Path:             launch.SFTP.Path,
			})
		}
	}
	if asset.Type == assets.TypeLinuxVM && req.Action == access.ActionSFTP {
		meta, err := parseSFTPMetadata(asset.MetadataJSON)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sftp asset metadata is invalid"})
			return
		}
		if launch.SFTP == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to allocate sftp proxy endpoint"})
			return
		}
		launch = h.sessionsService.AttachSFTPPayload(launch, sessions.SFTPLaunchPayload{
			Host:             launch.SFTP.Host,
			Port:             launch.SFTP.Port,
			ProxyUsername:    launch.SFTP.ProxyUsername,
			UpstreamUsername: launch.SFTP.UpstreamUsername,
			TargetAssetName:  launch.SFTP.TargetAssetName,
			TargetHost:       launch.SFTP.TargetHost,
			Password:         launch.SFTP.Password,
			Path:             meta.Path,
		})
	}
	if asset.Type == assets.TypeRedis && req.Action == access.ActionRedis {
		cred, err := h.credentialsService.ResolveForAsset(r.Context(), asset.ID, credentials.TypePassword)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve redis credential"})
			return
		}
		if h.redisProxyService == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "redis proxy is not configured"})
			return
		}
		if launch.Redis == nil || strings.TrimSpace(launch.Redis.Password) == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "redis launch token was not issued"})
			return
		}
		meta, err := parseRedisMetadata(asset.MetadataJSON)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "redis asset metadata is invalid"})
			return
		}
		lctx := sessions.LaunchContext{
			SessionID: launch.SessionID,
			UserID:    currentUser.ID,
			AssetID:   asset.ID,
			Action:    req.Action,
			Protocol:  sessions.ProtocolRedis,
			AssetType: assets.TypeRedis,
			Host:      asset.Host,
			Port:      asset.Port,
		}
		if auditErr := h.sessionsService.RecordCredentialUsage(r.Context(), lctx, credentials.TypePassword, "launch_prepare", requestctx.FromContext(r.Context())); auditErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record credential usage audit"})
			return
		}
		proxyHost, proxyPort, err := h.redisProxyService.RegisterSession(redisproxy.SessionRegistration{
			SessionID:             launch.SessionID,
			UserID:                currentUser.ID,
			AssetID:               asset.ID,
			AssetName:             strings.TrimSpace(asset.Name),
			UpstreamHost:          asset.Host,
			UpstreamPort:          asset.Port,
			UseTLS:                meta.TLS,
			InsecureSkipVerifyTLS: meta.InsecureSkipVerifyTLS,
			Database:              meta.Database,
			ClientAuthToken:       launch.Redis.Password,
			RequestID:             requestctx.FromContext(r.Context()),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to allocate redis proxy endpoint"})
			return
		}
		launch = h.sessionsService.AttachRedisPayload(launch, sessions.RedisLaunchPayload{
			Host:                  proxyHost,
			Port:                  proxyPort,
			Username:              strings.TrimSpace(cred.Username),
			Password:              launch.Redis.Password,
			Database:              meta.Database,
			UseTLS:                false,
			InsecureSkipVerifyTLS: false,
		})
	}

	resp, err := buildLaunchResponse(launch)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build launch response"})
		return
	}
	if h.sessionsService.ConnectorTokenEnabled() && strings.TrimSpace(resp.ConnectorToken) == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to issue connector token for launch"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func dbeaverClientPassword(engine, connectorToken, sessionID string) string {
	fallback := strings.TrimSpace(sessionID)
	if fallback == "" {
		fallback = "accessd"
	}

	token := strings.TrimSpace(connectorToken)
	if token == "" {
		return fallback
	}
	// Keep client-leg credentials safe across strict JDBC/ODBC clients.
	// Some drivers (for example MSSQL) enforce password length limits around 128 chars.
	if len(token) > 128 {
		return fallback
	}
	return token
}

func (h *SessionsHandler) RecordEvent(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if isReadOnlyAuditor(currentUser) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	var req sessionEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	req.EventType = strings.TrimSpace(req.EventType)
	if req.EventType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event_type is required"})
		return
	}

	if err := h.sessionsService.RecordConnectorLaunchEvent(r.Context(), sessionID, currentUser.ID, req.EventType, req.Metadata); err != nil {
		if err == sessions.ErrUnauthorizedLaunch {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
			return
		}
		if strings.HasPrefix(err.Error(), "unsupported connector event type") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		slog.Error("failed to record connector launch event",
			"session_id", sessionID,
			"user_id", currentUser.ID,
			"event_type", req.EventType,
			"request_id", requestctx.FromContext(r.Context()),
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record session event"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "recorded", "session_id": sessionID})
}

// VerifyConnectorToken validates a connector token for operator-side connector online verification.
func (h *SessionsHandler) VerifyConnectorToken(w http.ResponseWriter, r *http.Request) {
	var req connectorTokenVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	token := strings.TrimSpace(req.ConnectorToken)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connector_token is required"})
		return
	}

	claims, err := h.sessionsService.VerifyConnectorToken(token)
	if err != nil {
		if errors.Is(err, sessions.ErrLaunchExpired) {
			writeJSON(w, http.StatusForbidden, map[string]any{"valid": false, "error": "connector token expired"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"valid": false, "error": "connector token invalid"})
		return
	}
	if sid := strings.TrimSpace(req.SessionID); sid != "" && sid != claims.SessionID {
		writeJSON(w, http.StatusForbidden, map[string]any{"valid": false, "error": "connector token session mismatch"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid": true,
		"claims": map[string]any{
			"sid": claims.SessionID,
			"uid": claims.UserID,
			"aid": claims.AssetID,
			"act": claims.Action,
			"exp": claims.ExpiresAt,
			"v":   claims.Version,
		},
	})
}

func (h *SessionsHandler) IssueConnectorBootstrapToken(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req connectorBootstrapIssueRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	origin := strings.TrimSpace(req.Origin)
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Origin"))
	}
	if origin == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "origin is required"})
		return
	}

	token, claims, err := h.sessionsService.IssueConnectorBootstrapToken(currentUser.ID, origin, 2*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"claims": map[string]any{
			"origin":             claims.Origin,
			"backend_verify_url": claims.BackendVerify,
			"exp":                claims.ExpiresAt,
			"v":                  claims.Version,
		},
	})
}

func (h *SessionsHandler) VerifyConnectorBootstrapToken(w http.ResponseWriter, r *http.Request) {
	var req connectorBootstrapVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}

	claims, err := h.sessionsService.VerifyConnectorBootstrapToken(token)
	if err != nil {
		if errors.Is(err, sessions.ErrLaunchExpired) {
			writeJSON(w, http.StatusForbidden, map[string]any{"valid": false, "error": "bootstrap token expired"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"valid": false, "error": "bootstrap token invalid"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid": true,
		"claims": map[string]any{
			"origin":             claims.Origin,
			"backend_verify_url": claims.BackendVerify,
			"exp":                claims.ExpiresAt,
			"v":                  claims.Version,
		},
	})
}

func (h *SessionsHandler) MySessions(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	filter, err := sessionFilterFromQuery(r, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	items, err := h.sessionsService.ListForUser(r.Context(), currentUser.ID, filter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, sessionsListResponse{Items: mapSessionSummaries(items)})
}

func (h *SessionsHandler) AdminSessions(w http.ResponseWriter, r *http.Request) {
	filter, err := sessionFilterFromQuery(r, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	items, err := h.sessionsService.ListForAdmin(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, sessionsListResponse{Items: mapSessionSummaries(items)})
}

func (h *SessionsHandler) AdminSummary(w http.ResponseWriter, r *http.Request) {
	windowDays := 7
	windowRaw := strings.TrimSpace(r.URL.Query().Get("window_days"))
	if windowRaw != "" {
		parsedWindow, parseErr := strconv.Atoi(windowRaw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "window_days must be an integer"})
			return
		}
		windowDays = parsedWindow
	}

	summary, err := h.sessionsService.GetAdminSummary(r.Context(), windowDays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build summary"})
		return
	}

	rows := make([]adminActionCountRow, 0, len(summary.ByAction))
	for _, item := range summary.ByAction {
		rows = append(rows, adminActionCountRow{
			Action: item.Action,
			Count:  item.Count,
		})
	}

	resp := adminSummaryResponse{
		WindowDays:  summary.WindowDays,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metrics: adminSummaryMetrics{
			RecentSessions:  summary.RecentSessions,
			ActiveSessions:  summary.ActiveSessions,
			FailedSessions:  summary.FailedSessions,
			ShellLaunches:   summary.ShellLaunches,
			DBeaverLaunches: summary.DBeaverLaunches,
		},
		ByAction: rows,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *SessionsHandler) AdminActiveSessions(w http.ResponseWriter, r *http.Request) {
	limit := 100
	limitRaw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limitRaw != "" {
		parsedLimit, parseErr := strconv.Atoi(limitRaw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
		limit = parsedLimit
	}

	items, err := h.sessionsService.ListActiveForAdmin(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, sessionsListResponse{Items: mapSessionSummaries(items)})
}

func (h *SessionsHandler) AdminRecentAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	limitRaw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limitRaw != "" {
		parsedLimit, parseErr := strconv.Atoi(limitRaw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
		limit = parsedLimit
	}

	items, err := h.sessionsService.ListRecentAudit(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list recent audit"})
		return
	}

	resp := adminAuditRecentResponse{Items: make([]adminAuditItemResponse, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, mapAdminAuditItem(item))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *SessionsHandler) AdminAuditEvents(w http.ResponseWriter, r *http.Request) {
	filter, err := auditFilterFromQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	items, err := h.sessionsService.ListAuditEvents(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := adminAuditEventsResponse{Items: make([]adminAuditItemResponse, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, mapAdminAuditItem(item))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *SessionsHandler) AdminAuditEventDetail(w http.ResponseWriter, r *http.Request) {
	eventRaw := strings.TrimSpace(r.PathValue("eventID"))
	if eventRaw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event id is required"})
		return
	}
	eventID, err := strconv.ParseInt(eventRaw, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event id must be an integer"})
		return
	}

	item, err := h.sessionsService.GetAuditEventByID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, sessions.ErrAuditEventNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "audit event not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, adminAuditEventDetailResponse{Item: mapAdminAuditItem(item)})
}

func (h *SessionsHandler) Detail(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	item, err := h.sessionsService.GetDetail(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !canViewSession(currentUser, item.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	lifecycle, err := h.sessionsService.GetLifecycleSummary(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build lifecycle summary"})
		return
	}

	writeJSON(w, http.StatusOK, mapSessionDetail(item, lifecycle))
}

func (h *SessionsHandler) Events(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	item, err := h.sessionsService.GetDetail(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !canViewSession(currentUser, item.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	limit := 300
	limitRaw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limitRaw != "" {
		parsedLimit, parseErr := strconv.Atoi(limitRaw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
		limit = parsedLimit
	}

	var afterID int64
	afterRaw := strings.TrimSpace(r.URL.Query().Get("after_id"))
	if afterRaw != "" {
		parsedAfter, parseErr := strconv.ParseInt(afterRaw, 10, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after_id must be an integer"})
			return
		}
		afterID = parsedAfter
	}

	events, err := h.sessionsService.ListEvents(r.Context(), sessionID, afterID, limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := sessionEventsResponse{
		Items: mapSessionEvents(item.Action, events),
	}
	if len(events) > 0 {
		resp.NextAfterID = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *SessionsHandler) Replay(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	item, err := h.sessionsService.GetDetail(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !canViewSession(currentUser, item.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	limit := 300
	limitRaw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limitRaw != "" {
		parsedLimit, parseErr := strconv.Atoi(limitRaw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
		limit = parsedLimit
	}

	var afterID int64
	afterRaw := strings.TrimSpace(r.URL.Query().Get("after_id"))
	if afterRaw != "" {
		parsedAfter, parseErr := strconv.ParseInt(afterRaw, 10, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after_id must be an integer"})
			return
		}
		afterID = parsedAfter
	}

	resp := sessionReplayResponse{
		SessionID:   sessionID,
		Supported:   item.Action == "shell",
		Approximate: item.Action == "shell",
		Items:       []sessionReplayChunk{},
	}
	if item.Action != "shell" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	chunks, err := h.sessionsService.ListReplayChunks(r.Context(), sessionID, afterID, limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp.Items = mapReplayChunks(chunks)
	if len(chunks) > 0 {
		resp.NextAfterID = chunks[len(chunks)-1].EventID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *SessionsHandler) ExportSessionSummary(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	item, err := h.sessionsService.GetDetail(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !canViewSession(currentUser, item.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	lifecycle, err := h.sessionsService.GetLifecycleSummary(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build lifecycle summary"})
		return
	}
	eventTypeCounts := map[string]int64{}
	var afterID int64
	for {
		events, listErr := h.sessionsService.ListEvents(r.Context(), sessionID, afterID, 1000)
		if listErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build event timeline"})
			return
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			eventTypeCounts[event.EventType]++
		}
		afterID = events[len(events)-1].ID
	}
	payload := map[string]any{
		"session":           mapSessionDetail(item, lifecycle),
		"event_type_counts": eventTypeCounts,
		"event_count":       lifecycle.EventCount,
	}

	filename := fmt.Sprintf("session-%s-summary.json", sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	writeJSON(w, http.StatusOK, payload)
}

func (h *SessionsHandler) ExportSessionTranscript(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	item, err := h.sessionsService.GetDetail(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !canViewSession(currentUser, item.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if item.Action != "shell" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "transcript export is supported for shell sessions only"})
		return
	}

	events := make([]sessions.SessionEvent, 0)
	var afterID int64
	for {
		page, pageErr := h.sessionsService.ListEvents(r.Context(), sessionID, afterID, 1000)
		if pageErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build transcript export"})
			return
		}
		if len(page) == 0 {
			break
		}
		events = append(events, page...)
		afterID = page[len(page)-1].ID
	}
	lines := normalizeTranscriptRows(events)

	var b strings.Builder
	b.WriteString("# AccessD session transcript (first-pass)\n")
	b.WriteString(fmt.Sprintf("# session_id: %s\n", item.SessionID))
	b.WriteString(fmt.Sprintf("# user: %s\n", item.Username))
	b.WriteString(fmt.Sprintf("# asset: %s\n", item.AssetName))
	b.WriteString(fmt.Sprintf("# generated_at: %s\n\n", time.Now().UTC().Format(time.RFC3339Nano)))

	for _, row := range lines {
		prefix := "OUT"
		if row.Direction == "in" {
			prefix = "IN "
		}
		b.WriteString(fmt.Sprintf("[%s] %s ", row.EventTime.UTC().Format(time.RFC3339Nano), prefix))
		b.WriteString(row.Text)
		if !strings.HasSuffix(row.Text, "\n") {
			b.WriteString("\n")
		}
	}
	if len(lines) == 0 {
		b.WriteString("(no transcript chunks captured)\n")
	}

	filename := fmt.Sprintf("session-%s-transcript.txt", sessionID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

type normalizedTranscriptRow struct {
	EventID   int64
	EventTime time.Time
	Direction string
	Text      string
}

func (h *SessionsHandler) AdminExportSessionsCSV(w http.ResponseWriter, r *http.Request) {
	filter, err := sessionFilterFromQuery(r, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	items, err := h.sessionsService.ListForAdmin(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{
		"session_id",
		"user_id",
		"username",
		"asset_id",
		"asset_name",
		"asset_type",
		"action",
		"launch_type",
		"status",
		"created_at",
		"started_at",
		"ended_at",
		"duration_seconds",
	})
	for _, item := range items {
		record := []string{
			item.SessionID,
			item.UserID,
			item.Username,
			item.AssetID,
			item.AssetName,
			item.AssetType,
			item.Action,
			item.LaunchType,
			item.Status,
			item.CreatedAt.UTC().Format(time.RFC3339Nano),
			formatNullableTime(item.StartedAt),
			formatNullableTime(item.EndedAt),
			formatNullableInt64(item.DurationSeconds),
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	if writerErr := writer.Error(); writerErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build csv export"})
		return
	}

	filename := fmt.Sprintf("admin-sessions-%s.csv", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func protocolForLaunch(assetType, action string) (string, error) {
	switch {
	case assetType == assets.TypeLinuxVM && action == access.ActionShell:
		return sessions.ProtocolSSH, nil
	case assetType == assets.TypeLinuxVM && action == access.ActionSFTP:
		return sessions.ProtocolSFTP, nil
	case assetType == assets.TypeDatabase && action == access.ActionDBeaver:
		return sessions.ProtocolDB, nil
	case assetType == assets.TypeRedis && action == access.ActionRedis:
		return sessions.ProtocolRedis, nil
	default:
		return "", errUnsupportedLaunch(assetType, action)
	}
}

func parseDBMetadata(raw json.RawMessage) (dbAssetMetadata, error) {
	meta := dbAssetMetadata{}
	if len(raw) == 0 {
		return meta, nil
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return dbAssetMetadata{}, err
	}
	meta.Engine = strings.TrimSpace(meta.Engine)
	meta.Database = strings.TrimSpace(meta.Database)
	meta.SSLMode = strings.TrimSpace(meta.SSLMode)
	if meta.Engine == "" {
		meta.Engine = "postgres"
	}
	switch strings.ToLower(meta.Engine) {
	case "postgres":
		meta.Engine = "postgres"
	case "postgresql":
		meta.Engine = "postgres"
	case "mysql":
		meta.Engine = "mysql"
	case "mariadb":
		meta.Engine = "mariadb"
	case "mssql":
		meta.Engine = "mssql"
	case "sqlserver", "sql_server":
		meta.Engine = "mssql"
	case "mongo":
		meta.Engine = "mongo"
	case "mongodb":
		meta.Engine = "mongo"
	default:
		meta.Engine = strings.ToLower(meta.Engine)
	}
	if meta.Engine == "mssql" {
		meta.SSLMode = normalizeMSSQLSSLMode(meta.SSLMode)
	}
	return meta, nil
}

func normalizeMSSQLSSLMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "":
		return "disable"
	case "require", "required", "true", "encrypt", "verify-ca", "verify-full", "verify_ca", "verify_identity":
		// MSSQL proxy currently does not support required TLS tunneling to upstream.
		// Coerce to disable to keep launch UX working until MSSQL TLS support is added.
		return "disable"
	default:
		return mode
	}
}

func parseRedisMetadata(raw json.RawMessage) (redisAssetMetadata, error) {
	meta := redisAssetMetadata{}
	if len(raw) == 0 {
		return meta, nil
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return redisAssetMetadata{}, err
	}
	if meta.Database < 0 {
		meta.Database = 0
	}
	return meta, nil
}

func parseSFTPMetadata(raw json.RawMessage) (sftpAssetMetadata, error) {
	meta := sftpAssetMetadata{}
	if len(raw) == 0 {
		return meta, nil
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return sftpAssetMetadata{}, err
	}
	meta.Path = strings.TrimSpace(meta.Path)
	return meta, nil
}

func buildLaunchResponse(result sessions.LaunchResult) (launchResponse, error) {
	resp := launchResponse{
		SessionID:      result.SessionID,
		LaunchType:     result.LaunchType,
		ConnectorToken: result.ConnectorToken,
	}

	switch result.LaunchType {
	case "shell":
		if result.Shell == nil {
			return launchResponse{}, errMissingLaunchPayload("shell")
		}
		resp.Launch = launchPayloadUnion{
			ProxyHost:        result.Shell.ProxyHost,
			ProxyPort:        result.Shell.ProxyPort,
			Username:         result.Shell.UpstreamUsername,
			ProxyUsername:    result.Shell.ProxyUsername,
			UpstreamUsername: result.Shell.UpstreamUsername,
			TargetAssetName:  result.Shell.TargetAssetName,
			TargetHost:       result.Shell.TargetHost,
			Token:            result.Shell.Token,
			ExpiresAt:        result.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		resp.AssetName = result.Shell.TargetAssetName
		resp.AssetHost = result.Shell.TargetHost
	case "dbeaver":
		if result.DBeaver == nil {
			return launchResponse{}, errMissingLaunchPayload("dbeaver")
		}
		resp.Launch = launchPayloadUnion{
			Engine:           result.DBeaver.Engine,
			Host:             result.DBeaver.Host,
			Port:             result.DBeaver.Port,
			Database:         result.DBeaver.Database,
			Username:         result.DBeaver.Username,
			UpstreamUsername: result.DBeaver.UpstreamUsername,
			TargetAssetName:  result.DBeaver.TargetAssetName,
			TargetHost:       result.DBeaver.TargetHost,
			Password:         result.DBeaver.Password,
			SSLMode:          result.DBeaver.SSLMode,
			ExpiresAt:        result.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		resp.AssetName = result.DBeaver.TargetAssetName
		resp.AssetHost = result.DBeaver.TargetHost
	case "sftp":
		if result.SFTP == nil {
			return launchResponse{}, errMissingLaunchPayload("sftp")
		}
		resp.Launch = launchPayloadUnion{
			Host:             result.SFTP.Host,
			Port:             result.SFTP.Port,
			Username:         result.SFTP.UpstreamUsername,
			ProxyUsername:    result.SFTP.ProxyUsername,
			UpstreamUsername: result.SFTP.UpstreamUsername,
			TargetAssetName:  result.SFTP.TargetAssetName,
			TargetHost:       result.SFTP.TargetHost,
			Password:         result.SFTP.Password,
			Path:             result.SFTP.Path,
			ExpiresAt:        result.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		resp.AssetName = result.SFTP.TargetAssetName
		resp.AssetHost = result.SFTP.TargetHost
	case "redis":
		if result.Redis == nil {
			return launchResponse{}, errMissingLaunchPayload("redis")
		}
		resp.Launch = launchPayloadUnion{
			RedisHost:                  result.Redis.Host,
			RedisPort:                  result.Redis.Port,
			RedisUsername:              result.Redis.Username,
			RedisPassword:              result.Redis.Password,
			RedisDatabase:              result.Redis.Database,
			RedisTLS:                   result.Redis.UseTLS,
			RedisInsecureSkipVerifyTLS: result.Redis.InsecureSkipVerifyTLS,
			ExpiresAt:                  result.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
	default:
		return launchResponse{}, errMissingLaunchPayload(result.LaunchType)
	}

	return resp, nil
}

func errUnsupportedLaunch(assetType, action string) error {
	return &launchValidationError{message: "unsupported launch combination for asset_type=" + assetType + " action=" + action}
}

func errMissingLaunchPayload(launchType string) error {
	return &launchValidationError{message: "missing launch payload for launch_type=" + launchType}
}

type launchValidationError struct {
	message string
}

func (e *launchValidationError) Error() string {
	return e.message
}

func sessionFilterFromQuery(r *http.Request, allowAdminFields bool) (sessions.SessionListFilter, error) {
	query := r.URL.Query()
	filter := sessions.SessionListFilter{
		Status:    strings.TrimSpace(query.Get("status")),
		Action:    strings.TrimSpace(query.Get("action")),
		AssetType: strings.TrimSpace(query.Get("asset_type")),
	}

	fromRaw := strings.TrimSpace(query.Get("from"))
	if fromRaw != "" {
		from, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return sessions.SessionListFilter{}, errInvalidQuery("from must be RFC3339")
		}
		filter.From = &from
	}

	toRaw := strings.TrimSpace(query.Get("to"))
	if toRaw != "" {
		to, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return sessions.SessionListFilter{}, errInvalidQuery("to must be RFC3339")
		}
		filter.To = &to
	}

	limitRaw := strings.TrimSpace(query.Get("limit"))
	if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			return sessions.SessionListFilter{}, errInvalidQuery("limit must be an integer")
		}
		filter.Limit = limit
	}

	if allowAdminFields {
		filter.UserID = strings.TrimSpace(query.Get("user_id"))
		filter.AssetID = strings.TrimSpace(query.Get("asset_id"))
	}

	return filter, nil
}

func auditFilterFromQuery(r *http.Request) (sessions.AuditListFilter, error) {
	query := r.URL.Query()
	filter := sessions.AuditListFilter{
		EventType: strings.TrimSpace(query.Get("event_type")),
		UserID:    strings.TrimSpace(query.Get("user_id")),
		AssetID:   strings.TrimSpace(query.Get("asset_id")),
		SessionID: strings.TrimSpace(query.Get("session_id")),
		Action:    strings.TrimSpace(query.Get("action")),
	}

	fromRaw := strings.TrimSpace(query.Get("from"))
	if fromRaw != "" {
		from, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return sessions.AuditListFilter{}, errInvalidQuery("from must be RFC3339")
		}
		filter.From = &from
	}
	toRaw := strings.TrimSpace(query.Get("to"))
	if toRaw != "" {
		to, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return sessions.AuditListFilter{}, errInvalidQuery("to must be RFC3339")
		}
		filter.To = &to
	}
	limitRaw := strings.TrimSpace(query.Get("limit"))
	if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			return sessions.AuditListFilter{}, errInvalidQuery("limit must be an integer")
		}
		filter.Limit = limit
	}

	return filter, nil
}

func mapSessionSummaries(items []sessions.SessionSummary) []sessionSummaryResponse {
	resp := make([]sessionSummaryResponse, 0, len(items))
	for _, item := range items {
		row := sessionSummaryResponse{
			SessionID:       item.SessionID,
			User:            sessionSummaryUser{ID: item.UserID, Username: item.Username},
			Asset:           sessionSummaryAsset{ID: item.AssetID, Name: item.AssetName, AssetType: item.AssetType},
			Action:          item.Action,
			LaunchType:      item.LaunchType,
			Status:          item.Status,
			CreatedAt:       item.CreatedAt.UTC().Format(time.RFC3339Nano),
			DurationSeconds: item.DurationSeconds,
			Lifecycle: sessionLifecycleSummary{
				Started:    item.StartedAt != nil,
				Ended:      item.EndedAt != nil,
				EventCount: 0,
			},
		}
		if item.StartedAt != nil {
			row.StartedAt = item.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if item.EndedAt != nil {
			row.EndedAt = item.EndedAt.UTC().Format(time.RFC3339Nano)
		}
		resp = append(resp, row)
	}
	return resp
}

func mapAdminAuditItem(item sessions.AuditItem) adminAuditItemResponse {
	row := adminAuditItemResponse{
		ID:        item.ID,
		EventTime: item.EventTime.UTC().Format(time.RFC3339Nano),
		EventType: item.EventType,
		Action:    item.Action,
		Outcome:   item.Outcome,
		Metadata:  item.Metadata,
	}
	if row.Metadata == nil {
		row.Metadata = map[string]any{}
	}
	if item.ActorUserID != nil || item.ActorUser != nil {
		row.ActorUser = &sessionEventUser{}
		if item.ActorUserID != nil {
			row.ActorUser.ID = *item.ActorUserID
		}
		if item.ActorUser != nil {
			row.ActorUser.Username = *item.ActorUser
		}
	}
	if item.AssetID != nil {
		row.Asset = &adminAuditAsset{ID: *item.AssetID}
		if item.AssetName != nil {
			row.Asset.Name = *item.AssetName
		}
		if item.AssetType != nil {
			row.Asset.AssetType = *item.AssetType
		}
	}
	if item.SessionID != nil {
		row.SessionID = *item.SessionID
	}
	if item.Session != nil {
		row.Session = &adminAuditSession{
			ID:        item.Session.ID,
			Action:    item.Session.Action,
			Status:    item.Session.Status,
			CreatedAt: item.Session.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	return row
}

func errInvalidQuery(message string) error {
	return &launchValidationError{message: message}
}

func canViewSession(currentUser auth.CurrentUser, sessionUserID string) bool {
	if currentUser.ID == sessionUserID {
		return true
	}
	return currentUser.HasRole("admin") || currentUser.HasRole("auditor")
}

func isReadOnlyAuditor(currentUser auth.CurrentUser) bool {
	return currentUser.HasRole("auditor") && !currentUser.HasRole("admin")
}

func mapSessionDetail(item sessions.SessionDetail, lifecycle sessions.SessionLifecycleSummary) sessionDetailResponse {
	lifecycleResp := sessionLifecycleSummary{
		Started:            item.StartedAt != nil || lifecycle.ShellStarted,
		Ended:              item.EndedAt != nil || lifecycle.Ended,
		Failed:             lifecycle.Failed || item.Status == sessions.StatusFailed,
		ShellStarted:       lifecycle.ShellStarted,
		ConnectorRequested: lifecycle.ConnectorRequested,
		ConnectorSucceeded: lifecycle.ConnectorSucceeded,
		ConnectorFailed:    lifecycle.ConnectorFailed,
		EventCount:         lifecycle.EventCount,
	}
	if lifecycle.FirstEventAt != nil {
		lifecycleResp.FirstEventAt = lifecycle.FirstEventAt.UTC().Format(time.RFC3339Nano)
	}
	if lifecycle.LastEventAt != nil {
		lifecycleResp.LastEventAt = lifecycle.LastEventAt.UTC().Format(time.RFC3339Nano)
	}

	resp := sessionDetailResponse{
		SessionID:       item.SessionID,
		User:            sessionSummaryUser{ID: item.UserID, Username: item.Username},
		Asset:           sessionSummaryAsset{ID: item.AssetID, Name: item.AssetName, AssetType: item.AssetType},
		Action:          item.Action,
		LaunchType:      item.LaunchType,
		Protocol:        item.Protocol,
		Status:          item.Status,
		LaunchedVia:     item.LaunchedVia,
		CreatedAt:       item.CreatedAt.UTC().Format(time.RFC3339Nano),
		DurationSeconds: item.DurationSeconds,
		Lifecycle:       lifecycleResp,
	}
	if item.StartedAt != nil {
		resp.StartedAt = item.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if item.EndedAt != nil {
		resp.EndedAt = item.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func mapSessionEvents(action string, items []sessions.SessionEvent) []sessionEventResponse {
	var transcriptRowsByEvent map[int64][]normalizedTranscriptRow
	if shellSession := strings.TrimSpace(action) == "shell"; shellSession {
		transcriptRowsByEvent = map[int64][]normalizedTranscriptRow{}
		for _, row := range normalizeTranscriptRows(items) {
			if row.EventID <= 0 {
				continue
			}
			transcriptRowsByEvent[row.EventID] = append(transcriptRowsByEvent[row.EventID], row)
		}
	}

	resp := make([]sessionEventResponse, 0, len(items))
	for _, item := range items {
		row := sessionEventResponse{
			ID:        item.ID,
			EventType: item.EventType,
			EventTime: item.EventTime.UTC().Format(time.RFC3339Nano),
			Payload:   item.Payload,
		}
		if item.ActorUserID != nil || item.ActorUser != nil {
			row.ActorUser = &sessionEventUser{}
			if item.ActorUserID != nil {
				row.ActorUser.ID = *item.ActorUserID
			}
			if item.ActorUser != nil {
				row.ActorUser.Username = *item.ActorUser
			}
		}
		if transcriptRowsByEvent != nil {
			if normalizedRows, ok := transcriptRowsByEvent[item.ID]; ok && len(normalizedRows) > 0 {
				textParts := make([]string, 0, len(normalizedRows))
				for _, nrow := range normalizedRows {
					textParts = append(textParts, nrow.Text)
				}
				transcript := sessionTranscriptChunk{
					Direction: normalizedRows[0].Direction,
					Text:      strings.Join(textParts, "\n"),
				}
				if item.EventType == sessions.EventDataIn || item.EventType == sessions.EventDataOut {
					stream, _ := item.Payload["stream"].(string)
					transcript.Stream = stream
					size := 0
					switch typed := item.Payload["size"].(type) {
					case float64:
						size = int(typed)
					case int:
						size = typed
					case int64:
						size = int(typed)
					}
					transcript.Size = size
				}
				row.Transcript = &transcript
			}
		}
		resp = append(resp, row)
	}
	return resp
}

func mapReplayChunks(chunks []sessions.ReplayChunk) []sessionReplayChunk {
	resp := make([]sessionReplayChunk, 0, len(chunks))
	for _, chunk := range chunks {
		resp = append(resp, sessionReplayChunk{
			EventID:   chunk.EventID,
			EventTime: chunk.EventTime.UTC().Format(time.RFC3339Nano),
			EventType: chunk.EventType,
			Direction: chunk.Direction,
			Stream:    chunk.Stream,
			Size:      chunk.Size,
			Text:      chunk.Text,
			OffsetSec: chunk.OffsetSec,
			DelaySec:  chunk.DelaySec,
			Cols:      chunk.Cols,
			Rows:      chunk.Rows,
			Asciicast: chunk.Asciicast,
		})
	}
	return resp
}

func formatNullableTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatNullableInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func normalizeTranscriptRows(items []sessions.SessionEvent) []normalizedTranscriptRow {
	rows := make([]normalizedTranscriptRow, 0, len(items))
	pendingEchoes := make([]string, 0, 32)
	inputBuffer := ""

	for _, item := range items {
		if item.EventType != sessions.EventDataIn && item.EventType != sessions.EventDataOut {
			continue
		}
		encoded, _ := item.Payload["data"].(string)
		if strings.TrimSpace(encoded) == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !utf8.Valid(decoded) {
			continue
		}
		if item.EventType == sessions.EventDataIn {
			commands, nextBuf := consumeInputChunk(string(decoded), inputBuffer)
			inputBuffer = nextBuf
			for _, command := range commands {
				trimmed := strings.TrimSpace(command)
				if trimmed == "" {
					continue
				}
				rows = append(rows, normalizedTranscriptRow{
					EventID:   item.ID,
					EventTime: item.EventTime,
					Direction: "in",
					Text:      trimmed,
				})
				pendingEchoes = append(pendingEchoes, trimmed)
				if len(pendingEchoes) > 32 {
					pendingEchoes = pendingEchoes[1:]
				}
			}
			continue
		}

		for _, line := range normalizeOutputChunk(string(decoded)) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if len(pendingEchoes) > 0 && trimmed == pendingEchoes[0] {
				pendingEchoes = pendingEchoes[1:]
				continue
			}
			rows = append(rows, normalizedTranscriptRow{
				EventID:   item.ID,
				EventTime: item.EventTime,
				Direction: "out",
				Text:      trimmed,
			})
		}
	}
	return rows
}

func consumeInputChunk(chunk, buffer string) ([]string, string) {
	commands := make([]string, 0, 1)
	clean := stripANSI(chunk)
	current := buffer
	lastBreak := rune(0)
	for _, r := range clean {
		if r == '\r' || r == '\n' {
			if lastBreak == '\r' && r == '\n' {
				lastBreak = r
				continue
			}
			if strings.TrimSpace(current) != "" {
				commands = append(commands, current)
			}
			current = ""
			lastBreak = r
			continue
		}
		lastBreak = 0
		if r == '\b' || r == 0x7f {
			current = trimLastRune(current)
			continue
		}
		if isControlRune(r) {
			continue
		}
		current += string(r)
	}
	return commands, current
}

func normalizeOutputChunk(chunk string) []string {
	clean := strings.ReplaceAll(stripANSI(chunk), "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\x1b[?2004h", "")
	clean = strings.ReplaceAll(clean, "\x1b[?2004l", "")

	lines := make([]string, 0, 2)
	current := ""
	for _, r := range clean {
		if r == '\r' {
			current = ""
			continue
		}
		if r == '\n' {
			if current != "" {
				lines = append(lines, current)
			}
			current = ""
			continue
		}
		if r == '\b' || r == 0x7f {
			current = trimLastRune(current)
			continue
		}
		if isControlRune(r) {
			continue
		}
		current += string(r)
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func stripANSI(value string) string {
	return ansiCSIRegex.ReplaceAllString(ansiOSCRegex.ReplaceAllString(value, ""), "")
}

var (
	ansiCSIRegex = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCRegex = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
)

func isControlRune(r rune) bool {
	if r == '\t' {
		return false
	}
	return r >= 0 && r < 0x20
}

func trimLastRune(value string) string {
	if value == "" {
		return value
	}
	_, size := utf8.DecodeLastRuneInString(value)
	if size <= 0 || size > len(value) {
		return ""
	}
	return value[:len(value)-size]
}
