// Package launch contains thin local client launch behavior.
package launch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	ProxyHost        string `json:"proxy_host"`
	ProxyPort        int    `json:"proxy_port"`
	Username         string `json:"username"` // upstream username (display identity)
	ProxyUsername    string `json:"proxy_username,omitempty"`
	UpstreamUsername string `json:"upstream_username,omitempty"`
	TargetAssetName  string `json:"target_asset_name,omitempty"`
	TargetHost       string `json:"target_host,omitempty"`
	Token            string `json:"token"`
	ExpiresAt        string `json:"expires_at"`
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
	Engine           string `json:"engine"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Database         string `json:"database,omitempty"`
	Username         string `json:"username"` // upstream username
	UpstreamUsername string `json:"upstream_username,omitempty"`
	TargetAssetName  string `json:"target_asset_name,omitempty"`
	TargetHost       string `json:"target_host,omitempty"`
	Password         string `json:"password,omitempty"`
	SSLMode          string `json:"ssl_mode,omitempty"`
	ExpiresAt        string `json:"expires_at"`
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
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Username         string `json:"username"` // upstream username (display identity)
	ProxyUsername    string `json:"proxy_username,omitempty"`
	UpstreamUsername string `json:"upstream_username,omitempty"`
	TargetAssetName  string `json:"target_asset_name,omitempty"`
	TargetHost       string `json:"target_host,omitempty"`
	Password         string `json:"password"`
	Path             string `json:"path,omitempty"`
	ExpiresAt        string `json:"expires_at"`
}

// LauncherConfig holds configuration for the launcher.
type LauncherConfig struct {
	PuTTYPath      string
	WinSCPPath     string
	FileZillaPath  string
	DBeaverPath    string
	RedisCLIPath   string
	DBeaverTempTTL time.Duration
	Logger         *slog.Logger
}

// Launcher provides cross-platform application launch functionality.
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

	logger *slog.Logger
}

// NewLauncher creates a new Launcher with the given configuration.
func NewLauncher(cfg LauncherConfig) *Launcher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Launcher{
		PuTTYPath:      cfg.PuTTYPath,
		WinSCPPath:     cfg.WinSCPPath,
		FileZillaPath:  cfg.FileZillaPath,
		DBeaverPath:    cfg.DBeaverPath,
		RedisCLIPath:   cfg.RedisCLIPath,
		DBeaverTempTTL: cfg.DBeaverTempTTL,
		Resolver:       nil, // Set via SetResolver
		logger:         logger,
	}
}

// SetResolver sets the discovery resolver for the launcher.
func (l *Launcher) SetResolver(r *discovery.Resolver) {
	l.Resolver = r
}

// log returns the launcher's logger, falling back to slog.Default().
func (l *Launcher) log() *slog.Logger {
	if l.logger != nil {
		return l.logger
	}
	return slog.Default()
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
	TempMaterialCreated     bool   `json:"temp_material_created"`
	ManifestWritten         bool   `json:"manifest_written"`
	CleanupScheduled        bool   `json:"cleanup_scheduled"`
	CleanupAfterSeconds     int64  `json:"cleanup_after_seconds"`
	StaleCleanupRemovedDirs int    `json:"stale_cleanup_removed_dirs,omitempty"`
	ResolvedPath            string `json:"resolved_path,omitempty"`
	LaunchMode              string `json:"launch_mode,omitempty"` // "direct_binary", "open_a", "applescript"
}

type RedisLaunchDiagnostics struct {
	CommandPreview string `json:"command_preview"`
	UsesTLS        bool   `json:"uses_tls"`
	Database       int    `json:"database"`
	ResolvedPath   string `json:"resolved_path,omitempty"`
}

type SFTPLaunchDiagnostics struct {
	Client         string `json:"client"`
	Target         string `json:"target"`
	InitialPath    string `json:"initial_path,omitempty"`
	CommandPreview string `json:"command_preview,omitempty"`
	ResolvedPath   string `json:"resolved_path,omitempty"`
	LaunchMode     string `json:"launch_mode,omitempty"` // "direct_binary", "open_a"
	Protocol       string `json:"protocol,omitempty"`    // "sftp"
}

type ShellLaunchDiagnostics struct {
	ResolvedPath string `json:"resolved_path,omitempty"`
	Terminal     string `json:"terminal,omitempty"`
}

const dbeaverTempPrefix = "accessd-dbeaver-launch-"

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
	if strings.TrimSpace(r.Launch.ProxyUsername) == "" && strings.TrimSpace(r.Launch.Username) == "" {
		return fmt.Errorf("launch.proxy_username or launch.username is required")
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
	if strings.TrimSpace(r.Launch.Username) == "" && strings.TrimSpace(r.Launch.UpstreamUsername) == "" {
		return fmt.Errorf("launch.username or launch.upstream_username is required")
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
	if strings.TrimSpace(r.Launch.ProxyUsername) == "" && strings.TrimSpace(r.Launch.Username) == "" {
		return fmt.Errorf("launch.proxy_username or launch.username is required")
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

func (l *Launcher) LaunchShell(ctx context.Context, req Request) (ShellLaunchDiagnostics, error) {
	proxyUsername := proxyUsernameForShell(req.Launch)
	displayUsername := upstreamUsernameForShell(req.Launch)
	displayTarget := targetIdentity(req.AssetName, req.Launch.TargetAssetName, req.Launch.TargetHost)
	l.log().Info("shell launch: starting",
		"session_id", req.SessionID,
		"proxy_host", req.Launch.ProxyHost,
		"proxy_port", req.Launch.ProxyPort,
		"proxy_username", proxyUsername,
		"upstream_username", displayUsername,
		"display_target", displayTarget,
		"token_type", "launch",
		"token_len", len(req.Launch.Token),
		"os", runtime.GOOS)

	// Resolve terminal preference if Resolver is available.
	var termPref string
	if l.Resolver != nil {
		termPref = l.Resolver.ResolveTerminal().Terminal
	}

	switch runtime.GOOS {
	case "darwin":
		return launchMacOS(ctx, req, termPref, l.logger)
	case "linux":
		return launchLinux(ctx, req, termPref, l.logger)
	case "windows":
		puttyPath := strings.TrimSpace(l.PuTTYPath)
		if l.Resolver != nil {
			res, err := l.Resolver.ResolveApp(discovery.AppPuTTY)
			if err != nil {
				return ShellLaunchDiagnostics{}, mapDiscoveryError(err, "putty_not_installed", "failed to open PuTTY for shell launch")
			}
			puttyPath = res.Path
		}
		return launchWindows(ctx, req, puttyPath, l.logger)
	default:
		return ShellLaunchDiagnostics{}, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func (l *Launcher) LaunchDBeaver(ctx context.Context, req DBeaverRequest) (DBeaverLaunchDiagnostics, error) {
	l.log().Info("dbeaver launch: starting",
		"session_id", req.SessionID,
		"engine", req.Launch.Engine,
		"host", req.Launch.Host,
		"port", req.Launch.Port,
		"database", req.Launch.Database,
		"os", runtime.GOOS)
	spec := dbeaverConnectionSpec(req)
	l.log().Info("dbeaver launch: connection spec prepared",
		"session_id", req.SessionID,
		"spec", sanitizeCommandArg(spec))
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
	var resolvedPath string
	var launchMode string

	if l.Resolver != nil {
		res, err := l.Resolver.ResolveApp(discovery.AppDBeaver)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return DBeaverLaunchDiagnostics{}, mapDiscoveryError(err, "dbeaver_not_installed", "failed to launch DBeaver")
		}
		dbeaverPath = res.Path
		resolvedPath = res.Path
	} else {
		var err error
		dbeaverPath, err = resolveDBeaverPath(dbeaverPath)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return DBeaverLaunchDiagnostics{}, err
		}
		resolvedPath = dbeaverPath
	}

	var launchErr error
	launchWithSpec := func(connSpec string) (error, string) {
		switch runtime.GOOS {
		case "darwin":
			return launchDBeaverMacOS(ctx, connSpec, dbeaverPath, l.logger)
		case "linux":
			return launchDBeaverLinux(ctx, connSpec, dbeaverPath, l.logger), "direct_binary"
		case "windows":
			return launchDBeaverWindows(ctx, connSpec, dbeaverPath, l.logger), "direct_binary"
		default:
			return &LaunchError{
				Code:    "unsupported_os",
				Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
				Hint:    "use darwin/linux/windows for connector shell + dbeaver launch",
			}, ""
		}
	}

	launchErr, launchMode = launchWithSpec(spec)
	if launchErr != nil {
		repairTag := "repair-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		repairedSpec := dbeaverConnectionSpecWithNameSuffix(req, repairTag)
		if strings.TrimSpace(repairedSpec) != "" && repairedSpec != spec {
			l.log().Warn(
				"dbeaver launch failed; retrying with repaired connection identity",
				"session_id", req.SessionID,
				"repair_tag", repairTag,
				"error", launchErr,
			)
			if retryErr, retryMode := launchWithSpec(repairedSpec); retryErr == nil {
				launchErr = nil
				launchMode = retryMode
				l.log().Info(
					"dbeaver launch recovery succeeded",
					"session_id", req.SessionID,
					"repair_tag", repairTag,
				)
			}
		}
	}
	if launchErr != nil {
		_ = os.RemoveAll(tempDir)
		return DBeaverLaunchDiagnostics{}, launchErr
	}

	if runtime.GOOS == "darwin" {
		go func() {
			start := time.Now()
			// Retry activation because app startup/focus handoff timing varies on macOS.
			activationSteps := []time.Duration{1 * time.Second, 2 * time.Second, 2 * time.Second}
			for idx, wait := range activationSteps {
				time.Sleep(wait)
				if err := activateMacOSApp("DBeaver"); err != nil {
					l.log().Warn("dbeaver launch: activation attempt failed",
						"attempt", idx+1,
						"elapsed_ms", time.Since(start).Milliseconds(),
						"error", err)
					continue
				}
				l.log().Info("dbeaver launch: activation attempted",
					"attempt", idx+1,
					"elapsed_ms", time.Since(start).Milliseconds())
			}
		}()
	}

	diagnostics.CleanupScheduled = true
	diagnostics.ResolvedPath = resolvedPath
	diagnostics.LaunchMode = launchMode
	go func(path string, ttl time.Duration) {
		time.Sleep(ttl)
		_ = os.RemoveAll(path)
	}(tempDir, cleanupTTL)
	return diagnostics, nil
}

func (l *Launcher) LaunchRedisCLI(ctx context.Context, req RedisRequest) (RedisLaunchDiagnostics, error) {
	var termPref string
	if l.Resolver != nil {
		termPref = l.Resolver.ResolveTerminal().Terminal
	}
	var redisCLIPath string
	var resolvedPath string
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
		resolvedPath = res.Path
	} else {
		var err error
		redisCLIPath, err = resolveRedisCLIPath(strings.TrimSpace(l.RedisCLIPath))
		if err != nil {
			return RedisLaunchDiagnostics{}, err
		}
		resolvedPath = redisCLIPath
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

	if err := launchTerminalCommandWithPreference(ctx, command, termPref, l.logger); err != nil {
		return RedisLaunchDiagnostics{}, err
	}

	return RedisLaunchDiagnostics{
		CommandPreview: preview,
		UsesTLS:        req.Launch.TLS,
		Database:       req.Launch.Database,
		ResolvedPath:   resolvedPath,
	}, nil
}

func launchTerminalCommandWithPreference(ctx context.Context, command, termPref string, logger *slog.Logger) error {
	switch runtime.GOOS {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(termPref)) {
		case "iterm":
			return launchTerminalCommandITerm(ctx, command, logger)
		default:
			return launchTerminalCommandMacOS(ctx, command, logger)
		}
	case "linux":
		pref := strings.ToLower(strings.TrimSpace(termPref))
		if pref != "" && pref != "auto" {
			return launchLinuxTerminalByName(ctx, command, pref, logger)
		}
		return launchTerminalCommandLinux(ctx, command, logger)
	default:
		return launchTerminalCommand(ctx, command, logger)
	}
}

func (l *Launcher) LaunchSFTPClient(ctx context.Context, req SFTPRequest) (SFTPLaunchDiagnostics, error) {
	// FileZilla is the ONLY supported SFTP client on ALL platforms.
	// WinSCP is not used.
	var filezillaPath string
	var resolvedPath string
	if l.Resolver != nil {
		res, err := l.Resolver.ResolveApp(discovery.AppFileZilla)
		if err != nil {
			return SFTPLaunchDiagnostics{}, mapDiscoveryError(err, "filezilla_not_installed", "failed to launch FileZilla")
		}
		filezillaPath = res.Path
		resolvedPath = res.Path
	} else {
		var err error
		filezillaPath, err = resolveFileZillaPath(strings.TrimSpace(l.FileZillaPath))
		if err != nil {
			return SFTPLaunchDiagnostics{}, err
		}
		resolvedPath = filezillaPath
	}

	l.log().Info("sftp launch: resolved FileZilla path",
		"path", resolvedPath, "os", runtime.GOOS, "protocol", "sftp")

	hostKey, hostKeyErr := fetchSSHHostKey(ctx, req.Launch.Host, req.Launch.Port)
	if hostKeyErr != nil {
		return SFTPLaunchDiagnostics{}, &LaunchError{
			Code:    "sftp_hostkey_resolve_failed",
			Message: "failed to resolve SSH host key for FileZilla launch",
			Hint:    "verify AccessD SSH proxy is reachable and ready",
			Cause:   hostKeyErr,
		}
	}
	if err := ensureFileZillaKnownHost(req.Launch.Host, req.Launch.Port, hostKey); err != nil {
		return SFTPLaunchDiagnostics{}, &LaunchError{
			Code:    "sftp_known_hosts_update_failed",
			Message: "failed to prime FileZilla known hosts for automated SFTP launch",
			Hint:    "verify local filesystem permissions for FileZilla profile directories",
			Cause:   err,
		}
	}

	// Build sftp:// target URL — always explicit protocol, always URL-encoded password.
	target := filezillaTarget(req)
	siteName := sftpSiteDisplayName(req)
	fileZillaArgs := filezillaArgs(target, siteName)

	l.log().Info("sftp launch: executing FileZilla",
		"target_redacted", "sftp://<user>:***@"+req.Launch.Host+":"+strconv.Itoa(req.Launch.Port),
		"protocol", "sftp", "launch_mode", "direct_binary",
		"display_site", siteName)

	var launchErr error
	var launchMode string
	switch runtime.GOOS {
	case "darwin":
		launchErr, launchMode = launchFileZillaMacOS(ctx, filezillaPath, target, siteName, l.log())
	case "windows":
		launchErr = launchFirstAvailable(ctx, [][]string{append([]string{filezillaPath}, fileZillaArgs...)}, launchAttemptOptions{
			Code:          "sftp_launch_failed",
			MissingCode:   "filezilla_not_installed",
			BaseErr:       "failed to launch FileZilla",
			Hint:          "verify ACCESSD_CONNECTOR_FILEZILLA_PATH or install FileZilla",
			ConfiguredEnv: "ACCESSD_CONNECTOR_FILEZILLA_PATH",
		})
		launchMode = "direct_binary"
	case "linux":
		launchErr = launchFirstAvailable(ctx, [][]string{append([]string{filezillaPath}, fileZillaArgs...)}, launchAttemptOptions{
			Code:          "sftp_launch_failed",
			MissingCode:   "filezilla_not_installed",
			BaseErr:       "failed to launch FileZilla",
			Hint:          "verify ACCESSD_CONNECTOR_FILEZILLA_PATH or install filezilla in PATH",
			ConfiguredEnv: "ACCESSD_CONNECTOR_FILEZILLA_PATH",
		})
		launchMode = "direct_binary"
	default:
		return SFTPLaunchDiagnostics{}, &LaunchError{
			Code:    "unsupported_os",
			Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
			Hint:    "use darwin/linux/windows for connector SFTP launch",
		}
	}
	if launchErr != nil {
		return SFTPLaunchDiagnostics{}, launchErr
	}
	if runtime.GOOS == "darwin" {
		go func() {
			start := time.Now()
			for idx, wait := range []time.Duration{0, 2 * time.Second} {
				time.Sleep(wait)
				if err := activateMacOSApp("FileZilla"); err != nil {
					l.log().Warn("sftp launch: activation attempt failed",
						"attempt", idx+1,
						"elapsed_ms", time.Since(start).Milliseconds(),
						"error", err)
					continue
				}
				l.log().Info("sftp launch: activation attempted",
					"attempt", idx+1,
					"elapsed_ms", time.Since(start).Milliseconds())
			}
		}()
	}
	return SFTPLaunchDiagnostics{
		Client:         "filezilla",
		Target:         req.Launch.Host + ":" + strconv.Itoa(req.Launch.Port),
		InitialPath:    strings.TrimSpace(req.Launch.Path),
		CommandPreview: "filezilla sftp://<username>:***@host:port/path",
		ResolvedPath:   resolvedPath,
		LaunchMode:     launchMode,
		Protocol:       "sftp",
	}, nil
}

func launchMacOS(ctx context.Context, req Request, termPref string, logger *slog.Logger) (ShellLaunchDiagnostics, error) {
	if _, err := exec.LookPath("ssh"); err != nil {
		return ShellLaunchDiagnostics{}, &LaunchError{
			Code:    "ssh_not_installed",
			Message: "failed to launch shell because ssh is not installed",
			Hint:    "install OpenSSH client on this machine",
			Cause:   err,
		}
	}
	cmd, err := shellCommand(req)
	if err != nil {
		return ShellLaunchDiagnostics{}, err
	}
	var terminal string
	switch strings.ToLower(strings.TrimSpace(termPref)) {
	case "iterm":
		terminal = "iterm2"
		return ShellLaunchDiagnostics{ResolvedPath: "ssh", Terminal: terminal}, launchTerminalCommandITerm(ctx, cmd, logger)
	default:
		terminal = "terminal"
		return ShellLaunchDiagnostics{ResolvedPath: "ssh", Terminal: terminal}, launchTerminalCommandMacOS(ctx, cmd, logger)
	}
}

func launchLinux(ctx context.Context, req Request, termPref string, logger *slog.Logger) (ShellLaunchDiagnostics, error) {
	if _, err := exec.LookPath("ssh"); err != nil {
		return ShellLaunchDiagnostics{}, &LaunchError{
			Code:    "ssh_not_installed",
			Message: "failed to launch shell because ssh is not installed",
			Hint:    "install OpenSSH client on this machine",
			Cause:   err,
		}
	}
	cmd, err := shellCommand(req)
	if err != nil {
		return ShellLaunchDiagnostics{}, err
	}
	var terminal string
	pref := strings.ToLower(strings.TrimSpace(termPref))
	if pref != "" && pref != "auto" {
		terminal = pref
		return ShellLaunchDiagnostics{ResolvedPath: "ssh", Terminal: terminal}, launchLinuxTerminalByName(ctx, cmd, pref, logger)
	}
	terminal = "auto"
	return ShellLaunchDiagnostics{ResolvedPath: "ssh", Terminal: terminal}, launchTerminalCommandLinux(ctx, cmd, logger)
}

func launchTerminalCommand(ctx context.Context, command string, logger *slog.Logger) error {
	switch runtime.GOOS {
	case "darwin":
		return launchTerminalCommandMacOS(ctx, command, logger)
	case "linux":
		return launchTerminalCommandLinux(ctx, command, logger)
	case "windows":
		return launchTerminalCommandWindows(ctx, command, logger)
	default:
		return &LaunchError{
			Code:    "unsupported_os",
			Message: fmt.Sprintf("unsupported OS: %s", runtime.GOOS),
			Hint:    "use darwin/linux/windows for connector launch",
		}
	}
}

func launchTerminalCommandMacOS(ctx context.Context, command string, logger *slog.Logger) error {
	script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script "%s"
end tell`, escapeAppleScript(command))
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isNonFatalAppleScriptTimeout(string(out), err) {
			logger.Warn("Terminal launch reported timeout but command was likely dispatched", "output", strings.TrimSpace(string(out)))
			return nil
		}
		logger.Error("Terminal launch failed", "command", "osascript", "output", string(out), "error", err)
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open Terminal.app for launch command",
			Hint:    "verify osascript permissions and Terminal availability",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	logger.Debug("Terminal launched successfully", "command", "osascript")
	return nil
}

