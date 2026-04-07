#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_DIR="$ROOT_DIR/apps/api"
API_BIN="$ROOT_DIR/bin/pam-api"
API_BIN_REL="./bin/pam-api"

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

export PAM_ENV="${PAM_ENV:-development}"
export PAM_DB_URL="${PAM_DB_URL:-postgres://pam:pam_dev_password@127.0.0.1:5432/pam?sslmode=disable}"
export PAM_VAULT_KEY="${PAM_VAULT_KEY:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"
export PAM_LAUNCH_TOKEN_SECRET="${PAM_LAUNCH_TOKEN_SECRET:-pam-dev-launch-secret}"
export PAM_CONNECTOR_SECRET="${PAM_CONNECTOR_SECRET:-pam-dev-connector-secret}"
export PAM_HTTP_ADDR="${PAM_HTTP_ADDR:-:8080}"
export PAM_SSH_PROXY_ADDR="${PAM_SSH_PROXY_ADDR:-:2222}"
export PAM_SSH_PROXY_PUBLIC_HOST="${PAM_SSH_PROXY_PUBLIC_HOST:-127.0.0.1}"
export PAM_SSH_PROXY_PUBLIC_PORT="${PAM_SSH_PROXY_PUBLIC_PORT:-2222}"

cd "$API_DIR"

echo "[dev_api] mode=$MODE"
echo "[dev_api] env=$PAM_ENV"
echo "[dev_api] db=$PAM_DB_URL"
echo "[dev_api] http=$PAM_HTTP_ADDR ssh_proxy=$PAM_SSH_PROXY_PUBLIC_HOST:$PAM_SSH_PROXY_PUBLIC_PORT"
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
