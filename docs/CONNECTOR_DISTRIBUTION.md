# AccessD Connector Distribution

## Product Artifact

`accessd-connector` is a first-class release artifact. Each release publishes:

- `accessd-connector-<version>-darwin-arm64.pkg` (preferred)
- `accessd-connector-<version>-darwin-arm64.tar.gz`
- `accessd-connector-<version>-darwin-amd64.pkg` (preferred)
- `accessd-connector-<version>-darwin-amd64.tar.gz`
- `accessd-connector-<version>-linux-amd64.deb` (preferred when tooling available)
- `accessd-connector-<version>-linux-amd64.rpm` (preferred when tooling available)
- `accessd-connector-<version>-linux-amd64.tar.gz`
- `accessd-connector-<version>-linux-arm64.deb` (preferred when tooling available)
- `accessd-connector-<version>-linux-arm64.rpm` (preferred when tooling available)
- `accessd-connector-<version>-linux-arm64.tar.gz`
- `accessd-connector-<version>-windows-amd64.msi` (preferred when tooling available)
- `accessd-connector-<version>-windows-amd64.zip`
- `accessd-connector-<version>-checksums.txt`
- `*.sig` detached signatures (when `CONNECTOR_SIGNING_KEY_ID` is configured)

Create artifacts with:

```bash
make build-connector-release VERSION=0.2.0
```

## Release Metadata API

AccessD exposes connector release metadata at:

- `GET /api/connector/releases/latest`

This includes:

- latest connector version
- minimum compatible connector version
- per-OS/per-arch download URLs
- checksum file URL
- checksum signature URL
- runtime model (`on-demand`)
- install docs URL

Config variables:

- `ACCESSD_CONNECTOR_RELEASES_BASE_URL`
- `ACCESSD_CONNECTOR_LATEST_VERSION`
- `ACCESSD_CONNECTOR_MIN_VERSION`
- `ACCESSD_CONNECTOR_RELEASE_CHANNEL`

For this deployment model, `ACCESSD_CONNECTOR_RELEASES_BASE_URL` should point to your AccessD nginx host, for example:

- `https://accessd.example.internal/downloads/connectors`

## nginx Hosting Model

Connector artifacts are served by nginx from a local directory, not GitHub Releases.

Recommended filesystem path:

- `/var/www/accessd-downloads/connectors/v<version>/`

Example files:

- `/var/www/accessd-downloads/connectors/v0.2.0/accessd-connector-0.2.0-darwin-arm64.tar.gz`
- `/var/www/accessd-downloads/connectors/v0.2.0/accessd-connector-0.2.0-linux-amd64.tar.gz`
- `/var/www/accessd-downloads/connectors/v0.2.0/accessd-connector-0.2.0-windows-amd64.zip`
- `/var/www/accessd-downloads/connectors/v0.2.0/accessd-connector-0.2.0-checksums.txt`

After building artifacts:

```bash
make build-connector-release VERSION=0.2.0
sudo mkdir -p /var/www/accessd-downloads/connectors/v0.2.0
sudo cp dist/connector/v0.2.0/* /var/www/accessd-downloads/connectors/v0.2.0/
```

## Runtime Model

Current model: `on-demand`.

- Connector runs locally on the operator machine.
- UI checks connector availability (`/connector/version`) before launch handoff.
- If connector is missing or too old, UI shows an AccessD-specific install/update prompt with a direct OS/arch download URL.
- If connector is not running, UI now shows an explicit start hint (`accessd-connector` or `accessd-connector.exe`) along with install/update links.

Runtime configuration defaults:

- First startup auto-creates `~/.accessd-connector/config.yaml` with commented override examples.
- Build-time env variables are not required for launch behavior; runtime env is optional and used only for non-default overrides.
- API-side `ACCESSD_CONNECTOR_SECRET` remains required for signing connector launch tokens.
- Connector-side verification can use either:
  - online backend verification (`ACCESSD_CONNECTOR_BACKEND_VERIFY_URL`, recommended), or
  - local HMAC verification (`ACCESSD_CONNECTOR_SECRET`, legacy fallback).

This model avoids persistent background daemons for initial OSS rollout, while keeping an upgrade path to optional autostart agents.

