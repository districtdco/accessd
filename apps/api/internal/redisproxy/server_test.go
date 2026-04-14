package redisproxy

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/districtdco/accessd/api/internal/assets"
	"github.com/districtdco/accessd/api/internal/sessions"
)

func TestReadRESPCommandCapturesCommandAndArgs(t *testing.T) {
	raw := buildRESPCommand([]string{"SET", "user:1", "super-secret", "EX", "60"})
	frame, cmd, args, err := readRESPCommand(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("readRESPCommand: %v", err)
	}
	if string(frame) != string(raw) {
		t.Fatalf("raw frame mismatch")
	}
	if cmd != "SET" {
		t.Fatalf("cmd = %q, want SET", cmd)
	}
	if len(args) != 4 {
		t.Fatalf("args len = %d, want 4", len(args))
	}
	if args[0] != "user:1" || args[1] != "super-secret" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestDangerousCommandSet(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{cmd: "FLUSHALL", want: true},
		{cmd: "DEL", want: true},
		{cmd: "CONFIG", want: true},
		{cmd: "KEYS", want: true},
		{cmd: "SET", want: true},
		{cmd: "EXPIRE", want: true},
		{cmd: "EVAL", want: true},
		{cmd: "GET", want: false},
	}
	for _, tc := range cases {
		if got := isDangerousCommand(tc.cmd); got != tc.want {
			t.Fatalf("isDangerousCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestSummarizeArgsRedactsSensitiveValues(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want []string
	}{
		{
			name: "auth",
			cmd:  "AUTH",
			args: []string{"default", "top-secret"},
			want: []string{"<redacted>", "<redacted>"},
		},
		{
			name: "set",
			cmd:  "SET",
			args: []string{"user:1", "token-value"},
			want: []string{"user:1", "<redacted_value>"},
		},
		{
			name: "mset",
			cmd:  "MSET",
			args: []string{"k1", "v1", "k2", "v2"},
			want: []string{"k1", "<redacted_value>", "k2", "<redacted_value>"},
		},
		{
			name: "eval",
			cmd:  "EVAL",
			args: []string{"return redis.call('get','k')", "1", "k"},
			want: []string{"<redacted_script>", "1", "k"},
		},
	}

	for _, tc := range tests {
		got := summarizeArgs(tc.cmd, tc.args, 256)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: len(got)=%d len(want)=%d", tc.name, len(got), len(tc.want))
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: arg[%d]=%q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

func TestLaunchContextMatchesRedisSession(t *testing.T) {
	reg := SessionRegistration{SessionID: "s1", UserID: "u1", AssetID: "a1"}

	valid := sessions.LaunchContext{
		SessionID: "s1",
		UserID:    "u1",
		AssetID:   "a1",
		Action:    "redis",
		Protocol:  sessions.ProtocolRedis,
		AssetType: assets.TypeRedis,
	}
	if !launchContextMatchesRedisSession(valid, reg) {
		t.Fatalf("expected valid context to match")
	}

	invalid := []sessions.LaunchContext{
		{SessionID: "s2", UserID: "u1", AssetID: "a1", Action: "redis", Protocol: sessions.ProtocolRedis, AssetType: assets.TypeRedis},
		{SessionID: "s1", UserID: "u2", AssetID: "a1", Action: "redis", Protocol: sessions.ProtocolRedis, AssetType: assets.TypeRedis},
		{SessionID: "s1", UserID: "u1", AssetID: "a2", Action: "redis", Protocol: sessions.ProtocolRedis, AssetType: assets.TypeRedis},
		{SessionID: "s1", UserID: "u1", AssetID: "a1", Action: "shell", Protocol: sessions.ProtocolRedis, AssetType: assets.TypeRedis},
		{SessionID: "s1", UserID: "u1", AssetID: "a1", Action: "redis", Protocol: sessions.ProtocolSSH, AssetType: assets.TypeRedis},
		{SessionID: "s1", UserID: "u1", AssetID: "a1", Action: "redis", Protocol: sessions.ProtocolRedis, AssetType: assets.TypeLinuxVM},
	}
	for i, lctx := range invalid {
		if launchContextMatchesRedisSession(lctx, reg) {
			t.Fatalf("invalid case %d unexpectedly matched", i)
		}
	}
}
