#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
  echo "[install] run as root: sudo ./install_on_vm.sh"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUNDLE_DIR="${BUNDLE_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"

if [[ ! -f "${BUNDLE_DIR}/bin/accessd" ]]; then
  echo "[install] expected bundle layout not found at ${BUNDLE_DIR}"
  echo "[install] this script is intended for deploy bundles from scripts/build_deploy_bundle.sh"
  exit 1
fi

ACCESSD_CONNECTOR_TAG="${ACCESSD_CONNECTOR_TAG:-}"
if [[ -z "${ACCESSD_CONNECTOR_TAG}" ]]; then
  shopt -s nullglob
  connector_dirs=("${BUNDLE_DIR}"/connectors/v*)
  shopt -u nullglob
  if [[ "${#connector_dirs[@]}" -eq 0 ]]; then
    echo "[install] no connectors/v* directory found under ${BUNDLE_DIR}/connectors"
    exit 1
  fi
  IFS=$'\n' sorted_dirs=($(printf '%s\n' "${connector_dirs[@]}" | sort -V))
  unset IFS
  ACCESSD_CONNECTOR_TAG="$(basename "${sorted_dirs[-1]}")"
fi

VM_OPT_DIR="${VM_OPT_DIR:-/opt/accessd}"
VM_ETC_DIR="${VM_ETC_DIR:-/etc/accessd}"
VM_WWW_DIR="${VM_WWW_DIR:-/var/www/accessd}"
VM_DOWNLOADS_DIR="${VM_DOWNLOADS_DIR:-/var/www/accessd-downloads}"
VM_STATE_DIR="${VM_STATE_DIR:-/var/lib/accessd}"
VM_SYSTEMD_DIR="${VM_SYSTEMD_DIR:-/etc/systemd/system}"
VM_NGINX_SITES_AVAILABLE_DIR="${VM_NGINX_SITES_AVAILABLE_DIR:-/etc/nginx/sites-available}"
VM_NGINX_SITES_ENABLED_DIR="${VM_NGINX_SITES_ENABLED_DIR:-/etc/nginx/sites-enabled}"

ACCESSD_USER="${ACCESSD_USER:-accessd}"
ACCESSD_GROUP="${ACCESSD_GROUP:-accessd}"
WEB_GROUP="${WEB_GROUP:-www-data}"
NGINX_SITE_NAME="${NGINX_SITE_NAME:-accessd.conf}"

INSTALL_NGINX="${INSTALL_NGINX:-auto}"          # auto|true|false
INSTALL_POSTGRES="${INSTALL_POSTGRES:-auto}"    # auto|true|false
TLS_SETUP_MODE="${TLS_SETUP_MODE:-auto}"        # auto|prompt|existing|self-signed|csr|skip
ACCESSD_DOMAIN="${ACCESSD_DOMAIN:-}"

ACCESSD_TLS_CERT_DIR="${ACCESSD_TLS_CERT_DIR:-/etc/ssl/accessd}"
ACCESSD_TLS_VALID_DAYS="${ACCESSD_TLS_VALID_DAYS:-825}"
ACCESSD_TLS_ORG="${ACCESSD_TLS_ORG:-AccessD}"
ACCESSD_TLS_KEY_PATH="${ACCESSD_TLS_KEY_PATH:-${ACCESSD_TLS_CERT_DIR}/privkey.pem}"
ACCESSD_TLS_CERT_PATH="${ACCESSD_TLS_CERT_PATH:-${ACCESSD_TLS_CERT_DIR}/fullchain.pem}"
ACCESSD_TLS_CSR_PATH="${ACCESSD_TLS_CSR_PATH:-${ACCESSD_TLS_CERT_DIR}/accessd.csr}"

ACCESSD_PUBLIC_CERT_SOURCE="${ACCESSD_PUBLIC_CERT_SOURCE:-${ACCESSD_TLS_CERT_PATH}}"
PUBLISH_OPERATOR_CONNECTOR_ENV="${PUBLISH_OPERATOR_CONNECTOR_ENV:-true}"
PUBLISH_OPERATOR_TLS_CERT="${PUBLISH_OPERATOR_TLS_CERT:-true}"

GENERATED_ADMIN_PASSWORD=""
BOOTSTRAP_ADMIN_USERNAME=""

log() {
  echo "[install] $*"
}

warn() {
  echo "[install][warn] $*"
}

fail() {
  echo "[install][error] $*" >&2
  exit 1
}

bool_is_true() {
  local v="${1:-}"
  case "${v,,}" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

version_greater_than() {
  local left="$1"
  local right="$2"
  local greatest
  greatest="$(printf '%s\n%s\n' "${left}" "${right}" | sort -V | tail -n 1)"
  [[ "${greatest}" == "${left}" && "${left}" != "${right}" ]]
}

should_manage_nginx() {
  case "${INSTALL_NGINX,,}" in
    false|0|no|off) return 1 ;;
    true|1|yes|on) return 0 ;;
    auto|"") return 0 ;;
    *) return 0 ;;
  esac
}

has_tty() {
  [[ -t 0 && -t 1 ]]
}

trim() {
  local s="${1:-}"
  s="${s#${s%%[![:space:]]*}}"
  s="${s%${s##*[![:space:]]}}"
  printf '%s' "${s}"
}

escape_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

set_or_replace_env_kv() {
  local env_file="$1"
  local key="$2"
  local value="$3"
  local escaped
  escaped="$(escape_sed_replacement "${value}")"
  if grep -qE "^${key}=" "${env_file}"; then
    sed -i -E "s|^${key}=.*$|${key}=${escaped}|" "${env_file}"
  else
    echo "${key}=${value}" >> "${env_file}"
  fi
}

