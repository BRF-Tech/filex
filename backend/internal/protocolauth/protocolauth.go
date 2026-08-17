// Package protocolauth is the single door through which every non-HTTP
// protocol gets its caller.
//
// # The failure it exists to prevent
//
// `tenant.FromContext` returns (nil, false) when no scope is set, and the
// package contract says absence means "unscoped — see everything"
// (tenant/context.go:40-47). That is correct for single-tenant installs and it
// is why the type was designed that way. The consequence is the problem: **an
// entry point that authenticates on its own starts unscoped**, and stays that
// way until a human remembers to attach a scope.
//
// This is not hypothetical. It already happened on the one protocol we had
// shipped: /dav authenticates with HTTP Basic, so it sat outside the chain
// where auth.TenantResolver runs, ListEnabledStorages saw an unscoped context,
// and the root collection listed every tenant's storages. A tenant admin who
// mapped /dav got all ten olivov tenants read-write (H4, 2026-08-05).
//
// S3, SFTP, FTP, NFS and FUSE all authenticate outside the HTTP middleware
// chain *by definition* — their credentials are not a session cookie. So /dav
// was not a mistake someone made; it was the first instance of a shape that
// reproduces once per protocol.
//
// # The rule
//
// A protocol handler must not be able to construct a caller by itself. There
// is one entry point, and it returns everything at once, so that "forgot to
// attach the scope" stops being expressible. The glue this replaces already
// existed and was merely scattered — auth.ScopeForUser, auth.LoginAllowed,
// acl.Resolver.LoadSet, confine — and a protocol that has to *remember* to call
// four things will eventually skip one.
//
// # What it deliberately does NOT do
//
// It does not decide whether a caller may perform an operation. That is
// Principal.ACL plus the per-protocol verb mapping, because the shape of a
// denial is protocol-specific: x/net/webdav maps filesystem errors to 404/405
// and never 403, while S3 must answer NoSuchKey rather than AccessDenied or the
// endpoint becomes an existence oracle across tenants. Each protocol owns its
// refusal; none of them owns identity.
package protocolauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ErrUnauthorized is the only failure a protocol may report to the wire.
//
// It is deliberately the single error: "no such account", "wrong password",
// "disabled account", "suspended tenant" and "that credential may not be used
// here" must be indistinguishable to a caller, or the endpoint becomes an
// account-enumeration oracle. Operators get the detail from the logs.
var ErrUnauthorized = errors.New("protocolauth: unauthorized")

// ConfinePolicy says what to do with a credential that carries a path
// confinement (an API token with a `root:` scope).
type ConfinePolicy int

const (
	// ConfineRefuse rejects such a credential outright. Correct for a protocol
	// that cannot enforce the restriction, because accepting one would silently
	// promote a subtree-limited credential to whole-tree access. This is what
	// /dav has always done (dav.go:186-190) and it is the safe default: a
	// protocol that has not yet been taught about confinement gets it for free.
	ConfineRefuse ConfinePolicy = iota
	// ConfineHonor accepts it and carries the Root on the Principal, for a
	// protocol that enforces it on every path. S3 is the natural case — an
	// access key's shape is already (bucket, prefix), which is IAM's own model.
	ConfineHonor
)

// Principal is one resolved caller. Everything a protocol needs to be tenant-
// and permission-aware arrives together; nothing here can be forgotten,
// because nothing here has to be fetched.
type Principal struct {
	// User is the account. Never nil on a successful resolve.
	User *model.User
	// Token is the API token used, or nil for password authentication.
	Token *model.APIToken
	// Scope is the tenant scope. NEVER nil when the install is multi-tenant —
	// tenant.DenyAll when it could not be resolved, so an unresolvable caller
	// sees nothing rather than everything.
	Scope *tenant.Scope
	// Confine is the path restriction the credential carries, or nil when it
	// carries none. A protocol that asked for ConfineHonor MUST enforce it.
	Confine *confine.Root
	// AccessKey is the S3 credential this caller signed with, or nil for every
	// other authentication route. Carried so the S3 layer can report the key in
	// the audit trail without a second lookup.
	AccessKey *model.S3AccessKey
	// SSHKey is the registered public key this caller authenticated with, or
	// nil. Export is the NFS export the mount was bound to, or nil.
	//
	// ⚠ These two are not decoration. Together with Token and AccessKey they
	// are what a live session is re-checked against (see Recheck): a credential
	// the Principal cannot name is a credential that cannot be revoked out from
	// under an open connection.
	SSHKey *model.SSHPublicKey
	Export *model.NFSExport

	acl    *acl.Resolver
	aclTTL time.Duration

	mu   sync.Mutex
	sets map[int64]aclEntry
}

