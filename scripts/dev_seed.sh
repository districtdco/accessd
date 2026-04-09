#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd curl
require_cmd jq

API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8080}"
ADMIN_USERNAME="${ACCESSD_DEV_ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ACCESSD_DEV_ADMIN_PASSWORD:-admin123}"

SSH_TARGET_HOST="${ACCESSD_SEED_SSH_HOST:-127.0.0.1}"
SSH_TARGET_PORT="${ACCESSD_SEED_SSH_PORT:-22222}"
PG_TARGET_HOST="${ACCESSD_SEED_PG_HOST:-127.0.0.1}"
PG_TARGET_PORT="${ACCESSD_SEED_PG_PORT:-15432}"
MYSQL_TARGET_HOST="${ACCESSD_SEED_MYSQL_HOST:-127.0.0.1}"
MYSQL_TARGET_PORT="${ACCESSD_SEED_MYSQL_PORT:-13306}"
MSSQL_TARGET_HOST="${ACCESSD_SEED_MSSQL_HOST:-127.0.0.1}"
MSSQL_TARGET_PORT="${ACCESSD_SEED_MSSQL_PORT:-11433}"
REDIS_TARGET_HOST="${ACCESSD_SEED_REDIS_HOST:-127.0.0.1}"
REDIS_TARGET_PORT="${ACCESSD_SEED_REDIS_PORT:-16379}"

COOKIE_JAR="$(mktemp -t accessd-dev-seed-cookie.XXXXXX)"
trap 'rm -f "$COOKIE_JAR"' EXIT

login_payload="$(jq -n --arg u "$ADMIN_USERNAME" --arg p "$ADMIN_PASSWORD" '{username:$u,password:$p}')"

echo "[dev_seed] authenticating as $ADMIN_USERNAME against $API_BASE_URL"
curl -fsS -c "$COOKIE_JAR" -H 'content-type: application/json' -d "$login_payload" "$API_BASE_URL/auth/login" >/dev/null

me_json="$(curl -fsS -b "$COOKIE_JAR" "$API_BASE_URL/me")"
admin_user_id="$(echo "$me_json" | jq -r '.id')"
if [[ -z "$admin_user_id" || "$admin_user_id" == "null" ]]; then
  echo "[dev_seed] unable to resolve admin user id from /me" >&2
  exit 1
fi

echo "[dev_seed] admin user id: $admin_user_id"

upsert_asset() {
  local name="$1"
  local legacy_name="${2:-}"
  local asset_type="$3"
  local host="$4"
  local port="$5"
  local metadata_json="$6"

  local existing_id
  if [[ -n "$legacy_name" ]]; then
    existing_id="$(curl -fsS -b "$COOKIE_JAR" "$API_BASE_URL/admin/assets" | jq -r --arg n "$name" --arg l "$legacy_name" '.items[] | select(.name==$n or .name==$l) | .id' | head -n 1)"
  else
    existing_id="$(curl -fsS -b "$COOKIE_JAR" "$API_BASE_URL/admin/assets" | jq -r --arg n "$name" '.items[] | select(.name==$n) | .id' | head -n 1)"
  fi

  local body
  body="$(jq -n --arg name "$name" --arg asset_type "$asset_type" --arg host "$host" --argjson port "$port" --argjson metadata "$metadata_json" '{name:$name,asset_type:$asset_type,host:$host,port:$port,metadata:$metadata}')"

  if [[ -n "$existing_id" ]]; then
    curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -X PUT -d "$body" "$API_BASE_URL/admin/assets/$existing_id" >/dev/null
    echo "$existing_id"
    return
  fi

  curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -X POST -d "$body" "$API_BASE_URL/admin/assets" | jq -r '.id'
}

upsert_credential() {
  local asset_id="$1"
  local credential_type="$2"
  local username="$3"
  local secret="$4"
  local metadata_json="$5"

  local body
  body="$(jq -n --arg username "$username" --arg secret "$secret" --argjson metadata "$metadata_json" '{username:$username,secret:$secret,metadata:$metadata}')"

  curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -X PUT -d "$body" \
    "$API_BASE_URL/admin/assets/$asset_id/credentials/$credential_type" >/dev/null
}

grant_user_action() {
  local user_id="$1"
  local asset_id="$2"
  local action="$3"
  local body
  body="$(jq -n --arg asset_id "$asset_id" --arg action "$action" '{asset_id:$asset_id,action:$action}')"
  curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -X POST -d "$body" \
    "$API_BASE_URL/admin/users/$user_id/grants" >/dev/null
}

echo "[dev_seed] upserting local test assets"
linux_id="$(upsert_asset "accessd-local-linux" "pam-local-linux" "linux_vm" "$SSH_TARGET_HOST" "$SSH_TARGET_PORT" '{"env":"local","os":"docker","path":"/home/accessd"}')"
pg_id="$(upsert_asset "accessd-local-postgres" "pam-local-postgres" "database" "$PG_TARGET_HOST" "$PG_TARGET_PORT" '{"engine":"postgres","database":"app","ssl_mode":"disable","env":"local"}')"
mysql_id="$(upsert_asset "accessd-local-mysql" "pam-local-mysql" "database" "$MYSQL_TARGET_HOST" "$MYSQL_TARGET_PORT" '{"engine":"mysql","database":"appdb","ssl_mode":"disable","env":"local"}')"
mssql_id="$(upsert_asset "accessd-local-mssql" "pam-local-mssql" "database" "$MSSQL_TARGET_HOST" "$MSSQL_TARGET_PORT" '{"engine":"mssql","database":"appdb","ssl_mode":"disable","env":"local"}')"
redis_id="$(upsert_asset "accessd-local-redis" "pam-local-redis" "redis" "$REDIS_TARGET_HOST" "$REDIS_TARGET_PORT" '{"engine":"redis","database":0,"tls":false,"env":"local"}')"

echo "[dev_seed] upserting local test credentials"
upsert_credential "$linux_id" "password" "pam" "pam_dev_password" '{"seed":"dev_seed.sh"}'
upsert_credential "$pg_id" "db_password" "app_user" "app_password" '{"seed":"dev_seed.sh"}'
upsert_credential "$mysql_id" "db_password" "app_user" "app_password" '{"seed":"dev_seed.sh"}'
upsert_credential "$mssql_id" "db_password" "sa" "YourStrong!Passw0rd" '{"seed":"dev_seed.sh"}'
upsert_credential "$redis_id" "password" "default" "app_password" '{"seed":"dev_seed.sh"}'

echo "[dev_seed] ensuring direct grants for admin user"
grant_user_action "$admin_user_id" "$linux_id" "shell"
grant_user_action "$admin_user_id" "$linux_id" "sftp"
grant_user_action "$admin_user_id" "$pg_id" "dbeaver"
grant_user_action "$admin_user_id" "$mysql_id" "dbeaver"
grant_user_action "$admin_user_id" "$mssql_id" "dbeaver"
grant_user_action "$admin_user_id" "$redis_id" "redis"

echo
echo "[dev_seed] complete. seeded assets:"
echo "  - accessd-local-linux      ($linux_id)"
echo "  - accessd-local-postgres   ($pg_id)"
echo "  - accessd-local-mysql      ($mysql_id)"
echo "  - accessd-local-mssql      ($mssql_id)"
echo "  - accessd-local-redis      ($redis_id)"
