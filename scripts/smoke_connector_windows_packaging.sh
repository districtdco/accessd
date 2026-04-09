#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-${VERSION:-}}"
if [[ -z "${VERSION}" ]]; then
  echo "usage: $0 <version>  (example: $0 1.0.1)"
  exit 1
fi

VERSION="${VERSION#v}"
TAG="v${VERSION}"
DIST_DIR="${ROOT_DIR}/dist/connector/${TAG}"
MSI_PATH="${DIST_DIR}/accessd-connector-${VERSION}-windows-amd64.msi"
ZIP_PATH="${DIST_DIR}/accessd-connector-${VERSION}-windows-amd64.zip"

fail() {
  echo "[smoke][FAIL] $*"
  exit 1
}

pass() {
  echo "[smoke][PASS] $*"
}

require_cmd() {
  local c="$1"
  command -v "${c}" >/dev/null 2>&1 || fail "missing required command: ${c}"
}

require_cmd msiinfo
require_cmd unzip
require_cmd rg

[[ -f "${MSI_PATH}" ]] || fail "missing MSI: ${MSI_PATH}"
[[ -f "${ZIP_PATH}" ]] || fail "missing ZIP: ${ZIP_PATH}"
pass "artifacts exist"

media_export="$(msiinfo export "${MSI_PATH}" Media)"
grep -q '#cab1.cab' <<<"${media_export}" || fail "MSI is not using embedded cabinet (#cab1.cab)"
pass "MSI uses embedded cabinet"

file_export="$(msiinfo export "${MSI_PATH}" File)"
for required in ConnectorBinFile InstallScriptFile UninstallScriptFile BootstrapRunnerFile ChecksumsFile; do
  grep -q "${required}" <<<"${file_export}" || fail "MSI File table missing ${required}"
done
pass "MSI File table contains expected payload files"

seq_export="$(msiinfo export "${MSI_PATH}" InstallExecuteSequence)"
if ! awk -F'\t' '{gsub(/\r/,"",$0)} $1=="RunBootstrapScript" && $2=="NOT REMOVE" && $3=="4001"{found=1} END{exit(found?0:1)}' <<<"${seq_export}"; then
  fail "RunBootstrapScript sequence/condition is unexpected"
fi
pass "RunBootstrapScript condition is NOT REMOVE"

zip_listing="$(unzip -l "${ZIP_PATH}")"
for required in accessd-connector.exe install.ps1 uninstall.ps1 bootstrap-runner.exe release-files-sha256.txt; do
  grep -q "${required}" <<<"${zip_listing}" || fail "ZIP missing ${required}"
done
pass "ZIP contains expected payload files"

rg -n "Source and target binary paths are identical; skipping copy." \
  "${ROOT_DIR}/scripts/connector-installers/install-windows.ps1" >/dev/null \
  || fail "install-windows.ps1 is missing self-copy guard"
pass "install script contains self-copy guard"

echo "[smoke] Windows packaging smoke checks passed for ${TAG}"
