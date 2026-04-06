package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/districtd/pam/api/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewProvider(pool *pgxpool.Pool, cfg config.AuthConfig, logger *slog.Logger) (Provider, error) {
	localProvider := NewLocalProvider(pool, logger)
	mode := strings.ToLower(strings.TrimSpace(cfg.ProviderMode))
	switch mode {
	case "", "local":
		return localProvider, nil
	case "ldap":
		ldapProvider, err := NewLDAPProvider(pool, cfg.LDAP, logger)
		if err != nil {
			return nil, err
		}
		return ldapProvider, nil
	case "hybrid":
		ldapProvider, err := NewLDAPProvider(pool, cfg.LDAP, logger)
		if err != nil {
			return nil, err
		}
		return &FallbackProvider{
			primary:  ldapProvider,
			fallback: localProvider,
			logger:   logger.With("auth_provider", "hybrid"),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported auth provider mode %q", cfg.ProviderMode)
	}
}

type FallbackProvider struct {
	primary  Provider
	fallback Provider
	logger   *slog.Logger
}

func (p *FallbackProvider) Name() string {
	return fmt.Sprintf("%s->%s", p.primary.Name(), p.fallback.Name())
}

func (p *FallbackProvider) Authenticate(ctx context.Context, username, password string) (User, error) {
	user, err := p.primary.Authenticate(ctx, username, password)
	if err == nil {
		return user, nil
	}

	p.logger.Warn(
		"primary auth provider failed; trying fallback",
		"primary_provider", p.primary.Name(),
		"failure_reason", classifyAuthFailureReason(err),
		"error", err,
	)
	return p.fallback.Authenticate(ctx, username, password)
}

func classifyAuthFailureReason(err error) string {
	if errors.Is(err, ErrInvalidCredentials) {
		var ldapErr *ldapAuthError
		if errors.As(err, &ldapErr) {
			return string(ldapErr.kind)
		}
		return "invalid_credentials"
	}

	var ldapErr *ldapAuthError
	if errors.As(err, &ldapErr) {
		return string(ldapErr.kind)
	}
	return "provider_error"
}
