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
  IFS=$'\n' sorted_dirs=($(printf '%s\n' "${connector_dirs[@]}" | sort))
  unset IFS
  ACCESSD_CONNECTOR_TAG="$(basename "${sorted_dirs[-1]}")"
fi

VM_OPT_DIR="${VM_OPT_DIR:-/opt/accessd}"
VM_ETC_DIR="${VM_ETC_DIR:-/etc/accessd}"
VM_WWW_DIR="${VM_WWW_DIR:-/var/www/accessd}"
VM_DOWNLOADS_DIR="${VM_DOWNLOADS_DIR:-/var/www/accessd-downloads}"
VM_SYSTEMD_DIR="${VM_SYSTEMD_DIR:-/etc/systemd/system}"
VM_NGINX_SITES_AVAILABLE_DIR="${VM_NGINX_SITES_AVAILABLE_DIR:-/etc/nginx/sites-available}"
VM_NGINX_SITES_ENABLED_DIR="${VM_NGINX_SITES_ENABLED_DIR:-/etc/nginx/sites-enabled}"

ACCESSD_USER="${ACCESSD_USER:-accessd}"
ACCESSD_GROUP="${ACCESSD_GROUP:-accessd}"
WEB_GROUP="${WEB_GROUP:-www-data}"
NGINX_SITE_NAME="${NGINX_SITE_NAME:-accessd.conf}"
INSTALL_NGINX="${INSTALL_NGINX:-true}"
INSTALL_POSTGRES="${INSTALL_POSTGRES:-auto}" # auto|true|false
TLS_SETUP_MODE="${TLS_SETUP_MODE:-prompt}"   # prompt|existing|self-signed|csr|skip
ACCESSD_DOMAIN="${ACCESSD_DOMAIN:-}"
ACCESSD_TLS_CERT_DIR="${ACCESSD_TLS_CERT_DIR:-/etc/ssl/accessd}"
ACCESSD_TLS_VALID_DAYS="${ACCESSD_TLS_VALID_DAYS:-825}"
ACCESSD_TLS_ORG="${ACCESSD_TLS_ORG:-AccessD}"
ACCESSD_TLS_KEY_PATH="${ACCESSD_TLS_KEY_PATH:-${ACCESSD_TLS_CERT_DIR}/privkey.pem}"
ACCESSD_TLS_CERT_PATH="${ACCESSD_TLS_CERT_PATH:-${ACCESSD_TLS_CERT_DIR}/fullchain.pem}"
ACCESSD_TLS_CSR_PATH="${ACCESSD_TLS_CSR_PATH:-${ACCESSD_TLS_CERT_DIR}/accessd.csr}"
PUBLISH_OPERATOR_TLS_CERT="${PUBLISH_OPERATOR_TLS_CERT:-true}"
ACCESSD_PUBLIC_CERT_SOURCE="${ACCESSD_PUBLIC_CERT_SOURCE:-${ACCESSD_TLS_CERT_PATH}}"

log() {
  echo "[install] $*"
}

warn() {
  echo "[install][warn] $*"
}

bool_is_true() {
  local v="${1:-}"
  case "${v,,}" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
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
        ACCESSD_CORS_ALLOWED_ORIGINS|ACCESSD_CONNECTOR_RELEASES_BASE_URL)
          if [[ "${key}" == "ACCESSD_CORS_ALLOWED_ORIGINS" ]]; then
            set_or_replace_env_kv "${env_file}" "${key}" "https://${domain}"
          else
            set_or_replace_env_kv "${env_file}" "${key}" "https://${domain}/downloads/connectors"
          fi
          ;;
        *)
          set_or_replace_env_kv "${env_file}" "${key}" "${domain}"
          ;;
      esac
    fi
  done
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

choose_tls_mode() {
  local mode="${TLS_SETUP_MODE,,}"
  case "${mode}" in
    existing|self-signed|csr|skip)
      printf '%s' "${mode}"
      return 0
      ;;
  esac
  if ! has_tty; then
    printf 'existing'
    return 0
  fi
  echo "[install] TLS setup mode"
  echo "  1) existing cert/key (default)"
  echo "  2) generate self-signed cert"
  echo "  3) generate private key + CSR"
  echo "  4) skip TLS setup"
  local choice
  read -r -p "Select mode [1-4]: " choice
  choice="$(trim "${choice}")"
  case "${choice}" in
    2|self-signed) printf 'self-signed' ;;
    3|csr) printf 'csr' ;;
    4|skip) printf 'skip' ;;
    *) printf 'existing' ;;
  esac
}

