#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_BIN="${SCRIPT_DIR}/accessd-connector"
if [[ ! -f "${SOURCE_BIN}" ]]; then
  SOURCE_BIN="${SCRIPT_DIR}/../accessd-connector"
fi
if [[ ! -f "${SOURCE_BIN}" ]]; then
  echo "[accessd-connector] binary not found next to installer"
  echo "Place this script beside accessd-connector and re-run."
  exit 1
fi

verify_release_payload_integrity() {
  local verify_setting="${ACCESSD_CONNECTOR_VERIFY_RELEASE:-true}"
  local verify_setting_lc
  verify_setting_lc="$(printf '%s' "${verify_setting}" | tr '[:upper:]' '[:lower:]')"
  case "${verify_setting_lc}" in
    0|false|no|off)
      echo "[accessd-connector] Skipping payload integrity verification (ACCESSD_CONNECTOR_VERIFY_RELEASE=${verify_setting})."
      return 0
      ;;
  esac

  local allow_unverified="${ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE:-false}"
  local allow_unverified_bool="0"
  local allow_unverified_lc
  allow_unverified_lc="$(printf '%s' "${allow_unverified}" | tr '[:upper:]' '[:lower:]')"
  case "${allow_unverified_lc}" in
    1|true|yes|on) allow_unverified_bool="1" ;;
  esac
  local checks_file="${SCRIPT_DIR}/release-files-sha256.txt"
  if [[ ! -f "${checks_file}" ]]; then
    checks_file="${SCRIPT_DIR}/../release-files-sha256.txt"
  fi
  if [[ ! -f "${checks_file}" ]]; then
    return 0
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    if (cd "$(dirname "${checks_file}")" && sha256sum -c "$(basename "${checks_file}")" >/dev/null 2>&1); then
      echo "[accessd-connector] Payload integrity check passed."
      return 0
    fi
  elif command -v shasum >/dev/null 2>&1; then
    local base_dir
    base_dir="$(dirname "${checks_file}")"
    while IFS= read -r line; do
      [[ -n "${line}" ]] || continue
      local expected file_name actual
      expected="${line%% *}"
      file_name="${line##* }"
      if [[ ! -f "${base_dir}/${file_name}" ]]; then
        if [[ "${allow_unverified_bool}" != "1" ]]; then
          echo "[accessd-connector] integrity check failed: missing ${file_name}" >&2
          exit 1
        fi
        echo "[accessd-connector] WARNING: integrity check missing ${file_name}; continuing due ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE=${allow_unverified}"
        return 0
      fi
      actual="$(shasum -a 256 "${base_dir}/${file_name}" | awk '{print $1}')"
      if [[ "${actual}" != "${expected}" ]]; then
        if [[ "${allow_unverified_bool}" != "1" ]]; then
          echo "[accessd-connector] integrity check failed for ${file_name}" >&2
          exit 1
        fi
        echo "[accessd-connector] WARNING: integrity check mismatch for ${file_name}; continuing due ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE=${allow_unverified}"
        return 0
      fi
    done < "${checks_file}"
    echo "[accessd-connector] Payload integrity check passed."
    return 0
  fi

  if [[ "${allow_unverified_bool}" == "1" ]]; then
    echo "[accessd-connector] WARNING: no checksum tool found; continuing due ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE=${allow_unverified}"
    return 0
  fi
  echo "[accessd-connector] checksum tool missing; install sha256sum or shasum, or set ACCESSD_CONNECTOR_ALLOW_UNVERIFIED_RELEASE=true" >&2
  exit 1
}

INSTALL_DIR="${ACCESSD_CONNECTOR_INSTALL_DIR:-${HOME}/.local/bin}"
CONFIG_DIR="${HOME}/.accessd-connector"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
ENV_DIR="${HOME}/.config/accessd"
ENV_FILE="${ENV_DIR}/connector.env"
HELPER_DIR="${CONFIG_DIR}/bin"
DESKTOP_DIR="${HOME}/.local/share/applications"
TARGET_BIN="${INSTALL_DIR}/accessd-connector"
HANDLER_SCRIPT="${HELPER_DIR}/url-handler-linux.sh"
TRUST_SCRIPT="${HELPER_DIR}/trust-refresh-linux.sh"
DESKTOP_FILE="${DESKTOP_DIR}/accessd-connector.desktop"