remove_env_kv() {
  local env_file="$1"
  local key="$2"
  sed -i -E "/^${key}=.*/d" "${env_file}"
}

env_value_from_file() {
  local env_file="$1"
  local key="$2"
  local line
  line="$(grep -E "^${key}=" "${env_file}" | tail -n 1 || true)"
  if [[ -z "${line}" ]]; then
    return 1
  fi
  local value="${line#${key}=}"
  value="${value%\"}"
  value="${value#\"}"
  value="${value%\'}"
  value="${value#\'}"
  printf '%s' "${value}"
}

prompt_with_default() {
  local label="$1"
  local default="$2"
  if ! has_tty; then
    printf '%s' "${default}"
    return 0
  fi
  local input
  read -r -p "${label} [${default}]: " input
  input="$(trim "${input}")"
  if [[ -z "${input}" ]]; then
    printf '%s' "${default}"
  else
    printf '%s' "${input}"
  fi
}

generate_secret_b64_32() {
  openssl rand -base64 32 | tr -d '\n'
}

is_base64_32_bytes() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 1
  local tmp
  tmp="$(mktemp)"
  if ! printf '%s' "${value}" | openssl base64 -d -A -out "${tmp}" 2>/dev/null; then
    rm -f "${tmp}" || true
    return 1
  fi
  local size
  size="$(wc -c < "${tmp}" | tr -d '[:space:]')"
  rm -f "${tmp}" || true
  [[ "${size}" == "32" ]]
}

require_non_placeholder_env_value() {
  local env_file="$1"
  local key="$2"
  local value
  if ! value="$(env_value_from_file "${env_file}" "${key}")"; then
    fail "${key} is required in ${env_file}"
  fi
  if [[ -z "${value}" ]]; then
    fail "${key} is empty in ${env_file}"
  fi
  if [[ "${value}" == *CHANGE_ME* ]]; then
    fail "${key} in ${env_file} still contains CHANGE_ME placeholder"
  fi
}

ensure_apt_packages() {
  if ! command -v apt-get >/dev/null 2>&1; then
    warn "apt-get not found; package installation skipped"
    return 0
  fi
  local pkgs=("$@")
  local missing=()
  for p in "${pkgs[@]}"; do
    if ! dpkg -s "${p}" >/dev/null 2>&1; then
      missing+=("${p}")
    fi
  done
  if [[ "${#missing[@]}" -eq 0 ]]; then
    return 0
  fi
  log "installing packages: ${missing[*]}"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y "${missing[@]}"
}

