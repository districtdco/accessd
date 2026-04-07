# apps/api — PAM Backend

Go backend for PAM v1. Current slices include provider-based auth (`local`, `ldap`, `hybrid`) + cookie sessions + RBAC foundation, plus integrated brokered launch flows for shell, SFTP, DBeaver, and Redis CLI.

## What Exists Now

- `cmd/server` CLI entrypoint with modes:
  - `server`
  - `migrate up`
  - `migrate status`
  - `bootstrap`
- `internal/config`: environment-based configuration loading and validation
- `internal/db`: pgxpool initialization + startup connectivity verification
- `internal/migrate`: in-repo SQL migration runner (`schema_migrations` tracking)
- `internal/httpserver`: `net/http` server + router wiring
- `internal/handlers`: health/version + auth/access/session launch + session lifecycle event endpoints
- `internal/auth`: provider-based auth (`local` + `ldap` + `hybrid`), password hashing, cookie-session handling, RBAC middleware, dev bootstrap
- `internal/assets`: asset service with upsert/list/get (`linux_vm`, `database`, `redis`)
- `internal/credentials`: credential storage service (AES-256-GCM encryption/decryption with app master key)
- `internal/access`: grant service for allowed assets and action checks + `/access/my` backing logic
- `internal/sessions`: launch/session service (short-lived HMAC launch tokens, session lifecycle/event writes, connector lifecycle metadata writes)
- `internal/sshproxy`: SSH proxy server for first proxied shell path
- `internal/audit`: audit service scaffold
- `migrations/`: ordered SQL migration files (`000001_*.up.sql`, `000002_*.up.sql`, `000003_*.up.sql`)

## Commands

```bash
# From apps/api
go run ./cmd/server

# Explicit server mode
go run ./cmd/server server

# Apply pending migrations
go run ./cmd/server migrate up

# Show migration status
go run ./cmd/server migrate status

# Run migrations + dev auth bootstrap without starting HTTP server
go run ./cmd/server bootstrap

# Build with version metadata injection (useful for deploy artifacts)
go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/pam-api ./cmd/server
```

## Backend Integration Tests

Focused reliability tests now live under `internal/integration` and cover high-value auth/access/session/admin flows against real handlers/services and PostgreSQL.

```bash
cd apps/api
export PAM_TEST_DB_URL='postgres://postgres:postgres@localhost:5432/pam_test?sslmode=disable'
go test ./internal/integration -count=1
```

Notes:

- Tests are skipped when `PAM_TEST_DB_URL` is not set.
- Tests truncate app tables between test cases, so use a dedicated test database.
- Optional safeguard override: set `PAM_TEST_DB_UNSAFE_OK=1` if your DB URL does not include `test`.

## Startup / Runtime Safety Notes

- `server` startup now logs explicit phases (`migrations`, `bootstrap`) with fail-fast behavior.
- Migration failures stop startup and return non-zero exit.
- `GET /health/ready` remains the primary readiness endpoint for deploy checks.
- In systemd deployment, prefer running migrations in `ExecStartPre` (see `deploy/systemd/pam-api.service`).

## Configuration

Required:

- `PAM_DB_URL`: PostgreSQL connection URL

Optional:

