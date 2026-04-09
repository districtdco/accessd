#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_DIR="$ROOT_DIR/apps/api"
API_BIN="$ROOT_DIR/bin/accessd"
API_BIN_REL="./bin/accessd"

MODE="server"
for arg in "$@"; do
  case "$arg" in
    --bootstrap-only)
      MODE="bootstrap"
      ;;
    --migrate-only)
      MODE="migrate"
      ;;
    *)
      echo "unknown argument: $arg" >&2
      echo "usage: $0 [--bootstrap-only|--migrate-only]" >&2
      exit 1
      ;;
  esac
done

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi

mkdir -p "$ROOT_DIR/.gocache" "$ROOT_DIR/bin"
needs_build=0
if [[ ! -x "$API_BIN" ]]; then
  needs_build=1
elif find "$API_DIR" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$API_BIN" -print -quit | grep -q .; then
  needs_build=1
fi

if [[ "$needs_build" -eq 1 ]]; then
  echo "[dev_api] building $API_BIN_REL"
  (
    cd "$API_DIR"
    CGO_ENABLED="${CGO_ENABLED:-0}" GOCACHE="$ROOT_DIR/.gocache" go build -o "$API_BIN" ./cmd/server
  )
fi

export ACCESSD_ENV="${ACCESSD_ENV:-development}"
export ACCESSD_DB_URL="${ACCESSD_DB_URL:-postgres://pam:pam_dev_password@127.0.0.1:5432/pam?sslmode=disable}"
export ACCESSD_VAULT_KEY="${ACCESSD_VAULT_KEY:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"
export ACCESSD_LAUNCH_TOKEN_SECRET="${ACCESSD_LAUNCH_TOKEN_SECRET:-accessd-dev-launch-secret}"
export ACCESSD_CONNECTOR_SECRET="${ACCESSD_CONNECTOR_SECRET:-accessd-dev-connector-secret}"
export ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT="${ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT:-5m}"
export ACCESSD_HTTP_ADDR="${ACCESSD_HTTP_ADDR:-:8080}"
export ACCESSD_SSH_PROXY_ADDR="${ACCESSD_SSH_PROXY_ADDR:-:2222}"
export ACCESSD_SSH_PROXY_PUBLIC_HOST="${ACCESSD_SSH_PROXY_PUBLIC_HOST:-127.0.0.1}"
export ACCESSD_SSH_PROXY_PUBLIC_PORT="${ACCESSD_SSH_PROXY_PUBLIC_PORT:-2222}"
export ACCESSD_PG_PROXY_IDLE_TIMEOUT="${ACCESSD_PG_PROXY_IDLE_TIMEOUT:-30m}"
export ACCESSD_MYSQL_PROXY_IDLE_TIMEOUT="${ACCESSD_MYSQL_PROXY_IDLE_TIMEOUT:-30m}"
export ACCESSD_MSSQL_PROXY_IDLE_TIMEOUT="${ACCESSD_MSSQL_PROXY_IDLE_TIMEOUT:-30m}"
if [[ -z "${ACCESSD_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH:-}" ]]; then
  export ACCESSD_SSH_PROXY_UPSTREAM_KNOWN_HOSTS_PATH="$API_DIR/.accessd_upstream_known_hosts"
fi
export ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE="${ACCESSD_SSH_PROXY_UPSTREAM_HOSTKEY_MODE:-accept-new}"
cd "$API_DIR"

echo "[dev_api] mode=$MODE"
echo "[dev_api] env=$ACCESSD_ENV"
echo "[dev_api] db=$ACCESSD_DB_URL"
echo "[dev_api] http=$ACCESSD_HTTP_ADDR ssh_proxy=$ACCESSD_SSH_PROXY_PUBLIC_HOST:$ACCESSD_SSH_PROXY_PUBLIC_PORT"
echo "[dev_api] launch_materialize_timeout=$ACCESSD_LAUNCH_MATERIALIZE_TIMEOUT"
echo "[dev_api] db_proxy_idle_timeout pg=$ACCESSD_PG_PROXY_IDLE_TIMEOUT mysql=$ACCESSD_MYSQL_PROXY_IDLE_TIMEOUT mssql=$ACCESSD_MSSQL_PROXY_IDLE_TIMEOUT"
echo "[dev_api] binary=$API_BIN"

case "$MODE" in
  migrate)
    exec "$API_BIN" migrate up
    ;;
  bootstrap)
    exec "$API_BIN" bootstrap
    ;;
  server)
    exec "$API_BIN" server
    ;;
esac
