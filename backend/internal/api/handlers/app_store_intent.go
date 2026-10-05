// Package handlers - app_store_intent.go
//
// Installing an app from a store, and the licenses of paid apps
// (internal/appstore, docs/APP-PLUGINS.md → Installing from a store, Paid
// apps; docs/APP-PLUGINS-API.md → The store contract):
//
//	GET    /api/admin/app-plugins/stores                 - the trusted stores
//	POST   /api/admin/app-plugins/stores                 - {store, fingerprints}: trust a store with the keys shown
//	DELETE /api/admin/app-plugins/stores?store=<origin>  - stop trusting it
//	POST   /api/admin/app-plugins/store-intent           - {store, token}: read an install link → its review
//	POST   /api/admin/app-plugins/store-intent/install   - {handle, permissions, associations?, license_key?}
//	POST   /api/admin/app-plugins/store-intent/cancel    - {handle}
//	GET    /api/admin/app-plugins/licenses               - every paid app's license (the panel's warning band)
//	GET    /api/admin/app-plugins/{id}/license
//	PUT    /api/admin/app-plugins/{id}/license           - {key}: a new key, checked at once
//	POST   /api/admin/app-plugins/{id}/license/verify    - check now
//	GET    /api/files/plugins/license/{plugin}           - what the app reads about itself (fx.license.get())
//
// # Who may (the reasoning of app_plugins_admin.go and plugin_session_gate.go)
//
// Every admin route here is the PLATFORM OPERATOR's (requireSupertenant: an
// app runs for every tenant, so a tenant administrator gets 403) and needs an
// administrator SIGNED IN to the panel (requireSession): an API key of any
// scope - an admin key, a `root:`-confined key, an app token - is refused
// 403 session_required, on the reads too. Installing is already a person's
// decision; trusting a store decides whose install links are shown to that
// person at all, and a license key is not for a program to read back.
//
// ⚠ GET changes nothing here. Trusting a store is its own POST, naming the
// fingerprints the administrator was shown; the session cookie is SameSite
// Lax and every state-changing request passes the origin guard
// (internal/originguard), so a link or a page elsewhere cannot trust a store
// for anybody.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// AppStore is the handler set. Svc nil answers 503 (the runtime is off).
type AppStore struct {
	Svc   *appstore.Service
	Admin *AppPluginsAdmin
	// Demo: a public demo trusts no store (its admin account is public).
	Demo bool
	// PublicURL is this filex's configured address (FILEX_PUBLIC_URL, when
	// set): a link's filex_origin must be its origin. Empty: the origin the
	// request arrived at.
	PublicURL string
}

// NewAppStore builds the handler set over the Apps admin handlers it shares
// the review and the install with.
func NewAppStore(svc *appstore.Service, admin *AppPluginsAdmin, demo bool) *AppStore {
	return &AppStore{Svc: svc, Admin: admin, Demo: demo}
}

// MountAdmin registers the admin routes inside /api/admin/app-plugins. The
// static paths are registered before the caller's /{id} routes match them.
func (h *AppStore) MountAdmin(r chi.Router) {
	r.Get("/stores", h.ListStores)
	r.Post("/stores", h.TrustStore)
	r.Delete("/stores", h.UntrustStore)
	r.Post("/store-intent", h.Intent)
	r.Post("/store-intent/install", h.Install)
	r.Post("/store-intent/cancel", h.Cancel)
	r.Get("/licenses", h.Licenses)
	r.Get("/{id}/license", h.License)
	r.Put("/{id}/license", h.PutLicense)
	r.Post("/{id}/license/verify", h.VerifyLicense)
}

// gate: the platform operator, the runtime on, a signed-in administrator.
func (h *AppStore) gate(w http.ResponseWriter, r *http.Request, what string) bool {
	if h.Admin == nil || !h.Admin.gate(w, r) {
		if h.Admin == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "app_plugins_disabled"})
		}
		return false
	}
	if h.Svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "app_plugins_disabled", "message": h.Admin.disabledMessage()})
		return false
	}
	return sessionOnly(w, r, what+" needs an administrator signed in to the admin panel; an API key cannot do it.", nil)
}

