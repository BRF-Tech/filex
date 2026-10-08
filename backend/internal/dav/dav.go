// Package dav exposes filex storages as a WebDAV server under /dav.
//
// Layout: /dav/<storage-name>/<path> — the first path segment selects a
// configured storage; the (virtual) root collection lists every storage the
// authenticated caller may see. The heavy lifting is done by
// golang.org/x/net/webdav; this package contributes:
//
//   - HTTP Basic authentication (username = account e-mail OR the account's
//     username, whichever the client typed; the password is tried first
//     against the account password, then as an API token), see authenticate().
//   - A composite webdav.FileSystem bridging storage.Driver + its optional
//     capability sub-interfaces (fs.go / file.go).
//   - Authorization: storage read_only flag, ACL/RBAC via internal/acl, and
//     API-token verb scopes — enforced BOTH in a pre-gate here (so read-only /
//     forbidden writes deterministically return 403; x/net/webdav maps
//     filesystem errors to 404/405, never 403) AND inside the FileSystem
//     (defense in depth).
//   - Class-2 locking via webdav.NewMemLS() so Windows drive mapping can
//     write.
//   - Best-effort DB node-cache + search-index + thumbnail sync after
//     mutations (dbsync.go) — a sync failure never breaks the WebDAV reply.
//
// Kill switch: FILEX_DAV=0 (config.DAV.Enabled) — the handler then answers
// 404 for the whole subtree.
package dav

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/davlock"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/protoperm"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storageref"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// scopeOf returns the request's tenant scope. A nil scope means "unscoped"
// (single-tenant mode, background work) and reaches everything, matching
// tenant.Scope's own contract.
func scopeOf(ctx context.Context) *tenant.Scope {
	s, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}
	return s
}

// Prefix is the URL prefix the handler is mounted at.
const Prefix = "/dav"

// Config wires the handler to the server's shared services.
type Config struct {
	// Enabled — FILEX_DAV kill switch. When false ServeHTTP answers 404.
	Enabled bool
	Store   db.Store
	// Resolver returns the live storage.Driver for a storage id (the same
	// resolver the API handlers use).
	Resolver func(int64) (storage.Driver, error)
	// ACL resolves per-user grants (RBAC). Required.
	ACL *acl.Resolver
	// E2EPolicy is who may encrypt (internal/e2epolicy): a write that CREATES
	// an encrypted folder's key file or a `.fxe` asks it
	// (protoperm.EncryptionAllowed). The router's own, handed over like ACL.
	// nil: NewHandler builds one over Store (protoperm.EncryptionPolicy), so a
	// lost wiring line never switches the rule off.
	E2EPolicy *e2epolicy.Service
	// Index — optional search index; mutated nodes are (re/de)indexed.
	Index *search.Index
	// Thumbs — optional thumbnail pipeline; written files get async thumbs.
	Thumbs *thumb.Pipeline
	// Body resolves where a file's bytes are: the storage driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe —
	// unwired means driver-only, which is what /dav did before staging.
	Body *filebody.Resolver
	// Quota enforces the account's storage ceiling. Nil disables the check,
	// which is right for a test and wrong for the server — see preGate.
	Quota *quota.Service
	// MultiTenant mirrors config.MultiTenant for the login policy check.
	MultiTenant bool
	// Auth is the shared protocol credential resolver. Nil builds a private
	// one, which is correct for a test but wrong for the server: a resolver
	// per protocol is a credential cache per protocol, and therefore a
	// different answer per protocol to how long a revoked password keeps
	// working.
	Auth *protocolauth.Resolver
	// LockDir is where the WebDAV lock table is persisted. Empty keeps the
	// locks in memory only, which is what /dav did before 2026-08-16 and is
	// still right for a test — see internal/davlock for why it is wrong for a
	// server.
	LockDir string
	// Realm for WWW-Authenticate (default "filex").
	Realm string
}

