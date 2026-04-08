package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/districtd/pam/api/internal/admin"
	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/auth"
	"github.com/districtd/pam/api/internal/credentials"
)

type AdminHandler struct {
	adminService       *admin.Service
	assetsService      *assets.Service
	credentialsService *credentials.Service
}

type adminUsersResponse struct {
	Items []adminUserResponse `json:"items"`
}

type adminUserResponse struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	Email        string   `json:"email,omitempty"`
	DisplayName  string   `json:"display_name,omitempty"`
	AuthProvider string   `json:"auth_provider"`
	IsActive     bool     `json:"is_active"`
	Roles        []string `json:"roles"`
}

type adminUserDetailResponse struct {
	ID           string               `json:"id"`
	Username     string               `json:"username"`
	Email        string               `json:"email,omitempty"`
	DisplayName  string               `json:"display_name,omitempty"`
	AuthProvider string               `json:"auth_provider"`
	IsActive     bool                 `json:"is_active"`
	Roles        []string             `json:"roles"`
	Groups       []adminGroupResponse `json:"groups"`
}

type adminRolesResponse struct {
	Items []adminRoleResponse `json:"items"`
}

type adminRoleResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type assignRoleRequest struct {
	RoleName string `json:"role_name"`
}

type adminGroupsResponse struct {
	Items []adminGroupResponse `json:"items"`
}

type adminGroupResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
}

type adminGroupMembersResponse struct {
	Items []adminGroupMemberResponse `json:"items"`
}

type adminGroupMemberResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type adminAssetsResponse struct {
	Items []adminAssetResponse `json:"items"`
}

type adminAssetResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	AssetType       string `json:"asset_type"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Endpoint        string `json:"endpoint"`
	GrantCount      int    `json:"grant_count"`
	CredentialCount int    `json:"credential_count"`
}

type adminAssetDetailResponse struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	AssetType   string                   `json:"asset_type"`
	Host        string                   `json:"host"`
	Port        int                      `json:"port"`
	Endpoint    string                   `json:"endpoint"`
	Metadata    map[string]any           `json:"metadata"`
	Credentials []adminCredentialSummary `json:"credentials"`
}

type adminCredentialSummary struct {
	ID              string         `json:"id"`
	CredentialType  string         `json:"credential_type"`
	Username        string         `json:"username,omitempty"`
	Algorithm       string         `json:"algorithm"`
	KeyID           string         `json:"key_id"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	LastRotatedAt   string         `json:"last_rotated_at,omitempty"`
	SecretAvailable bool           `json:"secret_available"`
}

type upsertAssetRequest struct {
	Name      string          `json:"name"`
	AssetType string          `json:"asset_type"`
	Host      string          `json:"host"`
	Port      int             `json:"port"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type upsertCredentialRequest struct {
	Username string         `json:"username,omitempty"`
	Secret   string         `json:"secret"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type adminGrantsResponse struct {
	Items []adminGrantResponse `json:"items"`
}

type adminGrantResponse struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	SubjectName string `json:"subject_name"`
	AssetID     string `json:"asset_id"`
	AssetName   string `json:"asset_name"`
	Action      string `json:"action"`
	Effect      string `json:"effect"`
	CreatedAt   string `json:"created_at"`
}

type addUserGrantRequest struct {
	AssetID string `json:"asset_id"`
	Action  string `json:"action"`
}

type adminEffectiveAccessResponse struct {
	Items []adminEffectiveAccessItemResponse `json:"items"`
}

type adminEffectiveAccessItemResponse struct {
	AssetID   string                               `json:"asset_id"`
	AssetName string                               `json:"asset_name"`
	Actions   []adminEffectiveAccessActionResponse `json:"actions"`
}

type adminEffectiveAccessActionResponse struct {
	Action  string   `json:"action"`
	Sources []string `json:"sources"`
}

