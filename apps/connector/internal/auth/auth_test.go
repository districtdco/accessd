package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func signTestToken(secret string, claims ConnectorClaims) string {
	body, _ := json.Marshal(claims)
	bodyPart := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(bodyPart))
	sig := mac.Sum(nil)
	sigPart := base64.RawURLEncoding.EncodeToString(sig)
	return bodyPart + "." + sigPart
}

func TestVerifier_ValidToken(t *testing.T) {
	secret := "test-secret-key"
	v := NewVerifier(secret)
	if v == nil {
		t.Fatal("expected non-nil verifier")
	}

	claims := ConnectorClaims{
		SessionID: "sess-123",
		UserID:    "user-456",
		AssetID:   "asset-789",
		Action:    "shell",
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Version:   "v1",
	}
	token := signTestToken(secret, claims)

	result, err := v.Verify(token)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if result.SessionID != "sess-123" {
		t.Fatalf("expected session_id sess-123, got %s", result.SessionID)
	}
	if result.Action != "shell" {
		t.Fatalf("expected action shell, got %s", result.Action)
	}
}

func TestVerifier_InvalidSignature(t *testing.T) {
	v := NewVerifier("correct-secret")

	claims := ConnectorClaims{
		SessionID: "sess-123",
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Version:   "v1",
	}
	token := signTestToken("wrong-secret", claims)

	_, err := v.Verify(token)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestVerifier_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	v := NewVerifier(secret)

	claims := ConnectorClaims{
		SessionID: "sess-123",
		ExpiresAt: time.Now().Add(-5 * time.Minute).Unix(),
		Version:   "v1",
	}
	token := signTestToken(secret, claims)

	_, err := v.Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifier_MalformedToken(t *testing.T) {
	v := NewVerifier("secret")

	_, err := v.Verify("not-a-valid-token")
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestVerifier_EmptySecret(t *testing.T) {
	v := NewVerifier("")
	if v != nil {
		t.Fatal("expected nil verifier for empty secret")
	}
}
