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
	"errors"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/srvtext"
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
	// AppUIOrigin is FILEX_APP_UI_ORIGIN and OfficeFrameOrigin
	// FILEX_ONLYOFFICE_FRAME_ORIGIN. With either the editor's api.js runs in a
	// frame on another origin; with neither, in filex's own page - which the
	// ONLYOFFICE card says (external.CodeEditorSameOrigin, task #92).
	AppUIOrigin       string
	OfficeFrameOrigin string
	// ReversePath measures the third leg — the document server's route BACK to
	// filex — by asking it to download a one-shot URL of ours, through the same
	// signed fetch endpoint a document uses, and watching for the request. It
	// also asks whether the document server enforces JWT. A nil target means
	// the configuration in force. Nil when OnlyOffice is not wired, and only
	// meaningful for that service: nothing else calls filex back.
	ReversePath func(ctx context.Context, t *onlyoffice.Target) onlyoffice.ReverseResult
}

// AttachPublicURL wires the process's public URL into the advisories. Kept out
// of NewExternalAdmin so the two call sites (routes.go and the MCP admin
// surface) cannot silently disagree about the constructor signature.
func (h *ExternalAdmin) AttachPublicURL(publicURL string, set bool) {
	h.PublicURL = publicURL
	h.PublicURLSet = set
}

// AttachAppUIOrigin wires FILEX_APP_UI_ORIGIN into the advisories, beside the
// public URL and for the same two call sites.
func (h *ExternalAdmin) AttachAppUIOrigin(origin string) {
	h.AppUIOrigin = origin
}

// AttachOfficeFrameOrigin wires FILEX_ONLYOFFICE_FRAME_ORIGIN into the same
// advisories.
func (h *ExternalAdmin) AttachOfficeFrameOrigin(origin string) {
	h.OfficeFrameOrigin = origin
}

// advisories runs the shape checks for one row. The DNS note is only worth a
// lookup when an operator is waiting (Test); List passes nil so opening the
// admin page never blocks on a resolver.
func (h *ExternalAdmin) advisories(ctx context.Context, name, url, callbackURL string, lookup external.LookupFunc) []external.Advisory {
	return external.Advise(external.AdvisoryInput{
		Service: name, ServiceURL: url,
		PublicURL: h.PublicURL, PublicURLSet: h.PublicURLSet,
		CallbackURL: callbackURL, Lookup: lookup,
		AppUIOrigin: h.AppUIOrigin, OfficeFrameOrigin: h.OfficeFrameOrigin,
	})
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
			// The address the service comes back to, lifted out of the options
			// blob so a client never has to parse it.
			"callback_url": external.CallbackURLFromOptions(row.OptionsJSON),
			"LastCheck":    row.LastCheck,
			"LastState":    row.LastState,
			"env_managed":  h.EnvManaged[row.Name],
			// ⚠ Advisories ride on List, not only on Test. The whole defect
			// was a badge that looked settled without anyone pressing
			// anything; an operator must be able to see a browser-unreachable
			// address the moment the page paints.
			"advisories": h.advisories(r.Context(), row.Name, row.URL, external.CallbackURLFromOptions(row.OptionsJSON), nil),
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
	// CallbackURL is the address the SERVICE reaches filex at. Sent as its own
	// field rather than as a hand-assembled options blob so a client cannot
	// wipe the row's other options by writing only this one.
	CallbackURL *string `json:"callback_url,omitempty"`
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
	// ⚠ An address the service can be reached at, or none. "bu-bir-adres-
	// degil" was stored and then probed (release-candidate sweep, 2026-09-22,
	// QA #38); the admin page checks the same rule (web lib/formCheck
	// isHttpUrl) before it sends anything.
	if !httpAddress(url) || (req.CallbackURL != nil && !httpAddress(*req.CallbackURL)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "url_invalid",
			"message": srvtext.Text(langOf(r), "server.external.url_invalid", nil),
		})
		return
	}
	if req.CallbackURL != nil {
		merged, err := external.WithCallbackURL(options, *req.CallbackURL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		options = merged
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
		note := envPinnedNote(name)
		resp["note"] = note
		resp["env_var"] = externalEnvVar[name]
		// The audit row says it too: "switched off" in the trail of an
		// env-pinned service is "switched off until the next restart".
		auth.AddAuditDetail(r.Context(), "env_managed", true)
		auth.AddAuditDetail(r.Context(), "note", note)
	}
	auth.AddAuditDetail(r.Context(), "enabled", enabled)
	writeJSON(w, http.StatusOK, resp)
}

// externalEnvVar names the environment variable that pins each service
// (config.go; the config file's external_services.<name>.url does the same).
var externalEnvVar = map[string]string{
	external.OnlyOffice: "FILEX_ONLYOFFICE_URL",
	external.Drawio:     "FILEX_DRAWIO_URL",
}

