// Package auth provides connector-side verification of backend-signed launch tokens.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConnectorClaims are the claims embedded in a connector launch token signed by the API backend.
type ConnectorClaims struct {
	SessionID string `json:"sid"`
	UserID    string `json:"uid"`
	AssetID   string `json:"aid"`
	Action    string `json:"act"`
	ExpiresAt int64  `json:"exp"`
	Version   string `json:"v"`
}

// Verifier validates HMAC-signed connector tokens from the backend.
type Verifier struct {
	secret []byte
}

// NewVerifier creates a connector token verifier. Returns nil if secret is empty (verification disabled).
func NewVerifier(secret string) *Verifier {
	s := strings.TrimSpace(secret)
	if s == "" {
		return nil
	}
	key := make([]byte, len(s))
	copy(key, []byte(s))
	return &Verifier{secret: key}
}

// Verify checks the token signature, expiry, and returns the claims.
func (v *Verifier) Verify(token string) (ConnectorClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return ConnectorClaims{}, fmt.Errorf("malformed connector token")
	}
	bodyPart := parts[0]
	sigPart := parts[1]

	providedSig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return ConnectorClaims{}, fmt.Errorf("malformed connector token signature")
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(bodyPart))
	expectedSig := mac.Sum(nil)

	if subtle.ConstantTimeCompare(providedSig, expectedSig) != 1 {
		return ConnectorClaims{}, fmt.Errorf("invalid connector token signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return ConnectorClaims{}, fmt.Errorf("malformed connector token payload")
	}

	var claims ConnectorClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return ConnectorClaims{}, fmt.Errorf("invalid connector token payload")
	}

	if time.Now().Unix() >= claims.ExpiresAt {
		return ConnectorClaims{}, fmt.Errorf("connector token expired")
	}

	return claims, nil
}
