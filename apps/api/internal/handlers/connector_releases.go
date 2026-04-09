package handlers

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/districtd/pam/api/internal/config"
)

type ConnectorReleasesHandler struct {
	cfg config.ConnectorDistributionConfig
}

type connectorReleaseMetadataResponse struct {
	ProductName     string                             `json:"product_name"`
	BinaryName      string                             `json:"binary_name"`
	LatestVersion   string                             `json:"latest_version"`
	MinimumVersion  string                             `json:"minimum_version"`
	ReleaseChannel  string                             `json:"release_channel"`
	RuntimeModel    string                             `json:"runtime_model"`
	InstallDocsURL  string                             `json:"install_docs_url"`
	ChecksumFileURL string                             `json:"checksum_file_url"`
	Artifacts       []connectorReleaseArtifactResponse `json:"artifacts"`
	BackwardCompat  []string                           `json:"backward_compatibility"`
}

type connectorReleaseArtifactResponse struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	FileName    string `json:"file_name"`
	ArchiveType string `json:"archive_type"`
	DownloadURL string `json:"download_url"`
}

func NewConnectorReleasesHandler(cfg config.ConnectorDistributionConfig) *ConnectorReleasesHandler {
	return &ConnectorReleasesHandler{cfg: cfg}
}

func (h *ConnectorReleasesHandler) Latest(w http.ResponseWriter, _ *http.Request) {
	version := strings.TrimSpace(h.cfg.LatestVersion)
	trimmedVersion := strings.TrimPrefix(version, "v")
	tag := "v" + trimmedVersion
	base := strings.TrimRight(strings.TrimSpace(h.cfg.DownloadBase), "/")

	artifacts := []connectorReleaseArtifactResponse{
		h.artifact("darwin", "arm64", "tar.gz", trimmedVersion, tag, base),
		h.artifact("darwin", "amd64", "tar.gz", trimmedVersion, tag, base),
		h.artifact("linux", "amd64", "tar.gz", trimmedVersion, tag, base),
		h.artifact("linux", "arm64", "tar.gz", trimmedVersion, tag, base),
		h.artifact("windows", "amd64", "zip", trimmedVersion, tag, base),
	}

	writeJSON(w, http.StatusOK, connectorReleaseMetadataResponse{
		ProductName:     "AccessD",
		BinaryName:      "accessd-connector",
		LatestVersion:   version,
		MinimumVersion:  strings.TrimSpace(h.cfg.MinimumVersion),
		ReleaseChannel:  strings.TrimSpace(h.cfg.ReleaseChannel),
		RuntimeModel:    "on-demand",
		InstallDocsURL:  "https://github.com/districtd/accessd/blob/main/docs/CONNECTOR_DISTRIBUTION.md",
		ChecksumFileURL: fmt.Sprintf("%s/%s/accessd-connector-%s-checksums.txt", base, tag, trimmedVersion),
		Artifacts:       artifacts,
		BackwardCompat: []string{
			"Use ACCESSD_* environment variable names",
			"Connector installer auto-refreshes AccessD TLS trust when enabled",
		},
	})
}

func (h *ConnectorReleasesHandler) artifact(goos, arch, archiveType, version, tag, base string) connectorReleaseArtifactResponse {
	file := fmt.Sprintf("accessd-connector-%s-%s-%s.%s", version, goos, arch, archiveType)
	return connectorReleaseArtifactResponse{
		OS:          goos,
		Arch:        arch,
		FileName:    file,
		ArchiveType: archiveType,
		DownloadURL: strings.TrimRight(base, "/") + "/" + path.Join(tag, file),
	}
}
