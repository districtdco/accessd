# deploy/

Production-oriented deployment assets for AccessD on Debian + systemd.

For local multi-protocol testing, see [`LOCAL_TESTING.md`](../LOCAL_TESTING.md).

## Canonical Assets

| File | Purpose |
|------|---------|
| `systemd/accessd.service` | Hardened systemd unit for the API |
| `systemd/accessd-connector.service` | Optional connector service unit (managed-desktop mode only) |
| `env/accessd.env.example` | Production environment file template |
| `env/accessd-connector.env.example` | Connector environment template |
| `nginx/accessd.conf.example` | Production nginx vhost (static UI + API proxy) |
| `install_on_vm.sh` | VM installer helper for deployment bundle layout |
| `../docs/CONNECTOR_DISTRIBUTION.md` | Connector release, install, and update model |

## Deployment Layout

```
/opt/accessd/
  bin/accessd               API binary
  migrations/               SQL migration files

/etc/accessd/               Config + secrets (root:accessd, 0750)
  accessd.env               Runtime env (root:accessd, 0640)

/var/lib/accessd/           Persistent runtime data (accessd:accessd, 0700)
  ssh/
    accessd_proxy_host_key  SSH proxy identity key (0600)
    upstream_known_hosts    Target host keys (0600)

/var/www/accessd/           Built UI static files (root:www-data, 0755)
/var/www/accessd-downloads/ Connector release binaries served by nginx
  connectors/
    v0.1.0/
      accessd-connector-0.1.0-*.tar.gz|zip
      accessd-connector-0.1.0-checksums.txt
```

## Quick Steps

```bash
# 1. Copy and fill the env file
sudo cp deploy/env/accessd.env.example /etc/accessd/accessd.env
sudo chmod 0640 /etc/accessd/accessd.env
# Edit /etc/accessd/accessd.env — fill all CHANGE_ME_* values
# Verify: sudo grep -n 'CHANGE_ME' /etc/accessd/accessd.env
# Installer refuses to start services if required secrets still contain CHANGE_ME.

# 2. Install and enable the service
sudo cp deploy/systemd/accessd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now accessd

# 3. Verify
curl -fs http://127.0.0.1:8080/health/ready && echo "API ready"
sudo journalctl -u accessd -f
```

See [DEPLOY.md](../DEPLOY.md) for the complete guide.

Bundle install helper:

```bash
# After copying/extracting deploy bundle on VM
cd /tmp/accessd-<version>/deploy
sudo ./install_on_vm.sh
```

This maps bundle contents into default VM paths:
- `/opt/accessd` (binary + migrations)
- `/etc/accessd` (env files)
- `/var/www/accessd` (UI)
- `/var/www/accessd-downloads/connectors/<tag>` (connector downloads for nginx)

Installer behavior:
- first-run bootstrap can install nginx (and local postgres when DB URL is localhost)
- configures and enables `accessd` service, and enables/reloads nginx
- reruns are update-safe (binary/UI/templates refreshed, services restarted/reloaded)
- existing `/etc/accessd/*.env` are preserved; new templates are written as `*.example.new`

Useful installer flags:
- `INSTALL_NGINX=true|false` (default: `true`)
- `INSTALL_POSTGRES=auto|true|false` (default: `auto`)
- `TLS_SETUP_MODE=prompt|existing|self-signed|csr|skip` (default: `prompt`)
- `ACCESSD_DOMAIN=accessd.example.internal` (used for nginx `server_name` + env placeholders)
- `ACCESSD_TLS_CERT_DIR=/etc/ssl/accessd` (default cert dir)
- `ACCESSD_TLS_CERT_PATH=/etc/ssl/accessd/fullchain.pem` (nginx cert path)
- `ACCESSD_TLS_KEY_PATH=/etc/ssl/accessd/privkey.pem` (nginx key path)
- `ACCESSD_TLS_CSR_PATH=/etc/ssl/accessd/accessd.csr` (CSR output path when `TLS_SETUP_MODE=csr`)
- `ACCESSD_CONNECTOR_TAG=vX.Y.Z` (override connector artifact tag to publish)
- `PUBLISH_OPERATOR_TLS_CERT=true|false` (default: `true`; publishes `/downloads/certs/accessd-server.crt` when source cert exists)
- `ACCESSD_PUBLIC_CERT_SOURCE=/path/to/fullchain.pem` (default: `/etc/ssl/accessd/fullchain.pem`)
- `PUBLISH_OPERATOR_CONNECTOR_ENV=true|false` (default: `true`; publishes `/downloads/bootstrap/accessd-connector.env`)
- `PUBLISH_OPERATOR_CONNECTOR_SECRET=true|false` (default: `false`; include `ACCESSD_CONNECTOR_SECRET` in published operator env)

Interactive behavior:
- With `TLS_SETUP_MODE=prompt`, installer asks for domain and TLS mode.
- `self-signed` generates cert/key directly (quick lab setup).
- `csr` generates key + CSR and skips nginx reload until signed cert is installed.

Connector runtime note:

- In normal operator setups, connector is started on-demand by the UI via `accessd-connector://start` after installer protocol registration.
- `systemd/accessd-connector.service` is not required for this default flow.
- Connector installers can auto-trust the AccessD HTTPS cert on operator machines:
  - Default source URL: `https://<ui-domain>/downloads/certs/accessd-server.crt`
  - Override with `ACCESSD_CONNECTOR_TRUST_CERT_URL`
  - Disable with `ACCESSD_CONNECTOR_AUTO_TRUST_SERVER_CERT=false`
- Connector installers can auto-bootstrap runtime env on first run:
  - Default source URL: `https://<ui-domain>/downloads/bootstrap/accessd-connector.env`
  - Override with `ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL`
  - Existing local `~/.config/accessd/connector.env` is never overwritten.
  - Preferred verification model: connector uses `ACCESSD_CONNECTOR_BACKEND_VERIFY_URL` (`/api/connector/token/verify`) so operator machines do not need shared secret distribution.

## Secrets

All three of these secrets must be generated and populated before first start:

```bash
# Vault key (AES-256 encryption key for stored credentials)
openssl rand -base64 32   # → ACCESSD_VAULT_KEY

# Launch token HMAC secret
openssl rand -base64 32   # → ACCESSD_LAUNCH_TOKEN_SECRET

# Connector HMAC secret (same value in both accessd.env and accessd-connector.env)
openssl rand -base64 32   # → ACCESSD_CONNECTOR_SECRET
```

Connector hardening default:
- `ACCESSD_CONNECTOR_ALLOW_INSECURE_NO_TOKEN=false` (recommended default)
- Set `true` only for temporary local debugging; never for production rollout.

LDAP configuration:
- Configure LDAP from Admin UI (`Directory & LDAP`) after first admin login.
- LDAP provider mode, bind settings, filters, and TLS CA PEM are persisted in DB (`ldap_settings`).
- Do not keep LDAP runtime settings in `/etc/accessd/accessd.env`; this avoids env/UI drift.
