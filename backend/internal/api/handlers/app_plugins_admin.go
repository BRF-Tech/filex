// Package handlers — app_plugins_admin.go
//
// Admin surface for app plugins (internal/wasmplugin, docs/APP-PLUGINS.md):
//
//	GET    /api/admin/app-plugins                — runtime facts + every plugin
//	POST   /api/admin/app-plugins[?dry_run=1]    — install: multipart {wasm?, ui?, manifest, signature?, grant} | JSON {github_repo, ref?, permissions} | JSON {url?, manifest_url, sha256?, permissions}
//	                                             (no module — `wasm`/`url` — for a language pack; wire.Manifest.IsLanguagePack)
//	GET    /api/admin/app-plugins/{id}
//	PATCH  /api/admin/app-plugins/{id}           — {"enabled": bool}
//	POST   /api/admin/app-plugins/{id}/rollback  — back to the version the last upgrade replaced
//	POST   /api/admin/app-plugins/{id}/upgrade   — same bodies as install
//	POST   /api/admin/app-plugins/updates/check  — check every app's source now (updates.go)
//	DELETE /api/admin/app-plugins/{id}
//	GET/PUT /api/admin/app-plugins/{id}/settings
//	GET/PUT /api/admin/app-plugins/{id}/overrides
//	GET    /api/admin/app-plugins/{id}/logs?after=N
//
// ⚠ Instance-wide, never tenant-scoped, for the same reason storage plugins
// are: a plugin's actions appear in every tenant's file menu. Only the
// supertenant may touch this surface.
//
// ⚠⚠ Installing, upgrading, going back, switching, removing and the action
// overrides need an administrator SIGNED IN to the panel (requireSession): an
// API key gets 403 and is pointed at /api/admin/plugin-requests. The review
// (?dry_run=1), the list, a detail, the logs and the update check stay open
// to an admin-scoped key.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// AppPluginsAdmin is the handler set. Registry may be nil when the runtime is
// disabled by configuration; every route then answers 503.
type AppPluginsAdmin struct {
	Registry *wasmplugin.Registry
	// DisabledReason is shown when Registry is nil (config flag vs. arch).
	DisabledReason string
	// Audit records an administrator's forced unlock; nil = not recorded.
	Audit func(ctx context.Context, userID *int64, action string, storageID int64, rel, ip string) error
}

// NewAppPluginsAdmin constructs the handler.
func NewAppPluginsAdmin(reg *wasmplugin.Registry, disabledReason string) *AppPluginsAdmin {
	return &AppPluginsAdmin{Registry: reg, DisabledReason: disabledReason}
}

func (h *AppPluginsAdmin) gate(w http.ResponseWriter, r *http.Request) bool {
	if !requireSupertenant(w, r, "app plugins are managed by the platform operator") {
		return false
	}
	if h.Registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "app_plugins_disabled",
			"message": h.disabledMessage(),
		})
		return false
	}
	return true
}

func (h *AppPluginsAdmin) disabledMessage() string {
	if h.DisabledReason != "" {
		return h.DisabledReason
	}
	return "app plugins are disabled on this instance (FILEX_APP_PLUGINS_DISABLED)"
}

// runtimeFacts is the header of the list answer.
//
// `filex_version` / `compat_enforced`: the filex apps' `filex` ranges are
// judged against, and whether they are at all (not on a development build —
// wasmplugin/compat.go). `update_check`: the daily update check runs;
// `updates_checked_at`: when the last check ran (absent: never).
func (h *AppPluginsAdmin) runtimeFacts(ctx context.Context) map[string]any {
	if h.Registry == nil {
		return map[string]any{
			"enabled": false, "arch_ok": wasmplugin.ArchSupported(), "disabled_reason": h.disabledMessage(),
			"requires_signature": false, "engines": map[string]bool{},
			"filex_version": wasmplugin.FilexVersion(), "compat_enforced": wasmplugin.CompatEnforced(), "update_check": false,
		}
	}
	facts := map[string]any{
		"enabled": true, "arch_ok": true, "disabled_reason": "",
		"requires_signature": h.Registry.RequiresSignature(), "engines": h.Registry.Engines(),
		"engine_names":    engineNames(),
		"dir":             h.Registry.Dir(),
		"filex_version":   wasmplugin.FilexVersion(),
		"compat_enforced": wasmplugin.CompatEnforced(),
		"update_check":    h.Registry.BackgroundUpdates(),
	}
	if t := h.Registry.LastUpdateCheck(ctx); !t.IsZero() {
		facts["updates_checked_at"] = t
	}
	return facts
}

