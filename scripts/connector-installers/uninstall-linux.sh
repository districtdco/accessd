#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${ACCESSD_CONNECTOR_INSTALL_DIR:-${HOME}/.local/bin}"
CONFIG_DIR="${HOME}/.accessd-connector"
DESKTOP_DIR="${HOME}/.local/share/applications"
TARGET_BIN="${INSTALL_DIR}/accessd-connector"
HANDLER_SCRIPT="${CONFIG_DIR}/bin/url-handler-linux.sh"
DESKTOP_FILE="${DESKTOP_DIR}/accessd-connector.desktop"
REMOVE_CONFIG="${ACCESSD_CONNECTOR_REMOVE_CONFIG:-0}"

stop_connector() {
  if pgrep -x "accessd-connector" >/dev/null 2>&1; then
    pkill -x "accessd-connector" >/dev/null 2>&1 || true
    for _ in {1..20}; do
      if ! pgrep -x "accessd-connector" >/dev/null 2>&1; then
        break
      fi
      sleep 0.2
    done
  fi
}

stop_connector

rm -f "${TARGET_BIN}"
rm -f "${HANDLER_SCRIPT}"
rm -f "${DESKTOP_FILE}"

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "${DESKTOP_DIR}" >/dev/null 2>&1 || true
fi

if [[ "${REMOVE_CONFIG}" == "1" ]]; then
  rm -rf "${CONFIG_DIR}"
  echo "[accessd-connector] Removed connector config directory: ${CONFIG_DIR}"
else
  echo "[accessd-connector] Preserved connector config directory: ${CONFIG_DIR}"
  echo "[accessd-connector] Set ACCESSD_CONNECTOR_REMOVE_CONFIG=1 to remove it."
fi

echo "[accessd-connector] Uninstall complete."
