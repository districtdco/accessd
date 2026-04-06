package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestSessionDetail_OwnerCanAccess(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	// Launch a session
	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if launchResp.Code != http.StatusOK {
		t.Fatalf("expected launch 200, got %d: %s", launchResp.Code, launchResp.Body.String())
	}
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	// Owner can access detail
	detailResp := h.requestJSON(http.MethodGet, "/sessions/"+sessionID, nil, operatorCookie)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("expected owner session detail 200, got %d: %s", detailResp.Code, detailResp.Body.String())
	}
	detail := h.responseJSON(t, detailResp)
	if asString(detail["session_id"]) != sessionID {
		t.Fatalf("expected session_id %s, got %s", sessionID, asString(detail["session_id"]))
	}
}

func TestSessionDetail_NonOwnerDenied(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	viewerCookie := h.login(h.seed.viewerName, h.seed.viewerPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	// Non-owner cannot access detail
	viewerDetail := h.requestJSON(http.MethodGet, "/sessions/"+sessionID, nil, viewerCookie)
	if viewerDetail.Code != http.StatusForbidden {
		t.Fatalf("expected non-owner session detail 403, got %d", viewerDetail.Code)
	}
}

func TestSessionDetail_AdminCanAccess(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)
	adminCookie := h.login(h.seed.adminUsername, h.seed.adminPassword)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	// Admin can access any session detail
	adminDetail := h.requestJSON(http.MethodGet, "/sessions/"+sessionID, nil, adminCookie)
	if adminDetail.Code != http.StatusOK {
		t.Fatalf("expected admin session detail 200, got %d: %s", adminDetail.Code, adminDetail.Body.String())
	}
}

func TestSessionEvents_OwnerCanAccess(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	eventsResp := h.requestJSON(http.MethodGet, "/sessions/"+sessionID+"/events", nil, operatorCookie)
	if eventsResp.Code != http.StatusOK {
		t.Fatalf("expected events 200, got %d: %s", eventsResp.Code, eventsResp.Body.String())
	}
	eventsPayload := h.responseJSON(t, eventsResp)
	items, ok := eventsPayload["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %#v", eventsPayload)
	}
	// Should have at least the launch_created event
	if len(items) == 0 {
		t.Fatalf("expected at least one event, got 0")
	}
}

func TestSessionReplay_ReturnsShape(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	replayResp := h.requestJSON(http.MethodGet, "/sessions/"+sessionID+"/replay", nil, operatorCookie)
	if replayResp.Code != http.StatusOK {
		t.Fatalf("expected replay 200, got %d: %s", replayResp.Code, replayResp.Body.String())
	}
	replayPayload := h.responseJSON(t, replayResp)
	if asString(replayPayload["session_id"]) != sessionID {
		t.Fatalf("expected replay session_id match")
	}
}

func TestSessionLaunch_LaunchTypeIsShell(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	if launchResp.Code != http.StatusOK {
		t.Fatalf("expected launch 200, got %d", launchResp.Code)
	}
	payload := h.responseJSON(t, launchResp)
	if asString(payload["launch_type"]) != "shell" {
		t.Fatalf("expected launch_type=shell, got %s", asString(payload["launch_type"]))
	}
	launchObj, ok := payload["launch"].(map[string]any)
	if !ok {
		t.Fatalf("expected launch object, got %#v", payload["launch"])
	}
	if asString(launchObj["proxy_host"]) == "" {
		t.Fatalf("expected proxy_host in shell launch payload")
	}
	if asString(launchObj["token"]) == "" {
		t.Fatalf("expected token in shell launch payload")
	}
	if asString(launchObj["expires_at"]) == "" {
		t.Fatalf("expected expires_at in shell launch payload")
	}
}

func TestSessionLaunch_MissingAssetID(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	resp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": "",
		"action":   "shell",
	}, operatorCookie)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing asset_id, got %d", resp.Code)
	}
}

func TestSessionExport_Summary(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	summaryResp := h.requestJSON(http.MethodGet, "/sessions/"+sessionID+"/export/summary", nil, operatorCookie)
	if summaryResp.Code != http.StatusOK {
		t.Fatalf("expected summary export 200, got %d: %s", summaryResp.Code, summaryResp.Body.String())
	}
	contentDisposition := summaryResp.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisposition, "summary") {
		t.Fatalf("expected Content-Disposition with summary, got %q", contentDisposition)
	}
}

func TestSessionExport_Transcript(t *testing.T) {
	h := newTestHarness(t)

	operatorCookie := h.login(h.seed.operatorName, h.seed.operatorPass)

	launchResp := h.requestJSON(http.MethodPost, "/sessions/launch", map[string]any{
		"asset_id": h.seed.allowedAssetID,
		"action":   "shell",
	}, operatorCookie)
	launchPayload := h.responseJSON(t, launchResp)
	sessionID := asString(launchPayload["session_id"])

	transcriptResp := h.requestJSON(http.MethodGet, "/sessions/"+sessionID+"/export/transcript", nil, operatorCookie)
	if transcriptResp.Code != http.StatusOK {
		t.Fatalf("expected transcript export 200, got %d: %s", transcriptResp.Code, transcriptResp.Body.String())
	}
}