- `PAM_APP_NAME` (default: `pam-api`)
- `PAM_ENV` (default: `development`)
- `PAM_HTTP_ADDR` (default: `:8080`)
- `PAM_CORS_ALLOWED_ORIGINS` (default in development: `http://localhost:3000,http://127.0.0.1:3000`; empty by default outside development)
- `PAM_CONFIG_FILE` (optional path to `KEY=VALUE` env file; loaded first, explicit env vars still take precedence)
- `PAM_SHUTDOWN_TIMEOUT` (default: `15s`)
- `PAM_VERSION` (default: `0.1.0-dev`)
- `PAM_COMMIT` (default: `dev`)
- `PAM_BUILT_AT` (default: `unknown`)
- `PAM_MIGRATIONS_DIR` (default: `migrations`)
- `PAM_MIGRATIONS_TABLE` (default: `schema_migrations`)
- `PAM_DB_MAX_CONNS` (default: `10`)
- `PAM_DB_MIN_CONNS` (default: `1`)
- `PAM_DB_MAX_CONN_LIFETIME` (default: `1h`)
- `PAM_DB_MAX_CONN_IDLE_TIME` (default: `15m`)
- `PAM_AUTH_COOKIE_NAME` (default: `pam_session`)
- `PAM_AUTH_SESSION_TTL` (default: `12h`)
- `PAM_AUTH_COOKIE_SECURE` (default: `false` in `development`, `true` otherwise)
- `PAM_AUTH_COOKIE_SAMESITE` (default: `lax` in `development`, `strict` otherwise; options: `lax`, `strict`, `none`)
- `PAM_AUTH_PROVIDER_MODE` (default: `local`; options: `local`, `ldap`, `hybrid`)
- `PAM_VAULT_KEY` (required): master key for credential encryption (base64-encoded 32-byte key recommended; required outside development unless `PAM_ALLOW_UNSAFE_MODE=true`)
- `PAM_VAULT_KEY_ID` (default: `v1`)
- `PAM_LAUNCH_TOKEN_SECRET` (required): HMAC signing secret for launch tokens
- `PAM_LAUNCH_TOKEN_TTL` (default: `2m`)
- `PAM_LAUNCH_MATERIALIZE_TIMEOUT` (default: `45s`): timeout for connector-accepted launches to materialize into a proxy/client connection before auto-fail
- `PAM_LAUNCH_SWEEP_INTERVAL` (default: `15s`): interval for stale pending launch sweep
- `PAM_SSH_PROXY_ADDR` (default: `:2222`)
- `PAM_SSH_PROXY_PUBLIC_HOST` (default: `127.0.0.1`)
- `PAM_SSH_PROXY_PUBLIC_PORT` (default: `2222`)
- `PAM_SSH_PROXY_USERNAME` (default: `pam`)
- `PAM_SSH_PROXY_IDLE_TIMEOUT` (default: `5m`)
- `PAM_SSH_PROXY_MAX_SESSION_DURATION` (default: `8h`)
- `PAM_PG_PROXY_BIND_HOST` (default: `127.0.0.1`)
- `PAM_PG_PROXY_PUBLIC_HOST` (default: `127.0.0.1`)
- `PAM_PG_PROXY_CONNECT_TIMEOUT` (default: `10s`)
- `PAM_PG_PROXY_QUERY_LOG_QUEUE` (default: `1024`)
- `PAM_PG_PROXY_QUERY_MAX_BYTES` (default: `16384`)
- `PAM_PG_PROXY_IDLE_TIMEOUT` (default: `5m`)
- `PAM_PG_PROXY_MAX_SESSION_DURATION` (default: `8h`)
- `PAM_MYSQL_PROXY_BIND_HOST` (default: `127.0.0.1`)
- `PAM_MYSQL_PROXY_PUBLIC_HOST` (default: `127.0.0.1`)
- `PAM_MYSQL_PROXY_CONNECT_TIMEOUT` (default: `10s`)
- `PAM_MYSQL_PROXY_QUERY_LOG_QUEUE` (default: `1024`)
- `PAM_MYSQL_PROXY_QUERY_MAX_BYTES` (default: `16384`)
- `PAM_MYSQL_PROXY_IDLE_TIMEOUT` (default: `5m`)
- `PAM_MYSQL_PROXY_MAX_SESSION_DURATION` (default: `8h`)
- `PAM_MSSQL_PROXY_BIND_HOST` (default: `127.0.0.1`)
- `PAM_MSSQL_PROXY_PUBLIC_HOST` (default: `127.0.0.1`)
- `PAM_MSSQL_PROXY_CONNECT_TIMEOUT` (default: `10s`)
- `PAM_MSSQL_PROXY_QUERY_LOG_QUEUE` (default: `1024`)
- `PAM_MSSQL_PROXY_QUERY_MAX_BYTES` (default: `16384`)
- `PAM_MSSQL_PROXY_IDLE_TIMEOUT` (default: `5m`)
- `PAM_MSSQL_PROXY_MAX_SESSION_DURATION` (default: `8h`)
- `PAM_REDIS_PROXY_BIND_HOST` (default: `127.0.0.1`)
- `PAM_REDIS_PROXY_PUBLIC_HOST` (default: `127.0.0.1`)
- `PAM_REDIS_PROXY_CONNECT_TIMEOUT` (default: `10s`)
- `PAM_REDIS_PROXY_COMMAND_LOG_QUEUE` (default: `1024`)
- `PAM_REDIS_PROXY_ARG_MAX_LEN` (default: `128`)
- `PAM_REDIS_PROXY_IDLE_TIMEOUT` (default: `5m`)
- `PAM_REDIS_PROXY_MAX_SESSION_DURATION` (default: `8h`)
- `PAM_SSH_PROXY_HOST_KEY_PATH` (default: `.pam_ssh_proxy_host_key`)
- `PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE` (default: `known-hosts`; options: `accept-new`, `known-hosts`, `insecure`)
- `PAM_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH` (default: `.pam_upstream_known_hosts`)
- `PAM_DEV_ADMIN_USERNAME` (default: `admin`)
- `PAM_DEV_ADMIN_PASSWORD` (default: `admin123`)
- `PAM_DEV_ADMIN_EMAIL` (default: `admin@pam.local`)
- `PAM_DEV_ADMIN_NAME` (default: `PAM Administrator`)
- `PAM_LDAP_URL` (optional; overrides host/port when set)
- `PAM_LDAP_HOST` (default: `127.0.0.1`)
- `PAM_LDAP_PORT` (default: `389`)
- `PAM_LDAP_BASE_DN` (required when auth mode is `ldap` or `hybrid`)
- `PAM_LDAP_BIND_DN` / `PAM_LDAP_BIND_PASSWORD` (optional service account for user/group searches)
- `PAM_LDAP_USER_FILTER` (default: `(&(objectCategory=person)(objectClass=user)({{username_attr}}={{username}}))`)
- `PAM_LDAP_USERNAME_ATTR` (default: `sAMAccountName`)
- `PAM_LDAP_DISPLAY_NAME_ATTR` (default: `displayName`; falls back to `cn`/`name` when empty)
- `PAM_LDAP_EMAIL_ATTR` (default: `mail`)
- `PAM_LDAP_USE_TLS` (default: `false`)
- `PAM_LDAP_STARTTLS` (default: `false`; mutually exclusive with `PAM_LDAP_USE_TLS`)
- `PAM_LDAP_INSECURE_SKIP_VERIFY` (default: `false`; dev-only certificate bypass)
- `PAM_LDAP_GROUP_BASE_DN` (optional; defaults to `PAM_LDAP_BASE_DN`)
- `PAM_LDAP_GROUP_FILTER` (default: `(&(objectClass=group)(member={{user_dn}}))`)
- `PAM_LDAP_GROUP_NAME_ATTR` (default: `cn`)
- `PAM_LDAP_GROUP_ROLE_MAPPING` (optional additive mapping: `ldapGroup=role1|role2,groupDN=role3`)
- `PAM_ALLOW_UNSAFE_MODE` (default: `false`; enables development-only unsafe settings outside `development`)

