package httpserver

import (
	"net/http"

	"github.com/districtdco/accessd/api/internal/auth"
	"github.com/districtdco/accessd/api/internal/handlers"
)

type RouteHandlers struct {
	Health    *handlers.HealthHandler
	Version   *handlers.VersionHandler
	Connector *handlers.ConnectorReleasesHandler
	Auth      *handlers.AuthHandler
	Access    *handlers.AccessHandler
	Sessions  *handlers.SessionsHandler
	Admin     *handlers.AdminHandler
	AuthSvc   *auth.Service
}

func NewRouter(h RouteHandlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", h.Health.Live)
	mux.HandleFunc("GET /health/ready", h.Health.Ready)
	mux.HandleFunc("GET /version", h.Version.Get)
	if h.Connector != nil {
		mux.HandleFunc("GET /connector/releases/latest", h.Connector.Latest)
		mux.HandleFunc("GET /connector/releases", h.Connector.Versions)
	}
	mux.HandleFunc("POST /connector/token/verify", h.Sessions.VerifyConnectorToken)
	mux.HandleFunc("POST /connector/bootstrap/verify", h.Sessions.VerifyConnectorBootstrapToken)
	mux.Handle("POST /connector/bootstrap/issue", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.IssueConnectorBootstrapToken)))
	mux.HandleFunc("POST /auth/login", h.Auth.Login)
	mux.HandleFunc("POST /auth/logout", h.Auth.Logout)
	mux.Handle("GET /me", h.AuthSvc.Authenticated(http.HandlerFunc(h.Auth.Me)))
	mux.Handle("PUT /auth/password", h.AuthSvc.Authenticated(http.HandlerFunc(h.Auth.ChangePassword)))
	mux.Handle("GET /auth/ping", h.AuthSvc.Authenticated(http.HandlerFunc(h.Auth.AuthPing)))
	mux.Handle("GET /access/my", h.AuthSvc.Authenticated(http.HandlerFunc(h.Access.MyAccess)))
	mux.Handle("GET /sessions/my", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.MySessions)))
	mux.Handle("GET /sessions/{sessionID}", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.Detail)))
	mux.Handle("GET /sessions/{sessionID}/events", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.Events)))
	mux.Handle("GET /sessions/{sessionID}/replay", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.Replay)))
	mux.Handle("GET /sessions/{sessionID}/export/summary", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.ExportSessionSummary)))
	mux.Handle("GET /sessions/{sessionID}/export/transcript", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.ExportSessionTranscript)))
	mux.Handle("POST /sessions/launch", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.Launch)))
	mux.Handle("POST /sessions/{sessionID}/events", h.AuthSvc.Authenticated(http.HandlerFunc(h.Sessions.RecordEvent)))
	mux.Handle("GET /admin/ping", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Auth.AdminPing), "admin"))
	mux.Handle("GET /admin/users", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListUsers), "admin"))
	mux.Handle("POST /admin/users", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.CreateUser), "admin"))
	mux.Handle("GET /admin/users/{userID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.GetUserDetail), "admin"))
	mux.Handle("PUT /admin/users/{userID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.UpdateUser), "admin"))
	mux.Handle("PUT /admin/users/{userID}/active", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.SetUserActive), "admin"))
	mux.Handle("PUT /admin/users/{userID}/password", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ResetUserPassword), "admin"))
	mux.Handle("GET /admin/users/{userID}/effective-access", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.UserEffectiveAccess), "admin"))
	mux.Handle("GET /admin/users/{userID}/grants", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListUserGrants), "admin"))
	mux.Handle("POST /admin/users/{userID}/grants", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.AddUserGrant), "admin"))
	mux.Handle("DELETE /admin/users/{userID}/grants/{assetID}/{action}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.RemoveUserGrant), "admin"))
	mux.Handle("POST /admin/users/{userID}/roles", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.AssignRoleToUser), "admin"))
	mux.Handle("DELETE /admin/users/{userID}/roles/{roleName}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.RemoveRoleFromUser), "admin"))
	mux.Handle("GET /admin/roles", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListRoles), "admin"))
	mux.Handle("GET /admin/groups", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListGroups), "admin"))
	mux.Handle("GET /admin/groups/{groupID}/members", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListGroupMembers), "admin"))
	mux.Handle("GET /admin/groups/{groupID}/grants", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListGroupGrants), "admin"))
	mux.Handle("GET /admin/assets", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListAssets), "admin"))
	mux.Handle("POST /admin/assets", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.CreateAsset), "admin"))
	mux.Handle("GET /admin/assets/{assetID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.GetAssetDetail), "admin"))
	mux.Handle("PUT /admin/assets/{assetID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.UpdateAsset), "admin"))
	mux.Handle("DELETE /admin/assets/{assetID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.DeleteAsset), "admin"))
	mux.Handle("GET /admin/assets/{assetID}/credentials", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListAssetCredentials), "admin"))
	mux.Handle("PUT /admin/assets/{assetID}/credentials/{credentialType}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.UpsertAssetCredential), "admin"))
	mux.Handle("GET /admin/assets/{assetID}/grants", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListAssetGrants), "admin"))
	mux.Handle("GET /admin/ldap/settings", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.GetLDAPSettings), "admin"))
	mux.Handle("PUT /admin/ldap/settings", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.UpsertLDAPSettings), "admin"))
	mux.Handle("POST /admin/ldap/test", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.TestLDAPConnection), "admin"))
	mux.Handle("POST /admin/ldap/sync", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.TriggerLDAPSync), "admin"))
	mux.Handle("GET /admin/ldap/sync-runs", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Admin.ListLDAPSyncRuns), "admin"))
	mux.Handle("GET /admin/sessions", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminSessions), "admin", "auditor"))
	mux.Handle("GET /admin/sessions/export", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminExportSessionsCSV), "admin", "auditor"))
	mux.Handle("GET /admin/sessions/active", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminActiveSessions), "admin", "auditor"))
	mux.Handle("GET /admin/audit/recent", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminRecentAudit), "admin", "auditor"))
	mux.Handle("GET /admin/audit/events", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminAuditEvents), "admin", "auditor"))
	mux.Handle("GET /admin/audit/events/{eventID}", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminAuditEventDetail), "admin", "auditor"))
	mux.Handle("GET /admin/summary", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Sessions.AdminSummary), "admin", "auditor"))
	return mux
}
