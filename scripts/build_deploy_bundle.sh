#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-${VERSION:-}}"
if [[ -z "${VERSION}" ]]; then
  echo "usage: $0 <version>  (example: $0 0.2.0)"
  exit 1
fi

VERSION="${VERSION#v}"
TAG="v${VERSION}"
API_GOOS="${API_GOOS:-linux}"
API_GOARCH="${API_GOARCH:-amd64}"
COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VM_OPT_DIR="${VM_OPT_DIR:-/opt/accessd}"
VM_ETC_DIR="${VM_ETC_DIR:-/etc/accessd}"
VM_WWW_DIR="${VM_WWW_DIR:-/var/www/accessd}"
VM_DOWNLOADS_DIR="${VM_DOWNLOADS_DIR:-/var/www/accessd-downloads}"
VM_SYSTEMD_DIR="${VM_SYSTEMD_DIR:-/etc/systemd/system}"
VM_NGINX_SITES_AVAILABLE_DIR="${VM_NGINX_SITES_AVAILABLE_DIR:-/etc/nginx/sites-available}"
VM_NGINX_SITES_ENABLED_DIR="${VM_NGINX_SITES_ENABLED_DIR:-/etc/nginx/sites-enabled}"

BUNDLE_DIR="${ROOT_DIR}/deploy/artifacts/accessd-${VERSION}"
BUNDLE_TARBALL="${ROOT_DIR}/deploy/artifacts/accessd-${VERSION}.tar.gz"
CONNECTOR_OUT_DIR="${BUNDLE_DIR}/connectors/${TAG}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "[bundle] preparing ${BUNDLE_DIR}"
rm -rf "${BUNDLE_DIR}"
mkdir -p "${BUNDLE_DIR}/bin" "${BUNDLE_DIR}/migrations" "${BUNDLE_DIR}/ui" "${BUNDLE_DIR}/connectors" "${BUNDLE_DIR}/deploy"
mkdir -p "${ROOT_DIR}/.gocache" "${ROOT_DIR}/deploy/artifacts"

echo "[bundle] building api (${API_GOOS}/${API_GOARCH})"
(
  cd "${ROOT_DIR}/apps/api"
  CGO_ENABLED=0 GOOS="${API_GOOS}" GOARCH="${API_GOARCH}" \
    GOCACHE="${ROOT_DIR}/.gocache" \
    go build -trimpath \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILT_AT}" \
    -o "${BUNDLE_DIR}/bin/accessd" ./cmd/server
)

echo "[bundle] building ui"
(
  cd "${ROOT_DIR}/apps/ui"
  if [[ ! -d "node_modules" ]]; then
    npm ci
  fi
  npm run build
)
cp -R "${ROOT_DIR}/apps/ui/dist/." "${BUNDLE_DIR}/ui/"
cp -R "${ROOT_DIR}/apps/api/migrations/." "${BUNDLE_DIR}/migrations/"

echo "[bundle] building connector release artifacts"
CONNECTOR_DIST_DIR="${ROOT_DIR}/dist/connector/${TAG}"
"${ROOT_DIR}/scripts/build_connector_release.sh" "${VERSION}"
mkdir -p "${CONNECTOR_OUT_DIR}"
cp -R "${CONNECTOR_DIST_DIR}/." "${CONNECTOR_OUT_DIR}/"

echo "[bundle] copying deployment templates"
mkdir -p "${BUNDLE_DIR}/deploy/env" "${BUNDLE_DIR}/deploy/systemd" "${BUNDLE_DIR}/deploy/nginx"
cp "${ROOT_DIR}/deploy/env/accessd.env.example" "${BUNDLE_DIR}/deploy/env/"
cp "${ROOT_DIR}/deploy/env/accessd-connector.env.example" "${BUNDLE_DIR}/deploy/env/"
cp "${ROOT_DIR}/deploy/systemd/accessd.service" "${BUNDLE_DIR}/deploy/systemd/"
cp "${ROOT_DIR}/deploy/systemd/accessd-connector.service" "${BUNDLE_DIR}/deploy/systemd/"
cp "${ROOT_DIR}/deploy/nginx/accessd.conf.example" "${BUNDLE_DIR}/deploy/nginx/"
cp "${ROOT_DIR}/deploy/install_on_vm.sh" "${BUNDLE_DIR}/deploy/install_on_vm.sh"
chmod 0755 "${BUNDLE_DIR}/deploy/install_on_vm.sh"
cp "${ROOT_DIR}/deploy/README.md" "${BUNDLE_DIR}/deploy/README.md"

cat > "${BUNDLE_DIR}/MANIFEST.txt" <<EOF
AccessD deployment bundle
version: ${VERSION}
tag: ${TAG}
commit: ${COMMIT}
built_at_utc: ${BUILT_AT}
api_target: ${API_GOOS}/${API_GOARCH}

Contents:
- bin/accessd
- migrations/* (api SQL migrations)
- ui/* (static frontend build)
- connectors/${TAG}/* (connector archives + checksums)
- deploy/* (env/systemd/nginx templates)

VM mapping defaults:
- API binary: ${VM_OPT_DIR}/bin/accessd
- API migrations: ${VM_OPT_DIR}/migrations
- Env files: ${VM_ETC_DIR}/*.env
- UI static files: ${VM_WWW_DIR}
- Connector downloads: ${VM_DOWNLOADS_DIR}/connectors/${TAG}
- systemd unit dir: ${VM_SYSTEMD_DIR}
- nginx site dirs: ${VM_NGINX_SITES_AVAILABLE_DIR}, ${VM_NGINX_SITES_ENABLED_DIR}
- installer helper: deploy/install_on_vm.sh
EOF

echo "[bundle] generating checksums"
CHECKSUM_FILE="${BUNDLE_DIR}/SHA256SUMS.txt"
rm -f "${CHECKSUM_FILE}"
if command -v sha256sum >/dev/null 2>&1; then
  while IFS= read -r file; do
    (cd "${BUNDLE_DIR}" && sha256sum "${file#${BUNDLE_DIR}/}") >> "${CHECKSUM_FILE}"
  done < <(find "${BUNDLE_DIR}" -type f ! -name "SHA256SUMS.txt" | LC_ALL=C sort)
else
  while IFS= read -r file; do
    (cd "${BUNDLE_DIR}" && shasum -a 256 "${file#${BUNDLE_DIR}/}") >> "${CHECKSUM_FILE}"
  done < <(find "${BUNDLE_DIR}" -type f ! -name "SHA256SUMS.txt" | LC_ALL=C sort)
fi

echo "[bundle] creating tarball ${BUNDLE_TARBALL}"
rm -f "${BUNDLE_TARBALL}"
tar -C "${ROOT_DIR}/deploy/artifacts" -czf "${BUNDLE_TARBALL}" "accessd-${VERSION}"

echo
echo "Bundle ready:"
echo "  - directory: ${BUNDLE_DIR}"
echo "  - tarball:   ${BUNDLE_TARBALL}"
echo
echo "Copy to VM example:"
echo "  scp -r '${BUNDLE_DIR}' user@vm:/tmp/"
echo "  # or"
echo "  scp '${BUNDLE_TARBALL}' user@vm:/tmp/"
