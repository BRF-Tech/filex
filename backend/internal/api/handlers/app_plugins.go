// Package handlers — app_plugins.go
//
// User surface for app plugins (internal/wasmplugin):
//
//	GET  /api/files/plugins/actions                          — what applies to this caller
//	POST /api/files/plugins/actions/{plugin}/{action}/run    — {storage_id, paths[], params} → 202 op | 200 surface
//	GET  /api/files/plugins/views/{plugin}/{view}            — ?storage_id=&path= → surface (event "open")
//	POST /api/files/plugins/views/{plugin}/{view}/event      — {storage_id, path, state, event, action_id, data} → surface | 202 op
//
// Every run is authorised HERE, at submit: tenant ownership of the storage,
// ACL on every path (viewer to read, editor when the action writes),
// read-only storages refused for writing actions, encrypted folders refused
// (the server has no key), and the action's applies rule re-checked against
// the real files — the menu's client-side filter is a convenience, not a
// gate. The ops worker that later runs the job has no user of its own.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// AppPlugins is the user-facing handler set + the OutputSink the registry
// commits job outputs through.
type AppPlugins struct {
	Registry        *wasmplugin.Registry
	Store           db.Store
	ACL             *acl.Resolver
	Ops             *ops.Service
	StorageResolver func(int64) (storage.Driver, error)
	Index           *search.Index
	Thumbs          *thumb.Pipeline
	// Quota is the per-person ceiling an interface's save is held to, like
	// every other write (security review UI-7). Nil = no ceiling.
	Quota *quota.Service
	// Assoc keeps which handler opens which kind of file (internal/assoc):
	// the listing carries its rules, and an interface switched off for a
	// kind is refused on it (openAllowed). Nil: every viewer opens its kinds.
	Assoc *assoc.Service
}

// NewAppPlugins constructs the handler and wires it as the registry's sink.
func NewAppPlugins(reg *wasmplugin.Registry, store db.Store, aclr *acl.Resolver, opsSvc *ops.Service,
	resolver func(int64) (storage.Driver, error), index *search.Index, thumbs *thumb.Pipeline) *AppPlugins {
	h := &AppPlugins{Registry: reg, Store: store, ACL: aclr, Ops: opsSvc, StorageResolver: resolver, Index: index, Thumbs: thumbs}
	if reg != nil {
		reg.SetOutputSink(h)
		reg.SetHomeResolver(h.homeOf)
		reg.SetHeldPermissions(h.heldAppPerms)
		SetLockReasons(reg.LockReason)
		SetAppLabels(reg.AppLabel)
	}
	return h
}

func (h *AppPlugins) off(w http.ResponseWriter) bool {
	if h.Registry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app_plugins_disabled"})
		return true
	}
	return false
}

func isAdmin(r *http.Request) bool {
	u := auth.UserFrom(r.Context())
	return u != nil && u.IsAdmin()
}

// callerID is the signed-in person's id, 0 for nobody.
func callerID(r *http.Request) int64 {
	if u := auth.UserFrom(r.Context()); u != nil {
		return u.ID
	}
	return 0
}

// Actions answers the menu list for this caller.
func (h *AppPlugins) Actions(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	ans, err := h.Registry.ActionsFor(r.Context(), isAdmin(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// What needs one of an app's user permissions the caller does not hold
	// is left out (perm/app.go): the menu never offers what the run refuses.
	if h.ACL != nil {
		if res, perr := h.ACL.Perms(r.Context(), auth.UserFrom(r.Context())); perr == nil && res != nil {
			holds := func(key, def string) bool {
				if key == "" {
					return true
				}
				ok, _ := res.AppAllowed(key, perm.AppDefault(def))
				return ok
			}
			actions := ans.Actions[:0]
			for _, a := range ans.Actions {
				if holds(a.Requires, a.RequiresDefault) {
					actions = append(actions, a)
				}
			}
			ans.Actions = actions
			views := ans.Views[:0]
			for _, v := range ans.Views {
				if holds(v.Requires, v.RequiresDefault) {
					views = append(views, v)
				}
			}
			ans.Views = views
		}
	}
	if h.Assoc != nil {
		if rules := h.Assoc.Rules(r.Context(), assoc.CapOpen); len(rules) > 0 {
			ans.OpenRules = rules
		}
	}
	writeJSON(w, http.StatusOK, ans)
}

// openAllowed refuses an app's interface on a file of a kind the
// administrator switched that interface off for (Admin → Plugins → Default
// apps), answering 403 handler_off itself. Only a `viewer` view is an opener;
// any other view is not this rule's business.
//
// ⚠ The listing already leaves such a view out of every list the explorer
// draws. This is the server's own line: an old tab, or a crafted request,
// that saves through the interface or calls its module on that file anyway.
func (h *AppPlugins) openAllowed(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, v *wire.View, name string) bool {
	if h.Assoc == nil || v == nil || v.Placement != "viewer" || name == "" {
		return true
	}
	if h.Assoc.OpenAllowed(r.Context(), name, assoc.OpenID(p.Row.Name, v.ID)) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "handler_off",
		"message": "the administrator turned this app off for ." + assoc.ExtOf(name) + " files",
	})
	return false
}

