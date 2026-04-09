# AccessD — Private Deployment Guide (Debian + systemd + nginx)

This guide covers end-to-end deployment of the AccessD Infrastructure Access Gateway system on a
fresh Debian host. It is written for an internal/private deployment, not a public SaaS context.

---

## 0. Exact Order (Single VM: AccessD + PostgreSQL + nginx)

Use this exact sequence when API, DB, and nginx are all on the same VM:

1. Build and copy bundle to VM:
```bash
# from repo root on build machine
./scripts/build_deploy_bundle.sh
scp -r dist/accessd-<version> user@vm:/tmp/
```
2. SSH to VM and run installer with local-DB + nginx enabled:
```bash
ssh user@vm
cd /tmp/accessd-<version>/deploy
sudo INSTALL_POSTGRES=true INSTALL_NGINX=true TLS_SETUP_MODE=prompt ./install_on_vm.sh
```
3. During installer prompts:
- enter your real domain (for nginx `server_name` + AccessD public host vars)
- choose TLS mode:
- `self-signed` for internal/testing
- `csr` if you want CA-signed cert flow
- `existing` if cert/key already present
4. Fill required secrets in `/etc/accessd/accessd.env` if placeholders remain:
```bash
sudo grep -n 'CHANGE_ME' /etc/accessd/accessd.env
```
5. Re-run installer after env updates (idempotent):
```bash
cd /tmp/accessd-<version>/deploy
sudo INSTALL_POSTGRES=true INSTALL_NGINX=true TLS_SETUP_MODE=existing ./install_on_vm.sh
```
6. Verify services:
```bash
sudo systemctl status accessd --no-pager
sudo systemctl status nginx --no-pager
curl -kfs https://<your-domain>/api/health/ready
```
7. Install connector on operator machines and launch from UI.

Notes:
- In `csr` mode, nginx reload is skipped until signed cert is installed at:
- cert: `/etc/ssl/accessd/fullchain.pem`
- key: `/etc/ssl/accessd/privkey.pem`
- Installer publishes `/downloads/certs/accessd-server.crt` (when cert exists) for connector trust bootstrap.

---

## Table of Contents