// storeFail answers an appstore refusal with its code, its detail and the
// status that says whose move it is.
func storeFail(w http.ResponseWriter, err error) {
	e, ok := appstore.AsError(err)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	code := http.StatusBadRequest
	switch e.Code {
	case appstore.CodeTrustRequired, appstore.CodeKeyChanged, appstore.CodeIntentUsed,
		appstore.CodePinMismatch, appstore.CodeVersionRollback, appstore.CodeSourceChanged:
		code = http.StatusConflict
	case appstore.CodeStoreRefused, appstore.CodeKeyNotConfigured:
		code = http.StatusForbidden
	case appstore.CodeIntentUnknown, appstore.CodeIntentNotFound:
		code = http.StatusNotFound
	case appstore.CodeIntentGone, appstore.CodeIntentExpired:
		code = http.StatusGone
	case appstore.CodeUnreachable, appstore.CodeBadAnswer, appstore.CodeSignature:
		code = http.StatusBadGateway
	}
	writeJSON(w, code, e)
}

func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return false
	}
	return true
}

func actorName(r *http.Request) string {
	if u := auth.UserFrom(r.Context()); u != nil {
		if u.Email != "" {
			return u.Email
		}
		return u.DisplayName
	}
	return ""
}

func actorUserID(r *http.Request) int64 {
	if u := auth.UserFrom(r.Context()); u != nil {
		return u.ID
	}
	return 0
}

// instance is this filex's own origin, what a link's filex_origin is held
// to: the configured public URL's, or - none configured - the origin this
// request arrived at (scheme by TLS or X-Forwarded-Proto, the Host header).
//
// ⚠ A configured public URL that is not an origin is an error, never a fall
// back to the request: an operator who set FILEX_PUBLIC_URL meant the links
// to be held to it (store review, second round, Y4).
func (h *AppStore) instance(r *http.Request) (string, error) {
	if h.PublicURL != "" {
		o, err := appstore.InstanceOrigin(h.PublicURL)
		if err != nil {
			slog.Warn("app-store: FILEX_PUBLIC_URL is not an origin; every store link is refused", slog.String("public_url", h.PublicURL))
			return "", &appstore.Error{Code: appstore.CodeWrongInstance,
				Message: "this filex's FILEX_PUBLIC_URL (" + h.PublicURL + ") is not an address a link can be held to; the operator fixes it, until then no store link is taken",
				Detail:  map[string]any{"public_url_invalid": true}}
		}
		return o, nil
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	o, err := appstore.InstanceOrigin(scheme + "://" + r.Host)
	if err != nil {
		return "", nil
	}
	return o, nil
}

func (h *AppStore) origin(w http.ResponseWriter, raw string) (string, bool) {
	o, err := appstore.NormalizeOrigin(raw, h.Svc.Loopback())
	if err != nil {
		storeFail(w, err)
		return "", false
	}
	return o, true
}

// ── Trusted stores ─────────────────────────────────────────────────────

func (h *AppStore) ListStores(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "listing the trusted stores") {
		return
	}
	list, err := h.Svc.ListTrust(r.Context())
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": list})
}

