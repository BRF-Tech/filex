// Package handlers - ai_doors.go
//
// The explorer's operations behind the AI surface (task #114): copy, an app's
// action (file_convert), the operations queue, the trash, version history,
// 7z/TAR archives, one's own links and file requests - each an MCP tool and a
// REST route under /api/ai, both answering what the explorer's own route
// answers.
//
// ⚠⚠ They are not written again here. Each one runs the handler the explorer
// calls (Ops, AppPlugins, Trash, Versions, Archive, SharesMine, Share), in
// process, behind the same root confinement middleware - so the permission
// checks, the tenant boundary, the app rules (min_role, admin_only, applies,
// hidden actions) and the answers are the explorer's by construction, and a
// fix to one door is a fix to all three. What this file adds is only what the
// AI surface owes on top, before the handler runs:
//
//   - paths in the AI surface's own form (aiOps.resolveStorage): a bare path
//     of a `root:` token resolves under its root, the bound user needs at
//     least viewer, filex's own folders do not exist;
//   - end-to-end encryption with the AI surface's codes (ai_e2e.go):
//     E2E_BOUNDARY, E2E_PLAINTEXT_REFUSED, E2E_ENCRYPTED - the explorer's
//     route refuses some of these too, but without the code an agent matches
//     on;
//   - no key: every write the handler judges through gate() - and every member
//     an archive extraction lands - is judged as the AI surface's own
//     (syspath.Keyless), so an encrypted folder's key file is never written,
//     copied over or restored from here (keylessDoor).
//
// The audit row of a write is its REST route's (auth.ActionForPath), written
// by the /api/ai group's AuditMiddleware for REST and by mcpAuditRow for the
// MCP tool, with the facts the handler adds (SetAuditTarget, AddAuditDetail)
// in both - the door passes the recorder through, it never opens a second one.
package handlers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// AIDoors are the explorer's own handlers, exactly as routes.go builds them
// for /api/files and /api/shares. The AI surface runs them in process. A nil
// field leaves its tools answering 503.
type AIDoors struct {
	Ops        *Ops
	Apps       *AppPlugins
	Trash      *Trash
	Versions   *Versions
	Archive    *Archive
	SharesMine *SharesMine
	Share      *Share
	// The bell, stars, comments and item permissions (ai_doors_people.go).
	Notifications *Notifications
	Meta          *Meta
	Comments      *Comments
	Grants        *Grants
	// ACL answers the per-route permission the explorer's route asks before
	// its handler (plugins.run on an app's action).
	ACL *acl.Resolver
}

// AttachDoors wires the explorer's handlers into the AI REST surface.
func (h *AI) AttachDoors(d *AIDoors) { h.ops.doors = d }

// doorAnswer is what a handler answered: its HTTP status and its JSON body.
type doorAnswer struct {
	status int
	body   []byte
}

// doorRefusal is an answer in the explorer's own shape.
func doorRefusal(status int, msg string) doorAnswer {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return doorAnswer{status: status, body: b}
}

func doorsOff(what string) doorAnswer {
	return doorRefusal(http.StatusServiceUnavailable, what+" is not available on this server")
}

// errAIBadInput is a call the AI surface cannot act on as asked - a field
// missing, a storage root where a file is meant: 400 (aiStatus).
var errAIBadInput = errors.New("bad request")

func badInput(msg string) error { return denied(errAIBadInput, "%s", msg) }

// ── the request the handler sees ─────────────────────────────────────────────

type doorOriginKey struct{}

// doorOrigin is what the handler reads off the caller's request besides the
// context: the host it was asked on (a link is minted on that origin -
// tenanturl.FromRequest) and the language it answers a job's label in.
// ⚠ Not X-Filex-Root: on the AI surface a token's own `root:` scope is the
// confinement (docs/MCP.md), the one aiOps.resolveStorage resolves bare paths
// under; a header narrowing the handler's view behind that would make the two
// disagree about where a path is.
type doorOrigin struct {
	host   string
	tls    *tls.ConnectionState
	header http.Header
}

var doorOriginHeaders = []string{"X-Forwarded-Proto", "X-Forwarded-Host", "Accept-Language"}

