package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/db"
)

const archiveIsInstanceWide = "archive settings apply to the whole instance and are managed by the platform operator"

// ArchiveAdmin exposes validated archive policy and read-only provider status.
// Executable paths are deliberately absent: they are process configuration,
// not data an HTTP administrator may turn into a command.
type ArchiveAdmin struct {
	Store   db.Store
	Service *archivecli.Service
}

func NewArchiveAdmin(store db.Store, service *archivecli.Service) *ArchiveAdmin {
	return &ArchiveAdmin{Store: store, Service: service}
}

type archiveAdminResponse struct {
	archivecli.Policy
	Providers []archivecli.ProviderStatus `json:"providers"`
}

type archivePolicyPatch struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	DefaultFormat    *string  `json:"default_format,omitempty"`
	AllowedFormats   []string `json:"allowed_formats,omitempty"`
	MaxEntries       *int     `json:"max_entries,omitempty"`
	MaxExpandedBytes *int64   `json:"max_expanded_bytes,omitempty"`
	TimeoutSeconds   *int     `json:"timeout_seconds,omitempty"`
}

func (h *ArchiveAdmin) snapshot(r *http.Request) archiveAdminResponse {
	return archiveAdminResponse{Policy: h.Service.Policy(r.Context()), Providers: h.Service.Providers(r.Context())}
}

func (h *ArchiveAdmin) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, archiveIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.snapshot(r))
}

func (h *ArchiveAdmin) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, archiveIsInstanceWide) {
		return
	}
	var req archivePolicyPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur := h.Service.Policy(r.Context())
	formats := cur.AllowedFormats
	if req.AllowedFormats != nil {
		formats = archivecli.CanonicalFormats(req.AllowedFormats)
		if len(formats) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "allowed_formats must contain a supported creation format"})
			return
		}
		for _, f := range formats {
			if !archivecli.IsCreateFormat(f) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "creation format is not supported: " + f})
				return
			}
		}
	}
	defaultFormat := cur.DefaultFormat
	if req.DefaultFormat != nil {
		defaultFormat = archivecli.CanonicalFormat(*req.DefaultFormat)
	}
	if !archivecli.IsCreateFormat(defaultFormat) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "default_format is not a supported creation format"})
		return
	}
	if !containsString(formats, defaultFormat) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "default_format must be included in allowed_formats"})
		return
	}
	if req.MaxEntries != nil && (*req.MaxEntries < 1 || *req.MaxEntries > 1_000_000) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_entries must be between 1 and 1000000"})
		return
	}
	if req.MaxExpandedBytes != nil && (*req.MaxExpandedBytes < 1<<20 || *req.MaxExpandedBytes > 100<<40) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_expanded_bytes must be between 1 MiB and 100 TiB"})
		return
	}
	if req.TimeoutSeconds != nil && (*req.TimeoutSeconds < 10 || *req.TimeoutSeconds > 24*60*60) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "timeout_seconds must be between 10 and 86400"})
		return
	}
	writes := map[string]string{}
	if req.Enabled != nil {
		writes[archivecli.SettingEnabled] = strconv.FormatBool(*req.Enabled)
	}
	if req.DefaultFormat != nil {
		writes[archivecli.SettingDefaultFormat] = defaultFormat
	}
	if req.AllowedFormats != nil {
		writes[archivecli.SettingAllowedFormats] = strings.Join(formats, ",")
	}
	if req.MaxEntries != nil {
		writes[archivecli.SettingMaxEntries] = strconv.Itoa(*req.MaxEntries)
	}
	if req.MaxExpandedBytes != nil {
		writes[archivecli.SettingMaxExpandedBytes] = strconv.FormatInt(*req.MaxExpandedBytes, 10)
	}
	if req.TimeoutSeconds != nil {
		writes[archivecli.SettingTimeoutSeconds] = strconv.Itoa(*req.TimeoutSeconds)
	}
	for key, value := range writes {
		if err := h.Store.UpsertSetting(r.Context(), key, value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, h.snapshot(r))
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

// Test performs an actual encrypted create/list/extract round trip. A version
// command alone proves that a file exists, not that its codecs and crypto work.
func (h *ArchiveAdmin) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, archiveIsInstanceWide) {
		return
	}
	root, err := os.MkdirTemp(h.Service.WorkDir(), "filex-archive-test-*")
	if err != nil {
		// WorkDir may not exist yet on a fresh installation.
		if err := os.MkdirAll(h.Service.WorkDir(), 0o700); err == nil {
			root, err = os.MkdirTemp(h.Service.WorkDir(), "filex-archive-test-*")
		}
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.RemoveAll(root)
	src, out := filepath.Join(root, "source"), filepath.Join(root, "out")
	if err := os.MkdirAll(src, 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.WriteFile(filepath.Join(src, "probe.txt"), []byte("filex archive provider probe\n"), 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	archive := filepath.Join(root, "probe.7z")
	const password = "filex-provider-probe"
	solid := true
	if err := h.Service.Create(r.Context(), src, archive, archivecli.CreateOptions{
		Format: "7z", Password: password, EncryptNames: true, Compression: 1,
		Solid: &solid, DictionarySizeMiB: 4,
	}); err != nil {
		writeArchiveProviderError(w, err)
		return
	}
	entries, err := h.Service.List(r.Context(), archive, password)
	if err != nil {
		writeArchiveProviderError(w, err)
		return
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Service.Extract(r.Context(), archive, out, password, nil); err != nil {
		writeArchiveProviderError(w, err)
		return
	}
	b, err := os.ReadFile(filepath.Join(out, "probe.txt"))
	if err != nil || string(b) != "filex archive provider probe\n" {
		if err == nil {
			err = errors.New("provider extracted unexpected bytes")
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": len(entries)})
}