func launchTerminalCommandITerm(ctx context.Context, command string, logger *slog.Logger) error {
	script := fmt.Sprintf(`tell application "iTerm"
	activate
	create window with default profile command "%s"
end tell`, escapeAppleScript(command))
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isNonFatalAppleScriptTimeout(string(out), err) {
			logger.Warn("iTerm2 launch reported timeout but command was likely dispatched", "output", strings.TrimSpace(string(out)))
			return nil
		}
		logger.Error("iTerm2 launch failed", "command", "osascript", "output", string(out), "error", err)
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open iTerm2 for launch command",
			Hint:    "verify iTerm2 is installed and osascript permissions are granted",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	logger.Debug("iTerm2 launched successfully", "command", "osascript")
	return nil
}

func isNonFatalAppleScriptTimeout(output string, err error) bool {
	if err == nil {
		return false
	}
	joined := strings.ToLower(strings.TrimSpace(output + " " + err.Error()))
	return strings.Contains(joined, "appleevent timed out") || strings.Contains(joined, "error -1712")
}

func launchLinuxTerminalByName(ctx context.Context, command, terminal string, logger *slog.Logger) error {
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
	logger.Debug("Linux terminal launched", "terminal", terminal, "command", args[0])
	return nil
}

func launchTerminalCommandLinux(ctx context.Context, command string, logger *slog.Logger) error {
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
			logger.Debug("Linux terminal launched", "terminal", args[0])
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

func launchTerminalCommandWindows(ctx context.Context, command string, logger *slog.Logger) error {
	cmd := exec.CommandContext(ctx, "cmd", "/C", "start", "", "cmd", "/K", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("Windows terminal launch failed", "command", "cmd", "output", string(out), "error", err)
		return &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open terminal",
			Hint:    "verify cmd.exe is available and terminal launching is permitted",
			Details: strings.TrimSpace(string(out)),
			Cause:   err,
		}
	}
	logger.Debug("Windows terminal launched", "command", "cmd")
	return nil
}

