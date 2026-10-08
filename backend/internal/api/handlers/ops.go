package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Ops handles async copy/move/delete tasks.
//
// State is persisted in the pending_ops table — restart-safe so a crash
// doesn't lose in-flight work. The actual execution happens in the worker
// goroutine launched in server.New (see ops.Service.Run).
type Ops struct {
	Service *ops.Service
	Store   db.Store // for path → storage_id resolution in the per-verb endpoints
	ACL     *acl.Resolver
	// E2EPolicy is who may encrypt (e2e_policy_gate.go): a move under a name
	// of the caller's choosing that lands on a key file's or a `.fxe`'s name
	// asks it (refuseE2E), unless it is free: a folder, a `.fxe` that stays a
	// `.fxe`, a key file that stays its own folder's. A COPY that makes a new
	// encrypted item where it lands asks it too, whatever its name: a `.fxe`,
	// a key file, a folder that holds either (refuseE2ECopy). nil: not wired,
	// allowed.
	E2EPolicy *e2epolicy.Service
	// StorageResolver is how refuseE2E tells a file from a folder at a
	// source. nil: every source counts as a file.
	StorageResolver func(int64) (storage.Driver, error)
}

// NewOps constructs an Ops handler.
func NewOps(svc *ops.Service, store db.Store) *Ops {
	return &Ops{Service: svc, Store: store}
}

// AttachACL wires the RBAC resolver so async copy/move/delete require ≥editor
// on their sources (and destination) at submit time — the async worker itself
// runs without a user, so authorization is a submit-time gate.
func (o *Ops) AttachACL(r *acl.Resolver) { o.ACL = r }

// errors used by the per-verb wrappers.
var (
	errMixedAdapters = errsString("sources span multiple adapters")
	errBadPath       = errsString("bad source path")
)

func errUnknownAdapter(name string) error { return errsString("unknown adapter: " + name) }

type errsString string

func (e errsString) Error() string { return string(e) }

// opsBodyLimit is how much of a queue request's body is read: the limit the
// confinement middleware reads a JSON body to.
const opsBodyLimit = 8 << 20

// opsRequest is the body of POST /api/files/ops.
type opsRequest struct {
	Kind      string `json:"kind"` // copy, move, delete (clientKinds)
	StorageID int64  `json:"storage_id"`
	// DestStorageID targets another storage for copy/move (cross-depo paste).
	// Omitted or 0 means "same storage as the sources".
	DestStorageID int64    `json:"dest_storage_id,omitempty"`
	Sources       []string `json:"sources"`
	Dest          string   `json:"dest,omitempty"`
}

// clientKinds are the kinds a client may name on POST /api/files/ops: the
// three this handler knows how to judge.
//
// ⚠⚠ Not whatever ops.SubmitTo accepts. The queue's funnel also takes the
// kinds other handlers queue AFTER judging them in their own terms — a rename
// (vfRename: the storage's read-only flag, the new name's spelling), a
// restore (mayRestore: the entry's tenant, root, lock and original path), a
// purge (Purge: the entry's tenant), an upload commit, an app's action — and
// this handler judges none of that. Passing the client's kind straight through
// let `{"kind":"restore","storage_id":<own>,"sources":["<any node id>"]}`
// restore another tenant's trash entry, a hidden open-with copy or a node
// outside a root token, and a rename skip its name checks (2026-09-26 review
// of PR #61, which added those kinds to the funnel).
var clientKinds = map[string]bool{ops.OpCopy: true, ops.OpMove: true, ops.OpDelete: true}

