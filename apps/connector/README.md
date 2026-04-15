# apps/connector — AccessD Connector

Thin local connector process for brokered launch flows: shell, SFTP, DBeaver, and Redis CLI.

## What the connector does NOT do

- Enforce AccessD policy (backend remains trust boundary)
- Call AccessD auth APIs directly in this slice
- Store tokens or credentials persistently
- Replay or deeply inspect client-side file activity in this step

## Connector Trust Model

Recommended mode (no operator-side shared secret):
- Connector calls backend verify endpoint (`ACCESSD_CONNECTOR_BACKEND_VERIFY_URL`, default derived from UI origin)
- Every launch request must include a `connector_token` signed by backend
- Connector verifies token online and rejects invalid/expired/session-mismatch requests with HTTP 403

Legacy mode:
- If `ACCESSD_CONNECTOR_SECRET` is set, connector can verify token signature locally (HMAC)

Hard requirement:
- Connector requires at least one verification mode (`ACCESSD_CONNECTOR_BACKEND_VERIFY_URL` or `ACCESSD_CONNECTOR_SECRET`).
- Verification is disabled only when `ACCESSD_CONNECTOR_ALLOW_INSECURE_NO_TOKEN=true` (development-only escape hatch).

## Local HTTP API (this slice)

- `GET /healthz` → connector liveness
- `GET /version` → connector build/version metadata
- `GET /info` → runtime diagnostics (effective config + missing required env)
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
    "username": "accessd",
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
    "host": "accessd.example.internal",
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
    "redis_host": "accessd.example.internal",
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
    "username": "accessd",
    "password": "short-lived-launch-token",
    "path": "/home/ubuntu",
    "expires_at": "2026-04-06T14:30:00Z"
  }
}
```

## OS Launch Behavior

- macOS: opens `Terminal.app` and runs connector-managed `bridge-shell` session (token injected automatically)
- Linux: tries `x-terminal-emulator`, `gnome-terminal`, `konsole`, then `xfce4-terminal`, each running connector-managed `bridge-shell` session
- Windows: launches PuTTY (`putty -ssh <username>@<proxy_host> -P <proxy_port>`)
- DBeaver launch behavior:
  - macOS: `ACCESSD_CONNECTOR_DBEAVER_PATH` (if set), else `open -a DBeaver`, then common app bundle paths, then `dbeaver`
  - Linux: `ACCESSD_CONNECTOR_DBEAVER_PATH` (if set), else `dbeaver`, then `dbeaver-ce`
  - Windows: `ACCESSD_CONNECTOR_DBEAVER_PATH` (if set), else `cmd /C start "" dbeaver -con <spec>`, then `dbeaver.exe`
- SFTP launch behavior:
  - macOS/Linux: `ACCESSD_CONNECTOR_FILEZILLA_PATH` (if set), else FileZilla in PATH/common macOS app paths
  - Windows: `ACCESSD_CONNECTOR_WINSCP_PATH` (if set), else WinSCP in PATH/common install paths
- Redis launch behavior:
  - macOS/Linux/Windows: `ACCESSD_CONNECTOR_REDIS_CLI_PATH` (if set), else `redis-cli` from PATH/common install paths, then launch inside a local terminal

Connector shell launch now uses a non-interactive bridge mode on macOS/Linux that reads the short-lived launch token from secure temp material and authenticates automatically. No manual token paste is required.
Windows shell launch continues through PuTTY with `-pw` token injection.

For DBeaver, connector creates a temporary local manifest file under OS temp directory (`accessd-dbeaver-launch-*`) and launches DBeaver using CLI connection spec with `connect=true` and `savePassword=false` for immediate session usability without persistent credential storage.
The connector auto-cleans temp launch directories after a TTL (`ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL`, default `15m`) and performs stale cleanup on startup. The manifest intentionally omits plaintext DB password; it records non-sensitive launch metadata only.
Connector launch diagnostics also redact sensitive values (for example `password=` fields, SFTP URL passwords, and `REDISCLI_AUTH`) before returning operator-visible error details.
Launch requests now fail fast with structured codes for common local issues (`*_not_installed`, `invalid_configured_path`, `terminal_not_installed`, `terminal_launch_failed`, etc.) instead of optimistic acceptance.

For Redis in this slice, connector launches local `redis-cli` in a new terminal window against a AccessD session-scoped Redis proxy endpoint. `REDISCLI_AUTH` is set to the short-lived AccessD launch token; upstream Redis credentials stay managed server-side.
Redis TLS note: connector command builders support `redis-cli --tls` when launch payload includes `redis_tls=true`; in the current AccessD slice the API issues non-TLS client-leg Redis proxy endpoints, so connector launches are typically plaintext on loopback/session endpoints. Upstream Redis TLS is handled by the AccessD proxy-to-target leg when enabled on the asset. For environments with self-signed TLS on the client leg, `ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE=true` can automatically append `--insecure` for local redis-cli launch.
For SFTP in this slice, connector launches FileZilla/WinSCP against the AccessD SFTP relay endpoint (session token is passed as SFTP password in launch payload), pre-resolves proxy host key trust material, and then auto-connects without interactive host-key confirmation prompts. File-operation telemetry is captured server-side by the AccessD relay.

## Configuration (env)

| Variable | Default | Purpose |
|---|---|---|
| `ACCESSD_CONNECTOR_ADDR` | `127.0.0.1:9494` | Connector bind address |
| `ACCESSD_CONNECTOR_ALLOWED_ORIGIN` | `http://127.0.0.1:3000,http://localhost:3000` | CORS allowlist (comma-separated origins) |
| `ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN` | `false` | If `true`, sets `Access-Control-Allow-Origin: *` (unsafe) |
| `ACCESSD_CONNECTOR_ALLOW_REMOTE` | `false` | If `true`, allows non-loopback HTTP callers (unsafe) |
| `ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL` | derived from UI domain | Installer helper override for auto-downloaded runtime env URL (`/downloads/bootstrap/accessd-connector.env`) |
| `ACCESSD_CONNECTOR_BACKEND_VERIFY_URL` | derived from allowed origin | API endpoint for online connector-token verification (`/api/connector/token/verify`) |
| `ACCESSD_CONNECTOR_BACKEND_VERIFY_TIMEOUT` | `5s` | HTTP timeout for backend online token verification |
| `ACCESSD_CONNECTOR_BACKEND_CA_CERT_FILE` | derived (`~/.accessd-connector/certs/accessd-<host>.crt/.cer`) | Optional backend CA file for connector verify TLS trust (recommended for private/self-signed CA) |
| `ACCESSD_CONNECTOR_BACKEND_VERIFY_INSECURE` | `false` | Emergency-only bypass for backend verify TLS chain validation |
| `ACCESSD_CONNECTOR_AUTO_TRUST_SERVER_CERT` | `true` | Installer helper: auto-fetch and trust AccessD HTTPS cert on operator machine |
| `ACCESSD_CONNECTOR_TRUST_CERT_URL` | derived from UI domain | Installer helper override for cert download URL |
| `ACCESSD_CONNECTOR_PUTTY_PATH` | `putty` | PuTTY executable/path on Windows |
| `ACCESSD_CONNECTOR_WINSCP_PATH` | `winscp` | WinSCP executable/path on Windows |
| `ACCESSD_CONNECTOR_FILEZILLA_PATH` | `filezilla` | FileZilla executable/path on macOS/Linux |
| `ACCESSD_CONNECTOR_DBEAVER_PATH` | *(auto-discovery)* | DBeaver app/binary path override |
| `ACCESSD_CONNECTOR_REDIS_CLI_PATH` | *(auto-discovery)* | `redis-cli` path override |
| `ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL` | `15m` | TTL for DBeaver temp launch material cleanup |
| `ACCESSD_CONNECTOR_PROXY_HOSTKEY_MODE` | `accept-replace` | Shell-bridge proxy host-key policy (`strict`, `accept-new`, `accept-replace`) |
| `ACCESSD_CONNECTOR_PROXY_KNOWN_HOSTS_PATH` | `~/.accessd-connector/proxy_known_hosts` | Local known_hosts path for shell-bridge proxy key trust |
| `ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE` | `false` | Auto-add `redis-cli --insecure` when `redis_tls=true` and payload does not already request insecure |
| `ACCESSD_CONNECTOR_SECRET` | *(empty)* | Optional legacy local-HMAC verification secret. Prefer backend online verify URL to avoid operator-side secret distribution. |

