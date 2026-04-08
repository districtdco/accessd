package sessions

import (
	"strings"
)

// AuditActionSemantics provides protocol-aware audit action labels that are
// user-facing while preserving internal low-level fidelity.
//
// The design preserves:
// - Internal low-level audit records (e.g., credential_usage, proxy_upstream_auth)
// - Protocol/session context attached to upstream auth events
// - User-facing protocol-aware labels in the audit UI

// ProtocolAwareAction maps internal action codes to user-facing protocol-aware labels.
// This is used for display purposes in the audit UI.
func ProtocolAwareAction(action, protocol, launchType string) string {
	trimmed := strings.TrimSpace(action)
	proto := strings.TrimSpace(protocol)
	ltype := strings.TrimSpace(launchType)

	// Map internal actions to protocol-aware display labels
	switch trimmed {
	case "shell":
		return "shell_session"
	case "sftp":
		return "sftp_session"
	case "dbeaver":
		return "database_session"
	case "redis":
		return "redis_session"
	default:
		// Fallback to protocol-aware naming
		if proto != "" {
			return proto + "_session"
		}
		if ltype != "" {
			return ltype + "_session"
		}
		return trimmed + "_session"
	}
}

// ProtocolAwareActionEnd maps internal end actions to protocol-aware display labels.
func ProtocolAwareActionEnd(action string) string {
	trimmed := strings.TrimSpace(action)
	if trimmed == "" {
		return "session_end"
	}
	// Map to protocol-aware end action
	switch trimmed {
	case "shell":
		return "shell_session_end"
	case "sftp":
		return "sftp_session_end"
	case "dbeaver":
		return "database_session_end"
	case "redis":
		return "redis_session_end"
	default:
		return trimmed + "_end"
	}
}

// CredentialUsageAction maps credential usage stages to protocol-aware labels.
// This preserves the internal stage information while making it user-friendly.
func CredentialUsageAction(credentialType, usageStage, protocol, action string) string {
	_ = credentialType
	stage := strings.TrimSpace(usageStage)
	proto := strings.TrimSpace(protocol)
	act := strings.TrimSpace(action)

	// Build protocol-aware action label
	var baseAction string
	switch act {
	case "shell":
		baseAction = "shell"
	case "sftp":
		baseAction = "sftp"
	case "dbeaver":
		baseAction = "database"
	case "redis":
		baseAction = "redis"
	default:
		if proto != "" {
			baseAction = proto
		} else {
			baseAction = "upstream"
		}
	}

	// Format: {protocol}_{stage}
	return baseAction + "_" + stage
}

// UpstreamAuthAction maps upstream authentication events to protocol-aware labels.
// This replaces generic "proxy_upstream_auth" with protocol-specific labels.
func UpstreamAuthAction(protocol, action string) string {
	_ = action
	proto := strings.TrimSpace(protocol)

	// Map to protocol-aware upstream auth action
	switch proto {
	case ProtocolSSH:
		return "ssh_upstream_auth"
	case ProtocolSFTP:
		return "sftp_upstream_auth"
	case ProtocolDB:
		return "database_upstream_auth"
	case ProtocolRedis:
		return "redis_upstream_auth"
	default:
		return "upstream_auth"
	}
}

// AuditMetadata enriches audit events with protocol/session context.
// This preserves internal fidelity while adding user-facing context.
type AuditMetadata struct {
	// Internal low-level action (preserved for debugging)
	InternalAction string `json:"internal_action,omitempty"`

	// Protocol-aware action for display
	ProtocolAction string `json:"protocol_action,omitempty"`

	// Session context
	SessionType string `json:"session_type,omitempty"` // "shell", "sftp", "database", "redis"
	LaunchType  string `json:"launch_type,omitempty"`  // "shell", "sftp", "dbeaver", "redis"
	Protocol    string `json:"protocol,omitempty"`     // "ssh", "sftp", "database", "redis"
	UpstreamType string `json:"upstream_type,omitempty"` // "ssh_proxy", "sftp_proxy", "db_proxy", "redis_proxy"

	// Credential context
	CredentialType string `json:"credential_type,omitempty"`
	UsageStage     string `json:"usage_stage,omitempty"`

	// Request context
	RequestID string `json:"request_id,omitempty"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

// EnrichAuditMetadata creates enriched audit metadata with protocol context.
func EnrichAuditMetadata(protocol, action, launchType string, extra map[string]any) AuditMetadata {
	proto := strings.TrimSpace(protocol)
	act := strings.TrimSpace(action)
	ltype := strings.TrimSpace(launchType)

	_ = ltype // Used below

	// Determine session type
	var sessionType string
	switch act {
	case "shell":
		sessionType = "shell"
	case "sftp":
		sessionType = "sftp"
	case "dbeaver":
		sessionType = "database"
	case "redis":
		sessionType = "redis"
	default:
		if proto != "" {
			sessionType = proto
		} else {
			sessionType = "unknown"
		}
	}

	// Determine upstream type
	var upstreamType string
	switch proto {
	case ProtocolSSH:
		upstreamType = "ssh_proxy"
	case ProtocolSFTP:
		upstreamType = "sftp_proxy"
	case ProtocolDB:
		upstreamType = "db_proxy"
	case ProtocolRedis:
		upstreamType = "redis_proxy"
	default:
		upstreamType = "unknown"
	}

	metadata := AuditMetadata{
		InternalAction: act,
		ProtocolAction: ProtocolAwareAction(act, proto, ltype),
		SessionType:    sessionType,
		LaunchType:     ltype,
		Protocol:       proto,
		UpstreamType:   upstreamType,
		RecordedAt:     "", // Will be set by caller
	}

	// Copy extra metadata
	if extra != nil {
		if ct, ok := extra["credential_type"].(string); ok {
			metadata.CredentialType = ct
		}
		if us, ok := extra["usage_stage"].(string); ok {
			metadata.UsageStage = us
		}
		if rid, ok := extra["request_id"].(string); ok {
			metadata.RequestID = rid
		}
	}

	return metadata
}