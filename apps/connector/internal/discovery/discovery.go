// Package discovery provides cross-platform application discovery for AccessD
// connector launchers. It resolves application paths using a strict priority
// order: environment variable → config file → OS-specific auto-detection →
// actionable error. It never silently succeeds if a binary is not valid.
package discovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// AppName identifies a launchable application.
type AppName string

const (
	AppDBeaver   AppName = "dbeaver"
	AppFileZilla AppName = "filezilla"
	AppWinSCP    AppName = "winscp"
	AppPuTTY     AppName = "putty"
	AppRedisCLI  AppName = "redis_cli"
)

// TerminalName identifies a terminal emulator preference.
type TerminalName string

const (
	TerminalAuto          TerminalName = "auto"
	TerminalMacOSDefault  TerminalName = "terminal"
	TerminalITerm         TerminalName = "iterm"
	TerminalGnome         TerminalName = "gnome-terminal"
	TerminalKonsole       TerminalName = "konsole"
	TerminalXfce          TerminalName = "xfce4-terminal"
	TerminalXterm         TerminalName = "xterm"
	TerminalPowerShell    TerminalName = "powershell"
	TerminalCmd           TerminalName = "cmd"
	TerminalWindowsTermWt TerminalName = "wt"
)

// envKeys maps each app to its environment variable override.
var envKeys = map[AppName]string{
	AppDBeaver:   "ACCESSD_CONNECTOR_DBEAVER_PATH",
	AppFileZilla: "ACCESSD_CONNECTOR_FILEZILLA_PATH",
	AppWinSCP:    "ACCESSD_CONNECTOR_WINSCP_PATH",
	AppPuTTY:     "ACCESSD_CONNECTOR_PUTTY_PATH",
	AppRedisCLI:  "ACCESSD_CONNECTOR_REDIS_CLI_PATH",
}

// terminalEnvKeys maps each OS to its terminal preference env var.
var terminalEnvKeys = map[string]string{
	"darwin":  "ACCESSD_CONNECTOR_TERMINAL_MACOS",
	"linux":   "ACCESSD_CONNECTOR_TERMINAL_LINUX",
	"windows": "ACCESSD_CONNECTOR_TERMINAL_WINDOWS",
}

// Resolution describes how a path was resolved, for diagnostics.
type Resolution struct {
	Path   string `json:"path"`
	Source string `json:"source"` // "env", "config", "auto", "override"
	App    string `json:"app"`
}

// TerminalResolution describes the resolved terminal preference.
type TerminalResolution struct {
	Terminal string `json:"terminal"`
	Source   string `json:"source"` // "env", "config", "auto"
}

// Resolver resolves application paths and terminal preferences using the
// strict priority order.
type Resolver struct {
	cfg *ConnectorConfig
}

// NewResolver creates a Resolver. If cfg is nil, only env and auto-detection
// are used.
func NewResolver(cfg *ConnectorConfig) *Resolver {
	return &Resolver{cfg: cfg}
}

