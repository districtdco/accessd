// Package launch contains thin local client launch behavior.
package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/districtd/pam/connector/internal/discovery"
)

type Request struct {
	SessionID      string `json:"session_id"`
	AssetID        string `json:"asset_id,omitempty"`
	AssetName      string `json:"asset_name,omitempty"`
	ConnectorToken string `json:"connector_token,omitempty"`
	Launch         Shell  `json:"launch"`
}

type Shell struct {
	ProxyHost string `json:"proxy_host"`
	ProxyPort int    `json:"proxy_port"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type DBeaverRequest struct {
	SessionID      string         `json:"session_id"`
	AssetID        string         `json:"asset_id,omitempty"`
	AssetName      string         `json:"asset_name,omitempty"`
	ConnectorToken string         `json:"connector_token,omitempty"`
	Launch         DBeaverPayload `json:"launch"`
}

type RedisRequest struct {
	SessionID      string       `json:"session_id"`
	AssetID        string       `json:"asset_id,omitempty"`
	AssetName      string       `json:"asset_name,omitempty"`
	ConnectorToken string       `json:"connector_token,omitempty"`
	Launch         RedisPayload `json:"launch"`
}

type SFTPRequest struct {
	SessionID      string      `json:"session_id"`
	AssetID        string      `json:"asset_id,omitempty"`
	AssetName      string      `json:"asset_name,omitempty"`
	ConnectorToken string      `json:"connector_token,omitempty"`
	Launch         SFTPPayload `json:"launch"`
}

type DBeaverPayload struct {
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Database  string `json:"database,omitempty"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	SSLMode   string `json:"ssl_mode,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

type RedisPayload struct {
	Host                  string `json:"redis_host"`
	Port                  int    `json:"redis_port"`
	Username              string `json:"redis_username,omitempty"`
	Password              string `json:"redis_password"`
	Database              int    `json:"redis_database,omitempty"`
	TLS                   bool   `json:"redis_tls,omitempty"`
	InsecureSkipVerifyTLS bool   `json:"redis_insecure_skip_verify_tls,omitempty"`
	ExpiresAt             string `json:"expires_at"`
}

type SFTPPayload struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Path      string `json:"path,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

type Launcher struct {
	PuTTYPath      string
	WinSCPPath     string
	FileZillaPath  string
	DBeaverPath    string
	RedisCLIPath   string
	DBeaverTempTTL time.Duration

	// Resolver provides cross-platform application discovery. When set, it
	// is used as the primary resolution mechanism for all app paths,
	// superseding the per-app path fields above. The per-app fields remain
	// as a backward-compatible fallback.
	Resolver *discovery.Resolver
}

type LaunchError struct {
	Code    string
	Message string
	Hint    string
	Details string
	Cause   error
}

func (e *LaunchError) Error() string {
	if e == nil {
		return "launch error"
	}
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *LaunchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type DBeaverLaunchDiagnostics struct {
	TempMaterialCreated     bool  `json:"temp_material_created"`
	ManifestWritten         bool  `json:"manifest_written"`
	CleanupScheduled        bool  `json:"cleanup_scheduled"`
	CleanupAfterSeconds     int64 `json:"cleanup_after_seconds"`
	StaleCleanupRemovedDirs int   `json:"stale_cleanup_removed_dirs,omitempty"`
}

type RedisLaunchDiagnostics struct {
	CommandPreview string `json:"command_preview"`
	UsesTLS        bool   `json:"uses_tls"`
	Database       int    `json:"database"`
}

type SFTPLaunchDiagnostics struct {
	Client         string `json:"client"`
	Target         string `json:"target"`
	InitialPath    string `json:"initial_path,omitempty"`
	CommandPreview string `json:"command_preview,omitempty"`
}

const dbeaverTempPrefix = "pam-dbeaver-launch-"

func (r Request) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(r.Launch.ProxyHost) == "" {
		return fmt.Errorf("launch.proxy_host is required")
	}
	if r.Launch.ProxyPort <= 0 || r.Launch.ProxyPort > 65535 {
		return fmt.Errorf("launch.proxy_port is invalid")
	}
	if strings.TrimSpace(r.Launch.Username) == "" {
		return fmt.Errorf("launch.username is required")
	}
	if strings.TrimSpace(r.Launch.Token) == "" {
		return fmt.Errorf("launch.token is required")
	}
	if strings.TrimSpace(r.Launch.ExpiresAt) == "" {
		return fmt.Errorf("launch.expires_at is required")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, r.Launch.ExpiresAt)
	if err != nil {
		return fmt.Errorf("launch.expires_at must be RFC3339 timestamp")
	}
	if time.Now().UTC().After(expiresAt) {
		return fmt.Errorf("launch token is already expired")
	}
	return nil
}

func (r DBeaverRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(r.Launch.Engine) == "" {
		return fmt.Errorf("launch.engine is required")
	}
	if strings.TrimSpace(r.Launch.Host) == "" {
		return fmt.Errorf("launch.host is required")
	}
	if r.Launch.Port <= 0 || r.Launch.Port > 65535 {
		return fmt.Errorf("launch.port is invalid")
	}
	if strings.TrimSpace(r.Launch.Username) == "" {
		return fmt.Errorf("launch.username is required")
	}
	if strings.TrimSpace(r.Launch.ExpiresAt) == "" {
		return fmt.Errorf("launch.expires_at is required")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, r.Launch.ExpiresAt)
	if err != nil {
		return fmt.Errorf("launch.expires_at must be RFC3339 timestamp")
	}
	if time.Now().UTC().After(expiresAt) {
		return fmt.Errorf("launch token is already expired")
	}
	return nil
}

func (r RedisRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(r.Launch.Host) == "" {
		return fmt.Errorf("launch.redis_host is required")
	}
	if r.Launch.Port <= 0 || r.Launch.Port > 65535 {
		return fmt.Errorf("launch.redis_port is invalid")
	}
	if strings.TrimSpace(r.Launch.Password) == "" {
		return fmt.Errorf("launch.redis_password is required")
	}
	if r.Launch.Database < 0 {
		return fmt.Errorf("launch.redis_database must be >= 0")
	}
	if strings.TrimSpace(r.Launch.ExpiresAt) == "" {
		return fmt.Errorf("launch.expires_at is required")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, r.Launch.ExpiresAt)
	if err != nil {
		return fmt.Errorf("launch.expires_at must be RFC3339 timestamp")
	}
	if time.Now().UTC().After(expiresAt) {
		return fmt.Errorf("launch token is already expired")
	}
	return nil
}

func (r SFTPRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(r.Launch.Host) == "" {
		return fmt.Errorf("launch.host is required")
	}
	if r.Launch.Port <= 0 || r.Launch.Port > 65535 {
		return fmt.Errorf("launch.port is invalid")
	}
	if strings.TrimSpace(r.Launch.Username) == "" {
		return fmt.Errorf("launch.username is required")
	}
	if strings.TrimSpace(r.Launch.Password) == "" {
		return fmt.Errorf("launch.password is required")
	}
	if strings.TrimSpace(r.Launch.ExpiresAt) == "" {
		return fmt.Errorf("launch.expires_at is required")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, r.Launch.ExpiresAt)
	if err != nil {
		return fmt.Errorf("launch.expires_at must be RFC3339 timestamp")
	}
	if time.Now().UTC().After(expiresAt) {
		return fmt.Errorf("launch token is already expired")
	}
	return nil
}

func (l Launcher) LaunchShell(ctx context.Context, req Request) error {
	// Resolve terminal preference if Resolver is available.
	var termPref string
	if l.Resolver != nil {
		termPref = l.Resolver.ResolveTerminal().Terminal
	}

	switch runtime.GOOS {
	case "darwin":
		return launchMacOS(ctx, req, termPref)
	case "linux":
		return launchLinux(ctx, req, termPref)
	case "windows":
		puttyPath := strings.TrimSpace(l.PuTTYPath)
		if l.Resolver != nil {
			res, err := l.Resolver.ResolveApp(discovery.AppPuTTY)
			if err != nil {
				return mapDiscoveryError(err, "putty_not_installed", "failed to open PuTTY for shell launch")
			}
			puttyPath = res.Path
		}
		return launchWindows(ctx, req, puttyPath)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func (l Launcher) LaunchDBeaver(ctx context.Context, req DBeaverRequest) (DBeaverLaunchDiagnostics, error) {
	spec := dbeaverConnectionSpec(req)
	if strings.TrimSpace(spec) == "" {
		return DBeaverLaunchDiagnostics{}, &LaunchError{
			Code:    "invalid_dbeaver_spec",
			Message: "failed to build dbeaver connection spec",
			Hint:    "verify engine/host/port launch payload",
		}
	}

	tempDir, err := os.MkdirTemp("", dbeaverTempPrefix)
	if err != nil {
		return DBeaverLaunchDiagnostics{}, &LaunchError{
			Code:    "temp_material_create_failed",
			Message: "failed to create temporary dbeaver launch directory",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   err,
		}
	}
	diagnostics := DBeaverLaunchDiagnostics{
		TempMaterialCreated: true,
	}

	manifestPath := filepath.Join(tempDir, "launch_manifest.json")
	if err := writeDBeaverManifest(manifestPath, req); err != nil {
		_ = os.RemoveAll(tempDir)
		return DBeaverLaunchDiagnostics{}, &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to write dbeaver launch manifest",
			Hint:    "check local filesystem permissions",
			Cause:   err,
		}
	}
	diagnostics.ManifestWritten = true
	cleanupTTL := l.DBeaverTempTTL
	if cleanupTTL <= 0 {
		cleanupTTL = 15 * time.Minute
	}
	diagnostics.CleanupAfterSeconds = int64(cleanupTTL.Seconds())

	dbeaverPath := strings.TrimSpace(l.DBeaverPath)
	if l.Resolver != nil {
		res, err := l.Resolver.ResolveApp(discovery.AppDBeaver)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return DBeaverLaunchDiagnostics{}, mapDiscoveryError(err, "dbeaver_not_installed", "failed to launch DBeaver")
		}
		dbeaverPath = res.Path
	}

	var launchErr error
	switch runtime.GOOS {
	case "darwin":
		launchErr = launchDBeaverMacOS(ctx, spec, dbeaverPath)
	case "linux":
		launchErr = launchDBeaverLinux(ctx, spec, dbeaverPath)
	case "windows":
		launchErr = launchDBeaverWindows(ctx, spec, dbeaverPath)
	default:
		launchErr = &LaunchError{
			Code:    "unsupported_os",
			Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
			Hint:    "use darwin/linux/windows for connector shell + dbeaver launch",
		}
	}
	if launchErr != nil {
		_ = os.RemoveAll(tempDir)
		return DBeaverLaunchDiagnostics{}, launchErr
	}

	diagnostics.CleanupScheduled = true
	go func(path string, ttl time.Duration) {
		time.Sleep(ttl)
		_ = os.RemoveAll(path)
	}(tempDir, cleanupTTL)
	return diagnostics, nil
}

func (l Launcher) LaunchRedisCLI(ctx context.Context, req RedisRequest) (RedisLaunchDiagnostics, error) {
	var termPref string
	if l.Resolver != nil {
		termPref = l.Resolver.ResolveTerminal().Terminal
	}
	var redisCLIPath string
	if l.Resolver != nil {
		res, err := l.Resolver.ResolveApp(discovery.AppRedisCLI)
		if err != nil {
			return RedisLaunchDiagnostics{}, &LaunchError{
				Code:    "redis_cli_not_found",
				Message: err.Error(),
				Hint:    discoverHint(err),
			}
		}
		redisCLIPath = res.Path
	} else {
		var err error
		redisCLIPath, err = resolveRedisCLIPath(strings.TrimSpace(l.RedisCLIPath))
		if err != nil {
			return RedisLaunchDiagnostics{}, err
		}
	}
	command := redisCLICommand(req, redisCLIPath)
	if runtime.GOOS == "windows" {
		command = redisCLICommandWindows(req, redisCLIPath)
	}
	preview := redisCLICommandPreview(req)
	if strings.TrimSpace(command) == "" {
		return RedisLaunchDiagnostics{}, &LaunchError{
			Code:    "invalid_redis_command",
			Message: "failed to build redis-cli command",
			Hint:    "verify redis launch payload fields",
		}
	}

	if err := launchTerminalCommandWithPreference(ctx, command, termPref); err != nil {
		return RedisLaunchDiagnostics{}, err
	}

	return RedisLaunchDiagnostics{
		CommandPreview: preview,
		UsesTLS:        req.Launch.TLS,
		Database:       req.Launch.Database,
	}, nil
}

func launchTerminalCommandWithPreference(ctx context.Context, command, termPref string) error {
	switch runtime.GOOS {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(termPref)) {
		case "iterm":
			return launchTerminalCommandITerm(ctx, command)
		default:
			return launchTerminalCommandMacOS(ctx, command)
		}
	case "linux":
		pref := strings.ToLower(strings.TrimSpace(termPref))
		if pref != "" && pref != "auto" {
			return launchLinuxTerminalByName(ctx, command, pref)
		}
		return launchTerminalCommandLinux(ctx, command)
	default:
		return launchTerminalCommand(ctx, command)
	}
}