// withDoorOrigin remembers r's origin on ctx: AIMCP.ServeHTTP for the tools
// (a tool call has no *http.Request of its own), and the REST twins below.
func withDoorOrigin(ctx context.Context, r *http.Request) context.Context {
	o := &doorOrigin{host: r.Host, tls: r.TLS, header: http.Header{}}
	for _, k := range doorOriginHeaders {
		if v := r.Header.Get(k); v != "" {
			o.header.Set(k, v)
		}
	}
	return context.WithValue(ctx, doorOriginKey{}, o)
}

type keylessDoorKey struct{}

// keylessDoor reports whether the request is one an AI door runs: a surface
// that holds no encryption key (gate, landing.target).
func keylessDoor(ctx context.Context) bool {
	v, _ := ctx.Value(keylessDoorKey{}).(bool)
	return v
}

// door runs one of the explorer's handlers for the AI surface's caller and
// answers what it answered.
//
// The request carries the caller's context - the bound user, the token and
// its verbs, the tenant scope, the address, and the audit recorder when one is
// open - so the handler judges exactly whom the explorer's route would judge.
// confine.Middleware runs first, as on /api/files: it rewrites the body's
// path fields under a `root:` token's root and refuses one outside it, and
// leaves the root on the context for the handlers that filter by it (the
// trash list, the version history, the share list). wrap is the per-route
// middleware the explorer's route has (RequirePermission), outermost first.
func (a *aiOps) door(ctx context.Context, h http.HandlerFunc, method, target string, params map[string]string, body any, wrap ...func(http.Handler) http.Handler) doorAnswer {
	var rdr io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return doorRefusal(http.StatusBadRequest, "bad request: "+err.Error())
		}
		rdr = bytes.NewReader(b)
	}
	c := context.WithValue(ctx, keylessDoorKey{}, true)
	if len(params) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		c = context.WithValue(c, chi.RouteCtxKey, rctx)
	}
	req, err := http.NewRequestWithContext(c, method, target, rdr)
	if err != nil {
		return doorRefusal(http.StatusBadRequest, "bad request: "+err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if o, ok := ctx.Value(doorOriginKey{}).(*doorOrigin); ok && o != nil {
		req.Host, req.TLS = o.host, o.tls
		for k, v := range o.header {
			req.Header[k] = v
		}
	}
	if ip := clientip.FromContext(ctx); ip != "" {
		req.RemoteAddr = ip
	}
	var next http.Handler = h
	for i := len(wrap) - 1; i >= 0; i-- {
		next = wrap[i](next)
	}
	rec := newBufRecorder()
	confine.Middleware(next).ServeHTTP(rec, req)
	return doorAnswer{status: rec.status, body: rec.buf.Bytes()}
}

// qualified resolves an AI-surface path to the explorer's `adapter://rel`.
func (a *aiOps) qualified(ctx context.Context, p string) (*model.Storage, string, string, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, "", "", err
	}
	return s, rel, joinAdapterPath(s.Name, rel), nil
}

// ── copy ─────────────────────────────────────────────────────────────────────

// Copy queues a copy of src to dst: the explorer's paste (POST
// /api/files/copy). dst is where the copy lands, as file_move's dst is - the
// folder it names and the name in it; the queue never overwrites, a taken
// name lands beside it (`rapor-copy.txt`). Across storages the bytes travel
// through the queue's transfer (ops.Transfer). Answers 202 {op}.
func (a *aiOps) Copy(ctx context.Context, src, dst string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Ops == nil {
		return doorsOff("copying"), nil
	}
	sSrc, relSrc, qSrc, err := a.qualified(ctx, src)
	if err != nil {
		return doorAnswer{}, err
	}
	sDst, relDst, _, err := a.qualified(ctx, dst)
	if err != nil {
		return doorAnswer{}, err
	}
	if relSrc == "" || relDst == "" {
		return doorAnswer{}, badInput("src and dst required")
	}
	// The copy READS its source and NAMES its destination (ops.Targets): no
	// app lock stops it, filex's own names and an encrypted folder's key file
	// do (Keyless, through gate).
	if err := a.gate(ctx, sSrc, writegate.Names(relSrc)); err != nil {
		return doorAnswer{}, err
	}
	if err := a.gate(ctx, sDst, writegate.Names(relDst)); err != nil {
		return doorAnswer{}, err
	}
	// The explorer's paste refuses the same crossing, but with a bare 409; this
	// one says E2E_BOUNDARY, as file_move does.
	if lk, ok := a.store.(e2e.NodeByPathLookup); ok {
		if err := e2e.GuardTransfer(ctx, lk, sSrc.ID, []string{relSrc}, sDst.ID, aiParent(relDst)); err != nil {
			return doorAnswer{}, err
		}
	}
	return a.door(ctx, a.doors.Ops.SubmitCopy, http.MethodPost, "/api/files/copy", nil, map[string]any{
		"source": []string{qSrc},
		"target": joinAdapterPath(sDst.Name, aiParent(relDst)),
		"name":   path.Base(relDst),
	}), nil
}

