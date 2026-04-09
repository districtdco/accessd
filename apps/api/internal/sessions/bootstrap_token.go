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

const connectorBootstrapTokenVersion = "v1"

type ConnectorBootstrapClaims struct {
	Origin        string `json:"origin"`
	BackendVerify string `json:"backend_verify_url"`
	IssuedForUser string `json:"issued_for_user,omitempty"`
	ExpiresAt     int64  `json:"exp"`
	Version       string `json:"v"`
}

type ConnectorBootstrapSigner struct {
	secret []byte
}

func NewConnectorBootstrapSigner(secret []byte) *ConnectorBootstrapSigner {
	dup := make([]byte, len(secret))
	copy(dup, secret)
	return &ConnectorBootstrapSigner{secret: dup}
}

func (s *ConnectorBootstrapSigner) Sign(claims ConnectorBootstrapClaims) (string, error) {
	if strings.TrimSpace(claims.Origin) == "" || strings.TrimSpace(claims.BackendVerify) == "" || claims.ExpiresAt <= 0 {
		return "", fmt.Errorf("invalid bootstrap claims")
	}
	claims.Version = connectorBootstrapTokenVersion
	body, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal bootstrap claims: %w", err)
	}
	bodyPart := base64.RawURLEncoding.EncodeToString(body)
	signaturePart := base64.RawURLEncoding.EncodeToString(s.sign(bodyPart))
	return bodyPart + "." + signaturePart, nil
}

func (s *ConnectorBootstrapSigner) Verify(token string) (ConnectorBootstrapClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return ConnectorBootstrapClaims{}, fmt.Errorf("malformed bootstrap token")
	}
	bodyPart := parts[0]
	sigPart := parts[1]
	providedSig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return ConnectorBootstrapClaims{}, fmt.Errorf("malformed bootstrap token signature")
	}
	expectedSig := s.sign(bodyPart)
	if subtle.ConstantTimeCompare(providedSig, expectedSig) != 1 {
		return ConnectorBootstrapClaims{}, fmt.Errorf("invalid bootstrap token signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return ConnectorBootstrapClaims{}, fmt.Errorf("malformed bootstrap token payload")
	}
	var claims ConnectorBootstrapClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return ConnectorBootstrapClaims{}, fmt.Errorf("invalid bootstrap token payload")
	}
	if claims.Version != connectorBootstrapTokenVersion {
		return ConnectorBootstrapClaims{}, fmt.Errorf("unsupported bootstrap token version")
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return ConnectorBootstrapClaims{}, ErrLaunchExpired
	}
	if strings.TrimSpace(claims.Origin) == "" || strings.TrimSpace(claims.BackendVerify) == "" {
		return ConnectorBootstrapClaims{}, fmt.Errorf("bootstrap token missing required fields")
	}
	return claims, nil
}

func (s *ConnectorBootstrapSigner) sign(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