func launchWindows(ctx context.Context, req Request, puttyPath string, logger *slog.Logger) (ShellLaunchDiagnostics, error) {
	resolvedPuTTY, err := resolvePuTTYPath(strings.TrimSpace(puttyPath))
	if err != nil {
		return ShellLaunchDiagnostics{}, err
	}
	target := proxyUsernameForShell(req.Launch) + "@" + req.Launch.ProxyHost
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
		return ShellLaunchDiagnostics{}, &LaunchError{
			Code:    "terminal_launch_failed",
			Message: "failed to open PuTTY for shell launch",
			Hint:    "verify ACCESSD_CONNECTOR_PUTTY_PATH or install putty in PATH",
			Details: fmt.Sprintf("command: %s", sanitizeCommandArgs([]string{resolvedPuTTY, "-ssh", target, "-P", strconv.Itoa(req.Launch.ProxyPort)})),
			Cause:   err,
		}
	}
	logger.Debug("PuTTY launched", "path", resolvedPuTTY, "target", target)
	return ShellLaunchDiagnostics{ResolvedPath: resolvedPuTTY}, nil
}

func shellCommand(req Request) (string, error) {
	tokenPath, err := prepareLaunchTokenFile(req.Launch.Token)
	if err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", &LaunchError{
			Code:    "shell_launch_setup_failed",
			Message: "failed to resolve connector executable path for shell launch",
			Hint:    "restart connector from a local binary path",
			Cause:   err,
		}
	}
	proxyUsername := proxyUsernameForShell(req.Launch)
	upstreamUsername := upstreamUsernameForShell(req.Launch)
	displayAsset := strings.TrimSpace(req.Launch.TargetAssetName)
	if displayAsset == "" {
		displayAsset = strings.TrimSpace(req.AssetName)
	}
	displayHost := strings.TrimSpace(req.Launch.TargetHost)
	hostKeyMode := defaultBridgeHostKeyMode()
	knownHostsPath := defaultBridgeKnownHostsFile()
	command := fmt.Sprintf(
		"%s bridge-shell --host %s --port %d --username %s --session-id %s --asset-name %s --target-host %s --upstream-username %s --token-file %s --hostkey-mode %s --known-hosts-file %s",
		shellEscape(executable),
		shellEscape(req.Launch.ProxyHost),
		req.Launch.ProxyPort,
		shellEscape(proxyUsername),
		shellEscape(strings.TrimSpace(req.SessionID)),
		shellEscape(displayAsset),
		shellEscape(displayHost),
		shellEscape(upstreamUsername),
		shellEscape(tokenPath),
		shellEscape(hostKeyMode),
		shellEscape(knownHostsPath),
	)
	return command, nil
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
		if redisCLIShouldUseInsecureTLS(req) {
			parts = append(parts, "--insecure")
		}
	}

	title := fmt.Sprintf("AccessD redis session %s for %s", strings.TrimSpace(req.SessionID), strings.TrimSpace(req.AssetName))
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
		if redisCLIShouldUseInsecureTLS(req) {
			parts = append(parts, "--insecure")
		}
	}
	redisCmd := strings.Join(parts, " ")
	return fmt.Sprintf(
		"echo AccessD redis session %s for %s && set REDISCLI_AUTH=%s && %s && set REDISCLI_AUTH= && echo. && echo redis-cli exited; keep this window open for review.",
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
		if redisCLIShouldUseInsecureTLS(req) {
			parts = append(parts, "--insecure")
		}
	}
	parts = append(parts, "(auth via REDISCLI_AUTH)")
	return strings.Join(parts, " ")
}

