package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeCommandArg_DBeaverPassword(t *testing.T) {
	arg := "driver=sqlserver|host=db.local|user=sa|password=supersecret|database=master"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "password=<redacted>") {
		t.Fatalf("expected redacted password marker, got %q", got)
	}
}

func TestSanitizeCommandArg_SFTPURLPassword(t *testing.T) {
	arg := "sftp://alice:ultrasecret@example.com:22/home/alice"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "ultrasecret") {
		t.Fatalf("expected sftp password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "alice:<redacted>@") {
		t.Fatalf("expected redacted sftp password marker, got %q", got)
	}
}

func TestResolveBinaryPath_OverrideMissing(t *testing.T) {
	_, fromOverride, err := resolveBinaryPath("/tmp/does-not-exist-pam-launcher", nil)
	if err == nil {
		t.Fatalf("expected error for missing override path")
	}
	if !fromOverride {
		t.Fatalf("expected fromOverride=true for explicit override path")
	}
}

func TestResolveBinaryPath_FallbackPath(t *testing.T) {
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "pam-test-bin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}

	got, fromOverride, err := resolveBinaryPath("", []string{"/tmp/definitely-missing", binPath})
	if err != nil {
		t.Fatalf("expected fallback resolution to succeed: %v", err)
	}
	if fromOverride {
		t.Fatalf("expected fromOverride=false when using fallback search")
	}
	if got != binPath {
		t.Fatalf("expected resolved path %q, got %q", binPath, got)
	}
}

func TestPrepareLaunchTokenFile(t *testing.T) {
	tokenPath, err := prepareLaunchTokenFile("super-secret-token")
	if err != nil {
		t.Fatalf("prepare token file: %v", err)
	}
	defer os.Remove(tokenPath)

	tokenBlob, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if !strings.Contains(string(tokenBlob), "super-secret-token") {
		t.Fatalf("token file should contain token")
	}

}

func TestShellCommand_DoesNotLeakToken(t *testing.T) {
	req := Request{
		SessionID: "session-1",
		AssetName: "db1",
		Launch: Shell{
			ProxyHost: "127.0.0.1",
			ProxyPort: 2222,
			Username:  "pam",
			Token:     "very-secret-launch-token",
		},
	}
	command, err := shellCommand(req)
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	if strings.Contains(command, req.Launch.Token) {
		t.Fatalf("shell command must not contain raw token")
	}
	if !strings.Contains(command, "bridge-shell") {
		t.Fatalf("shell command should invoke bridge-shell mode")
	}
	if !strings.Contains(command, "--token-file") {
		t.Fatalf("shell command should pass token-file path")
	}
}

func TestSanitizeCommandArgs_PuttyPasswordRedacted(t *testing.T) {
	args := []string{"putty", "-ssh", "pam@127.0.0.1", "-P", "2222", "-pw", "supersecret"}
	got := sanitizeCommandArgs(args)
	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected putty password to be redacted, got %q", got)
	}
}

func TestUpsertFileZillaSite_EnforcesSingleConnection(t *testing.T) {
	tempHome := t.TempDir()
	prevHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
	})

	siteRef, err := upsertFileZillaSite("pam local", "sftp://pam:launch-token@127.0.0.1:2222/home/pam")
	if err != nil {
		t.Fatalf("upsertFileZillaSite: %v", err)
	}
	if siteRef != "0/pam local" {
		t.Fatalf("siteRef = %q, want %q", siteRef, "0/pam local")
	}

	sitePath, err := resolveFileZillaSiteManagerPath()
	if err != nil {
		t.Fatalf("resolveFileZillaSiteManagerPath: %v", err)
	}
	doc, err := readFileZillaSiteFile(sitePath)
	if err != nil {
		t.Fatalf("readFileZillaSiteFile: %v", err)
	}
	if len(doc.Servers.Server) != 1 {
		t.Fatalf("expected 1 server entry, got %d", len(doc.Servers.Server))
	}
	if got := doc.Servers.Server[0].MaximumMultipleConnections; got != 1 {
		t.Fatalf("MaximumMultipleConnections = %d, want 1", got)
	}
}

