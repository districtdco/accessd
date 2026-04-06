package auth

import "context"

type contextKey string

const currentUserKey contextKey = "auth.current_user"

func WithCurrentUser(ctx context.Context, user CurrentUser) context.Context {
	return context.WithValue(ctx, currentUserKey, user)
}

func CurrentUserFromContext(ctx context.Context) (CurrentUser, bool) {
	user, ok := ctx.Value(currentUserKey).(CurrentUser)
	return user, ok
}
