package integration

import (
	"net/http"
	"testing"
)

func TestRBAC_AdminEndpointsRequireAdminRole(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)
	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	adminEndpoints := []string{
		"/admin/users",
		"/admin/roles",
		"/admin/groups",
		"/admin/assets",
	}

	for _, endpoint := range adminEndpoints {
		// unauthenticated -> 401
		unauth := h.requestJSON(http.MethodGet, endpoint, nil, nil)
		if unauth.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401 for unauth, got %d", endpoint, unauth.Code)
		}
		// non-admin -> 403
		forbidden := h.requestJSON(http.MethodGet, endpoint, nil, operatorCookie)
		if forbidden.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403 for operator, got %d", endpoint, forbidden.Code)
		}
		// admin -> 200
		allowed := h.requestJSON(http.MethodGet, endpoint, nil, adminCookie)
		if allowed.Code != http.StatusOK {
			t.Errorf("%s: expected 200 for admin, got %d: %s", endpoint, allowed.Code, allowed.Body.String())
		}
	}
}

func TestRBAC_AuditorCanAccessSessionsAndAudit(t *testing.T) {
	h := newTestHarness(t)

	auditorID := h.createLocalUserWithRole("auditor1", "auditor123", "auditor@example.com", "Auditor One", "auditor")
	_ = auditorID
	auditorCookie := h.login("auditor1", "auditor123")

	// Auditor can access admin sessions
	sessResp := h.requestJSON(http.MethodGet, "/admin/sessions", nil, auditorCookie)
	if sessResp.Code != http.StatusOK {
		t.Fatalf("expected auditor /admin/sessions 200, got %d", sessResp.Code)
	}

	// Auditor can access audit events
	auditResp := h.requestJSON(http.MethodGet, "/admin/audit/events", nil, auditorCookie)
	if auditResp.Code != http.StatusOK {
		t.Fatalf("expected auditor /admin/audit/events 200, got %d", auditResp.Code)
	}

	// Auditor can access summary
	summaryResp := h.requestJSON(http.MethodGet, "/admin/summary", nil, auditorCookie)
	if summaryResp.Code != http.StatusOK {
		t.Fatalf("expected auditor /admin/summary 200, got %d", summaryResp.Code)
	}

	// Auditor cannot access admin-only endpoints
	usersResp := h.requestJSON(http.MethodGet, "/admin/users", nil, auditorCookie)
	if usersResp.Code != http.StatusForbidden {
		t.Fatalf("expected auditor /admin/users 403, got %d", usersResp.Code)
	}
}

func TestRBAC_RoleAssignmentAndRemoval(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	// Assign auditor role to operator
	assignResp := h.requestJSON(http.MethodPost, "/admin/users/"+h.seed.operatorID+"/roles", map[string]any{
		"role": "auditor",
	}, adminCookie)
	if assignResp.Code != http.StatusOK && assignResp.Code != http.StatusNoContent {
		t.Fatalf("expected role assignment success, got %d: %s", assignResp.Code, assignResp.Body.String())
	}

	// Verify the operator now has auditor role by checking /admin/sessions
	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	sessResp := h.requestJSON(http.MethodGet, "/admin/sessions", nil, operatorCookie)
	if sessResp.Code != http.StatusOK {
		t.Fatalf("expected operator with auditor role to access /admin/sessions, got %d", sessResp.Code)
	}

	// Remove auditor role
	removeResp := h.requestJSON(http.MethodDelete, "/admin/users/"+h.seed.operatorID+"/roles/auditor", nil, adminCookie)
	if removeResp.Code != http.StatusOK && removeResp.Code != http.StatusNoContent {
		t.Fatalf("expected role removal success, got %d: %s", removeResp.Code, removeResp.Body.String())
	}

	// Re-login to pick up new role set
	operatorCookie2 := h.login(h.seed.operatorName, h.seed.operatorPass)
	sessResp2 := h.requestJSON(http.MethodGet, "/admin/sessions", nil, operatorCookie2)
	if sessResp2.Code != http.StatusForbidden {
		t.Fatalf("expected operator without auditor role to get 403, got %d", sessResp2.Code)
	}
}