type aclEntry struct {
	set *acl.Set
	exp time.Time
}

// WithContext stamps the request context with everything downstream code reads
// from it. Protocols call this once per request (or once per session, for
// stream protocols), and then behave exactly like an HTTP handler that went
// through the middleware chain.
//
// ⚠ Both halves matter and both have been forgotten before. Without the user,
// a write produces a node owned by nobody — so its bytes are never counted
// against a quota — and a file event with no actor, so the audit trail cannot
// say who did it (fixed for /dav in 2026-08-15). Without the scope, the caller
// sees every tenant.
func (p *Principal) WithContext(ctx context.Context) context.Context {
	if p == nil {
		return ctx
	}
	ctx = auth.WithUser(ctx, p.User)
	if p.Scope != nil {
		ctx = tenant.WithScope(ctx, p.Scope)
	}
	return ctx
}

// ACL returns the caller's grant set for one storage, loading it at most once
// per TTL.
//
// Caching here rather than per request is what makes the set usable by a
// stream protocol: an SFTP session issues thousands of stats, and LoadSet is a
// query.
//
// ⚠⚠ The TTL is not a performance knob, it is the answer to "I took away
// somebody's access — when does it stop working?". A Principal outlives one
// request: an SFTP or FTPS session is open for hours and a mount for days, so a
// set cached for the life of the Principal would mean a grant removed at
// 09:00 keeps serving files until the user logs out. It is the same number as
// the password cache (Resolver.CacheTTL) so there is ONE answer to that
// question rather than one per protocol.
func (p *Principal) ACL(ctx context.Context, s *model.Storage) (*acl.Set, error) {
	if p == nil || p.acl == nil {
		return nil, ErrUnauthorized
	}
	var key int64
	if s != nil {
		key = s.ID
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if ent, ok := p.sets[key]; ok && (p.aclTTL <= 0 || ent.exp.After(now)) {
		return ent.set, nil
	}
	set, err := p.acl.LoadSet(ctx, p.User, s)
	if err != nil {
		return nil, err
	}
	if p.sets == nil {
		p.sets = map[int64]aclEntry{}
	}
	p.sets[key] = aclEntry{set: set, exp: now.Add(p.aclTTL)}
	return set, nil
}

// ForgetACL drops the cached grant sets, so the next question hits the
// database. Called when a session is re-checked and the account changed under
// it — the alternative is waiting out the TTL twice.
func (p *Principal) ForgetACL() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sets = nil
}

// HasScope reports whether the credential grants an API-token verb scope.
// Password authentication carries every scope; a token consults its allow-list.
func (p *Principal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	if p.Token == nil {
		return true
	}
	return p.Token.HasScope(scope)
}

// Resolver turns credentials into principals.
type Resolver struct {
	Store db.Store
	ACL   *acl.Resolver
	// MultiTenant mirrors config.MultiTenant. When true a Principal always
	// carries a Scope.
	MultiTenant bool
	// Confine says how to treat a path-confined credential. The zero value is
	// ConfineRefuse, which is the conservative direction on purpose.
	Confine ConfinePolicy
	// Secrets seals and opens the credentials that must be recoverable rather
	// than hashed — today the S3 access keys, because SigV4 verifies by
	// recomputing an HMAC chain. Nil (or keyless) means no key is configured,
	// and issuing one then FAILS rather than storing plaintext.
	Secrets *secretbox.Box
	// CacheTTL is how long a successful PASSWORD verification is remembered.
	//
	// ⚠ This number is a security statement: it is how long a revoked password
	// keeps working. It exists because Basic-auth-style protocols present the
	// password on EVERY request and bcrypt costs ~100ms — without a cache a
	// WebDAV PROPFIND storm or an S3 client at a few hundred requests a second
	// would spend all its time hashing. Zero disables the cache.
	CacheTTL time.Duration

	mu    sync.Mutex
	creds map[[32]byte]credEntry
	// live is the registry of open stream-protocol sessions (see live.go).
	// Guarded by the same mutex; the maps are small and never touched on a hot
	// path.
	live     map[uint64]*LiveSession
	nextLive atomic.Uint64
}

