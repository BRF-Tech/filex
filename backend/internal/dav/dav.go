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
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/davlock"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/thumb"
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
		sync:  protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginDAV),
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

	p, ok := h.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+h.cfg.Realm+`", charset="UTF-8"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	// Stamp the account AND the tenant scope in one call. /dav authenticates
	// itself, so it never ran auth.Middleware: without the user a PUT produced
	// a node owned by nobody (bytes uncounted, file event actorless), and
	// without the scope the root collection listed every tenant's storages — a
	// tenant admin who mapped /dav got all ten olivov tenants read-write (H4,
	// 2026-08-05). Both halves come from protocolauth now, so a protocol
	// cannot attach one and forget the other.
	r = r.WithContext(p.WithContext(r.Context()))

	if status, msg := h.preGate(r, p); status != 0 {
		http.Error(w, msg, status)
		return
	}

	dh := &webdav.Handler{
		Prefix:     Prefix,
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
func (h *Handler) authenticate(r *http.Request) (*principal, bool) {
	ident, secret, ok := r.BasicAuth()
	if !ok || secret == "" || strings.TrimSpace(ident) == "" {
		return nil, false
	}
	p, err := h.auth.Any(r.Context(), ident, secret)
	if err != nil {
		return nil, false
	}
	return &principal{Principal: p}, true
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
	st, err := h.cfg.Store.GetStorageByName(ctx, name)
	if err != nil || st == nil || !st.Enabled || !scopeOf(ctx).CanAccessStorage(st.ID) {
		return http.StatusNotFound, "storage not found"
	}
	if status, msg := h.gateWrite(ctx, p, st, rel, r.Method); status != 0 {
		return status, msg
	}

	// ⚠⚠ The quota, which /dav did not enforce at all until 2026-08-16 while
	// every other write surface did (manager, AI, ShareX, S3, SFTP, FTPS, NFS).
	// A user at their limit could keep writing indefinitely by mapping a drive
	// — and because syncWrite counts the bytes afterwards, the number in the
	// admin panel just kept climbing past the ceiling.
	//
	// Checked HERE, from Content-Length, rather than at Close: this is before
	// the client has uploaded anything, and it is the only place that can
	// answer 507 Insufficient Storage (RFC 4331 §5) — x/net/webdav turns a
	// Close error into 405, which tells a client to stop trying the METHOD.
	// A PUT with no Content-Length still gets caught at Close; see writeFile.
	if r.Method == http.MethodPut && r.ContentLength > 0 && h.cfg.Quota != nil {
		if u := auth.UserFrom(ctx); u != nil {
			if err := h.cfg.Quota.CheckCanWrite(ctx, u.ID, r.ContentLength); err != nil {
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
		dname, drel, ok := splitDavPath(du.Path)
		if !ok || dname == "" {
			return http.StatusBadGateway, "destination outside /dav"
		}
		if r.Method == "MOVE" && dname != name {
			// Rename cannot span drivers; COPY can (it streams through the
			// composite FileSystem), MOVE would need copy+delete orchestration.
			return http.StatusBadGateway, "cross-storage MOVE is not supported (use COPY + DELETE)"
		}
		dst := st
		if dname != name {
			if dst, err = h.cfg.Store.GetStorageByName(ctx, dname); err != nil || dst == nil || !dst.Enabled ||
				!scopeOf(ctx).CanAccessStorage(dst.ID) {
				return http.StatusConflict, "destination storage not found"
			}
		}
		if status, msg := h.gateWrite(ctx, p, dst, drel, r.Method); status != 0 {
			return status, msg
		}
	}
	return 0, ""
}

// gateWrite enforces the write-side policy on one (storage, rel) target:
// read-only flag → 403, missing driver capability → 403, ACL level below
// editor → 403.
func (h *Handler) gateWrite(ctx context.Context, p *principal, st *model.Storage, rel, method string) (int, string) {
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
	if set.Effective(rel) < acl.LevelEditor {
		return http.StatusForbidden, "insufficient permissions"
	}
	return 0, ""
}