// ── apps ─────────────────────────────────────────────────────────────────────

// appActionsAnswer is app_actions' answer: the actions that apply to path.
type appActionsAnswer struct {
	Path    string                 `json:"path"`
	Actions []wasmplugin.ActionRow `json:"actions"`
}

// AppActions lists the app actions that apply to the file at p: the
// explorer's menu (GET /api/files/plugins/actions - running apps, enabled
// actions, admin-only ones for an administrator, an app permission the
// account holds), narrowed to the ones whose `applies` rule takes this file,
// judged by the very item the run judges (appItemOf).
func (a *aiOps) AppActions(ctx context.Context, p string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Apps == nil {
		return doorsOff("app actions"), nil
	}
	s, rel, q, err := a.qualified(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("a storage root is not an input - name a file or folder")
	}
	// An app cannot read a file inside an encrypted folder (the run refuses
	// it); say so in the AI surface's words rather than list actions that
	// would all fail.
	if err := a.encryptedRefusal(ctx, s, rel); err != nil {
		return doorAnswer{}, err
	}
	ans := a.door(ctx, a.doors.Apps.Actions, http.MethodGet, "/api/files/plugins/actions", nil, nil)
	if ans.status != http.StatusOK {
		return ans, nil
	}
	var menu wasmplugin.ActionsAnswer
	if err := json.Unmarshal(ans.body, &menu); err != nil {
		return doorAnswer{}, err
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return doorAnswer{}, err
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return doorAnswer{}, err
	}
	hash := pathkey.Hash(s.ID, "/"+rel)
	var keys []string
	if states, serr := a.store.ListAppPluginStateKeys(ctx, s.ID, []string{hash}); serr == nil {
		keys = states[hash]
	}
	out := appActionsAnswer{Path: q, Actions: []wasmplugin.ActionRow{}}
	for _, row := range menu.Actions {
		it := appItemOf(rel, obj, keys, currentUserID(ctx), row.Plugin)
		if wasmplugin.Matches(row.Applies, []wasmplugin.Item{it}) {
			out.Actions = append(out.Actions, row)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return doorAnswer{}, err
	}
	return doorAnswer{status: http.StatusOK, body: b}, nil
}

// AppRun starts an app's action on paths: the explorer's POST
// /api/files/plugins/actions/{plugin}/{action}/run, behind the same
// plugins.run permission. Without params, an action that has a form answers
// the form (`surface`) - its fields are what params takes; with params (an
// empty object for an action with no form) the job is queued: 202 {op,
// job_id}. A hidden action - the second half of an app's own flow - is
// refused, as from the menu.
func (a *aiOps) AppRun(ctx context.Context, plugin, action string, paths []string, params map[string]any) (doorAnswer, error) {
	if a.doors == nil || a.doors.Apps == nil {
		return doorsOff("app actions"), nil
	}
	if plugin == "" || action == "" {
		return doorAnswer{}, badInput("plugin and action are required")
	}
	if len(paths) == 0 {
		return doorAnswer{}, badInput("paths are required")
	}
	qualified := make([]string, 0, len(paths))
	for _, p := range paths {
		s, rel, q, err := a.qualified(ctx, p)
		if err != nil {
			return doorAnswer{}, err
		}
		if rel == "" {
			return doorAnswer{}, badInput("a storage root is not an input - name a file or folder")
		}
		if err := a.encryptedRefusal(ctx, s, rel); err != nil {
			return doorAnswer{}, err
		}
		qualified = append(qualified, q)
	}
	body := map[string]any{"paths": qualified}
	if params != nil {
		body["params"] = params
	}
	var wrap []func(http.Handler) http.Handler
	if a.doors.ACL != nil {
		wrap = append(wrap, RequirePermission(a.doors.ACL, perm.PluginsRun))
	}
	target := "/api/files/plugins/actions/" + url.PathEscape(plugin) + "/" + url.PathEscape(action) + "/run"
	return a.door(ctx, a.doors.Apps.Run, http.MethodPost, target,
		map[string]string{"plugin": plugin, "action": action}, body, wrap...), nil
}

// convertApp / convertAction name the Convert app's action, the job
// file_convert starts (the same one the explorer's Convert… menu runs).
const (
	convertApp    = "convert"
	convertAction = "convert"
)

// Convert converts the file at p to target (a format such as "xlsx", "pdf"):
// the Convert app's action with {target}. Answers 202 {op, job_id}; the
// result lands beside the file under a free name, and op_get reports it.
func (a *aiOps) Convert(ctx context.Context, p, target string) (doorAnswer, error) {
	target = strings.TrimPrefix(strings.TrimSpace(target), ".")
	if target == "" {
		return doorAnswer{}, badInput("target format is required (e.g. pdf, xlsx, docx)")
	}
	return a.AppRun(ctx, convertApp, convertAction, []string{p}, map[string]any{"target": target})
}

// ── the operations queue ─────────────────────────────────────────────────────

// OpsList lists the caller's operations (GET /api/files/ops): their own, or
// everybody's in reach for an administrator's unconfined key - the queue's
// own rule (opsViewer). status narrows it ("running", "failed"…).
func (a *aiOps) OpsList(ctx context.Context, status string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Ops == nil {
		return doorsOff("the operations queue"), nil
	}
	target := "/api/files/ops"
	if status != "" {
		target += "?" + url.Values{"status": {status}}.Encode()
	}
	return a.door(ctx, a.doors.Ops.List, http.MethodGet, target, nil, nil), nil
}

// OpGet is one operation's state (GET /api/files/ops/{id}); someone else's
// answers 404, as an id that never existed does.
func (a *aiOps) OpGet(ctx context.Context, id int64) (doorAnswer, error) {
	if a.doors == nil || a.doors.Ops == nil {
		return doorsOff("the operations queue"), nil
	}
	sid := strconv.FormatInt(id, 10)
	return a.door(ctx, a.doors.Ops.Status, http.MethodGet, "/api/files/ops/"+sid, map[string]string{"id": sid}, nil), nil
}

// OpCancel stops a pending or running operation the caller may see (POST
// /api/files/ops/{id}/cancel): 409 FINISHED once it has ended, 409
// NOT_CANCELLABLE for one that finishes what it starts.
func (a *aiOps) OpCancel(ctx context.Context, id int64) (doorAnswer, error) {
	if a.doors == nil || a.doors.Ops == nil {
		return doorsOff("the operations queue"), nil
	}
	sid := strconv.FormatInt(id, 10)
	return a.door(ctx, a.doors.Ops.Cancel, http.MethodPost, "/api/files/ops/"+sid+"/cancel", map[string]string{"id": sid}, nil), nil
}

// ── trash ────────────────────────────────────────────────────────────────────

// TrashList is the caller's trash (GET /api/files/manager/trash): what they
// may see of it, inside their root. storage (an adapter name) narrows it to
// one storage.
func (a *aiOps) TrashList(ctx context.Context, storageName string, limit, offset int) (doorAnswer, error) {
	if a.doors == nil || a.doors.Trash == nil {
		return doorsOff("the trash"), nil
	}
	q := url.Values{}
	if storageName != "" {
		s, _, err := a.resolveStorage(ctx, strings.TrimSuffix(storageName, "://")+"://")
		if err != nil {
			return doorAnswer{}, err
		}
		q.Set("storage_id", strconv.FormatInt(s.ID, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	target := "/api/files/manager/trash"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return a.door(ctx, a.doors.Trash.List, http.MethodGet, target, nil, nil), nil
}

// TrashRestore brings trash entries back (the `id`s of trash_list), on the
// queue (POST /api/files/manager/restore?queued=1): every entry is judged
// first - tenant, root, files.create where it came from, app locks - and the
// batch is queued only when all pass. At most 1000 per call. Answers 202
// {ops}.
func (a *aiOps) TrashRestore(ctx context.Context, ids []int64) (doorAnswer, error) {
	if a.doors == nil || a.doors.Trash == nil {
		return doorsOff("the trash"), nil
	}
	if len(ids) == 0 {
		return doorAnswer{}, badInput("node_ids required (the ids trash_list answers)")
	}
	return a.door(ctx, a.doors.Trash.Restore, http.MethodPost, "/api/files/manager/restore?queued=1", nil,
		map[string]any{"node_ids": ids}), nil
}

// ── version history ──────────────────────────────────────────────────────────

// versionNode is the catalogue row of the file at p, for the version routes
// that take a node id. Created on demand (catalogueOnDemand): an agent
// addresses files by path, and one written a moment ago may not be indexed.
func (a *aiOps) versionNode(ctx context.Context, p string, writes bool) (*model.Node, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, badInput("a storage root has no versions - name a file")
	}
	if writes {
		// A rollback or a snapshot writes this file's history or its bytes:
		// not one an app has frozen, and never an encrypted folder's key file
		// (rolling it back would bring back the key a password change retired).
		if err := a.gate(ctx, s, writegate.Writes(rel)); err != nil {
			return nil, err
		}
	}
	return catalogueOnDemand(ctx, a.store, a.resolver, a.sync(), s.ID, rel)
}

func (a *aiOps) versionsOn() bool {
	return a.doors != nil && a.doors.Versions != nil && a.doors.Versions.Service != nil
}

// Versions is a file's version history (GET /api/files/versions).
func (a *aiOps) Versions(ctx context.Context, p string) (doorAnswer, error) {
	if !a.versionsOn() {
		return doorsOff("version history"), nil
	}
	n, err := a.versionNode(ctx, p, false)
	if err != nil {
		return doorAnswer{}, err
	}
	return a.door(ctx, a.doors.Versions.List, http.MethodGet,
		"/api/files/versions?node_id="+strconv.FormatInt(n.ID, 10), nil, nil), nil
}

// VersionRestore replaces the file's content with one of its versions (POST
// /api/files/versions/restore): edit rights (files.modify), and the content
// being replaced is kept as a version first.
func (a *aiOps) VersionRestore(ctx context.Context, p string, versionID int64, snapshotCurrent bool) (doorAnswer, error) {
	if !a.versionsOn() {
		return doorsOff("version history"), nil
	}
	if versionID <= 0 {
		return doorAnswer{}, badInput("version_id is required (an id file_versions answers)")
	}
	n, err := a.versionNode(ctx, p, true)
	if err != nil {
		return doorAnswer{}, err
	}
	return a.door(ctx, a.doors.Versions.Restore, http.MethodPost, "/api/files/versions/restore", nil, map[string]any{
		"node_id": n.ID, "version_id": versionID, "snapshot_current": snapshotCurrent,
	}), nil
}

// Snapshot keeps the file's current content as a version now (POST
// /api/files/versions/snapshot): edit rights, as it writes into the storage.
func (a *aiOps) Snapshot(ctx context.Context, p string) (doorAnswer, error) {
	if !a.versionsOn() {
		return doorsOff("version history"), nil
	}
	n, err := a.versionNode(ctx, p, true)
	if err != nil {
		return doorAnswer{}, err
	}
	return a.door(ctx, a.doors.Versions.Snapshot, http.MethodPost, "/api/files/versions/snapshot", nil,
		map[string]any{"node_id": n.ID}), nil
}

// ── archives ─────────────────────────────────────────────────────────────────

// aiArchiveCreate is what archive_create packs: the explorer's archive/create
// body, with the AI surface's paths.
type aiArchiveCreate struct {
	Sources      []string `json:"sources"`
	Dest         string   `json:"dest"`
	Format       string   `json:"format,omitempty"`
	Password     string   `json:"password,omitempty"`
	EncryptNames bool     `json:"encrypt_filenames,omitempty"`
	Compression  *int     `json:"compression,omitempty"`
}

// ArchiveCreate packs sources into a new archive at dest on the server (POST
// /api/files/archive/create): zip, 7z or the TAR family, a password for zip
// and 7z. It never replaces an existing file (409 TARGET_EXISTS). Answers 202
// {op}. ⚠ The password travels to the job in memory only: no queue row, log
// or audit row carries it.
func (a *aiOps) ArchiveCreate(ctx context.Context, in aiArchiveCreate) (doorAnswer, error) {
	if a.doors == nil || a.doors.Archive == nil {
		return doorsOff("archive creation"), nil
	}
	if len(in.Sources) == 0 || in.Dest == "" {
		return doorAnswer{}, badInput("sources and dest are required")
	}
	sDest, relDest, qDest, err := a.qualified(ctx, in.Dest)
	if err != nil {
		return doorAnswer{}, err
	}
	if relDest == "" {
		return doorAnswer{}, badInput("dest must name the archive file")
	}
	if err := a.gate(ctx, sDest, writegate.Writes(relDest)); err != nil {
		return doorAnswer{}, err
	}
	// The archive is a new plaintext file where it lands (file_zip's rule).
	destDir := aiParent(relDest)
	if err := a.plaintextRefusal(ctx, sDest, destDir); err != nil {
		return doorAnswer{}, err
	}
	sources := make([]string, 0, len(in.Sources))
	byStorage := map[int64][]string{}
	for _, raw := range in.Sources {
		s, rel, q, rerr := a.qualified(ctx, raw)
		if rerr != nil {
			return doorAnswer{}, rerr
		}
		if rel == "" {
			return doorAnswer{}, badInput("source path required (cannot pack a storage root)")
		}
		sources = append(sources, q)
		byStorage[s.ID] = append(byStorage[s.ID], rel)
	}
	// An encrypted file does not leave its folder inside an archive either; the
	// encrypted folder itself may, its key file goes with it (file_zip's rule).
	// allow_plaintext waives only "plaintext into an encrypted folder", which
	// plaintextRefusal has already put to the caller.
	if lk, ok := a.store.(e2e.NodeByPathLookup); ok {
		for sid, rels := range byStorage {
			if gerr := e2e.GuardTransfer(ctx, lk, sid, rels, sDest.ID, destDir); gerr != nil &&
				!(plaintextConsented(ctx) && errors.Is(gerr, e2e.ErrPlaintextIntoEncrypted)) {
				return doorAnswer{}, gerr
			}
		}
	}
	body := map[string]any{"sources": sources, "dest": qDest}
	if in.Format != "" {
		body["format"] = in.Format
	}
	if in.Password != "" {
		body["password"] = in.Password
	}
	if in.EncryptNames {
		body["encrypt_filenames"] = true
	}
	if in.Compression != nil {
		body["compression"] = *in.Compression
	}
	return a.door(ctx, a.doors.Archive.Create, http.MethodPost, "/api/files/archive/create", nil, body), nil
}

// aiArchiveExtract is what archive_extract opens.
type aiArchiveExtract struct {
	Path     string   `json:"path"`
	Dest     string   `json:"dest,omitempty"`
	Members  []string `json:"members,omitempty"`
	Password string   `json:"password,omitempty"`
}

// ArchiveExtract extracts an archive already in storage (zip, 7z, RAR, the
// TAR family) into dest on the server - by default the archive's own folder
// (POST /api/files/archive/extract). members picks entries (all when empty).
// Answers 202 {op}. Members under one of filex's own names - or an encrypted
// folder's key file, from here - are skipped.
func (a *aiOps) ArchiveExtract(ctx context.Context, in aiArchiveExtract) (doorAnswer, error) {
	if a.doors == nil || a.doors.Archive == nil {
		return doorsOff("archive extraction"), nil
	}
	s, rel, q, err := a.qualified(ctx, in.Path)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("path must name the archive file")
	}
	// Ciphertext cannot be opened here (file_unzip's rule).
	if err := a.encryptedRefusal(ctx, s, rel); err != nil {
		return doorAnswer{}, err
	}
	destRel := aiParent(rel)
	qDest := joinAdapterPath(s.Name, destRel)
	if in.Dest != "" {
		sDst, dRel, dq, derr := a.qualified(ctx, in.Dest)
		if derr != nil {
			return doorAnswer{}, derr
		}
		if sDst.ID != s.ID {
			return doorAnswer{}, badInput("the destination must be on the same storage as the archive")
		}
		destRel, qDest = dRel, dq
	}
	// The members land as plaintext: into an encrypted folder only with
	// allow_plaintext (file_unzip's rule).
	if err := a.plaintextRefusal(ctx, s, destRel); err != nil {
		return doorAnswer{}, err
	}
	body := map[string]any{"path": q, "dest": qDest}
	if len(in.Members) > 0 {
		body["members"] = in.Members
	}
	if in.Password != "" {
		body["password"] = in.Password
	}
	return a.door(ctx, a.doors.Archive.Extract, http.MethodPost, "/api/files/archive/extract", nil, body), nil
}

// ── links ────────────────────────────────────────────────────────────────────

// SharesList lists the caller's own public links and file requests (GET
// /api/shares - "My shares"): in their tenant, inside their token's root. A
// row's token is what file_unshare revokes.
func (a *aiOps) SharesList(ctx context.Context, active bool, limit, offset int) (doorAnswer, error) {
	if a.doors == nil || a.doors.SharesMine == nil {
		return doorsOff("the link list"), nil
	}
	q := url.Values{}
	if active {
		q.Set("active", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	target := "/api/shares"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return a.door(ctx, a.doors.SharesMine.List, http.MethodGet, target, nil, nil), nil
}

// aiFileRequest is what file_request_create asks for.
type aiFileRequest struct {
	Path          string          `json:"path"`
	MaxUploads    int             `json:"max_uploads,omitempty"`
	DropSettings  json.RawMessage `json:"drop_settings,omitempty"`
	ExpiresInDays int             `json:"expires_in_days,omitempty"`
	Pin           bool            `json:"pin,omitempty"`
}

// FileRequest mints a file request - a public UPLOAD link into a folder (POST
// /api/files/share with kind=drop): edit rights on the folder and the
// account's share.upload_links, never into an encrypted folder
// (E2E_ENCRYPTED), as the explorer's dialog.
func (a *aiOps) FileRequest(ctx context.Context, in aiFileRequest) (doorAnswer, error) {
	if a.doors == nil || a.doors.Share == nil {
		return doorsOff("file requests"), nil
	}
	_, rel, q, err := a.qualified(ctx, in.Path)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("path must name a folder (not a storage root)")
	}
	body := map[string]any{"path": q, "kind": model.ShareKindDrop}
	if in.MaxUploads > 0 {
		body["max_uploads"] = in.MaxUploads
	}
	if len(in.DropSettings) > 0 && string(in.DropSettings) != "null" {
		body["drop_settings"] = in.DropSettings
	}
	if in.ExpiresInDays > 0 {
		body["expires_in"] = in.ExpiresInDays * 86400
	}
	if in.Pin {
		body["password"] = true
	}
	return a.door(ctx, a.doors.Share.HandleCreate, http.MethodPost, "/api/files/share", nil, body), nil
}

// ── REST twins (/api/ai) ─────────────────────────────────────────────────────
//
// Each route answers what its handler answered - status and body - or the AI
// surface's own refusal (writeAIError) for what was refused before the handler
// ran. routes.go asks each one's verb.

// writeDoor answers a door's result over REST.
func writeDoor(w http.ResponseWriter, ans doorAnswer, err error) {
	if err != nil {
		writeAIError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ans.status)
	_, _ = w.Write(ans.body)
}

// decodeAI reads a JSON body, answering 400 itself.
func decodeAI(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return false
	}
	return true
}

func queryInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(key))
	return n
}

