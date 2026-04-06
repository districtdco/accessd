# packages/contracts — Shared API Contracts

Shared API contract definitions used by the backend (apps/api), frontend (apps/ui), and connector (apps/connector).

## Contents

| File | Purpose |
|------|---------|
| `api.yaml` | OpenAPI 3.1 specification — the source of truth for all API endpoints |
| `types/` | Shared type definitions (generated or manually maintained) |

## Contract Workflow

1. API changes start here — update `api.yaml` first
2. Backend implements against the spec
3. Frontend generates or updates TypeScript types from the spec
4. Connector uses the same endpoint contracts

## Planned Endpoint Groups

- `GET /health/live` — liveness
- `GET /health/ready` — readiness
- `GET /version` — build/version metadata
- `POST /auth/login` — Authentication (provider-backed: `local`, `ldap`, `hybrid`)
- `POST /auth/refresh` — Token refresh
- `POST /auth/logout` — Logout
- `GET /me` — Current authenticated user (session cookie)
- `GET /access/my` — Current user's resolved access points and actions
- `GET /users`, `POST /users`, etc. — User management
- `GET /groups`, `POST /groups`, etc. — Group management
- `GET /assets`, `GET /assets/mine` — Asset inventory
- `GET /policies`, `POST /policies` — Access policy management
- `POST /sessions/launch` — Session launch (returns launch payload)
- `GET /sessions/my` — Current-user session history
- `GET /sessions/{id}` — Session detail + lifecycle summary
- `GET /sessions/{id}/events` — Ordered event timeline
- `GET /sessions/{id}/replay` — First-pass normalized shell replay chunks
- `GET /sessions/{id}/export/summary` — Session recap JSON export
- `GET /sessions/{id}/export/transcript` — Shell transcript text export
- `GET /admin/sessions/export` — Filtered session history CSV export
- `GET /admin/audit/events` — Filterable audit search for admin/auditor
- `GET /admin/audit/events/{event_id}` — Audit event detail + correlation metadata
- `GET /audit/events` — Audit event log
