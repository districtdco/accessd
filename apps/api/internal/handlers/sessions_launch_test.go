package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/districtdco/accessd/api/internal/sessions"
)

func TestBuildLaunchResponse_IncludesConnectorTokenAcrossLaunchTypes(t *testing.T) {
	expiresAt := time.Date(2026, time.April, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		result sessions.LaunchResult
	}{
		{
			name: "shell",
			result: sessions.LaunchResult{
				SessionID:      "session-shell",
				LaunchType:     "shell",
				ConnectorToken: "connector-shell",
				ExpiresAt:      expiresAt,
				Shell: &sessions.ShellLaunchPayload{
					ProxyHost:        "127.0.0.1",
					ProxyPort:        2222,
					ProxyUsername:    "pam",
					UpstreamUsername: "appuser",
					TargetAssetName:  "linux-app-01",
					TargetHost:       "10.10.10.10",
					Token:            "launch-token",
				},
			},
		},
		{
			name: "dbeaver",
			result: sessions.LaunchResult{
				SessionID:      "session-dbeaver",
				LaunchType:     "dbeaver",
				ConnectorToken: "connector-dbeaver",
				ExpiresAt:      expiresAt,
				DBeaver: &sessions.DBeaverLaunchPayload{
					Engine:   "postgres",
					Host:     "127.0.0.1",
					Port:     5432,
					Database: "app",
					Username: "app_user",
					SSLMode:  "disable",
				},
			},
		},
		{
			name: "sftp",
			result: sessions.LaunchResult{
				SessionID:      "session-sftp",
				LaunchType:     "sftp",
				ConnectorToken: "connector-sftp",
				ExpiresAt:      expiresAt,
				SFTP: &sessions.SFTPLaunchPayload{
					Host:             "127.0.0.1",
					Port:             2222,
					ProxyUsername:    "pam",
					UpstreamUsername: "appuser",
					TargetAssetName:  "linux-app-01",
					TargetHost:       "10.10.10.10",
					Password:         "launch-token",
					Path:             "/home/pam",
				},
			},
		},
		{
			name: "redis",
			result: sessions.LaunchResult{
				SessionID:      "session-redis",
				LaunchType:     "redis",
				ConnectorToken: "connector-redis",
				ExpiresAt:      expiresAt,
				Redis: &sessions.RedisLaunchPayload{
					Host:                  "127.0.0.1",
					Port:                  6379,
					Username:              "default",
					Password:              "launch-token",
					Database:              0,
					UseTLS:                false,
					InsecureSkipVerifyTLS: false,
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			resp, err := buildLaunchResponse(tt.result)
			if err != nil {
				t.Fatalf("buildLaunchResponse returned error: %v", err)
			}
			if resp.ConnectorToken != tt.result.ConnectorToken {
				t.Fatalf("expected connector token %q, got %q", tt.result.ConnectorToken, resp.ConnectorToken)
			}
			if resp.SessionID != tt.result.SessionID {
				t.Fatalf("expected session id %q, got %q", tt.result.SessionID, resp.SessionID)
			}
			if resp.LaunchType != tt.result.LaunchType {
				t.Fatalf("expected launch type %q, got %q", tt.result.LaunchType, resp.LaunchType)
			}
		})
	}
}

func TestParseDBMetadata_MSSQLSSLModeNormalization(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantSSL string
	}{
		{
			name:    "empty mode defaults to disable",
			raw:     `{"engine":"mssql"}`,
			wantSSL: "disable",
		},
		{
			name:    "require coerced to disable",
			raw:     `{"engine":"mssql","ssl_mode":"require"}`,
			wantSSL: "disable",
		},
		{
			name:    "verify full coerced to disable",
			raw:     `{"engine":"sqlserver","ssl_mode":"verify-full"}`,
			wantSSL: "disable",
		},
		{
			name:    "allow preserved",
			raw:     `{"engine":"mssql","ssl_mode":"allow"}`,
			wantSSL: "allow",
		},
		{
			name:    "mongodb normalized to mongo",
			raw:     `{"engine":"mongodb","ssl_mode":"disable"}`,
			wantSSL: "disable",
		},
		{
			name:    "mysql mixed-case normalized to mysql",
			raw:     `{"engine":"MySQL","ssl_mode":"prefer"}`,
			wantSSL: "prefer",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			meta, err := parseDBMetadata(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("parseDBMetadata returned error: %v", err)
			}
			if got := meta.SSLMode; got != tt.wantSSL {
				t.Fatalf("ssl mode mismatch: got %q want %q", got, tt.wantSSL)
			}
			if tt.name == "mongodb normalized to mongo" {
				if got := meta.Engine; got != "mongo" {
					t.Fatalf("engine mismatch: got %q want mongo", got)
				}
				return
			}
			if tt.name == "mysql mixed-case normalized to mysql" {
				if got := meta.Engine; got != "mysql" {
					t.Fatalf("engine mismatch: got %q want mysql", got)
				}
				return
			}
			if got := meta.Engine; got != "mssql" {
				t.Fatalf("engine mismatch: got %q want mssql", got)
			}
		})
	}
}

func TestDBeaverClientPassword(t *testing.T) {
	tests := []struct {
		name           string
		engine         string
		connectorToken string
		sessionID      string
		want           string
	}{
		{
			name:           "postgres prefers connector token",
			engine:         "postgres",
			connectorToken: "connector-token",
			sessionID:      "session-1",
			want:           "connector-token",
		},
		{
			name:           "postgres falls back to session id",
			engine:         "postgres",
			connectorToken: "",
			sessionID:      "session-2",
			want:           "session-2",
		},
		{
			name:           "any engine uses session id when token too long",
			engine:         "mssql",
			connectorToken: "this-is-a-very-long-connector-token-that-should-not-be-used-as-an-mssql-password-because-password-limits-can-be-strict-in-some-client-drivers-12345678901234567890",
			sessionID:      "141950e0-7908-4cf9-b9e1-5d8cbd8a9e59",
			want:           "141950e0-7908-4cf9-b9e1-5d8cbd8a9e59",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := dbeaverClientPassword(tt.engine, tt.connectorToken, tt.sessionID)
			if got != tt.want {
				t.Fatalf("password mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}
