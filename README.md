# PAM — Privileged Access Management

A lean PAM system that centralizes access to infrastructure assets. Users authenticate through PAM, and all infrastructure access is brokered and audited so users do not receive raw target credentials.

## Architecture

- Monorepo with modular monolith backend
- Backend: Go (`net/http`, `pgx/pgxpool`, SQL-first)
- UI: React + TailAdmin (planned)
- Connector: thin desktop launcher (planned)
- Data store: PostgreSQL
- Auth source: provider-based (`local` for development/POC, LDAP planned next)

See [PLAN.md](PLAN.md) for full architecture and [CHECKLIST.md](CHECKLIST.md) for progress.

## Repository Structure

```text
apps/
  api/          Go backend (HTTP API + DB + migrations)
  ui/           React frontend scaffold (feature work pending)
  connector/    Go connector scaffold (launch behavior pending)
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

## Auth In Development Mode

- Local auth provider is active for development/POC.
- Passwords are stored as bcrypt hashes in PostgreSQL.
- API auth uses server-controlled HTTP-only session cookies.
- RBAC is active in backend middleware with roles: `admin`, `operator`, `auditor`, `user`.
- Default development admin is bootstrapped idempotently from environment settings.
- LDAP is intentionally deferred and will be added behind the existing auth-provider boundary.

## Development Setup

### Prerequisites

- Go 1.22+
- Node.js 20+ / npm (for future UI work)
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
```

## Environment Variables (Backend)

| Variable | Purpose | Required |
|----------|---------|----------|
| `PAM_DB_URL` | PostgreSQL connection string | Yes |
| `PAM_HTTP_ADDR` | HTTP bind address (default `:8080`) | No |
| `PAM_MIGRATIONS_DIR` | Migration directory (default `migrations`) | No |
| `PAM_VERSION` / `PAM_COMMIT` / `PAM_BUILT_AT` | Version endpoint metadata | No |
| `PAM_AUTH_COOKIE_NAME` | Session cookie name (default `pam_session`) | No |
| `PAM_AUTH_SESSION_TTL` | Session duration (default `12h`) | No |
| `PAM_DEV_ADMIN_USERNAME` / `PAM_DEV_ADMIN_PASSWORD` | Default local admin bootstrap credentials | No |

## Deferred for Later Slices

- LDAP auth provider integration (deferred; local auth is the current development mode)
- proxy protocols (SSH/DB/SFTP/Redis)
- credential encryption/decryption logic (table exists, service behavior pending)
- connector launch/session behavior
- TailAdmin UI integration

## License

Proprietary — DistrictD
