package discovery

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ConnectorConfig represents the optional config file for app path overrides
// and terminal preferences. Loaded from ~/.accessd-connector/config.yaml or
// ACCESSD_CONNECTOR_CONFIG_FILE.
type ConnectorConfig struct {
	Apps     map[string]string `yaml:"apps"`
	Terminal TerminalConfig    `yaml:"terminal"`

	// loadedFrom records which file this config was read from, for diagnostics.
	loadedFrom string
}

// TerminalConfig holds per-OS terminal emulator preferences.
type TerminalConfig struct {
	MacOS   string `yaml:"macos"`
	Linux   string `yaml:"linux"`
	Windows string `yaml:"windows"`
}

// appPath returns the configured path for an app, or empty string if not set.
func (c *ConnectorConfig) appPath(app AppName) string {
	if c == nil || c.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Apps[string(app)])
}

// terminalPref returns the terminal preference for the given OS.
func (c *ConnectorConfig) terminalPref(goos string) string {
	if c == nil {
		return ""
	}
	switch goos {
	case "darwin":
		return strings.TrimSpace(c.Terminal.MacOS)
	case "linux":
		return strings.TrimSpace(c.Terminal.Linux)
	case "windows":
		return strings.TrimSpace(c.Terminal.Windows)
	default:
		return ""
	}
}

// LoadedFrom returns the file path this config was loaded from.
func (c *ConnectorConfig) LoadedFrom() string {
	if c == nil {
		return ""
	}
	return c.loadedFrom
}

// LoadConfig loads the connector config file. It checks:
// 1. ACCESSD_CONNECTOR_CONFIG_FILE env var
// 2. Default OS-specific path (~/.accessd-connector/config.yaml)
//
// Returns nil (not an error) if no config file exists. Returns an error only
// if the file exists but cannot be parsed.
func LoadConfig() (*ConnectorConfig, error) {
	// 1. Explicit env override
	if envPath := strings.TrimSpace(os.Getenv("ACCESSD_CONNECTOR_CONFIG_FILE")); envPath != "" {
		cfg, err := loadConfigFromFile(envPath)
		if err != nil {
			return nil, fmt.Errorf("ACCESSD_CONNECTOR_CONFIG_FILE=%s: %w", envPath, err)
		}
		if cfg != nil {
			log.Printf("discovery: loaded config from %s (via ACCESSD_CONNECTOR_CONFIG_FILE)", envPath)
		}
		return cfg, nil
	}

	// 2. Default path
	defaultPath := DefaultConfigPath()
	if defaultPath == "" {
		return nil, nil
	}
	cfg, err := loadConfigFromFile(defaultPath)
	if err != nil {
		// If file doesn't exist at default path, that's fine
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("config file %s: %w", defaultPath, err)
	}
	if cfg != nil {
		log.Printf("discovery: loaded config from %s", defaultPath)
	}
	return cfg, nil
}

// EnsureDefaultConfigFile creates a starter config file at the default path
// if it does not exist. It does not overwrite existing files.
func EnsureDefaultConfigFile() (string, bool, error) {
	path := DefaultConfigPath()
	if strings.TrimSpace(path) == "" {
		return "", false, nil
	}
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return path, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, false, fmt.Errorf("create config directory: %w", err)
	}
	content := strings.TrimSpace(`# AccessD Connector config
# Auto-generated on first connector startup.
# You can override app paths and terminal preference here.
#
# apps:
#   dbeaver: "/Applications/DBeaver.app"
#   filezilla: "/Applications/FileZilla.app"
#   redis_cli: "/usr/local/bin/redis-cli"
#
# terminal:
#   macos: "terminal"   # terminal|iterm
#   linux: "auto"       # auto|gnome-terminal|konsole|xfce4-terminal|xterm
#   windows: "cmd"      # cmd|powershell|wt
`) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return path, false, fmt.Errorf("write default config: %w", err)
	}
	return path, true, nil
}

// loadConfigFromFile reads and parses a config file. Returns nil,nil if file
// does not exist. We use a simple key-value parser to avoid external YAML
// dependencies, keeping the connector dependency-free.
func loadConfigFromFile(path string) (*ConnectorConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, err
	}
	defer f.Close()

	cfg := &ConnectorConfig{
		Apps:       make(map[string]string),
		loadedFrom: path,
	}

	// Simple YAML-subset parser: handles flat "section:" headers and
	// "  key: value" entries. This avoids adding a YAML dependency.
	scanner := bufio.NewScanner(f)
	section := ""
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Detect section headers (no leading whitespace, ends with colon, no value)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed[:len(trimmed)-1], ":") {
				section = strings.TrimSuffix(trimmed, ":")
				continue
			}
		}

		// Parse key: value pairs (indented under a section)
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && strings.Contains(trimmed, ":") {
			colonIdx := strings.Index(trimmed, ":")
			key := strings.TrimSpace(trimmed[:colonIdx])
			value := strings.TrimSpace(trimmed[colonIdx+1:])
			// Strip surrounding quotes
			value = stripQuotes(value)

			switch section {
			case "apps":
				if key != "" && value != "" {
					cfg.Apps[key] = value
				}
			case "terminal":
				switch key {
				case "macos":
					cfg.Terminal.MacOS = value
				case "linux":
					cfg.Terminal.Linux = value
				case "windows":
					cfg.Terminal.Windows = value
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return cfg, nil
}

// stripQuotes removes surrounding single or double quotes from a string.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