Deployment note:
- The API binary serves HTTP in this slice. Production must terminate HTTPS/TLS at an external reverse proxy or load balancer.
- Reference edge configuration is available at `deploy/nginx/pam-edge.conf`.

## Current HTTP Endpoints

- `GET /health/live`
- `GET /health/ready`
- `GET /version`
- `POST /auth/login`
- `POST /auth/logout`
- `GET /me`
- `GET /auth/ping` (authenticated)
- `GET /access/my` (authenticated; denied to read-only auditors)
- `GET /sessions/my` (authenticated)
- `GET /sessions/{sessionID}` (authenticated; owner/admin/auditor)
- `GET /sessions/{sessionID}/events` (authenticated; owner/admin/auditor)
- `GET /sessions/{sessionID}/replay` (authenticated; owner/admin/auditor; shell-first helper)
- `GET /sessions/{sessionID}/export/summary` (authenticated; owner/admin/auditor; JSON recap export)
- `GET /sessions/{sessionID}/export/transcript` (authenticated; owner/admin/auditor; shell text export)
- `POST /sessions/launch` (authenticated; denied to read-only auditors)
- `POST /sessions/{sessionID}/events` (authenticated; denied to read-only auditors)
- `GET /admin/ping` (admin only)
- `GET /admin/sessions` (admin/auditor)
- `GET /admin/sessions/export` (admin/auditor; CSV export)
- `GET /admin/sessions/active` (admin/auditor)
- `GET /admin/audit/recent` (admin/auditor)
- `GET /admin/audit/events` (admin/auditor; filterable search)
- `GET /admin/audit/events/{eventID}` (admin/auditor; detail view)
- `GET /admin/summary` (admin/auditor)