1. [Deployment Architecture](#1-deployment-architecture)
2. [Filesystem Layout](#2-filesystem-layout)
3. [Prerequisites](#3-prerequisites)
4. [Build Steps](#4-build-steps)
5. [Server Setup](#5-server-setup)
6. [Environment Files](#6-environment-files)
7. [systemd Service](#7-systemd-service)
8. [nginx Configuration](#8-nginx-configuration)
9. [First-Start and Verification](#9-first-start-and-verification)
10. [Update / Rollout Workflow](#10-update--rollout-workflow)
11. [Validation Checklist](#11-validation-checklist)
12. [Secret Rotation](#12-secret-rotation)
13. [SSH Proxy Known Hosts](#13-ssh-proxy-known-hosts)
14. [Backup and Restore](#14-backup-and-restore)
15. [Security Hardening and Operational Safeguards](#15-security-hardening-and-operational-safeguards)
16. [Troubleshooting](#16-troubleshooting)

---

## 1. Deployment Architecture

### What runs on the Debian server

| Component         | Process                   | Listener                              |
|-------------------|---------------------------|---------------------------------------|
| AccessD API           | `accessd` (systemd)       | `127.0.0.1:8080` (HTTP, loopback only)|
| SSH Proxy         | inside `accessd`          | `0.0.0.0:2222` or internal interface  |
| Database proxies  | inside `accessd`          | `127.0.0.1` (loopback, session-scoped)|
| Redis proxy       | inside `accessd`          | `127.0.0.1` (loopback, session-scoped)|
| UI (static files) | nginx                     | `443` (HTTPS), `80` → redirect        |
| nginx (TLS edge)  | nginx                     | `80`, `443`                           |
| PostgreSQL        | local or remote DB host   | `5432` (network or local socket)      |

### What does NOT run on the server

| Component  | Where it runs           | Notes                                         |
|------------|-------------------------|-----------------------------------------------|
| Connector  | Operator's own machine  | Loopback-only on port 9494. Never server-side.|
| DBeaver    | Operator's own machine  | Launched by connector                         |
| PuTTY/SSH  | Operator's own machine  | Launched by connector                         |

### Architecture summary

```
  Operator machine                       AccessD Server (Debian)
  ───────────────                        ───────────────────
  Browser ──────── HTTPS ──────────────► nginx :443
                                           │  static /var/www/accessd/
                                           └──► accessd :8080 (loopback)
                                                  │  migrates DB on start
                                                  └──► PostgreSQL

  SSH client ──── TCP 2222 ────────────► accessd SSH proxy :2222
  (spawned by                                    │
   connector)                                    └──► target SSH host

  DBeaver ──────── TCP ephemeral ──────► accessd DB proxy (loopback session port)
  (spawned by                                    │
   connector)                                    └──► target database

  Browser ──────► connector :9494 (loopback on operator machine only)
```

The API HTTP listener is **not** exposed directly — nginx terminates TLS and forwards
`/api/` to `127.0.0.1:8080`. The UI is served as pre-built static files. The connector
is an operator-side process only and is **never** proxied through server-side nginx.

---

## 2. Filesystem Layout

```
/opt/accessd/                        Application directory (root:pam, 0755)
  bin/
    accessd                      API binary          (root:pam, 0755)
  migrations/                    SQL migrations      (root:pam, 0755)
    000001_core_v1.up.sql
    000002_auth_rbac.up.sql
    000003_assets_credentials_access_v1.up.sql
    000004_audit_events_search_v1.up.sql
    000005_session_audit_perf_v1.up.sql
    000006_ldap_admin_v1.up.sql

/etc/accessd/                        Config directory    (root:pam, 0750)
  accessd.env                    Runtime secrets     (root:pam, 0640)

/var/lib/accessd/                    Persistent runtime data (pam:pam, 0700)
  ssh/
    accessd_proxy_host_key       SSH proxy identity  (accessd:accessd, 0600, auto-generated)
    upstream_known_hosts         Target host keys    (pam:pam, 0600)

/var/www/accessd/                    Built UI assets     (root:www-data, 0755)
  index.html
  assets/
    *.js, *.css, ...

/etc/systemd/system/
  accessd.service                Service unit
/etc/nginx/sites-available/
  pam                            nginx vhost config
/etc/nginx/sites-enabled/
  pam -> ../sites-available/pam  (symlink)
```

**Rationale for each path:**

- `/opt/accessd/` — FHS-recommended for locally installed, self-contained application
  software. Binary and migrations live here. Not writable by the `pam` service user.
- `/etc/accessd/` — Configuration including secrets. The `pam` user can read but not write.
  Root-owned, `0640`, so the env file contents are not visible to other system users.
- `/var/lib/accessd/` — Persistent state that must survive restarts: the SSH proxy host key
  and the upstream known-hosts file. The `pam` user owns this directory exclusively.
- `/var/www/accessd/` — Static UI files served by nginx. Nginx user (`www-data`) can read.
  Not writable by the `pam` service user.

---

## 3. Prerequisites

### System packages

```bash
sudo apt-get update
sudo apt-get install -y \
  nginx \
  curl \
  ca-certificates \
  openssl
```

PostgreSQL client tools (for backup/restore, optional):

```bash
sudo apt-get install -y postgresql-client
```

### Go toolchain (build machine or server)

```bash
# Install Go 1.22+ — use the official tarball, not apt, for a current version.
curl -Lo /tmp/go.tar.gz https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
export PATH=$PATH:/usr/local/go/bin
go version
```

### Node.js toolchain (build machine)

```bash
# Node 20 LTS via NodeSource
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs
node --version && npm --version
```

### Service account

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin --home /opt/accessd accessd
```

---

## 4. Build Steps

Build on a CI/CD machine or on the server itself. The API binary is statically linked
(CGO disabled) and can be cross-compiled on any Linux/macOS machine.

### Build the API binary

```bash
cd apps/api

GIT_SHA=$(git rev-parse --short HEAD)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION="0.1.0"  # or read from a VERSION file

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
  -trimpath \
  -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${GIT_SHA} \
    -X main.builtAt=${BUILD_TIME}" \
  -o accessd-linux-amd64 \
  ./cmd/server

# Verify the binary is statically linked
file accessd-linux-amd64
# Expected: ELF 64-bit LSB executable, x86-64, statically linked
```

### Build the UI

```bash
cd apps/ui
npm ci                   # clean install from lockfile
npm run build            # outputs to apps/ui/dist/
```

Verify the build output:

```bash
ls apps/ui/dist/
# index.html  assets/
```

---

## 5. Server Setup

### Create directories

```bash
# Application directory
sudo mkdir -p /opt/accessd/{bin,migrations}
sudo chown -R root:accessd /opt/accessd
sudo chmod -R 755 /opt/accessd

# Config directory
sudo mkdir -p /etc/accessd
sudo chown root:accessd /etc/accessd
sudo chmod 750 /etc/accessd

# Runtime data directory
sudo mkdir -p /var/lib/accessd/ssh
sudo chown -R pam:pam /var/lib/pam
sudo chmod 700 /var/lib/pam
sudo chmod 700 /var/lib/accessd/ssh

# UI static files directory
sudo mkdir -p /var/www/pam
sudo chown root:www-data /var/www/pam
sudo chmod 755 /var/www/pam
```

### Install the API binary

```bash
sudo install -o root -g pam -m 0755 apps/api/accessd-linux-amd64 /opt/accessd/bin/accessd
```

### Install migration files

```bash
sudo cp apps/api/migrations/*.sql /opt/accessd/migrations/
sudo chown -R root:pam /opt/accessd/migrations
sudo chmod 755 /opt/accessd/migrations
sudo chmod 644 /opt/accessd/migrations/*.sql
```

### Install the UI

```bash
sudo cp -r apps/ui/dist/. /var/www/accessd/
sudo chown -R root:www-data /var/www/pam
sudo find /var/www/pam -type d -exec chmod 755 {} \;
sudo find /var/www/pam -type f -exec chmod 644 {} \;
```

### PostgreSQL setup

If PostgreSQL runs on the same host:

```bash
sudo apt-get install -y postgresql
sudo -u postgres psql <<'SQL'
  CREATE USER pam WITH PASSWORD 'CHANGE_ME_DB_PASSWORD';
  CREATE DATABASE pam OWNER pam;
  REVOKE ALL ON DATABASE pam FROM public;
SQL
```

If PostgreSQL is on a separate host, create the user/database there and ensure
`sslmode=require` or `sslmode=verify-full` in `ACCESSD_DB_URL`.

---

## 6. Environment Files

### API environment file

Copy and fill the example:

```bash
sudo cp deploy/env/accessd.env.example /etc/accessd/accessd.env
sudo chown root:pam /etc/accessd/accessd.env
sudo chmod 0640 /etc/accessd/accessd.env
```

Generate required secrets:

```bash
# Vault key (AES-256 master key for credential encryption)
openssl rand -base64 32

# Launch token HMAC secret
openssl rand -base64 32

# Connector HMAC secret (same value goes into accessd-connector.env on operator machines)
openssl rand -base64 32
```

Edit `/etc/accessd/accessd.env` and replace every `CHANGE_ME_*` placeholder. The minimum
required changes are:

| Variable                  | What to set                           |
|---------------------------|---------------------------------------|
| `ACCESSD_DB_URL`              | Real PostgreSQL connection string     |
| `ACCESSD_VAULT_KEY`           | `openssl rand -base64 32` output      |
| `ACCESSD_LAUNCH_TOKEN_SECRET` | `openssl rand -base64 32` output      |
| `ACCESSD_CONNECTOR_SECRET`    | `openssl rand -base64 32` output      |
| `ACCESSD_DEV_ADMIN_PASSWORD`  | Strong bootstrap admin password       |
| `ACCESSD_CORS_ALLOWED_ORIGINS`| Your actual domain (`https://pam.example.internal`) |
| `ACCESSD_SSH_PROXY_PUBLIC_HOST`| Internal hostname operators use for SSH |
| `ACCESSD_*_PROXY_PUBLIC_HOST` | Internal hostname operators use for DB proxies |
| LDAP variables            | Your AD/LDAP server details           |

Verify no placeholders remain:

```bash
sudo grep -n 'CHANGE_ME' /etc/accessd/accessd.env && echo "PLACEHOLDERS REMAIN — DO NOT START"
```

### Connector environment (operator-side)

Operators run the connector on their own machines. Provide them with a pre-filled
`accessd-connector.env` based on `deploy/env/accessd-connector.env.example`. The key values
that must match what's in `accessd.env`:

```
ACCESSD_CONNECTOR_SECRET=<same value as ACCESSD_CONNECTOR_SECRET in accessd.env>
ACCESSD_CONNECTOR_ALLOWED_ORIGIN=https://pam.example.internal
```

---

## 7. systemd Service

### Install the service unit

```bash
sudo cp deploy/systemd/accessd.service /etc/systemd/system/accessd.service
sudo systemctl daemon-reload
```

Review the unit file at `deploy/systemd/accessd.service`. Key settings:

```ini
WorkingDirectory=/opt/accessd
EnvironmentFile=/etc/accessd/accessd.env
ExecStartPre=/opt/accessd/bin/accessd migrate up    # fail-fast migration pre-check
ExecStart=/opt/accessd/bin/accessd server
```

The `server` command internally also runs migrations and bootstraps the admin account
on first start, so migrations are applied before the HTTP listener comes up regardless.

Hardening settings enabled (see `accessd.service` for the full list):

| Setting                  | Effect                                              |
|--------------------------|-----------------------------------------------------|
| `CapabilityBoundingSet=` | Drops all Linux capabilities                        |
| `SystemCallFilter=@system-service` | Allows only server-appropriate syscalls |
| `ProtectSystem=strict`   | Entire filesystem read-only except ReadWritePaths   |
| `ProtectHome=true`       | No access to home directories                       |
| `PrivateTmp=true`        | Private /tmp namespace                              |
| `UMask=0077`             | Created files (SSH key, known hosts) are owner-only |
| `NoNewPrivileges=true`   | No privilege escalation after start                 |
| `RestrictNamespaces=true`| Cannot create new namespaces                        |
| `ProtectKernelTunables=true` | /proc/sys is read-only                         |
| `LockPersonality=true`   | Execution domain locked                             |

### Enable and start

```bash
sudo systemctl enable accessd
sudo systemctl start accessd
```

Watch startup (includes migration output):

```bash
sudo journalctl -u accessd -f
```

Expected first-start sequence in logs:

```
startup phase begin phase=migrations
startup phase complete phase=migrations
startup phase begin phase=bootstrap
startup phase complete phase=bootstrap
http server listening addr=127.0.0.1:8080
```

---

## 8. nginx Configuration

### Install the site

```bash
sudo cp deploy/nginx/accessd.conf.example /etc/nginx/sites-available/pam
# Edit server_name and cert paths to match your environment:
sudo nano /etc/nginx/sites-available/pam

sudo ln -sf /etc/nginx/sites-available/pam /etc/nginx/sites-enabled/pam
# Disable the default site if it's enabled:
sudo rm -f /etc/nginx/sites-enabled/default
```

### TLS certificate

If you use `deploy/install_on_vm.sh`, TLS can be configured during install:
- script prompts for domain + TLS mode (`existing`, `self-signed`, `csr`, `skip`)
- `self-signed` generates `/etc/ssl/accessd/privkey.pem` + `/etc/ssl/accessd/fullchain.pem`
- `csr` generates `/etc/ssl/accessd/accessd.csr` and waits for signed cert before nginx reload

For an internal deployment with an internal CA:

```bash
# Generate a self-signed cert (testing only) or use your internal CA:
sudo mkdir -p /etc/ssl/accessd
sudo openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -keyout /etc/ssl/accessd/privkey.pem \
  -out /etc/ssl/accessd/fullchain.pem \
  -subj "/CN=accessd.example.internal" \
  -addext "subjectAltName=DNS:accessd.example.internal"
sudo chmod 640 /etc/ssl/accessd/privkey.pem
sudo chown root:www-data /etc/ssl/accessd/privkey.pem
```

For Let's Encrypt (if the server has a public DNS name):

```bash
sudo apt-get install -y certbot python3-certbot-nginx
sudo certbot --nginx -d accessd.example.internal
```

### Test and reload nginx

```bash
sudo nginx -t
sudo systemctl enable nginx
sudo systemctl reload nginx
```

### What the nginx config does

- **Port 80**: redirects all traffic to HTTPS.
- **Port 443 / `/`**: serves pre-built UI static files from `/var/www/accessd/` with
  SPA fallback (`try_files $uri /index.html`) so React Router handles deep links.
- **Port 443 / `/api/`**: proxies to `127.0.0.1:8080`, stripping the `/api/` prefix.
  The Go API receives requests at its own root (`/health/live`, `/auth/login`, etc.).
- **Security headers**: HSTS, X-Frame-Options, X-Content-Type-Options, CSP.
- **No `/connector/` location**: the connector is operator-local and must never be
  exposed through server-side nginx.

---

## 9. First-Start and Verification

### API health

```bash
curl -fs http://127.0.0.1:8080/health/ready && echo "API ready"
# Expected: {"status":"ready","db":"ok"}
```

### UI via nginx

```bash
curl -fsk https://pam.example.internal/ | grep -o '<title>[^<]*</title>'
# Expected: <title>AccessD</title> (or similar)
```

### Admin login

Navigate to `https://pam.example.internal/login` in a browser and sign in with
the bootstrap admin credentials you set in `ACCESSD_DEV_ADMIN_USERNAME` /
`ACCESSD_DEV_ADMIN_PASSWORD`.

**After first login, immediately:**

1. Go to Admin → Users and create a proper named admin account with LDAP-backed or
   strong local credentials.
2. Rotate the bootstrap admin password or deactivate the bootstrap account.

### LDAP configuration

1. Go to Admin → LDAP Settings.
2. Verify your LDAP settings are loaded from the env file.
3. Click "Test Connection" to confirm LDAP reachability and bind.
4. Trigger a manual sync to populate LDAP users.

### SSH proxy reachability

From an operator machine:

```bash
ssh -p 2222 -o "StrictHostKeyChecking=no" pam@pam.example.internal
# Expect: connection refused or auth failure (not "connection refused" at network level)
# The proxy responds to SSH connections even before a session is launched.
```

---

## 10. Update / Rollout Workflow

### Step 1 — Build new artifacts

```bash
# On build machine:
# (Repeat build commands from §4 with updated version/SHA)
GIT_SHA=$(git rev-parse --short HEAD)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${GIT_SHA} -X main.builtAt=${BUILD_TIME}" \
  -o accessd-linux-amd64 \
  ./cmd/server

cd apps/ui && npm ci && npm run build
```

### Step 2 — Deploy new API binary

```bash
# Copy new binary alongside the running one first
sudo install -o root -g pam -m 0755 accessd-linux-amd64 /opt/accessd/bin/accessd.new

# Copy any new migration files
sudo cp apps/api/migrations/*.sql /opt/accessd/migrations/
sudo chown root:pam /opt/accessd/migrations/*.sql
sudo chmod 644 /opt/accessd/migrations/*.sql

# Atomic swap (avoids replacing the running binary in-place)
sudo mv /opt/accessd/bin/accessd.new /opt/accessd/bin/accessd
```

### Step 3 — Deploy new UI

```bash
# Copy into a staging path, then swap atomically to minimise served-from-disk window
sudo rm -rf /var/www/pam.new
sudo cp -r apps/ui/dist /var/www/pam.new
sudo chown -R root:www-data /var/www/pam.new
sudo find /var/www/pam.new -type d -exec chmod 755 {} \;
sudo find /var/www/pam.new -type f -exec chmod 644 {} \;

sudo mv /var/www/pam /var/www/pam.old
sudo mv /var/www/pam.new /var/www/pam
```

### Step 4 — Restart the service

The `server` command runs pending migrations on startup, so restarting applies
any new migrations automatically.

```bash
sudo systemctl restart accessd
sudo journalctl -u accessd -f --lines=50
```

Wait for `http server listening` in the logs.

### Step 5 — Verify health

```bash
curl -fs http://127.0.0.1:8080/health/ready && echo "API healthy"
curl -fsk https://pam.example.internal/api/version
```

### Step 6 — Cleanup

```bash
sudo rm -rf /var/www/pam.old
```

### Rollback

If something is wrong after the restart:

```bash
# Roll back binary (if you kept a copy)
sudo install -o root -g pam -m 0755 /opt/accessd/bin/accessd.prev /opt/accessd/bin/accessd
sudo systemctl restart accessd

# Roll back UI
sudo mv /var/www/pam /var/www/pam.broken
sudo mv /var/www/pam.old /var/www/pam

# Note: migration rollback is not automated. If a migration needs to be reversed,
# it must be done manually with a corresponding down SQL. Keep accessd.prev around
# until you're confident the deployment is stable.
```

**Best practice:** keep the previous binary at `/opt/accessd/bin/accessd.prev` during the
verification window. Once stable, remove it:

```bash
sudo cp /opt/accessd/bin/accessd /opt/accessd/bin/accessd.prev
```

---

## 11. Validation Checklist

After every deployment, verify the following before declaring it healthy:

### API health

- [ ] `curl -fs http://127.0.0.1:8080/health/live` returns `{"status":"ok"}`
- [ ] `curl -fs http://127.0.0.1:8080/health/ready` returns `{"status":"ready","db":"ok"}`
- [ ] `systemctl status accessd` shows `active (running)`
- [ ] `journalctl -u accessd --since="5 minutes ago"` shows no error-level entries

### UI access

- [ ] `https://pam.example.internal/` loads the login page in a browser
- [ ] Browser console shows no CSP violations or mixed-content warnings
- [ ] HTTPS certificate is valid and trusted by the operator's browser

### Admin login

- [ ] Login with admin credentials succeeds
- [ ] `/api/me` returns the correct user profile
- [ ] Admin sidebar is visible (Users, Assets, Sessions, Audit)

### LDAP (if configured)

- [ ] Admin → LDAP → Test Connection returns success
- [ ] A manual sync run completes without error
- [ ] At least one LDAP user appears in the Users list
- [ ] LDAP user can log in with their directory credentials
- [ ] LDAP group-to-role mappings are reflected correctly on user profiles

### Assets and access

- [ ] At least one asset is visible in Admin → Assets
- [ ] An access grant exists for a test user
- [ ] The access grant appears in that user's My Access view

### Audit visibility

- [ ] Admin → Audit shows a login event for the verification login
- [ ] Event detail is viewable and shows correct metadata

### Launcher flows (from an operator machine with connector installed)

- [ ] Connector is running on the operator machine (`curl -s http://127.0.0.1:9494/healthz`)
- [ ] Shell launch from My Access opens a native terminal window
- [ ] Session appears in Admin → Sessions as active
- [ ] After terminal exit, session shows as completed
- [ ] Session events and transcript are visible in Admin → Sessions → session detail
- [ ] (If DB assets configured) DBeaver launch creates an active session
- [ ] (If Redis configured) Redis-CLI launch creates an active session

---

## 12. Secret Rotation

### Vault key (`ACCESSD_VAULT_KEY`)

The vault key encrypts stored asset credentials. Rotation requires re-encrypting all
credentials — this is currently a manual process (v1 limitation).

```bash
# 1. Generate new key
openssl rand -base64 32

# 2. Re-encrypt all stored credentials using a migration script (contact the
#    development team for tooling — no automated rotation script yet).

# 3. Update /etc/accessd/accessd.env
sudo nano /etc/accessd/accessd.env   # replace ACCESSD_VAULT_KEY and optionally ACCESSD_VAULT_KEY_ID

# 4. Restart
sudo systemctl restart accessd
```

Do not rotate the vault key without completing step 2 — the service will be unable
to decrypt existing credentials.

### Launch token secret (`ACCESSD_LAUNCH_TOKEN_SECRET`)

```bash
openssl rand -base64 32
# Update /etc/accessd/accessd.env, then:
sudo systemctl restart accessd
```

In-flight launch tokens at the time of restart will be invalidated. Any operator who
clicked "Launch" within the token TTL window (default 2 minutes) will need to re-launch.

### Connector secret (`ACCESSD_CONNECTOR_SECRET`)

The same secret must be present on both the API and each operator's connector.

```bash
openssl rand -base64 32
# 1. Update ACCESSD_CONNECTOR_SECRET in /etc/accessd/accessd.env
# 2. Distribute the new value to all operators (update their accessd-connector.env)
# 3. Coordinate the cutover: restart API, then have operators restart their connectors.
sudo systemctl restart accessd
```

There is a brief window between API restart and connector restart where launch
verification will fail for operators using the old secret. Coordinate with operators
to minimize this window.

### LDAP credentials and TLS material

```bash
# Update Directory & LDAP in Admin UI, then:
sudo systemctl restart accessd
```

---

## 13. SSH Proxy Known Hosts

AccessD uses a `known_hosts` file to verify upstream target SSH host keys. This prevents
MITM attacks when the proxy connects to target servers on behalf of operators.

**Adding a new target host:**

```bash
# On the AccessD server, scan the target host's key
ssh-keyscan -t ed25519,rsa <target-host> | sudo tee -a /var/lib/accessd/ssh/upstream_known_hosts
sudo chmod 600 /var/lib/accessd/ssh/upstream_known_hosts
sudo chown pam:pam /var/lib/accessd/ssh/upstream_known_hosts
```

No service restart is needed — the proxy reads the known-hosts file on each connection.

**Rotating a target host key** (after reprovisioning a server):

```bash
# Remove old entry
ssh-keygen -R <target-host> -f /var/lib/accessd/ssh/upstream_known_hosts
# Add new key
ssh-keyscan -t ed25519,rsa <target-host> | sudo tee -a /var/lib/accessd/ssh/upstream_known_hosts
```

**SSH proxy host key** (the key the proxy presents to operators' SSH clients):

The host key is auto-generated by AccessD at `/var/lib/accessd/ssh/accessd_proxy_host_key` on first
start. It will be created with `0600` permissions due to the service `UMask=0077`.

Back this file up after first deployment. If it is lost and regenerated, operators will
see an SSH host key mismatch warning and will need to update their `~/.ssh/known_hosts`.

```bash
# Backup:
sudo cp /var/lib/accessd/ssh/accessd_proxy_host_key /secure/backup/path/accessd_proxy_host_key
```

---

## 14. Backup and Restore

### PostgreSQL backup

```bash
# Full database dump (custom format, compressed):
pg_dump -Fc -h 127.0.0.1 -U pam -d pam > pam_backup_$(date +%Y%m%d_%H%M%S).dump

# For remote DB:
pg_dump -Fc -h db.example.internal -U pam -d pam > pam_backup_$(date +%Y%m%d_%H%M%S).dump
```

### PostgreSQL restore

```bash
pg_restore -h 127.0.0.1 -U pam -d pam --clean --if-exists pam_backup_YYYYMMDD_HHMMSS.dump
# After restore, restart to re-validate connections:
sudo systemctl restart accessd
```

### What to include in backups

| Item                                     | Location                                |
|------------------------------------------|-----------------------------------------|
| PostgreSQL database dump                 | `pg_dump` output                        |
| API env file (contains secrets)          | `/etc/accessd/accessd.env`                  |
| SSH proxy host key                       | `/var/lib/accessd/ssh/accessd_proxy_host_key`   |
| Upstream known-hosts file                | `/var/lib/accessd/ssh/upstream_known_hosts` |

Store backups encrypted (the env file contains master keys). Do not include the raw
env file in unencrypted storage.

---

## 15. Security Hardening and Operational Safeguards

This section documents the security posture of this deployment, specific risks that
were identified and addressed, and operational do/don't guidance.

---

### 15.1 Secrets in environment files

**Risk:** The env file at `/etc/accessd/accessd.env` contains the database password,
AES vault key, HMAC launch token secret, connector HMAC secret, and LDAP bind password.
If this file is world-readable or stored in version control, all secrets are exposed.

**What was done:**
- File permissions set to `0640` (owner: root, group: pam). Only root and the `pam`
  service user can read it.
- The `deploy/env/accessd.env.example` file in this repository contains only placeholder
  values (`CHANGE_ME_*`). It must never be committed with real values.
- The systemd service reads the env file directly via `EnvironmentFile=` and does not
  pass secrets on the command line (which would expose them in `ps aux`).

**Do:**
- Store production env files in an encrypted secrets manager (Vault, AWS SSM, etc.) and
  render to disk only at deploy time.
- Restrict SSH access to the AccessD server to named accounts with audit logging.

**Don't:**
- Commit `/etc/accessd/accessd.env` to git.
- Copy the env file to a world-readable location.
- Log the env file contents in CI/CD pipelines.

---

### 15.2 API HTTP listener binding

**Risk:** If `ACCESSD_HTTP_ADDR` is set to `0.0.0.0:8080` or `:8080`, the API is directly
reachable on all interfaces without TLS. An incorrectly configured firewall could expose
unauthenticated API endpoints to the network.

**What was done:**
- `ACCESSD_HTTP_ADDR=127.0.0.1:8080` is the default in `accessd.env.example`. This binds
  the API listener exclusively to loopback — unreachable from any network interface.
- nginx proxies to the loopback address. TLS is terminated at nginx.

**Do:**
- Always verify `ACCESSD_HTTP_ADDR` is loopback-bound before deployment.
- Use a host-based firewall (`ufw` or `iptables`) to block direct access to port 8080
  from non-loopback sources as a defence-in-depth measure.

```bash
sudo ufw deny 8080/tcp
sudo ufw allow 443/tcp
sudo ufw allow 2222/tcp   # only if SSH proxy needs to be reachable from operators
sudo ufw enable
```

---

### 15.3 SSH proxy binding and connector trust model

**Risk:** The SSH proxy (`ACCESSD_SSH_PROXY_ADDR`) may need to be reachable from operator
machines. If bound to `0.0.0.0:2222`, it is reachable from the internet if the
server has a public IP.

The SSH proxy authenticates operators via HMAC launch tokens. A valid launch token is
short-lived (2 minutes) and session-scoped. However, an internet-exposed SSH port
invites brute-force scanning.

The connector is an operator-local process. If `ACCESSD_CONNECTOR_SECRET` is not set, any
process on the operator's machine can call the connector and trigger a launch.

**What was done:**
- `ACCESSD_SSH_PROXY_ADDR=0.0.0.0:2222` is shown in the env example with a comment
  explaining this should be scoped to an internal interface if possible.
- `ACCESSD_CONNECTOR_SECRET` is marked `[REQUIRED]` in both env examples. Without it,
  connector authentication is disabled.
- `ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE=accept-new` is the default and persists
  host fingerprints into `/var/lib/accessd/ssh/upstream_known_hosts` as targets are reached.
  Only `insecure` remains blocked in production unless `ACCESSD_ALLOW_UNSAFE_MODE=true`.

**Do:**
- Bind the SSH proxy to an internal/VPN-only interface rather than `0.0.0.0` where
  possible: `ACCESSD_SSH_PROXY_ADDR=10.0.0.5:2222`.
- Set `ACCESSD_CONNECTOR_SECRET` to the same value on both API and all operator connectors.
- Treat the SSH proxy port like any other privileged management port: restrict by firewall
  to operator subnet/VPN ranges only.
- Monitor SSH proxy logs for `accepted upstream host key` events and verify recorded
  `fingerprint_sha256` values against your source of truth.

**Don't:**
- Set `ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE=insecure` in production.
- Leave `ACCESSD_CONNECTOR_SECRET` empty in a multi-user environment.

---

### 15.4 Connector must never be proxied through server-side nginx

**Risk:** The original `pam-edge.conf` in this repository included a `/connector/`
nginx location that proxied to `http://127.0.0.1:9494`. This is **architecturally wrong
and dangerous**: it would make the connector API reachable from the internet, allowing
any client to trigger native application launches on the server.

**What was done:**
- The `/connector/` location has been removed from both `pam-edge.conf` and
  `accessd.conf.example`.
- Both files now include an explicit comment documenting why it must not be added.
- The connector env example binding (`ACCESSD_CONNECTOR_ADDR=127.0.0.1:9494`) keeps the
  connector loopback-only on the operator's machine.

**Do:**
- Deploy the connector only on operator machines.
- Distribute connector builds directly to operators.
- The connector CORS setting (`ACCESSD_CONNECTOR_ALLOWED_ORIGIN`) should match the AccessD
  web UI origin. Operators set this to `https://pam.example.internal`, not `localhost`.

**Don't:**
- Add a `/connector/` nginx location on the server.
- Set `ACCESSD_CONNECTOR_ALLOW_REMOTE=true` unless you have an explicit, audited reason.
- Set `ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN=true` ever.

---

### 15.5 Bootstrap admin credentials in production

**Risk:** The config has `ACCESSD_DEV_ADMIN_USERNAME=admin` and `ACCESSD_DEV_ADMIN_PASSWORD=admin123`
as default values. The `DEV` prefix is misleading — these values are used in production
if not overridden. A deployment that forgets to change these has a well-known admin account.

**What was done:**
- Both `ACCESSD_DEV_ADMIN_USERNAME` and `ACCESSD_DEV_ADMIN_PASSWORD` are now explicitly included
  in `accessd.env.example` with `CHANGE_ME_*` placeholders marked `[REQUIRED — change]`.
- The `grep CHANGE_ME` verification step in §6 will catch unfilled placeholders.

**Do:**
- Change `ACCESSD_DEV_ADMIN_PASSWORD` to a randomly generated strong password on every deployment.
- After first login, create named admin accounts for humans and deactivate or rotate
  the bootstrap account.
- Configure LDAP provider mode in Admin UI (`Directory & LDAP`) so local accounts are
  not the primary auth path in production.

**Don't:**
- Leave the default `admin`/`admin123` credentials in production even for "temporary" use.

---

### 15.6 nginx security headers

**Risk:** Without a Content-Security-Policy, XSS attacks against the AccessD UI could allow
credential theft or session hijacking. Without `X-Frame-Options: DENY`, the UI could be
framed in a clickjacking attack.

**What was done:**
- Both nginx configs now include: HSTS, `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `Permissions-Policy`, and a `Content-Security-Policy`.
- The CSP restricts script sources to `'self'`, allows `connect-src` to include the
  operator's local connector at `http://127.0.0.1:9494`, and sets `frame-ancestors 'none'`.
- `X-Frame-Options` upgraded from `SAMEORIGIN` to `DENY` — there is no legitimate
  framing use case for AccessD.

**CSP note:** Test the CSP against your actual UI build. The included CSP is conservative.
If xterm.js or another library requires `'unsafe-eval'` or `'wasm-unsafe-eval'`, add only
the minimum required to the CSP and document why.

---

### 15.7 nginx serving dev server (stale config)

**Risk:** The previous `pam-edge.conf` proxied `/` to `http://127.0.0.1:3000`, which is
the Vite development server. In a production deployment where no Vite dev server is
running, this would result in a 502 for every UI request.

**What was done:**
- Both nginx configs now serve static files directly from `/var/www/accessd/` using
  `root /var/www/pam` and `try_files $uri $uri/ /index.html`.
- A `location` block for hashed static assets sets long cache TTLs.
- The Vite dev server (`npm run dev`) is only for local development and plays no role
  in production.

---

### 15.8 systemd hardening

**Risk:** A compromised service process running with default systemd settings can access
any file on the filesystem, create network sockets to arbitrary addresses, load kernel
modules, and change system time.

**What was done:**
The service unit was updated with additional hardening beyond the original:

| Addition                          | Effect                                               |
|-----------------------------------|------------------------------------------------------|
| `ProtectSystem=strict`            | Entire filesystem read-only (was `full`)             |
| `CapabilityBoundingSet=`          | All capabilities dropped                             |
| `SystemCallFilter=@system-service`| Syscall allowlist                                    |
| `UMask=0077`                      | Created files (SSH keys) are owner-only by default   |
| `LockPersonality=true`            | Execution domain locked                              |
| `RestrictNamespaces=true`         | Cannot create new namespaces                         |
| `RestrictAddressFamilies=...`     | Only AF_INET, AF_INET6, AF_UNIX allowed              |
| `ProtectKernelTunables=true`      | /proc/sys read-only                                  |
| `ProtectKernelModules=true`       | Cannot load kernel modules                           |
| `ProtectKernelLogs=true`          | No kernel log access                                 |
| `ProtectClock=true`               | Cannot change system clock                           |
| `ProtectControlGroups=true`       | cgroups hierarchy read-only                          |
| `ProtectProc=invisible`           | Other processes not visible in /proc                 |
| `RemoveIPC=true`                  | IPC namespaces cleaned up on exit                    |
| `RestrictSUIDSGID=true`           | Cannot create SUID/SGID files                        |

`MemoryDenyWriteExecute` is intentionally not set. Modern Go runtime does not need
writable+executable memory, but some versions may use it for certain stack operations.
Audit this on your target Go version before enabling.

---

### 15.9 Credential leakage in logs

**Risk:** The API logs startup metadata including CORS origins and proxy addresses. Go
structured log JSON could inadvertently capture secrets if a misconfigured log handler
echoes environment variables.

**What was confirmed:**
- Startup logs (`main.go`) log configuration shape (addresses, booleans, timeouts) but
  not secret values. `ACCESSD_VAULT_KEY`, `ACCESSD_LAUNCH_TOKEN_SECRET`, and
  `ACCESSD_CONNECTOR_SECRET` are not logged.
- `connector_trust` is logged as a boolean (whether the secret is set), not the secret value.

**Do:**
- Route logs to journald (default with systemd) rather than to files with permissive ACLs.
- Restrict journalctl access: only members of the `adm` group or root can read system journals.
- If shipping logs to a SIEM, scrub any `password`, `key`, `secret`, `token` fields.

---

### 15.10 Database connection security

**Risk:** `sslmode=require` in `ACCESSD_DB_URL` validates that the connection uses TLS but
does not verify the server certificate. A MITM on the database path is possible.

**Recommendation:**
- Use `sslmode=verify-full` with a CA certificate if the PostgreSQL server has a
  certificate signed by an internal CA:
  ```
  ACCESSD_DB_URL=postgres://pam:password@db.example.internal:5432/pam?sslmode=verify-full&sslrootcert=/etc/ssl/certs/internal-ca.pem
  ```
- On a same-host deployment (loopback socket), `sslmode=disable` is acceptable since
  traffic does not leave the host, but `sslmode=require` is still preferred.

---

### 15.11 Stale temp files and credential leak via DBeaver

**Risk:** The connector writes temporary DBeaver connection spec files containing
database credentials to disk. If these are not cleaned up, they could be read by other
processes on the operator's machine.

**What was done:**
- `ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL=15m` is the default: temp files are deleted after 15
  minutes.
- The connector runs on the operator's own machine (not the server), so the exposure is
  limited to the operator's local filesystem.

**Do:**
- Ensure the operator's machine has appropriate filesystem permissions to prevent other
  local users from reading the temp directory.
- Keep `ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL` at 15 minutes or lower.

---

### 15.12 Audit integrity

**Risk:** An admin with direct database access can tamper with audit records, undermining
the integrity of the audit trail.

**Intentionally deferred:**
- The `audit_events` and `session_events` tables are append-only by application logic
  but are not protected by database-level triggers that prevent UPDATE/DELETE for the
  application user.
- For high-assurance environments, consider:
  - Creating a separate read-only audit DB user for the application user's audit queries.
  - Using a PostgreSQL row-level trigger that denies DELETE and UPDATE on these tables.
  - Shipping audit events to an immutable external sink (S3 with object lock, SIEM, etc.).

---

### 15.13 ACCESSD_ALLOW_UNSAFE_MODE in production

**Risk:** Setting `ACCESSD_ALLOW_UNSAFE_MODE=true` bypasses several production safety checks:
vault key format validation, LDAP insecure skip-verify, SSH `insecure` host key mode,
and session cookie secure flag. It should never be set in production.

**What was done:**
- `ACCESSD_ALLOW_UNSAFE_MODE=false` is set explicitly in `accessd.env.example`.
- The `grep CHANGE_ME` check verifies that this file has been correctly filled before
  deployment. The safe value is already correct in the example.

---

## 16. Troubleshooting

### Service fails to start

```bash
sudo journalctl -u accessd --lines=100 --no-pager
```

Common causes:

| Log message                                    | Fix                                                      |
|------------------------------------------------|----------------------------------------------------------|
| `ACCESSD_DB_URL is required`                       | Missing or empty ACCESSD_DB_URL in env file                  |
| `ACCESSD_VAULT_KEY is required`                    | Missing or empty ACCESSD_VAULT_KEY                           |
| `ACCESSD_VAULT_KEY must be base64-encoded 32 bytes`| Generate correctly: `openssl rand -base64 32`            |
| `ACCESSD_LAUNCH_TOKEN_SECRET is required`          | Generate and set the secret                              |
| `dial error` / `connection refused`            | PostgreSQL not running or wrong host/port in DB_URL      |
| `migrations: ...`                              | Migration SQL error — check for schema conflicts         |

### API returns 502 Bad Gateway

nginx is reaching the API but getting no response:

```bash
systemctl status accessd
curl -fs http://127.0.0.1:8080/health/live
```

### Failed logins

```bash
sudo journalctl -u accessd | grep '"action":"login"'
```

| Log event                    | Cause                                                    |
|------------------------------|----------------------------------------------------------|
| `login_failed_user_not_found`| Username not in LDAP or local DB                         |
| `login_failed_invalid_password`| Wrong password                                         |
| `login_failed_ldap_error`    | LDAP unreachable, TLS error, or wrong bind credentials   |
| `login_failed_rate_limited`  | Too many failures; 5-minute lockout window               |

### Connector launch fails

```bash
# On operator machine:
curl -s http://127.0.0.1:9494/healthz
# Check connector logs for "rejected" entries (connector_token invalid/missing)
```

Ensure `ACCESSD_CONNECTOR_SECRET` matches on both API and connector, and that the connector
was restarted after the last secret rotation.

### SSH proxy host key mismatch

Operators see "REMOTE HOST IDENTIFICATION HAS CHANGED" when connecting to the proxy.
This means the SSH proxy host key was regenerated (server rebuilt, key file deleted).

```bash
# Operator removes old entry from their known_hosts:
ssh-keygen -R [pam.example.internal]:2222 ~/.ssh/known_hosts
```

To prevent this: back up `/var/lib/accessd/ssh/accessd_proxy_host_key` and restore it after
any server rebuild.

### MSSQL proxy limitations

Full client↔proxy TDS TLS tunnel mode is not implemented in this release. MSSQL
connections via DBeaver use the proxy but without end-to-end TLS on the client-facing
side. Upstream TLS (AccessD → target MSSQL) is supported.

### Redis CLI (connector side)

The connector's `redis-cli` connects to the AccessD Redis proxy endpoint on loopback without
TLS. This is the session-scoped proxy port on the operator's machine, not a direct
network connection to Redis. Upstream Redis TLS from the AccessD server to the target Redis
instance is supported.