// engineNames is every engine id with the name a person reads
// (enginebin.DisplayName): the panel printed "libreoffice", "rsvg" beside the
// install review's "LibreOffice", "librsvg" for the same programs. ONE
// spelling, the server's, so the panel keeps no list of its own.
func engineNames() map[string]string {
	out := map[string]string{}
	for _, id := range enginebin.Names() {
		out[id] = enginebin.DisplayName(id)
	}
	return out
}

func (h *AppPluginsAdmin) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "app plugins are managed by the platform operator") {
		return
	}
	// The list is answered even when the runtime is off, so the panel can
	// show WHY there is nothing to list instead of a bare 503.
	writeJSON(w, http.StatusOK, h.listBody(r.Context()))
}

// listBody is the answer of GET /api/admin/app-plugins, and the part of
// "Check now"'s answer the list redraws from.
func (h *AppPluginsAdmin) listBody(ctx context.Context) map[string]any {
	list := []*wasmplugin.Status{}
	if h.Registry != nil {
		for _, p := range h.Registry.All() {
			list = append(list, h.Registry.StatusOf(p))
		}
	}
	return map[string]any{"runtime": h.runtimeFacts(ctx), "plugins": list}
}

// CheckUpdates asks every app's source for a newer version now, applies
// what may be applied, and answers what happened with the list redrawn:
// POST /api/admin/app-plugins/updates/check. A check already running (the
// daily one) is waited for rather than doubled.
//
// ⚠ Detached from the request: a check installs apps, and an administrator
// who closes the tab must not cut one in half (Upgrade holds its own swap
// either way).
func (h *AppPluginsAdmin) CheckUpdates(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rep, err := h.Registry.CheckUpdates(context.WithoutCancel(r.Context()))
	if err != nil {
		h.fail(w, err)
		return
	}
	body := h.listBody(r.Context())
	body["report"] = rep
	writeJSON(w, http.StatusOK, body)
}

