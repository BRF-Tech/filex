// Package handlers — auth_providers.go
//
// Admin CRUD for auth driver configuration.
//
//	GET    /api/admin/auth-providers
//	PATCH  /api/admin/auth-providers/{name}
//	POST   /api/admin/auth-providers/{name}/test
//
// V0.1: configuration is stored in the `settings` table under keys with the
// prefix `auth.<name>.<field>`. The UI sends a complete JSON config and we
// upsert each leaf. Hot-reload of the actual driver is left to a server
// restart for now (TODO: live re-init via auth.Registry).
package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
)

// AuthProviders handles /api/admin/auth-providers.
type AuthProviders struct {
	Store db.Store
}

// NewAuthProviders constructs the handler.
func NewAuthProviders(store db.Store) *AuthProviders {
	return &AuthProviders{Store: store}
}

// providerInfo is the wire shape for one auth driver.
type providerInfo struct {
	Name           string                 `json:"name"`
	Enabled        bool                   `json:"enabled"`
	Capabilities   auth.Capabilities      `json:"capabilities"`
	ConfigRedacted map[string]interface{} `json:"config_redacted"`
}

// List returns every registered auth driver and its current config (redacted).
func (h *AuthProviders) List(w http.ResponseWriter, r *http.Request) {
	names := auth.Names()
	sort.Strings(names)

	enabled := map[string]bool{}
	for _, d := range auth.Enabled() {
		enabled[d.Name()] = true
	}

	out := []providerInfo{}
	for _, n := range names {
		info := providerInfo{
			Name:           n,
			Enabled:        enabled[n],
			ConfigRedacted: map[string]interface{}{},
		}
		if drv, err := auth.Get(n); err == nil {
			info.Capabilities = drv.Capabilities()
		}
		// Pull stored settings under prefix `auth.<n>.`.
		prefix := "auth." + n + "."
		all, _ := h.Store.ListSettings(r.Context())
		for k, v := range all {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			leaf := strings.TrimPrefix(k, prefix)
			val := v
			if isSecretKey(leaf) && val != "" {
				val = "***"
			}
			info.ConfigRedacted[leaf] = val
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// Update writes a full JSON config under `auth.<name>.<key>` settings.
func (h *AuthProviders) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	var raw map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	for k, v := range raw {
		if v == nil {
			continue
		}
		key := "auth." + name + "." + k
		val, _ := stringifyValue(v)
		_ = h.Store.UpsertSetting(r.Context(), key, val)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"warning": "configuration saved; restart the server for the change to take effect",
	})
}

// Test exercises the driver against the new config (driver-specific).
//
// V0.1 returns a stub OK so the UI works; richer test logic (OIDC discovery,
// LDAP bind probe) lives in the driver implementations and can be added later.
func (h *AuthProviders) Test(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, err := auth.Get(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such driver"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"name": name,
		"note": "V0.1 stub — drivers self-test on Init at server start",
	})
}

func isSecretKey(k string) bool {
	switch strings.ToLower(k) {
	case "client_secret", "secret", "password", "bind_password", "token":
		return true
	}
	return false
}

func stringifyValue(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	default:
		b, err := json.Marshal(v)
		return string(b), err
	}
}
