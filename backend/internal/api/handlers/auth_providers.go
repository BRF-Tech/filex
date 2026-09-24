// Package handlers — auth_providers.go
//
// Admin → Identity providers: the sign-in providers this instance runs.
//
//	GET    /api/admin/auth-providers
//	PATCH  /api/admin/auth-providers/{name}
//	POST   /api/admin/auth-providers/{name}/test
//
// ⚠⚠ Until v0.43.0 this page wrote `auth.<name>.*` settings that NOTHING
// read — sign-in was built from the environment only, once, at boot — and
// answered "configuration saved; restart the server" to a restart that
// changed nothing (release-candidate sweep, 2026-09-21). The owner's
// decision: the page really manages sign-in, and it can never lock the
// instance out. The rules live in internal/authsetup; this file enforces
// them at the door:
//
//   - a provider the environment defines is read-only here, and says where it
//     comes from (409 environment_managed on a change);
//   - password sign-in and the recovery sign-in are the environment's — the
//     page has no switch for either (409 not_managed_here);
//   - a save runs the real test first; switching a provider ON while its test
//     fails needs `confirm_failed_test` (409 test_failed names the steps);
//   - switching off the last way an administrator can sign in is refused
//     (409 last_sign_in_method);
//   - a secret is sealed (FILEX_SECRET_KEY) and never sent back;
//   - a change is applied at once (authsetup.Live.Reload) and audited with
//     the provider, the switch and the NAMES of the fields that changed.
//
// Instance-wide: only the platform operator (supertenant admin) sees or
// changes any of it. A tenant administrator gets 403 — the sign-in of the
// whole instance, and its secrets, are not a tenant's to read; a tenant's own
// sign-in lives on its provider row (/api/admin/providers).
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// authProvidersAreInstanceWide is the refusal a tenant admin reads.
const authProvidersAreInstanceWide = "authentication drivers apply to the whole instance and are managed by the platform operator"

// AuthProviders handles /api/admin/auth-providers.
type AuthProviders struct {
	Store db.Store
	// Live is the running set of providers; nil on a harness that has none,
	// where nothing can be changed.
	Live *authsetup.Live
	// DemoMode marks a public playground: nothing about sign-in changes there.
	DemoMode bool
}

// NewAuthProviders constructs the handler.
func NewAuthProviders(store db.Store, live *authsetup.Live) *AuthProviders {
	return &AuthProviders{Store: store, Live: live}
}

// providerView is one provider on the page.
type providerView struct {
	Name         string            `json:"name"`
	Capabilities auth.Capabilities `json:"capabilities"`
	// Managed: the page may change it (a kind the page manages, not defined
	// by the environment).
	Managed bool `json:"managed"`
	// Origin: environment | page | builtin.
	Origin string `json:"origin"`
	From   string `json:"from,omitempty"`
	// Enabled: switched on (by the environment or on the page).
	Enabled bool `json:"enabled"`
	// State: running | failed | off.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Legacy: saved on this page before v0.43.0 and never applied; imported
	// switched off for the operator to review.
	Legacy bool `json:"legacy,omitempty"`
	// Shadowed: the page holds a configuration under this name, but the
	// environment defines the provider, so the environment's is the one used.
	Shadowed bool `json:"shadowed,omitempty"`
	// ConfigRedacted is every non-secret value; a secret is never included.
	ConfigRedacted map[string]any `json:"config_redacted"`
	// SecretsSet names the secret fields that hold a value ("set — replace?").
	SecretsSet map[string]bool   `json:"secrets_set"`
	Fields     []authsetup.Field `json:"fields,omitempty"`
	Testable   bool              `json:"testable"`
}

