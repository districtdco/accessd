# PAM — Privileged Access Management

A lean PAM system that centralizes access to infrastructure assets. Users authenticate through PAM, and all infrastructure access is brokered and audited so users do not receive raw target credentials.

## Architecture

- Monorepo with modular monolith backend
- Backend: Go (`net/http`, `pgx/pgxpool`, SQL-first)
- UI: React (minimal shell + SFTP + DBeaver + Redis flow implemented; TailAdmin deferred)
- Connector: thin local launcher (shell + SFTP + DBeaver + Redis CLI)
- Data store: PostgreSQL
- Auth source: provider-based (`local`, `ldap`, `hybrid`)

See [PLAN.md](PLAN.md) for full architecture and [CHECKLIST.md](CHECKLIST.md) for progress.

## CI

GitHub Actions workflow: `.github/workflows/ci.yml`

- OpenAPI contract validation (`packages/contracts/api.yaml`)
- Go test + build for:
  - `apps/api`
  - `apps/connector`
- UI install + build (`apps/ui`)

## Repository Structure

```text
apps/
  api/          Go backend (HTTP API + DB + migrations)
  ui/           React frontend for login + my access + shell handoff
  connector/    Go local connector (`/launch/shell`, `/launch/sftp`, `/launch/dbeaver`, `/launch/redis` -> native client launch)
packages/
  contracts/    Shared API contracts (OpenAPI)
scripts/        Dev tooling and helper scripts
deploy/         Deployment-oriented templates/docs
```

## Current Backend Baseline (Implemented)

- environment-driven config loading
- structured startup with `slog`
- PostgreSQL pool init with `pgxpool`
- startup DB connectivity verification
- in-repo SQL migration runner (`migrate up`, `migrate status`)
- graceful HTTP shutdown
- endpoints:
  - `GET /health/live`
  - `GET /health/ready`
  - `GET /version`
  - `POST /auth/login`
  - `POST /auth/logout`
  - `GET /me`
  - `GET /auth/ping` (authenticated)
  - `GET /admin/ping` (admin-only)
  - `GET /access/my` (authenticated "my access" control-plane view)
  - `POST /sessions/launch` (authenticated brokered launch; supports `linux_vm/shell`, `linux_vm/sftp`, `database/dbeaver`, and `redis/redis`)
  - `POST /sessions/{sessionID}/events` (authenticated connector launch lifecycle metadata)
- core control-plane services:
  - assets service (`linux_vm`, `database`, `redis`) with pgx-backed upsert/list/get
  - credentials service with AES-256-GCM encryption at rest using `PAM_VAULT_KEY`
  - access grant service (allowed assets + action checks per user)
  - sessions service with short-lived HMAC launch tokens
  - SSH proxy service (`golang.org/x/crypto/ssh`) for first proxied shell path
- development bootstrap seeds assets, encrypted credentials, and access grants for default admin (idempotent in `development` env)

## Production TLS Posture

- API listener (`PAM_HTTP_ADDR`) is HTTP in this slice by design.
- Production must terminate TLS at an edge reverse proxy/load balancer and forward traffic to private PAM listeners.
- A reference nginx edge config is included at `deploy/nginx/pam-edge.conf`.

## Auth Providers

- Authentication mode is controlled by `PAM_AUTH_PROVIDER_MODE`:
  - `local`: local PostgreSQL-backed auth only
  - `ldap`: LDAP auth only
  - `hybrid`: LDAP first, then local fallback (recommended for development/bootstrap)
- Passwords are stored as bcrypt hashes in PostgreSQL.
- API auth uses server-controlled HTTP-only session cookies.
- RBAC is active in backend middleware with roles: `admin`, `operator`, `auditor`, `user`.
- RBAC v1 intent:
  - `admin`: full admin/mutation access
  - `auditor`: read-only review surfaces (admin sessions/audit/summary + broad session visibility), no launch/mutation actions (`/access/my` denied for read-only auditors)
  - `operator` and `user`: assigned access workflows + own-session visibility, no admin surfaces
- Default development admin is bootstrapped idempotently from environment settings.
- LDAP-authenticated users are mapped into local PAM users on successful login (create/update by username, `auth_provider=ldap`).
- LDAP defaults are tuned for Samba Active Directory (`sAMAccountName`, AD-friendly filters/attrs), with local/hybrid fallback behavior unchanged.
- Optional LDAP group-to-role mapping is available via env configuration (additive-only role assignment on login; existing local roles are preserved). Mapping keys can be either group `cn` or full group DN.

## Development Setup

### Prerequisites

- Go 1.22+
- Node.js 20+ / npm
- PostgreSQL 16+
- Docker & Docker Compose (local dev services)

