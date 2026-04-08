CREATE TABLE IF NOT EXISTS ldap_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    provider_mode TEXT NOT NULL DEFAULT 'local' CHECK (provider_mode IN ('local', 'ldap', 'hybrid')),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    host TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL DEFAULT 389 CHECK (port > 0 AND port <= 65535),
    url TEXT NOT NULL DEFAULT '',
    base_dn TEXT NOT NULL DEFAULT '',
    bind_dn TEXT NOT NULL DEFAULT '',
    bind_password TEXT NOT NULL DEFAULT '',
    user_search_filter TEXT NOT NULL DEFAULT '(&(objectClass=user)({{username_attr}}={{username}}))',
    sync_user_filter TEXT NOT NULL DEFAULT '(objectClass=user)',
    username_attribute TEXT NOT NULL DEFAULT 'sAMAccountName',
    display_name_attribute TEXT NOT NULL DEFAULT 'displayName',
    email_attribute TEXT NOT NULL DEFAULT 'mail',
    group_search_base_dn TEXT NOT NULL DEFAULT '',
    group_search_filter TEXT NOT NULL DEFAULT '(&(objectClass=group)(member={{user_dn}}))',
    group_name_attribute TEXT NOT NULL DEFAULT 'cn',
    group_role_mapping TEXT NOT NULL DEFAULT '',
    use_tls BOOLEAN NOT NULL DEFAULT FALSE,
    start_tls BOOLEAN NOT NULL DEFAULT FALSE,
    insecure_skip_verify BOOLEAN NOT NULL DEFAULT FALSE,
    deactivate_missing_users BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ldap_sync_runs (
    id BIGSERIAL PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    triggered_by UUID REFERENCES users(id) ON DELETE SET NULL,
    summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    error TEXT
);

CREATE INDEX IF NOT EXISTS idx_ldap_sync_runs_started_at ON ldap_sync_runs(started_at DESC);