type credEntry struct {
	userID int64
	exp    time.Time
}

// DefaultCacheTTL matches what /dav has used since it shipped.
const DefaultCacheTTL = 5 * time.Minute

// New builds a Resolver with the default cache TTL.
func New(store db.Store, aclResolver *acl.Resolver, multiTenant bool) *Resolver {
	return &Resolver{
		Store:       store,
		ACL:         aclResolver,
		MultiTenant: multiTenant,
		CacheTTL:    DefaultCacheTTL,
	}
}

// Password resolves an identifier (e-mail OR username — identity.Resolve owns
// which) plus an account password.
//
// ⚠ An account with TOTP enabled is REFUSED here, on every protocol. None of
// these protocols has a second-factor channel, so accepting the password would
// make each of them a documented 2FA bypass: whoever knows the password gets in
// without ever meeting the second factor. Such an account must mint an API
// token (or, later, an access key or a registered public key) — credentials
// that are individually revocable, which is the whole point of the app-specific
// password pattern.
func (r *Resolver) Password(ctx context.Context, identifier, password string) (*Principal, error) {
	ident := identity.Normalize(identifier)
	if ident == "" || password == "" {
		return nil, ErrUnauthorized
	}

	if u := r.cached(ctx, ident, password); u != nil {
		return r.principal(ctx, u, nil)
	}

	u, err := identity.Resolve(ctx, r.Store, ident)
	if err != nil || u == nil {
		return nil, ErrUnauthorized
	}
	if u.PasswordHash == "" || u.TOTPEnabled {
		return nil, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrUnauthorized
	}
	r.remember(ident, password, u.ID)
	return r.principal(ctx, u, nil)
}

// Token resolves an API token. When identifier is non-empty the token must
// belong to the account it names — the protocols that carry a username field
// (WebDAV Basic, FTP USER, SFTP) should pass it, so a token pasted against the
// wrong account is refused rather than silently acting as its real owner.
func (r *Resolver) Token(ctx context.Context, identifier, token string) (*Principal, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrUnauthorized
	}
	tok, err := r.Store.GetAPITokenByHash(ctx, apitoken.HashToken(token))
	if err != nil || tok == nil {
		return nil, ErrUnauthorized
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		return nil, ErrUnauthorized
	}

	root, confined := tokenRoot(tok)
	if confined && r.Confine == ConfineRefuse {
		return nil, ErrUnauthorized
	}

	u, err := r.Store.GetUser(ctx, tok.UserID)
	if err != nil || u == nil {
		return nil, ErrUnauthorized
	}
	if identifier != "" && !identity.Names(u, identifier) {
		return nil, ErrUnauthorized
	}
	_ = r.Store.TouchAPIToken(ctx, tok.ID)

	p, err := r.principal(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	if confined {
		p.Confine = &root
	}
	return p, nil
}

// PublicKey resolves a registered SSH public key by fingerprint.
//
// ⚠ It does NOT prove possession — x/crypto/ssh has already verified the
// signature by the time this is called. What it decides is which account the
// key belongs to, and whether that account may log in at all.
//
// ⚠ Unlike Password, an account with TOTP enabled is ACCEPTED here, and that is
// deliberate: a registered key is a second, individually revocable credential —
// exactly the app-specific-password pattern TOTP accounts are pointed at. The
// key can be switched off from the account's own settings without touching the
// password.
func (r *Resolver) PublicKey(ctx context.Context, identifier, fingerprint string) (*Principal, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, ErrUnauthorized
	}
	k, err := r.Store.GetSSHPublicKey(ctx, fingerprint)
	if err != nil || k == nil || !k.Usable() {
		return nil, ErrUnauthorized
	}
	u, err := r.Store.GetUser(ctx, k.UserID)
	if err != nil || u == nil {
		return nil, ErrUnauthorized
	}
	// ⚠ The key must belong to the account the client NAMED. Without this a
	// key registered by one user logs in as whoever the client typed, which is
	// impersonation with a valid signature.
	if identifier != "" && !identity.Names(u, identifier) {
		return nil, ErrUnauthorized
	}
	p, err := r.principal(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	p.SSHKey = k
	_ = r.Store.TouchSSHPublicKey(ctx, k.ID)
	return p, nil
}