func (h *AppPluginsAdmin) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	settings, fields, err := h.Registry.Settings(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	overrides, err := h.Registry.Overrides(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	// The schedule of an app that is woken: the wake-up row (key "") says
	// when the next one is and what the last one decided; the others are the
	// work it asked for. Empty for every app without the permission, and a
	// read failure costs the drawer this one section rather than the page.
	schedule, err := h.Registry.ScheduleOf(r.Context(), p.Row.ID)
	if err != nil {
		schedule = nil
	}
	// What a wake-up decided is stored in every language the app speaks;
	// the drawer reads it in the reader's (wasmplugin.wakeNote).
	for _, it := range schedule {
		it.Error = wasmplugin.LocalizeNote(it.Error, langOf(r))
	}
	writeJSON(w, http.StatusOK, appPluginDetailBody(h.Registry.StatusOf(p), p, langOf(r), settings, fields, overrides, schedule))
}

// appPluginDetailBody is the answer of GET /api/admin/app-plugins/{id}.
//
// ⚠⚠ Built HERE and nowhere else, because a test serialises it into the wire
// fixture the web client's tests run against (app_plugins_wire_test.go →
// testdata/wire/). Twice this release the browser typed one of these answers
// differently from what this code sends, and both times every test stayed
// green because its fixture was typed the browser's way: the drawer drew a
// signed app as "Unsigned" (the app's own fields arrive under `plugin`, the
// client read them flat), then the install review printed each permission's
// reason as raw `{"en": …, "tr": …}` (`reason` is a wire.Text, the client said
// string). A fixture the SERVER writes cannot be typed the browser's way.
//
// ⚠ `permissions` here is NOT the list of permission ids `plugin.permissions`
// carries — it is the reviewed rows (id, label in `lang`, reason as {en, tr}),
// the same PermissionRows the install review shows. `granted` is the id list.
func appPluginDetailBody(st *wasmplugin.Status, p *wasmplugin.Installed, lang string, settings map[string]string, fields []wire.Field, overrides []wasmplugin.OverrideRow, schedule []*model.AppPluginScheduleItem) map[string]any {
	perms := make([]string, 0, len(p.Perms))
	for _, x := range p.Perms {
		perms = append(perms, string(x))
	}
	return map[string]any{
		"plugin":         st,
		"manifest":       p.Manifest.Manifest,
		"granted":        perms,
		"permissions":    wasmplugin.PermissionRows(p.Manifest, lang),
		"settings":       settings,
		"setting_fields": fields,
		"overrides":      overrides,
		"schedule":       schedule,
	}
}

func (h *AppPluginsAdmin) plugin(w http.ResponseWriter, r *http.Request) (*wasmplugin.Installed, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil, false
	}
	p, ok := h.Registry.ByID(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return nil, false
	}
	return p, true
}

