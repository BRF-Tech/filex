// Package handlers — external_admin.go
//
// Admin CRUD for external services (OnlyOffice, Drawio, Mermaid, …).
//
//	GET    /api/admin/external
//	PATCH  /api/admin/external/{name}
//	POST   /api/admin/external/{name}/test
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/external"
)

// redactedSecret is what List puts in place of a stored secret. PATCH ignores
// it on the way back in: the admin UI re-sends whatever it was shown, and
// storing this string would replace a working JWT secret with six characters of
// asterisk — a failure that only shows up the next time somebody opens a
// document, which is precisely the class of bug issue #17 was.
const redactedSecret = "***"

// externalIsInstanceWide is the refusal a tenant admin reads. There is ONE
// document server, ONE converter, and one shared JWT secret behind them, so
// this surface decides where every tenant's documents are sent and what
// credential is used to sign the handoff. Repointing it at another host is
// enough to read and rewrite every tenant's office documents in transit, and
// the Test button will dial whatever address it is given.
const externalIsInstanceWide = "external services apply to the whole instance and are managed by the platform operator"

// The vantage point of the server-side probe, and the names of the two legs it
// does not cover. Callers (admin UI, MCP) key off these strings, so they are
// constants rather than inline literals.
const (
	checkedFromServer   = "filex-server"
	legBrowserToService = "browser-to-service"
	legServiceToFilex   = "service-to-filex"
)

// ExternalAdmin handles /api/admin/external.
type ExternalAdmin struct {
	Store db.Store
	Caps  *capability.Service
	// Live is the runtime resolver every consumer reads through. Invalidated
	// on write so an operator's change takes effect on the next request.
	Live *external.Resolver
	// EnvManaged names the services pinned by env/YAML. Those rows are
	// re-asserted from the environment at every boot, so a change made here
	// applies live but does not survive a restart — and the API says so.
	EnvManaged map[string]bool
	// PublicURL is the address filex hands to the document server for the
	// document fetch and the save callback. It is the third address in the
	// three-address problem (see internal/external/advisory.go) and the one
	// nothing used to check, so it travels with every List and Test response.
	PublicURL string
	// PublicURLSet is false when nobody chose PublicURL and it defaulted to
	// http://localhost:5212. Worth saying out loud: an operator who never set
	// it does not know a default is in play.
	PublicURLSet bool
}

// AttachPublicURL wires the process's public URL into the advisories. Kept out
// of NewExternalAdmin so the two call sites (routes.go and the MCP admin
// surface) cannot silently disagree about the constructor signature.
func (h *ExternalAdmin) AttachPublicURL(publicURL string, set bool) {
	h.PublicURL = publicURL
	h.PublicURLSet = set
}

// advisories runs the shape checks for one row. The DNS note is only worth a
// lookup when an operator is waiting (Test); List passes nil so opening the
// admin page never blocks on a resolver.
func (h *ExternalAdmin) advisories(ctx context.Context, name, url string, lookup external.LookupFunc) []external.Advisory {
	return external.Advisories(name, url, h.PublicURL, h.PublicURLSet, lookup)
}

// NewExternalAdmin constructs the handler.
func NewExternalAdmin(store db.Store, caps *capability.Service, live *external.Resolver, envManaged map[string]bool) *ExternalAdmin {
	return &ExternalAdmin{Store: store, Caps: caps, Live: live, EnvManaged: envManaged}
}

// List returns every configured external service.
func (h *ExternalAdmin) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, externalIsInstanceWide) {
		return
	}
	rows, err := h.Store.ListExternalServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		secret := row.SecretEnc
		if secret != "" {
			secret = redactedSecret
		}
		out = append(out, map[string]any{
			// Historical PascalCase wire shape — web/src/api/external.ts reads
			// both cases, but existing installs' admin bundles read only these.
			"Name":        row.Name,
			"Enabled":     row.Enabled,
			"URL":         row.URL,
			"SecretEnc":   secret,
			"OptionsJSON": row.OptionsJSON,
			"LastCheck":   row.LastCheck,
			"LastState":   row.LastState,
			"env_managed": h.EnvManaged[row.Name],
			// ⚠ Advisories ride on List, not only on Test. The whole defect
			// was a badge that looked settled without anyone pressing
			// anything; an operator must be able to see a browser-unreachable
			// address the moment the page paints.
			"advisories": h.advisories(r.Context(), row.Name, row.URL, nil),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":    out,
		"public_url": h.PublicURL,
	})
}