type runRequest struct {
	StorageID int64          `json:"storage_id"`
	Paths     []string       `json:"paths"`
	Params    map[string]any `json:"params"`
}

// checked is what the submit-time authorisation established.
type checked struct {
	plugin  *wasmplugin.Installed
	action  *wire.Action
	storage *model.Storage
	rels    []string
	items   []wasmplugin.Item
	// output is the surface's per-job override (JobRequest.Output), nil for
	// the manifest default.
	output *wire.Output
	// dest is the folder a `folder` output was checked into, nil otherwise.
	dest *outputFolder
}

// outputFolder is a folder a person chose for a job's result, as checked.
type outputFolder struct {
	storage *model.Storage
	rel     string
}

// checkOutputFolder judges the folder a person chose for a job's result
// (JobRequest.Output{Mode: "folder", Dir}) the way any write there is judged:
// a storage this caller may reach and that takes writes, a folder that
// exists, the caller at least EDITOR on it, and no lock or filex-own folder
// in the way (writegate). On refusal the answer is written.
//
// ⚠⚠ The server's check, not the screen's. The picker only offers folders,
// but a crafted submit can name any path — a read-only archive, another
// tenant's storage, `.filex-trash` — and each of those is refused here.
func (h *AppPlugins) checkOutputFolder(w http.ResponseWriter, r *http.Request, qualified string, pluginID int64) (*outputFolder, bool) {
	adapter, rel := splitAdapterPath(strings.ReplaceAll(strings.TrimSpace(qualified), "\\", "/"))
	if adapter == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_folder", "message": "output.dir must be an adapter-qualified folder (storage://path)"})
		return nil, false
	}
	st, err := h.Store.GetStorageByName(r.Context(), adapter)
	if err != nil || st == nil || !st.Enabled {
		notFound(w, "folder")
		return nil, false
	}
	if !ownsStorage(w, r, st.ID, "folder") {
		return nil, false
	}
	if st.ReadOnly {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "read_only", "message": "the chosen folder's storage is read-only"})
		return nil, false
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	if rel != "" {
		drv, derr := h.StorageResolver(st.ID)
		if derr != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
			return nil, false
		}
		obj, serr := drv.Stat(r.Context(), rel)
		if serr != nil || obj.Kind != storage.KindDirectory {
			notFound(w, "folder")
			return nil, false
		}
	}
	if !rootAllows(r.Context(), h.Store, st.ID, rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "the chosen folder is outside this token's root"})
		return nil, false
	}
	if !aclAllowForPlugin(r.Context(), h.ACL, h.Store, st.ID, rel, acl.LevelEditor, pluginID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "you may not write into the chosen folder"})
		return nil, false
	}
	if gate(w, r, h.ACL, st.ID, writegate.Writes(rel)) {
		return nil, false
	}
	// ⚠ An encrypted folder holds only what the person's browser encrypted:
	// the server has no key, so an app's plaintext never goes in (security
	// review UI-9 — for "save as" and a job's chosen folder alike).
	if lk, ok := h.Store.(e2e.NodeByPathLookup); ok {
		if _, enc := e2e.FindRoot(r.Context(), lk, st.ID, rel); enc {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "encrypted", "message": "an app cannot write into an encrypted folder"})
			return nil, false
		}
	}
	return &outputFolder{storage: st, rel: rel}, true
}

// maxJobParamsBytes is the ceiling on the marshalled parameters ONE job row may
// carry. The row is a database column the worker reads back and an
// administrator reads in the panel, so a surface that stuffs a whole document
// into `params` is refused at the door rather than at the INSERT — and a
// plugin author gets 413 with a number instead of a driver error with none.
//
// ⚠⚠ ONE constant for BOTH doors, for the same reason jobOutputMode is one
// function: the authenticated submit (enqueue) and the public page's
// (PublicAPI.enqueueAsCreator) queue rows into the SAME table, and a limit
// spelled twice is a limit that drifts — the page path had no limit at all
// until 2026-09-20, so an outside visitor could queue a row the authenticated
// caller beside them was refused. Raise it here and both doors move together.
const maxJobParamsBytes = 64 << 10

// maxPluginPaths is the largest selection an app is handed: by the run that
// opens its screen (authorise) and by every event of that screen (viewEvent).
// One number for both, because the browser now sends the whole selection on
// each event (filex #64) and every path costs an ACL walk — an event door
// wider than the run door would sell a crafted body thousands of them.
const maxPluginPaths = 500