func (l Launcher) LaunchSFTPClient(ctx context.Context, req SFTPRequest) (SFTPLaunchDiagnostics, error) {
	switch runtime.GOOS {
	case "windows":
		var winscpPath string
		if l.Resolver != nil {
			res, err := l.Resolver.ResolveApp(discovery.AppWinSCP)
			if err != nil {
				return SFTPLaunchDiagnostics{}, &LaunchError{
					Code:    "winscp_not_found",
					Message: err.Error(),
					Hint:    discoverHint(err),
				}
			}
			winscpPath = res.Path
		} else {
			var err error
			winscpPath, err = resolveWinSCPPath(strings.TrimSpace(l.WinSCPPath))
			if err != nil {
				return SFTPLaunchDiagnostics{}, err
			}
		}
		target := winscpTarget(req)
		cmd := exec.CommandContext(ctx, winscpPath, target)
		if err := cmd.Start(); err != nil {
			return SFTPLaunchDiagnostics{}, &LaunchError{
				Code:    "sftp_launch_failed",
				Message: "failed to launch WinSCP",
				Hint:    "verify PAM_CONNECTOR_WINSCP_PATH or install winscp in PATH",
				Details: fmt.Sprintf("command: %s", sanitizeCommandArgs([]string{winscpPath, target})),
				Cause:   err,
			}
		}
		return SFTPLaunchDiagnostics{
			Client:         "winscp",
			Target:         req.Launch.Host + ":" + strconv.Itoa(req.Launch.Port),
			InitialPath:    strings.TrimSpace(req.Launch.Path),
			CommandPreview: "winscp sftp://<username>:***@host:port/path",
		}, nil
	case "darwin", "linux":
		var filezillaPath string
		if l.Resolver != nil {
			res, err := l.Resolver.ResolveApp(discovery.AppFileZilla)
			if err != nil {
				return SFTPLaunchDiagnostics{}, mapDiscoveryError(err, "filezilla_not_installed", "failed to launch FileZilla")
			}
			filezillaPath = res.Path
		} else {
			var err error
			filezillaPath, err = resolveFileZillaPath(strings.TrimSpace(l.FileZillaPath))
			if err != nil {
				return SFTPLaunchDiagnostics{}, err
			}
		}
		target := filezillaTarget(req)
		var launchErr error
		if runtime.GOOS == "darwin" {
			launchErr = launchFileZillaMacOS(ctx, filezillaPath, target)
		} else {
			cmd := exec.CommandContext(ctx, filezillaPath, target)
			if err := cmd.Start(); err != nil {
				launchErr = &LaunchError{
					Code:    "sftp_launch_failed",
					Message: "failed to launch FileZilla",
					Hint:    "verify PAM_CONNECTOR_FILEZILLA_PATH or install filezilla in PATH",
					Details: fmt.Sprintf("command: %s", sanitizeCommandArgs([]string{filezillaPath, target})),
					Cause:   err,
				}
			}
		}
		if launchErr != nil {
			return SFTPLaunchDiagnostics{}, launchErr
		}
		return SFTPLaunchDiagnostics{
			Client:         "filezilla",
			Target:         req.Launch.Host + ":" + strconv.Itoa(req.Launch.Port),
			InitialPath:    strings.TrimSpace(req.Launch.Path),
			CommandPreview: "filezilla sftp://<username>:***@host:port/path",
		}, nil
	default:
		return SFTPLaunchDiagnostics{}, &LaunchError{
			Code:    "unsupported_os",
			Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
			Hint:    "use darwin/linux/windows for connector SFTP launch",
		}
	}
}