## First Managed Launch Path Test

1. Start API:

```bash
cd apps/api
export PAM_DB_URL='postgres://postgres:postgres@localhost:5432/pam?sslmode=disable'
export PAM_VAULT_KEY='replace-with-dev-key'
export PAM_LAUNCH_TOKEN_SECRET='replace-with-dev-launch-token-secret'
export PAM_LAUNCH_MATERIALIZE_TIMEOUT='45s'
export PAM_LAUNCH_SWEEP_INTERVAL='15s'
go run ./cmd/server
```

2. Login and keep cookie:

```bash
curl -i -c /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"admin123"}' \
  http://127.0.0.1:8080/auth/login
```

3. Find a linux asset id:

```bash
curl -s -b /tmp/pam.cookie http://127.0.0.1:8080/access/my
```

4. Launch shell:

```bash
curl -s -b /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"asset_id":"<linux_asset_uuid>","action":"shell"}' \
 http://127.0.0.1:8080/sessions/launch
```

5. Launch DBeaver (database asset):

```bash
curl -s -b /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"asset_id":"<database_asset_uuid>","action":"dbeaver"}' \
  http://127.0.0.1:8080/sessions/launch
```

6. Launch SFTP (linux VM asset):

```bash
curl -s -b /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"asset_id":"<linux_asset_uuid>","action":"sftp"}' \
  http://127.0.0.1:8080/sessions/launch
```

7. Launch Redis CLI (redis asset):

```bash
curl -s -b /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"asset_id":"<redis_asset_uuid>","action":"redis"}' \
  http://127.0.0.1:8080/sessions/launch
```

8. Record connector lifecycle events (typically done by UI):

```bash
curl -s -b /tmp/pam.cookie \
  -H 'content-type: application/json' \
  -d '{"event_type":"connector_launch_requested","metadata":{"connector_action":"dbeaver"}}' \
  http://127.0.0.1:8080/sessions/<session_uuid>/events
```

9. Connect SSH client to proxy with launch token:

```bash
ssh -o PreferredAuthentications=keyboard-interactive -p <proxy_port> <username>@<proxy_host>
```

Paste launch token when prompted. Password auth also works as first-pass fallback (use token as password).

## Current Limitations (First Pass)

- Supports `linux_vm + shell`, `linux_vm + sftp`, `database + dbeaver`, and `redis + redis` launch creation in this slice.
- DBeaver path now launches through an engine-specific PAM DB proxy endpoint (`postgres`, `mysql`, `mssql`) with query capture into `session_events` (`event_type=db_query`).
- Redis launch now targets a session-scoped PAM RESP proxy endpoint and captures commands into `session_events` (`event_type=redis_command`).
- Redis command audit payload uses argument summaries with value/script redaction for sensitive patterns (`AUTH`, `SET`/`MSET`/`HMSET`, `CONFIG SET`, `EVAL`, `ACL SETUSER`).
- SFTP path now launches through PAM SSH/SFTP relay using session launch token authentication and logs file operations (`event_type=file_operation`) in `session_events`.
- DBeaver launch payload no longer includes the DB password; connector connects to a session-scoped PAM proxy endpoint.
- Connector now returns richer launch diagnostics and DBeaver temp-material cleanup metadata to improve operator troubleshooting.
- Connector lifecycle metadata currently tracks:
  - `launch_created`
  - `connector_launch_requested`
  - `connector_launch_succeeded` / `connector_launch_failed`
  - `session_ended` / `session_failed` (proxy-driven lifecycle for SSH, SFTP, DBeaver DB proxies, and Redis proxy sessions)
