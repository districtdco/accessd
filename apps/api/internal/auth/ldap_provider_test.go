package auth

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func TestParseGroupRoleMappingAcceptsGroupNameAndDN(t *testing.T) {
	raw := "PAM Operators=operator,cn=pam-admins,ou=groups,dc=corp,dc=example,dc=com=admin|auditor"
	got, err := parseGroupRoleMapping(raw)
	if err != nil {
		t.Fatalf("parseGroupRoleMapping() unexpected error: %v", err)
	}

	if roles := got["pam operators"]; len(roles) != 1 || roles[0] != "operator" {
		t.Fatalf("mapping for group name mismatch: %#v", roles)
	}
	if roles := got["cn=pam-admins,ou=groups,dc=corp,dc=example,dc=com"]; len(roles) != 2 {
		t.Fatalf("mapping for DN mismatch: %#v", roles)
	}
}

func TestParseGroupRoleMappingMergesDuplicateEntries(t *testing.T) {
	raw := "pam-ops=operator,pam-ops=operator|auditor"
	got, err := parseGroupRoleMapping(raw)
	if err != nil {
		t.Fatalf("parseGroupRoleMapping() unexpected error: %v", err)
	}

	roles := got["pam-ops"]
	if len(roles) != 2 {
		t.Fatalf("expected merged unique roles, got %#v", roles)
	}
}

func TestGroupMappingKeysIncludesDNAndConfiguredAttribute(t *testing.T) {
	entry := &ldap.Entry{
		DN: "CN=PAM Operators,OU=Groups,DC=corp,DC=example,DC=com",
		Attributes: []*ldap.EntryAttribute{
			{
				Name:   "cn",
				Values: []string{"PAM Operators"},
			},
		},
	}

	keys := groupMappingKeys(entry, "cn")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d (%#v)", len(keys), keys)
	}
}

func TestLDAPAuthErrorUnwrapsInvalidCredentialsKinds(t *testing.T) {
	userNotFound := &ldapAuthError{kind: ldapFailureUserNotFound, err: errors.New("missing")}
	if !errors.Is(userNotFound, ErrInvalidCredentials) {
		t.Fatal("user_not_found should unwrap to ErrInvalidCredentials")
	}

	invalidPassword := &ldapAuthError{kind: ldapFailureInvalidPassword, err: errors.New("bad password")}
	if !errors.Is(invalidPassword, ErrInvalidCredentials) {
		t.Fatal("invalid_password should unwrap to ErrInvalidCredentials")
	}

	configIssue := &ldapAuthError{kind: ldapFailureBindSearchConfig, err: errors.New("bad bind dn")}
	if errors.Is(configIssue, ErrInvalidCredentials) {
		t.Fatal("bind_or_search_config_issue should not unwrap to ErrInvalidCredentials")
	}
}
