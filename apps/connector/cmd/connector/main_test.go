package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/districtd/pam/connector/internal/auth"
)

type stubVerifier struct {
	claims auth.ConnectorClaims
	err    error
}

func (s stubVerifier) Verify(_ string) (auth.ConnectorClaims, error) {
	if s.err != nil {
		return auth.ConnectorClaims{}, s.err
	}
	return s.claims, nil
}

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
	tokenErr := &connectorTokenError{}
	if ok := errors.As(err, &tokenErr); !ok {
		t.Fatalf("expected connectorTokenError, got %T (%v)", err, err)
	}
	if tokenErr.code != "connector_token_missing" {
		t.Fatalf("expected connector_token_missing code, got %q", tokenErr.code)
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
	} else {
		tokenErr := &connectorTokenError{}
		if ok := errors.As(err, &tokenErr); !ok {
			t.Fatalf("expected connectorTokenError, got %T (%v)", err, err)
		}
		if tokenErr.code != "connector_token_session_mismatch" {
			t.Fatalf("expected connector_token_session_mismatch code, got %q", tokenErr.code)
		}
	}
}

func TestClassifyConnectorTokenVerifyError_InvalidSignature(t *testing.T) {
	err := classifyConnectorTokenVerifyError(fmt.Errorf("invalid connector token signature"))
	tokenErr := &connectorTokenError{}
	if ok := errors.As(err, &tokenErr); !ok {
		t.Fatalf("expected connectorTokenError, got %T (%v)", err, err)
	}
	if tokenErr.code != "connector_token_invalid" {
		t.Fatalf("expected connector_token_invalid code, got %q", tokenErr.code)
	}
}

func TestClassifyConnectorTokenVerifyError_Expired(t *testing.T) {
	err := classifyConnectorTokenVerifyError(fmt.Errorf("connector token expired"))
	tokenErr := &connectorTokenError{}
	if ok := errors.As(err, &tokenErr); !ok {
		t.Fatalf("expected connectorTokenError, got %T (%v)", err, err)
	}
	if tokenErr.code != "connector_token_expired" {
		t.Fatalf("expected connector_token_expired code, got %q", tokenErr.code)
	}
}

func TestVerifyConnectorToken_SkipsValidationWhenVerifierDisabled(t *testing.T) {
	if err := verifyConnectorToken(nil, "", "session-1"); err != nil {
		t.Fatalf("expected nil error when verifier is disabled, got %v", err)
	}
}

func TestFallbackConnectorTokenVerifier_FallsBackWhenPrimaryFails(t *testing.T) {
	verifier := fallbackConnectorTokenVerifier{
		verifiers: []connectorTokenVerifier{
			stubVerifier{err: fmt.Errorf("local signature mismatch")},
			stubVerifier{claims: auth.ConnectorClaims{SessionID: "session-1"}},
		},
	}

	claims, err := verifier.Verify("token")
	if err != nil {
		t.Fatalf("expected fallback verifier to succeed, got %v", err)
	}
	if claims.SessionID != "session-1" {
		t.Fatalf("expected fallback claims session_id=session-1, got %q", claims.SessionID)
	}
}

func TestFallbackConnectorTokenVerifier_ReportsAllVerifierFailures(t *testing.T) {
	verifier := fallbackConnectorTokenVerifier{
		verifiers: []connectorTokenVerifier{
			stubVerifier{err: fmt.Errorf("local invalid")},
			stubVerifier{err: fmt.Errorf("backend invalid")},
		},
	}

	_, err := verifier.Verify("token")
	if err == nil {
		t.Fatalf("expected fallback verifier failure")
	}
	if got := err.Error(); got == "" || got == "connector token verification failed: " {
		t.Fatalf("expected non-empty aggregated error, got %q", got)
	}
}

func TestLaunchTracker_DeduplicatesInFlightAndCompleted(t *testing.T) {
	tracker := newLaunchTracker(5 * time.Minute)
	key := "shell:session-1"
	if !tracker.TryStart(key) {
		t.Fatalf("expected first TryStart to succeed")
	}
	if tracker.TryStart(key) {
		t.Fatalf("expected duplicate TryStart to be rejected while in flight")
	}
	tracker.FinishSuccess(key)
	if tracker.TryStart(key) {
		t.Fatalf("expected duplicate TryStart to be rejected after success")
	}
}

func TestLaunchTracker_AllowsRetryAfterFailure(t *testing.T) {
	tracker := newLaunchTracker(5 * time.Minute)
	key := "shell:session-2"
	if !tracker.TryStart(key) {
		t.Fatalf("expected first TryStart to succeed")
	}
	tracker.FinishFailure(key)
	if !tracker.TryStart(key) {
		t.Fatalf("expected TryStart to succeed after failure cleanup")
	}
}