- Session review helpers in this slice:
  - `GET /sessions/{id}` includes lifecycle summary (`started/ended/failed`, connector flags, event count, first/last event time)
  - `GET /sessions/{id}/events` returns ordered raw events plus decoded transcript hints for shell `data_in/data_out` events when payload is valid base64 text
  - `GET /sessions/{id}/replay` returns timed shell replay chunks with asciicast-v2-like tuples (`[offset, code, data]`) for input/output plus terminal resize events when captured
- Session recap/export helpers in this slice:
  - `GET /sessions/{id}/export/summary` returns session detail + event type counts + total event count
  - `GET /sessions/{id}/export/transcript` returns first-pass shell transcript text (`data_in`/`data_out` decode path)
  - `GET /admin/sessions/export` returns filterable CSV session summary rows for admin/auditor views
- Audit review helpers in this slice:
  - `GET /admin/audit/events` supports practical filters: `event_type`, `user_id`, `asset_id`, `session_id`, `action`, `from`, `to`, `limit`
  - `GET /admin/audit/events/{id}` returns event metadata payload plus linked actor/session/asset summaries
- Upstream credential type is `password` only in this slice.
- Upstream host key validation defaults to `known-hosts`; production-like deployments should pre-populate trusted host keys.
- `accept-new` and `insecure` SSH host-key modes are blocked outside `development` unless `PAM_ALLOW_UNSAFE_MODE=true`.
- `PAM_AUTH_COOKIE_SECURE=false`, `PAM_AUTH_COOKIE_SAMESITE=none`, and `PAM_LDAP_INSECURE_SKIP_VERIFY=true` are blocked outside `development` unless `PAM_ALLOW_UNSAFE_MODE=true`.
- Admin credential updates (`PUT /admin/assets/{assetID}/credentials/{credentialType}`) now write an explicit `audit_events` record (`event_type=admin_action`, `action=credential_upsert`).
- Session stream capture stores `data_in`/`data_out` chunks in `session_events` with base64 payload plus asciicast-v2-like timing tuples; terminal resizes are tracked as `terminal_resize`.
- Shell replay uses recorded timing and resize events but is still text-stream playback, not a full terminal state emulator (no CSI/stateful cursor fidelity yet).
- API request logging now propagates and emits `X-Request-Id`, and launch-to-proxy registration carries `request_id` into proxy logs for correlation.
- Launch tokens now also carry `request_id` so SSH/SFTP/Redis token-authenticated proxy activity can be correlated back to originating API request logs.
- Proxy services enforce configurable idle timeout + max session duration and perform graceful shutdown draining of active sessions before forced close on shutdown timeout.
- Credential usage is now audited (`audit_events.event_type=credential_usage`) for launch-time credential resolution and proxy upstream authentication stages.
- No asciicast format or compression yet.
- PostgreSQL proxy captures simple and extended query traffic, supports TLS negotiation on both client and upstream links, and supports upstream auth methods `cleartext`/`md5`/`SCRAM-SHA-256`.
- MySQL proxy captures `COM_QUERY` and common prepared flows (`COM_STMT_PREPARE`/`COM_STMT_EXECUTE`) with practical TLS support.
- MSSQL proxy captures SQL batch and common RPC prepared flows (`sp_prepare`/`sp_prepexec`/`sp_executesql`/`sp_execute`) with per-connection template caching when derivable.
- MSSQL TLS limitation in this slice: client->proxy TLS tunneling and upstream TDS-TLS tunneling are not implemented; use non-required TLS mode (for example `ssl_mode=disable`) for DBeaver MSSQL launches.
- Redis client-leg TLS limitation in this slice: connector `redis-cli` currently connects to the PAM Redis proxy endpoint without TLS (typically loopback/session endpoint). Upstream Redis TLS from PAM proxy to target is supported via asset metadata.
- SFTP relay captures practical operations (`upload_write`, `download_read`, `delete`, `rename`, `mkdir`, `rmdir`, `stat`, `list`) with path and size (when derivable). Remaining gap: operation-level success/failure and full protocol coverage for less-common SFTP extensions.

