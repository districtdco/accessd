package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestAuthSessionFlow_LoginLogoutMe(t *testing.T) {
	h := newTestHarness(t)

	loginResp := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{
		"username": h.seed.adminUsername,
		"password": h.seed.adminPassword,
	}, nil)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", loginResp.Code, loginResp.Body.String())
	}

	cookies := loginResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie from login")
	}
	sessionCookie := cookies[0]

	loginPayload := h.responseJSON(t, loginResp)
	userPayload, ok := loginPayload["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected login user payload, got: %#v", loginPayload)
	}
	if userPayload["username"] != h.seed.adminUsername {
		t.Fatalf("expected username %q, got %#v", h.seed.adminUsername, userPayload["username"])
	}

	meResp := h.requestJSON(http.MethodGet, "/me", nil, sessionCookie)
	if meResp.Code != http.StatusOK {
		t.Fatalf("expected /me 200, got %d: %s", meResp.Code, meResp.Body.String())
	}

	logoutResp := h.requestJSON(http.MethodPost, "/auth/logout", nil, sessionCookie)
	if logoutResp.Code != http.StatusNoContent {
		t.Fatalf("expected logout 204, got %d: %s", logoutResp.Code, logoutResp.Body.String())
	}

	postLogoutMeResp := h.requestJSON(http.MethodGet, "/me", nil, sessionCookie)
	if postLogoutMeResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected /me 401 after logout, got %d: %s", postLogoutMeResp.Code, postLogoutMeResp.Body.String())
	}
}

func TestAdminRBAC_UnauthorizedForbiddenAndAllowed(t *testing.T) {
	h := newTestHarness(t)

	unauthResp := h.requestJSON(http.MethodGet, "/admin/ping", nil, nil)
	if unauthResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauth /admin/ping 401, got %d", unauthResp.Code)
	}

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	forbiddenResp := h.requestJSON(http.MethodGet, "/admin/ping", nil, operatorCookie)
	if forbiddenResp.Code != http.StatusForbidden {
		t.Fatalf("expected operator /admin/ping 403, got %d", forbiddenResp.Code)
	}

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)
	allowedResp := h.requestJSON(http.MethodGet, "/admin/ping", nil, adminCookie)
	if allowedResp.Code != http.StatusOK {
		t.Fatalf("expected admin /admin/ping 200, got %d", allowedResp.Code)
	}
}

func TestAccessMy_ForSeededUser(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	resp := h.requestJSON(http.MethodGet, "/access/my", nil, operatorCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected /access/my 200, got %d: %s", resp.Code, resp.Body.String())
	}

	payload := h.responseJSON(t, resp)
	items, ok := payload["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty access items, got %#v", payload)
	}

	firstItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first access item object, got %#v", items[0])
	}
	if firstItem["asset_id"] != h.seed.allowedAssetID {
		t.Fatalf("expected granted asset_id %s, got %#v", h.seed.allowedAssetID, firstItem["asset_id"])
	}
	actions, ok := firstItem["allowed_actions"].([]any)
	if !ok {
		t.Fatalf("expected allowed_actions array, got %#v", firstItem["allowed_actions"])
	}
	foundShell := false
	for _, action := range actions {
		if action == "shell" {
			foundShell = true
			break
		}
	}
	if !foundShell {
		t.Fatalf("expected shell action in allowed_actions, got %#v", actions)
	}
}

func TestSessionsLaunch_HappyPathAndDeniedPath(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	happyResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if happyResp.Code != http.StatusOK {
		t.Fatalf("expected launch happy path 200, got %d: %s", happyResp.Code, happyResp.Body.String())
	}
	happyPayload := h.responseJSON(t, happyResp)
	if strings.TrimSpace(asString(happyPayload["session_id"])) == "" {
		t.Fatalf("expected session_id in launch payload: %#v", happyPayload)
	}
	if asString(happyPayload["launch_type"]) != "shell" {
		t.Fatalf("expected launch_type shell, got %#v", happyPayload["launch_type"])
	}

	deniedResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.deniedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if deniedResp.Code != http.StatusForbidden {
		t.Fatalf("expected launch denied path 403, got %d: %s", deniedResp.Code, deniedResp.Body.String())
	}
}