func redisCLIShouldUseInsecureTLS(req RedisRequest) bool {
	if req.Launch.InsecureSkipVerifyTLS {
		return true
	}
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_REDIS_TLS_AUTO_INSECURE")))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func filezillaTarget(req SFTPRequest) string {
	user := urlEscape(proxyUsernameForSFTP(req.Launch))
	pass := urlEscape(strings.TrimSpace(req.Launch.Password))
	host := strings.TrimSpace(req.Launch.Host)
	path := strings.TrimSpace(req.Launch.Path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("sftp://%s:%s@%s:%d%s", user, pass, host, req.Launch.Port, path)
}

func filezillaArgs(target, displaySiteName string) []string {
	name := strings.TrimSpace(displaySiteName)
	if name == "" {
		return []string{target}
	}
	siteRef, err := upsertFileZillaSite(name, target)
	if err != nil {
		return []string{target}
	}
	return []string{"-c", siteRef}
}

type fileZillaSiteFile struct {
	XMLName  xml.Name         `xml:"FileZilla3"`
	Version  string           `xml:"version,attr,omitempty"`
	Platform string           `xml:"platform,attr,omitempty"`
	Servers  fileZillaServers `xml:"Servers"`
}

type fileZillaServers struct {
	Server []fileZillaServer `xml:"Server"`
}

type fileZillaServer struct {
	Host      string         `xml:"Host"`
	Port      int            `xml:"Port"`
	Protocol  int            `xml:"Protocol"`
	Type      int            `xml:"Type"`
	User      string         `xml:"User"`
	Pass      *fileZillaPass `xml:"Pass,omitempty"`
	Logontype int            `xml:"Logontype"`
	// Force a single connection so FileZilla does not open extra
	// transfer connections that can outlive short-lived launch tokens.
	MaximumMultipleConnections int               `xml:"MaximumMultipleConnections,omitempty"`
	EncodingType               string            `xml:"EncodingType,omitempty"`
	BypassProxy                int               `xml:"BypassProxy"`
	Name                       string            `xml:"Name"`
	SyncBrowsing               int               `xml:"SyncBrowsing,omitempty"`
	DirectoryComparison        int               `xml:"DirectoryComparison,omitempty"`
	RemoteDir                  string            `xml:"RemoteDir,omitempty"`
	Extra                      []fileZillaAnyXML `xml:",any"`
}

type fileZillaPass struct {
	Encoding string `xml:"encoding,attr,omitempty"`
	Value    string `xml:",chardata"`
}

type fileZillaAnyXML struct {
	XMLName xml.Name
	Value   string `xml:",innerxml"`
}

func upsertFileZillaSite(displayName, target string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "sftp" {
		return "", fmt.Errorf("filezilla site target must use sftp")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("filezilla site target host is required")
	}
	port := 22
	if p := strings.TrimSpace(u.Port()); p != "" {
		val, parseErr := strconv.Atoi(p)
		if parseErr != nil || val <= 0 {
			return "", fmt.Errorf("filezilla site target port is invalid")
		}
		port = val
	}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("filezilla site target credentials are required")
	}
	remoteDir := strings.TrimSpace(u.EscapedPath())

	path, err := resolveFileZillaSiteManagerPath()
	if err != nil {
		return "", err
	}
	doc, err := readFileZillaSiteFile(path)
	if err != nil {
		return "", err
	}
	name := sanitizeFileZillaSiteName(displayName)
	entry := fileZillaServer{
		Host:                       host,
		Port:                       port,
		Protocol:                   1,
		Type:                       0,
		User:                       username,
		Pass:                       &fileZillaPass{Encoding: "base64", Value: base64.StdEncoding.EncodeToString([]byte(password))},
		Logontype:                  1,
		MaximumMultipleConnections: 1,
		EncodingType:               "Auto",
		BypassProxy:                0,
		Name:                       name,
		SyncBrowsing:               0,
		DirectoryComparison:        0,
		RemoteDir:                  remoteDir,
	}
	replaced := false
	for idx := range doc.Servers.Server {
		if strings.EqualFold(strings.TrimSpace(doc.Servers.Server[idx].Name), name) {
			doc.Servers.Server[idx] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Servers.Server = append(doc.Servers.Server, entry)
	}
	if err := writeFileZillaSiteFile(path, doc); err != nil {
		return "", err
	}
	return "0/" + name, nil
}

func resolveFileZillaSiteManagerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	paths := []string{
		filepath.Join(home, ".config", "filezilla", "sitemanager.xml"),
		filepath.Join(home, "Library", "Application Support", "FileZilla", "sitemanager.xml"),
		filepath.Join(home, ".filezilla", "sitemanager.xml"),
	}
	for _, p := range paths {
		if _, statErr := os.Stat(p); statErr == nil {
			return p, nil
		}
	}
	return paths[0], nil
}

