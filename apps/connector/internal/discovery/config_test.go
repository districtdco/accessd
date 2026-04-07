package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromFile_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `# PAM Connector config
apps:
  dbeaver: "/Applications/DBeaver.app"
  filezilla: "/usr/bin/filezilla"
  redis_cli: "/usr/local/bin/redis-cli"

terminal:
  macos: "iterm"
  linux: "gnome-terminal"
  windows: "wt"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfigFromFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	// Check apps
	if got := cfg.Apps["dbeaver"]; got != "/Applications/DBeaver.app" {
		t.Errorf("apps.dbeaver: expected /Applications/DBeaver.app, got %q", got)
	}
	if got := cfg.Apps["filezilla"]; got != "/usr/bin/filezilla" {
		t.Errorf("apps.filezilla: expected /usr/bin/filezilla, got %q", got)
	}
	if got := cfg.Apps["redis_cli"]; got != "/usr/local/bin/redis-cli" {
		t.Errorf("apps.redis_cli: expected /usr/local/bin/redis-cli, got %q", got)
	}

	// Check terminal prefs
	if cfg.Terminal.MacOS != "iterm" {
		t.Errorf("terminal.macos: expected iterm, got %q", cfg.Terminal.MacOS)
	}
	if cfg.Terminal.Linux != "gnome-terminal" {
		t.Errorf("terminal.linux: expected gnome-terminal, got %q", cfg.Terminal.Linux)
	}
	if cfg.Terminal.Windows != "wt" {
		t.Errorf("terminal.windows: expected wt, got %q", cfg.Terminal.Windows)
	}

	if cfg.loadedFrom != cfgPath {
		t.Errorf("loadedFrom: expected %q, got %q", cfgPath, cfg.loadedFrom)
	}
}

func TestLoadConfigFromFile_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := "apps:\n  dbeaver: '/Applications/DBeaver.app'\n  putty: \"C:\\\\Program Files\\\\PuTTY\\\\putty.exe\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfigFromFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Apps["dbeaver"]; got != "/Applications/DBeaver.app" {
		t.Errorf("expected unquoted path, got %q", got)
	}
	// The YAML-like parser preserves backslashes as-is from the file content
	if got := cfg.Apps["putty"]; got != `C:\\Program Files\\PuTTY\\putty.exe` {
		t.Errorf("expected windows path with backslashes, got %q", got)
	}
}

func TestLoadConfigFromFile_MissingFile(t *testing.T) {
	cfg, err := loadConfigFromFile("/tmp/does-not-exist-pam-config-test.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if cfg != nil {
		t.Fatal("expected nil config for missing file")
	}
}

func TestLoadConfigFromFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfigFromFile(cfgPath)
	if err != nil {
		t.Fatalf("expected no error for empty file: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config even for empty file")
	}
}

func TestLoadConfigFromFile_CommentsOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `# This is a comment
# Another comment
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfigFromFile(cfgPath)
	if err != nil {
		t.Fatalf("expected no error for comments-only file: %v", err)
	}
	if len(cfg.Apps) != 0 {
		t.Errorf("expected empty apps map, got %v", cfg.Apps)
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `apps:
  dbeaver: "/custom/dbeaver"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("PAM_CONNECTOR_CONFIG_FILE", cfgPath)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if got := cfg.Apps["dbeaver"]; got != "/custom/dbeaver" {
		t.Errorf("expected /custom/dbeaver, got %q", got)
	}
}

func TestLoadConfig_EnvOverrideInvalidPath(t *testing.T) {
	t.Setenv("PAM_CONNECTOR_CONFIG_FILE", "/tmp/does-not-exist-pam-cfg-test.yaml")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when PAM_CONNECTOR_CONFIG_FILE points to missing file")
	}
}

func TestLoadConfig_NoFileReturnsNil(t *testing.T) {
	t.Setenv("PAM_CONNECTOR_CONFIG_FILE", "")
	// Use a non-existent home dir to prevent default file from being found
	t.Setenv("HOME", "/tmp/does-not-exist-pam-home")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error when no config exists: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when no file exists")
	}
}

func TestConnectorConfig_AppPath(t *testing.T) {
	cfg := &ConnectorConfig{
		Apps: map[string]string{
			"dbeaver":   "/path/to/dbeaver",
			"redis_cli": "/path/to/redis-cli",
		},
	}
	if got := cfg.appPath(AppDBeaver); got != "/path/to/dbeaver" {
		t.Errorf("expected /path/to/dbeaver, got %q", got)
	}
	if got := cfg.appPath(AppRedisCLI); got != "/path/to/redis-cli" {
		t.Errorf("expected /path/to/redis-cli, got %q", got)
	}
	if got := cfg.appPath(AppPuTTY); got != "" {
		t.Errorf("expected empty for unconfigured app, got %q", got)
	}
}

func TestConnectorConfig_NilSafe(t *testing.T) {
	var cfg *ConnectorConfig
	if got := cfg.appPath(AppDBeaver); got != "" {
		t.Errorf("expected empty from nil config, got %q", got)
	}
	if got := cfg.terminalPref("darwin"); got != "" {
		t.Errorf("expected empty from nil config, got %q", got)
	}
	if got := cfg.LoadedFrom(); got != "" {
		t.Errorf("expected empty from nil config, got %q", got)
	}
}

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`"`, `"`},
		{``, ``},
	}
	for _, tt := range tests {
		got := stripQuotes(tt.input)
		if got != tt.want {
			t.Errorf("stripQuotes(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
