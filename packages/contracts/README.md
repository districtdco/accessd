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

## Current Endpoint Groups

- Health/version: `GET /health/live`, `GET /health/ready`, `GET /version`
- Auth/account: `POST /auth/login`, `POST /auth/logout`, `PUT /auth/password`, `GET /me`, `GET /auth/ping`
- Access/session launch: `GET /access/my`, `POST /sessions/launch`, `POST /sessions/{id}/events`
- Session review/export: `GET /sessions/my`, `GET /sessions/{id}`, `GET /sessions/{id}/events`, `GET /sessions/{id}/replay`, `GET /sessions/{id}/export/summary`, `GET /sessions/{id}/export/transcript`
- Connector integration: `GET /connector/releases/latest`, `GET /connector/releases`, `POST /connector/token/verify`, `POST /connector/bootstrap/verify`, `POST /connector/bootstrap/issue`
- Admin users/roles/groups: `/admin/users*`, `/admin/roles`, `/admin/groups*`
- Admin assets/grants: `/admin/assets*`, `/admin/users/{id}/grants*`, `/admin/users/{id}/effective-access`
- Admin LDAP: `/admin/ldap/settings`, `/admin/ldap/test`, `/admin/ldap/sync`, `/admin/ldap/sync-runs`
- Admin sessions/audit/summary: `/admin/sessions*`, `/admin/audit/*`, `/admin/summary`
