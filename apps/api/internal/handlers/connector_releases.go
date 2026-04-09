package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	ChecksumSigURL  string                             `json:"checksum_sig_url"`
	Artifacts       []connectorReleaseArtifactResponse `json:"artifacts"`
	BackwardCompat  []string                           `json:"backward_compatibility"`
}

type connectorReleaseVersionResponse struct {
	Version         string                             `json:"version"`
	Tag             string                             `json:"tag"`
	ChecksumFileURL string                             `json:"checksum_file_url"`
	ChecksumSigURL  string                             `json:"checksum_sig_url"`
	Artifacts       []connectorReleaseArtifactResponse `json:"artifacts"`
}

type connectorReleaseVersionsResponse struct {
	LatestVersion  string                            `json:"latest_version"`
	MinimumVersion string                            `json:"minimum_version"`
	Versions       []connectorReleaseVersionResponse `json:"versions"`
}

type connectorReleaseArtifactResponse struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	PackageType  string `json:"package_type"`
	FileName     string `json:"file_name"`
	ArchiveType  string `json:"archive_type"`
	DownloadURL  string `json:"download_url"`
	SignatureURL string `json:"signature_url"`
	Preferred    bool   `json:"preferred"`
}

func NewConnectorReleasesHandler(cfg config.ConnectorDistributionConfig) *ConnectorReleasesHandler {
	return &ConnectorReleasesHandler{cfg: cfg}
}

func (h *ConnectorReleasesHandler) Latest(w http.ResponseWriter, _ *http.Request) {
	version := strings.TrimSpace(h.cfg.LatestVersion)
	trimmedVersion := normalizeVersion(version)
	tag := "v" + trimmedVersion
	base := strings.TrimRight(strings.TrimSpace(h.cfg.DownloadBase), "/")

	publishedArtifacts, checksumURL, checksumSigURL := h.publishedReleaseArtifacts(trimmedVersion, tag, base)

	writeJSON(w, http.StatusOK, connectorReleaseMetadataResponse{
		ProductName:     "AccessD",
		BinaryName:      "accessd-connector",
		LatestVersion:   trimmedVersion,
		MinimumVersion:  normalizeVersion(h.cfg.MinimumVersion),
		ReleaseChannel:  strings.TrimSpace(h.cfg.ReleaseChannel),
		RuntimeModel:    "on-demand",
		InstallDocsURL:  "https://github.com/districtd/accessd/blob/main/docs/CONNECTOR_DISTRIBUTION.md",
		ChecksumFileURL: checksumURL,
		ChecksumSigURL:  checksumSigURL,
		Artifacts:       publishedArtifacts,
		BackwardCompat: []string{
			"Use ACCESSD_* environment variable names",
			"Connector installer auto-refreshes AccessD TLS trust when enabled",
			"Package-first model (pkg/msi/deb/rpm) with archive fallback",
		},
	})
}

func (h *ConnectorReleasesHandler) Versions(w http.ResponseWriter, _ *http.Request) {
	base := strings.TrimRight(strings.TrimSpace(h.cfg.DownloadBase), "/")
	latestVersion := normalizeVersion(h.cfg.LatestVersion)
	minVersion := normalizeVersion(h.cfg.MinimumVersion)

	versions := h.availableVersionsFromDisk()
	if len(versions) == 0 && latestVersion != "" {
		versions = []string{latestVersion}
	}

	respVersions := make([]connectorReleaseVersionResponse, 0, len(versions))
	for _, version := range versions {
		tag := "v" + version
		artifacts, checksumURL, checksumSigURL := h.publishedReleaseArtifacts(version, tag, base)
		if len(artifacts) == 0 && checksumURL == "" {
			continue
		}
		respVersions = append(respVersions, connectorReleaseVersionResponse{
			Version:         version,
			Tag:             tag,
			ChecksumFileURL: checksumURL,
			ChecksumSigURL:  checksumSigURL,
			Artifacts:       artifacts,
		})
	}

	writeJSON(w, http.StatusOK, connectorReleaseVersionsResponse{
		LatestVersion:  latestVersion,
		MinimumVersion: minVersion,
		Versions:       respVersions,
	})
}