func TestDBeaverConnectionSpec_AppendsSessionSuffixToName(t *testing.T) {
	req := DBeaverRequest{
		SessionID: "13392c6b-9627-4544-b542-ad5eb9f2d883",
		AssetName: "accessd-local-postgres",
		Launch: DBeaverPayload{
			Engine:           "postgres",
			Host:             "127.0.0.1",
			Port:             57533,
			Username:         "accessd",
			UpstreamUsername: "app_user",
			TargetAssetName:  "accessd-local-postgres",
			TargetHost:       "10.0.0.15",
		},
	}

	spec := dbeaverConnectionSpec(req)
	if !strings.Contains(spec, "name=accessd-local-postgres - app_user (10.0.0.15) [13392c6b]") {
		t.Fatalf("expected session suffix in dbeaver connection name, got %q", spec)
	}
}

func TestDBeaverConnectionSpecWithNameSuffix_AppendsRepairSuffix(t *testing.T) {
	req := DBeaverRequest{
		SessionID: "13392c6b-9627-4544-b542-ad5eb9f2d883",
		AssetName: "accessd-local-postgres",
		Launch: DBeaverPayload{
			Engine:           "postgres",
			Host:             "127.0.0.1",
			Port:             57533,
			Username:         "accessd",
			UpstreamUsername: "app_user",
			TargetAssetName:  "accessd-local-postgres",
			TargetHost:       "10.0.0.15",
		},
	}

	spec := dbeaverConnectionSpecWithNameSuffix(req, "repair-123")
	if !strings.Contains(spec, "name=accessd-local-postgres - app_user (10.0.0.15) [13392c6b] {repair-123}") {
		t.Fatalf("expected repair suffix in dbeaver connection name, got %q", spec)
	}
}

func TestDBeaverConnectionSpec_SQLServerSetsProxySafeJDBCProps(t *testing.T) {
	req := DBeaverRequest{
		SessionID: "cf79f0d8-0e0c-4e8f-9305-590f2f6c4b6f",
		AssetName: "accessd-local-mssql",
		Launch: DBeaverPayload{
			Engine:   "mssql",
			Host:     "127.0.0.1",
			Port:     34017,
			Database: "master",
			Username: "sa",
			SSLMode:  "require",
		},
	}

	spec := dbeaverConnectionSpec(req)
	for _, want := range []string{
		"driver=microsoft",
		"prop.encrypt=false",
		"prop.trustServerCertificate=true",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("expected %q in sqlserver spec, got %q", want, spec)
		}
	}
	if strings.Contains(spec, "prop.authentication=") {
		t.Fatalf("did not expect authentication override in sqlserver spec, got %q", spec)
	}
	if strings.Contains(spec, "sslMode=") {
		t.Fatalf("did not expect generic sslMode in sqlserver spec, got %q", spec)
	}
}

func TestRedisCLICommandPreview_AutoInsecureFromEnv(t *testing.T) {
	prev := os.Getenv("ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE")
	if err := os.Setenv("ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE", "true"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE", prev)
	})

	req := RedisRequest{
		Launch: RedisPayload{
			Host:      "127.0.0.1",
			Port:      6379,
			TLS:       true,
			Database:  0,
			Password:  "token",
			Username:  "default",
			ExpiresAt: "2099-01-01T00:00:00Z",
		},
	}

	preview := redisCLICommandPreview(req)
	if !strings.Contains(preview, "--tls") || !strings.Contains(preview, "--insecure") {
		t.Fatalf("expected redis preview to include --tls and --insecure, got %q", preview)
	}
}

func TestFileZillaSiteManagerCandidatePaths_WindowsPrefersAppData(t *testing.T) {
	home := `C:\Users\ankit`
	appData := `C:\Users\ankit\AppData\Roaming`
	paths := fileZillaSiteManagerCandidatePaths(home, appData, "windows")
	if len(paths) == 0 {
		t.Fatalf("expected candidate paths")
	}
	wantPrefix := filepath.Join(appData, "FileZilla", "sitemanager.xml")
	if paths[0] != wantPrefix {
		t.Fatalf("first windows path = %q, want %q", paths[0], wantPrefix)
	}
}

func TestFileZillaSiteManagerCandidatePaths_DarwinUsesLibraryPath(t *testing.T) {
	home := "/Users/ankit"
	paths := fileZillaSiteManagerCandidatePaths(home, "", "darwin")
	if len(paths) == 0 {
		t.Fatalf("expected candidate paths")
	}
	wantPrefix := filepath.Join(home, "Library", "Application Support", "FileZilla", "sitemanager.xml")
	if paths[0] != wantPrefix {
		t.Fatalf("first darwin path = %q, want %q", paths[0], wantPrefix)
	}
}