Runtime notes:

- Connector launch does not require build-time environment variables. Build flags (`version`, `commit`, `builtAt`) are optional metadata only.
- On first startup, connector auto-creates `~/.accessd-connector/config.yaml` (unless overridden by `ACCESSD_CONNECTOR_CONFIG_FILE`) with commented examples for app path and terminal overrides.
- Most operator machines can run with zero env configuration. Set env only when overriding defaults (port/origins/path overrides/security hardening).
- If local dependencies are missing (for example DBeaver/FileZilla/redis-cli/OpenSSH/PuTTY), launches return structured actionable errors; the UI surfaces install guidance.

Security notes:

- Keep connector bound to loopback (`127.0.0.1`) in normal operator workflows.
- `ACCESSD_CONNECTOR_ALLOW_REMOTE=true` and `ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN=true` are development/debug escape hatches and materially weaken connector trust boundaries.

## Structure

```
cmd/connector/     Entry point
internal/
  launch/          Shell launch payload + OS-specific launcher
  config/          Connector runtime config
  auth/            Connector token verification (online + optional local HMAC fallback)
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
GOOS=darwin GOARCH=arm64 go build -o bin/accessd-connector-darwin-arm64 ./cmd/connector
GOOS=linux GOARCH=amd64 go build -o bin/accessd-connector-linux-amd64 ./cmd/connector
GOOS=windows GOARCH=amd64 go build -o bin/accessd-connector-windows-amd64.exe ./cmd/connector
```