// Submit queues a new op and returns the opID.
func (o *Ops) Submit(w http.ResponseWriter, r *http.Request) {
	// ⚠⚠ A `root:` token's paths are held to its root FIRST, the way the
	// confinement middleware holds a JSON body, whatever this body's
	// Content-Type: a text/plain body reached the checks below as written,
	// and they answered `..` 400 BAD_PATH, a read-only storage 403 READ_ONLY
	// and the root's refusal with the path appended - a different answer for
	// each shape and for what lay outside the folder (0.52.0).
	body, ok := confinedBody(w, r, opsBodyLimit)
	if !ok {
		return
	}
	var req opsRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !clientKinds[req.Kind] {
		writeError(w, r, http.StatusBadRequest, "bad_kind", nil, "code", "BAD_KIND")
		return
	}
	// A token does what its verbs name (auth/token_verbs.go), and here the
	// verb is the body's kind: delete asks `delete`, copy and move `write`.
	// Asked before anything else about the request.
	verb := auth.VerbWrite
	if req.Kind == ops.OpDelete {
		verb = auth.VerbDelete
	}
	if !auth.AllowVerb(w, r, verb) {
		return
	}
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	// Tenancy FIRST, before the ACL — because the ACL cannot answer this
	// question. `storage_id` and `dest_storage_id` arrive as bare integers in
	// the request body and nothing between here and the worker looks them up
	// against the caller's tenant; the only gate used to be aclAllowID, which
	// is tenant-blind and, on an rbac_enabled=false storage (the migration
	// default), answers *editor* for a plain user. So a member of one customer
	// could queue a copy, a move or a DELETE naming another customer's storage
	// and the queue would carry it out. Asking before the ACL also keeps the
	// refusal uniform: a foreign id answers 404 whether or not grants happen to
	// exist on it, so the endpoint cannot be used to enumerate the platform.
	if !ownsStorage(w, r, req.StorageID, "storage") {
		return
	}
	if destID := req.DestStorageID; destID != 0 && !ownsStorage(w, r, destID, "storage") {
		return
	}
	// ⚠⚠ The checks below ask about the storage-relative form of each path
	// (bareRel: an `<adapter>://` prefix dropped), but the queue is handed the
	// path as it was sent, and a driver reads `x/y://kutu/a` as the folder
	// `x/y:` and below it. A `dest` of `baska/alt://kutu/` was judged as `kutu`
	// (inside a `root:` token's folder, and where the caller holds a grant)
	// and written to `baska/alt:/kutu/` (GHSA-8gvc-6w52-6c7j). The paths of
	// this body are storage-relative (`storage_id` names the storage), so one
	// the checks and the worker would read differently is refused: a `://`
	// anywhere, or a `..` segment (`..\` too, which a Windows host folds).
	for _, p := range append(append([]string{}, req.Sources...), req.Dest) {
		if strings.Contains(p, "://") || pathHasDotDot(p) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path: " + p, "code": "BAD_PATH"})
			return
		}
	}
	// The storage-relative form of every path the op names, read once for the
	// checks below.
	destID := req.DestStorageID
	if destID == 0 {
		destID = req.StorageID
	}
	rels := make([]string, 0, len(req.Sources))
	for _, s := range req.Sources {
		rels = append(rels, bareRel(s))
	}
	drel := bareRel(req.Dest)
	writesDest := req.Kind != ops.OpDelete && req.Dest != ""
	// A `root:` token (or an X-Filex-Root) stays inside its root here too.
	//
	// ⚠ confine.Middleware cannot do it for this door: it rewrites the body
	// keys it knows (`source`, `target`, `path`, …) and this body names its
	// paths `sources` and `dest`, beside a bare `storage_id` — so a confined
	// caller could queue a copy, move or delete of anything in the storage
	// (lesson #543).
	//
	// The middleware's reading (confinedBody, above) places a bare path on the
	// root's own storage; this one places it on the storage `storage_id`
	// names, which is where the queue writes. Both answer alike: one refusal,
	// whether or not that storage exists (rootAllows fails closed on an id it
	// cannot resolve) and before its read-only flag is read.
	for _, rel := range rels {
		if !rootAllows(r.Context(), o.Store, req.StorageID, rel) {
			confine.Refuse(w)
			return
		}
	}
	if writesDest && !rootAllows(r.Context(), o.Store, destID, drel) {
		confine.Refuse(w)
		return
	}
	if o.refuseReadOnly(w, r, req.Kind, req.StorageID, destID) {
		return
	}
	// Names and app locks first (ops.Targets — the same list SubmitTo judges):
	// a frozen document is answered 423 with who froze it, before the
	// permission check below reads the lock's viewer cap as a plain 403.
	if o.gateOp(w, r, req.Kind, req.StorageID, req.DestStorageID, req.Sources, req.Dest) {
		return
	}
	// RBAC: require ≥editor on each source (and, for copy/move, the dest).
	for i, rel := range rels {
		if !o.opAllow(w, r, req.StorageID, rel, opSourcePerm(req.Kind), "insufficient permission: "+req.Sources[i]) {
			return
		}
	}
	if writesDest && !o.opAllow(w, r, destID, drel, opDestPerm(req.Kind), "insufficient permission (dest)") {
		return
	}

	/* wiring:e2 — same boundary rule for the unified endpoint. */
	ctx := r.Context()
	if req.Kind != ops.OpDelete {
		if lk, ok := o.Store.(e2e.NodeByPathLookup); ok {
			if err := e2e.GuardTransfer(r.Context(), lk, req.StorageID, rels, destID, drel); err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
		}
		var done bool
		if ctx, done = o.refuseE2E(w, r, req.Kind, req.StorageID, rels, destID, req.Dest); done {
			return
		}
	}

	op, err := o.Service.SubmitTo(ctx, req.Kind, req.StorageID, req.DestStorageID, req.Sources, req.Dest)
	if answerGate(w, r, err) {
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, op)
}

// Per-verb wrappers for the SFC. The SFC's `useFileApi` posts to:
//
//	POST /api/files/copy   { source: ["<adapter>://<rel>", …], target: "<adapter>://<rel>" }
//	POST /api/files/move   { source: ..., target: ..., sourceDir: "<adapter>://<rel>" }
//	POST /api/files/delete { source: ... }
//
// We translate to the unified ops.Submit by splitting the adapter
// prefix from the first source path. Mixed-adapter batches reject —
// `Mover.Move` etc. are storage-bound.