// Handler is the /dav HTTP handler.
type Handler struct {
	cfg   Config
	locks webdav.LockSystem
	auth  *protocolauth.Resolver
	// sync is the shared post-write bookkeeping (node cache, search index,
	// thumbnails, write hooks). Shared with every other protocol server so a
	// fix lands once — see internal/protocolsync.
	sync *protocolsync.Syncer
}

// NewHandler builds the /dav handler. The lock system is shared across all
// requests (class-2 locks demand server-side state).
func NewHandler(cfg Config) *Handler {
	if cfg.Realm == "" {
		cfg.Realm = "filex"
	}
	// Who may encrypt: the router's rule, or one built over this handler's
	// own store — never none (protoperm.EncryptionPolicy).
	cfg.E2EPolicy = protoperm.EncryptionPolicy(cfg.E2EPolicy, cfg.Store, cfg.ACL, cfg.MultiTenant)
	// protocolauth's zero ConfinePolicy is ConfineRefuse, which is the right
	// one here: /dav has no confine middleware, so accepting a `root:`-scoped
	// token would silently promote a subtree-limited credential to whole-tree
	// access.
	pa := cfg.Auth
	if pa == nil {
		pa = protocolauth.New(cfg.Store, cfg.ACL, cfg.MultiTenant)
	}
	// ⚠⚠ The lock system is durable now. webdav.NewMemLS() holds every lock in
	// a map, so a restart silently forgot them all: a client that took a lock
	// before the deploy presented a token that named nothing afterwards, its
	// PUT got 412, and the server would meanwhile have let somebody else lock
	// the same file. The lock said "exclusive" and stopped being true without
	// telling anybody. See internal/davlock.
	locks := webdav.LockSystem(davlock.NewMemory())
	if cfg.LockDir != "" {
		if ls, err := davlock.New(cfg.LockDir); err == nil {
			locks = ls
		} else {
			// Not fatal: locks that only live in memory are what /dav always
			// had. Refusing to serve WebDAV because a cache file is unwritable
			// would trade a small problem for an outage.
			slog.Warn("dav: locks are memory-only", slog.String("err", err.Error()))
		}
	}
	return &Handler{
		cfg:   cfg,
		locks: locks,
		auth:  pa,
		sync:  protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginDAV).WithResolver(cfg.Resolver),
	}
}

