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
OUT_DIR="${ROOT_DIR}/dist/connector/${TAG}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

mkdir -p "${OUT_DIR}"

build_one() {
  local goos="$1"
  local goarch="$2"
  local archive_ext="$3"
  local exe_name="accessd-connector"
  local bin_name="${exe_name}"
  if [[ "${goos}" == "windows" ]]; then
    bin_name="${exe_name}.exe"
  fi

  local work_dir="${TMP_DIR}/${goos}-${goarch}"
  mkdir -p "${work_dir}"

  (
    cd "${ROOT_DIR}/apps/connector"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath \
      -ldflags "-X main.version=${VERSION} -X main.commit=$(git -C "${ROOT_DIR}" rev-parse --short HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o "${work_dir}/${bin_name}" ./cmd/connector
  )

  if [[ "${goos}" == "windows" ]]; then
    cp "${ROOT_DIR}/scripts/connector-installers/install-windows.ps1" "${work_dir}/install.ps1"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-windows.ps1" "${work_dir}/uninstall.ps1"
  elif [[ "${goos}" == "darwin" ]]; then
    cp "${ROOT_DIR}/scripts/connector-installers/install-macos.sh" "${work_dir}/install.sh"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-macos.sh" "${work_dir}/uninstall.sh"
    chmod 0755 "${work_dir}/install.sh"
    chmod 0755 "${work_dir}/uninstall.sh"
  else
    cp "${ROOT_DIR}/scripts/connector-installers/install-linux.sh" "${work_dir}/install.sh"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-linux.sh" "${work_dir}/uninstall.sh"
    chmod 0755 "${work_dir}/install.sh"
    chmod 0755 "${work_dir}/uninstall.sh"
  fi

  local archive_name="accessd-connector-${VERSION}-${goos}-${goarch}.${archive_ext}"
  local archive_path="${OUT_DIR}/${archive_name}"

  if [[ "${archive_ext}" == "zip" ]]; then
    (
      cd "${work_dir}"
      zip -q "${archive_path}" "${bin_name}" "install.ps1" "uninstall.ps1"
    )
  else
    (
      cd "${work_dir}"
      tar -czf "${archive_path}" "${bin_name}" "install.sh" "uninstall.sh"
    )
  fi
}

build_one "darwin" "arm64" "tar.gz"
build_one "darwin" "amd64" "tar.gz"
build_one "linux" "amd64" "tar.gz"
build_one "linux" "arm64" "tar.gz"
build_one "windows" "amd64" "zip"

CHECKSUM_FILE="${OUT_DIR}/accessd-connector-${VERSION}-checksums.txt"
rm -f "${CHECKSUM_FILE}"
for f in "${OUT_DIR}"/accessd-connector-"${VERSION}"-*; do
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${f}" >> "${CHECKSUM_FILE}"
  else
    shasum -a 256 "${f}" >> "${CHECKSUM_FILE}"
  fi
done

echo "Connector release artifacts created at: ${OUT_DIR}"
echo "Checksums: ${CHECKSUM_FILE}"