type perVerbReq struct {
	Source    []string `json:"source"`
	Target    string   `json:"target,omitempty"`
	SourceDir string   `json:"sourceDir,omitempty"`
	// Name, for a copy or move of ONE source, is the name it gets in the
	// target folder: the move and the rename are one step of the queue (one
	// driver Move/Copy onto the literal destination), so nothing can stop
	// half-way between them. Encrypted-names folders need exactly this: a
	// name is sealed for the folder it is in, so an item moved to another
	// folder must arrive under a name sealed for that folder
	// (docs/E2E-ENCRYPTION.md → "Folder ids"). A taken name is refused, never
	// suffixed.
	Name string `json:"name,omitempty"`
}

func (o *Ops) submitPerVerb(w http.ResponseWriter, r *http.Request, kind string) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	// A `root:` token's `source`, `target` and `sourceDir` are held to its root
	// first, as the middleware holds them in a JSON body - rewritten into the
	// root's qualified form, or refused with its answer - whatever this
	// body's Content-Type. Read as written, a text/plain body's path outside
	// the root met the storage lookup (400 unknown adapter), the sources' lock
	// and name gate, the read-only flag (403 READ_ONLY, the storage named)
	// and only then the root (0.52.0).
	body, ok := confinedBody(w, r, opsBodyLimit)
	if !ok {
		return
	}
	var req perVerbReq
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.Source) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing source"})
		return
	}

	storageID, sources, err := o.resolveBatch(r.Context(), req.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Tenancy before RBAC — see Submit. resolveBatch resolves the
	// `<adapter>://` prefix through Store.GetStorageByName, which tenantstore
	// does NOT wrap, so up to here a name is a name no matter whose storage it
	// is. This is the door that made `POST /api/files/delete
	// {"source":["<other tenant>://x"]}` a 202.
	if !ownsStorage(w, r, storageID, "storage") {
		return
	}

	// Sources first (their names and app locks), before the permission check
	// reads a lock's viewer cap as a plain 403; the destination once it is
	// known, below.
	if srcT, _ := ops.Targets(kind, sources, ""); gate(w, r, o.ACL, storageID, srcT...) {
		return
	}
	// RBAC: require ≥editor on every source (the async worker runs userless,
	// so authorize here at submit time).
	for _, rel := range sources {
		if !o.opAllow(w, r, storageID, rel, opSourcePerm(kind), "insufficient permission: "+rel) {
			return
		}
	}

	dest := ""
	if req.Target != "" {
		// SFC's per-verb endpoints model `target` as a directory
		// (the destination FOLDER for copy/move). The unified ops
		// worker's `joinIntoDir(dest, src)` keys off a trailing
		// slash to choose drop-into-dir vs rename-to-literal. The
		// SFC may or may not send the trailing slash — force one on
		// here so the user-facing semantics match the docs.
		// Bypass splitAdapterPath (which strips both ends) and
		// extract the relative manually so we keep the slash.
		raw := req.Target
		if idx := strings.Index(raw, "://"); idx >= 0 {
			raw = raw[idx+3:]
		}
		raw = strings.TrimLeft(raw, "/") // drop leading slashes
		// The sources are refused a `..` segment (resolveBatch); the target
		// the same way, `..\` included: the root check reads `\` as part of a
		// name and a Windows host's driver as a separator.
		if pathHasDotDot(raw) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad target path"})
			return
		}
		if raw == "" {
			// Storage root — drop sources at the root with their own
			// basename.
			//
			// ⚠⚠ "/" and not "": the ops service refuses an EMPTY dest
			// for copy/move ("ops: dest required"), so writing "" here
			// meant that pasting into the top of a storage — exactly
			// what FileExplorer.qualify() sends as `<storage>://` — was
			// rejected for every driver, built-in ones included
			// (measured 2026-08-19). The two halves of the same feature
			// disagreed: this side said "empty means root", that side
			// said "empty means missing". A trailing slash is also what
			// joinIntoDir keys off to drop into a directory rather than
			// rename onto a literal name.
			dest = "/"
		} else if strings.HasSuffix(raw, "/") {
			dest = raw
		} else {
			dest = raw + "/"
		}
	}
	// One source moved or copied under a name of the caller's choosing: the
	// destination becomes the literal path (joinIntoDir's "no trailing slash"
	// form), so the queue does both in one driver call.
	if req.Name != "" {
		if kind == "delete" || len(sources) != 1 || dest == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name needs a copy or move of exactly one source into a target"})
			return
		}
		if bad := badTransferName(req.Name); bad != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": bad})
			return
		}
		dest = strings.TrimRight(dest, "/") + "/" + req.Name
		if dest[0] == '/' {
			dest = strings.TrimLeft(dest, "/")
		}
	}

	// Which storage does the TARGET live in? For a paste inside one depo this
	// is the source's storage; for a paste into another depo it is a second
	// one, and the queue has to carry it.
	//
	// ⚠ This used to be dropped on the floor: `beta://hedef` had its prefix
	// stripped and the remaining `hedef/` was applied to the SOURCE storage,
	// so a cross-depo paste answered 202 and wrote the file into a folder it
	// invented inside the depo the user was copying FROM (measured
	// 2026-08-29). Silence, in the one direction where the user cannot see
	// the mistake — the file simply is not where they put it.
	destStorageID := storageID
	if kind != "delete" && req.Target != "" {
		if adapter := adapterOf(req.Target); adapter != "" {
			st, gerr := o.Store.GetStorageByName(r.Context(), adapter)
			if gerr != nil || st == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": errUnknownAdapter(adapter).Error()})
				return
			}
			destStorageID = st.ID
			// The destination is the WRITE end, and it is named by the client
			// exactly the way the sources are. Refuse before the read-only
			// check below, so a foreign storage answers 404 rather than
			// disclosing whether it happens to be writable.
			if !ownsStorage(w, r, destStorageID, "storage") {
				return
			}
		}
	}
	if o.refuseReadOnly(w, r, kind, storageID, destStorageID) {
		return
	}

	// RBAC: copy/move write into the destination dir — require ≥editor there,
	// in the DESTINATION's storage (checking the source's would ask about a
	// path in the wrong depo, and answer about permissions nobody granted).
	if kind != "delete" && dest != "" {
		if !o.opAllow(w, r, destStorageID, strings.Trim(dest, "/"), opDestPerm(kind), "insufficient permission (dest)") {
			return
		}
	}

	if o.gateOp(w, r, kind, storageID, destStorageID, sources, dest) {
		return
	}

	/* wiring:e2 — refuse transfers that cross an encryption boundary.
	 * Copy and move are server-side byte operations and the server holds no
	 * key, so it can neither encrypt on the way in nor decrypt on the way
	 * out. The only honest answer is no. See internal/e2e/guard.go. */
	ctx := r.Context()
	if kind != "delete" {
		if lk, ok := o.Store.(e2e.NodeByPathLookup); ok {
			if err := e2e.GuardTransfer(r.Context(), lk, storageID, sources, destStorageID, strings.Trim(dest, "/")); err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
		}
		var done bool
		if ctx, done = o.refuseE2E(w, r, kind, storageID, sources, destStorageID, dest); done {
			return
		}
	}

	op, err := o.Service.SubmitTo(ctx, kind, storageID, destStorageID, sources, dest)
	if answerGate(w, r, err) {
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
}