func readFileZillaSiteFile(path string) (fileZillaSiteFile, error) {
	doc := fileZillaSiteFile{Version: "3", Servers: fileZillaServers{Server: []fileZillaServer{}}}
	blob, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return fileZillaSiteFile{}, err
	}
	if len(strings.TrimSpace(string(blob))) == 0 {
		return doc, nil
	}
	if unmarshalErr := xml.Unmarshal(blob, &doc); unmarshalErr != nil {
		return fileZillaSiteFile{}, unmarshalErr
	}
	if doc.Servers.Server == nil {
		doc.Servers.Server = []fileZillaServer{}
	}
	return doc, nil
}

func writeFileZillaSiteFile(path string, doc fileZillaSiteFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	blob, err := xml.MarshalIndent(doc, "", "\t")
	if err != nil {
		return err
	}
	content := append([]byte(xml.Header), blob...)
	content = append(content, '\n')
	return os.WriteFile(path, content, 0o600)
}

func sanitizeFileZillaSiteName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "AccessD Session"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", "\n", " ", "\r", " ", "\t", " ")
	clean = replacer.Replace(clean)
	return strings.TrimSpace(clean)
}

func sftpSiteDisplayName(req SFTPRequest) string {
	asset := strings.TrimSpace(req.Launch.TargetAssetName)
	if asset == "" {
		asset = strings.TrimSpace(req.AssetName)
	}
	user := strings.TrimSpace(req.Launch.UpstreamUsername)
	if user == "" {
		user = strings.TrimSpace(req.Launch.Username)
	}
	host := strings.TrimSpace(req.Launch.TargetHost)
	if asset == "" && user == "" {
		return ""
	}
	name := asset
	if name == "" {
		name = host
	}
	if user != "" {
		name += " - " + user
	}
	if host != "" && !strings.EqualFold(host, asset) {
		name += " (" + host + ")"
	}
	return strings.TrimSpace(name)
}

