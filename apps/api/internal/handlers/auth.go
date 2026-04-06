package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/districtd/pam/api/internal/auth"
)

type AuthHandler struct {
	authService *auth.Service
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Roles       []string `json:"roles"`
}

type loginResponse struct {
	User userResponse `json:"user"`
}

func NewAuthHandler(authService *auth.Service) *AuthHandler {
	return &AuthHandler{authService: authService}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	result, cookie, err := h.authService.LoginWithContext(r.Context(), auth.LoginRequest{
		Username:  req.Username,
		Password:  req.Password,
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		if err == auth.ErrRateLimited {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed login attempts, please wait"})
			return
		}
		if err == auth.ErrInvalidCredentials {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}

	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, loginResponse{User: mapUser(result.User)})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	return host
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(h.authService.SessionCookieName())
	if err == nil {
		_ = h.authService.Logout(r.Context(), cookie.Value)
	}

	http.SetCookie(w, h.authService.ClearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	writeJSON(w, http.StatusOK, userResponse{
		ID:          currentUser.ID,
		Username:    currentUser.Username,
		Email:       currentUser.Email,
		DisplayName: currentUser.DisplayName,
		Roles:       currentUser.Roles,
	})
}

func (h *AuthHandler) AuthPing(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"scope":  "authenticated",
		"user":   currentUser.Username,
	})
}

func (h *AuthHandler) AdminPing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"scope":  "admin",
	})
}

func mapUser(user auth.User) userResponse {
	return userResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Roles:       user.Roles,
	}
}
