#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WITH_TARGETS="false"
WITH_MSSQL="false"

for arg in "$@"; do
  case "$arg" in
    --with-targets)
      WITH_TARGETS="true"
      ;;
    --with-mssql)
      WITH_TARGETS="true"
      WITH_MSSQL="true"
      ;;
    *)
      echo "unknown argument: $arg" >&2
      echo "usage: $0 [--with-targets] [--with-mssql]" >&2
      exit 1
      ;;
  esac
done

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  echo "docker compose is required" >&2
  exit 1
fi

services=(postgres ldap)
if [[ "$WITH_TARGETS" == "true" ]]; then
  services+=(ssh-target pg-target mysql-target redis-target)
  if [[ "$WITH_MSSQL" == "true" ]]; then
    services+=(mssql-target)
  fi
fi

echo "[dev_up] starting services: ${services[*]}"
("${COMPOSE[@]}" -f "$ROOT_DIR/docker-compose.yml" up -d "${services[@]}")

echo "[dev_up] waiting for PAM postgres to become reachable"
for _ in $(seq 1 30); do
  if ("${COMPOSE[@]}" -f "$ROOT_DIR/docker-compose.yml" exec -T postgres pg_isready -U pam -d pam >/dev/null 2>&1); then
    echo "[dev_up] postgres is ready"
    break
  fi
  sleep 1
done

if ! ("${COMPOSE[@]}" -f "$ROOT_DIR/docker-compose.yml" exec -T postgres pg_isready -U pam -d pam >/dev/null 2>&1); then
  echo "[dev_up] postgres did not become ready in time" >&2
  exit 1
fi

echo
printf 'Started services:\n'
printf '  - PAM control-plane DB:     postgres://pam:pam_dev_password@127.0.0.1:5432/pam?sslmode=disable\n'
printf '  - LDAP (optional dev auth): ldap://127.0.0.1:389 and ldaps://127.0.0.1:636\n'

if [[ "$WITH_TARGETS" == "true" ]]; then
  printf '  - SSH/SFTP target:          127.0.0.1:22222 (user: pam / pass: pam_dev_password)\n'
  printf '  - Postgres target:          127.0.0.1:15432 (user: app_user / pass: app_password / db: app)\n'
  printf '  - MySQL target:             127.0.0.1:13306 (user: app_user / pass: app_password / db: appdb)\n'
  printf '  - Redis target:             127.0.0.1:16379 (pass: app_password)\n'
  if [[ "$WITH_MSSQL" == "true" ]]; then
    printf '  - MSSQL target:             127.0.0.1:11433 (user: sa / pass: YourStrong!Passw0rd / db: appdb)\n'
  else
    printf '  - MSSQL target:             not started (use --with-mssql for optional local container)\n'
  fi
fi

echo
echo "Next steps:"
echo "  1) ./scripts/dev_api.sh"
echo "  2) ./scripts/dev_seed.sh"
echo "  3) ./scripts/dev_connector.sh"
echo "  4) ./scripts/dev_ui.sh"
