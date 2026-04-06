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
	Password  string `json:"password"`
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
	DBeaverTempTTL time.Duration
}

type LaunchError struct {
	Code    string
	Message string
	Hint    string
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
	switch runtime.GOOS {
	case "darwin":
		return launchMacOS(ctx, req)
	case "linux":
		return launchLinux(ctx, req)
	case "windows":
		return launchWindows(ctx, req, l.PuTTYPath)
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

	var launchErr error
	switch runtime.GOOS {
	case "darwin":
		launchErr = launchDBeaverMacOS(ctx, spec)
	case "linux":
		launchErr = launchDBeaverLinux(ctx, spec)
	case "windows":
		launchErr = launchDBeaverWindows(ctx, spec)
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
	command := redisCLICommand(req)
	if runtime.GOOS == "windows" {
		command = redisCLICommandWindows(req)
	}
	preview := redisCLICommandPreview(req)
	if strings.TrimSpace(command) == "" {
		return RedisLaunchDiagnostics{}, &LaunchError{
			Code:    "invalid_redis_command",
			Message: "failed to build redis-cli command",
			Hint:    "verify redis launch payload fields",
		}
	}

	if err := launchTerminalCommand(ctx, command); err != nil {
		return RedisLaunchDiagnostics{}, err
	}

	return RedisLaunchDiagnostics{
		CommandPreview: preview,
		UsesTLS:        req.Launch.TLS,
		Database:       req.Launch.Database,
	}, nil
}

func (l Launcher) LaunchSFTPClient(ctx context.Context, req SFTPRequest) (SFTPLaunchDiagnostics, error) {
	switch runtime.GOOS {
	case "windows":
		if strings.TrimSpace(l.WinSCPPath) == "" {
			l.WinSCPPath = "winscp"
		}
		target := winscpTarget(req)
		cmd := exec.CommandContext(ctx, l.WinSCPPath, target)
		if err := cmd.Start(); err != nil {
			return SFTPLaunchDiagnostics{}, &LaunchError{
				Code:    "sftp_launch_failed",
				Message: "failed to launch WinSCP",
				Hint:    "verify PAM_CONNECTOR_WINSCP_PATH or install winscp in PATH",
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
		filezillaPath := strings.TrimSpace(l.FileZillaPath)
		if filezillaPath == "" {
			filezillaPath = "filezilla"
		}
		target := filezillaTarget(req)
		cmd := exec.CommandContext(ctx, filezillaPath, target)
		if err := cmd.Start(); err != nil {
			return SFTPLaunchDiagnostics{}, &LaunchError{
				Code:    "sftp_launch_failed",
				Message: "failed to launch FileZilla",
				Hint:    "verify PAM_CONNECTOR_FILEZILLA_PATH or install filezilla in PATH",
				Cause:   err,
			}
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

func launchMacOS(ctx context.Context, req Request) error {
	return launchTerminalCommandMacOS(ctx, shellCommand(req))
}

func launchLinux(ctx context.Context, req Request) error {
	return launchTerminalCommandLinux(ctx, shellCommand(req))
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
	if err := cmd.Start(); err != nil {
		return &LaunchError{
			Code:    "terminal_open_failed",
			Message: "failed to open Terminal.app for launch command",
			Hint:    "verify osascript permissions and Terminal availability",
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
	for _, args := range attempts {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", args[0], err))
		}
	}
	return &LaunchError{
		Code:    "terminal_open_failed",
		Message: "failed to open terminal",
		Hint:    "install a supported terminal launcher (x-terminal-emulator, gnome-terminal, konsole, or xfce4-terminal)",
		Cause:   fmt.Errorf(strings.Join(errs, "; ")),
	}
}

func launchTerminalCommandWindows(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "cmd", "/C", "start", "", "cmd", "/K", command)
	if err := cmd.Start(); err != nil {
		return &LaunchError{
			Code:    "terminal_open_failed",
			Message: "failed to open terminal",
			Hint:    "verify cmd.exe is available and terminal launching is permitted",
			Cause:   err,
		}
	}
	return nil
}

func launchWindows(ctx context.Context, req Request, puttyPath string) error {
	if strings.TrimSpace(puttyPath) == "" {
		puttyPath = "putty"
	}
	target := req.Launch.Username + "@" + req.Launch.ProxyHost
	cmd := exec.CommandContext(
		ctx,
		puttyPath,
		"-ssh",
		target,
		"-P",
		strconv.Itoa(req.Launch.ProxyPort),
	)
	if err := cmd.Start(); err != nil {
		return &LaunchError{
			Code:    "terminal_open_failed",
			Message: "failed to open PuTTY for shell launch",
			Hint:    "verify PAM_CONNECTOR_PUTTY_PATH or install putty in PATH",
			Cause:   err,
		}
	}
	return nil
}

func shellCommand(req Request) string {
	ssh := fmt.Sprintf(
		"ssh -o PreferredAuthentications=keyboard-interactive,password -p %d %s@%s",
		req.Launch.ProxyPort,
		shellEscape(req.Launch.Username),
		shellEscape(req.Launch.ProxyHost),
	)
	title := fmt.Sprintf("PAM session %s for %s", strings.TrimSpace(req.SessionID), strings.TrimSpace(req.AssetName))
	prelude := fmt.Sprintf(
		"echo %s; echo %s; echo %s; %s",
		shellEscape(title),
		shellEscape("Paste launch token when prompted:"),
		shellEscape(req.Launch.Token),
		ssh,
	)
	return prelude
}

func redisCLICommand(req RedisRequest) string {
	parts := []string{
		"redis-cli",
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

func redisCLICommandWindows(req RedisRequest) string {
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

func launchDBeaverMacOS(ctx context.Context, spec string) error {
	attempts := [][]string{
		{"open", "-a", "DBeaver", "--args", "-con", spec},
		{"dbeaver", "-con", spec},
	}
	return launchFirstAvailable(
		ctx,
		attempts,
		"dbeaver_launch_failed",
		"failed to launch DBeaver on macOS",
		"ensure DBeaver is installed and callable via `open -a DBeaver` or `dbeaver`",
	)
}

func launchDBeaverLinux(ctx context.Context, spec string) error {
	attempts := [][]string{
		{"dbeaver", "-con", spec},
		{"dbeaver-ce", "-con", spec},
	}
	return launchFirstAvailable(
		ctx,
		attempts,
		"dbeaver_launch_failed",
		"failed to launch DBeaver on Linux",
		"ensure dbeaver or dbeaver-ce is installed and available in PATH",
	)
}

func launchDBeaverWindows(ctx context.Context, spec string) error {
	attempts := [][]string{
		{"cmd", "/C", "start", "", "dbeaver", "-con", spec},
		{"cmd", "/C", "start", "", "dbeaver.exe", "-con", spec},
	}
	return launchFirstAvailable(
		ctx,
		attempts,
		"dbeaver_launch_failed",
		"failed to launch DBeaver on Windows",
		"ensure DBeaver is installed and available via PATH or launcher association",
	)
}

func launchFirstAvailable(ctx context.Context, attempts [][]string, code, baseErr, hint string) error {
	var errs []string
	var allMissing bool = true
	for _, args := range attempts {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		} else {
			if !errors.Is(err, exec.ErrNotFound) {
				allMissing = false
			}
			errs = append(errs, fmt.Sprintf("%s: %v", strings.Join(args, " "), err))
		}
	}
	if len(errs) == 0 {
		return &LaunchError{Code: code, Message: baseErr, Hint: hint}
	}
	finalCode := code
	finalHint := hint
	if allMissing {
		finalCode = "client_not_installed"
	}
	return &LaunchError{
		Code:    finalCode,
		Message: baseErr,
		Hint:    finalHint,
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
		"password=" + specValue(req.Launch.Password),
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
