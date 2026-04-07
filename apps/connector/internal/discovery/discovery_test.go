package discovery

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateBinary_EmptyPath(t *testing.T) {
	_, err := validateBinary("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateBinary_AbsolutePathMissing(t *testing.T) {
	_, err := validateBinary("/tmp/does-not-exist-pam-discovery-test")
	if err == nil {
		t.Fatal("expected error for missing absolute path")
	}
}

func TestValidateBinary_AbsolutePathExists(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "test-bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp binary: %v", err)
	}
	got, err := validateBinary(bin)
	if err != nil {
		t.Fatalf("expected success for valid binary: %v", err)
	}
	if got != bin {
		t.Fatalf("expected %q, got %q", bin, got)
	}
}

func TestValidateBinary_Directory(t *testing.T) {
	dir := t.TempDir()
	_, err := validateBinary(dir)
	if err == nil {
		t.Fatal("expected error for directory path")
	}
}

func TestValidateBinary_BareName(t *testing.T) {
	// "ls" should be findable on any test system
	if runtime.GOOS == "windows" {
		t.Skip("skipping bare name test on windows")
	}
	got, err := validateBinary("ls")
	if err != nil {
		t.Fatalf("expected ls to be found in PATH: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty resolved path")
	}
}

func TestResolverResolveApp_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dbeaver-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp binary: %v", err)
	}

	t.Setenv("PAM_CONNECTOR_DBEAVER_PATH", bin)
	r := NewResolver(nil)
	res, err := r.ResolveApp(AppDBeaver)
	if err != nil {
		t.Fatalf("expected env override to succeed: %v", err)
	}
	if res.Source != "env" {
		t.Fatalf("expected source=env, got %q", res.Source)
	}
	if res.Path != bin {
		t.Fatalf("expected path=%q, got %q", bin, res.Path)
	}
}

func TestResolverResolveApp_EnvOverrideInvalid(t *testing.T) {
	t.Setenv("PAM_CONNECTOR_DBEAVER_PATH", "/tmp/does-not-exist-pam-test")
	r := NewResolver(nil)
	_, err := r.ResolveApp(AppDBeaver)
	if err == nil {
		t.Fatal("expected error for invalid env override path")
	}
	de, ok := err.(*DiscoveryError)
	if !ok {
		t.Fatalf("expected DiscoveryError, got %T", err)
	}
	if de.Source != "env" {
		t.Fatalf("expected source=env, got %q", de.Source)
	}
}

func TestResolverResolveApp_ConfigOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "redis-cli-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp binary: %v", err)
	}

	// Clear env to ensure config takes priority
	t.Setenv("PAM_CONNECTOR_REDIS_CLI_PATH", "")

	cfg := &ConnectorConfig{
		Apps: map[string]string{
			"redis_cli": bin,
		},
		loadedFrom: "/test/config.yaml",
	}
	r := NewResolver(cfg)
	res, err := r.ResolveApp(AppRedisCLI)
	if err != nil {
		t.Fatalf("expected config override to succeed: %v", err)
	}
	if res.Source != "config" {
		t.Fatalf("expected source=config, got %q", res.Source)
	}
	if res.Path != bin {
		t.Fatalf("expected path=%q, got %q", bin, res.Path)
	}
}

func TestResolverResolveApp_ConfigOverrideInvalid(t *testing.T) {
	t.Setenv("PAM_CONNECTOR_REDIS_CLI_PATH", "")
	cfg := &ConnectorConfig{
		Apps: map[string]string{
			"redis_cli": "/tmp/does-not-exist-pam-test",
		},
		loadedFrom: "/test/config.yaml",
	}
	r := NewResolver(cfg)
	_, err := r.ResolveApp(AppRedisCLI)
	if err == nil {
		t.Fatal("expected error for invalid config path")
	}
	de, ok := err.(*DiscoveryError)
	if !ok {
		t.Fatalf("expected DiscoveryError, got %T", err)
	}
	if de.Source != "config" {
		t.Fatalf("expected source=config, got %q", de.Source)
	}
}

