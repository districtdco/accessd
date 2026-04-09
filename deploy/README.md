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

See [DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md](../DEPLOY_PRIVATE_DEBIAN_SYSTEMD.md) for the complete guide.

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
- `ACCESSD_CONNECTOR_TAG=vX.Y.Z` (override connector artifact tag to publish)

Connector runtime note:

- In normal operator setups, connector is started on-demand by the UI via `accessd-connector://start` after installer protocol registration.
- `systemd/accessd-connector.service` is not required for this default flow.

## Legacy PAM_* Variables

AccessD automatically migrates `PAM_*` environment variables to `ACCESSD_*` equivalents on startup. Existing deployments using `PAM_*` continue to work without changes.

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

LDAP with Samba AD self-signed/private CA:
- Keep `ACCESSD_LDAP_INSECURE_SKIP_VERIFY=false`.
- Set `ACCESSD_LDAP_CA_CERT_FILE` to the PEM CA certificate path on the server host.
- Example: `/etc/accessd/certs/samba-ad-ca.pem`.
