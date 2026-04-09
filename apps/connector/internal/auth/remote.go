package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type RemoteVerifier struct {
	verifyURL string
	client    *http.Client
}

func NewRemoteVerifier(verifyURL string, timeout time.Duration) *RemoteVerifier {
	u := strings.TrimSpace(verifyURL)
	if u == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &RemoteVerifier{
		verifyURL: u,
		client:    &http.Client{Timeout: timeout},
	}
}

func (v *RemoteVerifier) Verify(token string) (ConnectorClaims, error) {
	payload, err := json.Marshal(map[string]string{
		"connector_token": strings.TrimSpace(token),
	})
	if err != nil {
		return ConnectorClaims{}, fmt.Errorf("marshal verify payload: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, v.verifyURL, bytes.NewReader(payload))
	if err != nil {
		return ConnectorClaims{}, fmt.Errorf("create verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return ConnectorClaims{}, fmt.Errorf("verify request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ConnectorClaims{}, fmt.Errorf("connector token rejected by backend")
	}

	var decoded struct {
		Valid  bool            `json:"valid"`
		Claims ConnectorClaims `json:"claims"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ConnectorClaims{}, fmt.Errorf("decode verify response: %w", err)
	}
	if !decoded.Valid {
		return ConnectorClaims{}, fmt.Errorf("connector token invalid")
	}
	return decoded.Claims, nil
}

