$ErrorActionPreference = 'Stop'

$installDir = if ($env:ACCESSD_CONNECTOR_INSTALL_DIR) { $env:ACCESSD_CONNECTOR_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'AccessD\\bin' }
$legacyInstallDirs = @(
  (Join-Path $env:LOCALAPPDATA 'AccessD Connector\\bin'),
  (Join-Path $env:LOCALAPPDATA 'Programs\\AccessD Connector')
)
$configDir = Join-Path $env:USERPROFILE '.accessd-connector'
$targetBin = Join-Path $installDir 'accessd-connector.exe'
$handlerScript = Join-Path $configDir 'bin\url-handler-windows.ps1'
$protocolRoot = 'HKCU:\Software\Classes\accessd-connector'
$removeConfig = $env:ACCESSD_CONNECTOR_REMOVE_CONFIG -eq '1'

try {
  $running = Get-Process -Name 'accessd-connector' -ErrorAction SilentlyContinue
  if ($running) {
    $running | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 600
  }
} catch {
}

Remove-Item -Force -ErrorAction SilentlyContinue $targetBin
Remove-Item -Force -ErrorAction SilentlyContinue $handlerScript

foreach ($legacyDir in $legacyInstallDirs) {
  if ([string]::IsNullOrWhiteSpace($legacyDir)) { continue }
  try {
    if (Test-Path $legacyDir) {
      Remove-Item -Path $legacyDir -Recurse -Force -ErrorAction SilentlyContinue
      Write-Host "[accessd-connector] Removed legacy install directory: $legacyDir"
    }
  } catch {
    Write-Host "[accessd-connector] WARNING: failed to remove legacy install directory ${legacyDir}: $($_.Exception.Message)"
  }
}

if (Test-Path $protocolRoot) {
  Remove-Item -Path $protocolRoot -Recurse -Force -ErrorAction SilentlyContinue
}

if ($removeConfig) {
  Remove-Item -Path $configDir -Recurse -Force -ErrorAction SilentlyContinue
  Write-Host "[accessd-connector] Removed connector config directory: $configDir"
} else {
  Write-Host "[accessd-connector] Preserved connector config directory: $configDir"
  Write-Host '[accessd-connector] Set ACCESSD_CONNECTOR_REMOVE_CONFIG=1 to remove it.'
}

Write-Host '[accessd-connector] Uninstall complete.'
