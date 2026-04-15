#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8080}"
UI_BASE_URL="${UI_BASE_URL:-http://127.0.0.1:3000}"

if [[ "${1:-}" == "--api-only" ]]; then
  "$ROOT_DIR/scripts/test_api_smoke_extended.sh"
  exit 0
fi

echo "[test_matrix] running API launch matrix smoke"
"$ROOT_DIR/scripts/test_api_smoke_extended.sh"

echo
echo "[test_matrix] manual verification runbook"
echo "1. Open UI: $UI_BASE_URL/login"
echo "2. Sign in (default dev admin: admin / admin123 unless overridden)."
echo "3. Go to Access page and launch each flow:"
echo "   - accessd-local-linux -> Shell"
echo "   - accessd-local-linux -> SFTP"
echo "   - accessd-local-postgres -> DBeaver"
echo "   - accessd-local-mysql -> DBeaver"
echo "   - accessd-local-mssql -> DBeaver"
echo "   - accessd-local-mongo -> Robo 3T"
echo "   - accessd-local-redis -> Redis CLI"
echo "4. For each session, verify in UI session detail:"
echo "   - session created"
echo "   - lifecycle includes connector_launch_requested/succeeded (or failed with reason)"
echo "   - timeline/transcript appears for supported protocol data"
echo "5. Check admin audit page for corresponding events and ensure no secret values appear."
echo
echo "Known limitation reminders:"
echo "  - MSSQL full TLS tunnel mode is not implemented in this slice."
echo "  - Redis client-leg TLS to AccessD proxy is not implemented in this slice."
echo "  - Shell token entry is still manual at SSH prompt."
