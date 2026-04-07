package requestctx

import (
	"context"
	"strings"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func WithRequestID(ctx context.Context, requestID string) context.Context {
	trimmed := strings.TrimSpace(requestID)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, trimmed)
}

func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	raw := ctx.Value(requestIDKey)
	value, _ := raw.(string)
	return strings.TrimSpace(value)
}
