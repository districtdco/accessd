package integration

import (
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthLogin_InvalidCredentials(t *testing.T) {
	h := newTestHarness(t)

	resp := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": h.seed.adminUsername,
		"password": "wrong-password",
	}, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAuthLogin_UnknownUser(t *testing.T) {
	h := newTestHarness(t)

	resp := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": "nonexistent-user",
		"password": "whatever",
	}, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown user, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAuthLogin_EmptyBody(t *testing.T) {
	h := newTestHarness(t)

	resp := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": "",
		"password": "",
	}, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for empty credentials, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAuthLogin_MalformedJSON(t *testing.T) {
	h := newTestHarness(t)

	resp := h.requestJSON(http.MethodPost, "/auth/login", "not-json", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed json, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAuthMe_Unauthenticated(t *testing.T) {
	h := newTestHarness(t)

	resp := h.requestJSON(http.MethodGet, "/me", nil, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /me, got %d", resp.Code)
	}
}

func TestAuthMe_ReturnsRoles(t *testing.T) {
	h := newTestHarness(t)

	cookie := h.login(h.seed.adminUsername, h.seed.adminPassword)
	resp := h.requestJSON(http.MethodGet, "/me", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	payload := h.responseJSON(t, resp)
	roles, ok := payload["roles"].([]any)
	if !ok || len(roles) == 0 {
		t.Fatalf("expected non-empty roles, got %#v", payload)
	}
	found := false
	for _, r := range roles {
		if r == "admin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected admin role in %#v", roles)
	}
}

func TestAuthLogout_InvalidatesCookie(t *testing.T) {
	h := newTestHarness(t)

	cookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	// cookie should work before logout
	meResp := h.requestJSON(http.MethodGet, "/me", nil, cookie)
	if meResp.Code != http.StatusOK {
		t.Fatalf("expected /me 200 before logout, got %d", meResp.Code)
	}

	// logout
	logoutResp := h.requestJSON(http.MethodPost, "/auth/logout", nil, cookie)
	if logoutResp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", logoutResp.Code)
	}

	// cookie should be invalid after logout
	meResp2 := h.requestJSON(http.MethodGet, "/me", nil, cookie)
	if meResp2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", meResp2.Code)
	}
}

func TestAuthLogin_MultipleUsersIndependent(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)
	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	// admin /me shows admin
	adminMe := h.requestJSON(http.MethodGet, "/me", nil, adminCookie)
	adminPayload := h.responseJSON(t, adminMe)
	if asString(adminPayload["username"]) != h.seed.adminUsername {
		t.Fatalf("expected admin username, got %s", asString(adminPayload["username"]))
	}

	// operator /me shows operator
	opMe := h.requestJSON(http.MethodGet, "/me", nil, operatorCookie)
	opPayload := h.responseJSON(t, opMe)
	if asString(opPayload["username"]) != h.seed.operatorName {
		t.Fatalf("expected operator username, got %s", asString(opPayload["username"]))
	}
}

func TestAuthChangePassword_Success(t *testing.T) {
	h := newTestHarness(t)
	cookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	resp := h.requestJSON(http.MethodPut, "/auth/password", map[string]any{
		"current_password": h.seed.operatorPass,
		"new_password":     "operator1234",
	}, cookie)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}

	const hashQuery = `SELECT COALESCE(password_hash, '') FROM users WHERE id = $1 LIMIT 1;`
	var storedHash string
	if err := h.pool.QueryRow(h.ctx, hashQuery, h.seed.operatorID).Scan(&storedHash); err != nil {
		t.Fatalf("load stored hash: %v", err)
	}
	if storedHash == "" {
		t.Fatal("expected non-empty password hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("operator1234")); err != nil {
		t.Fatalf("new password hash mismatch: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(h.seed.operatorPass)); err == nil {
		t.Fatal("old password should not match updated hash")
	}

	oldLogin := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": h.seed.operatorName,
		"password": h.seed.operatorPass,
	}, nil)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected old password login to fail with 401, got %d: %s", oldLogin.Code, oldLogin.Body.String())
	}

	newLogin := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": h.seed.operatorName,
		"password": "operator1234",
	}, nil)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("expected new password login to succeed with 200, got %d: %s", newLogin.Code, newLogin.Body.String())
	}
}

func TestAuthChangePassword_InvalidCurrentPassword(t *testing.T) {
	h := newTestHarness(t)
	cookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	resp := h.requestJSON(http.MethodPut, "/auth/password", map[string]any{
		"current_password": "wrong-current",
		"new_password":     "operator1234",
	}, cookie)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAuthChangePassword_RejectsLDAPManagedUser(t *testing.T) {
	h := newTestHarness(t)
	cookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	const setLDAP = `UPDATE users SET auth_provider = 'ldap', password_hash = NULL WHERE id = $1;`
	if _, err := h.pool.Exec(h.ctx, setLDAP, h.seed.operatorID); err != nil {
		t.Fatalf("set ldap provider for user: %v", err)
	}

	resp := h.requestJSON(http.MethodPut, "/auth/password", map[string]any{
		"current_password": h.seed.operatorPass,
		"new_password":     "operator1234",
	}, cookie)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.Code, resp.Body.String())
	}
}
