package config

import (
	"os"
	"path/filepath"
	"testing"
)

func clearAccessDEnvForTests(t *testing.T) {
	t.Helper()
	keys := []string{
		"ACCESSD_CONFIG_FILE",
		"ACCESSD_ENV",
		"ACCESSD_DB_URL",
		"ACCESSD_VAULT_KEY",
		"ACCESSD_LAUNCH_TOKEN_SECRET",
		"ACCESSD_AUTH_COOKIE_SECURE",
		"ACCESSD_ALLOW_UNSAFE_MODE",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}

func TestLoadLDAPSambaDefaults(t *testing.T) {
	clearAccessDEnvForTests(t)
	t.Setenv("ACCESSD_ENV", "development")
	t.Setenv("ACCESSD_DB_URL", "postgres://postgres:postgres@localhost:5432/pam?sslmode=disable")
	t.Setenv("ACCESSD_VAULT_KEY", "dev-only-key")
	t.Setenv("ACCESSD_LAUNCH_TOKEN_SECRET", "dev-secret")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if got, want := cfg.Auth.LDAP.UsernameAttribute, "sAMAccountName"; got != want {
		t.Fatalf("UsernameAttribute = %q, want %q", got, want)
	}
	if got, want := cfg.Auth.LDAP.UserSearchFilter, "(&(objectClass=user)({{username_attr}}={{username}}))"; got != want {
		t.Fatalf("UserSearchFilter = %q, want %q", got, want)
	}
	if got, want := cfg.Auth.LDAP.DisplayNameAttribute, "displayName"; got != want {
		t.Fatalf("DisplayNameAttribute = %q, want %q", got, want)
	}
	if got, want := cfg.Auth.LDAP.GroupSearchFilter, "(&(objectClass=group)(member={{user_dn}}))"; got != want {
		t.Fatalf("GroupSearchFilter = %q, want %q", got, want)
	}
	if got, want := cfg.Auth.ProviderMode, "local"; got != want {
		t.Fatalf("ProviderMode = %q, want %q", got, want)
	}
}

func TestLoad_UsesConfigFileWhenEnvMissing(t *testing.T) {
	clearAccessDEnvForTests(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "accessd.env")
	content := "ACCESSD_DB_URL=postgres://postgres:postgres@localhost:5432/pam?sslmode=disable\n" +
		"ACCESSD_VAULT_KEY=dev-only-key\n" +
		"ACCESSD_LAUNCH_TOKEN_SECRET=dev-secret\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("ACCESSD_CONFIG_FILE", cfgPath)
	t.Setenv("ACCESSD_DB_URL", "")
	t.Setenv("ACCESSD_VAULT_KEY", "")
	t.Setenv("ACCESSD_LAUNCH_TOKEN_SECRET", "")
	t.Setenv("ACCESSD_ENV", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DB.URL == "" {
		t.Fatalf("expected DB URL from config file")
	}
}

func TestLoad_ConfigFileDoesNotOverrideExplicitEnv(t *testing.T) {
	clearAccessDEnvForTests(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "accessd.env")
	content := "ACCESSD_DB_URL=postgres://postgres:postgres@localhost:5432/from_file?sslmode=disable\n" +
		"ACCESSD_VAULT_KEY=dev-only-key\n" +
		"ACCESSD_LAUNCH_TOKEN_SECRET=dev-secret\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("ACCESSD_CONFIG_FILE", cfgPath)
	t.Setenv("ACCESSD_ENV", "development")
	t.Setenv("ACCESSD_DB_URL", "postgres://postgres:postgres@localhost:5432/from_env?sslmode=disable")
	t.Setenv("ACCESSD_VAULT_KEY", "dev-only-key")
	t.Setenv("ACCESSD_LAUNCH_TOKEN_SECRET", "dev-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got, want := cfg.DB.URL, "postgres://postgres:postgres@localhost:5432/from_env?sslmode=disable"; got != want {
		t.Fatalf("DB.URL = %q, want %q", got, want)
	}
}
