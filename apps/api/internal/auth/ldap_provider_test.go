package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/districtd/pam/api/internal/config"
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

func TestBuildLDAPTLSConfig_InvalidCACertFile(t *testing.T) {
	cfg := config.LDAPConfig{
		CACertFile: "/tmp/accessd-nonexistent-ca.pem",
	}
	_, err := buildLDAPTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CA cert file")
	}
}

func TestBuildLDAPTLSConfig_LoadsCustomCACert(t *testing.T) {
	certPEM, rawSubject := generateSelfSignedCACertPEM(t)
	tmp, err := os.CreateTemp("", "accessd-ldap-ca-*.pem")
	if err != nil {
		t.Fatalf("create temp cert file: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(certPEM); err != nil {
		t.Fatalf("write temp cert file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp cert file: %v", err)
	}

	cfg := config.LDAPConfig{
		CACertFile: tmp.Name(),
	}
	tlsCfg, err := buildLDAPTLSConfig(cfg)
	if err != nil {
		t.Fatalf("buildLDAPTLSConfig() unexpected error: %v", err)
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be configured")
	}
	found := false
	for _, subject := range tlsCfg.RootCAs.Subjects() {
		if string(subject) == string(rawSubject) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected custom CA subject to be loaded into RootCAs")
	}
}

func TestLDAPUsernameCandidates(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "plain username",
			input:    "ankit",
			expected: []string{"ankit"},
		},
		{
			name:     "domain backslash username",
			input:    "DISTRICTD\\ankit",
			expected: []string{"DISTRICTD\\ankit", "ankit"},
		},
		{
			name:     "upn username",
			input:    "ankit@districtd.lan",
			expected: []string{"ankit@districtd.lan", "ankit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ldapUsernameCandidates(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("candidate count = %d, want %d (%#v)", len(got), len(tt.expected), got)
			}
			for i := range tt.expected {
				if got[i] != tt.expected[i] {
					t.Fatalf("candidate[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestLDAPLookupUsernameAttributesIncludesFallbacks(t *testing.T) {
	got := ldapLookupUsernameAttributes("sAMAccountName")
	required := []string{"sAMAccountName", "uid", "userPrincipalName", "mail", "cn"}
	for _, want := range required {
		found := false
		for _, attr := range got {
			if attr == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected attribute %q in %#v", want, got)
		}
	}
}

func generateSelfSignedCACertPEM(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "AccessD Test LDAP Root CA",
			Organization: []string{"AccessD Tests"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if len(pemBytes) == 0 {
		t.Fatal("failed to encode cert to pem")
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse generated cert: %v", err)
	}
	return pemBytes, parsed.RawSubject
}
