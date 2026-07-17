// Package dav exposes filex storages as a WebDAV server under /dav.
//
// Layout: /dav/<storage-name>/<path> — the first path segment selects a
// configured storage; the (virtual) root collection lists every storage the
// authenticated caller may see. The heavy lifting is done by
// golang.org/x/net/webdav; this package contributes:
//
//   - HTTP Basic authentication (username = account e-mail; the password is
//     tried first against the account password, then as an API token), see
//     authenticate().
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
	"crypto/sha256"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Prefix is the URL prefix the handler is mounted at.
const Prefix = "/dav"

// credTTL is how long a successful password verification is cached so the
// per-request Basic auth of WebDAV clients doesn't run bcrypt (~100ms) on
// every PROPFIND. Only POSITIVE password results are cached; API-token
// lookups are a single indexed sha256 query and always run fresh.
const credTTL = 5 * time.Minute

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
	// MultiTenant mirrors config.MultiTenant for the login policy check.
	MultiTenant bool
	// Realm for WWW-Authenticate (default "filex").
	Realm string
}

// Handler is the /dav HTTP handler.
type Handler struct {
	cfg   Config
	locks webdav.LockSystem

	credMu sync.Mutex
	creds  map[[32]byte]credEntry
}

type credEntry struct {
	userID int64
	exp    time.Time
}

// NewHandler builds the /dav handler. The lock system is shared across all
// requests (class-2 locks demand server-side state).
func NewHandler(cfg Config) *Handler {
	if cfg.Realm == "" {
		cfg.Realm = "filex"
	}
	return &Handler{
		cfg:   cfg,
		locks: webdav.NewMemLS(),
		creds: map[[32]byte]credEntry{},
	}
}

// principal is the resolved caller for one request.
type principal struct {
	user  *model.User
	token *model.APIToken // nil when password-authenticated
}

// hasScope reports whether the caller may use the given verb scope. Password
// sessions carry every scope; tokens consult their allow-list.
func (p *principal) hasScope(scope string) bool {
	if p.token == nil {
		return true
	}
	return p.token.HasScope(scope)
}

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
// must be the account e-mail. The password is tried in order:
//
//  1. account password (bcrypt against users.password_hash); accounts with
//     TOTP enabled are refused here — Basic auth cannot carry a second
//     factor, so those accounts must mint an API token instead.
//  2. API token (sha256 lookup in api_tokens); the token must belong to the
//     user with that e-mail. Tokens carrying a `root:` confinement scope are
//     refused: /dav has no confine middleware, accepting them would turn a
//     subtree-limited credential into whole-tree access.
func (h *Handler) authenticate(r *http.Request) (*principal, bool) {
	email, secret, ok := r.BasicAuth()
	if !ok || secret == "" {
		return nil, false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, false
	}
	ctx := r.Context()

	// 1) account password (with positive-result cache).
	if u := h.authPassword(ctx, email, secret); u != nil {
		if !auth.LoginAllowed(ctx, h.cfg.Store, h.cfg.MultiTenant, u) {
			return nil, false
		}
		return &principal{user: u}, true
	}

	// 2) API token.
	tok, err := h.cfg.Store.GetAPITokenByHash(ctx, apitoken.HashToken(secret))
	if err != nil || tok == nil {
		return nil, false
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		return nil, false
	}
	for _, s := range strings.Split(tok.Scopes, ",") {
		if strings.HasPrefix(strings.TrimSpace(s), apitoken.ScopeRootPrefix) {
			return nil, false // confined token — see doc comment
		}
	}
	u, err := h.cfg.Store.GetUser(ctx, tok.UserID)
	if err != nil || u == nil || !strings.EqualFold(u.Email, email) {
		return nil, false
	}
	if !auth.LoginAllowed(ctx, h.cfg.Store, h.cfg.MultiTenant, u) {
		return nil, false
	}
	_ = h.cfg.Store.TouchAPIToken(ctx, tok.ID)
	return &principal{user: u, token: tok}, true
}

// authPassword verifies email+password against the users table, consulting
// the positive-result cache first so steady-state WebDAV traffic costs one
// cheap user fetch instead of a bcrypt compare per request.
func (h *Handler) authPassword(ctx context.Context, email, password string) *model.User {
	key := sha256.Sum256([]byte(email + "\x00" + password))
	now := time.Now()

	h.credMu.Lock()
	ent, hit := h.creds[key]
	h.credMu.Unlock()
	if hit && ent.exp.After(now) {
		u, err := h.cfg.Store.GetUser(ctx, ent.userID)
		if err == nil && u != nil && strings.EqualFold(u.Email, email) &&
			u.PasswordHash != "" && !u.TOTPEnabled {
			return u
		}
	}

	u, err := h.cfg.Store.GetUserByEmail(ctx, email)
	if err != nil || u == nil || u.PasswordHash == "" || u.TOTPEnabled {
		return nil
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil
	}

	h.credMu.Lock()
	if len(h.creds) > 4096 { // crude bound; entries also expire via TTL
		h.creds = map[[32]byte]credEntry{}
	}
	h.creds[key] = credEntry{userID: u.ID, exp: now.Add(credTTL)}
	h.credMu.Unlock()
	return u
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
	if !p.hasScope(scope) {
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
	st, err := h.cfg.Store.GetStorageByName(ctx, name)
	if err != nil || st == nil || !st.Enabled {
		return http.StatusNotFound, "storage not found"
	}
	if status, msg := h.gateWrite(ctx, p, st, rel, r.Method); status != 0 {
		return status, msg
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
			if dst, err = h.cfg.Store.GetStorageByName(ctx, dname); err != nil || dst == nil || !dst.Enabled {
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
	set, err := h.cfg.ACL.LoadSet(ctx, p.user, st)
	if err != nil {
		return http.StatusInternalServerError, "acl load failed"
	}
	if set.Effective(rel) < acl.LevelEditor {
		return http.StatusForbidden, "insufficient permissions"
	}
	return 0, ""
}