// Any is the order the password-carrying protocols use: try the secret as an
// account password, then as an API token. It exists so every such protocol
// makes the same choice — the alternative is each one inventing its own order,
// and the order is observable (it decides which credential wins when a token
// happens to equal a password).
func (r *Resolver) Any(ctx context.Context, identifier, secret string) (*Principal, error) {
	if p, err := r.Password(ctx, identifier, secret); err == nil {
		return p, nil
	}
	return r.Token(ctx, identifier, secret)
}

// principal applies the policy checks every credential type shares and
// assembles the caller.
func (r *Resolver) principal(ctx context.Context, u *model.User, tok *model.APIToken) (*Principal, error) {
	if !auth.LoginAllowed(ctx, r.Store, r.MultiTenant, u) {
		return nil, ErrUnauthorized
	}
	p := &Principal{User: u, Token: tok, acl: r.ACL, aclTTL: r.CacheTTL}
	if r.MultiTenant {
		// Never nil in multi-tenant mode: ScopeForUser answers tenant.DenyAll
		// when the account has no resolvable provider, so an unresolvable
		// caller sees nothing instead of everything.
		p.Scope = auth.ScopeForUser(ctx, r.Store, u)
	}
	return p, nil
}

// tokenRoot extracts a `root:<adapter>://<rel>` confinement scope from a token.
func tokenRoot(tok *model.APIToken) (confine.Root, bool) {
	if tok == nil {
		return confine.Root{}, false
	}
	for _, s := range strings.Split(tok.Scopes, ",") {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, apitoken.ScopeRootPrefix) {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(s, apitoken.ScopeRootPrefix))
		if root, ok := confine.ParseRoot(raw); ok {
			return root, true
		}
	}
	return confine.Root{}, false
}

// ───────────────────────────── credential cache ─────────────────────────────

func (r *Resolver) cacheKey(ident, password string) [32]byte {
	return sha256.Sum256([]byte(ident + "\x00" + password))
}

// cached returns the account a previous successful verification recorded, or
// nil. It re-reads the account rather than trusting the cached copy, and
// re-checks the two things that must still hold — that the identifier still
// names this account, and that TOTP has not been switched on since.
func (r *Resolver) cached(ctx context.Context, ident, password string) *model.User {
	if r.CacheTTL <= 0 {
		return nil
	}
	key := r.cacheKey(ident, password)
	r.mu.Lock()
	ent, ok := r.creds[key]
	r.mu.Unlock()
	if !ok || !ent.exp.After(time.Now()) {
		return nil
	}
	u, err := r.Store.GetUser(ctx, ent.userID)
	if err != nil || u == nil {
		return nil
	}
	if !identity.Names(u, ident) || u.PasswordHash == "" || u.TOTPEnabled {
		return nil
	}
	return u
}

// remember records a successful password verification. Only POSITIVE results
// are cached: caching a failure would let a caller keep a lockout or a
// rate-limit decision alive past the point where the real check would pass.
func (r *Resolver) remember(ident, password string, userID int64) {
	if r.CacheTTL <= 0 {
		return
	}
	key := r.cacheKey(ident, password)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.creds == nil {
		r.creds = map[[32]byte]credEntry{}
	}
	if len(r.creds) > 4096 { // crude bound; entries also expire via TTL
		r.creds = map[[32]byte]credEntry{}
	}
	r.creds[key] = credEntry{userID: userID, exp: time.Now().Add(r.CacheTTL)}
}

// Forget drops every cached password result. Called when a credential is
// revoked so the TTL is an upper bound, not the only bound.
func (r *Resolver) Forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creds = nil
}