// pluginACLNeed is the ACL level a caller must hold on every input of a job:
// viewer to read, editor once the job puts bytes back on the storage, and
// whatever higher floor the action's own `min_role` declares (a manifest may
// insist on editor for a read-only action, or on owner outright).
//
// ⚠⚠ ONE reading of min_role for BOTH doors. The public page path used to
// make no ACL check at all, so an action an app author had marked
// `min_role: owner` ran on a link whose creator held viewer; folding the
// floor into one function is what keeps the two doors from disagreeing about
// what the manifest asked for.
func pluginACLNeed(action *wire.Action, writes bool) acl.Level {
	need := acl.LevelViewer
	if writes {
		need = acl.LevelEditor
	}
	if action == nil {
		return need
	}
	switch action.MinRole {
	case "editor":
		if need < acl.LevelEditor {
			need = acl.LevelEditor
		}
	case "owner":
		need = acl.LevelOwner
	}
	return need
}

// jobOutputMode is what an about-to-be-queued job will DO with its result: the
// action's manifest output, unless the surface asked for another one for this
// one job (`wire.JobRequest.Output` — the wizard's "a new version of the file
// / a new file beside it, called X"). `writes` says whether that mode puts
// bytes on the storage, which is what decides the read-only refusal and the
// ACL level the caller needs; `ok` is false only for a mode outside the closed
// set, which is a plugin bug and answered 400.
//
// ⚠⚠ ONE reading of an override, for BOTH enqueue paths — the authenticated
// one (authorise → enqueue) and the public page's
// (PublicAPI.enqueueAsCreator). The page path is the one that keeps being
// written second: it shipped without `page_token_hash`, and then without the
// override at all, so an outside signer's "put it in a new file called X"
// silently landed as a new VERSION of the original — the person's own file
// overwritten by the choice they made against it. Anything about WHAT a job
// does with its result belongs here, where neither path can have its own
// opinion.
func jobOutputMode(action *wire.Action, out *wire.Output) (mode string, writes bool, ok bool) {
	if action != nil {
		mode = action.Output.Mode
	}
	if out != nil {
		if !wasmplugin.ValidOutputMode(out.Mode) {
			return "", false, false
		}
		mode = out.Mode
	}
	return mode, mode == "sibling" || mode == "version", true
}

// applyJobOutput stamps the surface's own output choice into the params the
// job row carries, which is where the worker reads it back from
// (wasmplugin.OutputOverride). nil params become a map, because a job with no
// parameters may still have made a choice; a nil choice leaves the manifest's
// output to speak.
func applyJobOutput(params map[string]any, out *wire.Output) map[string]any {
	// ⚠⚠ The host's own parameter names (__output, the page door's stamps)
	// are dropped from whatever came in with the caller's or the surface's
	// params; only the choice validated at submit (authorise) is stamped.
	params = wasmplugin.StripHostParams(params)
	if out != nil {
		wasmplugin.SetOutputOverride(params, out)
	}
	return params
}

