# apps/api — PAM Backend

Go backend serving the HTTP API, proxy layer, credential vault, and audit engine as a single binary.

## Modules

| Package | Responsibility |
|---------|---------------|
| `cmd/server` | Entry point — starts HTTP server, SSH proxy, DB broker, SFTP relay, Redis proxy |
| `internal/auth` | LDAP bind authentication, JWT issuance, auth middleware, minimal LDAP attribute sync |
| `internal/user` | User, group, role CRUD and membership |
| `internal/asset` | Asset inventory CRUD (SSH, database, Redis, SFTP) |
| `internal/policy` | Access policy assignment and evaluation |
| `internal/vault` | Credential storage with AES-256-GCM encryption |
| `internal/proxy/ssh` | SSH proxy server with keyboard-interactive auth and session recording |
| `internal/proxy/db` | TCP proxy for brokered database connections |
| `internal/proxy/sftp` | SFTP relay server |
| `internal/proxy/redis` | Redis TCP proxy |
| `internal/audit` | Audit event writer, session recording storage |
| `internal/api` | HTTP handlers, middleware, route registration |
| `internal/db` | PostgreSQL connection, query layer, migration runner |
| `migrations/` | SQL migration files |

## Build

```bash
# From repo root
make build-api

# Or directly
cd apps/api && go build -o bin/pam-server ./cmd/server
```