assert_safe_web_root() {
  local root="$1"
  if [[ -z "${root}" ]]; then
    fail "VM_WWW_DIR cannot be empty"
  fi
  if [[ "${root}" != /* ]]; then
    fail "VM_WWW_DIR must be an absolute path: ${root}"
  fi
  if [[ "${root}" == "/" || "${root}" == "/var" || "${root}" == "/var/www" ]]; then
    fail "refusing to operate on unsafe VM_WWW_DIR=${root}"
  fi
  if [[ "${root}" != /var/www/accessd* ]]; then
    fail "VM_WWW_DIR must stay under /var/www/accessd*: ${root}"
  fi
}

clear_dir_contents_safe() {
  local root="$1"
  assert_safe_web_root "${root}"
  mkdir -p "${root}"
  find "${root}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
}

copy_env_if_missing() {
  local src="$1"
  local dst="$2"
  local group="$3"
  if [[ -f "${dst}" ]]; then
    cp "${src}" "${dst}.example.new"
    chown root:"${group}" "${dst}.example.new"
    chmod 0640 "${dst}.example.new"
    log "preserved existing $(basename "${dst}"), wrote template update: ${dst}.example.new"
    merge_env_missing_keys "${src}" "${dst}"
  else
    install -o root -g "${group}" -m 0640 "${src}" "${dst}"
    log "created env file: ${dst}"
  fi
}

merge_env_missing_keys() {
  local template="$1"
  local target="$2"
  [[ -f "${template}" && -f "${target}" ]] || return 0

  local added=0
  while IFS= read -r line; do
    [[ "${line}" =~ ^[A-Z0-9_]+= ]] || continue
    local key="${line%%=*}"
    if ! grep -qE "^${key}=" "${target}"; then
      if [[ "${added}" -eq 0 ]]; then
        {
          echo ""
          echo "# Added automatically from latest template during upgrade."
        } >> "${target}"
      fi
      echo "${line}" >> "${target}"
      added=$((added + 1))
    fi
  done < "${template}"

  if [[ "${added}" -gt 0 ]]; then
    log "merged ${added} missing env keys into ${target} (existing values untouched)"
  fi
}

prompt_for_domain_if_needed() {
  if [[ -n "${ACCESSD_DOMAIN}" ]]; then
    return 0
  fi

  local inferred_domain=""
  local source_val=""
  local first_origin=""
  local nginx_site_file="${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}"

  extract_host_from_value() {
    local raw="$1"
    raw="$(trim "${raw}")"
    [[ -n "${raw}" ]] || return 1
    raw="${raw%%,*}"
    raw="$(trim "${raw}")"
    raw="${raw#http://}"
    raw="${raw#https://}"
    raw="${raw%%/*}"
    raw="${raw%%:*}"
    raw="$(trim "${raw}")"
    [[ -n "${raw}" ]] || return 1
    [[ "${raw}" == "localhost" || "${raw}" == "127.0.0.1" || "${raw}" == "accessd.example.internal" ]] && return 1
    printf '%s' "${raw}"
    return 0
  }

  if [[ -z "${inferred_domain}" && -f "${VM_ETC_DIR}/accessd.env" ]]; then
    source_val="$(env_value_from_file "${VM_ETC_DIR}/accessd.env" "ACCESSD_CORS_ALLOWED_ORIGINS" || true)"
    if inferred_domain="$(extract_host_from_value "${source_val}" 2>/dev/null)"; then :; else inferred_domain=""; fi
  fi
  if [[ -z "${inferred_domain}" && -f "${VM_ETC_DIR}/accessd.env" ]]; then
    source_val="$(env_value_from_file "${VM_ETC_DIR}/accessd.env" "ACCESSD_CONNECTOR_RELEASES_BASE_URL" || true)"
    if inferred_domain="$(extract_host_from_value "${source_val}" 2>/dev/null)"; then :; else inferred_domain=""; fi
  fi
  if [[ -z "${inferred_domain}" && -f "${VM_ETC_DIR}/accessd-connector.env" ]]; then
    source_val="$(env_value_from_file "${VM_ETC_DIR}/accessd-connector.env" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" || true)"
    if inferred_domain="$(extract_host_from_value "${source_val}" 2>/dev/null)"; then :; else inferred_domain=""; fi
  fi
  if [[ -z "${inferred_domain}" && -f "${nginx_site_file}" ]]; then
    first_origin="$(awk '/^[[:space:]]*server_name[[:space:]]+/{
      for (i=2; i<=NF; i++) {
        gsub(/;/, "", $i)
        if ($i != "_" && $i != "localhost" && $i != "127.0.0.1" && $i != "accessd.example.internal" && $i != "") {
          print $i
          exit
        }
      }
    }' "${nginx_site_file}" | head -n 1 || true)"
    first_origin="$(trim "${first_origin}")"
    if [[ -n "${first_origin}" ]]; then
      inferred_domain="${first_origin}"
    fi
  fi

  if [[ -n "${inferred_domain}" ]]; then
    ACCESSD_DOMAIN="${inferred_domain}"
    log "reusing existing AccessD domain: ${ACCESSD_DOMAIN}"
    return 0
  fi

  if has_tty; then
    read -r -p "AccessD domain (nginx server_name + API/UI origin) [accessd.example.internal]: " ACCESSD_DOMAIN
    ACCESSD_DOMAIN="$(trim "${ACCESSD_DOMAIN}")"
  fi
  if [[ -z "${ACCESSD_DOMAIN}" ]]; then
    ACCESSD_DOMAIN="accessd.example.internal"
  fi
}

apply_domain_to_api_env_if_placeholder() {
  local env_file="$1"
  local domain="$2"
  [[ -f "${env_file}" ]] || return 0

  local keys=(
    "ACCESSD_CORS_ALLOWED_ORIGINS"
    "ACCESSD_SSH_PROXY_PUBLIC_HOST"
    "ACCESSD_PG_PROXY_PUBLIC_HOST"
    "ACCESSD_MYSQL_PROXY_PUBLIC_HOST"
    "ACCESSD_MSSQL_PROXY_PUBLIC_HOST"
    "ACCESSD_REDIS_PROXY_PUBLIC_HOST"
    "ACCESSD_CONNECTOR_RELEASES_BASE_URL"
  )

  for key in "${keys[@]}"; do
    local current
    current="$(env_value_from_file "${env_file}" "${key}" || true)"
    if [[ -z "${current}" || "${current}" == *accessd.example.internal* ]]; then
      case "${key}" in
        ACCESSD_CORS_ALLOWED_ORIGINS)
          set_or_replace_env_kv "${env_file}" "${key}" "https://${domain}"
          ;;
        ACCESSD_CONNECTOR_RELEASES_BASE_URL)
          set_or_replace_env_kv "${env_file}" "${key}" "https://${domain}/downloads/connectors"
          ;;
        *)
          set_or_replace_env_kv "${env_file}" "${key}" "${domain}"
          ;;
      esac
    fi
  done
}

choose_tls_mode() {
  local mode="${TLS_SETUP_MODE,,}"
  case "${mode}" in
    existing|self-signed|csr|skip)
      printf '%s' "${mode}"
      return 0
      ;;
    auto|"")
      if [[ -f "${ACCESSD_TLS_CERT_PATH}" && -f "${ACCESSD_TLS_KEY_PATH}" ]]; then
        printf 'existing'
      else
        printf 'self-signed'
      fi
      return 0
      ;;
  esac

  if [[ "${mode}" != "prompt" ]]; then
    warn "unknown TLS_SETUP_MODE=${TLS_SETUP_MODE}; using auto behavior"
    if [[ -f "${ACCESSD_TLS_CERT_PATH}" && -f "${ACCESSD_TLS_KEY_PATH}" ]]; then
      printf 'existing'
    else
      printf 'self-signed'
    fi
    return 0
  fi

  if ! has_tty; then
    if [[ -f "${ACCESSD_TLS_CERT_PATH}" && -f "${ACCESSD_TLS_KEY_PATH}" ]]; then
      printf 'existing'
    else
      printf 'self-signed'
    fi
    return 0
  fi

  echo "[install] TLS setup mode" >&2
  echo "  1) existing cert/key (default)" >&2
  echo "  2) generate self-signed cert" >&2
  echo "  3) generate private key + CSR" >&2
  echo "  4) skip TLS setup" >&2
  local choice
  read -r -p "Select mode [1-4]: " choice </dev/tty
  choice="$(trim "${choice}")"
  case "${choice}" in
    2|self-signed) printf 'self-signed' ;;
    3|csr) printf 'csr' ;;
    4|skip) printf 'skip' ;;
    *) printf 'existing' ;;
  esac
}

setup_tls_assets() {
  local mode="$1"
  mkdir -p "${ACCESSD_TLS_CERT_DIR}"

  case "${mode}" in
    self-signed)
      log "generating self-signed TLS cert for domain ${ACCESSD_DOMAIN}"
      openssl req -x509 -newkey rsa:4096 -sha256 -nodes \
        -keyout "${ACCESSD_TLS_KEY_PATH}" \
        -out "${ACCESSD_TLS_CERT_PATH}" \
        -days "${ACCESSD_TLS_VALID_DAYS}" \
        -subj "/CN=${ACCESSD_DOMAIN}/O=${ACCESSD_TLS_ORG}" \
        -addext "subjectAltName=DNS:${ACCESSD_DOMAIN}"
      chmod 0600 "${ACCESSD_TLS_KEY_PATH}"
      chmod 0644 "${ACCESSD_TLS_CERT_PATH}"
      ;;
    csr)
      log "generating TLS private key + CSR for domain ${ACCESSD_DOMAIN}"
      openssl req -new -newkey rsa:4096 -sha256 -nodes \
        -keyout "${ACCESSD_TLS_KEY_PATH}" \
        -out "${ACCESSD_TLS_CSR_PATH}" \
        -subj "/CN=${ACCESSD_DOMAIN}/O=${ACCESSD_TLS_ORG}" \
        -addext "subjectAltName=DNS:${ACCESSD_DOMAIN}"
      chmod 0600 "${ACCESSD_TLS_KEY_PATH}"
      chmod 0644 "${ACCESSD_TLS_CSR_PATH}"
      warn "CSR created at ${ACCESSD_TLS_CSR_PATH}; install signed cert at ${ACCESSD_TLS_CERT_PATH} before nginx TLS enable."
      ;;
    existing|skip)
      ;;
    *)
      warn "unknown TLS mode ${mode}; skipping TLS setup"
      ;;
  esac
}

nginx_tls_ready() {
  [[ -f "${ACCESSD_TLS_CERT_PATH}" && -f "${ACCESSD_TLS_KEY_PATH}" ]]
}

configure_nginx_site_for_domain_and_tls() {
  local site_file="$1"
  local domain="$2"
  local cert_path="$3"
  local key_path="$4"
  [[ -f "${site_file}" ]] || return 0

  local domain_esc cert_esc key_esc
  domain_esc="$(escape_sed_replacement "${domain}")"
  cert_esc="$(escape_sed_replacement "${cert_path}")"
  key_esc="$(escape_sed_replacement "${key_path}")"

  sed -i -E "s|server_name accessd\\.example\\.internal;|server_name ${domain_esc};|g" "${site_file}"
  sed -i -E "s|ssl_certificate[[:space:]]+/etc/ssl/accessd/fullchain\\.pem;|ssl_certificate     ${cert_esc};|g" "${site_file}"
  sed -i -E "s|ssl_certificate_key[[:space:]]+/etc/ssl/accessd/privkey\\.pem;|ssl_certificate_key ${key_esc};|g" "${site_file}"
}

configure_api_env_interactive() {
  local env_file="${VM_ETC_DIR}/accessd.env"
  [[ -f "${env_file}" ]] || return 0

  apply_domain_to_api_env_if_placeholder "${env_file}" "${ACCESSD_DOMAIN}"

  local db_url
  db_url="$(env_value_from_file "${env_file}" "ACCESSD_DB_URL" || true)"
  if [[ -z "${db_url}" || "${db_url}" == *CHANGE_ME* ]]; then
    local use_local="yes"
    if has_tty; then
      local choice
      read -r -p "Use local PostgreSQL on this VM? [Y/n]: " choice
      choice="$(trim "${choice}")"
      case "${choice,,}" in
        n|no) use_local="no" ;;
      esac
    fi

    if [[ "${use_local}" == "yes" ]]; then
      local db_user db_name db_pass
      db_user="$(prompt_with_default "PostgreSQL username" "accessd")"
      db_name="$(prompt_with_default "PostgreSQL database name" "accessd")"
      db_pass="$(generate_secret_b64_32)"
      if has_tty; then
        local pass_in
        read -r -p "PostgreSQL password (leave empty to auto-generate): " pass_in
        pass_in="$(trim "${pass_in}")"
        if [[ -n "${pass_in}" ]]; then
          db_pass="${pass_in}"
        fi
      fi
      set_or_replace_env_kv "${env_file}" "ACCESSD_DB_URL" "postgres://${db_user}:${db_pass}@127.0.0.1:5432/${db_name}?sslmode=disable"
      log "configured ACCESSD_DB_URL for local postgres"
    else
      local remote_url=""
      if has_tty; then
        read -r -p "Enter ACCESSD_DB_URL (full postgres URL): " remote_url
      fi
      remote_url="$(trim "${remote_url:-}")"
      [[ -n "${remote_url}" ]] && set_or_replace_env_kv "${env_file}" "ACCESSD_DB_URL" "${remote_url}"
    fi
  fi

  local vault_key launch_secret connector_secret admin_password admin_email
  local admin_username
  vault_key="$(env_value_from_file "${env_file}" "ACCESSD_VAULT_KEY" || true)"
  launch_secret="$(env_value_from_file "${env_file}" "ACCESSD_LAUNCH_TOKEN_SECRET" || true)"
  connector_secret="$(env_value_from_file "${env_file}" "ACCESSD_CONNECTOR_SECRET" || true)"
  admin_password="$(env_value_from_file "${env_file}" "ACCESSD_DEV_ADMIN_PASSWORD" || true)"
  admin_email="$(env_value_from_file "${env_file}" "ACCESSD_DEV_ADMIN_EMAIL" || true)"
  admin_username="$(env_value_from_file "${env_file}" "ACCESSD_DEV_ADMIN_USERNAME" || true)"

  if [[ -z "${vault_key}" || "${vault_key}" == *CHANGE_ME* ]] || ! is_base64_32_bytes "${vault_key}"; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_VAULT_KEY" "$(generate_secret_b64_32)"
    log "generated ACCESSD_VAULT_KEY"
  fi
  if [[ -z "${launch_secret}" || "${launch_secret}" == *CHANGE_ME* ]]; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_LAUNCH_TOKEN_SECRET" "$(generate_secret_b64_32)"
    log "generated ACCESSD_LAUNCH_TOKEN_SECRET"
  fi
  if [[ -z "${connector_secret}" || "${connector_secret}" == *CHANGE_ME* ]]; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_SECRET" "$(generate_secret_b64_32)"
    log "generated ACCESSD_CONNECTOR_SECRET"
  fi

  if has_tty; then
    local current_email
    current_email="${admin_email:-accessd-admin@${ACCESSD_DOMAIN}}"
    set_or_replace_env_kv "${env_file}" "ACCESSD_DEV_ADMIN_EMAIL" "$(prompt_with_default "Bootstrap admin email" "${current_email}")"
  elif [[ -z "${admin_email}" || "${admin_email}" == *CHANGE_ME* ]]; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_DEV_ADMIN_EMAIL" "accessd-admin@${ACCESSD_DOMAIN}"
  fi

  if [[ -z "${admin_password}" || "${admin_password}" == *CHANGE_ME* ]]; then
    local generated
    generated="$(generate_secret_b64_32)"
    if has_tty; then
      local admin_in
      read -r -p "Bootstrap admin password (leave empty to auto-generate): " admin_in
      admin_in="$(trim "${admin_in}")"
      if [[ -n "${admin_in}" ]]; then
        generated="${admin_in}"
      fi
    fi
    set_or_replace_env_kv "${env_file}" "ACCESSD_DEV_ADMIN_PASSWORD" "${generated}"
    GENERATED_ADMIN_PASSWORD="${generated}"
    log "configured ACCESSD_DEV_ADMIN_PASSWORD"
  fi

  BOOTSTRAP_ADMIN_USERNAME="$(env_value_from_file "${env_file}" "ACCESSD_DEV_ADMIN_USERNAME" || true)"
  if [[ -z "${BOOTSTRAP_ADMIN_USERNAME}" ]]; then
    BOOTSTRAP_ADMIN_USERNAME="${admin_username:-admin}"
  fi

  set_or_replace_env_kv "${env_file}" "ACCESSD_ALLOW_UNSAFE_MODE" "false"
}

configure_connector_env_defaults() {
  local env_file="${VM_ETC_DIR}/accessd-connector.env"
  [[ -f "${env_file}" ]] || return 0

  set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" "https://${ACCESSD_DOMAIN}"
  set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" "https://${ACCESSD_DOMAIN}/api/connector/token/verify"
  # Operator bootstrap must not ship shared secret. Backend verify mode is default.
  remove_env_kv "${env_file}" "ACCESSD_CONNECTOR_SECRET"
}

sync_connector_release_metadata() {
  local env_file="${VM_ETC_DIR}/accessd.env"
  [[ -f "${env_file}" ]] || return 0

  local deployed_version="${ACCESSD_CONNECTOR_TAG#v}"
  if [[ -z "${deployed_version}" ]]; then
    return 0
  fi

  local latest_version="${deployed_version}"
  local connectors_root="${VM_DOWNLOADS_DIR}/connectors"
  local existing_tag existing_version
  shopt -s nullglob
  local existing_dirs=("${connectors_root}"/v*)
  shopt -u nullglob
  if [[ "${#existing_dirs[@]}" -gt 0 ]]; then
    IFS=$'\n' sorted_existing=($(printf '%s\n' "${existing_dirs[@]}" | sort -V))
    unset IFS
    existing_tag="$(basename "${sorted_existing[-1]}")"
    existing_version="${existing_tag#v}"
    if [[ -n "${existing_version}" ]]; then
      latest_version="${existing_version}"
    fi
  fi

  set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_RELEASES_BASE_URL" "https://${ACCESSD_DOMAIN}/downloads/connectors"
  set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_RELEASES_FS_ROOT" "${VM_DOWNLOADS_DIR}/connectors"
  set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_LATEST_VERSION" "${latest_version}"

  # Preserve operator-chosen minimum when present; default to latest on first install.
  local existing_min
  existing_min="$(env_value_from_file "${env_file}" "ACCESSD_CONNECTOR_MIN_VERSION" || true)"
  existing_min="$(trim "${existing_min}")"
  if [[ -z "${existing_min}" || "${existing_min}" == *CHANGE_ME* ]]; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_MIN_VERSION" "${latest_version}"
    return 0
  fi
  # Guardrail: minimum cannot exceed latest.
  if version_greater_than "${existing_min}" "${latest_version}"; then
    set_or_replace_env_kv "${env_file}" "ACCESSD_CONNECTOR_MIN_VERSION" "${latest_version}"
  fi
}

enforce_required_secrets_configured() {
  local api_env="${VM_ETC_DIR}/accessd.env"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_DB_URL"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_VAULT_KEY"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_LAUNCH_TOKEN_SECRET"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_CONNECTOR_SECRET"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_DEV_ADMIN_PASSWORD"
}

sql_escape_single_quotes() {
  local s="$1"
  printf "%s" "${s//\'/\'\'}"
}

parse_db_url_components() {
  local db_url="$1"
  if [[ "${db_url}" =~ ^postgres(ql)?://([^:/?#]+):([^@/?#]+)@(\[[^]]+\]|[^:/?#]+)(:([0-9]+))?/([^?]+) ]]; then
    DB_URL_USER="${BASH_REMATCH[2]}"
    DB_URL_PASS="${BASH_REMATCH[3]}"
    DB_URL_HOST="${BASH_REMATCH[4]}"
    DB_URL_PORT="${BASH_REMATCH[6]:-5432}"
    DB_URL_NAME="${BASH_REMATCH[7]}"
    DB_URL_HOST="${DB_URL_HOST#[}"
    DB_URL_HOST="${DB_URL_HOST%]}"
    DB_URL_NAME="${DB_URL_NAME%%/*}"
    return 0
  fi
  return 1
}

read_db_url_from_env_file() {
  local env_file="$1"
  if [[ ! -f "${env_file}" ]]; then
    return 1
  fi
  local line
  line="$(grep -E '^ACCESSD_DB_URL=' "${env_file}" | tail -n 1 || true)"
  if [[ -z "${line}" ]]; then
    return 1
  fi
  DB_URL_RAW="${line#ACCESSD_DB_URL=}"
  DB_URL_RAW="${DB_URL_RAW%\"}"
  DB_URL_RAW="${DB_URL_RAW#\"}"
  DB_URL_RAW="${DB_URL_RAW%\'}"
  DB_URL_RAW="${DB_URL_RAW#\'}"
  [[ -n "${DB_URL_RAW}" ]]
}

should_setup_local_postgres() {
  case "${INSTALL_POSTGRES,,}" in
    true|1|yes|on) return 0 ;;
    false|0|no|off) return 1 ;;
    auto|"")
      [[ -n "${DB_URL_HOST:-}" ]] || return 1
      case "${DB_URL_HOST}" in
        127.0.0.1|localhost|::1) return 0 ;;
        *) return 1 ;;
      esac
      ;;
    *) return 1 ;;
  esac
}

normalize_local_db_sslmode_if_needed() {
  local env_file="${VM_ETC_DIR}/accessd.env"
  if ! read_db_url_from_env_file "${env_file}"; then
    return 0
  fi
  if ! parse_db_url_components "${DB_URL_RAW}"; then
    return 0
  fi
  if ! should_setup_local_postgres; then
    return 0
  fi
  if [[ "${DB_URL_RAW}" == *"sslmode=require"* ]]; then
    local new_url="${DB_URL_RAW/sslmode=require/sslmode=disable}"
    set_or_replace_env_kv "${env_file}" "ACCESSD_DB_URL" "${new_url}"
    log "updated local ACCESSD_DB_URL sslmode=require -> sslmode=disable"
  fi
}

setup_local_postgres_if_requested() {
  if ! read_db_url_from_env_file "${VM_ETC_DIR}/accessd.env"; then
    warn "ACCESSD_DB_URL missing; skipping postgres setup"
    return 0
  fi
  if ! parse_db_url_components "${DB_URL_RAW}"; then
    warn "unable to parse ACCESSD_DB_URL; skipping postgres setup"
    return 0
  fi
  if ! should_setup_local_postgres; then
    log "postgres setup skipped (INSTALL_POSTGRES=${INSTALL_POSTGRES}, db_host=${DB_URL_HOST})"
    return 0
  fi

  ensure_apt_packages postgresql
  systemctl enable --now postgresql

  if [[ "${DB_URL_PASS}" == *CHANGE_ME* ]]; then
    fail "DB password still placeholder in ACCESSD_DB_URL"
  fi

  local user_esc pass_esc db_esc
  user_esc="$(sql_escape_single_quotes "${DB_URL_USER}")"
  pass_esc="$(sql_escape_single_quotes "${DB_URL_PASS}")"
  db_esc="$(sql_escape_single_quotes "${DB_URL_NAME}")"

  log "ensuring local postgres role/database (${DB_URL_USER}/${DB_URL_NAME})"
  sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${user_esc}') THEN
    EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '${user_esc}', '${pass_esc}');
  ELSE
    EXECUTE format('ALTER ROLE %I WITH LOGIN PASSWORD %L', '${user_esc}', '${pass_esc}');
  END IF;
END
\$\$;
SQL

  if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${db_esc}'" | grep -q 1; then
    sudo -u postgres createdb -O "${DB_URL_USER}" "${DB_URL_NAME}"
  fi

  sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
REVOKE ALL ON DATABASE "${DB_URL_NAME}" FROM public;
SQL
}

render_accessd_systemd_unit() {
  local src="$1"
  local dst="$2"
  local wd_esc env_esc pre_esc start_esc rw_esc user_esc group_esc
  wd_esc="$(escape_sed_replacement "${VM_OPT_DIR}")"
  env_esc="$(escape_sed_replacement "${VM_ETC_DIR}/accessd.env")"
  pre_esc="$(escape_sed_replacement "${VM_OPT_DIR}/bin/accessd migrate up")"
  start_esc="$(escape_sed_replacement "${VM_OPT_DIR}/bin/accessd server")"
  rw_esc="$(escape_sed_replacement "${VM_STATE_DIR}")"
  user_esc="$(escape_sed_replacement "${ACCESSD_USER}")"
  group_esc="$(escape_sed_replacement "${ACCESSD_GROUP}")"

  sed \
    -e "s|^User=.*$|User=${user_esc}|" \
    -e "s|^Group=.*$|Group=${group_esc}|" \
    -e "s|^WorkingDirectory=.*$|WorkingDirectory=${wd_esc}|" \
    -e "s|^EnvironmentFile=.*$|EnvironmentFile=${env_esc}|" \
    -e "s|^ExecStartPre=.*$|ExecStartPre=${pre_esc}|" \
    -e "s|^ExecStart=.*$|ExecStart=${start_esc}|" \
    -e "s|^ReadWritePaths=.*$|ReadWritePaths=${rw_esc}|" \
    "${src}" > "${dst}"

  chmod 0644 "${dst}"
  chown root:root "${dst}"
}

assert_required_runtime_paths() {
  local env_file="${VM_ETC_DIR}/accessd.env"
  local bin_file="${VM_OPT_DIR}/bin/accessd"
  local mig_dir="${VM_OPT_DIR}/migrations"

  [[ -f "${env_file}" ]] || fail "missing env file: ${env_file}"
  [[ -x "${bin_file}" ]] || fail "missing executable: ${bin_file}"
  [[ -d "${mig_dir}" ]] || fail "missing migrations dir: ${mig_dir}"
}

run_accessd_setup_commands() {
  local bin="${VM_OPT_DIR}/bin/accessd"
  local env_file="${VM_ETC_DIR}/accessd.env"
  log "running accessd migrate up (pre-start)"
  sudo -u "${ACCESSD_USER}" ACCESSD_CONFIG_FILE="${env_file}" "${bin}" migrate up

  log "running accessd bootstrap (pre-start)"
  sudo -u "${ACCESSD_USER}" ACCESSD_CONFIG_FILE="${env_file}" "${bin}" bootstrap
}

publish_operator_tls_cert_if_available() {
  if ! bool_is_true "${PUBLISH_OPERATOR_TLS_CERT}"; then
    return 0
  fi
  if [[ ! -f "${ACCESSD_PUBLIC_CERT_SOURCE}" ]]; then
    log "operator TLS cert publish skipped: cert not present (${ACCESSD_PUBLIC_CERT_SOURCE})"
    return 0
  fi

  local cert_dir="${VM_DOWNLOADS_DIR}/certs"
  local cert_out="${cert_dir}/accessd-server.crt"
  mkdir -p "${cert_dir}"

  awk '
    /-----BEGIN CERTIFICATE-----/ {in_cert=1}
    in_cert {print}
    /-----END CERTIFICATE-----/ {exit}
  ' "${ACCESSD_PUBLIC_CERT_SOURCE}" > "${cert_out}" || true

  if [[ ! -s "${cert_out}" ]]; then
    warn "failed extracting certificate from ${ACCESSD_PUBLIC_CERT_SOURCE}"
    rm -f "${cert_out}" || true
    return 0
  fi

  chown root:"${WEB_GROUP}" "${cert_out}"
  chmod 0644 "${cert_out}"
  log "published operator TLS cert: ${cert_out}"
}

publish_operator_connector_env_if_available() {
  if ! bool_is_true "${PUBLISH_OPERATOR_CONNECTOR_ENV}"; then
    return 0
  fi

  local source_env="${VM_ETC_DIR}/accessd-connector.env"
  [[ -f "${source_env}" ]] || { warn "connector env source not found: ${source_env}"; return 0; }

  local out_dir="${VM_DOWNLOADS_DIR}/bootstrap"
  local out_file="${out_dir}/accessd-connector.env"
  mkdir -p "${out_dir}"

  awk '
    /^ACCESSD_CONNECTOR_SECRET=/ { next }
    { print }
  ' "${source_env}" > "${out_file}"

  set_or_replace_env_kv "${out_file}" "ACCESSD_CONNECTOR_ALLOWED_ORIGIN" "https://${ACCESSD_DOMAIN}"
  set_or_replace_env_kv "${out_file}" "ACCESSD_CONNECTOR_BACKEND_VERIFY_URL" "https://${ACCESSD_DOMAIN}/api/connector/token/verify"

  chown root:"${WEB_GROUP}" "${out_file}"
  chmod 0640 "${out_file}"
  log "published operator connector env (without secret): ${out_file}"
}

write_bootstrap_notes() {
  if [[ -n "${GENERATED_ADMIN_PASSWORD}" ]]; then
    local note_file="${VM_ETC_DIR}/BOOTSTRAP_ADMIN_PASSWORD.txt"
    {
      echo "ACCESSD bootstrap admin password"
      echo "Generated at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
      echo "Admin email: $(env_value_from_file "${VM_ETC_DIR}/accessd.env" "ACCESSD_DEV_ADMIN_EMAIL" || echo unknown)"
      echo "Password: ${GENERATED_ADMIN_PASSWORD}"
      echo "Delete this file after first successful login and password rotation."
    } > "${note_file}"
    chmod 0600 "${note_file}"
    chown root:root "${note_file}"
    log "wrote generated bootstrap admin password to ${note_file}"
  fi
}

start_accessd_service_or_fail() {
  systemctl daemon-reload
  systemctl enable accessd
  if systemctl is-active --quiet accessd; then
    systemctl restart accessd
  else
    systemctl start accessd
  fi

  if ! systemctl is-active --quiet accessd; then
    echo "[install][error] accessd failed to start. Last logs:" >&2
    journalctl -u accessd -n 60 --no-pager >&2 || true
    exit 1
  fi
}

log "bundle: ${BUNDLE_DIR}"
log "connector tag: ${ACCESSD_CONNECTOR_TAG}"

if should_manage_nginx; then
  ensure_apt_packages nginx curl ca-certificates openssl
fi

log "ensuring users/groups/directories"
id -u "${ACCESSD_USER}" >/dev/null 2>&1 || useradd --system --home "${VM_OPT_DIR}" --shell /usr/sbin/nologin "${ACCESSD_USER}"
getent group "${ACCESSD_GROUP}" >/dev/null 2>&1 || groupadd --system "${ACCESSD_GROUP}"
usermod -a -G "${ACCESSD_GROUP}" "${ACCESSD_USER}" >/dev/null 2>&1 || true

mkdir -p "${VM_OPT_DIR}/bin" "${VM_OPT_DIR}/migrations" "${VM_ETC_DIR}" "${VM_WWW_DIR}" "${VM_DOWNLOADS_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}"
mkdir -p "${VM_STATE_DIR}/ssh"
chown -R "${ACCESSD_USER}:${ACCESSD_GROUP}" "${VM_STATE_DIR}"
chmod 0700 "${VM_STATE_DIR}" "${VM_STATE_DIR}/ssh"

log "installing accessd binary + migrations"
install -o root -g "${ACCESSD_GROUP}" -m 0755 "${BUNDLE_DIR}/bin/accessd" "${VM_OPT_DIR}/bin/accessd"
cp -R "${BUNDLE_DIR}/migrations/." "${VM_OPT_DIR}/migrations/"
chown -R root:"${ACCESSD_GROUP}" "${VM_OPT_DIR}/migrations"
find "${VM_OPT_DIR}/migrations" -type f -exec chmod 0644 {} +

log "installing ui static files"
clear_dir_contents_safe "${VM_WWW_DIR}"
cp -R "${BUNDLE_DIR}/ui/." "${VM_WWW_DIR}/"
chown -R root:"${WEB_GROUP}" "${VM_WWW_DIR}"
find "${VM_WWW_DIR}" -type d -exec chmod 0755 {} +
find "${VM_WWW_DIR}" -type f -exec chmod 0644 {} +

log "publishing connector artifacts"
find "${VM_DOWNLOADS_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -R "${BUNDLE_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}/." "${VM_DOWNLOADS_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}/"
chown -R root:"${WEB_GROUP}" "${VM_DOWNLOADS_DIR}"
find "${VM_DOWNLOADS_DIR}" -type d -exec chmod 0755 {} +
find "${VM_DOWNLOADS_DIR}" -type f -exec chmod 0644 {} +

log "installing env + unit + nginx templates"
copy_env_if_missing "${BUNDLE_DIR}/deploy/env/accessd.env.example" "${VM_ETC_DIR}/accessd.env" "${ACCESSD_GROUP}"
copy_env_if_missing "${BUNDLE_DIR}/deploy/env/accessd-connector.env.example" "${VM_ETC_DIR}/accessd-connector.env" "${ACCESSD_GROUP}"
render_accessd_systemd_unit "${BUNDLE_DIR}/deploy/systemd/accessd.service" "${VM_SYSTEMD_DIR}/accessd.service"

if should_manage_nginx; then
  install -o root -g root -m 0644 "${BUNDLE_DIR}/deploy/nginx/accessd.conf.example" "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}"
  ln -sfn "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}" "${VM_NGINX_SITES_ENABLED_DIR}/${NGINX_SITE_NAME}"
fi

prompt_for_domain_if_needed
configure_api_env_interactive
sync_connector_release_metadata
configure_connector_env_defaults
enforce_required_secrets_configured

TLS_MODE="$(choose_tls_mode)"
setup_tls_assets "${TLS_MODE}"

if should_manage_nginx; then
  configure_nginx_site_for_domain_and_tls \
    "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}" \
    "${ACCESSD_DOMAIN}" \
    "${ACCESSD_TLS_CERT_PATH}" \
    "${ACCESSD_TLS_KEY_PATH}"
fi

publish_operator_tls_cert_if_available
publish_operator_connector_env_if_available

normalize_local_db_sslmode_if_needed
setup_local_postgres_if_requested
assert_required_runtime_paths
run_accessd_setup_commands
write_bootstrap_notes

log "reloading systemd + services"
start_accessd_service_or_fail

if should_manage_nginx; then
  if nginx_tls_ready; then
    nginx -t
    systemctl enable --now nginx
    systemctl reload nginx
  else
    warn "nginx TLS cert/key not ready at ${ACCESSD_TLS_CERT_PATH} and ${ACCESSD_TLS_KEY_PATH}; skipping nginx start/reload"
    warn "complete TLS cert provisioning, then run: nginx -t && systemctl reload nginx"
  fi
fi

echo
log "completed"
echo "  - env file: ${VM_ETC_DIR}/accessd.env"
echo "  - service status: systemctl status accessd --no-pager"
echo "  - logs: journalctl -u accessd -f"
echo "  - connector bootstrap env: ${VM_DOWNLOADS_DIR}/bootstrap/accessd-connector.env"
if [[ -n "${BOOTSTRAP_ADMIN_USERNAME}" ]]; then
  echo "  - bootstrap admin username: ${BOOTSTRAP_ADMIN_USERNAME}"
fi
if [[ -n "${GENERATED_ADMIN_PASSWORD}" ]]; then
  echo "  - bootstrap admin password note: ${VM_ETC_DIR}/BOOTSTRAP_ADMIN_PASSWORD.txt"
  echo "  - bootstrap admin password (generated): ${GENERATED_ADMIN_PASSWORD}"
fi
