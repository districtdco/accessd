package sessions

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

const tokenVersion = "v1"

type LaunchTokenClaims struct {
	SessionID string `json:"sid"`
	UserID    string `json:"uid"`
	AssetID   string `json:"aid"`
	Action    string `json:"act"`
	RequestID string `json:"rid,omitempty"`
	ExpiresAt int64  `json:"exp"`
	Version   string `json:"v"`
}

type TokenSigner struct {
	secret []byte
}

func NewTokenSigner(secret []byte) *TokenSigner {
	dup := make([]byte, len(secret))
	copy(dup, secret)
	return &TokenSigner{secret: dup}
}

func (s *TokenSigner) Sign(claims LaunchTokenClaims) (string, error) {
	if strings.TrimSpace(claims.SessionID) == "" ||
		strings.TrimSpace(claims.UserID) == "" ||
		strings.TrimSpace(claims.AssetID) == "" ||
		strings.TrimSpace(claims.Action) == "" ||
		claims.ExpiresAt <= 0 {
		return "", fmt.Errorf("invalid launch claims")
	}
	claims.Version = tokenVersion
	body, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal launch claims: %w", err)
	}
	bodyPart := base64.RawURLEncoding.EncodeToString(body)
	signaturePart := base64.RawURLEncoding.EncodeToString(s.sign(bodyPart))
	return bodyPart + "." + signaturePart, nil
}

func (s *TokenSigner) Verify(token string) (LaunchTokenClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return LaunchTokenClaims{}, fmt.Errorf("malformed token")
	}
	bodyPart := parts[0]
	sigPart := parts[1]

	providedSig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return LaunchTokenClaims{}, fmt.Errorf("malformed token signature")
	}

	expectedSig := s.sign(bodyPart)
	if subtle.ConstantTimeCompare(providedSig, expectedSig) != 1 {
		return LaunchTokenClaims{}, fmt.Errorf("invalid token signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return LaunchTokenClaims{}, fmt.Errorf("malformed token payload")
	}

	var claims LaunchTokenClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return LaunchTokenClaims{}, fmt.Errorf("invalid token payload")
	}
	if claims.Version != tokenVersion {
		return LaunchTokenClaims{}, fmt.Errorf("unsupported token version")
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return LaunchTokenClaims{}, ErrLaunchExpired
	}
	return claims, nil
}

func (s *TokenSigner) sign(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