// fail maps registry errors to HTTP: caller mistakes are 4xx with a code the
// wizard switches on; everything else is a 500.
func (h *AppPluginsAdmin) fail(w http.ResponseWriter, err error) {
	var ie *wasmplugin.InstallError
	if errors.As(err, &ie) {
		code := http.StatusBadRequest
		switch ie.Code {
		case wasmplugin.ErrCodeNameTaken, wasmplugin.ErrCodeDescribeMismatch, wasmplugin.ErrCodePermissionsChanged, wasmplugin.ErrCodeIncompatible:
			code = http.StatusConflict
		case wasmplugin.ErrCodeNotFound:
			code = http.StatusNotFound
		case wasmplugin.ErrCodeDemo:
			code = http.StatusForbidden
		case wasmplugin.ErrCodeTooLarge:
			code = http.StatusRequestEntityTooLarge
		case wasmplugin.ErrCodeUpToDate:
			code = http.StatusConflict
		case wasmplugin.ErrCodeFetch:
			code = http.StatusBadGateway
		}
		writeJSON(w, code, installErrorBody(ie))
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// installErrorBody is a refused install on the wire. Built here and nowhere
// else, because app_plugins_wire_test.go serialises it into the fixture the
// install wizard's tests read (see appPluginDetailBody for why).
//
// fetch_failed says WHY (`reason`) and WHAT (`where`, `refs`, `status`), so
// the wizard writes the sentence in the reader's language and says what to
// check; `incompatible` says the range (`requires`) and this filex (`filex`);
// the English `message` stays for the API and the log. The shape is
// wasmplugin.InstallRefusal — the same one an update check's failure is
// stored in, so the Apps list explains it with the wizard's sentences.
func installErrorBody(ie *wasmplugin.InstallError) *wasmplugin.InstallRefusal {
	return wasmplugin.RefusalOf(ie)
}

// readInstall gathers an InstallInput from any of the three bodies — or, for
// an upgrade (upgradeID > 0), from `{"from_source": true}`: the newer version
// the app's own source has, found and fetched exactly as the update check
// does (wasmplugin.FetchUpdate).
func (h *AppPluginsAdmin) readInstall(w http.ResponseWriter, r *http.Request, upgradeID int64) (*wasmplugin.InstallInput, bool) {
	ct := r.Header.Get("Content-Type")
	dry := r.URL.Query().Get("dry_run") == "1" || r.URL.Query().Get("dry_run") == "true"
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
			return nil, false
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		mf, _, err := r.FormFile("manifest")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "manifest file is required"})
			return nil, false
		}
		// ⚠ Read one byte past the ceiling and REFUSE, never truncate: a
		// LimitReader alone hands the parser the first N bytes of a longer
		// document, and the admin is told the JSON is malformed ("unexpected
		// end of input") about a manifest that is merely large. The ceiling is
		// sized for complete translations (wire.MaxManifestBytes).
		manifest, err := io.ReadAll(io.LimitReader(mf, wire.MaxManifestBytes+1))
		mf.Close()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "manifest unreadable"})
			return nil, false
		}
		if len(manifest) > wire.MaxManifestBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": wasmplugin.ErrCodeTooLarge, "message": "the manifest is larger than " + strconv.Itoa(wire.MaxManifestBytes>>20) + " MiB"})
			return nil, false
		}
		// ⚠ The module is OPTIONAL here, and the registry decides: a language
		// pack (a manifest that only adds languages) installs from its
		// manifest alone and REFUSES a module; every other app refuses to
		// install without one ("no module supplied"). Deciding it here would
		// be a second copy of wire.Manifest.IsLanguagePack.
		var wasmR io.Reader
		wf, _, err := r.FormFile("wasm")
		switch {
		case errors.Is(err, http.ErrMissingFile):
		case err != nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "wasm unreadable"})
			return nil, false
		default:
			wasm, rerr := io.ReadAll(wf)
			wf.Close()
			if rerr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "wasm unreadable"})
				return nil, false
			}
			wasmR = strings.NewReader(string(wasm))
		}
		// The interface bundle (a zip), when the manifest has a `ui` block.
		// Optional here for the same reason the module is: the registry
		// decides, from the manifest (wasmplugin.stageUI).
		var uiR io.Reader
		uf, _, err := r.FormFile("ui")
		switch {
		case errors.Is(err, http.ErrMissingFile):
		case err != nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "ui bundle unreadable"})
			return nil, false
		default:
			// Read here, like the module: the form's files go when this
			// request's multipart form is removed. One byte past the ceiling
			// so the registry refuses it as too large, never truncated.
			b, rerr := io.ReadAll(io.LimitReader(uf, h.Registry.MaxUIBytes()+1))
			uf.Close()
			if rerr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "ui bundle unreadable"})
				return nil, false
			}
			uiR = bytes.NewReader(b)
		}
		var granted []string
		if g := strings.TrimSpace(r.FormValue("grant")); g != "" {
			var body struct {
				Permissions []string `json:"permissions"`
			}
			if err := json.Unmarshal([]byte(g), &body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad grant json"})
				return nil, false
			}
			granted = body.Permissions
		}
		return &wasmplugin.InstallInput{
			Manifest: manifest, Wasm: wasmR, UI: uiR, Signature: r.FormValue("signature"),
			Source: "upload", Granted: granted, DryRun: dry, Lang: langOf(r), ActorID: actorIDOf(r),
		}, true
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return nil, false
	}
	var req struct {
		wasmplugin.GitHubInput
		wasmplugin.URLInput
		FromSource  bool     `json:"from_source"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return nil, false
	}
	var in *wasmplugin.InstallInput
	switch {
	case req.FromSource && upgradeID > 0:
		in, err = h.Registry.FetchUpdate(r.Context(), upgradeID)
	case req.FromSource:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "from_source upgrades an installed app; an install names its source"})
		return nil, false
	case strings.TrimSpace(req.Repo) != "":
		in, err = h.Registry.FetchGitHub(r.Context(), req.GitHubInput)
	// ⚠ The MANIFEST address decides a URL install, not the module's: a
	// language pack has no module, so its body is `{"url": "", "manifest_url":
	// …}` — and keyed on `url` alone it fell through to "give github_repo, or
	// url + manifest_url", refusing every pack the wizard's From-a-URL tab and
	// the docs offer. FetchURL says what is missing for an app with a module.
	case strings.TrimSpace(req.URL) != "" || strings.TrimSpace(req.ManifestURL) != "":
		in, err = h.Registry.FetchURL(r.Context(), req.URLInput)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest_invalid", "message": "give github_repo, or url + manifest_url, or a multipart upload"})
		return nil, false
	}
	if err != nil {
		h.fail(w, err)
		return nil, false
	}
	in.Granted = req.Permissions
	in.DryRun = dry
	in.Lang = langOf(r)
	in.ActorID = actorIDOf(r)
	return in, true
}

