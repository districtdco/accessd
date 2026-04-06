package auth

import "context"

type Provider interface {
	Name() string
	Authenticate(ctx context.Context, username, password string) (User, error)
}
