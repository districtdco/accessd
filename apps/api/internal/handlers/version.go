package handlers

import (
	"net/http"
	"time"

	"github.com/districtd/pam/api/internal/config"
)

type VersionHandler struct {
	Version config.VersionInfo
}

type versionResponse struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"built_at"`
	Timestamp string `json:"timestamp"`
}

func NewVersionHandler(version config.VersionInfo) *VersionHandler {
	return &VersionHandler{Version: version}
}

func (h *VersionHandler) Get(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Service:   h.Version.Service,
		Version:   h.Version.Version,
		Commit:    h.Version.Commit,
		BuiltAt:   h.Version.BuiltAt,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
