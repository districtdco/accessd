# apps/connector — PAM Desktop Launcher

Thin local connector process for brokered launch flows: shell, SFTP, DBeaver, and Redis CLI.

## What the connector does NOT do

- Enforce PAM policy (backend remains trust boundary)
- Call PAM auth APIs directly in this slice
- Store tokens or credentials persistently
- Replay or deeply inspect client-side file activity in this step

## Connector Trust Model

When `PAM_CONNECTOR_SECRET` is set (same value on both API and connector):
- Every launch request must include a `connector_token` field signed by the backend
- The connector verifies the HMAC signature, checks session_id matches, and rejects expired tokens
- Unsigned or invalid requests are rejected with HTTP 403

When not set, verification is skipped (suitable for development only).

## Local HTTP API (this slice)

- `GET /healthz` → connector liveness
- `POST /launch/shell` → receive shell launch payload and spawn local client
- `POST /launch/dbeaver` → receive DBeaver launch payload and spawn local DBeaver process
- `POST /launch/redis` → receive Redis launch payload and spawn local `redis-cli` in terminal
- `POST /launch/sftp` → receive SFTP launch payload and spawn WinSCP/FileZilla
- launch failures return structured `error` with optional `code` and `hint` for faster troubleshooting
- by default, connector accepts only loopback requests; remote callers are rejected unless explicitly enabled