// authorise runs every submit-time check; on failure it has already written
// the answer. `opening` is a menu click that will OPEN the action's screen
// rather than queue a job (Run with no params): an action whose result may go
// into a folder the person chooses (output.elsewhere) is opened on a
// read-only storage, and only a job that would write THERE is refused.
func (h *AppPlugins) authorise(w http.ResponseWriter, r *http.Request, pluginName, actionID string, storageID int64, paths []string, fromSurface, opening bool, out *wire.Output) (*checked, bool) {
	storageID, paths, err := h.resolvePaths(r.Context(), storageID, paths)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, false
	}
	if len(paths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "paths are required"})
		return nil, false
	}
	if len(paths) > maxPluginPaths {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many paths"})
		return nil, false
	}
	if !ownsStorage(w, r, storageID, "storage") {
		return nil, false
	}
	st, err := h.Store.GetStorage(r.Context(), storageID)
	if err != nil || st == nil {
		notFound(w, "storage")
		return nil, false
	}
	p, action, applies, err := h.Registry.ResolveAction(r.Context(), pluginName, actionID, isAdmin(r))
	if err != nil {
		if wasmplugin.IsCode(err, wasmplugin.CodeRefused) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": err.Error()})
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": err.Error()})
		}
		return nil, false
	}
	if action.Hidden && !fromSurface {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "this action is started by the app, not from the menu"})
		return nil, false
	}
	// The app's own user permission (manifest `requires`, perm/app.go):
	// "Request signatures" the administrator has not let this account use.
	if !h.appUserPermOK(w, r, p, action.Requires) {
		return nil, false
	}
	mode, writes, okMode := jobOutputMode(action, out)
	if !okMode {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad output mode: " + out.Mode})
		return nil, false
	}
	var dest *outputFolder
	if mode == "folder" {
		// The person chose where the result goes. Only an action that says
		// its result may go elsewhere is let do it, and the folder is judged
		// like any other write: its storage, the person's level there, locks
		// and filex's own folders.
		if !action.Output.Elsewhere {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder_not_offered", "message": "this action writes beside its file, not into a chosen folder"})
			return nil, false
		}
		d, ok := h.checkOutputFolder(w, r, out.Dir, p.Row.ID)
		if !ok {
			return nil, false
		}
		dest = d
	}
	// What the job writes on the SOURCE's storage: its own result (sibling,
	// version), or — for a flow that ends in a write (applies.writable, the
	// signing request) — the file itself later on.
	writesSource := writes || applies.Writable
	if writesSource && st.ReadOnly && !(opening && action.View != "" && action.Output.Elsewhere) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "read_only", "message": "this storage is read-only"})
		return nil, false
	}
	need := pluginACLNeed(action, writesSource)
	// A token does what its verbs name (auth/token_verbs.go). An action a
	// viewer may run needs only `read`, like the viewer; one that needs edit
	// rights — it writes its result, into the file or into a chosen folder —
	// needs `write`. The run route asks only `read`, so this is the door.
	if (need > acl.LevelViewer || dest != nil) && !auth.AllowVerb(w, r, auth.VerbWrite) {
		return nil, false
	}
	drv, err := h.StorageResolver(storageID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return nil, false
	}
	lk, _ := h.Store.(e2e.NodeByPathLookup)
	c := &checked{plugin: p, action: action, storage: st, output: out, dest: dest}
	states := map[string][]string{}
	if len(applies.State) > 0 || len(applies.NoState) > 0 {
		hashes := make([]string, 0, len(paths))
		for _, raw := range paths {
			rel := strings.Trim(path.Clean("/"+strings.ReplaceAll(raw, "\\", "/")), "/")
			hashes = append(hashes, pathkey.Hash(storageID, "/"+rel))
		}
		if m, err := h.Store.ListAppPluginStateKeys(r.Context(), storageID, hashes); err == nil {
			states = m
		}
	}
	for _, raw := range paths {
		rel := strings.Trim(path.Clean("/"+strings.ReplaceAll(raw, "\\", "/")), "/")
		if rel == "" || rel == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a storage root cannot be an input"})
			return nil, false
		}
		// A root-confined token (the app token a host hands an embed) reaches
		// only its folder, here as on every other door; the ACL below is the
		// PERSON's, which an admin-bound token clears everywhere.
		if !rootAllows(r.Context(), h.Store, storageID, rel) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "outside this token's root: " + rel})
			return nil, false
		}
		if !aclAllowForPlugin(r.Context(), h.ACL, h.Store, storageID, rel, need, p.Row.ID) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "insufficient permission: " + rel})
			return nil, false
		}
		if lk != nil && e2e.UnderEncrypted(r.Context(), lk, storageID, "/"+rel) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "encrypted", "message": "plugins cannot read files in an encrypted folder: " + rel})
			return nil, false
		}
		obj, err := drv.Stat(r.Context(), rel)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": rel})
			return nil, false
		}
		it := appItemOf(rel, obj, states[pathkey.Hash(storageID, "/"+rel)], callerID(r), p.Row.Name)
		c.rels = append(c.rels, rel)
		c.items = append(c.items, it)
	}
	if !wasmplugin.Matches(applies, c.items) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "not_applicable", "message": "this action does not apply to the selected items"})
		return nil, false
	}
	return c, true
}

// appItemOf is one input as an app's `applies` rule sees it: its kind, mime
// type and extension, and the app's own state keys on it. The run's check
// (authorise) and the AI surface's "what applies to this file" (app_actions)
// both build it here, so the two cannot disagree about what an action
// matches.
//
// ⚠ Personal keys (`todo@7`) answer for the person asking only, as `todo@me`:
// the same view of them the listing gives (app badges).
func appItemOf(rel string, obj storage.Object, stateKeys []string, caller int64, plugin string) wasmplugin.Item {
	it := wasmplugin.Item{Kind: "file", Mime: obj.Mime, Ext: strings.TrimPrefix(strings.ToLower(path.Ext(rel)), ".")}
	if obj.Kind == storage.KindDirectory {
		it.Kind = "dir"
		it.Ext, it.Mime = "", ""
	}
	for _, k := range wasmplugin.PersonalStateKeys(stateKeys, caller) {
		if strings.HasPrefix(k, plugin+":") {
			it.State = append(it.State, strings.TrimPrefix(k, plugin+":"))
		}
	}
	return it
}

// resolvePaths accepts the two spellings a caller may use: a storage_id with
// storage-relative paths, or adapter-qualified paths (`docs://reports/x.pdf`,
// the explorer's own form) with no storage_id. Mixed adapters are refused —
// one job reads one storage.
func (h *AppPlugins) resolvePaths(ctx context.Context, storageID int64, paths []string) (int64, []string, error) {
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		adapter, rel := splitAdapterPath(strings.ReplaceAll(raw, "\\", "/"))
		if adapter != "" {
			st, err := h.Store.GetStorageByName(ctx, adapter)
			if err != nil || st == nil {
				return 0, nil, errUnknownAdapter(adapter)
			}
			if storageID == 0 {
				storageID = st.ID
			} else if st.ID != storageID {
				return 0, nil, errMixedAdapters
			}
		}
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if rel != "" && rel != "." {
			out = append(out, rel)
		}
	}
	if storageID <= 0 {
		return 0, nil, errsString("storage_id or adapter-qualified paths are required")
	}
	return storageID, out, nil
}