// refuseE2E is who may encrypt (e2e_policy_gate.go) at a queued copy or move,
// and answers the request when the rule says no. A copy is refuseE2ECopy's.
// For a move, a destination without a trailing slash is the path every
// source becomes (joinIntoDir's literal form): a source landing on a key
// file's or a `.fxe`'s name there is a new encryption unless it carries what
// is encrypted already (e2epolicy.RelocationEncrypts). A source moved INTO a
// folder keeps its own name, and is not asked.
//
// Asked for every source before anything is queued, as the transfer guard is:
// one refusal refuses the batch. The worker runs as nobody, so this is the
// only moment there is a person to judge — and a source let through because it
// was a folder is not settled: ctx tells the queue which ones are
// (ops.WithEncryptionSettled), and the worker fails any other that is a file
// by the time it runs. done: it answered the request.
func (o *Ops) refuseE2E(w http.ResponseWriter, r *http.Request, kind string, storageID int64, sources []string, destStorageID int64, dest string) (ctx context.Context, done bool) {
	if kind == ops.OpCopy {
		return o.refuseE2ECopy(w, r, storageID, sources, destStorageID, dest)
	}
	ctx = r.Context()
	if dest == "" || strings.HasSuffix(dest, "/") {
		return ctx, false
	}
	var srcDrv storage.Driver
	if o.StorageResolver != nil {
		if d, err := o.StorageResolver(storageID); err == nil {
			srcDrv = d
		}
	}
	if destStorageID == 0 {
		destStorageID = storageID
	}
	var settled []string
	for _, src := range sources {
		done, ok := refuseE2ERenameAt(w, r, o.E2EPolicy, o.Store, srcDrv, destStorageID, src, bareRel(dest), destStorageID == storageID)
		if done {
			return ctx, true
		}
		if ok {
			settled = append(settled, src)
		}
	}
	return ops.WithEncryptionSettled(ctx, settled), false
}

