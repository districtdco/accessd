# Migration Guide: PAM → AccessD

This guide covers the naming changes made when the project was renamed from PAM to AccessD.

---

## What Changed

### Binary names

| Old                | New                    |
|--------------------|------------------------|
| `pam-api`          | `accessd`              |
| `pam-connector`    | `accessd-connector`    |

### Environment variables

All `PAM_*` variables have been renamed to `ACCESSD_*`.

**Backward compatibility is built in.** AccessD automatically copies `PAM_*` env vars to their `ACCESSD_*` equivalents at startup, so existing deployments continue to work without any changes.

You can migrate at your own pace by updating your env files to use `ACCESSD_*` prefixes.

#### Example

```diff
- PAM_DB_URL=postgres://...
+ ACCESSD_DB_URL=postgres://...

- PAM_VAULT_KEY=...
+ ACCESSD_VAULT_KEY=...

- PAM_LAUNCH_TOKEN_SECRET=...
+ ACCESSD_LAUNCH_TOKEN_SECRET=...
```

Full variable mapping (old → new):

| Old (`PAM_*`)                              | New (`ACCESSD_*`)                               |
|--------------------------------------------|-------------------------------------------------|
| `PAM_DB_URL`                               | `ACCESSD_DB_URL`                                |
| `PAM_ENV`                                  | `ACCESSD_ENV`                                   |
| `PAM_HTTP_ADDR`                            | `ACCESSD_HTTP_ADDR`                             |
| `PAM_VAULT_KEY`                            | `ACCESSD_VAULT_KEY`                             |
| `PAM_VAULT_KEY_ID`                         | `ACCESSD_VAULT_KEY_ID`                          |
| `PAM_LAUNCH_TOKEN_SECRET`                  | `ACCESSD_LAUNCH_TOKEN_SECRET`                   |
| `PAM_CONNECTOR_SECRET`                     | `ACCESSD_CONNECTOR_SECRET`                      |
| `PAM_AUTH_COOKIE_NAME`                     | `ACCESSD_AUTH_COOKIE_NAME`                      |
| `PAM_AUTH_SESSION_TTL`                     | `ACCESSD_AUTH_SESSION_TTL`                      |
| `PAM_AUTH_COOKIE_SECURE`                   | `ACCESSD_AUTH_COOKIE_SECURE`                    |
| `PAM_AUTH_COOKIE_SAMESITE`                 | `ACCESSD_AUTH_COOKIE_SAMESITE`                  |
| `PAM_AUTH_PROVIDER_MODE`                   | `ACCESSD_AUTH_PROVIDER_MODE`                    |
| `PAM_DEV_ADMIN_USERNAME`                   | `ACCESSD_DEV_ADMIN_USERNAME`                    |
| `PAM_DEV_ADMIN_PASSWORD`                   | `ACCESSD_DEV_ADMIN_PASSWORD`                    |
| `PAM_LDAP_HOST`                            | `ACCESSD_LDAP_HOST`                             |
| `PAM_LDAP_PORT`                            | `ACCESSD_LDAP_PORT`                             |
| `PAM_LDAP_BASE_DN`                         | `ACCESSD_LDAP_BASE_DN`                          |
| `PAM_LDAP_BIND_DN`                         | `ACCESSD_LDAP_BIND_DN`                          |
| `PAM_LDAP_BIND_PASSWORD`                   | `ACCESSD_LDAP_BIND_PASSWORD`                    |
| `PAM_LDAP_USE_TLS`                         | `ACCESSD_LDAP_USE_TLS`                          |
| `PAM_LDAP_GROUP_ROLE_MAPPING`              | `ACCESSD_LDAP_GROUP_ROLE_MAPPING`               |
| `PAM_SSH_PROXY_ADDR`                       | `ACCESSD_SSH_PROXY_ADDR`                        |
| `PAM_SSH_PROXY_PUBLIC_HOST`                | `ACCESSD_SSH_PROXY_PUBLIC_HOST`                 |
| `PAM_SSH_PROXY_HOST_KEY_PATH`              | `ACCESSD_SSH_PROXY_HOST_KEY_PATH`               |
| `PAM_SSH_PROXY_UPSTREAM_HOSTKEY_MODE`      | `ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE`       |
| `PAM_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH`  | `ACCESSD_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH`   |
| `PAM_PG_PROXY_BIND_HOST`                   | `ACCESSD_PG_PROXY_BIND_HOST`                    |
| `PAM_PG_PROXY_PUBLIC_HOST`                 | `ACCESSD_PG_PROXY_PUBLIC_HOST`                  |
| `PAM_MYSQL_PROXY_BIND_HOST`                | `ACCESSD_MYSQL_PROXY_BIND_HOST`                 |
| `PAM_MYSQL_PROXY_PUBLIC_HOST`              | `ACCESSD_MYSQL_PROXY_PUBLIC_HOST`               |
| `PAM_MSSQL_PROXY_BIND_HOST`                | `ACCESSD_MSSQL_PROXY_BIND_HOST`                 |
| `PAM_MSSQL_PROXY_PUBLIC_HOST`              | `ACCESSD_MSSQL_PROXY_PUBLIC_HOST`               |
| `PAM_REDIS_PROXY_BIND_HOST`                | `ACCESSD_REDIS_PROXY_BIND_HOST`                 |
| `PAM_REDIS_PROXY_PUBLIC_HOST`              | `ACCESSD_REDIS_PROXY_PUBLIC_HOST`               |
| `PAM_ALLOW_UNSAFE_MODE`                    | `ACCESSD_ALLOW_UNSAFE_MODE`                     |
| `PAM_CONNECTOR_ADDR`                       | `ACCESSD_CONNECTOR_ADDR`                        |
| `PAM_CONNECTOR_ALLOWED_ORIGIN`             | `ACCESSD_CONNECTOR_ALLOWED_ORIGIN`              |
| `PAM_CONNECTOR_ALLOW_ANY_ORIGIN`           | `ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN`            |
| `PAM_CONNECTOR_ALLOW_REMOTE`               | `ACCESSD_CONNECTOR_ALLOW_REMOTE`                |
| `PAM_CONNECTOR_DBEAVER_TEMP_TTL`           | `ACCESSD_CONNECTOR_DBEAVER_TEMP_TTL`            |
| `PAM_CONNECTOR_PUTTY_PATH`                 | `ACCESSD_CONNECTOR_PUTTY_PATH`                  |
| `PAM_CONNECTOR_WINSCP_PATH`                | `ACCESSD_CONNECTOR_WINSCP_PATH`                 |
| `PAM_CONNECTOR_FILEZILLA_PATH`             | `ACCESSD_CONNECTOR_FILEZILLA_PATH`              |
| `PAM_CONNECTOR_DBEAVER_PATH`               | `ACCESSD_CONNECTOR_DBEAVER_PATH`                |
| `PAM_CONNECTOR_REDIS_CLI_PATH`             | `ACCESSD_CONNECTOR_REDIS_CLI_PATH`              |
| `PAM_CONFIG_FILE`                          | `ACCESSD_CONFIG_FILE`                           |