func launchMacOS(ctx context.Context, req Request, termPref string) error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return &LaunchError{
			Code:    "ssh_not_installed",
			Message: "failed to launch shell because ssh is not installed",
			Hint:    "install OpenSSH client on this machine",
			Cause:   err,
		}
	}
	cmd, err := shellCommand(req)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(termPref)) {
	case "iterm":
		return launchTerminalCommandITerm(ctx, cmd)
	default:
		return launchTerminalCommandMacOS(ctx, cmd)
	}
}

func launchLinux(ctx context.Context, req Request, termPref string) error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return &LaunchError{
			Code:    "ssh_not_installed",
			Message: "failed to launch shell because ssh is not installed",
			Hint:    "install OpenSSH client on this machine",
			Cause:   err,
		}
	}
	cmd, err := shellCommand(req)
	if err != nil {
		return err
	}
	pref := strings.ToLower(strings.TrimSpace(termPref))
	if pref != "" && pref != "auto" {
		return launchLinuxTerminalByName(ctx, cmd, pref)
	}
	return launchTerminalCommandLinux(ctx, cmd)
}

func launchTerminalCommand(ctx context.Context, command string) error {
	switch runtime.GOOS {
	case "darwin":
		return launchTerminalCommandMacOS(ctx, command)
	case "linux":
		return launchTerminalCommandLinux(ctx, command)
	case "windows":
		return launchTerminalCommandWindows(ctx, command)
	default:
		return &LaunchError{
			Code:    "unsupported_os",
			Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
			Hint:    "use darwin/linux/windows for connector launch",
		}
	}
}