### Quick Start

```bash
# Start local dependencies (PostgreSQL)
docker-compose up -d

# Run backend from apps/api directory
cd apps/api
export PAM_DB_URL='postgres://postgres:postgres@localhost:5432/pam?sslmode=disable'
go run ./cmd/server
```

### Backend Commands

```bash
cd apps/api

# Run server (also applies pending migrations)
go run ./cmd/server server

# Run migrations only
go run ./cmd/server migrate up

# Show migration status
go run ./cmd/server migrate status

# Run migrations + dev auth bootstrap only
go run ./cmd/server bootstrap

# Run focused backend integration tests (requires dedicated test DB)
export PAM_TEST_DB_URL='postgres://postgres:postgres@localhost:5432/pam_test?sslmode=disable'
go test ./internal/integration -count=1
```

### Smoke Check

```bash
# Requires running API and jq installed
./scripts/smoke_api.sh

# Equivalent make target
make smoke-api
```

## Environment Variables (Backend)

| Variable | Purpose | Required |
|----------|---------|----------|
| `PAM_DB_URL` | PostgreSQL connection string | Yes |
| `PAM_HTTP_ADDR` | HTTP bind address (default `:8080`) | No |
| `PAM_CORS_ALLOWED_ORIGINS` | Comma-separated API CORS allowlist (development default: `http://localhost:3000,http://127.0.0.1:3000`) | No |
| `PAM_CONFIG_FILE` | Optional env file path (`KEY=VALUE`) loaded before validation; explicit env vars still win | No |
| `PAM_MIGRATIONS_DIR` | Migration directory (default `migrations`) | No |
| `PAM_VERSION` / `PAM_COMMIT` / `PAM_BUILT_AT` | Version endpoint metadata | No |
| `PAM_AUTH_COOKIE_NAME` | Session cookie name (default `pam_session`) | No |
| `PAM_AUTH_COOKIE_SAMESITE` | Cookie SameSite policy (`lax`, `strict`, `none`; default `lax` in development, `strict` otherwise) | No |
| `PAM_AUTH_SESSION_TTL` | Session duration (default `12h`) | No |
| `PAM_AUTH_PROVIDER_MODE` | Auth mode: `local`, `ldap`, or `hybrid` (default `local`) | No |
| `PAM_VAULT_KEY` | Master key for credential encryption (base64-encoded 32-byte key required outside development unless unsafe override enabled) | Yes |
| `PAM_VAULT_KEY_ID` | Logical key id stored with encrypted credentials (default `v1`) | No |
| `PAM_LAUNCH_TOKEN_SECRET` | HMAC secret for short-lived launch tokens | Yes |
| `PAM_LAUNCH_TOKEN_TTL` | Launch token TTL (default `2m`) | No |
| `PAM_SSH_PROXY_ADDR` | SSH proxy listen address (default `:2222`) | No |
| `PAM_SSH_PROXY_PUBLIC_HOST` | Proxy host returned by launch API (default `127.0.0.1`) | No |
| `PAM_SSH_PROXY_PUBLIC_PORT` | Proxy port returned by launch API (default `2222`) | No |
| `PAM_SSH_PROXY_USERNAME` | SSH username for proxy login (default `pam`) | No |
| `PAM_DEV_ADMIN_USERNAME` / `PAM_DEV_ADMIN_PASSWORD` | Default local admin bootstrap credentials | No |
| `PAM_LDAP_URL` | LDAP/LDAPS URL (optional; overrides host/port) | No |
| `PAM_LDAP_HOST` / `PAM_LDAP_PORT` | LDAP host/port (defaults `127.0.0.1:389`) | No |
| `PAM_LDAP_BASE_DN` | LDAP user search base DN (required for `ldap`/`hybrid`) | Conditional |
| `PAM_LDAP_BIND_DN` / `PAM_LDAP_BIND_PASSWORD` | Optional LDAP service account for search | No |
| `PAM_LDAP_USER_FILTER` | User search filter template (supports `{{username}}`, `{{username_attr}}`; default AD/Samba-friendly user filter) | No |
| `PAM_LDAP_USERNAME_ATTR` | LDAP username attribute (default `sAMAccountName`) | No |
| `PAM_LDAP_DISPLAY_NAME_ATTR` | LDAP display name attribute (default `displayName`) | No |
| `PAM_LDAP_EMAIL_ATTR` | LDAP email attribute (default `mail`) | No |
| `PAM_LDAP_USE_TLS` / `PAM_LDAP_STARTTLS` | TLS mode toggles (`ldaps` or StartTLS; mutually exclusive) | No |
| `PAM_LDAP_INSECURE_SKIP_VERIFY` | Skip TLS certificate verification (dev only) | No |
| `PAM_LDAP_GROUP_BASE_DN` | Optional group search base DN (defaults to `PAM_LDAP_BASE_DN`) | No |
| `PAM_LDAP_GROUP_FILTER` | Optional group search filter template (supports `{{username}}`, `{{user_dn}}`; default AD/Samba-friendly group filter) | No |
| `PAM_LDAP_GROUP_NAME_ATTR` | Group name attribute (default `cn`) | No |
| `PAM_LDAP_GROUP_ROLE_MAPPING` | Optional mapping (additive): `ldapGroup=role1|role2,groupDN=role3` | No |
| `PAM_ALLOW_UNSAFE_MODE` | Allows unsafe development toggles outside `development` (default `false`) | No |

