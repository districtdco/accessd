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
USERNAME="${ACCESSD_SMOKE_USERNAME:-${PAM_SMOKE_USERNAME:-admin}}"
PASSWORD="${ACCESSD_SMOKE_PASSWORD:-${PAM_SMOKE_PASSWORD:-admin123}}"
ACTION="${ACCESSD_SMOKE_ACTION:-${PAM_SMOKE_ACTION:-shell}}"
ASSET_ID_OVERRIDE="${ACCESSD_SMOKE_ASSET_ID:-${PAM_SMOKE_ASSET_ID:-}}"

COOKIE_JAR="$(mktemp -t accessd-smoke-cookie.XXXXXX)"
trap 'rm -f "$COOKIE_JAR"' EXIT

PASS=0
FAIL=0

check() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "[smoke] PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "[smoke] FAIL: ${name}" >&2
    FAIL=$((FAIL + 1))
  fi
}

# ── Health ──
echo "[smoke] checking readiness: ${API_BASE_URL}/health/ready"
READY_JSON="$(curl -fsS "${API_BASE_URL}/health/ready")"
READY_STATUS="$(echo "$READY_JSON" | jq -r '.status // empty')"
if [[ "$READY_STATUS" != "ready" ]]; then
  echo "[smoke] readiness check failed: $READY_JSON" >&2
  exit 1
fi
echo "[smoke] PASS: health/ready"
PASS=$((PASS + 1))

# ── Version ──
VERSION_JSON="$(curl -fsS "${API_BASE_URL}/version")"
VERSION="$(echo "$VERSION_JSON" | jq -r '.version // empty')"
check "version endpoint" test -n "$VERSION"

# ── Login ──
echo "[smoke] login flow: ${USERNAME}"
LOGIN_JSON="$(curl -fsS -c "$COOKIE_JAR" -H 'content-type: application/json' -d "{\"username\":\"${USERNAME}\",\"password\":\"${PASSWORD}\"}" "${API_BASE_URL}/auth/login")"
LOGIN_USER="$(echo "$LOGIN_JSON" | jq -r '.user.username // empty')"
if [[ "$LOGIN_USER" != "$USERNAME" ]]; then
  echo "[smoke] FAIL: login (unexpected username: $LOGIN_JSON)" >&2
  FAIL=$((FAIL + 1))
else
  echo "[smoke] PASS: login"
  PASS=$((PASS + 1))
fi

# ── /me ──
ME_JSON="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/me")"
ME_USER="$(echo "$ME_JSON" | jq -r '.username // empty')"
check "/me returns username" test "$ME_USER" = "$USERNAME"

# ── /access/my ──
echo "[smoke] /access/my"
ACCESS_JSON="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/access/my")"
ITEM_COUNT="$(echo "$ACCESS_JSON" | jq '.items | length')"
if [[ "$ITEM_COUNT" -lt 1 ]]; then
  echo "[smoke] FAIL: access/my returned no items" >&2
  FAIL=$((FAIL + 1))
else
  echo "[smoke] PASS: access/my (${ITEM_COUNT} items)"
  PASS=$((PASS + 1))
fi

ASSET_ID="$ASSET_ID_OVERRIDE"
if [[ -z "$ASSET_ID" ]]; then
  ASSET_ID="$(echo "$ACCESS_JSON" | jq -r --arg action "$ACTION" '.items[] | select((.allowed_actions // []) | index($action)) | .asset_id' | head -n1)"
fi
if [[ -z "$ASSET_ID" ]]; then
  ASSET_ID="$(echo "$ACCESS_JSON" | jq -r '.items[0].asset_id // empty')"
fi
if [[ -z "$ASSET_ID" ]]; then
  echo "[smoke] unable to resolve asset_id from /access/my" >&2
  exit 1
fi

