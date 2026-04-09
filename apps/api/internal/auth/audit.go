package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	AuditLoginSuccess              = "login_success"
	AuditLoginFailed               = "login_failed"
	AuditLoginFailedInvalidPass    = "login_failed_invalid_password"
	AuditLoginFailedUserNotFound   = "login_failed_user_not_found"
	AuditLoginFailedLDAPError      = "login_failed_ldap_error"
	AuditLoginFailedRateLimited    = "login_failed_rate_limited"
	AuditPasswordChangeSuccess     = "password_change_success"
	AuditPasswordChangeFailed      = "password_change_failed"
)

type AuditEvent struct {
	EventType   string
	Action      string
	Outcome     string
	ActorUserID *string
	SourceIP    string
	UserAgent   string
	Metadata    map[string]any
}

func writeAuditEvent(ctx context.Context, pool *pgxpool.Pool, evt AuditEvent) error {
	metaJSON, err := json.Marshal(evt.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}

	const insertSQL = `
INSERT INTO audit_events (event_type, action, outcome, actor_user_id, source_ip, user_agent, metadata)
VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet, $6, $7);`

	if _, err := pool.Exec(ctx, insertSQL,
		evt.EventType,
		evt.Action,
		evt.Outcome,
		evt.ActorUserID,
		evt.SourceIP,
		evt.UserAgent,
		metaJSON,
	); err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}