// ResolveApp resolves the path for the given application using strict order:
// 1. ENV override  2. Config file  3. Auto-detection  4. Actionable error
func (r *Resolver) ResolveApp(app AppName) (Resolution, error) {
	// 1. ENV override
	if envKey, ok := envKeys[app]; ok {
		if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
			resolved, err := validateBinary(envVal)
			if err != nil {
				return Resolution{}, &DiscoveryError{
					App:     string(app),
					Source:  "env",
					Message: fmt.Sprintf("environment variable %s points to invalid path: %s", envKey, envVal),
					Hint:    fmt.Sprintf("verify %s points to a valid executable", envKey),
					Cause:   err,
				}
			}
			return Resolution{Path: resolved, Source: "env", App: string(app)}, nil
		}
	}

	// 2. Config file override
	if r.cfg != nil {
		if cfgPath := r.cfg.appPath(app); cfgPath != "" {
			resolved, err := validateBinary(cfgPath)
			if err != nil {
				return Resolution{}, &DiscoveryError{
					App:     string(app),
					Source:  "config",
					Message: fmt.Sprintf("config file path for %s is invalid: %s", app, cfgPath),
					Hint:    fmt.Sprintf("verify apps.%s in %s points to a valid executable", app, r.cfg.loadedFrom),
					Cause:   err,
				}
			}
			return Resolution{Path: resolved, Source: "config", App: string(app)}, nil
		}
	}

	// 3. Auto-detection (OS-specific search)
	candidates := autoDetectCandidates(app)
	for _, candidate := range candidates {
		resolved, err := validateBinary(candidate)
		if err == nil {
			return Resolution{Path: resolved, Source: "auto", App: string(app)}, nil
		}
	}

	// 4. Actionable error
	envKey := envKeys[app]
	return Resolution{}, &DiscoveryError{
		App:     string(app),
		Source:  "auto",
		Message: fmt.Sprintf("%s not found on this system", app),
		Hint:    buildInstallHint(app, envKey),
	}
}

// ResolveTerminal resolves the terminal emulator preference for the current OS.
// Priority: ENV → config file → "auto"
func (r *Resolver) ResolveTerminal() TerminalResolution {
	goos := runtime.GOOS

	// 1. ENV override
	if envKey, ok := terminalEnvKeys[goos]; ok {
		if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
			return TerminalResolution{Terminal: envVal, Source: "env"}
		}
	}

	// 2. Config file
	if r.cfg != nil {
		if pref := r.cfg.terminalPref(goos); pref != "" {
			return TerminalResolution{Terminal: pref, Source: "config"}
		}
	}

	// 3. Default
	return TerminalResolution{Terminal: string(TerminalAuto), Source: "auto"}
}

// DiscoveryError provides an actionable error when app resolution fails.
type DiscoveryError struct {
	App     string
	Source  string
	Message string
	Hint    string
	Cause   error
}

func (e *DiscoveryError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *DiscoveryError) Unwrap() error {
	return e.Cause
}