## Install Locations

Recommended stable install paths:

- macOS: `/usr/local/bin/accessd-connector` or `~/.local/bin/accessd-connector`
- Linux: `~/.local/bin/accessd-connector` (or `/usr/local/bin/accessd-connector` for managed installs)
- Windows: `%LocalAppData%\AccessD\bin\accessd-connector.exe`

Ensure the install path is on `PATH`, or launch with absolute path.

## Installer-Side Protocol Registration

Each connector release archive now includes an installer:

- macOS/Linux archives include `install.sh` and `uninstall.sh`
- Windows archive includes `install.ps1` and `uninstall.ps1`

Package artifact bootstrap behavior:

- macOS `.pkg` runs a postinstall hook that invokes connector bootstrap for the logged-in console user.
- Linux `.deb` / `.rpm` run post-install bootstrap hooks (best effort, user-context when available).
- Windows `.msi` runs a post-install custom action that invokes `install.ps1` (best effort).

These installers:

- install `accessd-connector` to a stable user path
- register the `accessd-connector://` protocol handler
- create a small URL-handler shim that starts connector if it is not already running
- perform dependency discovery for operator tools and write `~/.accessd-connector/config.yaml`
- if auto-detection misses tools, interactive installs prompt for paths (DBeaver/FileZilla/redis-cli/PuTTY/WinSCP and terminal preference)

Examples after extracting archive:

```bash
# macOS / Linux
./install.sh
# uninstall
./uninstall.sh
```

```powershell
# Windows PowerShell
powershell -ExecutionPolicy Bypass -File .\install.ps1
# uninstall
powershell -ExecutionPolicy Bypass -File .\uninstall.ps1
```

### macOS Gatekeeper (Internal Dev Teams)

For internal unsigned builds, macOS may show:

`Apple could not verify "<file>.pkg" is free of malware`.

Use one of these local unblock flows:

```bash
# Option A: remove quarantine attribute and install
xattr -dr com.apple.quarantine accessd-connector-<version>-darwin-<arch>.pkg
sudo installer -pkg accessd-connector-<version>-darwin-<arch>.pkg -target /
```

- or in Finder: right-click the `.pkg` -> `Open` -> `Open`.

This is acceptable for trusted internal dev distribution only.
For broader production rollout, use signed and notarized macOS installer artifacts.

Uninstall scripts preserve `~/.accessd-connector` / `%USERPROFILE%\.accessd-connector` by default.
To remove config too, run with `ACCESSD_CONNECTOR_REMOVE_CONFIG=1`.

Installer verification defaults:

- installers verify unpacked payload integrity when `release-files-sha256.txt` is present
- to skip verification: `ACCESSD_CONNECTOR_VERIFY_RELEASE=false`
- to continue despite mismatch/tooling gaps: `ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE=true`

UI auto-start then works through `accessd-connector://start` without manual operator steps in normal setups.

## Why Not systemd For Connector?

For standard operator workflows, you do not need systemd for connector:

- UI launch triggers protocol-based auto-start (`accessd-connector://start`)
- connector runs on-demand and exits only when stopped by the user/OS

Use systemd (or other background service managers) only for controlled environments (shared workstations, kiosk-style setups, strict MDM baselines) where always-running local agent behavior is explicitly desired.

## First-Run UX

When a user launches Shell/SFTP/DBeaver/Redis:

1. UI fetches release metadata.
2. UI checks connector runtime version.
3. If missing: show `AccessD connector not installed` with OS-specific download link.
4. If outdated: show `AccessD connector update available` with required minimum version and download link.
5. If compatible: proceed with session launch.

## Compatibility Policy

- Connector versioning follows server release tags (`vX.Y.Z`).
- Server declares `minimum_version` for connector compatibility.
- Connector older than minimum is blocked at UI preflight.
- Configuration policy:
  - Use `ACCESSD_*` env names.
  - Connector installer can auto-refresh AccessD TLS trust from `/downloads/certs/accessd-server.crt` when enabled.
  - Existing local connector env is preserved across upgrades; installer only refreshes managed origin/verify keys when they are missing or still placeholder defaults.
  - Protocol-handler autostart paths do not repeatedly re-import cert trust on each page reload.