// refuseE2ECopy is who may encrypt at a queued COPY (operator decision
// 2026-10-03): a copy that makes a new encrypted item where it lands is asked
// as that encryption, whatever name it lands under - a `.fxe` or a key file,
// or a plain file given either name (by the name it lands under), and a
// folder that holds a key file or a `.fxe` anywhere below it (a new encrypted
// folder at its destination). e2epolicy.Service.CheckCopy decides; a move or
// a rename is refuseE2E's, and stays free for what is encrypted already.
//
// A source copied INTO a folder is asked under its own name there: the queue
// may land it beside a taken name (`…-copy`), which keeps a `.fxe`'s
// extension and is a new folder all the same. Under the approval policy one
// approval is spent per source that needs one, before anything is queued; a
// refusal refuses the batch. The FILE sources asked and allowed, and the ones
// nothing needed asking about, are settled for the worker
// (ops.WithEncryptionSettled); a folder never is, so a file that takes its
// place before the job runs is refused there. done: it answered the request.
func (o *Ops) refuseE2ECopy(w http.ResponseWriter, r *http.Request, storageID int64, sources []string, destStorageID int64, dest string) (ctx context.Context, done bool) {
	ctx = r.Context()
	svc := o.E2EPolicy
	if svc == nil || dest == "" {
		return ctx, false
	}
	if destStorageID == 0 {
		destStorageID = storageID
	}
	var srcDrv storage.Driver
	if o.StorageResolver != nil {
		if d, err := o.StorageResolver(storageID); err == nil {
			srcDrv = d
		}
	}
	into := strings.HasSuffix(dest, "/")
	var (
		settled []string
		dstSt   *model.Storage
	)
	u := auth.UserFrom(ctx)
	for _, src := range sources {
		dst := bareRel(dest)
		if into {
			dst = strings.Trim(path.Join(dst, path.Base("/"+strings.Trim(src, "/"))), "/")
		}
		isDir, err := svc.SourceIsFolder(ctx, storageID, srcDrv, src)
		if err != nil {
			return ctx, answerE2E(w, r, &model.Storage{ID: destStorageID}, err)
		}
		if !isDir && !e2epolicy.IsEncryptionName(dst) {
			settled = append(settled, src)
			continue
		}
		if dstSt == nil {
			if dstSt, err = o.Store.GetStorage(ctx, destStorageID); err != nil {
				return ctx, answerE2E(w, r, &model.Storage{ID: destStorageID}, err)
			}
		}
		if answerE2E(w, r, dstSt, svc.CheckCopy(ctx, u, dstSt, dst, storageID, src, isDir)) {
			return ctx, true
		}
		// A folder was judged as a folder: one a file takes the place of
		// before the job runs is the worker's to refuse (it is not settled).
		if !isDir {
			settled = append(settled, src)
		}
	}
	return ops.WithEncryptionSettled(ctx, settled), false
}

// badTransferName says what is wrong with a caller-chosen transfer name, or ""
// when it is a single path segment.
func badTransferName(name string) string {
	switch {
	case name == "." || name == "..":
		return "name must not be . or .."
	case strings.ContainsAny(name, "/\\\x00"):
		return "name must be a single path segment"
	case len(name) > 255:
		return "name is longer than 255 bytes"
	}
	return ""
}

// bareRel is a path of POST /api/files/ops as the storage-relative path the
// checks ask about: an `<adapter>://` prefix dropped, no surrounding slashes.
func bareRel(p string) string {
	if _, rel := splitAdapterPath(p); rel != "" {
		return rel
	}
	return strings.Trim(p, "/")
}

// refuseReadOnly answers 403 for an op that would change a read-only storage,
// and reports whether it did: a move or a delete takes its sources out of
// theirs, a copy or a move puts something into the destination's.
//
// ⚠ Both ends, on both doors. The per-verb endpoints asked only about a
// destination named with an adapter, and the generic endpoint about nothing:
// a delete or a move OUT of a read-only storage was queued and carried out,
// and a copy into one through POST /api/files/ops as well (2026-09-26). The
// storage's flag is its administrator's "nothing here changes", and the
// worker, which runs with nobody's permissions, never reads it.
func (o *Ops) refuseReadOnly(w http.ResponseWriter, r *http.Request, kind string, storageID, destStorageID int64) bool {
	if destStorageID == 0 {
		destStorageID = storageID
	}
	if kind == ops.OpMove || kind == ops.OpDelete {
		if st := readOnlyStorage(r.Context(), o.Store, storageID); st != nil {
			writeError(w, r, http.StatusForbidden, "read_only", apierr.Params{"storage": st.Name}, "code", "READ_ONLY")
			return true
		}
	}
	if kind == ops.OpCopy || kind == ops.OpMove {
		if st := readOnlyStorage(r.Context(), o.Store, destStorageID); st != nil {
			writeError(w, r, http.StatusForbidden, "read_only", apierr.Params{"storage": st.Name}, "code", "READ_ONLY")
			return true
		}
	}
	return false
}

// readOnlyStorage is the storage behind id when it is read-only, nil when it
// is writable or cannot be read (the worker then fails on it, as before).
func readOnlyStorage(ctx context.Context, store db.Store, id int64) *model.Storage {
	if store == nil {
		return nil
	}
	st, err := store.GetStorage(ctx, id)
	if err != nil || st == nil || !st.ReadOnly {
		return nil
	}
	return st
}

