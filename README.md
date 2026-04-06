# PAM — Privileged Access Management

A lean PAM system that centralizes access to infrastructure assets. Users authenticate via LDAP, and all connections to servers, databases, file transfer endpoints, and Redis instances are brokered and audited through PAM. Users never receive raw target credentials.

## Architecture

- **Monorepo** with modular monolith backend
- **Backend**: Go (single binary serving HTTP API + SSH/DB/SFTP/Redis proxies)
- **UI**: React + TailAdmin (Vite)
- **Connector**: Thin desktop launcher (Go, cross-platform)
- **Data store**: PostgreSQL
- **Auth**: LDAP

See [PLAN.md](PLAN.md) for full architecture and design decisions.
See [CHECKLIST.md](CHECKLIST.md) for build progress.

## Repository Structure

```
apps/
  api/          Go backend — HTTP API, proxy layer, audit, vault
  ui/           React + TailAdmin — user and admin interface
  connector/    Desktop launcher — spawns approved clients for proxied sessions
packages/
  contracts/    Shared API contracts (OpenAPI spec, type definitions)
scripts/        Dev tooling and helper scripts
deploy/         Systemd unit files and production config templates
```

## Development Setup

### Prerequisites

- Go 1.22+
- Node.js 20+ / npm
- PostgreSQL 16+
- Docker & Docker Compose (for local dev services)

### Quick Start

```bash
# Start dev services (PostgreSQL, OpenLDAP)
docker-compose up -d

# Run backend
make dev-api

# Run UI dev server
make dev-ui

# Run all tests
make test
```

### Environment Variables

| Variable | Purpose | Required |
|----------|---------|----------|
| `PAM_VAULT_KEY` | AES-256-GCM master key for credential encryption (temporary v1 approach) | Yes |
| `PAM_DB_URL` | PostgreSQL connection string | Yes |
| `PAM_LDAP_URL` | LDAP server URL (ldaps:// or ldap:// with StartTLS) | Yes |
| `PAM_JWT_SECRET` | JWT signing secret | Yes |
| `PAM_SSH_HOST_KEY` | Path to SSH host key for PAM's SSH proxy | Yes |

## Deployment

v1 production target: **systemd on Linux VM**. Docker Compose is for local development only.

See `deploy/` for systemd unit files and production configuration templates.

## License

Proprietary — DistrictD