// TrustStore trusts a store with the keys it publishes, provided they are the
// ones the administrator was shown (`fingerprints`, as the trust question
// listed them).
func (h *AppStore) TrustStore(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "trusting a store") {
		return
	}
	if h.Demo {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": wasmplugin.ErrCodeDemo, "message": "a public demo trusts no store"})
		return
	}
	var req struct {
		Store        string   `json:"store"`
		Fingerprints []string `json:"fingerprints"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	origin, ok := h.origin(w, req.Store)
	if !ok {
		return
	}
	if len(req.Fingerprints) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "fingerprints: the keys you were shown are required"})
		return
	}
	auth.SkipAuditRow(r.Context())
	uid := actorIDOf(r)
	v, err := h.Svc.Approve(r.Context(), origin, req.Fingerprints, uid, actorName(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *AppStore) UntrustStore(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "removing a trusted store") {
		return
	}
	origin, ok := h.origin(w, r.URL.Query().Get("store"))
	if !ok {
		return
	}
	auth.SkipAuditRow(r.Context())
	found, err := h.Svc.Remove(r.Context(), origin, actorIDOf(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Install links ──────────────────────────────────────────────────────

// intentView is the part of a link the review shows. The license key it may
// carry stays on the server; its prefix is shown.
type intentView struct {
	Store            string `json:"store"`
	TokenID          string `json:"token_id"`
	App              string `json:"app"`
	Kind             string `json:"kind"`
	Version          string `json:"version"`
	Repo             string `json:"repo"`
	Ref              string `json:"ref"`
	Commit           string `json:"commit,omitempty"`
	Paid             bool   `json:"paid"`
	LicenseKeyPrefix string `json:"license_key_prefix,omitempty"`
	ExpiresAt        string `json:"expires_at"`
}

func viewOfIntent(in *appstore.Intent) intentView {
	v := intentView{Store: in.Store, TokenID: in.TokenID, App: in.App, Kind: in.Kind, Version: in.Version,
		Repo: in.Repo, Ref: in.Ref, Commit: in.Commit, Paid: in.Paid, ExpiresAt: in.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")}
	if in.LicenseKey != "" {
		v.LicenseKeyPrefix = appstore.Prefix(in.LicenseKey)
	}
	return v
}

// fetchForIntent reads the app from its repository at the commit the store
// signed - the same download a GitHub install makes, at an address a moved
// tag cannot change - and says what it serves. The tag must still serve the
// same manifest there (it pins the module and the interface by hash), or the
// link is refused: the release is no longer what the store approved.
func (h *AppStore) fetchForIntent(ctx context.Context, in *appstore.Intent) (*wasmplugin.InstallInput, *appstore.Source, error) {
	input, err := h.Admin.Registry.FetchGitHub(ctx, wasmplugin.GitHubInput{Repo: in.Repo, Ref: in.Ref, Commit: in.Commit})
	if err != nil {
		return nil, nil, err
	}
	tagged, err := h.Admin.Registry.GitHubManifest(ctx, in.Repo, in.Ref)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(tagged, input.Manifest) {
		return nil, nil, &appstore.Error{Code: appstore.CodePinMismatch,
			Message: "the tag " + in.Ref + " no longer serves the manifest of the commit the store approved; nothing was installed",
			Detail: map[string]any{"mismatches": []appstore.Mismatch{{Field: "commit", Link: in.Ref + "@" + in.Commit,
				Source: "the tag's manifest is sha256 " + appstore.SHA256Hex(tagged)}}}}
	}
	m, err := wasmplugin.ParseManifest(input.Manifest)
	if err != nil {
		return nil, nil, &wasmplugin.InstallError{Code: wasmplugin.ErrCodeManifestInvalid, Message: err.Error()}
	}
	src := &appstore.Source{ManifestBytes: input.Manifest, Name: m.Name, Version: m.Version, Kind: wasmplugin.KindApp,
		DeclaredPermissions: m.Permissions}
	if m.IsLanguagePack() {
		src.Kind = wasmplugin.KindLanguagePack
	}
	if m.Wasm != nil {
		src.WasmSHA256 = m.Wasm.SHA256
	}
	if m.UI != nil {
		src.UISHA256 = m.UI.Bundle.SHA256
	}
	return input, src, nil
}

// installedSource is where the installed app of a link's name came from:
// the store it was installed from ("" = none: a repository, an address or an
// upload), the GitHub repository it follows ("" = not one).
type installedSource struct {
	Store     string `json:"store"`
	Repo      string `json:"repo"`
	Version   string `json:"version"`
	SourceURL string `json:"source_url,omitempty"`
}

// sourceOf reads where p came from: the store from internal/appstore, the
// repository from the app's own row (what is installed, whatever a store
// said).
func (h *AppStore) sourceOf(ctx context.Context, p *wasmplugin.Installed) installedSource {
	src := installedSource{Version: p.Row.Version, SourceURL: p.Row.SourceURL}
	src.Store, _ = h.Svc.InstalledFrom(ctx, p.Row.Name)
	if p.Row.Source == "github" {
		repo, _, _ := strings.Cut(strings.TrimPrefix(p.Row.SourceURL, "https://github.com/"), "@")
		src.Repo = repo
	}
	return src
}

// installedFor is the app of the link's name already here. It refuses a link
// for an app that came from somewhere else - another store, or another
// repository (CodeSourceChanged: such a link would move the app, and a paid
// app's license key, to whoever signed it; the administrator removes the app
// first) - and a link that would put back an older version or the same one
// (a replayed old link, or a store's mistake): a store installs or upgrades,
// never downgrades.
//
// An app installed from its repository directly (no store) is upgraded by a
// store's FREE link for that same repository: nothing of another store's is
// in it. A PAID link for it is refused the same way: it would put an app
// nobody bought under a store's license - one the store could hold whenever
// it liked, with removing the app (and its data) the only way out (store
// review, second round, Y3).
func (h *AppStore) installedFor(ctx context.Context, origin string, in *appstore.Intent) (*wasmplugin.Installed, *installedSource, error) {
	p, ok := h.Admin.Registry.ByName(in.App)
	if !ok {
		return nil, nil, nil
	}
	src := h.sourceOf(ctx, p)
	adopts := src.Store == "" && in.Paid
	if !strings.EqualFold(src.Repo, in.Repo) || (src.Store != "" && src.Store != origin) || adopts {
		msg := in.App + " is installed from " + describeSource(src) + "; this link installs it from " + in.Repo + " through " + origin +
			". A store link does not move an app to another store or repository: remove the installed app first."
		if adopts && strings.EqualFold(src.Repo, in.Repo) {
			msg = in.App + " was installed from its repository, not from a store; " + origin +
				"'s paid link does not take an installed app under its license: remove the installed app first."
		}
		return nil, &src, &appstore.Error{Code: appstore.CodeSourceChanged,
			Message: msg,
			Detail:  map[string]any{"installed": src, "link": installedSource{Store: origin, Repo: in.Repo, Version: in.Version}}}
	}
	cur, err1 := update.ParseVersion(p.Row.Version)
	next, err2 := update.ParseVersion(in.Version)
	if err1 != nil || err2 != nil || next.Compare(cur) <= 0 {
		return nil, &src, &appstore.Error{Code: appstore.CodeVersionRollback,
			Message: in.App + " " + p.Row.Version + " is installed; the link is for " + in.Version + ", which is not newer. A store link installs or upgrades, never goes back.",
			Detail:  map[string]any{"installed": p.Row.Version, "link": in.Version}}
	}
	return p, &src, nil
}

func describeSource(src installedSource) string {
	where := src.Repo
	if where == "" {
		where = "an upload or an address"
		if src.SourceURL != "" {
			where = src.SourceURL
		}
	}
	if src.Store != "" {
		where += " through " + src.Store
	}
	return where
}

// Intent reads an install link and answers its review: the store must be
// trusted (otherwise the answer is the trust question - the store and its
// key fingerprints - and nothing was fetched from the link), the link signed,
// current and unused, and what the repository serves must be what the store
// pinned. Then the same dry run a GitHub install's review is.
func (h *AppStore) Intent(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "installing an app from a store") {
		return
	}
	auth.SkipAuditRow(r.Context())
	var req struct {
		Store string `json:"store"`
		Token string `json:"token"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	origin, ok := h.origin(w, req.Store)
	if !ok {
		return
	}
	self, err := h.instance(r)
	if err != nil {
		storeFail(w, err)
		return
	}
	in, err := h.Svc.ReadIntent(r.Context(), origin, req.Token, self)
	if err != nil {
		storeFail(w, err)
		return
	}
	input, src, err := h.fetchForIntent(r.Context(), in)
	if err != nil {
		h.failInstall(w, err)
		return
	}
	if err := appstore.ComparePins(in, src); err != nil {
		storeFail(w, err)
		return
	}
	was, wasFrom, err := h.installedFor(r.Context(), origin, in)
	if err != nil {
		storeFail(w, err)
		return
	}
	input.DryRun = true
	input.Lang = langOf(r)
	input.ActorID = actorIDOf(r)
	var dry *wasmplugin.DryRunAnswer
	if was != nil {
		_, dry, err = h.Admin.Registry.Upgrade(r.Context(), was.Row.ID, input)
	} else {
		_, dry, err = h.Admin.Registry.Install(r.Context(), input)
	}
	if err != nil {
		h.failInstall(w, err)
		return
	}
	// The bytes the dry run hashed, against the link (the manifest's hash was
	// compared above; this is the module filex actually downloaded).
	if in.WasmSHA256 != "" && dry.WasmSHA256 != "" && dry.WasmSHA256 != in.WasmSHA256 {
		storeFail(w, &appstore.Error{Code: appstore.CodePinMismatch, Message: "the module downloaded is not the one the store approved",
			Detail: map[string]any{"mismatches": []appstore.Mismatch{{Field: "wasm_sha256", Link: in.WasmSHA256, Source: dry.WasmSHA256}}}})
		return
	}
	h.Admin.fileTypesOf(r, dry, was)
	p := h.Svc.Hold(origin, req.Token, in, actorUserID(r))
	body := map[string]any{
		"handle": p.Handle, "store": origin, "store_trust": h.Svc.TrustStatus(r.Context(), origin),
		"intent": viewOfIntent(in), "review": dry,
	}
	if was != nil {
		// Where the installed app came from, beside what the link brings: the
		// review shows both (a link never moves an app to another source).
		body["upgrade_of"] = map[string]any{"id": was.Row.ID, "version": was.Row.Version,
			"store": wasFrom.Store, "repo": wasFrom.Repo, "source_url": wasFrom.SourceURL}
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *AppStore) failInstall(w http.ResponseWriter, err error) {
	if _, ok := appstore.AsError(err); ok {
		storeFail(w, err)
		return
	}
	h.Admin.fail(w, err)
}

// Install installs (or upgrades) what a reviewed link names: the repository
// is read AGAIN and held to the link's pins again - what the administrator
// approved is what lands, not what the source serves by now - then the same
// install a GitHub install is. A paid app is held from before it is
// installed until the store confirms its license.
func (h *AppStore) Install(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "installing an app from a store") {
		return
	}
	auth.SkipAuditRow(r.Context())
	var req struct {
		Handle       string            `json:"handle"`
		Permissions  []string          `json:"permissions"`
		Associations []assoc.Placement `json:"associations"`
		LicenseKey   string            `json:"license_key"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	// ⚠⚠ The review is TAKEN (read and removed at once): of two installs of
	// one review (a double click, or on purpose) one proceeds, the other is
	// told the review is not open. A failure below puts it back.
	p, err := h.Svc.Take(req.Handle, actorUserID(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	in := p.Intent
	ctx := r.Context()
	// One store install of an app at a time: the license, the install and
	// the record of where it came from are one step for one name.
	unlock := h.Svc.LockApp(in.App)
	defer unlock()
	installed := false
	defer func() {
		if !installed {
			h.Svc.PutBack(p)
		}
	}()
	input, src, err := h.fetchForIntent(ctx, in)
	if err != nil {
		h.failInstall(w, err)
		return
	}
	if err := appstore.ComparePins(in, src); err != nil {
		storeFail(w, err)
		return
	}
	was, _, err := h.installedFor(ctx, p.Origin, in)
	if err != nil {
		storeFail(w, err)
		return
	}
	key := strings.TrimSpace(req.LicenseKey)
	if key == "" {
		key = in.LicenseKey
	}
	actor := actorIDOf(r)
	var undo *appstore.Undo
	if in.Paid {
		// Held from BEFORE the install: the app never runs unlicensed.
		if undo, err = h.Svc.RequireUndoable(ctx, in.App, p.Origin, key, actor); err != nil {
			storeFail(w, err)
			return
		}
	}
	input.Granted = req.Permissions
	input.Lang = langOf(r)
	input.ActorID = actor
	input.Placements = req.Associations
	before := handledBy(was)
	var st *wasmplugin.Status
	if was != nil {
		st, _, err = h.Admin.Registry.Upgrade(context.WithoutCancel(ctx), was.Row.ID, input)
	} else {
		st, _, err = h.Admin.Registry.Install(context.WithoutCancel(ctx), input)
	}
	if err != nil {
		// Only what THIS request did is taken back: the license row it
		// created, never one another install made (Undo).
		h.Svc.Undo(context.WithoutCancel(ctx), undo)
		h.failInstall(w, err)
		return
	}
	installed = true
	var only map[string]bool
	if was != nil && len(req.Associations) > 0 {
		only = map[string]bool{}
		if now, ok := h.Admin.Registry.ByName(in.App); ok {
			only = assoc.NewKinds(before, handledBy(now))
		}
	}
	assocErrs := h.Admin.place(r, st.Name, req.Associations, only)
	body := map[string]any{"association_errors": assocErrs}
	if in.Paid && key != "" {
		lic, lerr := h.Svc.Check(context.WithoutCancel(ctx), in.App, actor)
		if lerr == nil {
			body["license"] = lic
		}
	} else if in.Paid {
		if lic, _ := h.Svc.LicenseOf(ctx, in.App); lic != nil {
			body["license"] = lic
		}
	}
	h.Svc.Finish(context.WithoutCancel(ctx), p, "installed", actor)
	if now, ok := h.Admin.Registry.ByName(in.App); ok {
		st = h.Admin.Registry.StatusOf(now)
	}
	body["plugin"] = st
	writeJSON(w, http.StatusCreated, body)
}

// Cancel ends a reviewed link without installing: the store is told
// `cancelled`, and the link is used up here.
func (h *AppStore) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "cancelling a store install") {
		return
	}
	auth.SkipAuditRow(r.Context())
	var req struct {
		Handle string `json:"handle"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	p, err := h.Svc.Take(req.Handle, actorUserID(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	h.Svc.Finish(context.WithoutCancel(r.Context()), p, "cancelled", actorIDOf(r))
	w.WriteHeader(http.StatusNoContent)
}

// ── Licenses ───────────────────────────────────────────────────────────

func (h *AppStore) Licenses(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "reading licenses") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"licenses": h.Svc.Licenses(r.Context())})
}