type adminLDAPSettingsResponse struct {
	ProviderMode           string `json:"provider_mode"`
	Enabled                bool   `json:"enabled"`
	Host                   string `json:"host"`
	Port                   int    `json:"port"`
	URL                    string `json:"url"`
	BaseDN                 string `json:"base_dn"`
	BindDN                 string `json:"bind_dn"`
	HasBindPassword        bool   `json:"has_bind_password"`
	UserSearchFilter       string `json:"user_search_filter"`
	SyncUserFilter         string `json:"sync_user_filter"`
	UsernameAttribute      string `json:"username_attribute"`
	DisplayNameAttribute   string `json:"display_name_attribute"`
	EmailAttribute         string `json:"email_attribute"`
	GroupSearchBaseDN      string `json:"group_search_base_dn"`
	GroupSearchFilter      string `json:"group_search_filter"`
	GroupNameAttribute     string `json:"group_name_attribute"`
	GroupRoleMapping       string `json:"group_role_mapping"`
	UseTLS                 bool   `json:"use_tls"`
	StartTLS               bool   `json:"start_tls"`
	InsecureSkipVerify     bool   `json:"insecure_skip_verify"`
	DeactivateMissingUsers bool   `json:"deactivate_missing_users"`
	UpdatedBy              string `json:"updated_by,omitempty"`
	UpdatedAt              string `json:"updated_at,omitempty"`
}

type upsertLDAPSettingsRequest struct {
	ProviderMode           string `json:"provider_mode"`
	Enabled                bool   `json:"enabled"`
	Host                   string `json:"host"`
	Port                   int    `json:"port"`
	URL                    string `json:"url"`
	BaseDN                 string `json:"base_dn"`
	BindDN                 string `json:"bind_dn"`
	BindPassword           string `json:"bind_password"`
	KeepExistingPassword   bool   `json:"keep_existing_password"`
	UserSearchFilter       string `json:"user_search_filter"`
	SyncUserFilter         string `json:"sync_user_filter"`
	UsernameAttribute      string `json:"username_attribute"`
	DisplayNameAttribute   string `json:"display_name_attribute"`
	EmailAttribute         string `json:"email_attribute"`
	GroupSearchBaseDN      string `json:"group_search_base_dn"`
	GroupSearchFilter      string `json:"group_search_filter"`
	GroupNameAttribute     string `json:"group_name_attribute"`
	GroupRoleMapping       string `json:"group_role_mapping"`
	UseTLS                 bool   `json:"use_tls"`
	StartTLS               bool   `json:"start_tls"`
	InsecureSkipVerify     bool   `json:"insecure_skip_verify"`
	DeactivateMissingUsers bool   `json:"deactivate_missing_users"`
}

type ldapSyncRunsResponse struct {
	Items []ldapSyncRunResponse `json:"items"`
}

type ldapSyncRunResponse struct {
	ID          int64                        `json:"id"`
	StartedAt   string                       `json:"started_at"`
	CompletedAt string                       `json:"completed_at,omitempty"`
	Status      string                       `json:"status"`
	TriggeredBy string                       `json:"triggered_by,omitempty"`
	Summary     adminLDAPSyncSummaryResponse `json:"summary"`
	Error       string                       `json:"error,omitempty"`
}

