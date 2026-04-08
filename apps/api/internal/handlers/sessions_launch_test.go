package handlers

import (
	"testing"
	"time"

	"github.com/districtd/pam/api/internal/sessions"
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