// Copy → POST /api/ai/copy {src, dst}.
func (h *AI) Copy(w http.ResponseWriter, r *http.Request) {
	var body aiMoveBody
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.Copy(withDoorOrigin(r.Context(), r), body.Src, body.Dst)
	writeDoor(w, ans, err)
}

// AppActions → GET /api/ai/apps/actions?path=
func (h *AI) AppActions(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.AppActions(withDoorOrigin(r.Context(), r), r.URL.Query().Get("path"))
	writeDoor(w, ans, err)
}

// aiAppRunBody is the body of POST /api/ai/apps/run.
type aiAppRunBody struct {
	Plugin string         `json:"plugin"`
	Action string         `json:"action"`
	Paths  []string       `json:"paths"`
	Params map[string]any `json:"params,omitempty"`
}

// AppRun → POST /api/ai/apps/run {plugin, action, paths, params?}.
func (h *AI) AppRun(w http.ResponseWriter, r *http.Request) {
	var body aiAppRunBody
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.AppRun(withDoorOrigin(r.Context(), r), body.Plugin, body.Action, body.Paths, body.Params)
	writeDoor(w, ans, err)
}

// Convert → POST /api/ai/convert {path, target}.
func (h *AI) Convert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path   string `json:"path"`
		Target string `json:"target"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.Convert(withDoorOrigin(r.Context(), r), body.Path, body.Target)
	writeDoor(w, ans, err)
}

// OpsList → GET /api/ai/ops?status=
func (h *AI) OpsList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.OpsList(withDoorOrigin(r.Context(), r), r.URL.Query().Get("status"))
	writeDoor(w, ans, err)
}

// opID reads the {id} of an /api/ai/ops route.
func opID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return 0, false
	}
	return id, true
}

// OpGet → GET /api/ai/ops/{id}
func (h *AI) OpGet(w http.ResponseWriter, r *http.Request) {
	id, ok := opID(w, r)
	if !ok {
		return
	}
	ans, err := h.ops.OpGet(withDoorOrigin(r.Context(), r), id)
	writeDoor(w, ans, err)
}

// OpCancel → POST /api/ai/ops/{id}/cancel
func (h *AI) OpCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := opID(w, r)
	if !ok {
		return
	}
	ans, err := h.ops.OpCancel(withDoorOrigin(r.Context(), r), id)
	writeDoor(w, ans, err)
}

// TrashList → GET /api/ai/trash?storage=&limit=&offset=
func (h *AI) TrashList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.TrashList(withDoorOrigin(r.Context(), r), r.URL.Query().Get("storage"), queryInt(r, "limit"), queryInt(r, "offset"))
	writeDoor(w, ans, err)
}

// TrashRestore → POST /api/ai/trash/restore {node_ids}.
func (h *AI) TrashRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeIDs []int64 `json:"node_ids"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.TrashRestore(withDoorOrigin(r.Context(), r), body.NodeIDs)
	writeDoor(w, ans, err)
}