func winscpTarget(req SFTPRequest) string {
	return filezillaTarget(req)
}

func proxyUsernameForShell(payload Shell) string {
	if v := strings.TrimSpace(payload.ProxyUsername); v != "" {
		return v
	}
	if v := strings.TrimSpace(payload.Username); v != "" {
		return v
	}
	return "accessd"
}

func upstreamUsernameForShell(payload Shell) string {
	if v := strings.TrimSpace(payload.UpstreamUsername); v != "" {
		return v
	}
	if v := strings.TrimSpace(payload.Username); v != "" {
		return v
	}
	return proxyUsernameForShell(payload)
}

func proxyUsernameForSFTP(payload SFTPPayload) string {
	if v := strings.TrimSpace(payload.ProxyUsername); v != "" {
		return v
	}
	if v := strings.TrimSpace(payload.Username); v != "" {
		return v
	}
	return "accessd"
}

func targetIdentity(assetName, payloadAssetName, targetHost string) string {
	asset := strings.TrimSpace(payloadAssetName)
	if asset == "" {
		asset = strings.TrimSpace(assetName)
	}
	host := strings.TrimSpace(targetHost)
	if asset == "" {
		return host
	}
	if host == "" || strings.EqualFold(asset, host) {
		return asset
	}
	return asset + " (" + host + ")"
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
			Hint:    "verify ACCESSD_CONNECTOR_PUTTY_PATH points to a valid PuTTY executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "putty_not_installed",
		Message: "failed to open PuTTY for shell launch",
		Hint:    "install PuTTY or set ACCESSD_CONNECTOR_PUTTY_PATH",
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
			Hint:    "verify ACCESSD_CONNECTOR_WINSCP_PATH points to a valid WinSCP executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "winscp_not_installed",
		Message: "failed to launch WinSCP",
		Hint:    "install WinSCP or set ACCESSD_CONNECTOR_WINSCP_PATH",
		Cause:   err,
	}
}

