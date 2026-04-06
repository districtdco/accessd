# apps/api — PAM Backend

Go backend for PAM v1. Current development mode includes local auth + cookie sessions + RBAC foundation, while LDAP and proxy protocols remain deferred.

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
- `internal/handlers`: health/version + auth/session endpoints
- `internal/auth`: local provider auth, password hashing, cookie-session handling, RBAC middleware, dev bootstrap
- `internal/{assets,access,credentials,sessions,audit}`: service skeleton packages for upcoming feature slices
- `migrations/`: ordered SQL migration files (`000001_*.up.sql`, `000002_*.up.sql`)

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
```

## Configuration

Required:

- `PAM_DB_URL`: PostgreSQL connection URL

Optional:

- `PAM_APP_NAME` (default: `pam-api`)
- `PAM_ENV` (default: `development`)
- `PAM_HTTP_ADDR` (default: `:8080`)
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
- `PAM_AUTH_COOKIE_SECURE` (default: `false`; set `true` behind HTTPS)
- `PAM_DEV_ADMIN_USERNAME` (default: `admin`)
- `PAM_DEV_ADMIN_PASSWORD` (default: `admin123`)
- `PAM_DEV_ADMIN_EMAIL` (default: `admin@pam.local`)
- `PAM_DEV_ADMIN_NAME` (default: `PAM Administrator`)

## Current HTTP Endpoints

- `GET /health/live`
- `GET /health/ready`
- `GET /version`
- `POST /auth/login`
- `POST /auth/logout`
- `GET /me`
- `GET /auth/ping` (authenticated)
- `GET /admin/ping` (admin only)

## Auth/RBAC Notes

- Development mode uses a local auth provider backed by PostgreSQL users.
- Passwords are stored as bcrypt hashes (`password_hash`), never plaintext.
- Authenticated API access uses server-controlled HTTP-only cookies.
- Roles are stored in `roles` and `user_roles`, with baseline roles:
  - `admin`
  - `operator`
  - `auditor`
  - `user`
- LDAP is intentionally deferred; provider boundary is in place so LDAP can be added later without changing authorization/session/policy internals.

## Deferred Intentionally

- LDAP provider implementation
- SSH/DB/SFTP/Redis proxy behavior
- Connector launch behavior
- TailAdmin/UI integration
- asset/access business APIs and policy CRUD