func TestResolverResolveApp_EnvTakesPriorityOverConfig(t *testing.T) {
	dir := t.TempDir()
	envBin := filepath.Join(dir, "env-redis")
	cfgBin := filepath.Join(dir, "cfg-redis")
	for _, p := range []string{envBin, cfgBin} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatalf("write temp binary: %v", err)
		}
	}

	t.Setenv("PAM_CONNECTOR_REDIS_CLI_PATH", envBin)
	cfg := &ConnectorConfig{
		Apps: map[string]string{
			"redis_cli": cfgBin,
		},
	}
	r := NewResolver(cfg)
	res, err := r.ResolveApp(AppRedisCLI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Source != "env" {
		t.Fatalf("expected env to take priority, got source=%q", res.Source)
	}
	if res.Path != envBin {
		t.Fatalf("expected env path %q, got %q", envBin, res.Path)
	}
}

func TestResolverResolveTerminal_Default(t *testing.T) {
	// Clear env
	t.Setenv("PAM_CONNECTOR_TERMINAL_MACOS", "")
	t.Setenv("PAM_CONNECTOR_TERMINAL_LINUX", "")
	t.Setenv("PAM_CONNECTOR_TERMINAL_WINDOWS", "")

	r := NewResolver(nil)
	res := r.ResolveTerminal()
	if res.Terminal != "auto" {
		t.Fatalf("expected terminal=auto, got %q", res.Terminal)
	}
	if res.Source != "auto" {
		t.Fatalf("expected source=auto, got %q", res.Source)
	}
}

func TestResolverResolveTerminal_EnvOverride(t *testing.T) {
	envKey := terminalEnvKeys[runtime.GOOS]
	if envKey == "" {
		t.Skip("no terminal env key for this OS")
	}
	t.Setenv(envKey, "iterm")
	r := NewResolver(nil)
	res := r.ResolveTerminal()
	if res.Terminal != "iterm" {
		t.Fatalf("expected terminal=iterm, got %q", res.Terminal)
	}
	if res.Source != "env" {
		t.Fatalf("expected source=env, got %q", res.Source)
	}
}

func TestResolverResolveTerminal_ConfigOverride(t *testing.T) {
	// Clear env
	t.Setenv("PAM_CONNECTOR_TERMINAL_MACOS", "")
	t.Setenv("PAM_CONNECTOR_TERMINAL_LINUX", "")
	t.Setenv("PAM_CONNECTOR_TERMINAL_WINDOWS", "")

	cfg := &ConnectorConfig{
		Terminal: TerminalConfig{
			MacOS:   "iterm",
			Linux:   "konsole",
			Windows: "wt",
		},
	}
	r := NewResolver(cfg)
	res := r.ResolveTerminal()
	if res.Source != "config" {
		t.Fatalf("expected source=config, got %q", res.Source)
	}
	switch runtime.GOOS {
	case "darwin":
		if res.Terminal != "iterm" {
			t.Fatalf("expected terminal=iterm, got %q", res.Terminal)
		}
	case "linux":
		if res.Terminal != "konsole" {
			t.Fatalf("expected terminal=konsole, got %q", res.Terminal)
		}
	case "windows":
		if res.Terminal != "wt" {
			t.Fatalf("expected terminal=wt, got %q", res.Terminal)
		}
	}
}

func TestAutoDetectCandidates_ReturnsNonEmpty(t *testing.T) {
	for _, app := range []AppName{AppDBeaver, AppFileZilla, AppWinSCP, AppPuTTY, AppRedisCLI} {
		candidates := autoDetectCandidates(app)
		if len(candidates) == 0 {
			t.Errorf("expected non-empty candidates for %s on %s", app, runtime.GOOS)
		}
	}
}

func TestBuildInstallHint_ContainsEnvVar(t *testing.T) {
	hint := buildInstallHint(AppDBeaver, "PAM_CONNECTOR_DBEAVER_PATH")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
	if !containsSubstring(hint, "PAM_CONNECTOR_DBEAVER_PATH") {
		t.Fatalf("expected hint to mention env var, got %q", hint)
	}
}

func TestDefaultConfigPath_NonEmpty(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Fatal("expected non-empty default config path")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("expected absolute path, got %q", path)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsCheck(s, sub))
}

func containsCheck(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