// principal is the resolved caller for one request. It is protocolauth's
// Principal under a local name, so the rest of this package reads unchanged
// while identity, tenant scope, confinement and the ACL all arrive from the
// one door every protocol shares.
type principal struct{ *protocolauth.Principal }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled {
		http.NotFound(w, r)
		return
	}

	p, err := h.authenticate(r)
	if err != nil {
		// Too many wrong passwords: 429 says so, with when to come back. Every
		// other failure stays the one indistinguishable 401.
		if t, ok := protocolauth.AsThrottled(err); ok {
			secs := loginguard.RetryAfterSeconds(t.Verdict.RetryAfter)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			lang := srvtext.Pick(srvtext.FromAcceptLanguage(r.Header.Get("Accept-Language")))
			http.Error(w, t.Verdict.Message(lang), http.StatusTooManyRequests)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="`+h.cfg.Realm+`", charset="UTF-8"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	// Stamp the account AND the tenant scope in one call. /dav authenticates
	// itself, so it never ran auth.Middleware: without the user a PUT produced
	// a node owned by nobody (bytes uncounted, file event actorless), and
	// without the scope the root collection listed every tenant's storages — a
	// tenant admin who mapped /dav got all ten tenants of a multi-tenant deployment read-write (H4,
	// 2026-08-05). Both halves come from protocolauth now, so a protocol
	// cannot attach one and forget the other.
	r = r.WithContext(p.WithContext(r.Context()))

	if status, msg := h.preGate(r, p); status != 0 {
		http.Error(w, msg, status)
		return
	}

	// Under a base path (FILEX_BASE_PATH) the library has to see the path the
	// CLIENT used: it writes it into every PROPFIND href and reads it back out
	// of a MOVE/COPY Destination. The base was taken off for the router, so it
	// goes back on here, for the library only — splitDavPath above and below
	// keeps reading base-less paths.
	base := basepath.From(r.Context())
	if base != "" {
		u := *r.URL
		u.Path = base + u.Path
		if u.RawPath != "" {
			u.RawPath = base + u.RawPath
		}
		r = r.WithContext(r.Context())
		r.URL = &u
	}
	dh := &webdav.Handler{
		Prefix:     base + Prefix,
		FileSystem: newFS(h, p),
		LockSystem: h.locks,
		Logger: func(req *http.Request, err error) {
			if err != nil {
				slog.Debug("webdav", slog.String("method", req.Method),
					slog.String("path", req.URL.Path), slog.String("err", err.Error()))
			}
		},
	}
	dh.ServeHTTP(w, r)
}

// ───────────────────────────── authentication ─────────────────────────────

// authenticate resolves HTTP Basic credentials to a principal. The username
// field carries the account e-mail OR the account's username — identity.Resolve
// decides which, so this surface accepts exactly what the login form does. The
// password is tried in order:
//
//  1. account password (bcrypt against users.password_hash); accounts with
//     TOTP enabled are refused here — Basic auth cannot carry a second
//     factor, so those accounts must mint an API token instead.
//  2. API token (sha256 lookup in api_tokens); the token must belong to the
//     user with that e-mail. Tokens carrying a `root:` confinement scope are
//     refused: /dav has no confine middleware, accepting them would turn a
//     subtree-limited credential into whole-tree access.
//
// The error is protocolauth.ErrUnauthorized for every ordinary refusal and a
// *protocolauth.ThrottledError when a sign-in limit lock is what refused it.
func (h *Handler) authenticate(r *http.Request) (*principal, error) {
	ident, secret, ok := r.BasicAuth()
	if !ok || secret == "" || strings.TrimSpace(ident) == "" {
		return nil, protocolauth.ErrUnauthorized
	}
	// The address is the one every surface shares (internal/clientip), and it
	// is what the sign-in limit counts by: the peer's, or the forwarded one
	// only when the peer is a trusted proxy.
	ctx := protocolauth.WithSource(r.Context(), loginguard.ProtoDAV, clientip.FromRequest(r))
	// The address the client reached names the tenant on a multi-tenant
	// install — the same Host the web page resolves (docs/PROTOCOLS.md): a
	// drive mapped to a tenant's own address needs no realm in the user name,
	// and `beta/alex` there is refused.
	ctx = auth.WithRequestLoginHost(ctx, r)
	p, err := h.auth.Any(ctx, ident, secret)
	if err != nil {
		return nil, err
	}
	// access.webdav (package perm). WebDAV re-authenticates every request,
	// so taking it away ends the mapped drive at its next request.
	if p, err = h.auth.Admit(r.Context(), p, perm.AccessWebDAV, "webdav"); err != nil {
		return nil, protocolauth.ErrUnauthorized
	}
	return &principal{Principal: p}, nil
}

// ───────────────────────────── authorization ──────────────────────────────

// methodScope maps an HTTP/WebDAV method to the API-token verb scope it
// needs and whether it mutates. Unknown methods report ok=false and are
// rejected with 405 before reaching the library.
func methodScope(m string) (scope string, write bool, ok bool) {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "PROPFIND":
		return apitoken.ScopeRead, false, true
	case http.MethodDelete:
		return apitoken.ScopeDelete, true, true
	case http.MethodPut, "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PROPPATCH":
		return apitoken.ScopeWrite, true, true
	}
	return "", false, false
}

// splitDavPath splits a /dav URL path into (storage-name, storage-relative
// path). Both are cleaned; ok=false only for paths outside the prefix.
func splitDavPath(p string) (name, rel string, ok bool) {
	if p != Prefix && !strings.HasPrefix(p, Prefix+"/") {
		return "", "", false
	}
	rest := strings.Trim(strings.TrimPrefix(p, Prefix), "/")
	if rest == "" {
		return "", "", true // the /dav root collection
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], acl.CleanRel(rest[i+1:]), true
	}
	return rest, "", true
}