// envPinnedNote is what the PATCH answer and the audit row say about an edit
// to a service the environment pins: it applies now, the next start writes
// the environment back (switching the service on again if it was switched
// off here), and removing the variable is how it goes for good. The External
// services card says the same (external.envManagedHint).
func envPinnedNote(name string) string {
	v := externalEnvVar[name]
	if v == "" {
		v = "an environment variable"
	}
	return "Applied now, until filex restarts: this service is pinned by " + v +
		", and the next start writes the environment's value back, switching the service on again if it was switched off here. To switch it off for good, remove " + v + "."
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
	// The values in the admin page's form, when it sent them. "Test now" used
	// to probe the SAVED row while the box held another address, so an
	// operator who typed a new URL and pressed Test was told about the old
	// one (issue #80). The form's values are tested as they are, and nothing
	// is saved: a Test must not switch every open editor over to an address
	// that has not been checked yet. An empty body tests the saved row, as
	// before.
	var draft extPatchReq
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&draft); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
	}
	var (
		vals    testValues
		unsaved bool
	)
	if cur, err := h.Store.GetExternalService(r.Context(), name); err == nil && cur != nil {
		vals, unsaved = testValuesOf(cur, draft)
	}
	if unsaved && (!httpAddress(vals.url) || !httpAddress(vals.callback)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "url_invalid",
			"message": srvtext.Text(langOf(r), "server.external.url_invalid", nil),
		})
		return
	}
	var state *model.ExternalServiceState
	if unsaved {
		// Probed, not stored: the row's verdict stays about the row.
		state = h.Caps.ProbeExternalValues(name, vals.enabled, vals.url, vals.secret)
	} else {
		var err error
		state, err = h.Caps.ProbeExternal(r.Context(), name)
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
	}
	// ⚠ Say WHAT was verified, and by whom. This probe left the filex
	// process and nothing else: it says nothing about the browser that loads
	// the editor, and nothing about the document server's route back to
	// filex. A green light standing for three questions is exactly what sent
	// issue #17 round a second time.
	callbackURL := vals.callback
	adv := h.advisories(r.Context(), name, state.URL, callbackURL, external.SystemLookup(r.Context(), 2*time.Second))
	// The third leg, measured rather than disclaimed — when there is something
	// to measure. A service with no URL or no secret cannot be asked anything,
	// and a failed ask is reported as unchecked, never as a broken route.
	reverse := onlyoffice.ReverseResult{}
	if name == external.OnlyOffice && h.ReversePath != nil && state.State == "ok" {
		var target *onlyoffice.Target
		if unsaved {
			// The probe is signed with the form's secret and verified against
			// it, since that is what is being tested.
			target = &onlyoffice.Target{
				DocumentServerURL: vals.url, Secret: vals.secret, CallbackURL: vals.callback, Draft: true,
			}
		}
		reverse = h.ReversePath(r.Context(), target)
	}
	// A document server that took a request with no token does not enforce
	// JWT: it refuses filex's editor token, filters private addresses, and its
	// save callbacks arrive unsigned, which filex refuses (issue #80). That is
	// a warning on the card, translated by code like every other advisory.
	if reverse.JWTEnforced != nil && !*reverse.JWTEnforced {
		adv = append(adv, external.JWTNotEnforced(state.URL))
	}
	notChecked := []string{legBrowserToService}
	if !reverse.Checked {
		notChecked = append(notChecked, legServiceToFilex)
	}
	resp := map[string]any{
		"ok":        true,
		"name":      name,
		"reachable": state.State == "ok",
		"url":       state.URL,
		"state":     state.State,
		// detail is what the probe saw when the answer was not healthy — a
		// status code, a timeout, a connection error (issue #17). "Not
		// reachable" alone sent an operator to the network over a 502.
		"detail": state.Detail,
		// checked_from is the vantage point of THIS result. The admin page
		// runs its own probe from the browser and reports the two separately.
		"checked_from": checkedFromServer,
		// not_checked names the legs this response does not settle. The
		// browser leg is answered by the admin page. The callback leg is
		// answered right below — but only when the document server took the
		// question, so it stays on this list whenever it did not.
		"not_checked": notChecked,
		// service_to_filex is the leg the document server has to walk. checked
		// false means the question could not be put, which is not the same as
		// a broken route and must not be rendered as one.
		"service_to_filex": reverse,
		"public_url":       h.PublicURL,
		// The address the document server was sent to, so the admin page can
		// show what it measured rather than what it assumed.
		"callback_url": callbackURL,
		"advisories":   adv,
		// complete is false whenever anything is unsettled or wrong. It is
		// deliberately NOT "the probe succeeded": that is the conflation this
		// change exists to remove.
		"server_reachable": state.State == "ok",
		"has_warnings":     external.HasWarning(adv),
		// unsaved: this result is about the values the request carried, which
		// differ from the saved row. Nothing was stored.
		"unsaved": unsaved,
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

// testValues is what a Test probes: the saved row, with the form's values
// over it.
type testValues struct {
	enabled  bool
	url      string
	secret   string
	callback string
}

// testValuesOf lays a Test's body over the saved row. unsaved reports whether
// anything differs from the row. A secret that is empty (the form keeps the
// stored one) or the "***" placeholder List returns is not a change.
func testValuesOf(cur *db.ExternalService, d extPatchReq) (testValues, bool) {
	saved := testValues{
		enabled:  cur.Enabled,
		url:      cur.URL,
		secret:   cur.SecretEnc,
		callback: external.CallbackURLFromOptions(cur.OptionsJSON),
	}
	v := saved
	if d.Enabled != nil {
		v.enabled = *d.Enabled
	}
	if d.URL != nil {
		v.url = *d.URL
	}
	if d.Secret != nil && *d.Secret != "" && *d.Secret != redactedSecret {
		v.secret = *d.Secret
	}
	if d.CallbackURL != nil {
		v.callback = *d.CallbackURL
	}
	return v, v != saved
}

func nowOrZero() time.Time { return time.Now() }

// httpAddress: empty (no address), or an absolute http(s) URL with a host.
func httpAddress(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	u, err := neturl.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// _ keeps db pkg import alive even if linter complains (we use db.Store).
var _ db.Store = nil
