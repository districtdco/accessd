package config

import "testing"

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
