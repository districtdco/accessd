# Local Testing Runbook

This runbook is for full local operator testing of API + UI + connector with seeded assets.

## Prerequisites

- Docker + Docker Compose
- Go 1.22+
- Node.js 20+ and npm
- `curl` and `jq`
- Local desktop clients (for full manual flow):
  - SSH client (`ssh`)
  - DBeaver
  - FileZilla (macOS/Linux) or WinSCP (Windows)
  - `redis-cli`

Quick prerequisite check:

```bash
./scripts/check_env.sh
```

## Local Env Expectations

Default script values (override with env vars if needed):

- API DB: `postgres://pam:pam_dev_password@127.0.0.1:5432/pam?sslmode=disable`
- API bind: `:8080`
- UI bind: `http://127.0.0.1:3000`
- Connector bind: `127.0.0.1:9494`
- Dev admin login: `admin` / `admin123`
- Shared connector secret (API + connector): `accessd-dev-connector-secret`

Target test services started by `dev_up.sh --with-targets`:

- SSH/SFTP target: `127.0.0.1:22222` (`pam` / `pam_dev_password`)
- PostgreSQL target: `127.0.0.1:15432` (`app_user` / `app_password`, db `app`)
- MySQL target: `127.0.0.1:13306` (`app_user` / `app_password`, db `appdb`)
- Redis target: `127.0.0.1:16379` (password `app_password`)
- MongoDB target: `127.0.0.1:17017` (`app_user` / `app_password`, auth db `admin`)
- MSSQL target (optional): `127.0.0.1:11433` (`sa` / `YourStrong!Passw0rd`, db `appdb`)

## Startup Sequence

Note: On some macOS hosts, transient Go temp binaries created by `go run` may fail at runtime with dyld `missing LC_UUID load command`. The local scripts below build and run repo-local binaries in `./bin` as the supported workaround.

1. Start local dependencies:

```bash
./scripts/dev_up.sh --with-targets
# Optional MSSQL container:
# ./scripts/dev_up.sh --with-targets --with-mssql
```

2. Start API:

```bash
./scripts/dev_api.sh
```

3. Bootstrap predictable local assets/credentials/grants:

```bash
./scripts/dev_seed.sh
```

4. Start connector:

```bash
./scripts/dev_connector.sh
```

Optional explicit binary builds:

```bash
make build-api-bin
make build-connector-bin
```

5. Start UI:

```bash
./scripts/dev_ui.sh
```

6. Login in browser:

- `http://127.0.0.1:3000/login`
- default: `admin` / `admin123`

## Guided Test Flows

Use seeded assets from `dev_seed.sh`.

### SSH (`accessd-local-linux` + `shell`)

- Launch from Access page using `Shell`.
- Expect connector to open terminal and run connector-managed `bridge-shell` auth flow to the AccessD proxy.
- No manual token paste is required in normal connector flow.
- If terminal/PuTTY is missing or misconfigured, launch should fail immediately with explicit connector error details.
- Verify:
  - session appears in `/sessions`
  - session detail shows shell lifecycle and events
  - replay endpoint/UI section loads (`/sessions/:id`, shell replay panel)
  - audit has launch + login/session events

### SFTP (`accessd-local-linux` + `sftp`)

- Launch from Access page using `SFTP`.
- Expect connector to open FileZilla/WinSCP against AccessD SFTP relay.
- If FileZilla/WinSCP path is invalid, launch should fail immediately (not remain pending).
- Perform upload/download/rename/delete on a test file.
- Verify:
  - session created and visible in `/sessions`
  - session detail file-operation timeline populated
  - audit shows `file_operation` events with path metadata

### PostgreSQL (`accessd-local-postgres` + `dbeaver`)

- Launch from Access page using `DBeaver`.
- Expect connector to open DBeaver with AccessD session-scoped endpoint.
- If DBeaver is not installed or configured path is invalid, launch should fail immediately.
- Run a simple query (example: `select 1;`).
- Verify:
  - session created and visible
  - session detail DB timeline populated (`db_query` events where available)
  - admin audit includes launch/session lifecycle events

### MySQL (`accessd-local-mysql` + `dbeaver`)

- Launch `DBeaver` from Access page.
- Run a simple query (example: `select 1;`).
- Verify same checks as PostgreSQL flow.

### MSSQL (`accessd-local-mssql` + `dbeaver`)

- Launch `DBeaver` from Access page.
- Attempt query against target.
- Verify:
  - session creation and launch lifecycle always work
  - full connectivity depends on TLS mode requirements (see known limitations)

### MongoDB (`accessd-local-mongo` + `dbeaver`)

- Launch `DBeaver` from Access page.
- If configured, connector can route Mongo launches to Robo 3T as a fallback launcher.
- Run a simple check (for example list databases / collections).
- Verify:
  - session created and visible
  - session lifecycle transitions through connector/proxy/upstream events
  - admin audit includes launch/session lifecycle events

### Redis (`accessd-local-redis` + `redis`)

- Launch `Redis CLI` from Access page.
- Expect connector to open terminal and run `redis-cli` via AccessD proxy.
- If `redis-cli` is missing, launch should fail immediately.
- Run sample commands (`PING`, `SET`, `GET`).
- Verify:
  - session created and visible
  - redis command timeline appears in session detail (`redis_command` events)
  - audit/session lifecycle events present

## Verification Checklist

For each protocol flow, verify all of the following:

- session record exists (`/sessions`, `/sessions/:id`)
- session lifecycle transitions are visible (requested/succeeded/ended or failed)
- if connector reports launch success but no proxy/client connection occurs, session should transition from `pending` to `failed` after launch materialization timeout (`ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT`, default `45s`)
- timeline/transcript section is populated where supported
- corresponding audit events appear in `/admin/audit/events`
- no secrets appear in UI payloads, logs, or API responses

Useful API-only matrix smoke:

```bash
./scripts/test_matrix.sh --api-only
```

Full guided check:

```bash
./scripts/test_matrix.sh
```

## Known Limitations (Current)

- MSSQL full client<->proxy TLS tunnel mode is not implemented.
  - Local impact: tests can proceed only when TLS tunnel mode is not required.
  - Staging/production impact: blocker for environments requiring strict MSSQL TLS tunnel behavior.

- Redis client-leg TLS to AccessD Redis proxy is not implemented.
  - Local impact: works over loopback/plaintext in current local trust model.
  - Production impact: blocker where TLS is required from client to AccessD proxy endpoint.

- API listener TLS is intentionally edge-terminated (reverse proxy / load balancer), not app-native TLS.
  - Local impact: none (HTTP local dev is expected).
  - Production impact: must deploy behind TLS-terminating edge.

- Full Go runtime tests may fail in this specific environment due external dyld/runtime issue.
  - Local impact: compile/build validation still works; runtime `go test ./...` may need alternate host runtime.