// preGate applies the deterministic authorization layer BEFORE the webdav
// library: token verb scopes, read-only storages, missing driver write
// capabilities and ACL levels on the target (and MOVE/COPY destination).
// Returns (0, "") to continue, or an HTTP status + message to short-circuit.
//
// Read-side visibility (RBAC CanSee) is intentionally NOT gated here — the
// FileSystem answers os.ErrNotExist for invisible paths so unauthorized
// callers see the same 404 an absent file yields (privacy: no exists-oracle).
func (h *Handler) preGate(r *http.Request, p *principal) (int, string) {
	scope, write, known := methodScope(r.Method)
	if !known {
		return http.StatusMethodNotAllowed, "method not allowed"
	}
	if !p.HasScope(scope) {
		return http.StatusForbidden, "token scope does not allow " + r.Method
	}
	name, rel, ok := splitDavPath(r.URL.Path)
	if !ok {
		return http.StatusNotFound, "outside /dav"
	}
	if !write {
		// A GET of a file's bytes is a download (internal/perm
		// files.download). Only refused where the path is VISIBLE — an
		// invisible one must keep answering the library's 404, or the refusal
		// becomes an existence oracle.
		if r.Method == http.MethodGet && name != "" && rel != "" {
			if status, msg := h.gateDownload(r.Context(), p, name, rel); status != 0 {
				return status, msg
			}
		}
		return 0, ""
	}
	if name == "" {
		// Mutating the virtual root (PUT /dav, MKCOL /dav/x as a storage…)
		// is meaningless — storages are created in the admin panel.
		return http.StatusMethodNotAllowed, "the /dav root is read-only"
	}

	ctx := r.Context()
	// Same tenant gate the FileSystem applies (GetStorageByName is not one of
	// the methods tenantstore confines). Without it the pre-gate stays an
	// oracle even though the data path is closed: a foreign read-only storage
	// would answer 403 "read-only" where a non-existent one answers 404.
	st, err := storageref.Resolve(ctx, h.cfg.Store, name)
	if err != nil || st == nil || !st.Enabled || !scopeOf(ctx).CanAccessStorage(st.ID) {
		return http.StatusNotFound, "storage not found"
	}
	if status, msg := h.gateWrite(ctx, p, st, rel, r.Method, false); status != 0 {
		return status, msg
	}

	// ⚠⚠ The quota, which /dav did not enforce at all until 2026-08-16. Every
	// write surface asks it before the bytes land: the manager and the staged
	// upload, S3, SFTP, FTPS, NFS, and since 0.54 the agent API's write funnel
	// (/api/ai/upload, MCP file_write, ShareX, /u/{ticket}) and the text
	// editor, which until then held only the per-file limit. A user at their
	// limit could keep writing indefinitely by mapping a drive — and because
	// syncWrite counts the bytes afterwards, the number in the admin panel just
	// kept climbing past the ceiling.
	//
	// Checked HERE, from Content-Length, rather than at Close: this is before
	// the client has uploaded anything, and it is the only place that can
	// answer 507 Insufficient Storage (RFC 4331 §5) — x/net/webdav turns a
	// Close error into 405, which tells a client to stop trying the METHOD.
	// A PUT with no Content-Length still gets caught at Close; see writeFile.
	if r.Method == http.MethodPut && r.ContentLength > 0 && h.cfg.Quota != nil {
		if u := auth.UserFrom(ctx); u != nil {
			if err := h.cfg.Quota.CheckFile(ctx, u.ID, r.ContentLength, r.ContentLength); err != nil {
				return http.StatusInsufficientStorage, "quota exceeded"
			}
		}
	}

	// The last moment at which the bytes a PUT is about to replace still exist
	// -- see writehook/overwrite.go.
	//
	// ⚠⚠ It goes HERE, in preGate, and not in writeFile.Close() where the
	// driver write happens, because x/net/webdav collapses ANY Close() error
	// into a bare 405 -- and every real WebDAV client (rclone, Finder, the
	// desktop sync client) reads 405 as "PUT is not allowed here" and
	// abandons the file instead of retrying what is actually a transient
	// refusal. preGate can choose its own status, and it also short-circuits
	// before the body is spooled to a temp file, so a refusal costs no bytes.
	//
	// Deliberately AFTER gateWrite and the quota check above: an unauthorized
	// caller must not be able to trigger the guard's own side effect (a
	// snapshot write) against a file it cannot write to, and an upload already
	// doomed by quota should not pay for a snapshot it will never need.
	// PUT-only -- MOVE/COPY destination overwrites are a separate surface.
	if r.Method == http.MethodPut {
		if err := writehook.BeforeOverwrite(ctx, st.ID, rel); err != nil {
			return http.StatusServiceUnavailable, "could not preserve the existing file: " + err.Error()
		}
	}

	// MOVE/COPY also mutate the Destination.
	if r.Method == "MOVE" || r.Method == "COPY" {
		du, err := url.Parse(r.Header.Get("Destination"))
		if err != nil || du.Path == "" {
			return 0, "" // let the library produce its 400
		}
		dpath, inBase := basepath.Strip(basepath.From(ctx), du.Path)
		dname, drel, ok := splitDavPath(dpath)
		if !inBase || !ok || dname == "" {
			return http.StatusBadGateway, "destination outside /dav"
		}
		if r.Method == "MOVE" && dname != name {
			// Rename cannot span drivers; COPY can (it streams through the
			// composite FileSystem), MOVE would need copy+delete orchestration.
			return http.StatusBadGateway, "cross-storage MOVE is not supported (use COPY + DELETE)"
		}
		dst := st
		if dname != name {
			if dst, err = storageref.Resolve(ctx, h.cfg.Store, dname); err != nil || dst == nil || !dst.Enabled ||
				!scopeOf(ctx).CanAccessStorage(dst.ID) {
				return http.StatusConflict, "destination storage not found"
			}
		}
		if status, msg := h.gateWrite(ctx, p, dst, drel, r.Method, true); status != 0 {
			return status, msg
		}
		if r.Method == "COPY" {
			// Who may encrypt (internal/e2epolicy): a COPY that makes a new
			// encrypted item where it lands is asked as that encryption
			// (operator decision 2026-10-03): a `.fxe` or a key file copied to
			// a name that is free, a plain file copied onto either name, a
			// folder that holds either anywhere below it
			// (protoperm.CopyEncryptionAllowed). Here, once per COPY, with the
			// source in hand: gateWrite sees only the destination.
			srcDrv, err := h.cfg.Resolver(st.ID)
			if err != nil {
				return http.StatusInternalServerError, "storage driver unavailable"
			}
			dstDrv, err := h.cfg.Resolver(dst.ID)
			if err != nil {
				return http.StatusInternalServerError, "storage driver unavailable"
			}
			if status, msg := davEncryption(protoperm.CopyEncryptionAllowed(ctx, h.cfg.E2EPolicy, srcDrv, st.ID, rel, dstDrv, dst, drel)); status != 0 {
				return status, msg
			}
		}
		if r.Method == "MOVE" {
			// A new name in the same folder is files.rename, another folder
			// files.move, both at once needs both — at both ends. The
			// FileSystem's Rename asks again; this is where the refusal can
			// name the permission.
			set, err := h.cfg.ACL.LoadSet(ctx, p.User, st)
			if err != nil {
				return http.StatusInternalServerError, "acl load failed"
			}
			for _, need := range perm.RelocateNeeds(st.ID, rel, st.ID, drel) {
				if !set.AllowsAt(rel, need) || !set.AllowsAt(drel, need) {
					return http.StatusForbidden, "permission denied: your account lacks the " + string(need) + " permission"
				}
			}
			// Who may encrypt (internal/e2epolicy): a MOVE onto a key file's
			// or a `.fxe`'s name is asked as a PUT of the file there would be:
			// onto a file there it replaces it (Overwrite: T), onto nothing or
			// a folder it creates one. Free are only a folder, a `.fxe` that
			// stays a `.fxe` and a key file that stays its own folder's
			// (protoperm.RenameEncryptionAllowed). Here in the pre-gate, as
			// gateWrite asks a COPY: the FileSystem's Rename cannot answer
			// with a status of its own.
			drv, err := h.cfg.Resolver(st.ID)
			if err != nil {
				return http.StatusInternalServerError, "storage driver unavailable"
			}
			if status, msg := davEncryption(protoperm.RenameEncryptionAllowed(ctx, h.cfg.E2EPolicy, drv, st, rel, drel)); status != 0 {
				return status, msg
			}
		}
	}
	return 0, ""
}

