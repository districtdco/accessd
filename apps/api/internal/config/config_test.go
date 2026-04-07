package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLDAPSambaDefaults(t *testing.T) {
	t.Setenv("PAM_ENV", "development")
	t.Setenv("PAM_DB_URL", "postgres://postgres:postgres@localhost:5432/pam?sslmode=disable")
	t.Setenv("PAM_VAULT_KEY", "dev-only-key")
	t.Setenv("PAM_LAUNCH_TOKEN_SECRET", "dev-secret")
	t.Setenv("PAM_AUTH_PROVIDER_MODE", "ldap")
	t.Setenv("PAM_LDAP_BASE_DN", "dc=corp,dc=example,dc=com")

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
}

func TestLoadHybridRequiresLDAPBaseDN(t *testing.T) {
	t.Setenv("PAM_ENV", "development")
	t.Setenv("PAM_DB_URL", "postgres://postgres:postgres@localhost:5432/pam?sslmode=disable")
	t.Setenv("PAM_VAULT_KEY", "dev-only-key")
	t.Setenv("PAM_LAUNCH_TOKEN_SECRET", "dev-secret")
	t.Setenv("PAM_AUTH_PROVIDER_MODE", "hybrid")
	t.Setenv("PAM_LDAP_BASE_DN", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error, got nil")
	}
}

func TestLoad_UsesConfigFileWhenEnvMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pam.env")
	content := "PAM_DB_URL=postgres://postgres:postgres@localhost:5432/pam?sslmode=disable\n" +
		"PAM_VAULT_KEY=dev-only-key\n" +
		"PAM_LAUNCH_TOKEN_SECRET=dev-secret\n" +
		"PAM_AUTH_PROVIDER_MODE=local\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("PAM_CONFIG_FILE", cfgPath)
	t.Setenv("PAM_DB_URL", "")
	t.Setenv("PAM_VAULT_KEY", "")
	t.Setenv("PAM_LAUNCH_TOKEN_SECRET", "")
	t.Setenv("PAM_ENV", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DB.URL == "" {
		t.Fatalf("expected DB URL from config file")
	}
}

func TestLoad_ConfigFileDoesNotOverrideExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pam.env")
	content := "PAM_DB_URL=postgres://postgres:postgres@localhost:5432/from_file?sslmode=disable\n" +
		"PAM_VAULT_KEY=dev-only-key\n" +
		"PAM_LAUNCH_TOKEN_SECRET=dev-secret\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("PAM_CONFIG_FILE", cfgPath)
	t.Setenv("PAM_ENV", "development")
	t.Setenv("PAM_DB_URL", "postgres://postgres:postgres@localhost:5432/from_env?sslmode=disable")
	t.Setenv("PAM_VAULT_KEY", "dev-only-key")
	t.Setenv("PAM_LAUNCH_TOKEN_SECRET", "dev-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got, want := cfg.DB.URL, "postgres://postgres:postgres@localhost:5432/from_env?sslmode=disable"; got != want {
		t.Fatalf("DB.URL = %q, want %q", got, want)
	}
}