// Install installs an app — or, with ?dry_run=1, answers its review and
// installs nothing. The review is open to an admin-scoped API key (it is how
// an agent sees what it would be asking for); the install itself needs a
// signed-in administrator (requireSession).
func (h *AppPluginsAdmin) Install(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if !isDryRun(r) && !requireSession(w, r, "installing an app") {
		return
	}
	in, ok := h.readInstall(w, r, 0)
	if !ok {
		return
	}
	st, dry, err := h.Registry.Install(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	if dry != nil {
		writeJSON(w, http.StatusOK, dry)
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func (h *AppPluginsAdmin) Upgrade(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if !isDryRun(r) && !requireSession(w, r, "upgrading an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	in, ok := h.readInstall(w, r, p.Row.ID)
	if !ok {
		return
	}
	st, dry, err := h.Registry.Upgrade(r.Context(), p.Row.ID, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	if dry != nil {
		writeJSON(w, http.StatusOK, dry)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *AppPluginsAdmin) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "switching an app on or off") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled    *bool `json:"enabled"`
		AutoUpdate *bool `json:"auto_update"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		msg := "enabled (bool) required"
		if req.AutoUpdate != nil {
			// ⚠ filex 0.48: nothing updates itself any more — every newer
			// version waits for an administrator (wasmplugin/updates.go).
			msg = "automatic updates were removed: every newer version waits for an administrator's approval (Review update)"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": msg})
		return
	}
	st, err := h.Registry.SetEnabled(r.Context(), p.Row.ID, *req.Enabled)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// Rollback puts the version an upgrade replaced back:
// POST /api/admin/app-plugins/{id}/rollback. No new approval — that version's
// grant was approved when it was installed (wasmplugin/versions.go).
func (h *AppPluginsAdmin) Rollback(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "putting an app back to its previous version") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	st, err := h.Registry.Rollback(context.WithoutCancel(r.Context()), p.Row.ID, actorIDOf(r), langOf(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// actorIDOf is the signed-in administrator's id, for the audit row.
func actorIDOf(r *http.Request) *int64 {
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		return &id
	}
	return nil
}

func (h *AppPluginsAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "removing an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	if err := h.Registry.Remove(r.Context(), p.Row.ID); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AppPluginsAdmin) GetSettings(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	values, fields, err := h.Registry.Settings(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values, "fields": fields})
}

func (h *AppPluginsAdmin) PutSettings(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var req struct {
		Values map[string]string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Registry.PutSettings(r.Context(), p.Row.ID, req.Values); err != nil {
		h.fail(w, err)
		return
	}
	h.GetSettings(w, r)
}

func (h *AppPluginsAdmin) GetOverrides(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	rows, err := h.Registry.Overrides(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": rows})
}

// PutOverrides changes who may use an app's actions and where (admin_only,
// enabled, applies): a permission change, so a signed-in administrator's.
func (h *AppPluginsAdmin) PutOverrides(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "changing who may use an app's actions") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var req struct {
		Actions []wasmplugin.OverrideRow `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Registry.PutOverrides(r.Context(), p.Row.ID, req.Actions); err != nil {
		h.fail(w, err)
		return
	}
	h.GetOverrides(w, r)
}

func (h *AppPluginsAdmin) Logs(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, next, err := h.Registry.Logs(p.Row.ID, after)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "next": next})
}

// langOf is the language of everything a signed-in caller is answered in by
// an app or about an app: the locale an app's call runs in (views, jobs,
// their messages), the app's own labels (textOf) and the install review.
//
// ⚠⚠ The ONE rule (requestLang, public_i18n.go): the account's language, else
// the browser's, else the instance default, else English — each only when this
// server speaks it (English, Turkish, or a language a pack adds), as a tag
// that keeps its region (`pt-br`). It was `normLang`, which answered "tr" for
// anything starting with tr and "en" for EVERYTHING else: a Spanish user's
// signing wizard was told `en`, so an app that ships Spanish could never show
// it. English and Turkish resolve exactly as before; an app that lacks the
// language falls back to English per string (wire.Text.Get).
func langOf(r *http.Request) string { return requestLang(r) }

// pluginLang is the language an app answers one SCREEN in: the language the
// screen itself is drawn in when the client names it (`?lang=`, which the
// explorer puts on every app call — packages/core useFileApi), then langOf
// (the account's language, then Accept-Language).
//
// ⚠⚠ Why the screen outranks the account here, and only here: an embedded
// explorer (`<filex-explorer>` with `config.locale`) draws the language its
// HOST page chose, and nobody asked the account. An app picks its plain
// strings — a form field's label and help, a select's options — by
// `context.locale`, so a Turkish popup over an English account got
// "Identity" and "One signer per line…" in the middle of a Turkish wizard
// (2026-09-26). Accept-Language cannot carry that choice: it ranks below the
// account on purpose, because for any other client it is the language the
// browser was installed in. An unknown `?lang=` is ignored (srvtext.Pick).
func pluginLang(r *http.Request) string {
	if r == nil {
		return langOf(r)
	}
	return requestLang(r, r.URL.Query().Get("lang"))
}

// textOf is Text.Get with the request's language.
func textOf(t wire.Text, r *http.Request) string { return t.Get(langOf(r)) }

// SigningCA answers the tenant CA certificate (PEM) that readers import to
// trust the signatures app plugins make here. The CA is created on first
// use. Supertenant only (the admin surface), per the caller's tenant.
func (h *AppPluginsAdmin) SigningCA(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	pemText, err := h.Registry.CACertPEM(r.Context(), signingTenantOf(r))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "signing_unavailable", "message": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=\"filex-signing-ca.pem\"")
	_, _ = io.WriteString(w, pemText)
}

// RotateSigningCA retires the current CA; the next signature mints a new
// one. Old signatures stay verifiable with the old certificate.
func (h *AppPluginsAdmin) RotateSigningCA(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if err := h.Registry.RotateCA(r.Context(), signingTenantOf(r)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rotated": true})
}

// Locks lists the live app-plugin file locks:
// GET /api/admin/app-plugins/locks[?storage_id=].
func (h *AppPluginsAdmin) Locks(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	sid, _ := strconv.ParseInt(r.URL.Query().Get("storage_id"), 10, 64)
	rows, err := h.Registry.Locks(r.Context(), sid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locks": h.namedLocks(r.Context(), rows)})
}

// lockRow is a lock as the panel lists it: the row, plus the name of the
// storage it is on.
//
// ⚠ The name, not the id. The app's page listed a frozen file as
// "sözleşme.pdf — Depo #1" (v0.43.0 wave 2, 2026-09-22): a number no other
// screen shows, on the one table whose whole point is "which file, where".
// A storage that is gone keeps an empty name and the panel falls back to the
// id — the lock outlived it and is still worth lifting.
type lockRow struct {
	*model.AppPluginLock
	Storage string `json:"storage,omitempty"`
	// ReasonText is the reason in every language the app wrote it in; the
	// panel shows the reader's (lockReasonOf).
	ReasonText wire.Text `json:"reason_text,omitempty"`
}

func (h *AppPluginsAdmin) namedLocks(ctx context.Context, rows []*model.AppPluginLock) []lockRow {
	names := map[int64]string{}
	out := make([]lockRow, 0, len(rows))
	for _, l := range rows {
		name, seen := names[l.StorageID]
		if !seen {
			name = h.Registry.StorageName(ctx, l.StorageID)
			names[l.StorageID] = name
		}
		shown := *l
		plain, text := lockReasonOf(l)
		shown.Reason = plain
		out = append(out, lockRow{AppPluginLock: &shown, Storage: name, ReasonText: text})
	}
	return out
}

// Unlock lifts one lock by force: DELETE /api/admin/app-plugins/locks with
// {storage_id, path}. The audit row names who did it.
func (h *AppPluginsAdmin) Unlock(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req struct {
		StorageID int64  `json:"storage_id"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || req.StorageID <= 0 || strings.Trim(req.Path, "/") == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage_id and path are required"})
		return
	}
	was, err := h.Registry.Unlock(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !was {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "nothing is locked there"})
		return
	}
	if h.Audit != nil {
		var uid *int64
		if u := auth.UserFrom(r.Context()); u != nil {
			id := u.ID
			uid = &id
		}
		_ = h.Audit(r.Context(), uid, "app_plugin.unlock", req.StorageID, strings.Trim(req.Path, "/"), clientIP(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": true})
}

// SigningCAs lists the tenant's signing authorities, live and retired:
// GET /api/admin/app-plugins/signing/cas.
//
// Retired ones are listed on purpose. They are what a signature made before
// the last rotation verifies against, so an operator has to be able to see
// that they are still there — and that nothing is quietly deleting them.
func (h *AppPluginsAdmin) SigningCAs(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	list, err := h.Registry.CAs(r.Context(), signingTenantOf(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorities": list})
}

// ImportSigningCA takes over signing with an authority the operator already
// has: POST /api/admin/app-plugins/signing/ca/import, multipart with `cert`
// and `key` (PEM), or JSON {cert_pem, key_pem}.
//
// The current authority is retired, never deleted. An encrypted key is
// refused with the command that decrypts it rather than a shrug: the
// encrypted container formats in the wild are more varied than any one
// library reads, and half-working import is worse than a clear instruction.
func (h *AppPluginsAdmin) ImportSigningCA(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var certPEM, keyPEM []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
			return
		}
		certPEM = formFileBytes(r, "cert")
		keyPEM = formFileBytes(r, "key")
	} else {
		var body struct {
			CertPEM string `json:"cert_pem"`
			KeyPEM  string `json:"key_pem"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		certPEM, keyPEM = []byte(body.CertPEM), []byte(body.KeyPEM)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "both the certificate and its private key are needed, in PEM"})
		return
	}
	row, err := h.Registry.ImportCA(r.Context(), signingTenantOf(r), certPEM, keyPEM)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ca_invalid", "message": err.Error()})
		return
	}
	if h.Audit != nil {
		var uid *int64
		if u := auth.UserFrom(r.Context()); u != nil {
			id := u.ID
			uid = &id
		}
		_ = h.Audit(r.Context(), uid, "app_plugin.signing_ca_import", 0, row.Subject, clientIP(r))
	}
	list, _ := h.Registry.CAs(r.Context(), signingTenantOf(r))
	writeJSON(w, http.StatusOK, map[string]any{"imported": true, "subject": row.Subject, "authorities": list})
}

// formFileBytes reads one uploaded part, or nothing.
func formFileBytes(r *http.Request, field string) []byte {
	f, _, err := r.FormFile(field)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return nil
	}
	return b
}

func signingTenantOf(r *http.Request) int64 {
	if u := auth.UserFrom(r.Context()); u != nil && u.ProviderID != nil {
		return *u.ProviderID
	}
	return 0
}