// validateBinary checks that a path points to a valid executable.
// For bare names (no path separator), it uses PATH lookup.
// For .app bundles on macOS, it checks the bundle exists as a directory.
func validateBinary(candidate string) (string, error) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", fmt.Errorf("empty path")
	}

	// macOS .app bundle: verify directory exists
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(trimmed), ".app") {
		info, err := os.Stat(trimmed)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a valid .app bundle", trimmed)
		}
		macOSDir := filepath.Join(trimmed, "Contents", "MacOS")
		macInfo, macErr := os.Stat(macOSDir)
		if macErr != nil {
			return "", fmt.Errorf(".app bundle missing Contents/MacOS: %w", macErr)
		}
		if !macInfo.IsDir() {
			return "", fmt.Errorf(".app bundle has invalid Contents/MacOS directory")
		}
		return trimmed, nil
	}

	// Absolute or relative path with separators: stat it
	if strings.ContainsAny(trimmed, `/\`) {
		info, err := os.Stat(trimmed)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("path is a directory: %s", trimmed)
		}
		return trimmed, nil
	}

	// Bare name: look up in PATH
	return exec.LookPath(trimmed)
}

// autoDetectCandidates returns OS-specific search candidates for an app.
func autoDetectCandidates(app AppName) []string {
	switch app {
	case AppDBeaver:
		return dbeaverCandidates()
	case AppFileZilla:
		return filezillaCandidates()
	case AppWinSCP:
		return winscpCandidates()
	case AppPuTTY:
		return puttyCandidates()
	case AppRedisCLI:
		return redisCLICandidates()
	default:
		return nil
	}
}

func dbeaverCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/DBeaver.app",
			"/Applications/DBeaverCE.app",
			"/Applications/DBeaver Community.app",
			"dbeaver",
		}
	case "linux":
		return []string{
			"dbeaver",
			"dbeaver-ce",
			"/usr/share/dbeaver-ce/dbeaver",
			"/usr/share/dbeaver/dbeaver",
			"/snap/bin/dbeaver-ce",
			"/opt/dbeaver/dbeaver",
		}
	case "windows":
		return []string{
			"dbeaver",
			"dbeaver.exe",
			`C:\Program Files\DBeaver\dbeaver.exe`,
			`C:\Program Files\DBeaver\DBeaver CE\dbeaver.exe`,
			`C:\Program Files (x86)\DBeaver\dbeaver.exe`,
		}
	default:
		return []string{"dbeaver"}
	}
}

func filezillaCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/FileZilla.app/Contents/MacOS/filezilla",
			"/Applications/FileZilla.app/Contents/MacOS/FileZilla",
			"/Applications/FileZilla.app",
			"filezilla",
		}
	case "linux":
		return []string{
			"filezilla",
			"/usr/bin/filezilla",
			"/snap/bin/filezilla",
		}
	case "windows":
		return []string{
			"filezilla",
			"filezilla.exe",
			`C:\Program Files\FileZilla FTP Client\filezilla.exe`,
			`C:\Program Files (x86)\FileZilla FTP Client\filezilla.exe`,
		}
	default:
		return []string{"filezilla"}
	}
}

func winscpCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"winscp",
			"winscp.exe",
			`C:\Program Files\WinSCP\WinSCP.exe`,
			`C:\Program Files (x86)\WinSCP\WinSCP.exe`,
		}
	default:
		// WinSCP is Windows-only
		return []string{"winscp"}
	}
}

func puttyCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"putty",
			"putty.exe",
			`C:\Program Files\PuTTY\putty.exe`,
			`C:\Program Files (x86)\PuTTY\putty.exe`,
		}
	case "darwin":
		return []string{"putty"}
	case "linux":
		return []string{
			"putty",
			"/usr/bin/putty",
		}
	default:
		return []string{"putty"}
	}
}

func redisCLICandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"redis-cli",
			"/usr/local/bin/redis-cli",
			"/opt/homebrew/bin/redis-cli",
		}
	case "linux":
		return []string{
			"redis-cli",
			"/usr/bin/redis-cli",
			"/usr/local/bin/redis-cli",
		}
	case "windows":
		return []string{
			"redis-cli",
			"redis-cli.exe",
			`C:\Program Files\Redis\redis-cli.exe`,
			`C:\Program Files\Memurai\redis-cli.exe`,
		}
	default:
		return []string{"redis-cli"}
	}
}

// buildInstallHint returns a helpful installation suggestion.
func buildInstallHint(app AppName, envKey string) string {
	var parts []string
	switch app {
	case AppDBeaver:
		parts = append(parts, "install DBeaver from https://dbeaver.io/download/")
	case AppFileZilla:
		parts = append(parts, "install FileZilla from https://filezilla-project.org/download.php")
	case AppWinSCP:
		parts = append(parts, "install WinSCP from https://winscp.net/eng/download.php")
	case AppPuTTY:
		parts = append(parts, "install PuTTY from https://www.putty.org/")
	case AppRedisCLI:
		switch runtime.GOOS {
		case "darwin":
			parts = append(parts, "install redis-cli via: brew install redis")
		case "linux":
			parts = append(parts, "install redis-cli via: apt install redis-tools (or yum install redis)")
		default:
			parts = append(parts, "install Redis from https://redis.io/download")
		}
	}
	if envKey != "" {
		parts = append(parts, fmt.Sprintf("or set %s to the full path", envKey))
	}
	configPath := DefaultConfigPath()
	if configPath != "" {
		parts = append(parts, fmt.Sprintf("or add apps.%s to %s", app, configPath))
	}
	return strings.Join(parts, "; ")
}

// DefaultConfigPath returns the default config file path for the current OS.
func DefaultConfigPath() string {
	switch runtime.GOOS {
	case "windows":
		home := os.Getenv("USERPROFILE")
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".accessd-connector", "config.yaml")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".accessd-connector", "config.yaml")
	}
}
