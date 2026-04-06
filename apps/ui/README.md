# apps/ui — PAM Frontend

Minimal React + Vite frontend for integrated shell + SFTP + DBeaver + Redis brokered launch flows.

## Implemented Routes

| Route | Purpose |
|------|---------|
| `/login` | Local dev login (`POST /auth/login`) |
| `/` | Auth-protected My Access table (`GET /me`, `GET /access/my`) |
| `/sessions` | My session history (`GET /sessions/my`) |
| `/sessions/:sessionID` | Session detail/review (metadata + timeline + shell transcript/replay helper) |
| `/admin/dashboard` | Admin/auditor recap view |
| `/admin/sessions` | Admin/auditor session history |
| `/admin/audit/events` | Admin/auditor audit search/filter page |
| `/admin/audit/events/:eventID` | Admin/auditor audit event detail view |

## Current Behavior

- Cookie session model is used as-is from backend (`credentials: include`).
- App boot fetches `/me`; unauthenticated users are redirected to `/login`.
- Logout calls `POST /auth/logout` and returns to login route.
- My Access table renders `asset_name`, `asset_type`, `endpoint`, `allowed_actions`.
- `Shell` button is shown only for `linux_vm` assets with `shell` action.
- `SFTP` button is shown only for `linux_vm` assets with `sftp` action.
- `DBeaver` button is shown only for `database` assets with `dbeaver` action.
- `Redis CLI` button is shown only for `redis` assets with `redis` action.
- Launch flow (both actions):
  1. `POST /sessions/launch` with `asset_id` and `action` (`shell`, `sftp`, `dbeaver`, or `redis`)
  2. `POST /sessions/{session_id}/events` with `connector_launch_requested`
  3. Forward launch payload to connector:
     - shell: `POST /connector/launch/shell`
     - sftp: `POST /connector/launch/sftp`
     - DBeaver: `POST /connector/launch/dbeaver`
     - redis: `POST /connector/launch/redis`
  4. Record outcome via `POST /sessions/{session_id}/events`:
     - `connector_launch_succeeded`
     - `connector_launch_failed`
- Session review flow:
  1. `GET /sessions/{session_id}` for metadata + lifecycle summary
  2. `GET /sessions/{session_id}/events` for ordered timeline events
  3. `GET /sessions/{session_id}/replay` for first-pass shell replay chunks
  4. Export actions:
     - `GET /sessions/{session_id}/export/summary` (JSON)
     - `GET /sessions/{session_id}/export/transcript` (shell text)
     - `GET /admin/sessions/export` (CSV with current admin-session filters)

## Session Review Behavior

- Shell session detail:
  - summary metadata + lifecycle
  - transcript view (search + in/out filter)
  - replay view (play/pause/reset, speed, slider position) from normalized replay chunks
  - timeline view of full session events
- DBeaver session detail:
  - metadata-focused review
  - connector launch lifecycle table (`requested/succeeded/failed`)
  - final outcome from lifecycle events/status
  - no transcript/replay emulation
- Redis session detail:
  - metadata-focused review
  - connector launch lifecycle table (`requested/succeeded/failed`)
  - no command-stream capture in this slice
- SFTP session detail:
  - metadata-focused review
  - connector launch lifecycle table (`requested/succeeded/failed`)
  - no per-file activity replay in this slice
- Replay caveat:
  - replay is approximate text/event playback only; terminal-perfect rendering is intentionally deferred.

## Audit Review Behavior

- Audit list page supports practical filters:
  - `event_type`, `user_id`, `asset_id`, `session_id`, `action`, `from`, `to`, `limit`
- Audit detail page shows:
  - core event fields (`event_type`, `action`, `outcome`, timestamp)
  - related user/asset/session links where available
  - raw metadata JSON for operator/auditor inspection

## Connector Handoff Payload

Frontend sends this JSON to the connector:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "dev-vm-01",
  "launch": {
    "proxy_host": "127.0.0.1",
    "proxy_port": 2222,
    "username": "pam",
    "token": "short-lived-launch-token",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

For DBeaver launches the frontend forwards:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "postgres-app",
  "launch": {
    "engine": "postgres",
    "host": "10.0.20.21",
    "port": 5432,
    "database": "app",
    "username": "app_user",
    "password": "resolved-db-password",
    "ssl_mode": "disable",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

Current DBeaver limitation: this first slice forwards DB credential material to local connector for temporary launch setup; deep DB traffic auditing is intentionally deferred.

Current Redis limitation: this slice launches local `redis-cli` with managed handoff metadata only; deep Redis command/protocol auditing is deferred.
Current SFTP limitation: this slice launches WinSCP/FileZilla with managed handoff metadata only; deep file-operation auditing is deferred.

## Development

```bash
# install deps once
cd apps/ui
npm install

# run dev server
npm run dev
```

Vite dev proxy config:

- `/api` → `http://localhost:8080`
- `/connector` → `http://127.0.0.1:9494`

Optional:

- `VITE_CONNECTOR_BASE` (default `/connector`) for connector handoff base URL.
