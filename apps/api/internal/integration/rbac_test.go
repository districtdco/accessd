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
	activeResp := h.requestJSON(http.MethodGet, "/admin/sessions/active", nil, auditorCookie)
	if activeResp.Code != http.StatusOK {
		t.Fatalf("expected auditor /admin/sessions/active 200, got %d", activeResp.Code)
	}
	exportResp := h.requestJSON(http.MethodGet, "/admin/sessions/export", nil, auditorCookie)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("expected auditor /admin/sessions/export 200, got %d", exportResp.Code)
	}

	// Auditor cannot access admin-only endpoints
	usersResp := h.requestJSON(http.MethodGet, "/admin/users", nil, auditorCookie)
	if usersResp.Code != http.StatusForbidden {
		t.Fatalf("expected auditor /admin/users 403, got %d", usersResp.Code)
	}
}

func TestRBAC_AdminMutationEndpointsDenyNonAdmins(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	viewerCookie := h.login(h.seed.viewerName, h.seed.viewerPass)
	auditorID := h.createLocalUserWithRole("auditor2", "auditor123", "auditor2@example.com", "Auditor Two", "auditor")
	_ = auditorID
	auditorCookie := h.login("auditor2", "auditor123")

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/users/" + h.seed.viewerID + "/roles"},
		{method: http.MethodDelete, path: "/admin/users/" + h.seed.viewerID + "/roles/user"},
		{method: http.MethodPost, path: "/admin/users/" + h.seed.viewerID + "/grants"},
		{method: http.MethodDelete, path: "/admin/users/" + h.seed.viewerID + "/grants/" + h.seed.allowedAssetID + "/shell"},
		{method: http.MethodPost, path: "/admin/assets"},
		{method: http.MethodPut, path: "/admin/assets/" + h.seed.allowedAssetID},
		{method: http.MethodPut, path: "/admin/assets/" + h.seed.allowedAssetID + "/credentials/password"},
	}

	for _, tc := range tests {
		unauth := h.requestJSON(tc.method, tc.path, nil, nil)
		if unauth.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected unauth 401, got %d", tc.method, tc.path, unauth.Code)
		}

		operatorResp := h.requestJSON(tc.method, tc.path, nil, operatorCookie)
		if operatorResp.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected operator 403, got %d", tc.method, tc.path, operatorResp.Code)
		}

		viewerResp := h.requestJSON(tc.method, tc.path, nil, viewerCookie)
		if viewerResp.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected user/viewer 403, got %d", tc.method, tc.path, viewerResp.Code)
		}

		auditorResp := h.requestJSON(tc.method, tc.path, nil, auditorCookie)
		if auditorResp.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected auditor 403, got %d", tc.method, tc.path, auditorResp.Code)
		}
	}
}

func TestRBAC_AuditorDeniedOperationalAccessEndpoints(t *testing.T) {
	h := newTestHarness(t)

	auditorID := h.createLocalUserWithRole("auditor6", "auditor123", "auditor6@example.com", "Auditor Six", "auditor")
	createdBy := &h.seed.adminID
	if err := h.access.GrantUserAction(h.ctx, auditorID, h.seed.allowedAssetID, "shell", createdBy); err != nil {
		t.Fatalf("grant auditor access: %v", err)
	}
	auditorCookie := h.login("auditor6", "auditor123")

	accessResp := h.requestJSON(http.MethodGet, "/access/my", nil, auditorCookie)
	if accessResp.Code != http.StatusForbidden {
		t.Fatalf("expected auditor /access/my 403, got %d", accessResp.Code)
	}

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, auditorCookie)
	if launchResp.Code != http.StatusForbidden {
		t.Fatalf("expected auditor /sessions/launch 403, got %d", launchResp.Code)
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
