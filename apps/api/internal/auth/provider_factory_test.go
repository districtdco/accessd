package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type stubProvider struct {
	name string
	err  error
	user User
}

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) Authenticate(_ context.Context, _, _ string) (User, error) {
	if s.err != nil {
		return User{}, s.err
	}
	return s.user, nil
}

func TestFallbackProviderUsesLocalFallbackForLDAPErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := &FallbackProvider{
		primary: stubProvider{
			name: "ldap",
			err:  &ldapAuthError{kind: ldapFailureBindSearchConfig, err: errors.New("bad ldap bind")},
		},
		fallback: stubProvider{
			name: "local",
			user: User{Username: "admin-local"},
		},
		logger: logger,
	}

	got, err := p.Authenticate(context.Background(), "admin-local", "password")
	if err != nil {
		t.Fatalf("Authenticate() unexpected error: %v", err)
	}
	if got.Username != "admin-local" {
		t.Fatalf("Authenticate() username = %q, want %q", got.Username, "admin-local")
	}
}

func TestClassifyAuthFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "invalid credentials",
			err:  ErrInvalidCredentials,
			want: "invalid_credentials",
		},
		{
			name: "ldap user not found",
			err:  &ldapAuthError{kind: ldapFailureUserNotFound, err: errors.New("missing")},
			want: "user_not_found",
		},
		{
			name: "ldap tls issue",
			err:  &ldapAuthError{kind: ldapFailureTLSOrConnectivity, err: errors.New("timeout")},
			want: "tls_or_connectivity_issue",
		},
		{
			name: "generic error",
			err:  errors.New("boom"),
			want: "provider_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAuthFailureReason(tt.err); got != tt.want {
				t.Fatalf("classifyAuthFailureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