// gateWrite enforces the write-side policy on one (storage, rel) target:
// read-only flag → 403, missing driver capability → 403, ACL level below
// editor → 403.
func (h *Handler) gateWrite(ctx context.Context, p *principal, st *model.Storage, rel, method string, dest bool) (int, string) {
	if st.ReadOnly {
		return http.StatusForbidden, "storage is read-only"
	}
	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		return http.StatusInternalServerError, "storage driver unavailable"
	}
	caps := storage.ComputeCapabilities(drv)
	switch method {
	case http.MethodPut:
		if !caps.Write {
			return http.StatusForbidden, "storage does not support writes"
		}
	case "MKCOL":
		if !caps.Mkdir {
			return http.StatusForbidden, "storage does not support mkdir"
		}
	case http.MethodDelete:
		if !caps.Delete {
			return http.StatusForbidden, "storage does not support delete"
		}
	case "MOVE":
		if !caps.Move {
			return http.StatusForbidden, "storage does not support move"
		}
	case "COPY":
		if !caps.Write {
			return http.StatusForbidden, "storage does not support writes"
		}
	}
	set, err := h.cfg.ACL.LoadSet(ctx, p.User, st)
	if err != nil {
		return http.StatusInternalServerError, "acl load failed"
	}
	// writegate — filex's own names and app locks, the question every write
	// door asks — before the level check, which would read a lock's viewer cap
	// as a plain 403. A COPY only reads its source; everything else changes
	// the path it names (a DELETE or MOVE of a folder takes what is in it).
	//
	// ⚠ Measured before this line: with the signing app's freeze on
	// imza/NDA.docx, `DELETE /dav/depo/imza` answered 204 and the document
	// under signature went to the trash (Word or Explorer on a mapped drive
	// does exactly that). A lock is read with the set, per request, so a
	// freeze taken a second ago counts.
	target := writegate.Writes(rel).As(syspath.Mounted)
	if method == "COPY" && !dest {
		target = writegate.Names(rel).As(syspath.Mounted)
	}
	if err := writegate.Check(set, 0, target); err != nil {
		var le *writegate.LockedError
		if errors.As(err, &le) {
			return http.StatusLocked, "locked by app " + le.Lock.PluginName + ": " + le.Rel
		}
		return http.StatusForbidden, err.Error()
	}
	if set.Effective(rel) < acl.LevelEditor {
		return http.StatusForbidden, "insufficient permissions"
	}
	if need := davPerm(ctx, drv, method, rel, dest); need != "" && !set.AllowsAt(rel, need) {
		return http.StatusForbidden, "permission denied: your account lacks the " + string(need) + " permission"
	}
	// Who may encrypt (internal/e2epolicy): a request that CREATES an
	// encrypted folder's key file or a `.fxe` needs the policy and
	// files.encrypt. A COPY is asked by preGate, with its source in hand (a
	// copied folder is judged by what it holds); a MOVE too, and only when it
	// is not free (a folder, a `.fxe` that stays a `.fxe`, a key file that
	// stays its own folder's).
	// Whether it creates is protoperm.EncryptionAllowed's to say, not davPerm's:
	// a folder with the name reads as "something to replace" there, and a COPY
	// onto it moves the folder to the trash and creates the file.
	//
	// ⚠ Here, in the pre-gate, and only here. x/net/webdav answers a refused
	// OpenFile with 404, which a client reads as "no such folder"; and under
	// the approval policy the question spends the approval, so the
	// FileSystem's OpenFile after this pre-gate would ask a second time and
	// refuse its own write. A rule that could not be decided is the server's
	// failure (500), not a refusal.
	if davWritesFile(method, dest) {
		return davEncryption(protoperm.EncryptionAllowed(ctx, h.cfg.E2EPolicy, drv, st, rel))
	}
	return 0, ""
}