// Users is the people-picker's directory search:
// GET /api/files/plugins/users?plugin=<name>&q=. It answers only for a running
// plugin that holds users:lookup (403 otherwise), through the tenant-scoped
// store — a tenant's picker never lists another tenant's people.
func (h *AppPlugins) Users(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("plugin"))
	p, ok := h.Registry.ByName(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if state, _ := p.State(); state != wasmplugin.StateRunning || !p.Grants.Has(wasmplugin.PermUsersLookup) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "this app may not look people up"})
		return
	}
	rows, err := h.Registry.LookupUsers(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": rows})
}

// Run submits an action. An action that declares a view answers the view's
// opening surface instead of queueing; the surface's submit comes back
// through ViewEvent, which queues.
func (h *AppPlugins) Run(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	var req runRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	pluginName, actionID := chi.URLParam(r, "plugin"), chi.URLParam(r, "action")
	c, ok := h.authorise(w, r, pluginName, actionID, req.StorageID, req.Paths, false, req.Params == nil, nil)
	if !ok {
		return
	}
	if c.action.View != "" && req.Params == nil {
		// c.storage, not req.StorageID: with adapter-qualified paths the
		// request carries no id, and a view opened on storage 0 would see
		// no files and no qualified paths.
		s, err := h.Registry.ViewEvent(r.Context(), pluginName, c.action.View, c.storage.ID, c.rels, auth.UserFrom(r.Context()), pluginLang(r),
			wire.ViewEventInput{Event: "open"})
		if err != nil {
			h.callFail(w, err)
			return
		}
		if s.Job != nil {
			// The view wants the job queued right away (no form needed).
			// Re-authorise against the action the SURFACE named — it may be
			// another one than the row that was clicked (a wizard that opens
			// and immediately queues its hidden second half), and it may
			// carry its own output choice. fromSurface, so a hidden action
			// is allowed here and only here.
			if c, ok = h.authorise(w, r, pluginName, s.Job.ActionID, req.StorageID, req.Paths, true, false, s.Job.Output); !ok {
				return
			}
			h.enqueue(w, r, c, s.Job.Params)
			return
		}
		h.checkSurfaceOpen(r, s)
		writeJSON(w, http.StatusOK, map[string]any{"surface": s})
		return
	}
	h.enqueue(w, r, c, req.Params)
}

// enqueue writes the job row and the ops row, answering 202.
func (h *AppPlugins) enqueue(w http.ResponseWriter, r *http.Request, c *checked, params map[string]any) {
	if h.Ops == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	u := auth.UserFrom(r.Context())
	var actorID *int64
	// ⚠ pluginLang, not the account field alone: somebody who never chose a
	// language in their profile still has one in the browser, and the
	// wizard they just walked through answered in it. Reading u.Locale
	// only made the queued job answer in English underneath a Turkish
	// screen — one flow, two languages, which is exactly the complaint.
	// The screen that queued the job may name its own language (`?lang=`,
	// an embed drawing Turkish over an English account): that one wins.
	locale := pluginLang(r)
	if u != nil {
		id := u.ID
		actorID = &id
	}
	params = applyJobOutput(params, c.output)
	pb, _ := json.Marshal(params)
	if len(pb) > maxJobParamsBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "params too large"})
		return
	}
	job := &model.AppPluginJob{
		ID: wasmplugin.NewJobID(), PluginID: c.plugin.Row.ID, PluginName: c.plugin.Row.Name, ActionID: c.action.ID,
		StorageID: c.storage.ID, PathsJSON: jsonString(c.rels), ParamsJSON: string(pb), ActorID: actorID, Locale: locale,
		Label: wasmplugin.EncodeJobText(c.action.Label), Status: model.AppPluginJobPending,
	}
	if err := h.Store.CreateAppPluginJob(r.Context(), job); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "job: " + err.Error()})
		return
	}
	op, err := h.Ops.SubmitTo(r.Context(), ops.OpPluginAction, c.storage.ID, c.storage.ID, c.rels, job.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "queue: " + err.Error()})
		return
	}
	// ⚠ One column, not the row: the worker may already have finished this
	// job and written its result (see SetAppPluginJobOp).
	opID := op.ID
	job.OpID = &opID
	_ = h.Store.SetAppPluginJobOp(r.Context(), job.ID, opID)
	runMeta := map[string]any{"action": c.action.ID, "storage_id": c.storage.ID, "paths": c.rels, "job": job.ID, "op": opID}
	if auth.Audited(r.Context()) {
		// A door that records this request's row (POST /api/ai/apps/run,
		// /api/ai/convert, the app_run / file_convert MCP tools) gets THIS
		// name and these facts: one row per run, with the token and the door
		// stamped on it, not the door's generic row plus a second one here.
		auth.SetAuditAction(r.Context(), "app_plugin.action_run", "app_plugin")
		auth.SetAuditTarget(r.Context(), c.plugin.Row.Name, "")
		for k, v := range runMeta {
			auth.AddAuditDetail(r.Context(), k, v)
		}
	} else {
		_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
			UserID: actorID, Action: "app_plugin.action_run", TargetType: "app_plugin", TargetID: c.plugin.Row.Name,
			Metadata: runMeta,
			IP:       clientIP(r),
		})
	}
	op.Plugin, op.Action, op.Label = c.plugin.Row.Name, c.action.ID, wasmplugin.JobText(job.Label, locale)
	writeJSON(w, http.StatusAccepted, map[string]any{"op": op, "job_id": job.ID})
}

