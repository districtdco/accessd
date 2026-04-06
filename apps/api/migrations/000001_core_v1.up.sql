CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username TEXT NOT NULL UNIQUE,
    email TEXT,
    display_name TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_groups (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_groups_group_id ON user_groups(group_id);

CREATE TABLE IF NOT EXISTS assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    asset_type TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port > 0 AND port <= 65535),
    status TEXT NOT NULL DEFAULT 'active',
    description TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, host, port)
);

CREATE INDEX IF NOT EXISTS idx_assets_type ON assets(asset_type);
CREATE INDEX IF NOT EXISTS idx_assets_status ON assets(status);

CREATE TABLE IF NOT EXISTS asset_protocols (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    protocol TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (asset_id, protocol)
);

CREATE INDEX IF NOT EXISTS idx_asset_protocols_protocol ON asset_protocols(protocol);

CREATE TABLE IF NOT EXISTS access_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type TEXT NOT NULL CHECK (subject_type IN ('user', 'group')),
    subject_id UUID NOT NULL,
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    effect TEXT NOT NULL DEFAULT 'allow' CHECK (effect IN ('allow', 'deny')),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (subject_type, subject_id, asset_id, action)
);

CREATE INDEX IF NOT EXISTS idx_access_grants_asset_id ON access_grants(asset_id);
CREATE INDEX IF NOT EXISTS idx_access_grants_subject ON access_grants(subject_type, subject_id);

CREATE TABLE IF NOT EXISTS credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    credential_type TEXT NOT NULL,
    algorithm TEXT NOT NULL DEFAULT 'AES-256-GCM',
    key_id TEXT NOT NULL,
    nonce BYTEA NOT NULL,
    encrypted_data BYTEA NOT NULL,
    aad BYTEA,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    version INTEGER NOT NULL DEFAULT 1,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_rotated_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_credentials_asset_id ON credentials(asset_id);
CREATE INDEX IF NOT EXISTS idx_credentials_type ON credentials(credential_type);

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    protocol TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'completed', 'failed', 'terminated', 'expired')),
    launched_via TEXT NOT NULL DEFAULT 'api',
    reason TEXT,
    client_ip INET,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_asset_id ON sessions(asset_id);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_created_at ON sessions(created_at DESC);

-- append-only event table
CREATE TABLE IF NOT EXISTS session_events (
    id BIGSERIAL PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    event_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    payload JSONB NOT NULL DEFAULT '{}'::JSONB
);

CREATE INDEX IF NOT EXISTS idx_session_events_session_id ON session_events(session_id);
CREATE INDEX IF NOT EXISTS idx_session_events_event_time ON session_events(event_time DESC);

-- append-only event table
CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    event_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    asset_id UUID REFERENCES assets(id) ON DELETE SET NULL,
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    action TEXT,
    outcome TEXT,
    source_ip INET,
    user_agent TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB
);

CREATE INDEX IF NOT EXISTS idx_audit_events_event_time ON audit_events(event_time DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor_user_id ON audit_events(actor_user_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_asset_id ON audit_events(asset_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_session_id ON audit_events(session_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_event_type ON audit_events(event_type);
