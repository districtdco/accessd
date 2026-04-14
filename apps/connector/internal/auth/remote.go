package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type RemoteVerifier struct {
	verifyURL string
	client    *http.Client
	clientErr error
}

type RemoteVerifierOptions struct {
	CACertFile         string
	InsecureSkipVerify bool
}

func NewRemoteVerifier(verifyURL string, timeout time.Duration, opts RemoteVerifierOptions) *RemoteVerifier {
	u := strings.TrimSpace(verifyURL)
	if u == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client, err := newRemoteHTTPClient(timeout, opts)
	return &RemoteVerifier{
		verifyURL: u,
		client:    client,
		clientErr: err,
	}
}

func (v *RemoteVerifier) Verify(token string) (ConnectorClaims, error) {
	if v.clientErr != nil {
		return ConnectorClaims{}, fmt.Errorf("configure verify client: %w", v.clientErr)
	}
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

func newRemoteHTTPClient(timeout time.Duration, opts RemoteVerifierOptions) (*http.Client, error) {
	caCertFile := strings.TrimSpace(opts.CACertFile)
	insecure := opts.InsecureSkipVerify
	if caCertFile == "" && !insecure {
		return &http.Client{Timeout: timeout}, nil
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure, //nolint:gosec // explicit emergency operator override
	}
	if caCertFile != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		certBytes, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("read backend ca cert file %q: %w", caCertFile, err)
		}
		if ok := pool.AppendCertsFromPEM(certBytes); !ok {
			cert, parseErr := x509.ParseCertificate(certBytes)
			if parseErr != nil {
				return nil, fmt.Errorf("parse backend ca cert file %q: %w", caCertFile, parseErr)
			}
			pool.AddCert(cert)
		}
		tlsConfig.RootCAs = pool
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}