// VersionsList → GET /api/ai/versions?path=
func (h *AI) VersionsList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.Versions(withDoorOrigin(r.Context(), r), r.URL.Query().Get("path"))
	writeDoor(w, ans, err)
}

// VersionRestore → POST /api/ai/versions/restore {path, version_id, snapshot_current?}.
func (h *AI) VersionRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path            string `json:"path"`
		VersionID       int64  `json:"version_id"`
		SnapshotCurrent bool   `json:"snapshot_current,omitempty"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.VersionRestore(withDoorOrigin(r.Context(), r), body.Path, body.VersionID, body.SnapshotCurrent)
	writeDoor(w, ans, err)
}

// VersionSnapshot → POST /api/ai/versions/snapshot {path}.
func (h *AI) VersionSnapshot(w http.ResponseWriter, r *http.Request) {
	var body aiPathBody
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.Snapshot(withDoorOrigin(r.Context(), r), body.Path)
	writeDoor(w, ans, err)
}

// ArchiveCreate → POST /api/ai/archive/create {sources, dest, format?, password?, allow_plaintext?}.
func (h *AI) ArchiveCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		aiArchiveCreate
		AllowPlaintext bool `json:"allow_plaintext,omitempty"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ctx := withPlaintextConsent(withDoorOrigin(r.Context(), r), body.AllowPlaintext)
	ans, err := h.ops.ArchiveCreate(ctx, body.aiArchiveCreate)
	writeDoor(w, ans, err)
}

// ArchiveExtract → POST /api/ai/archive/extract {path, dest?, members?, password?, allow_plaintext?}.
func (h *AI) ArchiveExtract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		aiArchiveExtract
		AllowPlaintext bool `json:"allow_plaintext,omitempty"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ctx := withPlaintextConsent(withDoorOrigin(r.Context(), r), body.AllowPlaintext)
	ans, err := h.ops.ArchiveExtract(ctx, body.aiArchiveExtract)
	writeDoor(w, ans, err)
}

// SharesList → GET /api/ai/shares?active=&limit=&offset=
func (h *AI) SharesList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.SharesList(withDoorOrigin(r.Context(), r), r.URL.Query().Get("active") == "true", queryInt(r, "limit"), queryInt(r, "offset"))
	writeDoor(w, ans, err)
}

// FileRequest → POST /api/ai/share/request {path, max_uploads?, drop_settings?, expires_in_days?, pin?}.
func (h *AI) FileRequest(w http.ResponseWriter, r *http.Request) {
	var body aiFileRequest
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.FileRequest(withDoorOrigin(r.Context(), r), body)
	writeDoor(w, ans, err)
}
