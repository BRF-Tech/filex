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
	// Branding is invalidated on branding.* writes so public pages pick the
	// new identity up immediately (wiring:e1). Nil-safe.
	Branding *BrandingSource
	// Appearance is invalidated on ui.default_theme writes, so an operator who
	// sets the instance default sees it take effect on the next page load
	// rather than up to fifteen seconds later (tema:v1). Nil-safe.
	//
	// ⚠ Two caches, not one: branding is keyed per HOST and overlaid per
	// tenant, while the appearance payload is a single instance-wide answer.
	Appearance *AppearanceSource
}

// NewSettings constructs a Settings handler.
func NewSettings(store db.Store) *Settings { return &Settings{Store: store} }

// AttachMailer wires the mailer so the SMTP "Test" button can verify / send.
func (h *Settings) AttachMailer(m *mailer.Service) { h.Mailer = m }

// AttachBranding wires the shared branding source (wiring:e1).
func (h *Settings) AttachBranding(b *BrandingSource) { h.Branding = b }

// AttachAppearance wires the shared theme source (tema:v1).
func (h *Settings) AttachAppearance(a *AppearanceSource) { h.Appearance = a }

// appearanceSettingKey reports whether writing key changes what
// /api/appearance answers.
func appearanceSettingKey(key string) bool { return key == DefaultThemeSettingKey }

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
		// `reason` is what the screen says (mailer.Reason); `error` stays as
		// the administrator's second line, not the sentence.
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "stage": "verify", "reason": mailer.Reason(err), "error": err.Error()})
		return
	}
	to := strings.TrimSpace(req.To)
	if to != "" {
		// The acting admin's language — a language pack's included — and the
		// mail is tagged with it (Content-Language).
		lang := requestLang(r)
		r = r.WithContext(mailer.WithLanguage(r.Context(), lang))
		subject, body := smtpTestMailText(lang)
		if err := h.Mailer.Send(r.Context(), to, subject, body); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "stage": "send", "reason": mailer.Reason(err), "error": err.Error()})
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
	overlayTenantBrandingSettings(r.Context(), m) /* wiring:e1 — tenant branding overlay */
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
	if !allowSettingWrite(w, r, key) {
		return
	}
	/* wiring:e1 — branding keys: validate + tenant-scope + cache bust */
	if isBrandingSettingKey(key) {
		if err := validateBrandingSetting(key, req.Value); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		key = tenantBrandingKey(r.Context(), key)
		defer h.Branding.Invalidate()
	}
	/* tema:v1 — operator stylesheet: run the guard pass, which both caps it
	   and refuses a sheet that could escape its scope wrapper. There is no
	   cache to bust: the sheet is served from /api/me/custom-css, which reads
	   settings live and is `no-store`. */
	if key == CustomCSSSettingKey {
		if err := validateCustomCSS(req.Value); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	/* tema:v1 — the instance default palette. */
	if appearanceSettingKey(key) {
		defer h.Appearance.Invalidate()
	}
	/* tablo:t3 — the instance default folder view: refused if it names a view,
	   a sort or a column the explorer cannot draw, and stored normalised. */
	if key == DefaultFolderViewSettingKey {
		norm, err := normaliseFolderViewDefault(req.Value)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		req.Value = norm
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
	// ⚠ Classify the WHOLE batch before writing any of it. The loop below
	// upserts as it goes, so refusing halfway would leave the tenant admin's
	// allowed keys written and the instance-wide ones not — a partial apply
	// the caller cannot tell from a success. One refusal, nothing written.
	for k, v := range raw {
		if k == "" || v == nil {
			continue
		}
		if !allowSettingWrite(w, r, k) {
			return
		}
		/* tema:v1 — the operator stylesheet is guarded HERE, in the
		   classify-everything-first loop, for the same reason the tenancy
		   check is: refusing it in the write loop below would leave the
		   other keys of the batch already written. */
		if k == CustomCSSSettingKey {
			val, _ := stringifyValue(v)
			if err := validateCustomCSS(val); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		/* tablo:t3 — same reasoning: refuse before anything in the batch is
		   written, not halfway through it. */
		if k == DefaultFolderViewSettingKey {
			val, _ := stringifyValue(v)
			if _, err := normaliseFolderViewDefault(val); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
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
		/* wiring:e1 — branding keys: validate + tenant-scope + cache bust */
		if isBrandingSettingKey(k) {
			if err := validateBrandingSetting(k, val); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			k = tenantBrandingKey(r.Context(), k)
			defer h.Branding.Invalidate()
		}
		/* tema:v1 — the instance default palette changes what the public
		   appearance endpoint answers, so drop its cache. */
		if appearanceSettingKey(k) {
			defer h.Appearance.Invalidate()
		}
		if k == DefaultFolderViewSettingKey {
			val, _ = normaliseFolderViewDefault(val)
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
	overlayTenantBrandingSettings(r.Context(), m) /* wiring:e1 — tenant branding overlay */
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

// ─────────────────── per-key tenancy classification ───────────────────

// allowSettingWrite decides whether this caller may write this settings key,
// answering 403 itself when it may not.
//
// # Why this is an ALLOWLIST and not a list of forbidden keys
//
// `settings` is one flat, global, unrestricted key/value table. `auth.*` (OIDC
// issuer, LDAP bind), `antivirus.*`, SMTP credentials, `share.max_ttl_days`,
// `public_url` all live in it beside `branding.*`, and in multi-tenant mode
// every tenant admin could PATCH any of them — the same class of takeover the
// four gated surfaces in supertenant.go were closed for, reachable by spelling
// a key instead of calling a route.
//
// It cannot be a blanket route gate, because `branding.*` is legitimately
// per-tenant: `tenantBrandingKey` rewrites a tenant admin's `branding.name`
// into `tenant.<id>.branding.name`, which is how a customer brands their own
// login page. Taking that away would be a regression, not a fix.
//
// So the rule is: a key is tenant-writable only if writing it lands somewhere
// that BELONGS to the tenant. Today exactly one namespace does — branding,
// because it is the only one that gets rewritten under a `tenant.<id>.` prefix
// on the way to the store. Everything else is one global row.
//
// ⚠⚠ The direction of the default is the whole point. A denylist would mean
// every key added after today is silently tenant-writable until somebody
// remembers to list it; this way a new key is instance-wide until somebody
// deliberately gives it a per-tenant home. The failure mode of an allowlist is
// a supertenant having to make a change for a tenant. The failure mode of a
// denylist is a tenant rewriting the instance's OIDC issuer.
//
// Single-tenant installs and the supertenant pass everything, as everywhere
// else: `confinedScope` reports false for both.
func allowSettingWrite(w http.ResponseWriter, r *http.Request, key string) bool {
	if _, confined := confinedScope(r.Context()); !confined {
		return true
	}
	if tenantScopedSettingKey(key) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "supertenant_only",
		"message": "\"" + key + "\" is an instance-wide setting; a tenant may only write its own branding.* keys",
	})
	return false
}

// tenantScopedSettingKey reports whether writing key lands in a per-tenant
// namespace rather than the single global row.
//
// ⚠ A tenant admin may only write the BARE `branding.*` form — that is what
// `tenantBrandingKey` rewrites into their own prefix. The already-prefixed
// `tenant.<id>.branding.*` spelling is refused, because it names a tenant
// explicitly and nothing in the rewrite path would stop that id being somebody
// else's: `isBrandingSettingKey` accepts both forms, and `tenantBrandingKey`
// returns an already-prefixed key UNCHANGED. Accepting it here would let a
// tenant admin rebrand another tenant's login page by typing their id.
func tenantScopedSettingKey(key string) bool {
	return strings.HasPrefix(key, "branding.")
}