### Session cookie name

The default session cookie name changed from `pam_session` to `accessd_session`.

**Impact:** existing browser sessions will be invalidated after deployment. Users will need to log in again after upgrading. This is expected and safe.

If you need to preserve existing sessions during a rolling upgrade, set:
```
ACCESSD_AUTH_COOKIE_NAME=pam_session
```
in your env file before upgrading, then change it back once all users have re-authenticated.

### Connector config directory

The connector's optional config file path changed:

| Old                              | New                                  |
|----------------------------------|--------------------------------------|
| `~/.pam-connector/config.yaml`   | `~/.accessd-connector/config.yaml`   |
| `PAM_CONNECTOR_CONFIG_FILE`      | `ACCESSD_CONNECTOR_CONFIG_FILE`      |

Operators using a custom connector config file should move it to the new path (or continue using the old env var — the compat migration bridges it).

### SSH proxy username default

The default SSH proxy username changed:

| Old     | New       |
|---------|-----------|
| `pam`   | `accessd` |

**Impact:** This only affects deployments where `PAM_SSH_PROXY_USERNAME` was not explicitly set (i.e., using the default). Operators who have `pam` in their SSH known_hosts for the proxy will see a username change in connection strings.

To preserve the old username:
```
ACCESSD_SSH_PROXY_USERNAME=pam
```

### Deployment file names

| Old                                  | New                                      |
|--------------------------------------|------------------------------------------|
| `deploy/systemd/pam-api.service`     | `deploy/systemd/accessd.service`         |
| `deploy/systemd/pam-connector.service` | `deploy/systemd/accessd-connector.service` |
| `deploy/env/pam-api.env.example`     | `deploy/env/accessd.env.example`         |
| `deploy/env/pam-connector.env.example` | `deploy/env/accessd-connector.env.example` |
| `deploy/nginx/pam.conf.example`      | `deploy/nginx/accessd.conf.example`      |
| `deploy/nginx/pam-edge.conf`         | `deploy/nginx/accessd-edge.conf`         |

The old files contain deprecation notices.

### Filesystem paths (for new deployments)

| Old                          | New                              |
|------------------------------|----------------------------------|
| `/opt/pam/`                  | `/opt/accessd/`                  |
| `/etc/pam/pam-api.env`       | `/etc/accessd/accessd.env`       |
| `/var/lib/pam/`              | `/var/lib/accessd/`              |
| `/var/www/pam/`              | `/var/www/accessd/`              |
| `pam` system user            | `accessd` system user            |

Existing deployments at `/opt/pam/` can be migrated by:
1. Updating the systemd service `WorkingDirectory` and binary paths
2. Updating env file references
3. Moving `/var/lib/pam/` to `/var/lib/accessd/` and updating `ACCESSD_SSH_PROXY_HOST_KEY_PATH`

Or stay at `/opt/pam/` for now — nothing breaks; the path is just a deployment convention.

---

## Step-by-step upgrade for an existing deployment

```bash
# 1. Build new binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o accessd ./apps/api/cmd/server

# 2. Install new binary
sudo install -o root -g accessd -m 0755 accessd /opt/accessd/bin/accessd
# Or if staying at old path:
sudo install -o root -g pam -m 0755 accessd /opt/pam/bin/pam-api

# 3. Update env file (PAM_* still works — migrate at your own pace)
# Optionally rename keys from PAM_* to ACCESSD_*

# 4. Restart
sudo systemctl restart accessd  # or pam-api if using old service name

# 5. Verify
curl -fs http://127.0.0.1:8080/health/ready && echo "healthy"
```

The PAM_* → ACCESSD_* migration is fully automatic. No configuration changes are required to upgrade.