type adminLDAPSyncSummaryResponse struct {
	Discovered  int      `json:"discovered"`
	Created     int      `json:"created"`
	Updated     int      `json:"updated"`
	Reactivated int      `json:"reactivated"`
	Unchanged   int      `json:"unchanged"`
	Deactivated int      `json:"deactivated"`
	Samples     []string `json:"samples,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

func NewAdminHandler(
	adminService *admin.Service,
	assetsService *assets.Service,
	credentialsService *credentials.Service,
) *AdminHandler {
	return &AdminHandler{
		adminService:       adminService,
		assetsService:      assetsService,
		credentialsService: credentialsService,
	}
}

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.adminService.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		return
	}

	resp := adminUsersResponse{Items: make([]adminUserResponse, 0, len(users))}
	for _, user := range users {
		resp.Items = append(resp.Items, adminUserResponse{
			ID:           user.ID,
			Username:     user.Username,
			Email:        user.Email,
			DisplayName:  user.DisplayName,
			AuthProvider: user.AuthProvider,
			IsActive:     user.IsActive,
			Roles:        user.Roles,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) GetUserDetail(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	user, err := h.adminService.GetUserDetail(r.Context(), userID)
	if err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get user"})
		return
	}

	resp := adminUserDetailResponse{
		ID:           user.ID,
		Username:     user.Username,
		Email:        user.Email,
		DisplayName:  user.DisplayName,
		AuthProvider: user.AuthProvider,
		IsActive:     user.IsActive,
		Roles:        user.Roles,
		Groups:       make([]adminGroupResponse, 0, len(user.Groups)),
	}
	for _, group := range user.Groups {
		resp.Groups = append(resp.Groups, adminGroupResponse{
			ID:          group.ID,
			Name:        group.Name,
			Description: group.Description,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.adminService.ListRoles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list roles"})
		return
	}

	resp := adminRolesResponse{Items: make([]adminRoleResponse, 0, len(roles))}
	for _, role := range roles {
		resp.Items = append(resp.Items, adminRoleResponse{
			ID:          role.ID,
			Name:        role.Name,
			Description: role.Description,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) AssignRoleToUser(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	var req assignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.adminService.AssignRoleToUser(r.Context(), userID, strings.TrimSpace(req.RoleName)); err != nil {
		switch {
		case errors.Is(err, admin.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, admin.ErrRoleNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "role not found"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) RemoveRoleFromUser(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	roleName := strings.TrimSpace(r.PathValue("roleName"))
	if userID == "" || roleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id and role name are required"})
		return
	}

	if err := h.adminService.RemoveRoleFromUser(r.Context(), userID, roleName); err != nil {
		switch {
		case errors.Is(err, admin.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, admin.ErrRoleNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "role not found"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.adminService.ListGroups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list groups"})
		return
	}

	resp := adminGroupsResponse{Items: make([]adminGroupResponse, 0, len(groups))}
	for _, group := range groups {
		resp.Items = append(resp.Items, adminGroupResponse{
			ID:          group.ID,
			Name:        group.Name,
			Description: group.Description,
			MemberCount: group.MemberCount,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) ListGroupMembers(w http.ResponseWriter, r *http.Request) {
	groupID := strings.TrimSpace(r.PathValue("groupID"))
	if groupID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group id is required"})
		return
	}

	members, err := h.adminService.ListGroupMembers(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, admin.ErrGroupNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := adminGroupMembersResponse{Items: make([]adminGroupMemberResponse, 0, len(members))}
	for _, member := range members {
		resp.Items = append(resp.Items, adminGroupMemberResponse{
			ID:          member.ID,
			Username:    member.Username,
			Email:       member.Email,
			DisplayName: member.DisplayName,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) ListGroupGrants(w http.ResponseWriter, r *http.Request) {
	groupID := strings.TrimSpace(r.PathValue("groupID"))
	if groupID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group id is required"})
		return
	}

	grants, err := h.adminService.ListGroupGrants(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, admin.ErrGroupNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, adminGrantsResponse{Items: mapGrantResponses(grants)})
}

func (h *AdminHandler) ListAssets(w http.ResponseWriter, r *http.Request) {
	assets, err := h.adminService.ListAssets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list assets"})
		return
	}

	resp := adminAssetsResponse{Items: make([]adminAssetResponse, 0, len(assets))}
	for _, asset := range assets {
		resp.Items = append(resp.Items, adminAssetResponse{
			ID:              asset.ID,
			Name:            asset.Name,
			AssetType:       asset.Type,
			Host:            asset.Host,
			Port:            asset.Port,
			Endpoint:        asset.Host + ":" + strconv.Itoa(asset.Port),
			GrantCount:      asset.GrantCount,
			CredentialCount: asset.CredentialCount,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) CreateAsset(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	input, err := decodeUpsertAssetRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	actorID := currentUser.ID
	item, err := h.assetsService.Create(r.Context(), assets.CreateInput{
		Name:         input.Name,
		Type:         input.AssetType,
		Host:         input.Host,
		Port:         input.Port,
		MetadataJSON: input.Metadata,
		CreatedBy:    &actorID,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, mapAssetResponse(item))
}

func (h *AdminHandler) GetAssetDetail(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	if assetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id is required"})
		return
	}

	item, err := h.assetsService.GetByID(r.Context(), assetID)
	if err != nil {
		if errors.Is(err, assets.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	creds, err := h.credentialsService.ListMetadataForAsset(r.Context(), assetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list credentials"})
		return
	}

	writeJSON(w, http.StatusOK, mapAssetDetailResponse(item, creds))
}

func (h *AdminHandler) UpdateAsset(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	if assetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id is required"})
		return
	}

	input, err := decodeUpsertAssetRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	item, err := h.assetsService.Update(r.Context(), assetID, assets.CreateInput{
		Name:         input.Name,
		Type:         input.AssetType,
		Host:         input.Host,
		Port:         input.Port,
		MetadataJSON: input.Metadata,
	})
	if err != nil {
		if errors.Is(err, assets.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, mapAssetResponse(item))
}

func (h *AdminHandler) ListAssetCredentials(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	if assetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id is required"})
		return
	}

	if _, err := h.assetsService.GetByID(r.Context(), assetID); err != nil {
		if errors.Is(err, assets.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	creds, err := h.credentialsService.ListMetadataForAsset(r.Context(), assetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list credentials"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": mapCredentialSummaries(creds),
	})
}

func (h *AdminHandler) UpsertAssetCredential(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	assetID := strings.TrimSpace(r.PathValue("assetID"))
	credentialType := strings.TrimSpace(r.PathValue("credentialType"))
	if assetID == "" || credentialType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id and credential type are required"})
		return
	}

	if _, err := h.assetsService.GetByID(r.Context(), assetID); err != nil {
		if errors.Is(err, assets.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var req upsertCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	metadata, err := marshalJSONMap(req.Metadata)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential metadata must be valid json object"})
		return
	}

	actorID := currentUser.ID
	stored, err := h.credentialsService.Upsert(r.Context(), credentials.CreateInput{
		AssetID:   assetID,
		Type:      credentialType,
		Username:  strings.TrimSpace(req.Username),
		Secret:    req.Secret,
		Metadata:  metadata,
		CreatedBy: &actorID,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.adminService.RecordCredentialUpsertAudit(r.Context(), admin.CredentialAuditInput{
		ActorUserID:    actorID,
		AssetID:        assetID,
		CredentialType: credentialType,
		Username:       strings.TrimSpace(req.Username),
		MetadataKeys:   sortedMapKeys(req.Metadata),
		SourceIP:       normalizeRemoteIP(r.RemoteAddr),
		UserAgent:      strings.TrimSpace(r.UserAgent()),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "credential updated but audit write failed"})
		return
	}

	// return metadata-only confirmation; never echo secrets
	writeJSON(w, http.StatusOK, map[string]any{
		"id":               stored.ID,
		"asset_id":         stored.AssetID,
		"credential_type":  stored.Type,
		"username":         stored.Username,
		"algorithm":        stored.Algorithm,
		"key_id":           stored.KeyID,
		"created_at":       stored.CreatedAt.UTC().Format(time.RFC3339Nano),
		"secret_available": true,
	})
}

func (h *AdminHandler) ListAssetGrants(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	if assetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id is required"})
		return
	}

	grants, err := h.adminService.ListAssetGrants(r.Context(), assetID)
	if err != nil {
		if errors.Is(err, admin.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, adminGrantsResponse{Items: mapGrantResponses(grants)})
}

func (h *AdminHandler) ListUserGrants(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	grants, err := h.adminService.ListUserDirectGrants(r.Context(), userID)
	if err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, adminGrantsResponse{Items: mapGrantResponses(grants)})
}

func (h *AdminHandler) AddUserGrant(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	var req addUserGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	actorID := currentUser.ID
	if err := h.adminService.GrantUserAllow(
		r.Context(),
		userID,
		strings.TrimSpace(req.AssetID),
		strings.TrimSpace(req.Action),
		&actorID,
	); err != nil {
		switch {
		case errors.Is(err, admin.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, admin.ErrAssetNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) RemoveUserGrant(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	action := strings.TrimSpace(r.PathValue("action"))
	if userID == "" || assetID == "" || action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id, asset id, and action are required"})
		return
	}

	if err := h.adminService.RevokeUserAllow(r.Context(), userID, assetID, action); err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) UserEffectiveAccess(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	items, err := h.adminService.UserEffectiveAccess(r.Context(), userID)
	if err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := adminEffectiveAccessResponse{Items: make([]adminEffectiveAccessItemResponse, 0, len(items))}
	for _, item := range items {
		actions := make([]adminEffectiveAccessActionResponse, 0, len(item.Actions))
		for _, action := range item.Actions {
			actions = append(actions, adminEffectiveAccessActionResponse{Action: action.Action, Sources: action.Sources})
		}
		resp.Items = append(resp.Items, adminEffectiveAccessItemResponse{
			AssetID:   item.AssetID,
			AssetName: item.AssetName,
			Actions:   actions,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) GetLDAPSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.adminService.GetLDAPSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load ldap settings"})
		return
	}
	writeJSON(w, http.StatusOK, mapLDAPSettingsResponse(settings))
}

func (h *AdminHandler) UpsertLDAPSettings(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req upsertLDAPSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	settings, err := h.adminService.UpsertLDAPSettings(r.Context(), currentUser.ID, admin.LDAPSettings{
		ProviderMode:           req.ProviderMode,
		Enabled:                req.Enabled,
		Host:                   req.Host,
		Port:                   req.Port,
		URL:                    req.URL,
		BaseDN:                 req.BaseDN,
		BindDN:                 req.BindDN,
		BindPassword:           req.BindPassword,
		UserSearchFilter:       req.UserSearchFilter,
		SyncUserFilter:         req.SyncUserFilter,
		UsernameAttribute:      req.UsernameAttribute,
		DisplayNameAttribute:   req.DisplayNameAttribute,
		EmailAttribute:         req.EmailAttribute,
		GroupSearchBaseDN:      req.GroupSearchBaseDN,
		GroupSearchFilter:      req.GroupSearchFilter,
		GroupNameAttribute:     req.GroupNameAttribute,
		GroupRoleMapping:       req.GroupRoleMapping,
		UseTLS:                 req.UseTLS,
		StartTLS:               req.StartTLS,
		InsecureSkipVerify:     req.InsecureSkipVerify,
		DeactivateMissingUsers: req.DeactivateMissingUsers,
	}, req.KeepExistingPassword)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mapLDAPSettingsResponse(settings))
}

func (h *AdminHandler) TestLDAPConnection(w http.ResponseWriter, r *http.Request) {
	var req upsertLDAPSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	result, err := h.adminService.TestLDAPConnection(r.Context(), admin.LDAPSettings{
		ProviderMode:           req.ProviderMode,
		Enabled:                req.Enabled,
		Host:                   req.Host,
		Port:                   req.Port,
		URL:                    req.URL,
		BaseDN:                 req.BaseDN,
		BindDN:                 req.BindDN,
		BindPassword:           req.BindPassword,
		UserSearchFilter:       req.UserSearchFilter,
		SyncUserFilter:         req.SyncUserFilter,
		UsernameAttribute:      req.UsernameAttribute,
		DisplayNameAttribute:   req.DisplayNameAttribute,
		EmailAttribute:         req.EmailAttribute,
		GroupSearchBaseDN:      req.GroupSearchBaseDN,
		GroupSearchFilter:      req.GroupSearchFilter,
		GroupNameAttribute:     req.GroupNameAttribute,
		GroupRoleMapping:       req.GroupRoleMapping,
		UseTLS:                 req.UseTLS,
		StartTLS:               req.StartTLS,
		InsecureSkipVerify:     req.InsecureSkipVerify,
		DeactivateMissingUsers: req.DeactivateMissingUsers,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AdminHandler) TriggerLDAPSync(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	run, err := h.adminService.TriggerLDAPSync(r.Context(), currentUser.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mapLDAPSyncRunResponse(run))
}

func (h *AdminHandler) ListLDAPSyncRuns(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	runs, err := h.adminService.ListLDAPSyncRuns(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list ldap sync runs"})
		return
	}
	resp := ldapSyncRunsResponse{Items: make([]ldapSyncRunResponse, 0, len(runs))}
	for _, run := range runs {
		resp.Items = append(resp.Items, mapLDAPSyncRunResponse(run))
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeUpsertAssetRequest(r *http.Request) (upsertAssetRequest, error) {
	var req upsertAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return upsertAssetRequest{}, errors.New("invalid json body")
	}

	rawMetadata := bytes.TrimSpace(req.Metadata)
	if len(rawMetadata) == 0 {
		rawMetadata = []byte("{}")
	}
	var metadataObject map[string]any
	if err := json.Unmarshal(rawMetadata, &metadataObject); err != nil {
		return upsertAssetRequest{}, errors.New("asset metadata must be valid json object")
	}
	normalized, _ := json.Marshal(metadataObject)

	req.Name = strings.TrimSpace(req.Name)
	req.AssetType = strings.TrimSpace(req.AssetType)
	req.Host = strings.TrimSpace(req.Host)
	req.Metadata = normalized
	return req, nil
}

func marshalJSONMap(value map[string]any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	blob, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(blob)
	if len(trimmed) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("invalid json")
	}
	return trimmed, nil
}

func mapAssetResponse(item assets.Asset) adminAssetResponse {
	return adminAssetResponse{
		ID:        item.ID,
		Name:      item.Name,
		AssetType: item.Type,
		Host:      item.Host,
		Port:      item.Port,
		Endpoint:  item.Host + ":" + strconv.Itoa(item.Port),
	}
}

func mapAssetDetailResponse(item assets.Asset, creds []credentials.CredentialMetadata) adminAssetDetailResponse {
	metadata := map[string]any{}
	if len(item.MetadataJSON) > 0 && json.Valid(item.MetadataJSON) {
		_ = json.Unmarshal(item.MetadataJSON, &metadata)
	}
	return adminAssetDetailResponse{
		ID:          item.ID,
		Name:        item.Name,
		AssetType:   item.Type,
		Host:        item.Host,
		Port:        item.Port,
		Endpoint:    item.Host + ":" + strconv.Itoa(item.Port),
		Metadata:    metadata,
		Credentials: mapCredentialSummaries(creds),
	}
}

func mapCredentialSummaries(items []credentials.CredentialMetadata) []adminCredentialSummary {
	resp := make([]adminCredentialSummary, 0, len(items))
	for _, item := range items {
		metadata := map[string]any{}
		if len(item.Metadata) > 0 && json.Valid(item.Metadata) {
			_ = json.Unmarshal(item.Metadata, &metadata)
		}
		row := adminCredentialSummary{
			ID:              item.ID,
			CredentialType:  item.Type,
			Username:        item.Username,
			Algorithm:       item.Algorithm,
			KeyID:           item.KeyID,
			Metadata:        metadata,
			CreatedAt:       item.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:       item.UpdatedAt.UTC().Format(time.RFC3339Nano),
			SecretAvailable: true,
		}
		if item.LastRotatedAt != nil {
			row.LastRotatedAt = item.LastRotatedAt.UTC().Format(time.RFC3339Nano)
		}
		resp = append(resp, row)
	}
	return resp
}

func mapGrantResponses(grants []admin.GrantRecord) []adminGrantResponse {
	items := make([]adminGrantResponse, 0, len(grants))
	for _, grant := range grants {
		items = append(items, adminGrantResponse{
			SubjectType: grant.SubjectType,
			SubjectID:   grant.SubjectID,
			SubjectName: grant.SubjectName,
			AssetID:     grant.AssetID,
			AssetName:   grant.AssetName,
			Action:      grant.Action,
			Effect:      grant.Effect,
			CreatedAt:   grant.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return items
}

func mapLDAPSettingsResponse(settings admin.LDAPSettings) adminLDAPSettingsResponse {
	resp := adminLDAPSettingsResponse{
		ProviderMode:           settings.ProviderMode,
		Enabled:                settings.Enabled,
		Host:                   settings.Host,
		Port:                   settings.Port,
		URL:                    settings.URL,
		BaseDN:                 settings.BaseDN,
		BindDN:                 settings.BindDN,
		HasBindPassword:        settings.HasBindPassword,
		UserSearchFilter:       settings.UserSearchFilter,
		SyncUserFilter:         settings.SyncUserFilter,
		UsernameAttribute:      settings.UsernameAttribute,
		DisplayNameAttribute:   settings.DisplayNameAttribute,
		EmailAttribute:         settings.EmailAttribute,
		GroupSearchBaseDN:      settings.GroupSearchBaseDN,
		GroupSearchFilter:      settings.GroupSearchFilter,
		GroupNameAttribute:     settings.GroupNameAttribute,
		GroupRoleMapping:       settings.GroupRoleMapping,
		UseTLS:                 settings.UseTLS,
		StartTLS:               settings.StartTLS,
		InsecureSkipVerify:     settings.InsecureSkipVerify,
		DeactivateMissingUsers: settings.DeactivateMissingUsers,
		UpdatedBy:              settings.UpdatedBy,
	}
	if !settings.UpdatedAt.IsZero() {
		resp.UpdatedAt = settings.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func mapLDAPSyncRunResponse(run admin.LDAPSyncRun) ldapSyncRunResponse {
	resp := ldapSyncRunResponse{
		ID:          run.ID,
		StartedAt:   run.StartedAt.UTC().Format(time.RFC3339Nano),
		Status:      run.Status,
		TriggeredBy: run.TriggeredBy,
		Summary: adminLDAPSyncSummaryResponse{
			Discovered:  run.Summary.Discovered,
			Created:     run.Summary.Created,
			Updated:     run.Summary.Updated,
			Reactivated: run.Summary.Reactivated,
			Unchanged:   run.Summary.Unchanged,
			Deactivated: run.Summary.Deactivated,
			Samples:     run.Summary.Samples,
			Warnings:    run.Summary.Warnings,
		},
		Error: run.Error,
	}
	if run.CompletedAt != nil {
		resp.CompletedAt = run.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func sortedMapKeys(value map[string]any) []string {
	if len(value) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	sort.Strings(keys)
	return keys
}

type createUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type updateUserRequest struct {
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
}

type setUserActiveRequest struct {
	IsActive bool `json:"is_active"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	user, err := h.adminService.CreateUser(r.Context(), admin.CreateUserInput{
		Username:    req.Username,
		Password:    req.Password,
		Email:       req.Email,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, adminUserResponse{
		ID:           user.ID,
		Username:     user.Username,
		Email:        user.Email,
		DisplayName:  user.DisplayName,
		AuthProvider: user.AuthProvider,
		IsActive:     user.IsActive,
		Roles:        user.Roles,
	})
}

func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.adminService.UpdateUser(r.Context(), userID, admin.UpdateUserInput{
		Email:       req.Email,
		DisplayName: req.DisplayName,
	}); err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) SetUserActive(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	var req setUserActiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.adminService.SetUserActive(r.Context(), userID, req.IsActive); err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id is required"})
		return
	}

	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.adminService.ResetUserPassword(r.Context(), userID, req.Password); err != nil {
		if errors.Is(err, admin.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	if assetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset id is required"})
		return
	}

	if err := h.adminService.DeleteAsset(r.Context(), assetID); err != nil {
		if errors.Is(err, admin.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func normalizeRemoteIP(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		trimmed = host
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return ""
	}
	return ip.String()
}