## Samba AD LDAP Example

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

- Base DN examples:
  - Domain base: `DC=corp,DC=example,DC=com`
  - Users OU: `OU=Users,DC=corp,DC=example,DC=com`
  - Groups OU: `OU=Groups,DC=corp,DC=example,DC=com`

## First Managed Launch Flow

1. Start backend API (`apps/api`) with required env vars:
   - `PAM_DB_URL`
   - `PAM_VAULT_KEY`
   - `PAM_LAUNCH_TOKEN_SECRET`
2. Start connector:

```bash
cd apps/connector
go run ./cmd/connector
```

3. Start UI:

```bash
cd apps/ui
npm install
npm run dev
```

4. Open `http://localhost:3000/login` and sign in (default dev: `admin` / `admin123`).
5. UI loads `/me` and `/access/my`, then renders access table.
6. Click `Shell`/`SFTP` (linux VM), `DBeaver` (database), or `Redis CLI` (redis asset) for an allowed action.
7. UI calls `POST /sessions/launch`, records `connector_launch_requested`, then forwards launch payload:
   - shell: `POST /launch/shell`
   - SFTP: `POST /launch/sftp`
   - DBeaver: `POST /launch/dbeaver`
   - Redis: `POST /launch/redis`
8. Connector opens native client:
   - macOS/Linux: terminal + `ssh`
   - macOS/Linux: FileZilla for SFTP
   - Windows: PuTTY
   - Windows: WinSCP for SFTP
   - DBeaver: local DBeaver app with `-con` connection spec
9. UI records connector outcome event:
   - `connector_launch_succeeded`
   - `connector_launch_failed`
10. Complete SSH auth with launch token prompt (shell path) or continue in DBeaver (DB path).

## Current Launch Slice Limitations

- DB launches are DBeaver-only (`database` + `action=dbeaver`), but statement-level `db_query` event capture is implemented for Postgres/MySQL/MSSQL proxies.
- Redis launches are connector-managed `redis-cli` sessions with `redis_command` event capture; client-side terminal replay is not implemented.
- SFTP launches are connector-managed FileZilla/WinSCP sessions with server-side `file_operation` capture; full protocol-extension coverage is still partial.
- SSH replay is transcript/timeline replay from event streams, not terminal-perfect emulation and not video replay.
- MSSQL TLS tunnel mode remains limited in this slice (full client<->proxy TDS TLS tunnel support is not implemented).
- Redis client-leg TLS to PAM proxy is not implemented in this slice (connector typically talks to loopback/session endpoint).
- Host key verification defaults to `known-hosts`; `accept-new`/`insecure` are development-oriented and blocked outside development unless `PAM_ALLOW_UNSAFE_MODE=true`.
- Token entry for shell remains user-driven (manual paste at prompt).

## Deployment Target (Linux VM + systemd)

Deployment assets are in `deploy/`:

- `deploy/systemd/pam-api.service`
- `deploy/systemd/pam-connector.service` (optional)
- `deploy/env/pam-api.env.example`
- `deploy/env/pam-connector.env.example`

The API unit runs migrations in `ExecStartPre` so migration failures block startup.
Production deployment must terminate HTTPS/TLS at an external reverse proxy/load balancer in front of PAM API and connector endpoints. The API binary in this slice serves HTTP only.

See [deploy/README.md](deploy/README.md) for step-by-step setup.

## Deferred for Later Slices

- Full LDAP sync engine/background sync jobs
- LDAP group membership reconciliation/removal logic (current mapping is additive-only on login)
- proxy protocols beyond first SSH shell slice (DB/SFTP/Redis, richer SSH features)
- connector launch flows beyond shell/SFTP/DBeaver/Redis
- TailAdmin UI integration and full design system

## License

Proprietary — DistrictD