// adapterOf returns the `<adapter>` of an `<adapter>://<rel>` path, or "" when
// the path carries no prefix (legacy embedders send bare paths).
func adapterOf(p string) string {
	if i := strings.Index(p, "://"); i > 0 {
		return p[:i]
	}
	return ""
}

// resolveBatch splits adapter prefixes off each path, ensures all
// sources live in the same storage, and returns the resolved storage
// id + bare relative paths.
func (o *Ops) resolveBatch(ctx context.Context, sources []string) (int64, []string, error) {
	var storageID int64
	out := make([]string, 0, len(sources))
	for i, s := range sources {
		adapter, rel := splitAdapterPath(s)
		if adapter == "" {
			// Fall back to first storage so legacy embedders that drop
			// the prefix still work.
			storages, err := o.Store.ListEnabledStorages(ctx)
			if err != nil || len(storages) == 0 {
				return 0, nil, errNoStorages
			}
			adapter = storages[0].Name
		}
		st, err := o.Store.GetStorageByName(ctx, adapter)
		if err != nil || st == nil {
			return 0, nil, errUnknownAdapter(adapter)
		}
		if i == 0 {
			storageID = st.ID
		} else if st.ID != storageID {
			return 0, nil, errMixedAdapters
		}
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if rel == "" || pathHasDotDot(rel) {
			return 0, nil, errBadPath
		}
		out = append(out, rel)
	}
	return storageID, out, nil
}

// SubmitCopy / SubmitMove / SubmitDelete are the per-verb endpoints.

func (o *Ops) SubmitCopy(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "copy")
}
func (o *Ops) SubmitMove(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "move")
}
func (o *Ops) SubmitDelete(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "delete")
}

