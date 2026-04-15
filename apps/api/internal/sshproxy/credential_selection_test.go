package sshproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/districtdco/accessd/api/internal/credentials"
)

type fakeCredentialResolver struct {
	entries map[string]credentials.ResolvedCredential
	errs    map[string]error
}

func (f fakeCredentialResolver) ResolveForAsset(_ context.Context, _ string, credentialType string) (credentials.ResolvedCredential, error) {
	if err, ok := f.errs[credentialType]; ok {
		return credentials.ResolvedCredential{}, err
	}
	if cred, ok := f.entries[credentialType]; ok {
		return cred, nil
	}
	return credentials.ResolvedCredential{}, credentials.ErrCredentialNotFound
}

func TestResolveLinuxUpstreamCredential_PrefersSSHKey(t *testing.T) {
	s := &Server{
		credSvc: fakeCredentialResolver{
			entries: map[string]credentials.ResolvedCredential{
				credentials.TypeSSHKey:   {Username: "key-user", Secret: "key-secret"},
				credentials.TypePassword: {Username: "pass-user", Secret: "pass-secret"},
			},
		},
	}

	cred, credType, err := s.resolveLinuxUpstreamCredential(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("resolveLinuxUpstreamCredential returned error: %v", err)
	}
	if credType != credentials.TypeSSHKey {
		t.Fatalf("expected %q, got %q", credentials.TypeSSHKey, credType)
	}
	if cred.Username != "key-user" {
		t.Fatalf("expected ssh key username, got %q", cred.Username)
	}
}

func TestResolveLinuxUpstreamCredential_FallsBackToPassword(t *testing.T) {
	s := &Server{
		credSvc: fakeCredentialResolver{
			entries: map[string]credentials.ResolvedCredential{
				credentials.TypePassword: {Username: "pass-user", Secret: "pass-secret"},
			},
		},
	}

	cred, credType, err := s.resolveLinuxUpstreamCredential(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("resolveLinuxUpstreamCredential returned error: %v", err)
	}
	if credType != credentials.TypePassword {
		t.Fatalf("expected %q, got %q", credentials.TypePassword, credType)
	}
	if cred.Username != "pass-user" {
		t.Fatalf("expected password username, got %q", cred.Username)
	}
}

func TestResolveLinuxUpstreamCredential_PropagatesSSHLookupErrors(t *testing.T) {
	wantErr := errors.New("vault is unavailable")
	s := &Server{
		credSvc: fakeCredentialResolver{
			errs: map[string]error{
				credentials.TypeSSHKey: wantErr,
			},
		},
	}

	_, _, err := s.resolveLinuxUpstreamCredential(context.Background(), "asset-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped ssh lookup error, got %v", err)
	}
}
