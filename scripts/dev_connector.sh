#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONNECTOR_DIR="$ROOT_DIR/apps/connector"
CONNECTOR_BIN="$ROOT_DIR/bin/pam-connector"
CONNECTOR_BIN_REL="./bin/pam-connector"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi

mkdir -p "$ROOT_DIR/.gocache" "$ROOT_DIR/bin"
needs_build=0
if [[ ! -x "$CONNECTOR_BIN" ]]; then
  needs_build=1
elif find "$CONNECTOR_DIR" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$CONNECTOR_BIN" -print -quit | grep -q .; then
  needs_build=1
fi

if [[ "$needs_build" -eq 1 ]]; then
  echo "[dev_connector] building $CONNECTOR_BIN_REL"
  (
    cd "$CONNECTOR_DIR"
    CGO_ENABLED="${CGO_ENABLED:-0}" GOCACHE="$ROOT_DIR/.gocache" go build -o "$CONNECTOR_BIN" ./cmd/connector
  )
fi

export PAM_CONNECTOR_ADDR="${PAM_CONNECTOR_ADDR:-127.0.0.1:9494}"
export PAM_CONNECTOR_ALLOWED_ORIGIN="${PAM_CONNECTOR_ALLOWED_ORIGIN:-http://127.0.0.1:3000,http://localhost:3000}"
export PAM_CONNECTOR_ALLOW_ANY_ORIGIN="${PAM_CONNECTOR_ALLOW_ANY_ORIGIN:-false}"
export PAM_CONNECTOR_ALLOW_REMOTE="${PAM_CONNECTOR_ALLOW_REMOTE:-false}"
export PAM_CONNECTOR_SECRET="${PAM_CONNECTOR_SECRET:-pam-dev-connector-secret}"

cd "$CONNECTOR_DIR"

echo "[dev_connector] connector address: $PAM_CONNECTOR_ADDR"
echo "[dev_connector] token verification: $( [[ -n "$PAM_CONNECTOR_SECRET" ]] && echo enabled || echo disabled )"
echo "[dev_connector] trust boundary: loopback-only unless PAM_CONNECTOR_ALLOW_REMOTE=true"
echo "[dev_connector] binary=$CONNECTOR_BIN"

exec "$CONNECTOR_BIN"