func launchTerminalCommandMacOS(ctx context.Context, command string) error {
	script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, escapeAppleScript(command))
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open Terminal.app for launch command",
			Hint:    "verify osascript permissions and Terminal availability",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	return nil
}

func launchTerminalCommandITerm(ctx context.Context, command string) error {
	script := fmt.Sprintf(`tell application "iTerm"
	create window with default profile command "%s"
end tell`, escapeAppleScript(command))
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open iTerm2 for launch command",
			Hint:    "verify iTerm2 is installed and osascript permissions are granted",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	return nil
}

func launchLinuxTerminalByName(ctx context.Context, command, terminal string) error {
	var args []string
	switch terminal {
	case "gnome-terminal":
		args = []string{"gnome-terminal", "--", "bash", "-lc", command}
	case "konsole":
		args = []string{"konsole", "-e", "bash", "-lc", command}
	case "xfce4-terminal":
		args = []string{"xfce4-terminal", "--command", `bash -lc "` + command + `"`}
	case "xterm":
		args = []string{"xterm", "-e", "bash", "-lc", command}
	default:
		args = []string{terminal, "-e", "bash", "-lc", command}
	}
	if _, err := exec.LookPath(args[0]); err != nil {
		return &LaunchError{
			Code:    "terminal_not_installed",
			Message: fmt.Sprintf("configured terminal %q not found", terminal),
			Hint:    fmt.Sprintf("install %s or change terminal.linux in config", terminal),
			Cause:   err,
		}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: fmt.Sprintf("failed to launch %s", terminal),
			Hint:    fmt.Sprintf("verify %s is working or set terminal.linux to auto", terminal),
			Cause:   err,
		}
	}
	return nil
}

func launchTerminalCommandLinux(ctx context.Context, command string) error {
	attempts := [][]string{
		{"x-terminal-emulator", "-e", "bash", "-lc", command},
		{"gnome-terminal", "--", "bash", "-lc", command},
		{"konsole", "-e", "bash", "-lc", command},
		{"xfce4-terminal", "--command", `bash -lc "` + command + `"`},
	}

	var errs []string
	var found bool
	for _, args := range attempts {
		if _, err := exec.LookPath(args[0]); err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				errs = append(errs, fmt.Sprintf("%s: not installed", args[0]))
				continue
			}
			errs = append(errs, fmt.Sprintf("%s: %v", args[0], err))
			continue
		}
		found = true
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", args[0], err))
		}
	}
	if !found {
		return &LaunchError{
			Code:    "terminal_not_installed",
			Message: "failed to open terminal",
			Hint:    "install a supported terminal launcher (x-terminal-emulator, gnome-terminal, konsole, or xfce4-terminal)",
			Cause:   fmt.Errorf(strings.Join(errs, "; ")),
		}
	}
	return &LaunchError{
		Code:    "terminal_launch_failed",
		Message: "failed to open terminal",
		Hint:    "install a supported terminal launcher (x-terminal-emulator, gnome-terminal, konsole, or xfce4-terminal)",
		Cause:   fmt.Errorf(strings.Join(errs, "; ")),
	}
}

func launchTerminalCommandWindows(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "cmd", "/C", "start", "", "cmd", "/K", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open terminal",
			Hint:    "verify cmd.exe is available and terminal launching is permitted",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	return nil
}

func launchWindows(ctx context.Context, req Request, puttyPath string) error {
	resolvedPuTTY, err := resolvePuTTYPath(strings.TrimSpace(puttyPath))
	if err != nil {
		return err
	}
	target := req.Launch.Username + "@" + req.Launch.ProxyHost
	cmd := exec.CommandContext(
		ctx,
		resolvedPuTTY,
		"-ssh",
		target,
		"-P",
		strconv.Itoa(req.Launch.ProxyPort),
		"-pw",
		req.Launch.Token,
	)
	if err := cmd.Start(); err != nil {
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open PuTTY for shell launch",
			Hint:    "verify PAM_CONNECTOR_PUTTY_PATH or install putty in PATH",
			Details: fmt.Sprintf("command: %s", sanitizeCommandArgs([]string{resolvedPuTTY, "-ssh", target, "-P", strconv.Itoa(req.Launch.ProxyPort)})),
			Cause:   err,
		}
	}
	return nil
}

func shellCommand(req Request) (string, error) {
	askpassPath, tokenPath, err := prepareSSHAskpassFiles(req.Launch.Token)
	if err != nil {
		return "", err
	}
	knownHostsPath := filepath.Join(defaultUserHomeDir(), ".pam-connector", "known_hosts")
	ssh := fmt.Sprintf(
		"SSH_ASKPASS=%s SSH_ASKPASS_REQUIRE=force DISPLAY=${DISPLAY:-pam-connector} ssh -o PreferredAuthentications=keyboard-interactive,password -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=1 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s -o LogLevel=ERROR -p %d %s@%s",
		shellEscape(askpassPath),
		shellEscape(knownHostsPath),
		req.Launch.ProxyPort,
		shellEscape(req.Launch.Username),
		shellEscape(req.Launch.ProxyHost),
	)
	title := fmt.Sprintf("PAM session %s for %s", strings.TrimSpace(req.SessionID), strings.TrimSpace(req.AssetName))
	prelude := fmt.Sprintf(
		"mkdir -p %s; touch %s; chmod 600 %s 2>/dev/null || true; echo %s; %s; exit_code=$?; rm -f %s %s; echo; echo %s; exec bash",
		shellEscape(filepath.Dir(knownHostsPath)),
		shellEscape(knownHostsPath),
		shellEscape(knownHostsPath),
		shellEscape(title),
		ssh,
		shellEscape(askpassPath),
		shellEscape(tokenPath),
		shellEscape("SSH session ended. Close this terminal when done."),
	)
	return prelude, nil
}

