package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/db"
)

// Settings handles /api/admin/settings.
type Settings struct {
	Store db.Store
}

// NewSettings constructs a Settings handler.
func NewSettings(store db.Store) *Settings { return &Settings{Store: store} }

// List returns all key/value pairs.
func (h *Settings) List(w http.ResponseWriter, r *http.Request) {
	m, err := h.Store.ListSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	redactSecretSettings(m)
	writeJSON(w, http.StatusOK, m)
}

// Set upserts a single setting.
//
// Body: {"value":"…"}
func (h *Settings) Set(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing key"})
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Store.UpsertSetting(r.Context(), key, req.Value); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Update upserts multiple settings in a single request — the collection-level
// counterpart of Set. The admin Settings page PATCHes the whole form here; the
// old API only had GET / + PUT /{key}, so the page's save 405'd.
//
// Body: a flat JSON object {key: value, …}. Non-string values (numbers, bools)
// are JSON-encoded to text so they round-trip through the string-valued
// settings store. Returns the redacted settings map after the write.
func (h *Settings) Update(w http.ResponseWriter, r *http.Request) {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	for k, v := range raw {
		if k == "" || v == nil {
			continue
		}
		val, _ := stringifyValue(v)
		// Never let a redacted placeholder overwrite a real secret.
		if val == "***" && isSecretSettingKey(k) {
			continue
		}
		if err := h.Store.UpsertSetting(r.Context(), k, val); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	m, err := h.Store.ListSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	redactSecretSettings(m)
	writeJSON(w, http.StatusOK, m)
}

// redactSecretSettings masks secret-bearing values in a settings map so admin
// reads never expose provider client secrets / bind passwords in clear text
// (auth.<provider>.* secrets share the settings table with general config).
func redactSecretSettings(m map[string]string) {
	for k, v := range m {
		if v != "" && isSecretSettingKey(k) {
			m[k] = "***"
		}
	}
}

// isSecretSettingKey reports whether a settings key holds a secret, testing its
// trailing dot-segment (auth.oidc.client_secret → "client_secret") against the
// shared secret-key set defined in auth_providers.go.
func isSecretSettingKey(key string) bool {
	leaf := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		leaf = key[i+1:]
	}
	return isSecretKey(leaf)
}