prompt_for_domain_if_needed() {
  if [[ -n "${ACCESSD_DOMAIN}" ]]; then
    return 0
  fi
  if has_tty; then
    read -r -p "AccessD domain (for nginx server_name/CORS) [accessd.example.internal]: " ACCESSD_DOMAIN
    ACCESSD_DOMAIN="$(trim "${ACCESSD_DOMAIN}")"
  fi
  if [[ -z "${ACCESSD_DOMAIN}" ]]; then
    ACCESSD_DOMAIN="accessd.example.internal"
  fi
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
      ACCESSD_PUBLIC_CERT_SOURCE="${ACCESSD_TLS_CERT_PATH}"
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
      warn "CSR created at ${ACCESSD_TLS_CSR_PATH}; install signed cert at ${ACCESSD_TLS_CERT_PATH} before enabling nginx TLS."
      ;;
    existing|skip)
      return 0
      ;;
    *)
      warn "unknown TLS_SETUP_MODE=${mode}; skipping TLS setup"
      ;;
  esac
}

nginx_tls_ready() {
  [[ -f "${ACCESSD_TLS_CERT_PATH}" && -f "${ACCESSD_TLS_KEY_PATH}" ]]
}

assert_safe_web_root() {
  local root="$1"
  if [[ -z "${root}" ]]; then
    echo "[install][error] VM_WWW_DIR cannot be empty" >&2
    exit 1
  fi
  if [[ "${root}" != /* ]]; then
    echo "[install][error] VM_WWW_DIR must be an absolute path: ${root}" >&2
    exit 1
  fi
  if [[ "${root}" == "/" || "${root}" == "/var" || "${root}" == "/var/www" ]]; then
    echo "[install][error] refusing to operate on unsafe VM_WWW_DIR=${root}" >&2
    exit 1
  fi
  if [[ "${root}" != /var/www/accessd* ]]; then
    echo "[install][error] VM_WWW_DIR must stay under /var/www/accessd*: ${root}" >&2
    exit 1
  fi
}

clear_dir_contents_safe() {
  local root="$1"
  assert_safe_web_root "${root}"
  mkdir -p "${root}"
  find "${root}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
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

require_non_placeholder_env_value() {
  local env_file="$1"
  local key="$2"
  local value
  if ! value="$(env_value_from_file "${env_file}" "${key}")"; then
    echo "[install][error] ${key} is required in ${env_file}" >&2
    exit 1
  fi
  if [[ -z "${value}" ]]; then
    echo "[install][error] ${key} is empty in ${env_file}" >&2
    exit 1
  fi
  if [[ "${value}" == *CHANGE_ME* ]]; then
    echo "[install][error] ${key} in ${env_file} still contains CHANGE_ME placeholder" >&2
    exit 1
  fi
}

enforce_required_secrets_configured() {
  local api_env="${VM_ETC_DIR}/accessd.env"
  local connector_env="${VM_ETC_DIR}/accessd-connector.env"

  require_non_placeholder_env_value "${api_env}" "ACCESSD_DB_URL"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_VAULT_KEY"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_LAUNCH_TOKEN_SECRET"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_CONNECTOR_SECRET"
  require_non_placeholder_env_value "${api_env}" "ACCESSD_DEV_ADMIN_PASSWORD"

  require_non_placeholder_env_value "${connector_env}" "ACCESSD_CONNECTOR_SECRET"
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

copy_env_if_missing() {
  local src="$1"
  local dst="$2"
  local group="$3"
  if [[ -f "${dst}" ]]; then
    cp "${src}" "${dst}.example.new"
    chown root:"${group}" "${dst}.example.new"
    chmod 0640 "${dst}.example.new"
    log "preserved existing $(basename "${dst}"), wrote template update: ${dst}.example.new"
  else
    install -o root -g "${group}" -m 0640 "${src}" "${dst}"
    log "created env file: ${dst}"
  fi
}

sql_escape_single_quotes() {
  local s="$1"
  printf "%s" "${s//\'/\'\'}"
}

parse_db_url_components() {
  local db_url="$1"
  # Matches: postgres://user:pass@host:port/db?...
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
      if [[ -z "${DB_URL_HOST:-}" ]]; then
        return 1
      fi
      case "${DB_URL_HOST}" in
        127.0.0.1|localhost|::1) return 0 ;;
        *) return 1 ;;
      esac
      ;;
    *)
      return 1
      ;;
  esac
}

setup_local_postgres_if_requested() {
  if ! read_db_url_from_env_file "${VM_ETC_DIR}/accessd.env"; then
    warn "ACCESSD_DB_URL not found in ${VM_ETC_DIR}/accessd.env; skipping postgres setup"
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
    warn "DB password still placeholder in ACCESSD_DB_URL; skipping role/db provisioning"
    return 0
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

publish_operator_tls_cert_if_available() {
  if ! bool_is_true "${PUBLISH_OPERATOR_TLS_CERT}"; then
    log "operator TLS cert publish skipped (PUBLISH_OPERATOR_TLS_CERT=${PUBLISH_OPERATOR_TLS_CERT})"
    return 0
  fi
  if [[ ! -f "${ACCESSD_PUBLIC_CERT_SOURCE}" ]]; then
    warn "operator TLS cert source not found: ${ACCESSD_PUBLIC_CERT_SOURCE}; skipping cert publish"
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
    warn "failed extracting PEM certificate from ${ACCESSD_PUBLIC_CERT_SOURCE}; skipping cert publish"
    rm -f "${cert_out}" || true
    return 0
  fi

  chown root:"${WEB_GROUP}" "${cert_out}"
  chmod 0644 "${cert_out}"
  log "published operator TLS cert: ${cert_out}"
}

log "bundle: ${BUNDLE_DIR}"
log "connector tag: ${ACCESSD_CONNECTOR_TAG}"

if bool_is_true "${INSTALL_NGINX}"; then
  ensure_apt_packages nginx curl ca-certificates openssl
fi

log "ensuring users/groups/directories"
id -u "${ACCESSD_USER}" >/dev/null 2>&1 || useradd --system --home "${VM_OPT_DIR}" --shell /usr/sbin/nologin "${ACCESSD_USER}"
getent group "${ACCESSD_GROUP}" >/dev/null 2>&1 || groupadd --system "${ACCESSD_GROUP}"
usermod -a -G "${ACCESSD_GROUP}" "${ACCESSD_USER}" >/dev/null 2>&1 || true

mkdir -p "${VM_OPT_DIR}/bin" "${VM_OPT_DIR}/migrations" "${VM_ETC_DIR}" "${VM_WWW_DIR}" "${VM_DOWNLOADS_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}"

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
cp -R "${BUNDLE_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}/." "${VM_DOWNLOADS_DIR}/connectors/${ACCESSD_CONNECTOR_TAG}/"
chown -R root:"${WEB_GROUP}" "${VM_DOWNLOADS_DIR}"
find "${VM_DOWNLOADS_DIR}" -type d -exec chmod 0755 {} +
find "${VM_DOWNLOADS_DIR}" -type f -exec chmod 0644 {} +

log "installing env + unit + nginx templates"
copy_env_if_missing "${BUNDLE_DIR}/deploy/env/accessd.env.example" "${VM_ETC_DIR}/accessd.env" "${ACCESSD_GROUP}"
copy_env_if_missing "${BUNDLE_DIR}/deploy/env/accessd-connector.env.example" "${VM_ETC_DIR}/accessd-connector.env" "${ACCESSD_GROUP}"
enforce_required_secrets_configured
install -o root -g root -m 0644 "${BUNDLE_DIR}/deploy/systemd/accessd.service" "${VM_SYSTEMD_DIR}/accessd.service"
install -o root -g root -m 0644 "${BUNDLE_DIR}/deploy/systemd/accessd-connector.service" "${VM_SYSTEMD_DIR}/accessd-connector.service"
if bool_is_true "${INSTALL_NGINX}"; then
  install -o root -g root -m 0644 "${BUNDLE_DIR}/deploy/nginx/accessd.conf.example" "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}"
  ln -sfn "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}" "${VM_NGINX_SITES_ENABLED_DIR}/${NGINX_SITE_NAME}"
fi

prompt_for_domain_if_needed
apply_domain_to_api_env_if_placeholder "${VM_ETC_DIR}/accessd.env" "${ACCESSD_DOMAIN}"

TLS_MODE="$(choose_tls_mode)"
setup_tls_assets "${TLS_MODE}"

if bool_is_true "${INSTALL_NGINX}"; then
  configure_nginx_site_for_domain_and_tls \
    "${VM_NGINX_SITES_AVAILABLE_DIR}/${NGINX_SITE_NAME}" \
    "${ACCESSD_DOMAIN}" \
    "${ACCESSD_TLS_CERT_PATH}" \
    "${ACCESSD_TLS_KEY_PATH}"
fi

publish_operator_tls_cert_if_available

setup_local_postgres_if_requested

log "reloading systemd + services"
systemctl daemon-reload
systemctl enable accessd
if systemctl is-active --quiet accessd; then
  systemctl restart accessd
else
  systemctl start accessd
fi
if bool_is_true "${INSTALL_NGINX}"; then
  if nginx_tls_ready; then
    systemctl enable --now nginx
    nginx -t
    systemctl reload nginx
  else
    warn "nginx TLS cert/key not ready at ${ACCESSD_TLS_CERT_PATH} and ${ACCESSD_TLS_KEY_PATH}; skipping nginx start/reload"
    warn "complete TLS cert provisioning and run: nginx -t && systemctl reload nginx"
  fi
fi

echo
log "completed"
echo "  - env file: ${VM_ETC_DIR}/accessd.env"
echo "  - example updates: ${VM_ETC_DIR}/accessd.env.example.new (if env already existed)"
echo "  - service status: systemctl status accessd --no-pager"
echo "  - logs: journalctl -u accessd -f"
