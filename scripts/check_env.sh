#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

missing=0

check_cmd() {
  local cmd="$1"
  local hint="$2"
  if command -v "$cmd" >/dev/null 2>&1; then
    printf '[env] ok   %-14s %s\n' "$cmd" "$(command -v "$cmd")"
  else
    printf '[env] miss %-14s %s\n' "$cmd" "$hint"
    missing=$((missing + 1))
  fi
}

check_cmd docker "install Docker Desktop or docker engine"
check_cmd go "install Go 1.22+"
check_cmd node "install Node.js 20+"
check_cmd npm "comes with Node.js"
check_cmd curl "install curl"
check_cmd jq "install jq"

if command -v docker >/dev/null 2>&1; then
  if docker compose version >/dev/null 2>&1; then
    printf '[env] ok   %-14s %s\n' "docker compose" "plugin available"
  elif command -v docker-compose >/dev/null 2>&1; then
    printf '[env] ok   %-14s %s\n' "docker-compose" "standalone binary"
  else
    printf '[env] miss %-14s %s\n' "docker compose" "install Docker Compose v2 plugin or docker-compose"
    missing=$((missing + 1))
  fi
fi

printf '\n[env] root: %s\n' "$ROOT_DIR"
if [[ -f "$ROOT_DIR/LOCAL_TESTING.md" ]]; then
  printf '[env] docs: LOCAL_TESTING.md\n'
fi

if [[ $missing -gt 0 ]]; then
  printf '\n[env] missing prerequisites: %d\n' "$missing" >&2
  exit 1
fi

printf '\n[env] all required tools are available.\n'
