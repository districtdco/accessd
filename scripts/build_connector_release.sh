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

COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SIGNING_KEY_ID="${CONNECTOR_SIGNING_KEY_ID:-${GPG_SIGNING_KEY_ID:-}}"

mkdir -p "${OUT_DIR}"
rm -f "${OUT_DIR}"/*

artifacts=()
warnings=()
missing_packages=()
ALLOW_MISSING_PACKAGES="${CONNECTOR_RELEASE_ALLOW_MISSING_PACKAGES:-false}"

has_cmd() {
  command -v "$1" >/dev/null 2>&1
}

sha256_of_file() {
  local file="$1"
  if has_cmd sha256sum; then
    sha256sum "${file}" | awk '{print $1}'
  else
    shasum -a 256 "${file}" | awk '{print $1}'
  fi
}

maybe_sign_file() {
  local file="$1"
  if [[ -z "${SIGNING_KEY_ID}" ]]; then
    return 0
  fi
  if ! has_cmd gpg; then
    warnings+=("gpg not found; skipped signatures for ${file}")
    return 0
  fi
  gpg --batch --yes --armor --local-user "${SIGNING_KEY_ID}" --detach-sign --output "${file}.sig" "${file}"
}

write_payload_checksums() {
  local work_dir="$1"
  shift
  local out_file="${work_dir}/release-files-sha256.txt"
  rm -f "${out_file}"
  local f hash
  for f in "$@"; do
    hash="$(sha256_of_file "${work_dir}/${f}")"
    printf '%s  %s\n' "${hash}" "${f}" >> "${out_file}"
  done
}

copy_installers() {
  local goos="$1"
  local work_dir="$2"
  if [[ "${goos}" == "windows" ]]; then
    cp "${ROOT_DIR}/scripts/connector-installers/install-windows.ps1" "${work_dir}/install.ps1"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-windows.ps1" "${work_dir}/uninstall.ps1"
  elif [[ "${goos}" == "darwin" ]]; then
    cp "${ROOT_DIR}/scripts/connector-installers/install-macos.sh" "${work_dir}/install.sh"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-macos.sh" "${work_dir}/uninstall.sh"
    chmod 0755 "${work_dir}/install.sh" "${work_dir}/uninstall.sh"
  else
    cp "${ROOT_DIR}/scripts/connector-installers/install-linux.sh" "${work_dir}/install.sh"
    cp "${ROOT_DIR}/scripts/connector-installers/uninstall-linux.sh" "${work_dir}/uninstall.sh"
    chmod 0755 "${work_dir}/install.sh" "${work_dir}/uninstall.sh"
  fi
}

build_binary_and_payload() {
  local goos="$1"
  local goarch="$2"
  local work_dir="$3"

  local exe_name="accessd-connector"
  local bin_name="${exe_name}"
  if [[ "${goos}" == "windows" ]]; then
    bin_name="${exe_name}.exe"
  fi

  mkdir -p "${work_dir}"
  (
    cd "${ROOT_DIR}/apps/connector"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath \
      -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILT_AT}" \
      -o "${work_dir}/${bin_name}" ./cmd/connector
  )

  copy_installers "${goos}" "${work_dir}"

  if [[ "${goos}" == "windows" ]]; then
    cat > "${work_dir}/bootstrap-runner.go" <<'GO'
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func logLine(w io.Writer, msg string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, msg+"\n")
}

func firstNonEmptyLine(s string) string {
	var fallback string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			if strings.HasSuffix(strings.ToLower(line), ".crt") {
				if _, err := os.Stat(line); err == nil {
					return line
				}
				if fallback == "" {
					fallback = line
				}
				continue
			}
			if fallback == "" {
				fallback = line
			}
		}
	}
	return fallback
}

func expandValue(raw, userProfile, localAppData string) string {
	v := strings.TrimSpace(raw)
	v = strings.Trim(v, "\"'")
	if userProfile != "" {
		repls := []string{"$HOME", "${HOME}", "%USERPROFILE%"}
		for _, p := range repls {
			if strings.EqualFold(v, p) {
				return userProfile
			}
			if strings.HasPrefix(strings.ToUpper(v), strings.ToUpper(p+"\\")) || strings.HasPrefix(strings.ToUpper(v), strings.ToUpper(p+"/")) {
				return userProfile + v[len(p):]
			}
		}
	}
	if localAppData != "" {
		p := "%LOCALAPPDATA%"
		if strings.EqualFold(v, p) {
			return localAppData
		}
		if strings.HasPrefix(strings.ToUpper(v), strings.ToUpper(p+"\\")) || strings.HasPrefix(strings.ToUpper(v), strings.ToUpper(p+"/")) {
			return localAppData + v[len(p):]
		}
	}
	return v
}

func ensureRuntimeEnv(runtimeEnvPath string, log io.Writer) {
	if _, err := os.Stat(runtimeEnvPath); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(runtimeEnvPath), 0o755)
	lines := []string{
		"# AccessD Connector runtime env (non-sensitive defaults)",
		"# Keep secrets out of this file.",
		"ACCESSD_CONNECTOR_ADDR=127.0.0.1:9494",
		"ACCESSD_CONNECTOR_ENABLE_TLS=true",
		"ACCESSD_CONNECTOR_TLS_CERT_FILE=%USERPROFILE%/.accessd-connector/tls/localhost.crt",
		"ACCESSD_CONNECTOR_TLS_KEY_FILE=%USERPROFILE%/.accessd-connector/tls/localhost.key",
		"ACCESSD_CONNECTOR_ALLOWED_ORIGIN=https://accessd.example.internal",
		"ACCESSD_CONNECTOR_ALLOW_ANY_ORIGIN=false",
		"ACCESSD_CONNECTOR_ALLOW_REMOTE=false",
		"ACCESSD_CONNECTOR_AUTO_TRUST_SERVER_CERT=true",
	}
	_ = os.WriteFile(runtimeEnvPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	logLine(log, "[accessd-connector] Wrote runtime env: "+runtimeEnvPath)
}

func loadRuntimeEnv(runtimeEnvPath string, log io.Writer) {
	f, err := os.Open(runtimeEnvPath)
	if err != nil {
		return
	}
	defer f.Close()
	userProfile := os.Getenv("USERPROFILE")
	localAppData := os.Getenv("LOCALAPPDATA")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := expandValue(parts[1], userProfile, localAppData)
		_ = os.Setenv(key, val)
	}
	if err := sc.Err(); err != nil {
		logLine(log, "env load warning="+err.Error())
	}
}

func verifyPayload(dir string, log io.Writer) bool {
	checksumsPath := filepath.Join(dir, "release-files-sha256.txt")
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		logLine(log, "[accessd-connector] WARNING: checksum file missing; skipping verification")
		return true
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		expected := strings.ToLower(strings.TrimSpace(parts[0]))
		name := strings.TrimSpace(parts[1])
		target := filepath.Join(dir, name)
		b, err := os.ReadFile(target)
		if err != nil {
			logLine(log, "payload verification failed: missing file "+name)
			return false
		}
		sum := sha256.Sum256(b)
		actual := hex.EncodeToString(sum[:])
		if actual != expected {
			logLine(log, "payload verification failed: checksum mismatch "+name)
			return false
		}
	}
	if err := sc.Err(); err != nil {
		logLine(log, "payload verification read error="+err.Error())
		return false
	}
	logLine(log, "[accessd-connector] Payload integrity check passed.")
	return true
}

func connectorResponsive(addr string) bool {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:9494"
	}
	httpsClient := &http.Client{
		Timeout: 1200 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // local loopback probe only
		},
	}
	if resp, err := httpsClient.Get("https://" + addr + "/version"); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 500 {
			return true
		}
	}
	httpClient := &http.Client{Timeout: 1200 * time.Millisecond}
	if resp, err := httpClient.Get("http://" + addr + "/version"); err == nil {
		_ = resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 500
	}
	return false
}

func runCertTrust(certPath string, log io.Writer) {
	if strings.TrimSpace(certPath) == "" {
		logLine(log, "[accessd-connector] WARNING: cert trust skipped: empty cert path from ensure-local-tls")
		return
	}
	if _, err := os.Stat(certPath); err != nil {
		logLine(log, "[accessd-connector] WARNING: cert trust skipped: cert file not found "+certPath)
		return
	}
	// Import-Certificate to CurrentUser\Root triggers a Windows security dialog
	// where the user must click Yes. certutil is a fallback that also triggers it.
	cmd := exec.Command("certutil.exe", "-user", "-f", "-addstore", "Root", certPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		logLine(log, "[accessd-connector] WARNING: cert trust failed: "+err.Error())
		if out.Len() > 0 {
			logLine(log, strings.TrimSpace(out.String()))
		}
		return
	}
	logLine(log, "[accessd-connector] Trusted local TLS cert in Windows certificate store: "+certPath)
	logLine(log, "[accessd-connector] IMPORTANT: Restart Chrome for the cert trust to take effect.")
}

func writeURLHandlerScript(connectorPath string, log io.Writer) string {
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userProfile = home
		}
	}
	helperDir := filepath.Join(userProfile, ".accessd-connector", "bin")
	_ = os.MkdirAll(helperDir, 0o755)
	handlerPath := filepath.Join(helperDir, "url-handler-windows.ps1")

	envFile := filepath.Join(userProfile, ".config", "accessd", "connector.env")
	logOut := filepath.Join(os.TempDir(), "accessd-connector.out")
	logErr := filepath.Join(os.TempDir(), "accessd-connector.err")

	script := `$ErrorActionPreference = 'SilentlyContinue'
$envFile = '` + envFile + `'
if (Test-Path $envFile) {
  Get-Content $envFile | ForEach-Object {
    $line = $_.Trim()
    if ([string]::IsNullOrWhiteSpace($line) -or $line.StartsWith('#')) { return }
    if ($line -notmatch '^[A-Za-z_][A-Za-z0-9_]*=') { return }
    $parts = $line.Split('=', 2)
    if ($parts.Count -ne 2) { return }
    [System.Environment]::SetEnvironmentVariable($parts[0].Trim(), $parts[1].Trim(), 'Process')
  }
}
$connectorAddr = if ([string]::IsNullOrWhiteSpace($env:ACCESSD_CONNECTOR_ADDR)) { '127.0.0.1:9494' } else { $env:ACCESSD_CONNECTOR_ADDR }
try {
  [System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
  try { Invoke-WebRequest -UseBasicParsing -Uri ("https://{0}/version" -f $connectorAddr) -TimeoutSec 1 | Out-Null; exit 0 } catch {}
  Invoke-WebRequest -UseBasicParsing -Uri ("http://{0}/version" -f $connectorAddr) -TimeoutSec 1 | Out-Null
  exit 0
} catch {}
$logOut = '` + logOut + `'
$logErr = '` + logErr + `'
Start-Process -FilePath '` + connectorPath + `' -NoNewWindow -RedirectStandardOutput $logOut -RedirectStandardError $logErr
`
	if err := os.WriteFile(handlerPath, []byte(script), 0o644); err != nil {
		logLine(log, "[accessd-connector] WARNING: failed to write URL handler script: "+err.Error())
		return ""
	}
	return handlerPath
}

func registerProtocol(connectorPath string, log io.Writer) {
	root := `HKCU\Software\Classes\accessd-connector`
	_ = exec.Command("reg.exe", "add", root, "/ve", "/d", "URL:AccessD Connector Protocol", "/f").Run()
	_ = exec.Command("reg.exe", "add", root, "/v", "URL Protocol", "/d", "", "/f").Run()
	_ = exec.Command("reg.exe", "add", root+`\DefaultIcon`, "/ve", "/d", `"`+connectorPath+`",0`, "/f").Run()

	// Register a PowerShell handler that loads the env file before starting the connector.
	// This ensures ACCESSD_* config vars are available even when Chrome invokes the URL
	// handler with a minimal environment (no ACCESSD_* vars set).
	handlerPath := writeURLHandlerScript(connectorPath, log)
	var commandValue string
	if handlerPath != "" {
		commandValue = `powershell.exe -NoLogo -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "` + handlerPath + `" "%1"`
	} else {
		// Fallback: run connector directly (will load env file via envWithConnectorEnvFile in spawnBackgroundServe)
		commandValue = `"` + connectorPath + `" "%1"`
	}
	_ = exec.Command("reg.exe", "add", root+`\shell\open\command`, "/ve", "/d", commandValue, "/f").Run()
	logLine(log, "[accessd-connector] Registered URL scheme: accessd-connector://")
}

func startConnector(connectorPath string, log io.Writer) {
	addr := os.Getenv("ACCESSD_CONNECTOR_ADDR")
	if connectorResponsive(addr) {
		logLine(log, "[accessd-connector] Connector already running; skipping auto-start.")
		return
	}
	cmd := exec.Command(connectorPath)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		logLine(log, "[accessd-connector] WARNING: failed to start connector: "+err.Error())
		return
	}
	logLine(log, "[accessd-connector] Started connector process (msi bootstrap).")
}

func downloadAndTrustServerCert(log io.Writer) {
	origin := strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_ALLOWED_ORIGIN"))
	if origin == "" || strings.Contains(origin, "accessd.example.internal") {
		return
	}
	host := strings.TrimPrefix(origin, "https://")
	host = strings.TrimPrefix(host, "http://")
	if idx := strings.IndexAny(host, "/:"); idx >= 0 {
		host = host[:idx]
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" {
		return
	}

	certURL := strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_TRUST_CERT_URL"))
	if certURL == "" {
		certURL = "https://" + host + "/downloads/certs/accessd-server.crt"
	}

	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userProfile = home
		}
	}
	certDir := filepath.Join(userProfile, ".accessd-connector", "certs")
	_ = os.MkdirAll(certDir, 0o755)
	certFile := filepath.Join(certDir, "accessd-"+host+".crt")

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // downloading cert to trust it
		},
	}
	resp, err := client.Get(certURL)
	if err != nil {
		logLine(log, "[accessd-connector] WARNING: server cert download failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logLine(log, "[accessd-connector] WARNING: server cert download status: "+resp.Status)
		return
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		logLine(log, "[accessd-connector] WARNING: server cert read failed: "+err.Error())
		return
	}
	if err := os.WriteFile(certFile, buf.Bytes(), 0o644); err != nil {
		logLine(log, "[accessd-connector] WARNING: server cert write failed: "+err.Error())
		return
	}
	logLine(log, "[accessd-connector] Downloaded server cert: "+certFile)

	cmd := exec.Command("certutil.exe", "-user", "-f", "-addstore", "Root", certFile)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		logLine(log, "[accessd-connector] WARNING: server cert trust failed: "+err.Error())
		if out.Len() > 0 {
			logLine(log, strings.TrimSpace(out.String()))
		}
		return
	}
	logLine(log, "[accessd-connector] Trusted server TLS cert in Windows certificate store: "+certFile)
	logLine(log, "[accessd-connector] IMPORTANT: Restart Chrome for the cert trust to take effect.")
}

func main() {
	exePath, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	baseDir := filepath.Dir(exePath)
	connectorPath := filepath.Join(baseDir, "accessd-connector.exe")
	logDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "AccessD")
	if logDir == "" || logDir == "." {
		logDir = os.TempDir()
	}
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "install-bootstrap.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logFile != nil {
		defer logFile.Close()
		_, _ = io.WriteString(logFile, "---- bootstrap-runner start "+time.Now().Format(time.RFC3339)+" ----\n")
		_, _ = io.WriteString(logFile, "connector="+connectorPath+"\n")
	}

	if _, err := os.Stat(connectorPath); err != nil {
		logLine(logFile, "error=connector binary missing")
		os.Exit(1)
	}
	if !verifyPayload(baseDir, logFile) {
		logLine(logFile, "error=payload verification failed")
		os.Exit(1)
	}

	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userProfile = home
		}
	}
	runtimeEnvPath := filepath.Join(userProfile, ".config", "accessd", "connector.env")
	ensureRuntimeEnv(runtimeEnvPath, logFile)
	loadRuntimeEnv(runtimeEnvPath, logFile)
	registerProtocol(connectorPath, logFile)

	ensureCmd := exec.Command(connectorPath, "ensure-local-tls")
	var ensureOut bytes.Buffer
	ensureCmd.Stdout = &ensureOut
	ensureCmd.Stderr = &ensureOut
	if err := ensureCmd.Run(); err != nil {
		logLine(logFile, "[accessd-connector] WARNING: ensure-local-tls failed: "+err.Error())
	}
	if txt := strings.TrimSpace(ensureOut.String()); txt != "" {
		logLine(logFile, "[accessd-connector] ensure-local-tls output: "+txt)
	}
	certPath := firstNonEmptyLine(ensureOut.String())
	runCertTrust(certPath, logFile)
	downloadAndTrustServerCert(logFile)

	startConnector(connectorPath, logFile)

	if logFile == nil {
		_, _ = os.Stdout.WriteString("status=ok\n")
	} else {
		_, _ = io.WriteString(logFile, "status=ok\n")
	}
}
GO
    # Bake the allowed origin into bootstrap-runner.go so the MSI env file has the correct
    # origin at install time — no manual env setup required on the user's machine.
    _baked_origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-${CONNECTOR_ALLOWED_ORIGIN:-}}"
    if [[ -n "${_baked_origin}" ]]; then
      sed -i.bak "s|https://accessd.example.internal|${_baked_origin}|g" "${work_dir}/bootstrap-runner.go"
      rm -f "${work_dir}/bootstrap-runner.go.bak"
    fi
    (
      cd "${ROOT_DIR}/apps/connector"
      CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
        go build -trimpath -ldflags "-s -w" \
        -o "${work_dir}/bootstrap-runner.exe" "${work_dir}/bootstrap-runner.go"
    )
    rm -f "${work_dir}/bootstrap-runner.go"
    write_payload_checksums "${work_dir}" "${bin_name}" "install.ps1" "uninstall.ps1" "bootstrap-runner.exe"
  else
    write_payload_checksums "${work_dir}" "${bin_name}" "install.sh" "uninstall.sh"
  fi

  printf '%s' "${bin_name}"
}

build_archive_artifact() {
  local goos="$1"
  local goarch="$2"
  local work_dir="$3"
  local bin_name="$4"

  local ext="tar.gz"
  if [[ "${goos}" == "windows" ]]; then
    ext="zip"
  fi

  local archive_name="accessd-connector-${VERSION}-${goos}-${goarch}.${ext}"
  local archive_path="${OUT_DIR}/${archive_name}"

  if [[ "${ext}" == "zip" ]]; then
    (
      cd "${work_dir}"
      zip -q "${archive_path}" "${bin_name}" "install.ps1" "uninstall.ps1" "bootstrap-runner.exe" "release-files-sha256.txt"
    )
  else
    (
      cd "${work_dir}"
      tar -czf "${archive_path}" "${bin_name}" "install.sh" "uninstall.sh" "release-files-sha256.txt"
    )
  fi

  artifacts+=("${archive_path}")
}

build_macos_pkg() {
  local goarch="$1"
  local work_dir="$2"
  local bin_name="$3"
  local pkg_allowed_origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-${CONNECTOR_ALLOWED_ORIGIN:-}}"
  local pkg_bootstrap_env_url="${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:-${CONNECTOR_BOOTSTRAP_ENV_URL:-}}"
  if ! has_cmd pkgbuild; then
    warnings+=("pkgbuild not found; skipped macOS pkg for ${goarch}")
    return 0
  fi

  local pkg_root="${TMP_DIR}/pkgroot-${goarch}"
  local pkg_scripts="${TMP_DIR}/pkgscripts-${goarch}"
  rm -rf "${pkg_root}"
  rm -rf "${pkg_scripts}"
  mkdir -p "${pkg_root}/usr/local/lib/accessd-connector"
  mkdir -p "${pkg_scripts}"
  cp "${work_dir}/${bin_name}" "${pkg_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/install.sh" "${pkg_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/uninstall.sh" "${pkg_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/release-files-sha256.txt" "${pkg_root}/usr/local/lib/accessd-connector/"
  cat > "${pkg_scripts}/postinstall" <<POSTINSTALL
#!/bin/bash
set -euo pipefail

pkg_allowed_origin="${pkg_allowed_origin}"
pkg_bootstrap_env_url="${pkg_bootstrap_env_url}"

target_root="\${3:-/}"
bootstrap="\${target_root%/}/usr/local/lib/accessd-connector/install.sh"
if [[ ! -f "\${bootstrap}" ]]; then
  exit 0
fi
chmod +x "\${bootstrap}" || true

console_user="\$(stat -f %Su /dev/console 2>/dev/null || true)"
if [[ -z "\${console_user}" || "\${console_user}" == "root" || "\${console_user}" == "loginwindow" ]]; then
  exit 0
fi

user_home="\$(dscl . -read "/Users/\${console_user}" NFSHomeDirectory 2>/dev/null | awk '{print \$2}' || true)"
if [[ -z "\${user_home}" ]]; then
  user_home="/Users/\${console_user}"
fi

uid="\$(id -u "\${console_user}" 2>/dev/null || true)"
if [[ -z "\${uid}" ]]; then
  exit 0
fi

run_cmd=(/usr/bin/sudo -u "\${console_user}" env HOME="\${user_home}")
if [[ -n "\${pkg_allowed_origin}" ]]; then
  run_cmd+=("ACCESSD_CONNECTOR_ALLOWED_ORIGIN=\${pkg_allowed_origin}")
fi
if [[ -n "\${pkg_bootstrap_env_url}" ]]; then
  run_cmd+=("ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL=\${pkg_bootstrap_env_url}")
fi
run_cmd+=("\${bootstrap}")
if command -v launchctl >/dev/null 2>&1; then
  launchctl asuser "\${uid}" "\${run_cmd[@]}" || true
else
  "\${run_cmd[@]}" || true
fi

# Trust certs in System keychain as root (PKG postinstall runs as root — no sudo needed).
# Chrome requires certs in System keychain; login keychain is not accessible from
# Chrome's sandboxed Network Service process on macOS Ventura/Sonoma.
connector_bin="\${target_root%/}/usr/local/lib/accessd-connector/accessd-connector"
if [[ -x "\${connector_bin}" ]] && command -v security >/dev/null 2>&1; then
  # Local connector TLS cert (127.0.0.1:9494)
  local_cert="\$(sudo -u "\${console_user}" env HOME="\${user_home}" "\${connector_bin}" ensure-local-tls 2>/dev/null | head -1 || true)"
  local_cert="\${local_cert%$'\r'}"
  if [[ -n "\${local_cert}" && -f "\${local_cert}" ]]; then
    security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "\${local_cert}" 2>/dev/null && \
      echo "[accessd-connector] Trusted local connector TLS cert in System keychain." || \
      echo "[accessd-connector] WARNING: Failed to trust local connector TLS cert in System keychain."
  fi

  # Server TLS cert for the AccessD UI origin
  env_file="\${user_home}/.config/accessd/connector.env"
  if [[ -f "\${env_file}" ]]; then
    allowed_origin="\$(grep -E '^ACCESSD_CONNECTOR_ALLOWED_ORIGIN=' "\${env_file}" | tail -1 | cut -d= -f2- | tr -d '[:space:]' || true)"
    if [[ -n "\${allowed_origin}" && "\${allowed_origin}" != *"accessd.example.internal"* ]]; then
      host="\${allowed_origin#http://}"
      host="\${host#https://}"
      host="\${host%%/*}"
      host="\${host%%:*}"
      server_cert="\${user_home}/.accessd-connector/certs/accessd-\${host}.crt"
      if [[ -f "\${server_cert}" ]]; then
        security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "\${server_cert}" 2>/dev/null && \
          echo "[accessd-connector] Trusted server TLS cert in System keychain." || \
          echo "[accessd-connector] WARNING: Failed to trust server TLS cert in System keychain."
      fi
    fi
  fi
fi
POSTINSTALL
  chmod 0755 "${pkg_scripts}/postinstall"

  local pkg_name="accessd-connector-${VERSION}-darwin-${goarch}.pkg"
  local pkg_path="${OUT_DIR}/${pkg_name}"
  pkgbuild \
    --identifier "io.accessd.connector.${goarch}" \
    --version "${VERSION}" \
    --root "${pkg_root}" \
    --scripts "${pkg_scripts}" \
    --install-location "/" \
    "${pkg_path}" >/dev/null

  artifacts+=("${pkg_path}")
}

build_linux_deb() {
  local goarch="$1"
  local work_dir="$2"
  local bin_name="$3"
  if ! has_cmd dpkg-deb; then
    warnings+=("dpkg-deb not found; skipped deb for ${goarch}")
    missing_packages+=("linux/${goarch}: deb (requires dpkg-deb)")
    return 0
  fi

  local deb_arch="${goarch}"
  if [[ "${goarch}" == "amd64" ]]; then
    deb_arch="amd64"
  elif [[ "${goarch}" == "arm64" ]]; then
    deb_arch="arm64"
  fi

  local deb_root="${TMP_DIR}/debroot-${goarch}"
  local deb_allowed_origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-${CONNECTOR_ALLOWED_ORIGIN:-}}"
  rm -rf "${deb_root}"
  mkdir -p "${deb_root}/DEBIAN" "${deb_root}/usr/local/lib/accessd-connector"

  cat > "${deb_root}/DEBIAN/control" <<CTRL
Package: accessd-connector
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${deb_arch}
Maintainer: AccessD Team <ops@accessd.local>
Description: AccessD operator connector and installer assets
CTRL
  cat > "${deb_root}/DEBIAN/postinst" <<'POSTINST'
#!/bin/bash
set -euo pipefail

bootstrap="/usr/local/lib/accessd-connector/install.sh"
if [[ ! -x "${bootstrap}" ]]; then
  exit 0
fi

# Source installer defaults (origin baked in at build time)
defaults_file="/usr/local/lib/accessd-connector/installer-defaults.env"
if [[ -f "${defaults_file}" ]]; then
  # shellcheck disable=SC1090
  . "${defaults_file}"
  export ACCESSD_CONNECTOR_ALLOWED_ORIGIN ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL 2>/dev/null || true
fi

run_as="${SUDO_USER:-}"
if [[ -z "${run_as}" && -t 0 ]]; then
  run_as="$(logname 2>/dev/null || true)"
fi

if [[ -n "${run_as}" && "${run_as}" != "root" ]]; then
  user_home="$(getent passwd "${run_as}" | cut -d: -f6 || true)"
  if [[ -z "${user_home}" ]]; then
    user_home="/home/${run_as}"
  fi
  /usr/bin/sudo -u "${run_as}" env HOME="${user_home}" \
    ${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:+ACCESSD_CONNECTOR_ALLOWED_ORIGIN="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN}"} \
    ${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:+ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL="${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL}"} \
    "${bootstrap}" || true
  exit 0
fi

"${bootstrap}" || true
POSTINST
  chmod 0755 "${deb_root}/DEBIAN/postinst"

  cp "${work_dir}/${bin_name}" "${deb_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/install.sh" "${deb_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/uninstall.sh" "${deb_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/release-files-sha256.txt" "${deb_root}/usr/local/lib/accessd-connector/"
  # Write baked-in defaults (origin, bootstrap env URL) for the postinst to source
  {
    [[ -n "${deb_allowed_origin}" ]] && printf 'ACCESSD_CONNECTOR_ALLOWED_ORIGIN=%s\n' "${deb_allowed_origin}" || true
    local bev="${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:-${CONNECTOR_BOOTSTRAP_ENV_URL:-}}"
    [[ -n "${bev}" ]] && printf 'ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL=%s\n' "${bev}" || true
  } > "${deb_root}/usr/local/lib/accessd-connector/installer-defaults.env"

  local deb_name="accessd-connector-${VERSION}-linux-${goarch}.deb"
  local deb_path="${OUT_DIR}/${deb_name}"
  if dpkg-deb --help 2>/dev/null | grep -q -- "--root-owner-group"; then
    dpkg-deb --root-owner-group --build "${deb_root}" "${deb_path}" >/dev/null
  else
    dpkg-deb --build "${deb_root}" "${deb_path}" >/dev/null
  fi

  artifacts+=("${deb_path}")
}

build_linux_rpm() {
  local goarch="$1"
  local work_dir="$2"
  local bin_name="$3"
  if ! has_cmd rpmbuild; then
    warnings+=("rpmbuild not found; skipped rpm for ${goarch}")
    return 0
  fi

  local rpm_arch="x86_64"
  if [[ "${goarch}" == "arm64" ]]; then
    rpm_arch="aarch64"
  fi

  local rpm_allowed_origin="${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-${CONNECTOR_ALLOWED_ORIGIN:-}}"
  local rpm_bev="${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:-${CONNECTOR_BOOTSTRAP_ENV_URL:-}}"
  local topdir="${TMP_DIR}/rpmbuild-${goarch}"
  local src_name="accessd-connector-${VERSION}-${goarch}"
  local src_root="${TMP_DIR}/${src_name}"
  rm -rf "${topdir}" "${src_root}"
  mkdir -p "${topdir}/BUILD" "${topdir}/RPMS" "${topdir}/SOURCES" "${topdir}/SPECS" "${topdir}/SRPMS"
  mkdir -p "${src_root}/usr/local/lib/accessd-connector"

  cp "${work_dir}/${bin_name}" "${src_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/install.sh" "${src_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/uninstall.sh" "${src_root}/usr/local/lib/accessd-connector/"
  cp "${work_dir}/release-files-sha256.txt" "${src_root}/usr/local/lib/accessd-connector/"
  # Write baked-in defaults for the %post scriptlet to source
  {
    [[ -n "${rpm_allowed_origin}" ]] && printf 'ACCESSD_CONNECTOR_ALLOWED_ORIGIN=%s\n' "${rpm_allowed_origin}" || true
    [[ -n "${rpm_bev}" ]] && printf 'ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL=%s\n' "${rpm_bev}" || true
  } > "${src_root}/usr/local/lib/accessd-connector/installer-defaults.env"

  tar -C "${TMP_DIR}" -czf "${topdir}/SOURCES/${src_name}.tar.gz" "${src_name}"

  cat > "${topdir}/SPECS/accessd-connector.spec" <<SPEC
Name: accessd-connector
Version: ${VERSION}
Release: 1
Summary: AccessD operator connector and installer assets
License: Proprietary
Source0: ${src_name}.tar.gz
BuildArch: ${rpm_arch}

%description
AccessD operator connector and installer assets.

%prep
%setup -q -n ${src_name}

%build

%install
mkdir -p %{buildroot}/usr/local/lib/accessd-connector
cp -a usr/local/lib/accessd-connector/. %{buildroot}/usr/local/lib/accessd-connector/

%post
bootstrap=/usr/local/lib/accessd-connector/install.sh
if [ ! -x "\$bootstrap" ]; then
  exit 0
fi
# Source installer defaults (origin baked in at build time)
defaults_file=/usr/local/lib/accessd-connector/installer-defaults.env
if [ -f "\$defaults_file" ]; then
  . "\$defaults_file"
  export ACCESSD_CONNECTOR_ALLOWED_ORIGIN ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL 2>/dev/null || true
fi
if [ -n "\${SUDO_USER:-}" ] && [ "\${SUDO_USER}" != "root" ]; then
  user_home=\$(getent passwd "\$SUDO_USER" | cut -d: -f6)
  if [ -z "\$user_home" ]; then
    user_home="/home/\$SUDO_USER"
  fi
  env_args=""
  [ -n "\${ACCESSD_CONNECTOR_ALLOWED_ORIGIN:-}" ] && env_args="\$env_args ACCESSD_CONNECTOR_ALLOWED_ORIGIN=\${ACCESSD_CONNECTOR_ALLOWED_ORIGIN}"
  [ -n "\${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL:-}" ] && env_args="\$env_args ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL=\${ACCESSD_CONNECTOR_BOOTSTRAP_ENV_URL}"
  /usr/bin/sudo -u "\$SUDO_USER" env HOME="\$user_home" \$env_args "\$bootstrap" || true
else
  "\$bootstrap" || true
fi

%files
/usr/local/lib/accessd-connector/*
SPEC

  rpmbuild --quiet --define "_topdir ${topdir}" -bb "${topdir}/SPECS/accessd-connector.spec"
  local built_rpm
  built_rpm="$(find "${topdir}/RPMS" -type f -name '*.rpm' | head -n 1)"
  if [[ -z "${built_rpm}" ]]; then
    warnings+=("rpmbuild ran but no rpm artifact found for ${goarch}")
    return 0
  fi

  local rpm_name="accessd-connector-${VERSION}-linux-${goarch}.rpm"
  local rpm_path="${OUT_DIR}/${rpm_name}"
  cp "${built_rpm}" "${rpm_path}"
  artifacts+=("${rpm_path}")
}

build_windows_msi() {
  local goarch="$1"
  local work_dir="$2"
  local bin_name="$3"
  if ! has_cmd wixl; then
    warnings+=("wixl not found; skipped msi for ${goarch}")
    missing_packages+=("windows/${goarch}: msi (requires wixl from msitools)")
    return 0
  fi

  local wxs_path="${TMP_DIR}/accessd-connector-${goarch}.wxs"
  local msi_name="accessd-connector-${VERSION}-windows-${goarch}.msi"
  local msi_path="${OUT_DIR}/${msi_name}"

  cat > "${wxs_path}" <<WXS
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*" Name="AccessD Connector" Language="1033" Version="${VERSION}" Manufacturer="AccessD" UpgradeCode="D7A5827A-89E9-4F9C-9AF2-8A1223E16541">
    <Package InstallerVersion="200" Compressed="yes" InstallScope="perUser" />
    <MajorUpgrade
      AllowSameVersionUpgrades="yes"
      DowngradeErrorMessage="A newer AccessD Connector version is already installed." />
    <MediaTemplate EmbedCab="yes" />
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="LocalAppDataFolder">
        <Directory Id="INSTALLDIR" Name="AccessD">
          <Directory Id="BIN" Name="bin" />
        </Directory>
      </Directory>
    </Directory>
    <DirectoryRef Id="BIN">
      <Component Id="ConnectorBin" Guid="*">
        <File Id="ConnectorBinFile" Source="${work_dir}/${bin_name}" KeyPath="yes" />
      </Component>
      <Component Id="InstallScript" Guid="*">
        <File Id="InstallScriptFile" Source="${work_dir}/install.ps1" KeyPath="yes" />
      </Component>
      <Component Id="UninstallScript" Guid="*">
        <File Id="UninstallScriptFile" Source="${work_dir}/uninstall.ps1" KeyPath="yes" />
      </Component>
      <Component Id="BootstrapRunner" Guid="*">
        <File Id="BootstrapRunnerFile" Source="${work_dir}/bootstrap-runner.exe" KeyPath="yes" />
      </Component>
      <Component Id="PayloadChecksums" Guid="*">
        <File Id="ChecksumsFile" Source="${work_dir}/release-files-sha256.txt" KeyPath="yes" />
      </Component>
    </DirectoryRef>
    <CustomAction
      Id="RunBootstrapScript"
      FileKey="BootstrapRunnerFile"
      ExeCommand=""
      Execute="deferred"
      Impersonate="yes"
      Return="ignore" />
    <InstallExecuteSequence>
      <Custom Action="RunBootstrapScript" After="InstallFiles">NOT REMOVE</Custom>
    </InstallExecuteSequence>
    <Feature Id="MainFeature" Title="AccessD Connector" Level="1">
      <ComponentRef Id="ConnectorBin" />
      <ComponentRef Id="InstallScript" />
      <ComponentRef Id="UninstallScript" />
      <ComponentRef Id="BootstrapRunner" />
      <ComponentRef Id="PayloadChecksums" />
    </Feature>
  </Product>
</Wix>
WXS

  if ! wixl -o "${msi_path}" "${wxs_path}"; then
    warnings+=("wixl failed; skipped msi for ${goarch}")
    missing_packages+=("windows/${goarch}: msi (wixl build failed)")
    rm -f "${msi_path}" || true
    return 0
  fi

  artifacts+=("${msi_path}")
}

build_target() {
  local goos="$1"
  local goarch="$2"
  local work_dir="${TMP_DIR}/${goos}-${goarch}"
  local bin_name
  bin_name="$(build_binary_and_payload "${goos}" "${goarch}" "${work_dir}")"

  build_archive_artifact "${goos}" "${goarch}" "${work_dir}" "${bin_name}"

  case "${goos}" in
    darwin)
      build_macos_pkg "${goarch}" "${work_dir}" "${bin_name}"
      ;;
    linux)
      build_linux_deb "${goarch}" "${work_dir}" "${bin_name}"
      build_linux_rpm "${goarch}" "${work_dir}" "${bin_name}"
      ;;
    windows)
      build_windows_msi "${goarch}" "${work_dir}" "${bin_name}"
      ;;
  esac
}

build_target "darwin" "arm64"
build_target "darwin" "amd64"
build_target "linux" "amd64"
build_target "linux" "arm64"
build_target "windows" "amd64"

if [[ "${#artifacts[@]}" -eq 0 ]]; then
  echo "[connector-release] ERROR: no connector artifacts were produced"
  exit 1
fi

if [[ "${#missing_packages[@]}" -gt 0 && "${ALLOW_MISSING_PACKAGES}" != "true" ]]; then
  echo "[connector-release] ERROR: required package artifacts were skipped:"
  for missing in "${missing_packages[@]}"; do
    echo "  - ${missing}"
  done
  echo
  echo "Install required packaging tools (dpkg-deb + wixl) or run with:"
  echo "  CONNECTOR_RELEASE_ALLOW_MISSING_PACKAGES=true $0 ${VERSION}"
  exit 1
fi

CHECKSUM_FILE="${OUT_DIR}/accessd-connector-${VERSION}-checksums.txt"
rm -f "${CHECKSUM_FILE}"
for artifact in "${artifacts[@]}"; do
  hash="$(sha256_of_file "${artifact}")"
  printf '%s  %s\n' "${hash}" "$(basename "${artifact}")" >> "${CHECKSUM_FILE}"
done

maybe_sign_file "${CHECKSUM_FILE}"
for artifact in "${artifacts[@]}"; do
  maybe_sign_file "${artifact}"
done

if [[ "${#warnings[@]}" -gt 0 ]]; then
  {
    echo "Connector release warnings:"
    for w in "${warnings[@]}"; do
      echo "- ${w}"
    done
    if [[ "${#missing_packages[@]}" -gt 0 ]]; then
      echo
      echo "Missing package artifacts:"
      for missing in "${missing_packages[@]}"; do
        echo "- ${missing}"
      done
    fi
  } > "${OUT_DIR}/accessd-connector-${VERSION}-warnings.txt"
fi

echo "Connector release artifacts created at: ${OUT_DIR}"
echo "Checksums: ${CHECKSUM_FILE}"
if [[ -n "${SIGNING_KEY_ID}" ]]; then
  echo "Signatures: enabled (GPG key ${SIGNING_KEY_ID})"
else
  echo "Signatures: skipped (set CONNECTOR_SIGNING_KEY_ID to enable)"
fi