type extPatchReq struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	URL         *string `json:"url,omitempty"`
	Secret      *string `json:"secret,omitempty"`       // plaintext from UI; will be encrypted server-side
	OptionsJSON *string `json:"options_json,omitempty"` // raw JSON blob
}

// Update upserts a row and re-runs the health probe.
func (h *ExternalAdmin) Update(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, externalIsInstanceWide) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	var req extPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, _ := h.Store.GetExternalService(r.Context(), name)
	enabled := true
	url := ""
	secret := ""
	options := "{}"
	if cur != nil {
		enabled = cur.Enabled
		url = cur.URL
		secret = cur.SecretEnc
		options = cur.OptionsJSON
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.URL != nil {
		url = *req.URL
	}
	if req.Secret != nil && *req.Secret != redactedSecret {
		secret = *req.Secret // TODO: encrypt with master key
	}
	if req.OptionsJSON != nil {
		options = *req.OptionsJSON
	}
	if err := h.Store.UpsertExternalService(r.Context(), name, enabled, url, secret, options, nowOrZero(), "unknown"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Invalidate the capability cache so the new URL is probed on next call.
	if h.Caps != nil {
		h.Caps.Invalidate()
	}
	// ⚠ And the runtime resolver, which is the half that used to be missing:
	// before this, the row was saved and reported back correctly while every
	// consumer kept using the boot-time value, so the UI showed a green Test
	// next to a feature that answered "not configured" (issue #17).
	h.Live.Invalidate()
	resp := map[string]any{"ok": true, "env_managed": h.EnvManaged[name]}
	if h.EnvManaged[name] {
		resp["note"] = "Applied now, but this service is pinned by an environment variable and will be reset from it the next time filex restarts."
	}
	writeJSON(w, http.StatusOK, resp)
}

// Test runs an immediate health probe and returns the state.
func (h *ExternalAdmin) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, externalIsInstanceWide) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if h.Caps == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capability service unavailable"})
		return
	}
	state, err := h.Caps.ProbeExternal(r.Context(), name)
	if err != nil {
		// "no rows in result set" → unknown service, not a probe
		// failure. Surface that as 404 so callers (and Cypress) can
		// distinguish "service down" from "you misspelled the name".
		if strings.Contains(err.Error(), "no rows in result set") || strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown external service: " + name,
				"name":  name,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           false,
			"reachable":    false,
			"error":        err.Error(),
			"name":         name,
			"checked_from": checkedFromServer,
			"public_url":   h.PublicURL,
		})
		return
	}
	// ⚠ Say WHAT was verified, and by whom. This probe left the filex
	// process and nothing else: it says nothing about the browser that loads
	// the editor, and nothing about the document server's route back to
	// filex. A green light standing for three questions is exactly what sent
	// issue #17 round a second time.
	adv := h.advisories(r.Context(), name, state.URL, external.SystemLookup(r.Context(), 2*time.Second))
	resp := map[string]any{
		"ok":        true,
		"name":      name,
		"reachable": state.State == "ok",
		"url":       state.URL,
		"state":     state.State,
		// checked_from is the vantage point of THIS result. The admin page
		// runs its own probe from the browser and reports the two separately.
		"checked_from": checkedFromServer,
		// not_checked names the legs this endpoint cannot settle. The browser
		// leg is answered by the admin page; the callback leg cannot be
		// answered at all — filex has no way to make another container issue a
		// request on demand — so it is covered by advisories instead.
		"not_checked": []string{legBrowserToService, legServiceToFilex},
		"public_url":  h.PublicURL,
		"advisories":  adv,
		// complete is false whenever anything is unsettled or wrong. It is
		// deliberately NOT "the probe succeeded": that is the conflation this
		// change exists to remove.
		"server_reachable": state.State == "ok",
		"has_warnings":     external.HasWarning(adv),
	}
	// ⚠ Say WHAT is missing. "unconfigured" on a service that has a URL means
	// the other half of its configuration is absent, and the operator is
	// looking at a Document Server they know is up — leaving them to guess is
	// how issue #17 read from the outside.
	if state.State == "unconfigured" && state.URL != "" {
		resp["error"] = "the URL is set but the JWT secret is not; OnlyOffice signs every editor session with it"
	}
	writeJSON(w, http.StatusOK, resp)
}

func nowOrZero() time.Time { return time.Now() }

// _ keeps db pkg import alive even if linter complains (we use db.Store).
var _ db.Store = nil