// List returns every sign-in provider and how it stands.
func (h *AuthProviders) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	out, err := h.views(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	body := map[string]any{"providers": out}
	if h.Live != nil {
		set := h.Live.Current()
		body["password_sign_in"] = set.PasswordLogin()
		body["recovery_login"] = set.Recovery()
		box := h.Live.Options().Box
		body["secret_key"] = box != nil && box.Enabled()
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *AuthProviders) views(r *http.Request) ([]providerView, error) {
	names := auth.Names()
	sort.Strings(names)
	var stored map[string]*authsetup.Stored
	if h.Store != nil {
		var err error
		if stored, err = authsetup.LoadStored(r.Context(), h.Store); err != nil {
			return nil, err
		}
	}
	var set *authsetup.Set
	if h.Live != nil {
		set = h.Live.Current()
	}
	out := make([]providerView, 0, len(names))
	for _, n := range names {
		v := providerView{Name: n, State: "off", ConfigRedacted: map[string]any{}, SecretsSet: map[string]bool{}}
		if drv, err := auth.Get(n); err == nil {
			v.Capabilities = drv.Capabilities()
			_, v.Testable = drv.(auth.Prober)
		}
		env, envHas := h.envEntry(n)
		switch {
		case n == "api-token":
			v.Origin, v.Enabled, v.State = "builtin", true, "running"
		case envHas:
			v.Origin, v.From, v.Enabled = authsetup.OriginEnvironment, authsetup.FromWords(env.From, langOf(r)), true
			v.State = stateOf(set, n)
			if e, ok := set.Entry(n); ok {
				v.Error = e.Err
			}
			for k, val := range env.Display {
				v.ConfigRedacted[k] = val
			}
			for _, k := range env.Secrets {
				v.SecretsSet[k] = true
			}
			if s := stored[n]; s != nil && s.Exists {
				v.Shadowed = true
			}
		case !authsetup.IsManaged(n):
			// `local` when the environment does not list it: password sign-in
			// is off, and only the environment can turn it on.
			v.Origin, v.From = authsetup.OriginEnvironment, "FILEX_AUTH_DRIVERS"
		default:
			v.Origin, v.Managed = authsetup.OriginPage, h.Live != nil
			v.Fields = authsetup.Schema[n]
			if s := stored[n]; s != nil {
				v.Enabled, v.Legacy = s.Enabled, s.Legacy
				for k, val := range s.Values {
					f, _ := authsetup.FieldOf(n, k)
					switch f.Kind {
					case authsetup.FieldSecret:
						if val != "" {
							v.SecretsSet[k] = true
						}
					case authsetup.FieldBool:
						v.ConfigRedacted[k] = val == "true"
					default:
						v.ConfigRedacted[k] = val
					}
				}
			}
			if v.Enabled {
				v.State = stateOf(set, n)
				if e, ok := set.Entry(n); ok {
					v.Error = e.Err
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func stateOf(set *authsetup.Set, name string) string {
	if set == nil {
		return "off"
	}
	e, ok := set.Entry(name)
	switch {
	case !ok:
		return "off"
	case e.Driver != nil:
		return "running"
	default:
		return "failed"
	}
}

// updateBody is a save. `config` holds the fields; the flat form (fields at
// the top level beside `enabled`) is what the page and the admin MCP tool
// sent before, and is still read.
type updateBody struct {
	Enabled           *bool
	Config            map[string]any
	ConfirmFailedTest bool
}

// Update saves one provider, applies it at once, and audits it.
func (h *AuthProviders) Update(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body := parseUpdate(raw)

	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	if env, ok := h.Live.EnvDefines(name); ok {
		from := authsetup.FromWords(env.From, langOf(r))
		writeProviderRefusal(w, r, http.StatusConflict, "environment_managed", map[string]any{"from": from},
			"server.auth_provider.environment_managed", srvtext.Vars{"name": name, "from": from})
		return
	}
	if !authsetup.IsManaged(name) {
		writeProviderRefusal(w, r, http.StatusConflict, "not_managed_here", nil, "server.auth_provider.not_managed_here", nil)
		return
	}

	stored, err := authsetup.LoadStored(r.Context(), h.Store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	opts := h.Live.Options()
	cur := stored[name]
	next, changed, err := authsetup.Merge(cur, authsetup.Change{Enabled: body.Enabled, Values: body.Config}, opts.Box)
	if errors.Is(err, authsetup.ErrSecretKeyRequired) {
		writeProviderRefusal(w, r, http.StatusBadRequest, "secret_key_required", nil, "server.auth_provider.secret_key_required", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// The real test, on exactly what is about to be saved.
	checks := []auth.ProbeCheck{}
	if cfg, _, cerr := authsetup.DriverConfig(next, opts.Box, opts); cerr != nil {
		checks = append(checks, auth.Check("secret", auth.ProbeFail))
	} else if drv, gerr := auth.Get(name); gerr == nil {
		if p, ok := drv.(auth.Prober); ok {
			if got := p.Probe(r.Context(), cfg, r); got != nil {
				checks = got
			}
		}
	}
	testOK := auth.ProbeOKAll(checks)
	var failed []string
	for _, c := range checks {
		if c.Status == auth.ProbeFail {
			failed = append(failed, c.ID)
		}
	}

	if next.Enabled && !testOK && !body.ConfirmFailedTest {
		// Nothing is saved. The page asks again, naming the steps that failed;
		// a DISABLED provider saves with a failing test (it runs nothing).
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "test_failed",
			"message": srvtext.Text(langOf(r), "server.auth_provider.confirm_failed_test", nil),
			"checks":  checks,
			"failed":  failed,
		})
		return
	}
	if cur.Enabled && !next.Enabled && stateOf(h.Live.Current(), name) == "running" {
		if paths := h.Live.AdminPaths(r.Context(), name); len(paths) == 0 {
			writeProviderRefusal(w, r, http.StatusConflict, "last_sign_in_method", nil,
				"server.auth_provider.last_sign_in_method", srvtext.Vars{"name": name})
			return
		}
	}

	if err := authsetup.Save(r.Context(), h.Store, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	if err := h.Live.Reload(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
		return
	}

	// ⚠ Names and flags only — never a value (auth.AuditDetail).
	auth.AddAuditDetail(r.Context(), "provider", name)
	auth.AddAuditDetail(r.Context(), "enabled_before", cur.Enabled)
	auth.AddAuditDetail(r.Context(), "enabled_after", next.Enabled)
	if len(changed) > 0 {
		auth.AddAuditDetail(r.Context(), "changed_fields", changed)
	}
	if len(failed) > 0 {
		auth.AddAuditDetail(r.Context(), "test_failed", failed)
		auth.AddAuditDetail(r.Context(), "confirmed_failed_test", body.ConfirmFailedTest)
	}

	views, _ := h.views(r)
	var view *providerView
	for i := range views {
		if views[i].Name == name {
			view = &views[i]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"provider": view,
		"checks":   checks,
		"test_ok":  testOK,
	})
}

func parseUpdate(raw map[string]any) updateBody {
	var b updateBody
	if v, ok := raw["enabled"].(bool); ok {
		b.Enabled = &v
	}
	if v, ok := raw["confirm_failed_test"].(bool); ok {
		b.ConfirmFailedTest = v
	}
	if c, ok := raw["config"].(map[string]any); ok {
		b.Config = c
		return b
	}
	b.Config = map[string]any{}
	for k, v := range raw {
		switch k {
		case "enabled", "confirm_failed_test", "config":
			continue
		}
		b.Config[k] = v
	}
	return b
}

// writeProviderRefusal answers a refused change with a code and the
// catalogue's sentence (a `server.auth_provider.*` key) in the reader's
// language.
func writeProviderRefusal(w http.ResponseWriter, r *http.Request, status int, code string, extra map[string]any, key string, vars srvtext.Vars) {
	body := map[string]any{"error": code, "message": srvtext.Text(langOf(r), key, vars)}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// Test checks a provider's configuration for real, step by step.
//
// ⚠⚠ It used to answer `{"ok": true}` for anything (release-candidate sweep,
// 2026-09-21). A test button that always passes is worse than none.
//
// A page provider is tested on the form as it stands (unsaved edits laid over
// what is stored; a secret left blank is the stored one, opened for the test
// and never returned). A provider the environment defines is tested on the
// environment's configuration — the form is read-only there, so a draft is
// ignored.
func (h *AuthProviders) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	drv, err := auth.Get(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such driver"})
		return
	}
	prober, ok := drv.(auth.Prober)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "testable": false, "ok": false, "checks": []auth.ProbeCheck{}})
		return
	}
	var draft map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&draft)
	}
	if c, ok := draft["config"].(map[string]any); ok {
		draft = c
	}
	var cfg map[string]any
	if env, isEnv := h.envEntry(name); isEnv {
		cfg = env.Config()
	} else {
		opts := authsetup.Options{}
		if h.Live != nil {
			opts = h.Live.Options()
		}
		cur := &authsetup.Stored{Name: name, Values: map[string]string{}}
		if h.Store != nil {
			if stored, err := authsetup.LoadStored(r.Context(), h.Store); err == nil && stored[name] != nil {
				cur = stored[name]
			}
		}
		if cfg, err = authsetup.DraftConfig(cur, draft, opts.Box, opts); err != nil {
			cfg = map[string]any{}
		}
	}
	checks := prober.Probe(r.Context(), cfg, r)
	if checks == nil {
		checks = []auth.ProbeCheck{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "testable": true, "ok": auth.ProbeOKAll(checks), "checks": checks,
	})
}

func (h *AuthProviders) envEntry(name string) (authsetup.Entry, bool) {
	if h.Live == nil {
		return authsetup.Entry{}, false
	}
	return h.Live.EnvDefines(name)
}

func isSecretKey(k string) bool {
	switch strings.ToLower(k) {
	case "client_secret", "secret", "password", "bind_password", "token":
		return true
	}
	return false
}

// stringifyValue renders a JSON value as the settings table stores it.
// (settings.go writes arbitrary settings through it.)
func stringifyValue(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	default:
		b, err := json.Marshal(v)
		return string(b), err
	}
}