func (h *AppPlugins) callFail(w http.ResponseWriter, err error) {
	var ce *wasmplugin.CallError
	if errors.As(err, &ce) {
		code := http.StatusBadGateway
		switch ce.Code {
		case wasmplugin.CodeUnsupported:
			code = http.StatusNotFound
		case wasmplugin.CodeRefused:
			code = http.StatusForbidden
		case wasmplugin.CodeTimeout:
			code = http.StatusGatewayTimeout
		case wasmplugin.CodeBusy:
			code = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "2")
		}
		writeJSON(w, code, map[string]string{"error": ce.Code, "message": ce.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

type viewRequest struct {
	StorageID int64          `json:"storage_id"`
	Path      string         `json:"path"`
	Paths     []string       `json:"paths"`
	State     map[string]any `json:"state"`
	Event     string         `json:"event"`
	ActionID  string         `json:"action_id"`
	Data      map[string]any `json:"data"`
}

// View answers a view's opening surface (event "open").
func (h *AppPlugins) View(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	var req viewRequest
	req.StorageID, _ = parseInt64(r.URL.Query().Get("storage_id"))
	req.Path = r.URL.Query().Get("path")
	req.Event = "open"
	// A home page opened at one of its sections (wire.Surface.Sections): the
	// frame keeps the section in its own address, so Back and a link land
	// where they were, and hands it to the plugin here.
	if sec := strings.TrimSpace(r.URL.Query().Get("section")); sec != "" {
		if len(sec) > 64 {
			sec = sec[:64]
		}
		req.Data = map[string]any{"section": sec}
	}
	h.viewEvent(w, r, &req)
}

// ViewEvent answers a change/submit/action event. A surface carrying a job
// request is queued with the same checks as Run.
func (h *AppPlugins) ViewEvent(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	var req viewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Event == "" {
		req.Event = "change"
	}
	h.viewEvent(w, r, &req)
}

func (h *AppPlugins) viewEvent(w http.ResponseWriter, r *http.Request, req *viewRequest) {
	pluginName, viewID := chi.URLParam(r, "plugin"), chi.URLParam(r, "view")
	// A screen that `requires` one of the app's user permissions is refused
	// to an account without it — opened or answered (perm/app.go).
	if p, ok := h.Registry.ByName(pluginName); ok {
		if v, ok := p.Manifest.View(viewID); ok && !h.appUserPermOK(w, r, p, v.Requires) {
			return
		}
	}
	// The person's own address, for `context.actor.ip` — the same reading
	// (trusted proxies included) a public page gets as `visitor_ip`.
	r = r.WithContext(wasmplugin.WithActorIP(r.Context(), clientIP(r)))
	// `paths` is the selection the screen was opened on, echoed by the browser
	// on every event; `path` is the older single-row spelling, still honoured
	// for a client that sends nothing else. ⚠ `paths` wins when both come: a
	// screen opened on three files that is answered about the first one only
	// is filex #64 (the second screen said "a.txt", the job ran on one file).
	// Every path is judged again below for the person asking — the browser
	// echoing a selection proves nothing about it.
	paths := req.Paths
	if len(paths) == 0 && req.Path != "" {
		paths = []string{req.Path}
	}
	if len(paths) > maxPluginPaths {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many paths"})
		return
	}
	var rels []string
	if len(paths) > 0 {
		sid, resolved, err := h.resolvePaths(r.Context(), req.StorageID, paths)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		req.StorageID = sid
		if !ownsStorage(w, r, req.StorageID, "storage") {
			return
		}
		lk, _ := h.Store.(e2e.NodeByPathLookup)
		for _, rel := range resolved {
			if !rootAllows(r.Context(), h.Store, req.StorageID, rel) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "outside this token's root: " + rel})
				return
			}
			if !aclAllowID(r.Context(), h.ACL, h.Store, req.StorageID, rel, acl.LevelViewer) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "insufficient permission: " + rel})
				return
			}
			if lk != nil && e2e.UnderEncrypted(r.Context(), lk, req.StorageID, "/"+rel) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "encrypted", "message": "plugins cannot read files in an encrypted folder"})
				return
			}
			rels = append(rels, rel)
		}
	} else if req.StorageID > 0 && !ownsStorage(w, r, req.StorageID, "storage") {
		// A screen opened on no file (a home page) still names a storage, and
		// the call's scope opens its driver and tells the app its name: the
		// same tenant check the path branch makes.
		return
	}
	// ⚠⚠ The HOST half of show_when / required_when (surface_conditions.go).
	// Before the plugin is told what was pressed, the values are measured
	// against the screen they came from: a hidden field's value is dropped and
	// an empty `required_when` field refuses the event outright, so no job can
	// be queued out of it. The browser does the same on its way out, but that
	// is a convenience for the person at the screen — THIS is the boundary,
	// and a crafted request meets it here.
	if missing, gerr := gateSurfaceValues(req.Event, req.State, req.Data, func(in wire.ViewEventInput) (*wire.Surface, error) {
		return h.Registry.ViewEvent(r.Context(), pluginName, viewID, req.StorageID, rels, auth.UserFrom(r.Context()), pluginLang(r), in)
	}); gerr != nil {
		h.callFail(w, gerr)
		return
	} else if len(missing) > 0 {
		writeSurfaceRequired(w, missing)
		return
	}
	s, err := h.Registry.ViewEvent(r.Context(), pluginName, viewID, req.StorageID, rels, auth.UserFrom(r.Context()), pluginLang(r),
		wire.ViewEventInput{Event: req.Event, ActionID: req.ActionID, State: req.State, Data: req.Data})
	if err != nil {
		h.callFail(w, err)
		return
	}
	if s.Job != nil {
		c, ok := h.authorise(w, r, pluginName, s.Job.ActionID, req.StorageID, paths, true, false, s.Job.Output)
		if !ok {
			return
		}
		h.enqueue(w, r, c, s.Job.Params)
		return
	}
	h.checkSurfaceOpen(r, s)
	writeJSON(w, http.StatusOK, map[string]any{"surface": s})
}

