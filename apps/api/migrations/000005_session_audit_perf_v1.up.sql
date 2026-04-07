CREATE INDEX IF NOT EXISTS idx_session_events_session_id_id
    ON session_events(session_id, id);

CREATE INDEX IF NOT EXISTS idx_session_events_session_event_id
    ON session_events(session_id, event_type, id);

CREATE INDEX IF NOT EXISTS idx_sessions_reason_created_at
    ON sessions(reason, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_events_event_time_id
    ON audit_events(event_time DESC, id DESC);