// davEncryption is what the pre-gate answers for the rule's verdict on a
// write: 403 for its no, 500 for a rule that could not be decided, and
// (0, "") to go on.
func davEncryption(a protoperm.EncryptionAnswer) (int, string) {
	switch a {
	case protoperm.EncryptionRefused:
		return http.StatusForbidden, "creating an encrypted folder or file is not allowed here"
	case protoperm.EncryptionUndecided:
		return http.StatusInternalServerError, protoperm.ErrEncryptionUndecided.Error()
	}
	return 0, ""
}

// davWritesFile reports whether method puts a FILE at its target, which
// creates it when no file is there, and gateWrite asks the rule for it: a PUT
// and a LOCK (x/net/webdav creates an empty file for a lock on nothing, and
// the PUT after it is then an overwrite). Not MKCOL, whose target is a folder,
// not PROPPATCH, which never creates, and not a COPY's destination, which
// preGate asks with the source in hand (protoperm.CopyEncryptionAllowed).
func davWritesFile(method string, dest bool) bool {
	switch method {
	case http.MethodPut, "LOCK":
		return !dest
	}
	return false
}

// davPerm is the per-user permission (internal/perm) a mutating method needs
// on rel — the source, or with dest the destination of a COPY/MOVE. "" means
// the ≥editor level alone decides, as it always did (the source of a COPY,
// which only reads it; UNLOCK, which releases what LOCK took).
func davPerm(ctx context.Context, drv storage.Driver, method, rel string, dest bool) perm.Perm {
	exists := func() bool {
		_, err := drv.Stat(ctx, rel)
		return err == nil
	}
	switch method {
	case http.MethodPut, "LOCK", "PROPPATCH":
		// LOCK and PROPPATCH are how Office and Finder begin a save; the
		// save is a change to a file that is there, or the addition of one.
		if exists() {
			return perm.FilesModify
		}
		return perm.FilesCreate
	case "MKCOL":
		return perm.FilesCreate
	case http.MethodDelete:
		return perm.FilesDelete
	case "MOVE":
		// Whether a MOVE is a rename (same folder), a move or both depends
		// on the two paths together: preGate asks once it has both, and the
		// FileSystem's Rename checks replacing what is at the destination.
		return ""
	case "COPY":
		if !dest {
			return ""
		}
		if exists() {
			return perm.FilesModify
		}
		return perm.FilesCreate
	}
	return ""
}

// gateDownload refuses a GET of a visible file the caller may not download.
func (h *Handler) gateDownload(ctx context.Context, p *principal, name, rel string) (int, string) {
	st, err := storageref.Resolve(ctx, h.cfg.Store, name)
	if err != nil || st == nil || !st.Enabled || !scopeOf(ctx).CanAccessStorage(st.ID) {
		return 0, "" // the FileSystem answers 404
	}
	set, err := h.cfg.ACL.LoadSet(ctx, p.User, st)
	if err != nil || set == nil {
		return 0, ""
	}
	if set.Effective(rel) >= acl.LevelViewer && !set.AllowsAt(rel, perm.FilesDownload) {
		return http.StatusForbidden, "permission denied: your account lacks the " + string(perm.FilesDownload) + " permission"
	}
	return 0, ""
}