func TestSessionsListing_MyAndAdminFilters(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	secondaryCookie := h.login("operator2", "operator456")
	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	operatorLaunch := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if operatorLaunch.Code != http.StatusOK {
		t.Fatalf("expected operator launch 200, got %d", operatorLaunch.Code)
	}

	secondaryLaunch := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.secondaryAsset,
		"action":   "shell",
	}, secondaryCookie)
	if secondaryLaunch.Code != http.StatusOK {
		t.Fatalf("expected secondary launch 200, got %d", secondaryLaunch.Code)
	}

	myResp := h.requestJSON(http.MethodGet, "/sessions/my?status=pending", nil, operatorCookie)
	if myResp.Code != http.StatusOK {
		t.Fatalf("expected /sessions/my 200, got %d: %s", myResp.Code, myResp.Body.String())
	}
	myPayload := h.responseJSON(t, myResp)
	myItems, ok := myPayload["items"].([]any)
	if !ok || len(myItems) == 0 {
		t.Fatalf("expected /sessions/my items, got %#v", myPayload)
	}
	for _, raw := range myItems {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected /sessions/my item object, got %#v", raw)
		}
		user, ok := item["user"].(map[string]any)
		if !ok {
			t.Fatalf("expected /sessions/my user object, got %#v", item)
		}
		if asString(user["id"]) != h.seed.operatorID {
			t.Fatalf("expected /sessions/my to only include operator sessions, got user_id=%s", asString(user["id"]))
		}
	}

	adminFilterResp := h.requestJSON(http.MethodGet, "/admin/sessions?status=pending&user_id="+h.seed.operatorID, nil, adminCookie)
	if adminFilterResp.Code != http.StatusOK {
		t.Fatalf("expected admin sessions filter 200, got %d: %s", adminFilterResp.Code, adminFilterResp.Body.String())
	}
	adminPayload := h.responseJSON(t, adminFilterResp)
	adminItems, ok := adminPayload["items"].([]any)
	if !ok || len(adminItems) == 0 {
		t.Fatalf("expected admin filtered sessions, got %#v", adminPayload)
	}
	for _, raw := range adminItems {
		item := raw.(map[string]any)
		user := item["user"].(map[string]any)
		if asString(user["id"]) != h.seed.operatorID {
			t.Fatalf("expected admin filtered sessions user_id=%s, got %s", h.seed.operatorID, asString(user["id"]))
		}
	}

	nonAdminResp := h.requestJSON(http.MethodGet, "/admin/sessions", nil, operatorCookie)
	if nonAdminResp.Code != http.StatusForbidden {
		t.Fatalf("expected operator /admin/sessions 403, got %d", nonAdminResp.Code)
	}
}

func TestExportEndpoints_Authorization(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	viewerCookie := h.login(h.seed.viewerName, h.seed.viewerPass)
	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if launchResp.Code != http.StatusOK {
		t.Fatalf("expected launch 200 for export setup, got %d: %s", launchResp.Code, launchResp.Body.String())
	}
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])
	if sessionID == "" {
		t.Fatalf("missing session_id in launch payload: %#v", launchPayload)
	}

	summaryPath := "/sessions/" + sessionID + "/export/summary"
	unauthSummary := h.requestJSON(http.MethodGet, summaryPath, nil, nil)
	if unauthSummary.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauth summary export 401, got %d", unauthSummary.Code)
	}
	viewerSummary := h.requestJSON(http.MethodGet, summaryPath, nil, viewerCookie)
	if viewerSummary.Code != http.StatusForbidden {
		t.Fatalf("expected non-owner summary export 403, got %d", viewerSummary.Code)
	}
	ownerSummary := h.requestJSON(http.MethodGet, summaryPath, nil, operatorCookie)
	if ownerSummary.Code != http.StatusOK {
		t.Fatalf("expected owner summary export 200, got %d: %s", ownerSummary.Code, ownerSummary.Body.String())
	}
	if !strings.Contains(ownerSummary.Header().Get("Content-Disposition"), "summary") {
		t.Fatalf("expected summary export content-disposition, got %q", ownerSummary.Header().Get("Content-Disposition"))
	}

	adminExportPath := "/admin/sessions/export"
	unauthAdminExport := h.requestJSON(http.MethodGet, adminExportPath, nil, nil)
	if unauthAdminExport.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauth admin export 401, got %d", unauthAdminExport.Code)
	}
	viewerAdminExport := h.requestJSON(http.MethodGet, adminExportPath, nil, viewerCookie)
	if viewerAdminExport.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin admin export 403, got %d", viewerAdminExport.Code)
	}
	adminExport := h.requestJSON(http.MethodGet, adminExportPath, nil, adminCookie)
	if adminExport.Code != http.StatusOK {
		t.Fatalf("expected admin export 200, got %d: %s", adminExport.Code, adminExport.Body.String())
	}
	if !strings.Contains(adminExport.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("expected csv content-type, got %q", adminExport.Header().Get("Content-Type"))
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
