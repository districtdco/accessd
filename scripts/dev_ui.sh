#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI_DIR="$ROOT_DIR/apps/ui"

if ! command -v npm >/dev/null 2>&1; then
  echo "npm is required" >&2
  exit 1
fi

cd "$UI_DIR"

if [[ ! -d node_modules ]]; then
  echo "[dev_ui] node_modules not found; running npm install"
  npm install
fi

echo "[dev_ui] UI dev server: http://127.0.0.1:3000"
echo "[dev_ui] expects API at /api -> http://localhost:8080 and connector at /connector -> http://127.0.0.1:9494"

exec npm run dev