func redisCLICommand(req RedisRequest, redisCLIPath string) string {
	parts := []string{
		shellEscape(redisCLIPath),
		"-h", shellEscape(req.Launch.Host),
		"-p", strconv.Itoa(req.Launch.Port),
	}
	if req.Launch.Database > 0 {
		parts = append(parts, "-n", strconv.Itoa(req.Launch.Database))
	}
	if user := strings.TrimSpace(req.Launch.Username); user != "" {
		parts = append(parts, "--user", shellEscape(user))
	}
	if req.Launch.TLS {
		parts = append(parts, "--tls")
		if req.Launch.InsecureSkipVerifyTLS {
			parts = append(parts, "--insecure")
		}
	}

	title := fmt.Sprintf("PAM redis session %s for %s", strings.TrimSpace(req.SessionID), strings.TrimSpace(req.AssetName))
	base := strings.Join(parts, " ")
	return fmt.Sprintf(
		"echo %s; export REDISCLI_AUTH=%s; %s; exit_code=$?; unset REDISCLI_AUTH; echo; echo %s; exec bash",
		shellEscape(title),
		shellEscape(req.Launch.Password),
		base,
		shellEscape("redis-cli exited with code ${exit_code}; close this terminal when done."),
	)
}

func redisCLICommandWindows(req RedisRequest, redisCLIPath string) string {
	parts := []string{
		`"` + strings.ReplaceAll(redisCLIPath, `"`, `""`) + `"`,
		"-h", req.Launch.Host,
		"-p", strconv.Itoa(req.Launch.Port),
	}
	if req.Launch.Database > 0 {
		parts = append(parts, "-n", strconv.Itoa(req.Launch.Database))
	}
	if user := strings.TrimSpace(req.Launch.Username); user != "" {
		parts = append(parts, "--user", user)
	}
	if req.Launch.TLS {
		parts = append(parts, "--tls")
		if req.Launch.InsecureSkipVerifyTLS {
			parts = append(parts, "--insecure")
		}
	}
	redisCmd := strings.Join(parts, " ")
	return fmt.Sprintf(
		"echo PAM redis session %s for %s && set REDISCLI_AUTH=%s && %s && set REDISCLI_AUTH= && echo. && echo redis-cli exited; keep this window open for review.",
		req.SessionID,
		req.AssetName,
		req.Launch.Password,
		redisCmd,
	)
}

func redisCLICommandPreview(req RedisRequest) string {
	parts := []string{
		"redis-cli",
		"-h", req.Launch.Host,
		"-p", strconv.Itoa(req.Launch.Port),
	}
	if req.Launch.Database > 0 {
		parts = append(parts, "-n", strconv.Itoa(req.Launch.Database))
	}
	if user := strings.TrimSpace(req.Launch.Username); user != "" {
		parts = append(parts, "--user", user)
	}
	if req.Launch.TLS {
		parts = append(parts, "--tls")
		if req.Launch.InsecureSkipVerifyTLS {
			parts = append(parts, "--insecure")
		}
	}
	parts = append(parts, "(auth via REDISCLI_AUTH)")
	return strings.Join(parts, " ")
}