func resolveFileZillaPath(configured string) (string, error) {
	fallbacks := []string{"filezilla"}
	switch runtime.GOOS {
	case "darwin":
		fallbacks = append(fallbacks,
			"/Applications/FileZilla.app/Contents/MacOS/filezilla",
			"/Applications/FileZilla.app/Contents/MacOS/FileZilla",
		)
	case "windows":
		fallbacks = append(fallbacks,
			"filezilla.exe",
			`C:\Program Files\FileZilla FTP Client\filezilla.exe`,
			`C:\Program Files (x86)\FileZilla FTP Client\filezilla.exe`,
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
			Hint:    "verify ACCESSD_CONNECTOR_FILEZILLA_PATH points to a valid FileZilla executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "filezilla_not_installed",
		Message: "failed to launch FileZilla",
		Hint:    "install FileZilla or set ACCESSD_CONNECTOR_FILEZILLA_PATH",
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
			Hint:    "verify ACCESSD_CONNECTOR_REDIS_CLI_PATH points to a valid redis-cli executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "redis_cli_not_installed",
		Message: "redis-cli is not installed",
		Hint:    "install redis-cli or set ACCESSD_CONNECTOR_REDIS_CLI_PATH",
		Cause:   err,
	}
}

func resolveDBeaverPath(configured string) (string, error) {
	// macOS: prefer direct binary paths — never return .app bundles.
	if runtime.GOOS == "darwin" {
		directPaths := []string{
			"/Applications/DBeaver.app/Contents/MacOS/dbeaver",
			"/Applications/DBeaver.app/Contents/MacOS/DBeaver",
			"/Applications/DBeaverCE.app/Contents/MacOS/dbeaver",
			"/Applications/DBeaverCE.app/Contents/MacOS/DBeaver",
		}
		for _, path := range directPaths {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	// Windows: Program Files locations
	if runtime.GOOS == "windows" {
		windowsPaths := []string{
			`C:\Program Files\DBeaver\dbeaver.exe`,
			`C:\Program Files\DBeaver\DBeaver.exe`,
			`C:\Program Files (x86)\DBeaver\dbeaver.exe`,
			`C:\Program Files (x86)\DBeaver CE\dbeaver.exe`,
		}
		for _, path := range windowsPaths {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	// Fallback to PATH lookup
	path, fromOverride, err := resolveBinaryPath(configured, []string{"dbeaver", "dbeaver.exe"})
	if err == nil {
		return path, nil
	}
	if fromOverride {
		return "", &LaunchError{
			Code:    "invalid_configured_path",
			Message: "configured DBeaver path is invalid",
			Hint:    "verify ACCESSD_CONNECTOR_DBEAVER_PATH points to a valid DBeaver executable",
			Cause:   err,
		}
	}
	return "", &LaunchError{
		Code:    "dbeaver_not_installed",
		Message: "failed to launch DBeaver",
		Hint:    "install DBeaver or set ACCESSD_CONNECTOR_DBEAVER_PATH",
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

func launchDBeaverMacOS(ctx context.Context, spec, configuredPath string, logger *slog.Logger) (error, string) {
	// Use direct binary execution ONLY — never `open -a` which drops CLI args.
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)

	if trimmed != "" {
		if strings.HasSuffix(strings.ToLower(trimmed), ".app") {
			paths, err := macOSBundleExecutables(trimmed, []string{"dbeaver", "DBeaver"})
			if err != nil {
				return err, "direct_binary"
			}
			for _, bin := range paths {
				attempts = append(attempts, []string{bin, "-con", spec})
			}
		} else {
			attempts = append(attempts, []string{trimmed, "-con", spec})
		}
	} else {
		attempts = append(attempts,
			[]string{"/Applications/DBeaver.app/Contents/MacOS/dbeaver", "-con", spec},
			[]string{"/Applications/DBeaver.app/Contents/MacOS/DBeaver", "-con", spec},
			[]string{"/Applications/DBeaverCE.app/Contents/MacOS/dbeaver", "-con", spec},
			[]string{"/Applications/DBeaverCE.app/Contents/MacOS/DBeaver", "-con", spec},
			[]string{"dbeaver", "-con", spec},
		)
	}

	if logger != nil {
		for _, a := range attempts {
			logger.Info("dbeaver launch: trying direct binary", "binary", a[0], "args", sanitizeCommandArgs(a[1:]))
		}
	}

	if err := launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "dbeaver_launch_failed",
		MissingCode:   "dbeaver_not_installed",
		BaseErr:       "failed to launch DBeaver on macOS",
		Hint:          "ensure DBeaver is installed or set ACCESSD_CONNECTOR_DBEAVER_PATH to a valid binary path",
		ConfiguredEnv: "ACCESSD_CONNECTOR_DBEAVER_PATH",
	}); err != nil {
		return err, "direct_binary"
	}
	return nil, "direct_binary"
}

func launchFileZillaMacOS(ctx context.Context, configuredPath, target, displaySiteName string, logger *slog.Logger) (error, string) {
	// Use direct binary execution ONLY — never `open -a` which may drop arguments.
	attempts := [][]string{}
	trimmed := strings.TrimSpace(configuredPath)

	if trimmed == "" {
		trimmed = "/Applications/FileZilla.app"
	}

	if strings.HasSuffix(strings.ToLower(trimmed), ".app") {
		paths, err := macOSBundleExecutables(trimmed, []string{"filezilla", "FileZilla"})
		if err != nil {
			return err, "direct_binary"
		}
		args := filezillaArgs(target, displaySiteName)
		for _, bin := range paths {
			attempts = append(attempts, append([]string{bin}, args...))
		}
	} else {
		args := filezillaArgs(target, displaySiteName)
		attempts = append(attempts, append([]string{trimmed}, args...))
	}

	if logger != nil {
		for _, a := range attempts {
			logger.Info("filezilla launch: trying direct binary",
				"binary", a[0],
				"args", sanitizeCommandArgs(a[1:]),
				"protocol", "sftp")
		}
	}

	if err := launchFirstAvailable(ctx, attempts, launchAttemptOptions{
		Code:          "sftp_launch_failed",
		MissingCode:   "filezilla_not_installed",
		BaseErr:       "failed to launch FileZilla",
		Hint:          "ensure FileZilla is installed or set ACCESSD_CONNECTOR_FILEZILLA_PATH to a valid binary path",
		ConfiguredEnv: "ACCESSD_CONNECTOR_FILEZILLA_PATH",
	}); err != nil {
		return err, "direct_binary"
	}
	return nil, "direct_binary"
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

func launchDBeaverLinux(ctx context.Context, spec, configuredPath string, logger *slog.Logger) error {
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
		Hint:          "ensure dbeaver or dbeaver-ce is installed, or set ACCESSD_CONNECTOR_DBEAVER_PATH",
		ConfiguredEnv: "ACCESSD_CONNECTOR_DBEAVER_PATH",
	})
}

func launchDBeaverWindows(ctx context.Context, spec, configuredPath string, logger *slog.Logger) error {
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
		Hint:          "ensure DBeaver is installed or set ACCESSD_CONNECTOR_DBEAVER_PATH",
		ConfiguredEnv: "ACCESSD_CONNECTOR_DBEAVER_PATH",
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
		bin := args[0]
		// For direct binary paths (not "open"), check if the binary exists
		// before trying to run it.
		if bin != "open" && bin != "cmd" {
			if _, lookErr := lookupBinary(bin); lookErr != nil {
				if errors.Is(lookErr, exec.ErrNotFound) {
					errs = append(errs, fmt.Sprintf("%s: not found", sanitizeCommandArgs(args)))
					continue
				}
			}
		}
		allMissing = false

		// Do not tie GUI child process lifetime to HTTP request cancellation.
		// Connector handlers return quickly, and canceling request context would
		// terminate launched apps immediately after launch.
		procCtx := context.WithoutCancel(ctx)
		if procCtx == nil {
			procCtx = context.Background()
		}
		cmd := exec.CommandContext(procCtx, args[0], args[1:]...)
		if startErr := cmd.Start(); startErr != nil {
			if errors.Is(startErr, exec.ErrNotFound) {
				allMissing = true
			}
			errs = append(errs, fmt.Sprintf("%s: %v", sanitizeCommandArgs(args), startErr))
			continue
		}

		// Probe briefly: if the process exits quickly with an error, it
		// failed to launch. If it's still running after the probe window,
		// it started successfully (GUI app).
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return nil // exited cleanly (e.g. "open -a" returns 0)
			}
			errs = append(errs, fmt.Sprintf("%s: %v", sanitizeCommandArgs(args), err))
		case <-time.After(2 * time.Second):
			return nil // still running — GUI app launched successfully
		}
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
	return dbeaverConnectionSpecWithNameSuffix(req, "")
}

func dbeaverConnectionSpecWithNameSuffix(req DBeaverRequest, suffix string) string {
	engine := normalizeEngine(req.Launch.Engine)
	parts := []string{
		"driver=" + specValue(engine),
		"host=" + specValue(req.Launch.Host),
		"port=" + strconv.Itoa(req.Launch.Port),
		"user=" + specValue(req.Launch.Username),
		"connect=true",
		"openConsole=true",
		"savePassword=true",
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
	displayName := strings.TrimSpace(req.Launch.TargetAssetName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.AssetName)
	}
	displayUser := strings.TrimSpace(req.Launch.UpstreamUsername)
	if displayUser == "" {
		displayUser = strings.TrimSpace(req.Launch.Username)
	}
	displayHost := strings.TrimSpace(req.Launch.TargetHost)
	if displayName != "" {
		label := displayName
		if displayUser != "" {
			label += " - " + displayUser
		}
		if displayHost != "" {
			label += " (" + displayHost + ")"
		}
		if sessionSuffix := shortSessionLabel(req.SessionID); sessionSuffix != "" {
			label += " [" + sessionSuffix + "]"
		}
		if extra := strings.TrimSpace(suffix); extra != "" {
			label += " {" + extra + "}"
		}
		parts = append(parts, "name="+specValue(label))
	}
	return strings.Join(parts, "|")
}

func shortSessionLabel(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
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

func prepareLaunchTokenFile(token string) (string, error) {
	tok, err := os.CreateTemp("", "accessd-launch-token-*")
	if err != nil {
		return "", &LaunchError{
			Code:    "temp_material_create_failed",
			Message: "failed to create temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   err,
		}
	}
	tokenPath := tok.Name()
	cleanup := func() {
		_ = os.Remove(tokenPath)
	}
	if _, writeErr := tok.WriteString(strings.TrimSpace(token) + "\n"); writeErr != nil {
		_ = tok.Close()
		cleanup()
		return "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to write temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   writeErr,
		}
	}
	if closeErr := tok.Close(); closeErr != nil {
		cleanup()
		return "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to persist temporary token file",
			Hint:    "check local temporary directory permissions and free space",
			Cause:   closeErr,
		}
	}
	if chmodErr := os.Chmod(tokenPath, 0o600); chmodErr != nil {
		cleanup()
		return "", &LaunchError{
			Code:    "temp_material_write_failed",
			Message: "failed to secure temporary token file",
			Hint:    "check local temporary directory permissions",
			Cause:   chmodErr,
		}
	}
	return tokenPath, nil
}

func shellEscape(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'"'"'`) + "'"
}

func escapeAppleScript(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

// activateMacOSApp brings a macOS application to the foreground.
func activateMacOSApp(name string) error {
	script := fmt.Sprintf(`tell application "%s" to activate`, escapeAppleScript(name))
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

func urlEscape(v string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		" ", "%20",
		"+", "%2B",
		"@", "%40",
		":", "%3A",
		"/", "%2F",
		"?", "%3F",
		"#", "%23",
		"&", "%26",
		"=", "%3D",
		";", "%3B",
	)
	return replacer.Replace(v)
}