verify_release_payload_integrity

has_tty() {
  [[ -t 0 && -t 1 ]]
}

trim() {
  local s="$1"
  s="${s#${s%%[![:space:]]*}}"
  s="${s%${s##*[![:space:]]}}"
  printf '%s' "$s"
}

origin_host() {
  local origin="$1"
  origin="$(trim "${origin}")"
  origin="${origin#http://}"
  origin="${origin#https://}"
  origin="${origin%%/*}"
  origin="${origin%%:*}"
  printf '%s' "${origin}"
}

env_get_value() {
  local file="$1"
  local key="$2"
  [[ -f "${file}" ]] || return 1
  awk -v key="${key}" '
    /^[[:space:]]*#/ { next }
    index($0, key "=") == 1 {
      sub(/^[^=]*=/, "", $0)
      print $0
      exit 0
    }
  ' "${file}"
}

env_set_value() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp_file="${file}.tmp.$$"
  awk -v key="${key}" -v value="${value}" '
    BEGIN { done=0 }
    {
      if (!done && $0 !~ /^[[:space:]]*#/ && index($0, key "=") == 1) {
        print key "=" value
        done=1
        next
      }
      print
    }
    END {
      if (!done) {
        print key "=" value
      }
    }
  ' "${file}" > "${tmp_file}"
  mv "${tmp_file}" "${file}"
}

derive_verify_url_from_origin() {
  local origin="$1"
  origin="$(trim "${origin}")"
  [[ -n "${origin}" ]] || return 0
  origin="${origin%/}"
  printf '%s/api/connector/token/verify' "${origin}"
}

is_placeholder_value() {
  local value="$1"
  value="$(trim "${value}")"
  [[ -z "${value}" ]] && return 0
  [[ "${value}" == *"accessd.example.internal"* ]]
}

refresh_runtime_env_file() {
  local source_file="${1:-}"
  [[ -f "${ENV_FILE}" ]] || return 0

  local existing_origin existing_verify source_origin source_verify desired_origin desired_verify
  existing_origin="$(trim "$(env_get_value "${ENV_FILE}" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" || true)")"
  existing_verify="$(trim "$(env_get_value "${ENV_FILE}" "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" || true)")"
  source_origin=""
  source_verify=""
  if [[ -n "${source_file}" && -f "${source_file}" ]]; then
    source_origin="$(trim "$(env_get_value "${source_file}" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" || true)")"
    source_verify="$(trim "$(env_get_value "${source_file}" "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" || true)")"
  fi

  desired_origin="${existing_origin}"
  if [[ -n "${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-}" ]]; then
    desired_origin="$(trim "${ACCESSD_CONNECTOR_ALLOWED_ORIGIN}")"
  elif is_placeholder_value "${existing_origin}"; then
    if [[ -n "${source_origin}" ]]; then
      desired_origin="${source_origin}"
    fi
  fi
  if [[ -n "${desired_origin}" && "${desired_origin}" != "${existing_origin}" ]]; then
    env_set_value "${ENV_FILE}" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" "${desired_origin}"
    echo "[accessd-connector] Updated ACCESSD_CONNECTOR_ALLOWED_ORIGIN in ${ENV_FILE}"
  fi

  desired_verify="${existing_verify}"
  if [[ -n "${ACCESSD_CONNECTOR_BACKEND_VERIFY_URL:-}" ]]; then
    desired_verify="$(trim "${ACCESSD_CONNECTOR_BACKEND_VERIFY_URL}")"
  elif is_placeholder_value "${existing_verify}"; then
    if [[ -n "${source_verify}" ]]; then
      desired_verify="${source_verify}"
    elif [[ -n "${desired_origin}" ]]; then
      desired_verify="$(derive_verify_url_from_origin "${desired_origin}")"
    fi
  fi
  if [[ -n "${desired_verify}" && "${desired_verify}" != "${existing_verify}" ]]; then
    env_set_value "${ENV_FILE}" "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" "${desired_verify}"
    echo "[accessd-connector] Updated ACCESSD_CONNECTOR_BACKEND_VERIFY_URL in ${ENV_FILE}"
  fi
}