Expected request shape:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "dev-vm-01",
  "connector_token": "base64-body.base64-sig",
  "launch": {
    "proxy_host": "127.0.0.1",
    "proxy_port": 2222,
    "username": "pam",
    "token": "short-lived-launch-token",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

DBeaver request shape:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "postgres-app",
  "connector_token": "base64-body.base64-sig",
  "launch": {
    "engine": "postgres",
    "host": "pam.example.internal",
    "port": 45432,
    "database": "app",
    "username": "app_user",
    "ssl_mode": "disable",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

Redis request shape:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "redis-cache",
  "connector_token": "base64-body.base64-sig",
  "launch": {
    "redis_host": "pam.example.internal",
    "redis_port": 46379,
    "redis_username": "default",
    "redis_password": "short-lived-launch-token",
    "redis_database": 0,
    "redis_tls": false,
    "redis_insecure_skip_verify_tls": false,
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

SFTP request shape:

```json
{
  "session_id": "uuid",
  "asset_id": "uuid",
  "asset_name": "linux-app-01",
  "connector_token": "base64-body.base64-sig",
  "launch": {
    "host": "127.0.0.1",
    "port": 2222,
    "username": "pam",
    "password": "short-lived-launch-token",
    "path": "/home/ubuntu",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

## OS Launch Behavior

- macOS: opens `Terminal.app` and runs `ssh -o PreferredAuthentications=keyboard-interactive,password ...`
- Linux: tries `x-terminal-emulator`, `gnome-terminal`, `konsole`, then `xfce4-terminal`, each running the same SSH command
- Windows: launches PuTTY (`putty -ssh <username>@<proxy_host> -P <proxy_port>`)
- DBeaver launch behavior:
  - macOS: `PAM_CONNECTOR_DBEAVER_PATH` (if set), else `open -a DBeaver`, then common app bundle paths, then `dbeaver`
  - Linux: `PAM_CONNECTOR_DBEAVER_PATH` (if set), else `dbeaver`, then `dbeaver-ce`
  - Windows: `PAM_CONNECTOR_DBEAVER_PATH` (if set), else `cmd /C start "" dbeaver -con <spec>`, then `dbeaver.exe`
- SFTP launch behavior:
  - macOS/Linux: `PAM_CONNECTOR_FILEZILLA_PATH` (if set), else FileZilla in PATH/common macOS app paths
  - Windows: `PAM_CONNECTOR_WINSCP_PATH` (if set), else WinSCP in PATH/common install paths
- Redis launch behavior:
  - macOS/Linux/Windows: `PAM_CONNECTOR_REDIS_CLI_PATH` (if set), else `redis-cli` from PATH/common install paths, then launch inside a local terminal

In this first pass, the token is displayed in the launched terminal flow so the user can paste it at prompt. Connector also attempts best-effort clipboard copy (`pbcopy`, `wl-copy`/`xclip`, or `Set-Clipboard`) to reduce manual friction. PuTTY automation for token entry is intentionally deferred.

For DBeaver in this first pass, connector creates a temporary local manifest file under OS temp directory (`pam-dbeaver-launch-*`) and launches DBeaver using CLI connection spec. The connector auto-cleans temp launch directories after a TTL (`PAM_CONNECTOR_DBEAVER_TEMP_TTL`, default `15m`) and performs stale cleanup on startup.
The manifest intentionally omits plaintext DB password; it records non-sensitive launch metadata only.
Connector launch diagnostics also redact sensitive values (for example `password=` fields, SFTP URL passwords, and `REDISCLI_AUTH`) before returning operator-visible error details.
Launch requests now fail fast with structured codes for common local issues (`*_not_installed`, `invalid_configured_path`, `terminal_not_installed`, `terminal_launch_failed`, etc.) instead of optimistic acceptance.

For Redis in this slice, connector launches local `redis-cli` in a new terminal window against a PAM session-scoped Redis proxy endpoint. `REDISCLI_AUTH` is set to the short-lived PAM launch token; upstream Redis credentials stay managed server-side.
Redis TLS note: connector command builders support `redis-cli --tls` when launch payload includes `redis_tls=true`; in the current PAM slice the API issues non-TLS client-leg Redis proxy endpoints, so connector launches are typically plaintext on loopback/session endpoints. Upstream Redis TLS is handled by the PAM proxy-to-target leg when enabled on the asset.
For SFTP in this slice, connector launches FileZilla/WinSCP against the PAM SFTP relay endpoint (session token is passed as SFTP password in launch payload). File-operation telemetry is captured server-side by the PAM relay.

## Configuration (env)

| Variable | Default | Purpose |
|---|---|---|
| `PAM_CONNECTOR_ADDR` | `127.0.0.1:9494` | Connector bind address |
| `PAM_CONNECTOR_ALLOWED_ORIGIN` | `http://127.0.0.1:3000,http://localhost:3000` | CORS allowlist (comma-separated origins) |
| `PAM_CONNECTOR_ALLOW_ANY_ORIGIN` | `false` | If `true`, sets `Access-Control-Allow-Origin: *` (unsafe) |
| `PAM_CONNECTOR_ALLOW_REMOTE` | `false` | If `true`, allows non-loopback HTTP callers (unsafe) |
| `PAM_CONNECTOR_PUTTY_PATH` | `putty` | PuTTY executable/path on Windows |
| `PAM_CONNECTOR_WINSCP_PATH` | `winscp` | WinSCP executable/path on Windows |
| `PAM_CONNECTOR_FILEZILLA_PATH` | `filezilla` | FileZilla executable/path on macOS/Linux |
| `PAM_CONNECTOR_DBEAVER_PATH` | *(auto-discovery)* | DBeaver app/binary path override |
| `PAM_CONNECTOR_REDIS_CLI_PATH` | *(auto-discovery)* | `redis-cli` path override |
| `PAM_CONNECTOR_DBEAVER_TEMP_TTL` | `15m` | TTL for DBeaver temp launch material cleanup |
| `PAM_CONNECTOR_SECRET` | *(empty)* | Shared HMAC secret for verifying backend-signed launch payloads. Must match `PAM_CONNECTOR_SECRET` on the API. When empty, verification is disabled. |

Security notes:

- Keep connector bound to loopback (`127.0.0.1`) in normal operator workflows.
- `PAM_CONNECTOR_ALLOW_REMOTE=true` and `PAM_CONNECTOR_ALLOW_ANY_ORIGIN=true` are development/debug escape hatches and materially weaken connector trust boundaries.

## Structure

```
cmd/connector/     Entry point
internal/
  launch/          Shell launch payload + OS-specific launcher
  config/          Connector runtime config
  auth/            Connector token verification (HMAC signature check)
```

## Run

```bash
cd apps/connector
go run ./cmd/connector
```

## Build

```bash
# From repo root
make build-connector

# Cross-compile
GOOS=darwin GOARCH=arm64 go build -o bin/pam-connector-darwin-arm64 ./cmd/connector
GOOS=linux GOARCH=amd64 go build -o bin/pam-connector-linux-amd64 ./cmd/connector
GOOS=windows GOARCH=amd64 go build -o bin/pam-connector-windows-amd64.exe ./cmd/connector
```
