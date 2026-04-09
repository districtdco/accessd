#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${ACCESSD_CONNECTOR_INSTALL_DIR:-${HOME}/.local/bin}"
CONFIG_DIR="${HOME}/.accessd-connector"
TARGET_BIN="${INSTALL_DIR}/accessd-connector"
APP_DIR="${HOME}/Applications/AccessD Connector.app"
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
rm -f "${CONFIG_DIR}/bin/url-handler-macos.sh"
rm -rf "${APP_DIR}"

if [[ "${REMOVE_CONFIG}" == "1" ]]; then
  rm -rf "${CONFIG_DIR}"
  echo "[accessd-connector] Removed connector config directory: ${CONFIG_DIR}"
else
  echo "[accessd-connector] Preserved connector config directory: ${CONFIG_DIR}"
  echo "[accessd-connector] Set ACCESSD_CONNECTOR_REMOVE_CONFIG=1 to remove it."
fi

echo "[accessd-connector] Uninstall complete."