pick_existing_path() {
  for candidate in "$@"; do
    [[ -z "${candidate}" ]] && continue
    if [[ -e "${candidate}" ]]; then
      printf '%s' "${candidate}"
      return 0
    fi
  done
  return 1
}

prompt_for_path_if_missing() {
  local label="$1"
  local current="$2"
  if [[ -n "${current}" ]]; then
    printf '%s' "${current}"
    return 0
  fi
  if ! has_tty; then
    printf ''
    return 0
  fi
  while true; do
    read -r -p "[accessd-connector] ${label} not detected. Enter full path or press Enter to skip: " input
    input="$(trim "${input}")"
    if [[ -z "${input}" ]]; then
      printf ''
      return 0
    fi
    if [[ -e "${input}" ]]; then
      printf '%s' "${input}"
      return 0
    fi
    echo "[accessd-connector] Path does not exist: ${input}"
  done
}

choose_terminal_pref() {
  local default="auto"
  if command -v gnome-terminal >/dev/null 2>&1; then
    default="gnome-terminal"
  elif command -v konsole >/dev/null 2>&1; then
    default="konsole"
  elif command -v xfce4-terminal >/dev/null 2>&1; then
    default="xfce4-terminal"
  elif command -v xterm >/dev/null 2>&1; then
    default="xterm"
  fi
  if ! has_tty; then
    printf '%s' "${default}"
    return 0
  fi
  echo "[accessd-connector] Shell terminal preference"
  echo "  1) auto"
  echo "  2) gnome-terminal"
  echo "  3) konsole"
  echo "  4) xfce4-terminal"
  echo "  5) xterm"
  read -r -p "Select terminal [1-5, default ${default}]: " choice
  choice="$(trim "${choice}")"
  case "${choice}" in
    2|gnome-terminal) printf 'gnome-terminal' ;;
    3|konsole) printf 'konsole' ;;
    4|xfce4-terminal) printf 'xfce4-terminal' ;;
    5|xterm) printf 'xterm' ;;
    1|auto|"") printf '%s' "${default}" ;;
    *) printf '%s' "${default}" ;;
  esac
}

write_installer_config() {
  local dbeaver_path="$1"
  local filezilla_path="$2"
  local redis_cli_path="$3"
  local terminal_pref="$4"

  local should_write="1"
  if [[ -f "${CONFIG_FILE}" ]]; then
    should_write="0"
    if has_tty; then
      read -r -p "[accessd-connector] Existing config found at ${CONFIG_FILE}. Replace with installer-detected paths? [y/N]: " replace_choice
      replace_choice="$(trim "${replace_choice}")"
      case "${replace_choice}" in
        y|Y|yes|YES)
          should_write="1"
          ;;
      esac
    fi
  fi

  if [[ "${should_write}" != "1" ]]; then
    echo "[accessd-connector] Keeping existing config: ${CONFIG_FILE}"
    return 0
  fi

  mkdir -p "${CONFIG_DIR}"
  {
    echo "# AccessD Connector config"
    echo "# Generated by installer"
    echo "apps:"
    if [[ -n "${dbeaver_path}" ]]; then
      echo "  dbeaver: \"${dbeaver_path}\""
    fi
    if [[ -n "${filezilla_path}" ]]; then
      echo "  filezilla: \"${filezilla_path}\""
    fi
    if [[ -n "${redis_cli_path}" ]]; then
      echo "  redis_cli: \"${redis_cli_path}\""
    fi
    echo ""
    echo "terminal:"
    echo "  linux: \"${terminal_pref}\""
  } > "${CONFIG_FILE}"
  chmod 0600 "${CONFIG_FILE}"
  echo "[accessd-connector] Wrote config: ${CONFIG_FILE}"
}