# ── Session launch ──
echo "[smoke] /sessions/launch asset_id=${ASSET_ID} action=${ACTION}"
LAUNCH_JSON="$(curl -fsS -b "$COOKIE_JAR" -H 'content-type: application/json' -d "{\"asset_id\":\"${ASSET_ID}\",\"action\":\"${ACTION}\"}" "${API_BASE_URL}/sessions/launch")"
SESSION_ID="$(echo "$LAUNCH_JSON" | jq -r '.session_id // empty')"
LAUNCH_TYPE="$(echo "$LAUNCH_JSON" | jq -r '.launch_type // empty')"
if [[ -z "$SESSION_ID" || -z "$LAUNCH_TYPE" ]]; then
  echo "[smoke] FAIL: session launch missing shape" >&2
  FAIL=$((FAIL + 1))
else
  echo "[smoke] PASS: session launch (session_id=${SESSION_ID} type=${LAUNCH_TYPE})"
  PASS=$((PASS + 1))
fi

# ── Session detail ──
if [[ -n "$SESSION_ID" ]]; then
  DETAIL_JSON="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/sessions/${SESSION_ID}")"
  DETAIL_SID="$(echo "$DETAIL_JSON" | jq -r '.session_id // empty')"
  check "session detail" test "$DETAIL_SID" = "$SESSION_ID"

  EVENTS_JSON="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/sessions/${SESSION_ID}/events?limit=25")"
  check "session events endpoint" echo "$EVENTS_JSON" | jq -e '.items' >/dev/null

  if [[ "$ACTION" == "shell" ]]; then
    REPLAY_JSON="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/sessions/${SESSION_ID}/replay?limit=25")"
    check "session replay endpoint" echo "$REPLAY_JSON" | jq -e '.supported' >/dev/null
  fi
fi

# ── Admin endpoints (if admin user) ──
ROLES="$(echo "$LOGIN_JSON" | jq -r '.user.roles[]? // empty' 2>/dev/null || true)"
if echo "$ROLES" | grep -q "admin"; then
  echo "[smoke] admin checks (user has admin role)"

  ADMIN_PING="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/admin/ping" 2>/dev/null || echo "")"
  check "admin/ping" test -n "$ADMIN_PING"

  ADMIN_USERS="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/admin/users" 2>/dev/null || echo "")"
  USER_COUNT="$(echo "$ADMIN_USERS" | jq '.items | length' 2>/dev/null || echo "0")"
  check "admin/users (${USER_COUNT} users)" test "$USER_COUNT" -ge 1

  ADMIN_SUMMARY="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/admin/summary" 2>/dev/null || echo "")"
  check "admin/summary" echo "$ADMIN_SUMMARY" | jq -e '.metrics' >/dev/null

  ADMIN_AUDIT="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/admin/audit/events" 2>/dev/null || echo "")"
  check "admin/audit/events" echo "$ADMIN_AUDIT" | jq -e '.items' >/dev/null
  FIRST_AUDIT_ID="$(echo "$ADMIN_AUDIT" | jq -r '.items[0].id // empty')"
  if [[ -n "$FIRST_AUDIT_ID" ]]; then
    ADMIN_AUDIT_DETAIL="$(curl -fsS -b "$COOKIE_JAR" "${API_BASE_URL}/admin/audit/events/${FIRST_AUDIT_ID}" 2>/dev/null || echo "")"
    check "admin/audit/events/{id}" echo "$ADMIN_AUDIT_DETAIL" | jq -e '.item.id' >/dev/null
  fi
fi

# ── Logout ──
LOGOUT_STATUS="$(curl -o /dev/null -w '%{http_code}' -fsS -b "$COOKIE_JAR" -X POST "${API_BASE_URL}/auth/logout")"
check "logout" test "$LOGOUT_STATUS" = "204"

# ── Summary ──
echo ""
echo "[smoke] ────────────────────────────────────"
echo "[smoke] results: ${PASS} passed, ${FAIL} failed"
if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
echo "[smoke] all checks passed"
