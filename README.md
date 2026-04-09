# AccessD

**Infrastructure Access Gateway**

AccessD is an open-source infrastructure access gateway that provides secure, audited access to servers, databases, and services — without giving operators raw credentials.

Developed by [DistrictD](https://districtd.co.in)

---

## What is AccessD?

AccessD sits between your operators and your infrastructure. Instead of distributing SSH keys, database passwords, or Redis auth strings, you grant users access through AccessD. Every session is brokered, every command is logged, and every credential stays server-side.

```
Operator browser ──► AccessD UI ──► grant check ──► launch token
Operator terminal ──► accessd-connector ──► SSH proxy ──► target server
Operator DBeaver ──► accessd-connector ──► DB proxy  ──► target database
```

No long-lived credentials on operator machines. No shared keys. Every session has an audit trail.

---

## Key Features

### Access Control
- Role-based access control (`admin`, `operator`, `auditor`, `user`)
- Per-asset, per-action access grants
- LDAP / Samba AD integration with group-to-role mapping
- Hybrid auth: LDAP primary + local fallback

### Protocol Support

| Protocol   | Client tool        | Audit            |
|------------|--------------------|------------------|
| SSH shell  | Native terminal    | Full transcript  |
| SFTP       | FileZilla / WinSCP | File transfers   |
| PostgreSQL | DBeaver            | Query log        |
| MySQL      | DBeaver            | Query log        |
| MSSQL      | DBeaver            | Query log        |
| Redis      | redis-cli          | Command log      |

### Audit & Visibility
- Append-only session event and audit log
- Session replay (terminal transcript)
- Export session summaries and transcripts
- Admin audit views with search and filtering
- Active session monitoring

### Security
- All credentials encrypted at rest (AES-256-GCM)
- Short-lived HMAC launch tokens (2-minute default TTL)
- Connector verification via shared HMAC secret
- SSH upstream host key verification (no TOFU in production)
- `Secure` + `SameSite=strict` session cookies in production
- Zero plaintext credentials on operator machines

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  AccessD Server (Debian / Linux)                             │
│                                                              │
│  ┌──────────┐   ┌──────────────────┐   ┌─────────────────┐  │
│  │  nginx   │   │   accessd        │   │  PostgreSQL     │  │
│  │  :443    │──►│   :8080 (HTTP)   │──►│  (credentials,  │  │
│  │  TLS +   │   │   :2222 (SSH px) │   │   sessions,     │  │
│  │  static  │   │   DB proxies     │   │   audit)        │  │
│  └──────────┘   └──────────────────┘   └─────────────────┘  │
└──────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────┐
│  Operator Machine                                            │
│                                                              │
│  Browser     ──► https://accessd.example.internal           │
│  Terminal    ──► :2222 SSH proxy (via accessd-connector)     │
│  DBeaver     ──► session-scoped DB proxy (via connector)     │
│  accessd-connector runs at 127.0.0.1:9494 (loopback only)   │
└──────────────────────────────────────────────────────────────┘
```

**Server-side:**
- `accessd` — Go binary: API server, SSH proxy, and all DB/Redis proxies in one process
- `nginx` — TLS termination, static UI, API reverse proxy
- `PostgreSQL` — persistent store (same host or remote)

**Operator-side:**
- `accessd-connector` — local launcher; spawns terminals, DBeaver, redis-cli on the operator's machine
- Browser — accesses the UI at the configured domain

The connector runs on the **operator's machine only** — never server-side.

---

## Screenshots

Placeholders for OSS launch (replace with real captures):

- `docs/screenshots/login.png` — AccessD login page
- `docs/screenshots/access.png` — My Access launch surface
- `docs/screenshots/sessions.png` — Session timeline and replay
- `docs/screenshots/audit.png` — AccessD audit log views

---

## Quick Start (Local Development)

**Prerequisites:** Go 1.22+, Node.js 20+, Docker

```bash
git clone https://github.com/districtd/accessd
cd accessd

# Start PostgreSQL
make dev-up

# Start the API (runs migrations + bootstraps admin account)
make dev-api

# Start the UI dev server (in another terminal)
make dev-ui

# Open http://localhost:3000
# Bootstrap credentials: admin / admin123
# Change these immediately after first login.
```

To test multi-protocol launches, start the connector on your local machine:

```bash
make dev-connector
```

---

## Connector Install / Setup

AccessD uses `accessd-connector` for local client launches (shell/SFTP/DBeaver/Redis).

- Distribution model: versioned cross-platform artifacts with checksums
- Hosting model: nginx-served binaries from your AccessD deployment domain
- Runtime model: on-demand local process
- Metadata endpoint: `GET /api/connector/releases/latest`

Quick checks:

```bash
# Connector health/version (operator machine)
accessd-connector
curl -fsS http://127.0.0.1:9494/version
```

Build release artifacts:

```bash
make build-connector-release VERSION=0.2.0
```

Install on operator machine from extracted release package:

```bash
# macOS / Linux
./install.sh
```

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

Installer behavior:

- registers `accessd-connector://` protocol for UI-triggered auto-start
- auto-detects local client binaries (DBeaver/FileZilla/redis-cli/PuTTY/WinSCP)
- prompts for manual paths when detection fails (interactive installs)
- writes/updates `~/.accessd-connector/config.yaml` for connector discovery

See [docs/CONNECTOR_DISTRIBUTION.md](docs/CONNECTOR_DISTRIBUTION.md) for:

- artifact naming and checksums
- OS-specific install paths
- first-run UX for missing/outdated connector
- compatibility and update strategy

`accessd-connector` systemd service is optional and intended for managed-desktop environments. Standard operator flow uses on-demand UI auto-start and does not require a background service.

---

## Production Deployment

See [DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md](DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md) for the complete end-to-end guide:

- Debian + systemd + nginx setup
- Filesystem layout and file permissions
- TLS configuration
- systemd service hardening (13+ security directives)
- LDAP / Samba AD configuration
- SSH proxy known-hosts management
- Secret rotation procedures
- Update / rollout workflow and rollback

**Deployment in brief:**

```bash
# Build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o accessd ./apps/api/cmd/server
npm --prefix apps/ui ci && npm --prefix apps/ui run build

# Install
sudo install -o root -g accessd -m 0755 accessd /opt/accessd/bin/accessd
sudo cp -r apps/ui/dist/. /var/www/accessd/

# Configure
sudo cp deploy/env/accessd.env.example /etc/accessd/accessd.env
# Edit /etc/accessd/accessd.env — fill all CHANGE_ME_* values
sudo chmod 0640 /etc/accessd/accessd.env

# Enable service
sudo cp deploy/systemd/accessd.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now accessd

# Enable nginx
sudo cp deploy/nginx/accessd.conf.example /etc/nginx/sites-available/accessd
sudo ln -s /etc/nginx/sites-available/accessd /etc/nginx/sites-enabled/accessd
sudo nginx -t && sudo systemctl reload nginx
```

---

## Configuration

All configuration is via environment variables or a flat `KEY=VALUE` file at `ACCESSD_CONFIG_FILE`.

### Required secrets

| Variable                       | Description                                     |
|--------------------------------|-------------------------------------------------|
| `ACCESSD_DB_URL`               | PostgreSQL connection string                    |
| `ACCESSD_VAULT_KEY`            | Base64-encoded 32-byte AES-256 encryption key   |
| `ACCESSD_LAUNCH_TOKEN_SECRET`  | HMAC secret for session launch tokens           |
| `ACCESSD_CONNECTOR_SECRET`     | Shared HMAC for connector payload verification  |

Generate secrets: `openssl rand -base64 32`

See [deploy/env/accessd.env.example](deploy/env/accessd.env.example) for the full configuration reference.

> **Backward compatibility:** Existing deployments using `PAM_*` environment variables continue to work unchanged. AccessD automatically bridges `PAM_*` → `ACCESSD_*` on startup. Migrate at your own pace.

---

## CLI Reference

```
accessd server              Start the API server (migrations + bootstrap run on every start)
accessd migrate up          Apply pending database migrations
accessd migrate status      Show migration application status
accessd bootstrap           Run migrations and bootstrap admin account only

accessd-connector           Start the local connector daemon (operator machine)
accessd-connector bridge-shell [flags]   Internal: transparent SSH auth bridge
```

---

## Roles

| Role       | Capabilities                                                          |
|------------|-----------------------------------------------------------------------|
| `admin`    | Full access: users, assets, grants, LDAP config, audit, sessions     |
| `operator` | Launch sessions, view admin surfaces (no mutations)                   |
| `auditor`  | Read-only: sessions and audit events; no launches or mutations        |
| `user`     | Own sessions and own access grants only                               |

---

## Security Model

**Credentials never leave the server.** Asset passwords, SSH keys, and database credentials are stored encrypted in PostgreSQL. Operators connect through AccessD's proxy — they never receive the underlying credentials.

**Sessions are short-lived and audited.** Every launch produces a session record. Terminals produce a full replay transcript. Database sessions log queries (configurable max size). Redis sessions log commands.

**Production safety defaults:**
- API HTTP listener bound to loopback (`127.0.0.1:8080`) by default
- Session cookies: `Secure` + `SameSite=strict` enforced in production
- Vault key must be a properly-formatted 32-byte base64 key in production
- SSH upstream host key mode defaults to `accept-new` with on-the-fly fingerprint persistence
- Connector bound to loopback on operator machine
- `ACCESSD_ALLOW_UNSAFE_MODE=false` is the default and is validated at startup

See [DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md §15](DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md#15-security-hardening-and-operational-safeguards) for the full security hardening guide.

---

## Contributing

Contributions are welcome. Please:

1. Fork the repository and create a feature branch
2. Run `make test` and `make lint` before submitting
3. Open a pull request with a clear description of what changes and why

For significant changes, open an issue first to align on the approach.

### Development targets

```bash
make dev-up            # Start PostgreSQL (Docker Compose)
make dev-api           # Start API server with migrations
make dev-ui            # Start Vite dev server
make dev-connector     # Start local connector
make test              # Run all tests (Go + TypeScript)
make lint              # Lint Go + TypeScript
make validate-contract # Validate OpenAPI spec
make test-matrix       # Multi-protocol launch matrix test (requires dev-up-targets)
```

---

## Roadmap

- [ ] Browser-native SSH terminal (no connector required for shell sessions)
- [ ] Automated vault key rotation tooling
- [ ] Operator CLI (`accessctl`) for asset and user management from the command line
- [ ] OIDC / SSO provider support
- [ ] Access request and approval workflow
- [ ] Kubernetes connector for pod exec sessions

---

## License

<!-- License TBD — see LICENSE file when added -->

---

*AccessD — Infrastructure Access Gateway*
*Developed by [DistrictD](https://districtd.co.in)*