// ── OutputSink ─────────────────────────────────────────────────────────

func (h *AppPlugins) syncer() *protocolsync.Syncer {
	return protocolsync.New(h.Store, h.Index, h.Thumbs, writehook.OriginPlugin)
}

// Catalogue records a file the storage has and the catalogue has not seen yet
// (wasmplugin.Cataloguer): share_create's answer when a job's own input has no
// node for the link to point at.
func (h *AppPlugins) Catalogue(ctx context.Context, storageID int64, rel string) (*model.Node, error) {
	return catalogueOnDemand(ctx, h.Store, h.StorageResolver, h.syncer(), storageID, rel)
}

// CommitSibling writes a new file into dir, picking a free name, and runs
// the shared post-write bookkeeping.
func (h *AppPlugins) CommitSibling(ctx context.Context, storageID int64, dir, name string, r io.Reader, size int64, actor *int64) (string, error) {
	drv, err := h.StorageResolver(storageID)
	if err != nil {
		return "", err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return "", errors.New("storage is not writable")
	}
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return "", errors.New("storage row missing")
	}
	// Checked at submit; checked again here, because a job runs later and an
	// administrator may have made the storage read-only in between.
	if st.ReadOnly {
		return "", errors.New("read_only: the storage is read-only")
	}
	if dir == "." || dir == "/" {
		dir = ""
	}
	rel := strings.Trim(path.Join(dir, name), "/")
	// An app names its own output; it does not get to name it `.keepdir`,
	// put it under `.versions/`, or land it on a document another app has
	// frozen (writegate; the app holding the lock is let through).
	if err := writegate.Check(h.ACL.Locks(ctx, storageID), writegate.AppFrom(ctx), writegate.Writes(rel)); err != nil {
		return "", err
	}
	rel, err = ops.UniqueDest(ctx, drv, rel)
	if err != nil {
		return "", err
	}
	if err := storage.EnsureFileTarget(ctx, drv, rel); err != nil {
		return "", err
	}
	if err := wr.Write(ctx, rel, r, size); err != nil {
		return "", err
	}
	mime := ""
	if obj, err := drv.Stat(ctx, rel); err == nil {
		size, mime = obj.Size, obj.Mime
	}
	h.syncer().Write(ctx, st, rel, size, mime)
	return rel, nil
}

// CommitVersion overwrites rel in place; the previous bytes become a version
// when versioning is on (writehook.BeforeOverwrite).
func (h *AppPlugins) CommitVersion(ctx context.Context, storageID int64, rel string, r io.Reader, size int64, actor *int64) error {
	drv, err := h.StorageResolver(storageID)
	if err != nil {
		return err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return errors.New("storage is not writable")
	}
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return errors.New("storage row missing")
	}
	rel = strings.Trim(rel, "/")
	// ⚠⚠ The signing app writes the signed document over the file it froze —
	// that is what the freeze is for, and writegate lets the lock holder
	// through (the job runner tells it who is writing, WithApp). Another
	// app's output landing on it is refused, whoever queued the job.
	//
	// An app's editor may also be working on a DRAFT (issue #71): a new
	// document of a type the app edits lives in its owner's drafts area until
	// its first save, and a new version of it is that person's own save
	// (syspath.OwnDraft) — for the person who queued the job, nobody else.
	var person int64
	if actor != nil {
		person = *actor
	}
	if err := writegate.Check(h.ACL.Locks(ctx, storageID), writegate.AppFrom(ctx),
		writegate.Writes(rel).As(syspath.OwnDraft).By(person)); err != nil {
		return err
	}
	if err := storage.EnsureFileTarget(ctx, drv, rel); err != nil {
		return err
	}
	if err := writehook.BeforeOverwrite(ctx, storageID, rel); err != nil {
		return err
	}
	if err := wr.Write(ctx, rel, r, size); err != nil {
		return err
	}
	mime := ""
	if obj, err := drv.Stat(ctx, rel); err == nil {
		size, mime = obj.Size, obj.Mime
	}
	h.syncer().Write(ctx, st, rel, size, mime)
	return nil
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := jsonNumber(s, &n)
	return n, err
}

