# deploy/

Production-oriented deployment assets for a Linux VM + `systemd` target.

## Included Assets

- `systemd/pam-api.service`
  - API service unit.
  - Runs migrations in `ExecStartPre` (`pam-api migrate up`) so startup fails fast if migrations fail.
- `env/pam-api.env.example`
  - Example environment file for API runtime.
  - Includes hardened defaults (`PAM_AUTH_COOKIE_SECURE=true`, `PAM_AUTH_COOKIE_SAMESITE=strict`, `PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE=known-hosts`).
- `systemd/pam-connector.service` (optional)
  - Optional connector service if you choose to run connector on the same Linux VM.
  - Default connector posture remains loopback-only.
- `env/pam-connector.env.example`
  - Example connector environment file with explicit unsafe toggles set to `false`.

## Suggested Deployment Layout

```text
/opt/pam/
  bin/pam-api
  bin/pam-connector
  apps/api/            # optional if running from source tree
  apps/connector/      # optional if running from source tree
/etc/pam/pam-api.env
/etc/pam/pam-connector.env
```

## API Deployment Steps

1. Build artifact:
   - `cd apps/api && go build -ldflags "-X main.version=<version> -X main.commit=<sha> -X main.builtAt=<utc-rfc3339>" -o /opt/pam/bin/pam-api ./cmd/server`
2. Install env file:
   - copy `deploy/env/pam-api.env.example` to `/etc/pam/pam-api.env` and fill real secrets.
3. Install unit:
   - copy `deploy/systemd/pam-api.service` to `/etc/systemd/system/pam-api.service`
4. Reload + start:
   - `sudo systemctl daemon-reload`
   - `sudo systemctl enable --now pam-api`
5. Verify:
   - `curl -f http://127.0.0.1:8080/health/ready`
   - `systemctl status pam-api`

## HTTPS / TLS Requirement

- The PAM API in this slice listens on HTTP only (`PAM_HTTP_ADDR`).
- Production deployments must place a reverse proxy or load balancer in front of PAM that:
  - terminates HTTPS/TLS for browser/API traffic
  - forwards trusted requests to PAM over private network interfaces
  - preserves correlation headers such as `X-Request-Id`
- Do not expose the raw API listener directly on the public internet.

## Connector Deployment Note

Connector is usually operator-local, not a shared remote service. Use `pam-connector.service` only when your operational model explicitly requires VM-local connector hosting.

Docker Compose remains for local development only (`docker-compose.yml` in repo root).

---

## Operations Guide

### Rotating Secrets

**Vault Key (`PAM_VAULT_KEY`)**
1. Generate new 32-byte base64 key: `openssl rand -base64 32`
2. Re-encrypt all credentials in the database using a migration script (not yet automated — v1 limitation)
3. Update `/etc/pam/pam-api.env` with new key
4. Restart: `sudo systemctl restart pam-api`

**Launch Token Secret (`PAM_LAUNCH_TOKEN_SECRET`)**
1. Generate new secret: `openssl rand -base64 32`
2. Update `/etc/pam/pam-api.env`
3. Restart: `sudo systemctl restart pam-api`
4. Any in-flight launch tokens will become invalid (sessions must be re-launched)

**Connector Secret (`PAM_CONNECTOR_SECRET`)**
1. Generate new secret: `openssl rand -base64 32`
2. Update both `/etc/pam/pam-api.env` (as `PAM_CONNECTOR_SECRET`) and connector env (as `PAM_CONNECTOR_SECRET`)
3. Restart both services. The same secret value must be set on both API and connector.

### Updating SSH Proxy Known Hosts

To add or update a target host's key for the SSH proxy:
1. Obtain the host key: `ssh-keyscan -t ed25519,rsa <target-host>`
2. Append to the known hosts file (default: `.pam_upstream_known_hosts` or the path in `PAM_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH`)
3. No restart needed — the proxy reads the file on each connection

### LDAP (Samba AD) Configuration Example

