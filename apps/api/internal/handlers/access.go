package handlers

import (
	"net/http"
	"strconv"

	"github.com/districtd/pam/api/internal/access"
	"github.com/districtd/pam/api/internal/auth"
)

type AccessHandler struct {
	accessService *access.Service
}

type myAccessResponse struct {
	Items []accessPointResponse `json:"items"`
}

type accessPointResponse struct {
	AssetID        string   `json:"asset_id"`
	AssetName      string   `json:"asset_name"`
	AssetType      string   `json:"asset_type"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Endpoint       string   `json:"endpoint"`
	AllowedActions []string `json:"allowed_actions"`
}

func NewAccessHandler(accessService *access.Service) *AccessHandler {
	return &AccessHandler{accessService: accessService}
}

func (h *AccessHandler) MyAccess(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.CurrentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	points, err := h.accessService.AllowedAssetsForUser(r.Context(), currentUser.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve access"})
		return
	}

	resp := myAccessResponse{Items: make([]accessPointResponse, 0, len(points))}
	for _, point := range points {
		resp.Items = append(resp.Items, accessPointResponse{
			AssetID:        point.AssetID,
			AssetName:      point.AssetName,
			AssetType:      point.AssetType,
			Host:           point.Host,
			Port:           point.Port,
			Endpoint:       point.Host + ":" + strconv.Itoa(point.Port),
			AllowedActions: point.AllowedActions,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}
