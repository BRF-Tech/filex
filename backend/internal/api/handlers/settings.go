package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
)

// Settings handles /api/admin/settings.
type Settings struct {
	Store  db.Store
	Mailer *mailer.Service
}

// NewSettings constructs a Settings handler.
func NewSettings(store db.Store) *Settings { return &Settings{Store: store} }

// AttachMailer wires the mailer so the SMTP "Test" button can verify / send.
func (h *Settings) AttachMailer(m *mailer.Service) { h.Mailer = m }

// SMTPTest verifies the SMTP config (auth handshake) and, when a `to` address
// is given, sends a real test message end-to-end.
//
//	POST /api/admin/settings/smtp-test  { "to": "you@example.com" }  (to optional)
func (h *Settings) SMTPTest(w http.ResponseWriter, r *http.Request) {
	if h.Mailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mailer not configured"})
		return
	}
	var req struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.Mailer.Verify(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "stage": "verify", "error": err.Error()})
		return
	}
	to := strings.TrimSpace(req.To)
	if to != "" {
		if err := h.Mailer.Send(r.Context(), to, "filex SMTP test", "Bu bir filex SMTP test e-postasıdır.\n\nThis is a filex SMTP test email."); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "stage": "send", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": false})
}

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