func jsonNumber(s string, n *int64) (bool, error) {
	if strings.TrimSpace(s) == "" {
		return false, nil
	}
	return true, json.Unmarshal([]byte(strings.TrimSpace(s)), n)
}

// checkSurfaceOpen decides whether a surface's `open` may be shown to this
// caller. A screen can name any path it likes; a person may only be sent to
// a file they could have opened themselves, so the path is resolved and run
// through the ACL here, where the caller is known. A path that does not pass
// simply loses its link — the screen still draws, it just does not offer to
// go somewhere the person has no business being.
func (h *AppPlugins) checkSurfaceOpen(r *http.Request, s *wire.Surface) {
	if s == nil || s.Open == nil {
		return
	}
	storageID, rels, err := h.resolvePaths(r.Context(), 0, []string{s.Open.Path})
	if err != nil || len(rels) != 1 {
		s.Open = nil
		return
	}
	if !ownsStorageQuiet(r, storageID) || !aclAllowID(r.Context(), h.ACL, h.Store, storageID, rels[0], acl.LevelViewer) {
		s.Open = nil
	}
}

// appUserPermOK asks one of the app's own user permissions (manifest
// `user_permissions`, named by an action's or a view's `requires`) for the
// caller — app.<app>.<id>, decided by perm.Result.AppAllowed: the person's
// exception, their custom role, the built-in role, then the app's default.
// On refusal it has written the 403, shaped like every permission refusal
// (error, permission, source, message).
//
// ⚠ Asked on EVERY door that starts the app's work for a person: a menu run,
// a screen opened, a screen's event, a job a screen queues. A door that
// forgets it is how "Request signatures" stays usable to a role it was taken
// from.
func (h *AppPlugins) appUserPermOK(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, id string) bool {
	if id == "" || h.ACL == nil || p == nil || p.Manifest == nil {
		return true
	}
	up, ok := p.Manifest.UserPermission(id)
	if !ok {
		// Validated at install; a row that names an undeclared one refuses.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return false
	}
	res, err := h.ACL.Perms(r.Context(), auth.UserFrom(r.Context()))
	if err != nil || res == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return false
	}
	key, allowed, src := appPermHeld(res, p, up)
	if allowed {
		return true
	}
	lang := langOf(r)
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error":      "permission_denied",
		"permission": key,
		"source":     src,
		"message": srvtext.Text(lang, "server.perm.app_denied", srvtext.Vars{
			"permission": up.Label.Get(lang),
			"app":        p.Manifest.Label.Get(lang),
		}),
	})
	return false
}

// appPermHeld is THE decision on one of an app's user permissions for the
// account whose resolved permissions are res: app.<app>.<id>, by
// perm.Result.AppAllowed with the manifest's default. The door
// (appUserPermOK) and what the app is told about the person (heldAppPerms,
// wire.Actor.Permissions) both ask it, so an app is never told "you may" by a
// list the door disagrees with.
func appPermHeld(res *perm.Result, p *wasmplugin.Installed, up wire.UserPermission) (key string, allowed bool, src perm.Source) {
	key = perm.AppKey(p.Manifest.Name, up.ID)
	allowed, src = res.AppAllowed(key, perm.AppDefault(up.Default))
	return key, allowed, src
}

// heldAppPerms answers wire.Actor.Permissions (Registry.SetHeldPermissions):
// the ids of p's user_permissions u holds, each decided by appPermHeld — the
// door's own question. It mirrors appUserPermOK where that one decides
// without asking: with no ACL wired every door lets them through, so all are
// held; when u's permissions cannot be read every door refuses, so none is.
func (h *AppPlugins) heldAppPerms(ctx context.Context, u *model.User, p *wasmplugin.Installed) []string {
	if u == nil || p == nil || p.Manifest == nil || len(p.Manifest.UserPermissions) == 0 {
		return nil
	}
	var res *perm.Result
	if h.ACL != nil {
		got, err := h.ACL.Perms(ctx, u)
		if err != nil || got == nil {
			return nil
		}
		res = got
	}
	out := make([]string, 0, len(p.Manifest.UserPermissions))
	for _, up := range p.Manifest.UserPermissions {
		if res != nil {
			if _, ok, _ := appPermHeld(res, p, up); !ok {
				continue
			}
		}
		out = append(out, up.ID)
	}
	return out
}