## Auth/RBAC Notes

- Auth mode is selected by `PAM_AUTH_PROVIDER_MODE`:
  - `local`: local provider only.
  - `ldap`: LDAP provider only.
  - `hybrid`: LDAP provider first, local fallback second.
- Passwords are stored as bcrypt hashes (`password_hash`), never plaintext.
- Authenticated API access uses server-controlled HTTP-only cookies.
- LDAP login maps users into local `users` rows (`auth_provider=ldap`) and updates basic profile attributes on login.
- LDAP diagnostics now differentiate user-not-found, invalid password, bind/search config issues, and TLS/connectivity issues in logs without leaking secrets.
- Optional LDAP group-to-role mapping is additive-only on login (`PAM_LDAP_GROUP_ROLE_MAPPING`); existing local roles are preserved.
- Group-role mapping keys can be either LDAP group names (`cn`) or full LDAP group DNs.
- Roles are stored in `roles` and `user_roles`, with baseline roles:
  - `admin`
  - `operator`
  - `auditor`
  - `user`
- Effective v1 authorization intent:
  - `admin`: full admin + mutation access
  - `auditor`: read-only review (`/admin/sessions*`, `/admin/audit*`, `/admin/summary`, and owner/admin/auditor session detail paths); no launch, session-event, or access-assignment actions (`/access/my` denied for read-only auditors)
  - `operator` / `user`: assigned-access launch paths + own-session visibility, no admin surfaces
- LDAP provider is production-usable for Samba AD auth/login mapping in this slice; full directory sync/reconciliation is still deferred.

## Samba AD Example Configuration

```env
PAM_AUTH_PROVIDER_MODE=hybrid
PAM_LDAP_HOST=dc1.corp.example.com
PAM_LDAP_PORT=636
PAM_LDAP_USE_TLS=true
PAM_LDAP_BASE_DN=DC=corp,DC=example,DC=com
PAM_LDAP_BIND_DN=CN=pam-reader,OU=Service Accounts,DC=corp,DC=example,DC=com
PAM_LDAP_BIND_PASSWORD=replace-me
PAM_LDAP_USERNAME_ATTR=sAMAccountName
PAM_LDAP_USER_FILTER=(&(objectCategory=person)(objectClass=user)({{username_attr}}={{username}}))
PAM_LDAP_DISPLAY_NAME_ATTR=displayName
PAM_LDAP_EMAIL_ATTR=mail
PAM_LDAP_GROUP_BASE_DN=OU=Groups,DC=corp,DC=example,DC=com
PAM_LDAP_GROUP_FILTER=(&(objectClass=group)(member={{user_dn}}))
PAM_LDAP_GROUP_NAME_ATTR=cn
PAM_LDAP_GROUP_ROLE_MAPPING=PAM Operators=operator,CN=PAM Admins,OU=Groups,DC=corp,DC=example,DC=com=admin|auditor
```

- Typical DN/base DN examples:
  - Domain base DN: `DC=corp,DC=example,DC=com`
  - Users OU base DN: `OU=Users,DC=corp,DC=example,DC=com`
  - Groups OU base DN: `OU=Groups,DC=corp,DC=example,DC=com`

## Deferred Intentionally

- Full LDAP sync/reconciliation jobs
- LDAP group membership removal/reconciliation (current group mapping is additive-only)
- proxy behavior beyond first SSH shell path
- Connector launch behavior
- TailAdmin/UI integration
- admin CRUD APIs for assets/credentials/access grants
