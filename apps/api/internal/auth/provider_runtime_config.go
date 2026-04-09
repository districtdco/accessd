package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/districtd/pam/api/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func resolveAuthConfigFromAdmin(ctx context.Context, pool *pgxpool.Pool, base config.AuthConfig) (config.AuthConfig, error) {
	const q = `
SELECT
	provider_mode,
	enabled,
	host,
	port,
	url,
	base_dn,
	bind_dn,
	bind_password,
	user_search_filter,
	username_attribute,
	display_name_attribute,
	surname_attribute,
	email_attribute,
	ssh_key_attribute,
	avatar_attribute,
	group_search_base_dn,
	group_search_filter,
	group_name_attribute,
	group_role_mapping,
	ca_cert_pem,
	use_tls,
	start_tls,
	insecure_skip_verify
FROM ldap_settings
WHERE id = 1
LIMIT 1;`

	var providerMode string
	var enabled bool
	var host string
	var port int
	var ldapURL string
	var baseDN string
	var bindDN string
	var bindPassword string
	var userFilter string
	var usernameAttr string
	var displayNameAttr string
	var surnameAttr string
	var emailAttr string
	var sshKeyAttr string
	var avatarAttr string
	var groupBaseDN string
	var groupFilter string
	var groupNameAttr string
	var groupRoleMapping string
	var caCertPEM string
	var useTLS bool
	var startTLS bool
	var insecureSkipVerify bool

	err := pool.QueryRow(ctx, q).Scan(
		&providerMode,
		&enabled,
		&host,
		&port,
		&ldapURL,
		&baseDN,
		&bindDN,
		&bindPassword,
		&userFilter,
		&usernameAttr,
		&displayNameAttr,
		&surnameAttr,
		&emailAttr,
		&sshKeyAttr,
		&avatarAttr,
		&groupBaseDN,
		&groupFilter,
		&groupNameAttr,
		&groupRoleMapping,
		&caCertPEM,
		&useTLS,
		&startTLS,
		&insecureSkipVerify,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			base.ProviderMode = "local"
			return base, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			base.ProviderMode = "local"
			return base, nil
		}
		return base, fmt.Errorf("query ldap_settings: %w", err)
	}

	resolved := base
	mode := strings.ToLower(strings.TrimSpace(providerMode))
	switch mode {
	case "", "local", "ldap", "hybrid":
	default:
		mode = "local"
	}
	if !enabled {
		mode = "local"
	}
	if mode == "" {
		mode = "local"
	}
	resolved.ProviderMode = mode

	resolved.LDAP.URL = strings.TrimSpace(ldapURL)
	resolved.LDAP.Host = strings.TrimSpace(host)
	if port <= 0 {
		port = 389
	}
	resolved.LDAP.Port = port
	resolved.LDAP.BaseDN = strings.TrimSpace(baseDN)
	resolved.LDAP.BindDN = strings.TrimSpace(bindDN)
	resolved.LDAP.BindPassword = strings.TrimSpace(bindPassword)
	resolved.LDAP.UserSearchFilter = strings.TrimSpace(userFilter)
	if resolved.LDAP.UserSearchFilter == "" {
		resolved.LDAP.UserSearchFilter = "(&(objectClass=user)({{username_attr}}={{username}}))"
	}
	resolved.LDAP.UsernameAttribute = strings.TrimSpace(usernameAttr)
	if resolved.LDAP.UsernameAttribute == "" {
		resolved.LDAP.UsernameAttribute = "sAMAccountName"
	}
	resolved.LDAP.DisplayNameAttribute = strings.TrimSpace(displayNameAttr)
	if resolved.LDAP.DisplayNameAttribute == "" {
		resolved.LDAP.DisplayNameAttribute = "displayName"
	}
	resolved.LDAP.SurnameAttribute = strings.TrimSpace(surnameAttr)
	if resolved.LDAP.SurnameAttribute == "" {
		resolved.LDAP.SurnameAttribute = "sn"
	}
	resolved.LDAP.EmailAttribute = strings.TrimSpace(emailAttr)
	if resolved.LDAP.EmailAttribute == "" {
		resolved.LDAP.EmailAttribute = "mail"
	}
	resolved.LDAP.SSHKeyAttribute = strings.TrimSpace(sshKeyAttr)
	if resolved.LDAP.SSHKeyAttribute == "" {
		resolved.LDAP.SSHKeyAttribute = "SshPublicKey"
	}
	resolved.LDAP.AvatarAttribute = strings.TrimSpace(avatarAttr)
	if resolved.LDAP.AvatarAttribute == "" {
		resolved.LDAP.AvatarAttribute = "jpegPhoto"
	}
	resolved.LDAP.GroupSearchBaseDN = strings.TrimSpace(groupBaseDN)
	resolved.LDAP.GroupSearchFilter = strings.TrimSpace(groupFilter)
	if resolved.LDAP.GroupSearchFilter == "" {
		resolved.LDAP.GroupSearchFilter = "(&(objectClass=group)(member={{user_dn}}))"
	}
	resolved.LDAP.GroupNameAttribute = strings.TrimSpace(groupNameAttr)
	if resolved.LDAP.GroupNameAttribute == "" {
		resolved.LDAP.GroupNameAttribute = "cn"
	}
	resolved.LDAP.GroupRoleMappingRaw = strings.TrimSpace(groupRoleMapping)
	resolved.LDAP.CACertPEM = strings.TrimSpace(caCertPEM)
	resolved.LDAP.UseTLS = useTLS
	resolved.LDAP.StartTLS = startTLS
	resolved.LDAP.InsecureSkipVerify = insecureSkipVerify
	resolved.LDAP.CACertFile = ""

	return resolved, nil
}
