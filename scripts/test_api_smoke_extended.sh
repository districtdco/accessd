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
USERNAME="${ACCESSD_SMOKE_USERNAME:-admin}"
PASSWORD="${ACCESSD_SMOKE_PASSWORD:-admin123}"

COOKIE_JAR="$(mktemp -t accessd-matrix-cookie.XXXXXX)"
trap 'rm -f "$COOKIE_JAR"' EXIT

declare -a CHECKS

login_payload="$(jq -n --arg u "$USERNAME" --arg p "$PASSWORD" '{username:$u,password:$p}')"
curl -fsS -c "$COOKIE_JAR" -H 'content-type: application/json' -d "$login_payload" "$API_BASE_URL/auth/login" >/dev/null

access_json="$(curl -fsS -b "$COOKIE_JAR" "$API_BASE_URL/access/my")"

find_asset_id() {
  local name="$1"
  echo "$access_json" | jq -r --arg n "$name" '.items[] | select(.asset_name==$n) | .asset_id' | head -n 1
}

launch_session() {
  local label="$1"
  local asset_id="$2"
  local action="$3"

  if [[ -z "$asset_id" ]]; then
    echo "[matrix] FAIL $label: missing asset id" >&2
    return 1
  fi

  local launch_body launch_json session_id launch_type
  launch_body="$(jq -n --arg aid "$asset_id" --arg action "$action" '{asset_id:$aid,action:$action}')"
  launch_json="$(curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -d "$launch_body" "$API_BASE_URL/sessions/launch")"
  session_id="$(echo "$launch_json" | jq -r '.session_id // empty')"
  launch_type="$(echo "$launch_json" | jq -r '.launch_type // empty')"

  if [[ -z "$session_id" || -z "$launch_type" ]]; then
    echo "[matrix] FAIL $label: invalid launch response" >&2
    return 1
  fi

  echo "[matrix] PASS $label: session_id=$session_id launch_type=$launch_type"
  CHECKS+=("$label:$session_id")
}

linux_id="$(find_asset_id 'accessd-local-linux')"
pg_id="$(find_asset_id 'accessd-local-postgres')"
mysql_id="$(find_asset_id 'accessd-local-mysql')"
mssql_id="$(find_asset_id 'accessd-local-mssql')"
redis_id="$(find_asset_id 'accessd-local-redis')"
mongo_id="$(find_asset_id 'accessd-local-mongo')"

launch_session "SSH shell" "$linux_id" "shell"
launch_session "SFTP" "$linux_id" "sftp"
launch_session "PostgreSQL DBeaver" "$pg_id" "dbeaver"
launch_session "MySQL DBeaver" "$mysql_id" "dbeaver"
launch_session "MSSQL DBeaver" "$mssql_id" "dbeaver"
launch_session "MongoDB Robo 3T" "$mongo_id" "dbeaver"
launch_session "Redis CLI" "$redis_id" "redis"

echo
echo "[matrix] launched sessions:"
for row in "${CHECKS[@]}"; do
  echo "  - $row"
done

echo
echo "[matrix] Verify details in UI (/sessions) and audit in /admin/audit/events."
