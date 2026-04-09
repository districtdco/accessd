# scripts/

Dev tooling and helper scripts for local bring-up, seeding, and test flow.

## Included

- `check_env.sh`
  - Verifies local tool prerequisites (`docker`, `go`, `node`, `npm`, `curl`, `jq`, compose availability).

- `dev_up.sh`
  - Starts local docker dependencies.
  - Default services: AccessD postgres + LDAP.
  - Optional target stack: `--with-targets` (SSH/SFTP, PostgreSQL, MySQL, Redis) and `--with-mssql`.

- `dev_api.sh`
  - Runs API with practical local defaults.
  - Builds and reuses `./bin/accessd` (rebuilds when source is newer).
  - Modes:
    - default: `server`
    - `--migrate-only`
    - `--bootstrap-only`

- `dev_ui.sh`
  - Runs UI (`npm install` automatically if `node_modules` is missing).

- `dev_connector.sh`
  - Runs connector with loopback-default trust settings and shared dev connector secret.
  - Builds and reuses `./bin/accessd-connector` (rebuilds when source is newer).

- `build_deploy_bundle.sh`
  - Builds a deployment-ready artifact bundle under `deploy/artifacts/accessd-<version>/`.
  - Includes:
    - API binary (`bin/accessd`, default target `linux/amd64`)
    - UI static build (`ui/`)
    - connector release archives + checksums (`connectors/v<version>/`)
    - deployment templates (`deploy/env`, `deploy/systemd`, `deploy/nginx`)
  - Also creates `deploy/artifacts/accessd-<version>.tar.gz` for easy transfer to a VM.
  - Bundle includes `deploy/install_on_vm.sh` for path-mapped install into `/opt`, `/etc`, `/var/www`, and nginx connector download directory.

- `build_connector_release.sh`
  - Builds cross-platform connector archives under `dist/connector/v<version>/`.
  - Each archive now includes platform installer:
    - macOS/Linux: `install.sh`
    - Windows: `install.ps1`
  - Installers register `accessd-connector://` protocol handler for UI-triggered auto-start.
  - Installers also auto-detect local client dependencies and can prompt for manual paths when detection fails.

- `dev_seed.sh`
  - Idempotently upserts local test assets/credentials/grants through admin APIs.
  - Seeds:
    - `accessd-local-linux` (shell + sftp)
    - `accessd-local-postgres` (dbeaver)
    - `accessd-local-mysql` (dbeaver)
    - `accessd-local-mssql` (dbeaver)
    - `accessd-local-redis` (redis)

- `test_api_smoke_extended.sh`
  - API-level launch matrix smoke for all seeded flows.
  - Verifies launch responses for SSH, SFTP, PostgreSQL, MySQL, MSSQL, Redis.

- `test_matrix.sh`
  - Runs API launch matrix smoke and prints guided manual UI verification checklist.
  - `--api-only` runs only the API smoke portion.

- `smoke_api.sh`
  - Lightweight backend smoke check.
  - Verifies:
    - `GET /health/ready`
    - login flow (`POST /auth/login`)
    - `GET /access/my`
    - `POST /sessions/launch` response shape
    - `GET /sessions/{id}` + `GET /sessions/{id}/events`
    - `GET /sessions/{id}/replay` for shell smoke action
    - admin audit list/detail checks when logged-in user has admin role

## Usage

```bash
./scripts/check_env.sh
./scripts/dev_up.sh --with-targets
./scripts/dev_api.sh
./scripts/dev_seed.sh
./scripts/dev_connector.sh
./scripts/dev_ui.sh
./scripts/build_deploy_bundle.sh 0.2.0

# In another shell:
./scripts/test_matrix.sh
```

Optional environment overrides:

- `API_BASE_URL` (default: `http://127.0.0.1:8080`)
- `ACCESSD_SMOKE_USERNAME` (default: `admin`)
- `ACCESSD_SMOKE_PASSWORD` (default: `admin123`)
- `ACCESSD_SMOKE_ACTION` (default: `shell`)
- `ACCESSD_SMOKE_ASSET_ID` (optional explicit asset id)

## macOS Local Runtime Note

On some macOS hosts, direct runtime execution of transient Go temp binaries (for example via `go run`) can fail with dyld errors such as `missing LC_UUID load command`.

Local dev scripts use repo-local binaries under `./bin` as the supported workaround:

- `./bin/accessd`
- `./bin/accessd-connector`

You can also build them explicitly:

```bash
make build-api-bin
make build-connector-bin
```