// List returns ops filtered by ?status=… (e.g. "running"). Used by the
// SPA's PendingOpsTray which polls every 2 s. Empty status returns the
// most-recent rows across all statuses (capped at 200 service-side).
//
// Response shape mirrors what the SPA's `opsApi.list` already
// understands: `{ "ops": [Op, …] }`, each row with its sources cut to a
// preview and counted (opListRow). The frontend's `normalizeOp`
// adapter then translates the backend's raw shape into the SPA's
// `PendingOp` contract.
func (o *Ops) List(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ops": []any{}})
		return
	}
	status := r.URL.Query().Get("status")
	// The tray is a plain authenticated user route, and the rows it returns
	// carry storage_id, dest_storage_id, sources_json and dest — live file
	// paths, spelled out. Restrict the query to the storages this caller can
	// reach (unscoped / supertenant: every storage), and below an
	// administrator to the rows this caller queued, on a single-tenant install
	// too (opsViewer).
	//
	// A trash empty is its TENANT's (ops.Viewer): it may name no storage at
	// all, and its counts describe that tenant's trash.
	list, err := o.Service.ListFor(readerCtx(r), status, opsViewer(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows := make([]opListRow, 0, len(list))
	for _, op := range list {
		if !opInCallerRoot(r.Context(), o.Store, op) {
			continue
		}
		rows = append(rows, newOpListRow(op))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ops": rows})
}

// readerCtx is the request's context carrying the language its rows are
// read in (srvtext.Reader). An app job keeps its label and its result in
// every language the app wrote (wasmplugin/jobtext.go); the row says them in
// the language on the reader's screen — `?lang=`, which the explorer sends —
// then the account's, then Accept-Language (pluginLang).
func readerCtx(r *http.Request) context.Context {
	return srvtext.WithReader(r.Context(), pluginLang(r))
}

// opsViewer is who the request is to the queue: every row for an unscoped
// caller or the supertenant, a tenant's storages' rows and its own trash
// empties otherwise (ops.ViewerOf, from the same tenant scope
// confinedScope reads).
//
// ⚠ And within that, only an administrator follows everybody's operations.
// Anyone else is shown the rows they queued (ops.Viewer.Own): a row spells out
// its sources and its destination, and the tenant scope says nothing about
// which of that tenant's folders a member may see. Every member of a tenant
// was handed every other member's paths here — on a production install, a
// colleague's move into a folder of client records, whatever the reader's
// grants — and in full, by sequential id, on GET /ops/{id}.
//
// ⚠⚠ "An administrator" is the CREDENTIAL, not the account
// (auth.CallerMayAdminister), and a caller confined to a folder is never one
// here. A read-scoped token minted on an administrator's account, a `root:`
// token, or an administrator's own session narrowed by X-Filex-Root is a
// credential for one folder — and was handed every operation on the instance,
// with every id walkable, because the account behind it was an admin.
func opsViewer(r *http.Request) ops.Viewer {
	v := ops.Viewer{All: true}
	if _, confined := confinedScope(r.Context()); confined {
		v = ops.ViewerOf(r.Context())
	}
	if _, rooted := callerRoot(r.Context()); rooted || !auth.CallerMayAdminister(r.Context()) {
		v.Own = true
		if u := auth.UserFrom(r.Context()); u != nil {
			v.Actor = u.ID
		}
	}
	return v
}

// sees reports whether the caller may follow (list, read, cancel) op: the
// queue's own rule (opsViewer) and, for a caller confined to a folder, every
// path the row names inside that folder (opInCallerRoot).
func (o *Ops) sees(r *http.Request, op *ops.Op) bool {
	return opsViewer(r).Sees(op) && opInCallerRoot(r.Context(), o.Store, op)
}

// opInCallerRoot reports whether every path op names lies inside the caller's
// `root:` (callerRoot). Inert for an unconfined caller.
//
// ⚠ opsViewer narrows a confined caller to the rows its ACCOUNT queued, and
// one account commonly stands behind many `root:` tokens - the documented way
// to embed filex is one service account and a token per project
// (docs/INTEGRATION.md). Every project's token was handed every other
// project's operations, paths spelled out, and could cancel them
// (GHSA-8gvc-6w52-6c7j). A row is placed by what its kind names: paths for the
// file operations, the trash entry for a restore, the staged upload for an
// upload commit. What cannot be placed is outside the root, except a finished
// upload commit whose session is gone: it names nothing but an upload id.
func opInCallerRoot(ctx context.Context, store db.Store, op *ops.Op) bool {
	root, rooted := callerRoot(ctx)
	if !rooted {
		return true
	}
	if op == nil {
		return false
	}
	names := map[int64]string{}
	in := func(storageID int64, p string) bool {
		name, rel := splitAdapterPath(p)
		if name == "" {
			n, ok := names[storageID]
			if !ok {
				n = rootStorageName(ctx, store, storageID)
				names[storageID] = n
			}
			name = n
		}
		return root.Within(name, rel)
	}
	destID := op.DestStorageID
	if destID == 0 {
		destID = op.StorageID
	}
	switch op.Kind {
	case ops.OpCopy, ops.OpMove, ops.OpDelete, ops.OpRename, ops.OpArchiveCreate, ops.OpArchiveExtract, ops.OpPluginAction:
		for _, s := range op.Sources {
			if !in(op.StorageID, s) {
				return false
			}
		}
		// A delete names no destination, and an app's action names its job.
		if op.Kind != ops.OpDelete && op.Kind != ops.OpPluginAction && op.Dest != "" && !in(destID, op.Dest) {
			return false
		}
		return true
	case ops.OpRestore:
		for _, s := range op.Sources {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return false
			}
			n, err := store.GetNode(ctx, id)
			if err != nil || n == nil {
				return false
			}
			orig, known := trash.OriginalPath(n)
			if !known || !in(n.StorageID, orig) {
				return false
			}
		}
		return true
	case ops.OpUploadCommit:
		for _, s := range op.Sources {
			row, err := store.GetStagedUpload(ctx, s)
			if err != nil || row == nil {
				if op.Status == ops.StatusPending || op.Status == ops.StatusRunning {
					return false
				}
				continue
			}
			if !in(row.StorageID, row.StorageKey) {
				return false
			}
		}
		return true
	}
	return false
}

// listSourcesPreview is how many of an op's sources one LIST row carries.
//
// A row stores every path it was given, and this listing is what the explorer
// fetches when it mounts and, while an op runs, every 2 s. A bulk delete queued
// in batches of a few hundred paths left rows of up to 82 KB — 200 of them made
// an 11.5 MB answer that every browser opening the drive downloaded and parsed.
// Nothing that reads the list shows the paths: both trays count progress from
// `total`, and the explorer's operations center labels a row with its
// destination or, for a delete, the folder it came from (source_dir, which
// the server never sent before). GET /ops/{id} still answers every source.
const listSourcesPreview = 5

// opListRow is one LIST row: the op as stored, its sources cut to a preview
// and counted. The outer Sources shadows the embedded one in the JSON.
type opListRow struct {
	*ops.Op
	Sources          []string `json:"sources"`
	SourceCount      int      `json:"source_count"`
	SourcesTruncated bool     `json:"sources_truncated,omitempty"`
	SourceDir        string   `json:"source_dir,omitempty"`
}

func newOpListRow(op *ops.Op) opListRow {
	row := opListRow{
		Op:          op,
		Sources:     op.Sources,
		SourceCount: len(op.Sources),
		SourceDir:   commonSourceDir(op.Sources),
	}
	if row.Sources == nil {
		row.Sources = []string{}
	}
	if len(row.Sources) > listSourcesPreview {
		row.Sources = row.Sources[:listSourcesPreview]
		row.SourcesTruncated = true
	}
	return row
}

// commonSourceDir is the deepest folder holding every source, in the sources'
// own storage-relative form; "" is the storage root.
func commonSourceDir(sources []string) string {
	var common []string
	for i, s := range sources {
		var segs []string
		if d := path.Dir(strings.Trim(s, "/")); d != "." {
			segs = strings.Split(d, "/")
		}
		if i == 0 {
			common = segs
			continue
		}
		n := 0
		for n < len(common) && n < len(segs) && common[n] == segs[n] {
			n++
		}
		common = common[:n]
		if n == 0 {
			break
		}
	}
	return strings.Join(common, "/")
}

// Cancel ends a pending or running op the caller may see: their own, or any
// in reach for an administrator (opsViewer). A row already finished answers
// 409 FINISHED, and a running one that finishes what it starts (a rename, a
// restore, a purge) 409 NOT_CANCELLABLE; an unknown id, another tenant's or
// another person's, 404, in the same words Status uses so the id range cannot
// be probed.
func (o *Ops) Cancel(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	op, err := o.Service.Get(readerCtx(r), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	// The person who queued it, or an administrator: opsViewer already narrows
	// everybody else to their own rows. A row that names nobody (written before
	// actor_id existed) is an administrator's to stop.
	if !o.sees(r, op) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	ok, err := o.Service.Cancel(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		// Read again: the row may have been claimed between the read above
		// and the cancel. A rename, a restore or a purge that has started
		// runs to its end (ops.Op.Cancellable is false) — it is not finished,
		// and saying so sent the person looking for a result that was still
		// on its way.
		if cur, gerr := o.Service.Get(r.Context(), id); gerr == nil &&
			(cur.Status == ops.StatusPending || cur.Status == ops.StatusRunning) {
			writeError(w, r, http.StatusConflict, "not_cancellable", nil, "code", "NOT_CANCELLABLE")
			return
		}
		writeError(w, r, http.StatusConflict, "finished", nil, "code", "FINISHED")
		return
	}
	op, _ = o.Service.Get(readerCtx(r), id)
	writeJSON(w, http.StatusOK, map[string]any{"op": op})
}

// Status returns the live or final state of a submitted op.
func (o *Ops) Status(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	op, err := o.Service.Get(readerCtx(r), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	// Ownership on the single-row read, matching the listing. The refusal wears
	// the same "unknown op" the miss above already produces, so probing the id
	// range cannot count another tenant's — or another person's — operations.
	// An op is in reach if EITHER end is — a cross-storage copy belongs to both
	// sides — and a trash empty is its tenant's (ops.Viewer); below an
	// administrator, only the caller's own row is (opsViewer).
	if !o.sees(r, op) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// gateOp asks writegate about everything one operation touches (ops.Targets),
// on the source storage and on the destination storage.
func (o *Ops) gateOp(w http.ResponseWriter, r *http.Request, kind string, storageID, destStorageID int64, sources []string, dest string) bool {
	if destStorageID == 0 {
		destStorageID = storageID
	}
	src, dst := ops.Targets(kind, sources, dest)
	return gate(w, r, o.ACL, storageID, src...) || gate(w, r, o.ACL, destStorageID, dst...)
}

// opSourcePerm is the per-user permission each source of an op needs, or ""
// for a copy: copying reads the source (it stays where it is) and the check
// that matters is files.create at the destination. The source keeps the
// ≥editor level it always needed, checked separately.
func opSourcePerm(kind string) perm.Perm {
	switch kind {
	case "move":
		return perm.FilesMove
	case "delete":
		return perm.FilesDelete
	default:
		return ""
	}
}

// opDestPerm is the permission a copy or move needs at its destination.
func opDestPerm(kind string) perm.Perm {
	if kind == "move" {
		return perm.FilesMove
	}
	return perm.FilesCreate
}

// opAllow checks one path of an op: the per-user permission p when set (its
// level included), otherwise the plain ≥editor level. It writes the refusal.
func (o *Ops) opAllow(w http.ResponseWriter, r *http.Request, storageID int64, rel string, p perm.Perm, legacyMsg string) bool {
	// The token's `root:` first. Every source and every destination of the
	// three per-verb doors and of POST /ops is asked here, so this holds them
	// to the root however the body was sent: up to 0.52 confine.Middleware
	// rewrote the per-verb `source`/`target` only in a body labelled JSON,
	// and the same body as text/plain reached the queue untouched
	// (GHSA-8gvc-6w52-6c7j).
	if !rootAllows(r.Context(), o.Store, storageID, rel) {
		confine.Refuse(w)
		return false
	}
	// A queued copy, move or delete of an entry the storage could not answer
	// for, or into one, is refused before it is queued (issue #104).
	if refuseUnavailableID(w, r, o.Store, storageID, rel) {
		return false
	}
	if p == "" {
		if !aclAllowID(r.Context(), o.ACL, o.Store, storageID, rel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": legacyMsg})
			return false
		}
		return true
	}
	if v := aclCanID(r.Context(), o.ACL, o.Store, storageID, rel, p); !v.ok {
		if !v.WritePerm(w, r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": legacyMsg})
		}
		return false
	}
	return true
}
