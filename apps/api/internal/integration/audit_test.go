package integration

import (
	"net/http"
	"testing"
)

func TestAuditEvents_AdminCanList(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	resp := h.requestJSON(http.MethodGet, "/admin/audit/events", nil, adminCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	payload := h.responseJSON(t, resp)
	if _, ok := payload["items"]; !ok {
		t.Fatalf("expected items key in audit events response")
	}
}

func TestAuditEvents_FilterByEventType(t *testing.T) {
	h := newTestHarness(t)

	// Login to generate audit events
	_ = h.login(h.seed.adminUsername, h.seed.adminPassword)
	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	resp := h.requestJSON(http.MethodGet, "/admin/audit/events?event_type=login_success", nil, adminCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	payload := h.responseJSON(t, resp)
	items, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %#v", payload)
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if asString(item["event_type"]) != "login_success" {
			t.Fatalf("expected all items to be login_success, got %s", asString(item["event_type"]))
		}
	}
	_ = items
}

func TestAuditEvents_NonAdminDenied(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	resp := h.requestJSON(http.MethodGet, "/admin/audit/events", nil, operatorCookie)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for operator, got %d", resp.Code)
	}
}

func TestAuditRecentActivity_AdminCanAccess(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	resp := h.requestJSON(http.MethodGet, "/admin/audit/recent", nil, adminCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAdminSummary_ReturnsMetrics(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	// Create a session to have something to count
	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	_ = h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)

	resp := h.requestJSON(http.MethodGet, "/admin/summary", nil, adminCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	payload := h.responseJSON(t, resp)
	if _, ok := payload["metrics"]; !ok {
		t.Fatalf("expected metrics in summary response")
	}
	if _, ok := payload["window_days"]; !ok {
		t.Fatalf("expected window_days in summary response")
	}
}

func TestAdminSessionsExport_CSV(t *testing.T) {
	h := newTestHarness(t)

	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	resp := h.requestJSON(http.MethodGet, "/admin/sessions/export", nil, adminCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	ct := resp.Header().Get("Content-Type")
	if ct != "text/csv" {
		t.Fatalf("expected text/csv content-type, got %q", ct)
	}
}