write_runtime_env_file() {
  local default_origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-https://accessd.example.internal}"
  mkdir -p "${ENV_DIR}"
  if [[ -f "${ENV_FILE}" ]]; then
    echo "[accessd-connector] Keeping existing runtime env: ${ENV_FILE}"
    refresh_runtime_env_file
    return 0
  fi

  {
    echo "# AccessD Connector runtime env (non-sensitive defaults)"
    echo "# Keep secrets out of this file."
    echo "ACCESSD_CONNECTOR_ADDR=127.0.0.1:9494"
    echo "ACCESSD_CONNECTOR_ENABLE_TLS=true"
    echo "ACCESSD_CONNECTOR_TLS_CERT_FILE=${HOME}/.accessd-connector/tls/localhost.crt"
    echo "ACCESSD_CONNECTOR_TLS_KEY_FILE=${HOME}/.accessd-connector/tls/localhost.key"
    echo "ACCESSD_CONNECTOR_ALLOWED_ORIGIN=${default_origin}"
    echo "# Optional backend online token verification endpoint."
    echo "# If unset, connector derives: <allowed_origin>/api/connector/token/verify"
    echo "# ACCESSD_CONNECTOR_BACKEND_VERIFY_URL=https://accessd.example.internal/api/connector/token/verify"
    echo "ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN=false"
    echo "ACCESSD_CONNECTOR_ALLOW_REMOTE=false"
    echo "# Optional bootstrap env URL. Default if unset:"
    echo "# https://<ACCESSD_CONNECTOR_ALLOWED_ORIGIN host>/downloads/bootstrap/accessd-connector.env"
    echo "# ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL=https://accessd.example.internal/downloads/bootstrap/accessd-connector.env"
    echo "ACCESSD_CONNECTOR_AUTO_TRUST_SERVER_CERT=true"
    echo "# ACCESSD_CONNECTOR_TRUST_CERT_URL=https://accessd.example.internal/downloads/certs/accessd-server.crt"
    echo ""
    echo "# Optional legacy local-HMAC mode:"
    echo "# ACCESSD_CONNECTOR_SECRET=<shared-secret>"
  } > "${ENV_FILE}"
  chmod 0600 "${ENV_FILE}"
  echo "[accessd-connector] Wrote runtime env: ${ENV_FILE}"
}

bootstrap_runtime_env_from_server() {
  mkdir -p "${ENV_DIR}"

  local origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-https://accessd.example.internal}"
  local host
  host="$(origin_host "${origin}")"
  if [[ -z "${host}" || "${host}" == "accessd.example.internal" ]]; then
    return 0
  fi
  local env_url="${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:-https://${host}/downloads/bootstrap/accessd-connector.env}"
  local tmp_env="${ENV_FILE}.tmp.$$"

  if ! command -v curl >/dev/null 2>&1; then
    return 0
  fi
  if ! curl -fsS -k -L "${env_url}" -o "${tmp_env}"; then
    rm -f "${tmp_env}" || true
    return 0
  fi
  if ! grep -q '^ACCESSD_CONNECTOR_ADDR=' "${tmp_env}"; then
    rm -f "${tmp_env}" || true
    return 0
  fi
  if [[ -f "${ENV_FILE}" ]]; then
    refresh_runtime_env_file "${tmp_env}"
    rm -f "${tmp_env}" || true
    return 0
  fi
  mv "${tmp_env}" "${ENV_FILE}"
  chmod 0600 "${ENV_FILE}"
  echo "[accessd-connector] Downloaded runtime env from ${env_url}"
}