func (h *AppStore) License(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "reading a license") {
		return
	}
	p, ok := h.Admin.plugin(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.LicenseOf(r.Context(), p.Row.Name)
	if err != nil {
		storeFail(w, err)
		return
	}
	if v == nil {
		v = &appstore.View{App: p.Row.Name, Required: false, Status: appstore.StatusFree}
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *AppStore) PutLicense(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "changing a license key") {
		return
	}
	p, ok := h.Admin.plugin(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.SetKey(context.WithoutCancel(r.Context()), p.Row.Name, req.Key, actorIDOf(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *AppStore) VerifyLicense(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "checking a license") {
		return
	}
	p, ok := h.Admin.plugin(w, r)
	if !ok {
		return
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.Check(context.WithoutCancel(r.Context()), p.Row.Name, actorIDOf(r))
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// AppLicense answers what an app reads about its own license: the status,
// and for a valid one its dates - never the key, the store or the holder
// (every user who may run apps, and any token with that right, reads this).
// A free app answers {"status": "free"}.
func (h *AppStore) AppLicense(w http.ResponseWriter, r *http.Request) {
	if h.Admin == nil || h.Admin.Registry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	name := chi.URLParam(r, "plugin")
	if _, ok := h.Admin.Registry.ByName(name); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if h.Svc == nil {
		writeJSON(w, http.StatusOK, appstore.AppView{Status: appstore.StatusFree})
		return
	}
	writeJSON(w, http.StatusOK, h.Svc.AppLicense(r.Context(), name))
}
