package httpserver

import (
	"net/http"

	"github.com/districtd/pam/api/internal/auth"
	"github.com/districtd/pam/api/internal/handlers"
)

type RouteHandlers struct {
	Health  *handlers.HealthHandler
	Version *handlers.VersionHandler
	Auth    *handlers.AuthHandler
	AuthSvc *auth.Service
}

func NewRouter(h RouteHandlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", h.Health.Live)
	mux.HandleFunc("GET /health/ready", h.Health.Ready)
	mux.HandleFunc("GET /version", h.Version.Get)
	mux.HandleFunc("POST /auth/login", h.Auth.Login)
	mux.HandleFunc("POST /auth/logout", h.Auth.Logout)
	mux.Handle("GET /me", h.AuthSvc.Authenticated(http.HandlerFunc(h.Auth.Me)))
	mux.Handle("GET /auth/ping", h.AuthSvc.Authenticated(http.HandlerFunc(h.Auth.AuthPing)))
	mux.Handle("GET /admin/ping", h.AuthSvc.RequireRoles(http.HandlerFunc(h.Auth.AdminPing), "admin"))
	return mux
}