write_trust_refresh_script() {
  cat > "${TRUST_SCRIPT}" <<'TRUST'
#!/usr/bin/env bash
set -euo pipefail

trim() {
  local s="$1"
  s="${s#${s%%[![:space:]]*}}"
  s="${s%${s##*[![:space:]]}}"
  printf '%s' "$s"
}

origin_host() {
  local origin="$1"
  origin="$(trim "${origin}")"
  origin="${origin#http://}"
  origin="${origin#https://}"
  origin="${origin%%/*}"
  origin="${origin%%:*}"
  printf '%s' "${origin}"
}

ENV_FILE="${HOME}/.config/accessd/connector.env"
if [[ -f "${ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "${ENV_FILE}"
  set +a
fi

auto_trust="${ACCESSD_CONNECTOR_AUTO_TRUST_SERVER_CERT:-true}"
auto_trust_lc="$(printf '%s' "${auto_trust}" | tr '[:upper:]' '[:lower:]')"
case "${auto_trust_lc}" in
  0|false|no|off)
    exit 0
    ;;
esac

host="$(origin_host "${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-}")"
if [[ -z "${host}" || "${host}" == "localhost" || "${host}" == "127.0.0.1" || "${host}" == "accessd.example.internal" ]]; then
  exit 0
fi

cert_url="${ACCESSD_CONNECTOR_TRUST_CERT_URL:-https://${host}/downloads/certs/accessd-server.crt}"
cert_dir="${HOME}/.accessd-connector/certs"
cert_file="${cert_dir}/accessd-${host}.crt"
cert_state="${cert_dir}/accessd-${host}.trusted.sha256"
mkdir -p "${cert_dir}"

if ! command -v curl >/dev/null 2>&1; then
  exit 0
fi
if ! curl -fsS -k -L "${cert_url}" -o "${cert_file}"; then
  exit 0
fi

cert_hash=""
if command -v shasum >/dev/null 2>&1; then
  cert_hash="$(shasum -a 256 "${cert_file}" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  cert_hash="$(sha256sum "${cert_file}" | awk '{print $1}')"
fi
if [[ -n "${cert_hash}" && -f "${cert_state}" ]]; then
  existing_hash="$(cat "${cert_state}" 2>/dev/null || true)"
  if [[ "${existing_hash}" == "${cert_hash}" ]]; then
    exit 0
  fi
fi

trust_ok="0"
if command -v update-ca-certificates >/dev/null 2>&1; then
  target="/usr/local/share/ca-certificates/accessd-${host}.crt"
  if [[ "$(id -u)" -eq 0 ]]; then
    if cp "${cert_file}" "${target}" && update-ca-certificates >/dev/null 2>&1; then
      trust_ok="1"
    fi
  elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    if sudo cp "${cert_file}" "${target}" && sudo update-ca-certificates >/dev/null 2>&1; then
      trust_ok="1"
    fi
  fi
fi

# Trust in Chrome's NSS database (certutil from libnss3-tools)
if command -v certutil >/dev/null 2>&1; then
  nss_db="${HOME}/.pki/nssdb"
  if [[ ! -d "${nss_db}" ]]; then
    mkdir -p "${nss_db}"
    certutil -d "sql:${nss_db}" -N --empty-password 2>/dev/null || true
  fi
  if certutil -d "sql:${nss_db}" -A -t "C,," -n "AccessD Server (${host})" -i "${cert_file}" 2>/dev/null; then
    echo "[accessd-connector] Trusted AccessD server cert in Chrome NSS database."
    echo "[accessd-connector] IMPORTANT: Restart Chrome for the cert trust to take effect."
    trust_ok="1"
  fi
fi

if [[ "${trust_ok}" == "1" && -n "${cert_hash}" ]]; then
  printf '%s' "${cert_hash}" > "${cert_state}"
fi
TRUST
  chmod 0755 "${TRUST_SCRIPT}"
}

trust_accessd_server_cert() {
  if [[ -x "${TRUST_SCRIPT}" ]]; then
    "${TRUST_SCRIPT}" || true
  fi
}

trust_cert_in_nss_db() {
  local cert_file="$1"
  local nick="$2"
  [[ -f "${cert_file}" ]] || return 0
  command -v certutil >/dev/null 2>&1 || return 0
  local nss_db="${HOME}/.pki/nssdb"
  if [[ ! -d "${nss_db}" ]]; then
    mkdir -p "${nss_db}"
    certutil -d "sql:${nss_db}" -N --empty-password 2>/dev/null || true
  fi
  certutil -d "sql:${nss_db}" -A -t "C,," -n "${nick}" -i "${cert_file}" 2>/dev/null || true
  echo "[accessd-connector] Trusted '${nick}' in Chrome NSS database (${nss_db})."
  echo "[accessd-connector] IMPORTANT: Restart Chrome for the cert trust to take effect."
}

trust_local_connector_tls_cert() {
  if [[ ! -x "${TARGET_BIN}" ]]; then
    return 0
  fi
  local cert_file
  cert_file="$("${TARGET_BIN}" ensure-local-tls 2>/dev/null || true)"
  cert_file="$(trim "${cert_file}")"
  [[ -f "${cert_file}" ]] || return 0
  if command -v update-ca-certificates >/dev/null 2>&1; then
    local target="/usr/local/share/ca-certificates/accessd-connector-localhost.crt"
    if [[ "$(id -u)" -eq 0 ]]; then
      cp "${cert_file}" "${target}" && update-ca-certificates >/dev/null 2>&1 || true
    elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
      sudo cp "${cert_file}" "${target}" && sudo update-ca-certificates >/dev/null 2>&1 || true
    fi
  fi
  # Trust in Chrome's NSS database (certutil from libnss3-tools)
  trust_cert_in_nss_db "${cert_file}" "AccessD Connector Local TLS"
}

is_connector_running() {
  if curl -fsS -k --max-time 1 "https://127.0.0.1:9494/version" >/dev/null 2>&1; then
    return 0
  fi
  if curl -fsS --max-time 1 "http://127.0.0.1:9494/version" >/dev/null 2>&1; then
    return 0
  fi
  if pgrep -x "accessd-connector" >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

stop_running_connector() {
  if ! pgrep -x "accessd-connector" >/dev/null 2>&1; then
    return 0
  fi
  pkill -x "accessd-connector" >/dev/null 2>&1 || true
  for _ in {1..20}; do
    if ! pgrep -x "accessd-connector" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

start_connector() {
  if [[ -f "${ENV_FILE}" ]]; then
    set -a
    # shellcheck disable=SC1090
    . "${ENV_FILE}"
    set +a
  fi
  nohup "${TARGET_BIN}" >/tmp/accessd-connector.out 2>/tmp/accessd-connector.err < /dev/null &
}

WAS_RUNNING="0"
if is_connector_running; then
  WAS_RUNNING="1"
  echo "[accessd-connector] Existing connector process detected. Stopping before reinstall..."
  if ! stop_running_connector; then
    echo "[accessd-connector] WARNING: connector process did not stop cleanly; continuing install."
  fi
fi

mkdir -p "${INSTALL_DIR}" "${HELPER_DIR}" "${DESKTOP_DIR}"
install -m 0755 "${SOURCE_BIN}" "${TARGET_BIN}"
write_trust_refresh_script

cat > "${HANDLER_SCRIPT}" <<'HANDLER'
#!/usr/bin/env bash
set -euo pipefail

CONNECTOR_BIN="${HOME}/.local/bin/accessd-connector"
if [[ ! -x "${CONNECTOR_BIN}" ]]; then
  CONNECTOR_BIN="accessd-connector"
fi

ENV_FILE="${HOME}/.config/accessd/connector.env"
trim() {
  local s="$1"
  s="${s#${s%%[![:space:]]*}}"
  s="${s%${s##*[![:space:]]}}"
  printf '%s' "${s}"
}
urldecode() {
  local data="${1//+/ }"
  printf '%b' "${data//%/\\x}"
}
origin_from_protocol_arg() {
  local uri="${1:-}"
  [[ "${uri}" == accessd-connector://* ]] || return 0
  local query="${uri#*\?}"
  [[ "${query}" != "${uri}" ]] || return 0
  local part key value
  IFS='&' read -r -a parts <<< "${query}"
  for part in "${parts[@]}"; do
    key="${part%%=*}"
    value="${part#*=}"
    if [[ "${key}" == "origin" ]]; then
      urldecode "${value}"
      return 0
    fi
  done
}
param_from_protocol_arg() {
  local want_key="$1"
  local uri="${2:-}"
  [[ "${uri}" == accessd-connector://* ]] || return 0
  local query="${uri#*\?}"
  [[ "${query}" != "${uri}" ]] || return 0
  local part key value
  IFS='&' read -r -a parts <<< "${query}"
  for part in "${parts[@]}"; do
    key="${part%%=*}"
    value="${part#*=}"
    if [[ "${key}" == "${want_key}" ]]; then
      urldecode "${value}"
      return 0
    fi
  done
}
env_get_value() {
  local key="$1"
  [[ -f "${ENV_FILE}" ]] || return 1
  awk -v key="${key}" '
    /^[[:space:]]*#/ { next }
    index($0, key "=") == 1 {
      sub(/^[^=]*=/, "", $0)
      print $0
      exit 0
    }
  ' "${ENV_FILE}"
}
env_set_value() {
  local key="$1"
  local value="$2"
  local tmp_file="${ENV_FILE}.tmp.$$"
  awk -v key="${key}" -v value="${value}" '
    BEGIN { done=0 }
    {
      if (!done && $0 !~ /^[[:space:]]*#/ && index($0, key "=") == 1) {
        print key "=" value
        done=1
        next
      }
      print
    }
    END {
      if (!done) {
        print key "=" value
      }
    }
  ' "${ENV_FILE}" > "${tmp_file}"
  mv "${tmp_file}" "${ENV_FILE}"
}
maybe_refresh_origin_from_protocol_arg() {
  [[ -f "${ENV_FILE}" ]] || return 0
  local incoming_origin current_origin current_verify desired_verify
  incoming_origin="$(trim "$(origin_from_protocol_arg "${1:-}" || true)")"
  [[ "${incoming_origin}" == http://* || "${incoming_origin}" == https://* ]] || return 0
  current_origin="$(trim "$(env_get_value "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" || true)")"
  if [[ -z "${current_origin}" || "${current_origin}" == *"accessd.example.internal"* ]]; then
    env_set_value "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" "${incoming_origin}"
  fi
  current_verify="$(trim "$(env_get_value "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" || true)")"
  if [[ -z "${current_verify}" || "${current_verify}" == *"accessd.example.internal"* ]]; then
    desired_verify="${incoming_origin%/}/api/connector/token/verify"
    env_set_value "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" "${desired_verify}"
  fi
}
maybe_apply_signed_bootstrap() {
  [[ -f "${ENV_FILE}" ]] || return 0
  command -v curl >/dev/null 2>&1 || return 0
  local arg="${1:-}" incoming_origin bootstrap_token verify_endpoint payload origin verify_url
  incoming_origin="$(trim "$(origin_from_protocol_arg "${arg}" || true)")"
  bootstrap_token="$(trim "$(param_from_protocol_arg "bootstrap_token" "${arg}" || true)")"
  [[ -n "${incoming_origin}" && -n "${bootstrap_token}" ]] || return 0
  [[ "${incoming_origin}" == http://* || "${incoming_origin}" == https://* ]] || return 0
  verify_endpoint="${incoming_origin%/}/api/connector/bootstrap/verify"
  payload="$(printf '{"token":"%s"}' "${bootstrap_token}")"
  local resp
  resp="$(curl -fsS -k -H "Content-Type: application/json" -X POST -d "${payload}" "${verify_endpoint}" 2>/dev/null || true)"
  [[ -n "${resp}" ]] || return 0
  if ! printf '%s' "${resp}" | grep -q '"valid"[[:space:]]*:[[:space:]]*true'; then
    return 0
  fi
  origin="$(printf '%s' "${resp}" | sed -n 's/.*"origin"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  verify_url="$(printf '%s' "${resp}" | sed -n 's/.*"backend_verify_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  origin="$(trim "${origin}")"
  verify_url="$(trim "${verify_url}")"
  if [[ "${origin}" == http://* || "${origin}" == https://* ]]; then
    env_set_value "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" "${origin}"
  fi
  if [[ "${verify_url}" == http://* || "${verify_url}" == https://* ]]; then
    env_set_value "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" "${verify_url}"
  fi
}
maybe_apply_signed_bootstrap "${1:-}"
maybe_refresh_origin_from_protocol_arg "${1:-}"
if [[ -f "${ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "${ENV_FILE}"
  set +a
fi

CONNECTOR_ADDR="${ACCESSD_CONNECTOR_ADDR:-127.0.0.1:9494}"
TRUST_SCRIPT="${HOME}/.accessd-connector/bin/trust-refresh-linux.sh"
BACKEND_VERIFY_URL="${ACCESSD_CONNECTOR_BACKEND_VERIFY_URL:-}"
if [[ -z "${ACCESSD_CONNECTOR_SECRET:-}" && -z "${BACKEND_VERIFY_URL}" ]]; then
  echo "[accessd-connector] WARNING: token verification not configured (set ACCESSD_CONNECTOR_BACKEND_VERIFY_URL or ACCESSD_CONNECTOR_SECRET)." >&2
fi

if curl -fsS -k --max-time 1 "https://${CONNECTOR_ADDR}/version" >/dev/null 2>&1 || \
   curl -fsS --max-time 1 "http://${CONNECTOR_ADDR}/version" >/dev/null 2>&1; then
  exit 0
fi

nohup "${CONNECTOR_BIN}" >/tmp/accessd-connector.out 2>/tmp/accessd-connector.err < /dev/null &
HANDLER
chmod 0755 "${HANDLER_SCRIPT}"

cat > "${DESKTOP_FILE}" <<EOF_DESKTOP
[Desktop Entry]
Type=Application
Name=AccessD Connector URL Handler
Comment=AccessD connector protocol bridge
Exec=${HANDLER_SCRIPT} %u
NoDisplay=true
Terminal=false
MimeType=x-scheme-handler/accessd-connector;
Categories=Network;
EOF_DESKTOP

if command -v xdg-mime >/dev/null 2>&1; then
  xdg-mime default accessd-connector.desktop x-scheme-handler/accessd-connector >/dev/null 2>&1 || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "${DESKTOP_DIR}" >/dev/null 2>&1 || true
fi

dbeaver_detected="$(pick_existing_path "$(command -v dbeaver 2>/dev/null || true)" "$(command -v dbeaver-ce 2>/dev/null || true)" "/usr/share/dbeaver/dbeaver" "/usr/share/dbeaver-ce/dbeaver" "/snap/bin/dbeaver-ce" "/opt/dbeaver/dbeaver" || true)"
filezilla_detected="$(pick_existing_path "$(command -v filezilla 2>/dev/null || true)" "/usr/bin/filezilla" "/snap/bin/filezilla" || true)"
redis_detected="$(pick_existing_path "$(command -v redis-cli 2>/dev/null || true)" "/usr/bin/redis-cli" "/usr/local/bin/redis-cli" || true)"

if ! command -v ssh >/dev/null 2>&1; then
  echo "[accessd-connector] WARNING: OpenSSH client (ssh) not detected. Shell launch will fail until installed."
fi

dbeaver_path="$(prompt_for_path_if_missing "DBeaver" "${dbeaver_detected}")"
filezilla_path="$(prompt_for_path_if_missing "FileZilla" "${filezilla_detected}")"
redis_cli_path="$(prompt_for_path_if_missing "redis-cli" "${redis_detected}")"
terminal_pref="$(choose_terminal_pref)"

write_installer_config "${dbeaver_path}" "${filezilla_path}" "${redis_cli_path}" "${terminal_pref}"
bootstrap_runtime_env_from_server
write_runtime_env_file
if [[ -f "${ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "${ENV_FILE}"
  set +a
fi
trust_accessd_server_cert
trust_local_connector_tls_cert

if [[ "${WAS_RUNNING}" == "1" ]]; then
  echo "[accessd-connector] Restarting connector after reinstall..."
  start_connector
fi

echo "[accessd-connector] Installed binary: ${TARGET_BIN}"
echo "[accessd-connector] Runtime env file: ${ENV_FILE}"
echo "[accessd-connector] Desktop handler: ${DESKTOP_FILE}"
echo "[accessd-connector] Registered URL scheme: accessd-connector://"
echo "[accessd-connector] UI auto-start can now invoke accessd-connector://start"