func filezillaTarget(req SFTPRequest) string {
	user := urlEscape(strings.TrimSpace(req.Launch.Username))
	pass := urlEscape(strings.TrimSpace(req.Launch.Password))
	host := strings.TrimSpace(req.Launch.Host)
	path := strings.TrimSpace(req.Launch.Path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("sftp://%s:%s@%s:%d%s", user, pass, host, req.Launch.Port, path)
}

func winscpTarget(req SFTPRequest) string {
	return filezillaTarget(req)
}

// discoverHint extracts the Hint field from a DiscoveryError, if present.
func discoverHint(err error) string {
	var de *discovery.DiscoveryError
	if errors.As(err, &de) {
		return de.Hint
	}
	return err.Error()
}

func mapDiscoveryError(err error, notFoundCode, message string) error {
	var de *discovery.DiscoveryError
	if errors.As(err, &de) {
		code := notFoundCode
		if strings.TrimSpace(code) == "" {
			code = "client_not_installed"
		}
		if de.Source == "env" || de.Source == "config" {
			code = "invalid_configured_path"
		}
		return &LaunchError{
			Code:    code,
			Message: message,
			Hint:    de.Hint,
			Cause:   err,
		}
	}
	return &LaunchError{
		Code:    notFoundCode,
		Message: message,
		Hint:    discoverHint(err),
		Cause:   err,
	}
}

func resolvePuTTYPath(configured string) (string, error) {
	path, fromOverride, err := resolveBinaryPath(configured, []string{
		"putty",
		"putty.exe",
		`C:\Program Files\PuTTY\putty.exe`,
		`C:\Program Files (x86)\PuTTY\putty.exe`,
	})
	if err == nil {
		return path, nil
	}
	if fromOverride {
		return "", &LaunchError{
			Code:    "invalid_configured_path",
			Message: "configured PuTTY path is invalid",
			Hint:    "verify PAM_CONNECTOR_PUTTY_PATH points to a valid PuTTY executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "putty_not_installed",
		Message: "failed to open PuTTY for shell launch",
		Hint:    "install PuTTY or set PAM_CONNECTOR_PUTTY_PATH",
		Cause:   err,
	}
}

func resolveWinSCPPath(configured string) (string, error) {
	path, fromOverride, err := resolveBinaryPath(configured, []string{
		"winscp",
		"winscp.exe",
		`C:\Program Files\WinSCP\WinSCP.exe`,
		`C:\Program Files (x86)\WinSCP\WinSCP.exe`,
	})
	if err == nil {
		return path, nil
	}
	if fromOverride {
		return "", &LaunchError{
			Code:    "invalid_configured_path",
			Message: "configured WinSCP path is invalid",
			Hint:    "verify PAM_CONNECTOR_WINSCP_PATH points to a valid WinSCP executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "winscp_not_installed",
		Message: "failed to launch WinSCP",
		Hint:    "install WinSCP or set PAM_CONNECTOR_WINSCP_PATH",
		Cause:   err,
	}
}

func resolveFileZillaPath(configured string) (string, error) {
	fallbacks := []string{"filezilla"}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/Applications/FileZilla.app/Contents/MacOS/filezilla",
			"/Applications/FileZilla.app/Contents/MacOS/FileZilla",
		)
	}
	path, fromOverride, err := resolveBinaryPath(configured, fallbacks)
	if err == nil {
		return path, nil
	}
	if fromOverride {
		return "", &LaunchError{
			Code:    "invalid_configured_path",
			Message: "configured FileZilla path is invalid",
			Hint:    "verify PAM_CONNECTOR_FILEZILLA_PATH points to a valid FileZilla executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "filezilla_not_installed",
		Message: "failed to launch FileZilla",
		Hint:    "install FileZilla or set PAM_CONNECTOR_FILEZILLA_PATH",
		Cause:   err,
	}
}

func resolveRedisCLIPath(configured string) (string, error) {
	fallbacks := []string{"redis-cli"}
	if runtime.GOOS == "windows" {
		fallbacks = append(fallbacks,
			"redis-cli.exe",
			`C:\Program Files\Redis\redis-cli.exe`,
			`C:\Program Files\Memurai\redis-cli.exe`,
		)
	}
	path, fromOverride, err := resolveBinaryPath(configured, fallbacks)
	if err == nil {
		return path, nil
	}
	if fromOverride {
		return "", &LaunchError{
			Code:    "invalid_configured_path",
			Message: "configured redis-cli path is invalid",
			Hint:    "verify PAM_CONNECTOR_REDIS_CLI_PATH points to a valid redis-cli executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "redis_cli_not_installed",
		Message: "redis-cli is not installed",
		Hint:    "install redis-cli or set PAM_CONNECTOR_REDIS_CLI_PATH",
		Cause:   err,
	}
}

func resolveBinaryPath(configured string, fallbacks []string) (string, bool, error) {
	trimmed := strings.TrimSpace(configured)
	if trimmed != "" {
		resolved, err := lookupBinary(trimmed)
		if err != nil {
			return "", true, err
		}
		return resolved, true, nil
	}
	var errs []string
	for _, candidate := range fallbacks {
		resolved, err := lookupBinary(candidate)
		if err == nil {
			return resolved, false, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
	}
	return "", false, fmt.Errorf(strings.Join(errs, "; "))
}

func lookupBinary(candidate string) (string, error) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", fmt.Errorf("empty command")
	}
	if strings.ContainsAny(trimmed, `/\`) {
		if info, err := os.Stat(trimmed); err != nil {
			return "", err
		} else if info.IsDir() {
			return "", fmt.Errorf("path is a directory")
		}
		return trimmed, nil
	}
	return exec.LookPath(trimmed)
}

func launchDBeaverMacOS(ctx context.Context, spec, configuredPath string) error {
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)
	if trimmed != "" {
		if strings.HasSuffix(strings.ToLower(trimmed), ".app") {
			paths, err := macOSBundleExecutables(trimmed, []string{"dbeaver", "DBeaver"})
			if err != nil {
				return err
			}
			for _, bin := range paths {
				attempts = append(attempts, []string{bin, "-con", spec})
			}
			attempts = append(attempts, []string{"open", "-a", trimmed, "--args", "-con", spec})
		} else {
			attempts = append(attempts, []string{trimmed, "-con", spec})
		}
	} else {
		attempts = append(attempts,
			[]string{"/Applications/DBeaver.app/Contents/MacOS/dbeaver", "-con", spec},
			[]string{"/Applications/DBeaver.app/Contents/MacOS/DBeaver", "-con", spec},
			[]string{"/Applications/DBeaverCE.app/Contents/MacOS/dbeaver", "-con", spec},
			[]string{"/Applications/DBeaverCE.app/Contents/MacOS/DBeaver", "-con", spec},
			[]string{"open", "-a", "DBeaver", "--args", "-con", spec},
			[]string{"open", "-a", "/Applications/DBeaver.app", "--args", "-con", spec},
			[]string{"open", "-a", "/Applications/DBeaverCE.app", "--args", "-con", spec},
			[]string{"dbeaver", "-con", spec},
		)
	}
	return launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "dbeaver_launch_failed",
		MissingCode:   "dbeaver_not_installed",
		BaseErr:       "failed to launch DBeaver on macOS",
		Hint:          "ensure DBeaver is installed or set PAM_CONNECTOR_DBEAVER_PATH to a valid app/binary path",
		ConfiguredEnv: "PAM_CONNECTOR_DBEAVER_PATH",
	})
}

func launchFileZillaMacOS(ctx context.Context, configuredPath, target string) error {
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)
	if trimmed == "" {
		trimmed = "/Applications/FileZilla.app"
	}
	if strings.HasSuffix(strings.ToLower(trimmed), ".app") {
		paths, err := macOSBundleExecutables(trimmed, []string{"filezilla", "FileZilla"})
		if err != nil {
			return err
		}
		for _, bin := range paths {
			attempts = append(attempts, []string{bin, target})
		}
		attempts = append(attempts, []string{"open", "-a", trimmed, "--args", target})
	} else {
		attempts = append(attempts, []string{trimmed, target})
	}
	return launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "sftp_launch_failed",
		MissingCode:   "filezilla_not_installed",
		BaseErr:       "failed to launch FileZilla",
		Hint:          "ensure FileZilla is installed or set PAM_CONNECTOR_FILEZILLA_PATH to a valid app/binary path",
		ConfiguredEnv: "PAM_CONNECTOR_FILEZILLA_PATH",
	})
}

func macOSBundleExecutables(bundlePath string, preferred []string) ([]string, error) {
	trimmed := strings.TrimSpace(bundlePath)
	if !strings.HasSuffix(strings.ToLower(trimmed), ".app") {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "configured macOS app path must end with .app",
			Hint:    "set app path to a valid .app bundle",
		}
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "configured .app bundle path is invalid",
			Hint:    "verify the configured .app bundle exists",
			Cause:   err,
		}
	}
	if !info.IsDir() {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "configured .app bundle is not a directory",
			Hint:    "set app path to a valid .app bundle",
		}
	}
	macOSDir := filepath.Join(trimmed, "Contents", "MacOS")
	macOSInfo, macOSErr := os.Stat(macOSDir)
	if macOSErr != nil || !macOSInfo.IsDir() {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "configured .app bundle is missing Contents/MacOS executable directory",
			Hint:    "reinstall the app or set path to a valid executable",
			Cause:   macOSErr,
		}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(preferred)+2)
	for _, name := range preferred {
		candidate := filepath.Join(macOSDir, name)
		if stat, statErr := os.Stat(candidate); statErr == nil && !stat.IsDir() {
			out = append(out, candidate)
			seen[candidate] = struct{}{}
		}
	}
	entries, readErr := os.ReadDir(macOSDir)
	if readErr != nil {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "failed to inspect .app bundle executables",
			Hint:    "verify local read permissions for the app bundle",
			Cause:   readErr,
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		candidate := filepath.Join(macOSDir, entry.Name())
		if _, ok := seen[candidate]; ok {
			continue
		}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil, &LaunchError{
			Code:    "invalid_bundle",
			Message: "configured .app bundle has no launchable binary in Contents/MacOS",
			Hint:    "reinstall the app or set path to the executable inside the bundle",
		}
	}
	return out, nil
}

func launchDBeaverLinux(ctx context.Context, spec, configuredPath string) error {
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)
	if trimmed != "" {
		attempts = append(attempts, []string{trimmed, "-con", spec})
	} else {
		attempts = append(attempts,
			[]string{"dbeaver", "-con", spec},
			[]string{"dbeaver-ce", "-con", spec},
		)
	}
	return launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "dbeaver_launch_failed",
		MissingCode:   "dbeaver_not_installed",
		BaseErr:       "failed to launch DBeaver on Linux",
		Hint:          "ensure dbeaver or dbeaver-ce is installed, or set PAM_CONNECTOR_DBEAVER_PATH",
		ConfiguredEnv: "PAM_CONNECTOR_DBEAVER_PATH",
	})
}

func launchDBeaverWindows(ctx context.Context, spec, configuredPath string) error {
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)
	if trimmed != "" {
		attempts = append(attempts, []string{"cmd", "/C", "start", "", trimmed, "-con", spec})
	} else {
		attempts = append(attempts,
			[]string{"cmd", "/C", "start", "", "dbeaver", "-con", spec},
			[]string{"cmd", "/C", "start", "", "dbeaver.exe", "-con", spec},
		)
	}
	return launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "dbeaver_launch_failed",
		MissingCode:   "dbeaver_not_installed",
		BaseErr:       "failed to launch DBeaver on Windows",
		Hint:          "ensure DBeaver is installed or set PAM_CONNECTOR_DBEAVER_PATH",
		ConfiguredEnv: "PAM_CONNECTOR_DBEAVER_PATH",
	})
}

type launchAttemptOptions struct {
	Code          string
	MissingCode   string
	BaseErr       string
	Hint          string
	ConfiguredEnv string
}

func launchFirstAvailable(ctx context.Context, attempts [][]string, options launchAttemptOptions) error {
	var errs []string
	allMissing := true
	configuredPath := strings.TrimSpace(options.ConfiguredEnv) != "" && strings.TrimSpace(os.Getenv(options.ConfiguredEnv)) != ""
	for _, args := range attempts {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			allMissing = false
		}
		msg := fmt.Sprintf("%s: %v", sanitizeCommandArgs(args), err)
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			msg += " (" + sanitizeCommandArg(trimmed) + ")"
		}
		errs = append(errs, msg)
	}
	if len(errs) == 0 {
		return &LaunchError{Code: options.Code, Message: options.BaseErr, Hint: options.Hint}
	}
	finalCode := options.Code
	finalHint := options.Hint
	if allMissing {
		if configuredPath {
			finalCode = "invalid_configured_path"
			finalHint = "configured launcher path is invalid; verify " + options.ConfiguredEnv
		} else if strings.TrimSpace(options.MissingCode) != "" {
			finalCode = options.MissingCode
		} else {
			finalCode = "client_not_installed"
		}
	}
	return &LaunchError{
		Code:    finalCode,
		Message: options.BaseErr,
		Hint:    finalHint,
		Details: strings.Join(errs, "; "),
		Cause:   fmt.Errorf(strings.Join(errs, "; ")),
	}
}

func dbeaverConnectionSpec(req DBeaverRequest) string {
	engine := normalizeEngine(req.Launch.Engine)
	parts := []string{
		"driver=" + specValue(engine),
		"host=" + specValue(req.Launch.Host),
		"port=" + strconv.Itoa(req.Launch.Port),
		"user=" + specValue(req.Launch.Username),
	}
	if password := strings.TrimSpace(req.Launch.Password); password != "" {
		parts = append(parts, "password="+specValue(password))
	}
	if db := strings.TrimSpace(req.Launch.Database); db != "" {
		parts = append(parts, "database="+specValue(db))
	}
	if sslMode := strings.TrimSpace(req.Launch.SSLMode); sslMode != "" {
		parts = append(parts, "sslMode="+specValue(sslMode))
	}
	if name := strings.TrimSpace(req.AssetName); name != "" {
		parts = append(parts, "name="+specValue("PAM - "+name))
	}
	return strings.Join(parts, "|")
}

func sanitizeCommandArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	safe := make([]string, len(args))
	for i, arg := range args {
		if i > 0 && strings.EqualFold(strings.TrimSpace(args[i-1]), "-pw") {
			safe[i] = "<redacted>"
			continue
		}
		safe[i] = sanitizeCommandArg(arg)
	}
	return strings.Join(safe, " ")
}

func sanitizeCommandArg(arg string) string {
	if strings.HasPrefix(arg, "sftp://") {
		if scheme := strings.Index(arg, "://"); scheme >= 0 {
			rest := arg[scheme+3:]
			if at := strings.Index(rest, "@"); at >= 0 {
				creds := rest[:at]
				if colon := strings.Index(creds, ":"); colon >= 0 {
					rest = creds[:colon] + ":<redacted>@" + rest[at+1:]
					return arg[:scheme+3] + rest
				}
			}
		}
	}
	if strings.Contains(arg, "password=") {
		parts := strings.Split(arg, "|")
		for i, part := range parts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "password=") {
				parts[i] = "password=<redacted>"
			}
		}
		arg = strings.Join(parts, "|")
	}
	if idx := strings.Index(strings.ToUpper(arg), "REDISCLI_AUTH="); idx >= 0 {
		prefix := arg[:idx]
		return prefix + "REDISCLI_AUTH=<redacted>"
	}
	return arg
}

func normalizeEngine(engine string) string {
	normalized := strings.ToLower(strings.TrimSpace(engine))
	switch normalized {
	case "postgres", "postgresql":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "sqlserver", "mssql":
		return "sqlserver"
	default:
		if normalized == "" {
			return "postgresql"
		}
		return normalized
	}
}

func specValue(v string) string {
	clean := strings.TrimSpace(v)
	clean = strings.ReplaceAll(clean, "|", `\|`)
	return clean
}

func writeDBeaverManifest(path string, req DBeaverRequest) error {
	payload := map[string]any{
		"session_id": req.SessionID,
		"asset_id":   req.AssetID,
		"asset_name": req.AssetName,
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"launch": map[string]any{
			"engine":     req.Launch.Engine,
			"host":       req.Launch.Host,
			"port":       req.Launch.Port,
			"database":   req.Launch.Database,
			"username":   req.Launch.Username,
			"ssl_mode":   req.Launch.SSLMode,
			"expires_at": req.Launch.ExpiresAt,
		},
	}
	blob, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o600)
}

func CleanupStaleDBeaverTemp(ttl time.Duration) (int, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return 0, err
	}
	removed := 0
	cutoff := time.Now().UTC().Add(-ttl)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, dbeaverTempPrefix) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().UTC().After(cutoff) {
			continue
		}
		if removeErr := os.RemoveAll(filepath.Join(os.TempDir(), name)); removeErr == nil {
			removed++
		}
	}
	return removed, nil
}

func TryCopyTokenToClipboard(ctx context.Context, token string) (bool, string) {
	value := strings.TrimSpace(token)
	if value == "" {
		return false, ""
	}
	switch runtime.GOOS {
	case "darwin":
		return runClipboardCommand(ctx, value, "pbcopy")
	case "linux":
		if ok, tool := runClipboardCommand(ctx, value, "wl-copy"); ok {
			return true, tool
		}
		if ok, tool := runClipboardCommand(ctx, value, "xclip", "-selection", "clipboard"); ok {
			return true, tool
		}
		return false, ""
	case "windows":
		return runClipboardCommand(ctx, value, "powershell", "-NoProfile", "-Command", "Set-Clipboard")
	default:
		return false, ""
	}
}

func runClipboardCommand(ctx context.Context, value, bin string, args ...string) (bool, string) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(value)
	if err := cmd.Run(); err != nil {
		return false, ""
	}
	return true, bin
}

func prepareSSHAskpassFiles(token string) (askpassPath string, tokenPath string, err error) {
	tok, err := os.CreateTemp("", "pam-launch-token-*")
	if err != nil {
		return "", "", &LaunchError{
			Code:    "temp_material_create_failed",
			Message: "failed to create temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   err,
		}
	}
	tokenPath = tok.Name()
	cleanup := func() {
		_ = os.Remove(tokenPath)
	}
	if _, writeErr := tok.WriteString(strings.TrimSpace(token) + "\n"); writeErr != nil {
		_ = tok.Close()
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to write temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   writeErr,
		}
	}
	if closeErr := tok.Close(); closeErr != nil {
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to persist temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   closeErr,
		}
	}
	if chmodErr := os.Chmod(tokenPath, 0o600); chmodErr != nil {
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to secure temporary token file",
			Hint:    "check local temporary directory permissions",
			Cause:   chmodErr,
		}
	}

	ask, err := os.CreateTemp("", "pam-ssh-askpass-*")
	if err != nil {
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_create_failed",
			Message: "failed to create temporary askpass helper",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   err,
		}
	}
	askpassPath = ask.Name()
	askpassScript := "#!/bin/sh\ncat " + shellEscape(tokenPath) + "\n"
	if _, writeErr := ask.WriteString(askpassScript); writeErr != nil {
		_ = ask.Close()
		_ = os.Remove(askpassPath)
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to write temporary askpass helper",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   writeErr,
		}
	}
	if closeErr := ask.Close(); closeErr != nil {
		_ = os.Remove(askpassPath)
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to persist temporary askpass helper",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   closeErr,
		}
	}
	if chmodErr := os.Chmod(askpassPath, 0o700); chmodErr != nil {
		_ = os.Remove(askpassPath)
		cleanup()
		return "", "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to secure temporary askpass helper",
			Hint:    "check local temporary directory permissions",
			Cause:   chmodErr,
		}
	}
	return askpassPath, tokenPath, nil
}

func defaultUserHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return os.TempDir()
	}
	return home
}

func shellEscape(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'"'"'`) + "'"
}

func escapeAppleScript(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

func urlEscape(v string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		" ", "%20",
		"@", "%40",
		":", "%3A",
		"/", "%2F",
		"?", "%3F",
		"#", "%23",
		"&", "%26",
		"=", "%3D",
	)
	return replacer.Replace(v)
}