func (h *ConnectorReleasesHandler) releaseFileExists(tag, fileName string) bool {
	root := strings.TrimSpace(h.cfg.DownloadRoot)
	if root == "" {
		return false
	}
	target := filepath.Join(root, tag, fileName)
	info, err := os.Stat(target)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (h *ConnectorReleasesHandler) availableVersionsFromDisk() []string {
	root := strings.TrimSpace(h.cfg.DownloadRoot)
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	versions := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasPrefix(name, "v") && !strings.HasPrefix(name, "V") {
			continue
		}
		version := normalizeVersion(name)
		if version == "" {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
	return versions
}

func compareVersions(leftRaw, rightRaw string) int {
	left := strings.Split(normalizeVersion(leftRaw), ".")
	right := strings.Split(normalizeVersion(rightRaw), ".")
	for i := 0; i < 3; i++ {
		li := versionPart(left, i)
		ri := versionPart(right, i)
		if li > ri {
			return 1
		}
		if li < ri {
			return -1
		}
	}
	return 0
}

func versionPart(parts []string, idx int) int {
	if idx >= len(parts) {
		return 0
	}
	part := strings.TrimSpace(parts[idx])
	if part == "" {
		return 0
	}
	numeric := part
	for i, r := range part {
		if r < '0' || r > '9' {
			numeric = part[:i]
			break
		}
	}
	if numeric == "" {
		return 0
	}
	n, err := strconv.Atoi(numeric)
	if err != nil {
		return 0
	}
	return n
}

func (h *ConnectorReleasesHandler) publishedReleaseArtifacts(version, tag, base string) ([]connectorReleaseArtifactResponse, string, string) {
	artifacts := []connectorReleaseArtifactResponse{
		h.artifact("darwin", "arm64", "pkg", "pkg", version, tag, base, true),
		h.artifact("darwin", "arm64", "archive", "tar.gz", version, tag, base, false),
		h.artifact("darwin", "amd64", "pkg", "pkg", version, tag, base, true),
		h.artifact("darwin", "amd64", "archive", "tar.gz", version, tag, base, false),
		h.artifact("linux", "amd64", "deb", "deb", version, tag, base, true),
		h.artifact("linux", "amd64", "rpm", "rpm", version, tag, base, true),
		h.artifact("linux", "amd64", "archive", "tar.gz", version, tag, base, false),
		h.artifact("linux", "arm64", "deb", "deb", version, tag, base, true),
		h.artifact("linux", "arm64", "rpm", "rpm", version, tag, base, true),
		h.artifact("linux", "arm64", "archive", "tar.gz", version, tag, base, false),
		h.artifact("windows", "amd64", "msi", "msi", version, tag, base, true),
		h.artifact("windows", "amd64", "archive", "zip", version, tag, base, false),
	}
	publishedArtifacts := make([]connectorReleaseArtifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !h.releaseFileExists(tag, artifact.FileName) {
			continue
		}
		if !h.releaseFileExists(tag, artifact.FileName+".sig") {
			artifact.SignatureURL = ""
		}
		publishedArtifacts = append(publishedArtifacts, artifact)
	}

	checksumFileName := fmt.Sprintf("accessd-connector-%s-checksums.txt", version)
	checksumURL := fmt.Sprintf("%s/%s/%s", base, tag, checksumFileName)
	if !h.releaseFileExists(tag, checksumFileName) {
		checksumURL = ""
	}
	checksumSigURL := checksumURL + ".sig"
	if checksumURL == "" || !h.releaseFileExists(tag, checksumFileName+".sig") {
		checksumSigURL = ""
	}
	return publishedArtifacts, checksumURL, checksumSigURL
}

func (h *ConnectorReleasesHandler) artifact(goos, arch, packageType, archiveType, version, tag, base string, preferred bool) connectorReleaseArtifactResponse {
	file := fmt.Sprintf("accessd-connector-%s-%s-%s.%s", version, goos, arch, archiveType)
	return connectorReleaseArtifactResponse{
		OS:           goos,
		Arch:         arch,
		PackageType:  packageType,
		FileName:     file,
		ArchiveType:  archiveType,
		DownloadURL:  strings.TrimRight(base, "/") + "/" + path.Join(tag, file),
		SignatureURL: strings.TrimRight(base, "/") + "/" + path.Join(tag, file+".sig"),
		Preferred:    preferred,
	}
}

func normalizeVersion(raw string) string {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}