```bash
PAM_AUTH_PROVIDER_MODE=ldap
PAM_LDAP_URL=ldaps://dc01.corp.example.com:636
PAM_LDAP_BASE_DN=DC=corp,DC=example,DC=com
PAM_LDAP_BIND_DN=CN=pam-svc,OU=Service Accounts,DC=corp,DC=example,DC=com
PAM_LDAP_BIND_PASSWORD=<service-account-password>
PAM_LDAP_USE_TLS=true
# Defaults are Samba AD-aligned:
#   PAM_LDAP_USERNAME_ATTR=sAMAccountName
#   PAM_LDAP_DISPLAY_NAME_ATTR=displayName
#   PAM_LDAP_EMAIL_ATTR=mail
#   PAM_LDAP_USER_FILTER=(&(objectClass=user)(sAMAccountName={username}))
# Optional group-to-role mapping:
PAM_LDAP_GROUP_ROLE_MAPPING=PAM Admins=admin,PAM Operators=operator|user
```

### Connector Trust Model

When `PAM_CONNECTOR_SECRET` is set on both API and connector:
- The API signs every launch payload with an HMAC token (`connector_token` in launch response)
- The connector verifies the signature, session_id match, and expiry before executing any launch
- Unsigned or expired requests are rejected with HTTP 403

When `PAM_CONNECTOR_SECRET` is **not set**: connector token verification is disabled (backwards-compatible, suitable for development).

---

## Troubleshooting

### Failed Logins

Check the API structured logs for entries with `"action":"login"`:
- `login_failed_user_not_found` — username does not exist in LDAP or local DB
- `login_failed_invalid_password` — user found but password wrong
- `login_failed_ldap_error` — LDAP bind/search or TLS/connectivity issue (check LDAP server reachability)
- `login_failed_rate_limited` — too many failed attempts; wait 5 minutes or check for brute-force

### Failed Launches

- Check that the user has an access grant for the asset+action (`/access/my`)
- Check that credentials exist for the asset (`/admin/assets/{id}/credentials`)
- For shell launches: verify SSH proxy is running and reachable at the configured public host/port
- For connector launches: check connector logs for `rejected` entries (missing/invalid connector token)

### Connector Not Reachable

- Default bind is `127.0.0.1:9494` — only accessible from the local machine
- If the UI cannot reach the connector, ensure it is running and the browser is on the same machine
- Check CORS: `PAM_CONNECTOR_ALLOWED_ORIGIN` must include the UI origin

### SSH Proxy Issues

- `host key verification failed` — add the target host key to the known hosts file (see above)
- `connection refused` — verify the target host is reachable from the API server on the configured SSH port
- `invalid launch token` — the token may be expired (default 2 min TTL) or the session was already used

### Known Proxy TLS Limitations

- MSSQL: full client<->proxy TDS TLS tunnel mode is not implemented in this slice.
- Redis: connector `redis-cli` currently connects to the PAM Redis proxy endpoint without TLS (expected loopback/session endpoint); upstream Redis TLS from PAM to target is supported.

---

## Backup & Restore

### PostgreSQL Backup

```bash
pg_dump -Fc -h <host> -U <user> -d pam > pam_backup_$(date +%Y%m%d).dump
```

### PostgreSQL Restore

```bash
pg_restore -h <host> -U <user> -d pam --clean --if-exists pam_backup_YYYYMMDD.dump
```

After restoring, restart the API service to re-validate connections:
```bash
sudo systemctl restart pam-api
```

---

## Security Defaults

| Setting | Default (dev) | Default (prod) | Override |
|---------|--------------|----------------|---------|
| Session cookie secure | false | true | `PAM_AUTH_COOKIE_SECURE` |
| Session cookie SameSite | lax | strict | `PAM_AUTH_COOKIE_SAMESITE` |
| LDAP insecure skip verify | false | blocked | `PAM_ALLOW_UNSAFE_MODE=true` |
| SSH upstream host key mode | known-hosts | known-hosts | blocked unless `PAM_ALLOW_UNSAFE_MODE=true` for insecure/accept-new |
| Connector remote access | false | false | `PAM_CONNECTOR_ALLOW_REMOTE=true` (unsafe) |
| Connector any origin | false | false | `PAM_CONNECTOR_ALLOW_ANY_ORIGIN=true` (unsafe) |
| Vault key format | any string | base64-encoded 32 bytes | `PAM_ALLOW_UNSAFE_MODE=true` to bypass |
| Connector token verification | disabled | enabled when `PAM_CONNECTOR_SECRET` set | Set same secret on API + connector |
