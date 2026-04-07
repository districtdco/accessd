package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/districtd/pam/connector/internal/auth"
)

func signConnectorTestToken(t *testing.T, secret string, claims auth.ConnectorClaims) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	bodyPart := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(bodyPart))
	sigPart := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bodyPart + "." + sigPart
}

func TestVerifyConnectorToken_RequiresTokenWhenVerifierEnabled(t *testing.T) {
	verifier := auth.NewVerifier("connector-secret")
	err := verifyConnectorToken(verifier, "", "session-1")
	if err == nil {
		t.Fatalf("expected missing token error")
	}
	if err.Error() != "missing connector_token" {
		t.Fatalf("expected missing connector_token error, got %q", err.Error())
	}
}

func TestVerifyConnectorToken_AcceptsValidTokenAndRejectsSessionMismatch(t *testing.T) {
	secret := "connector-secret"
	verifier := auth.NewVerifier(secret)
	token := signConnectorTestToken(t, secret, auth.ConnectorClaims{
		SessionID: "session-1",
		UserID:    "user-1",
		AssetID:   "asset-1",
		Action:    "shell",
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Version:   "v1",
	})

	if err := verifyConnectorToken(verifier, token, "session-1"); err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if err := verifyConnectorToken(verifier, token, "session-2"); err == nil {
		t.Fatalf("expected session mismatch error")
	}
}

func TestVerifyConnectorToken_SkipsValidationWhenVerifierDisabled(t *testing.T) {
	if err := verifyConnectorToken(nil, "", "session-1"); err != nil {
		t.Fatalf("expected nil error when verifier is disabled, got %v", err)
	}
}
