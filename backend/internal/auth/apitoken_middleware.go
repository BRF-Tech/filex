package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/model"
)

// apiTokenHeaderName is the dedicated header AI/MCP callers send. Kept in
// sync with drivers/apitoken.HeaderName (declared here too so this file has
// no dependency on the driver package — the driver imports auth, not the
// reverse).
const apiTokenHeaderName = "X-Filex-Token"

// tokenUserHeaderName selects which of the token's configured usernames this
// request acts under (audit, shares, presence). Empty → the token's default.
// A name outside the token's allow-list is rejected with 403 so a typo'd
// integration surfaces immediately instead of blending into the default.
const tokenUserHeaderName = "X-Filex-Token-User"

// tokenCtxKey carries the matched *model.APIToken on the request context.
type tokenCtxKey struct{}

// tokenUserCtxKey carries the resolved token username for the request.
type tokenUserCtxKey struct{}

// WithToken stores the matched API token on ctx, and notes its id (never the
// secret) on the request's access-log holder, like WithUser does the account.
func WithToken(ctx context.Context, t *model.APIToken) context.Context {
	if t != nil {
		httpx.RequestLogFrom(ctx).NoteToken(t.ID)
	}
	return context.WithValue(ctx, tokenCtxKey{}, t)
}

// TokenFrom returns the API token from ctx, nil if the request was not
// authenticated by an API token (e.g. a cookie session).
func TokenFrom(ctx context.Context) *model.APIToken {
	v, _ := ctx.Value(tokenCtxKey{}).(*model.APIToken)
	return v
}

// WithTokenUser stores the resolved token username on ctx.
func WithTokenUser(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, tokenUserCtxKey{}, username)
}

// TokenUserFrom returns the identity a token-authenticated request acts under
// ("" for cookie sessions). Set by the token middlewares alongside WithToken.
func TokenUserFrom(ctx context.Context) string {
	v, _ := ctx.Value(tokenUserCtxKey{}).(string)
	return v
}

// MCPResourcePath is the MCP endpoint, below the base path: the one door an
// OAuth access token (model.TokenSourceMCPOAuth) opens.
const MCPResourcePath = "/api/ai/mcp"

// MCPOAuthTokenPrefix starts every OAuth access token handlers.MCPOAuth
// issues. A key minted on the API / MCP pages is plain hex and never starts
// with it.
const MCPOAuthTokenPrefix = "fxo_"

// MCPOAuthDoor is what the MCP door asks the OAuth authorization server
// (handlers.MCPOAuth) about a request.
type MCPOAuthDoor interface {
	// MCPResource is the OAuth protected resource the request arrived at -
	// "https://<the address>/api/ai/mcp" - and the URL of its metadata, for
	// the 401 challenge; both "" where OAuth is not offered (no HTTPS public
	// URL, an address no tenant has).
	MCPResource(r *http.Request) (resource, metadataURL string)
	// AdmitsOAuthOwner reports whether u may use a grant of the address r
	// arrived at: on a tenant's own address only that tenant's accounts (and
	// the platform's own).
	AdmitsOAuthOwner(r *http.Request, u *model.User) bool
}

// APITokenMiddleware authenticates a request strictly via an API token
// (X-Filex-Token or Authorization: Bearer), attaches both the bound user
// and the matched token to the context, and rejects anything else with 401.
//
// Unlike the generic Middleware chain it does NOT accept cookie sessions —
// the AI/MCP namespace is token-only by design. Use RequireScope after it
// to gate individual verbs. The token's owner is judged by admitTokenOwner,
// as on the hybrid door: a disabled account, or one whose tenant is
// suspended, is refused with 403.
//
// With an MCPOAuthDoor, the MCP endpoint is also an OAuth protected resource
// (RFC 9728): a 401 there names the resource's metadata, and an OAuth access
// token is looked up under a hash bound to the resource the request arrived
// at (HashMCPOAuthToken). A token issued for one address - another tenant's,
// or the platform's - matches nothing on this one.
func APITokenMiddleware(store db.Store, oauth ...MCPOAuthDoor) func(http.Handler) http.Handler {
	var door MCPOAuthDoor
	if len(oauth) > 0 {
		door = oauth[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource := ""
			var challenge *mcpChallengeWriter
			if door != nil && r.URL.Path == MCPResourcePath {
				var metadata string
				resource, metadata = door.MCPResource(r)
				if metadata != "" {
					challenge = &mcpChallengeWriter{ResponseWriter: w, metadata: metadata}
					w = challenge
				}
			}
			raw := extractAPIToken(r)
			if raw == "" {
				writeAuthErr(w, http.StatusUnauthorized, "missing api token")
				return
			}
			if challenge != nil {
				challenge.presented = true
			}
			ctx := r.Context()
			hash := hashAPIToken(raw)
			if resource != "" && strings.HasPrefix(raw, MCPOAuthTokenPrefix) {
				hash = HashMCPOAuthToken(resource, raw)
			}
			tok, err := store.GetAPITokenByHash(ctx, hash)
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "invalid api token")
				return
			}
			if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
				writeAuthErr(w, http.StatusUnauthorized, "token expired")
				return
			}
			user, err := store.GetUser(ctx, tok.UserID)
			if err != nil || user == nil {
				writeAuthErr(w, http.StatusUnauthorized, "token user not found")
				return
			}
			// An OAuth grant opens the MCP endpoint of the address it was issued
			// for, for an account that still belongs there; nothing else.
			if tok.Source == model.TokenSourceMCPOAuth && (resource == "" || !door.AdmitsOAuthOwner(r, user)) {
				writeAuthErr(w, http.StatusUnauthorized, "invalid api token")
				return
			}
			username, ok := admitTokenOwner(w, r, store, user, tok)
			if !ok {
				return
			}

			ctx = WithUser(ctx, user)
			ctx = WithToken(ctx, tok)
			ctx = WithTokenUser(ctx, username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MiddlewareWithToken authenticates EITHER via an API token (X-Filex-Token /
// Bearer) OR, failing that, the regular user-session driver chain. It powers
// the /api/files surface so a host app can proxy with a root-confined API
// token (see package confine) while the native admin panel keeps using its
// cookie/JWT session. An API-token bearer and a session JWT never collide:
// the token's sha256 simply won't match a JWT, so we fall through cleanly.
func MiddlewareWithToken(store db.Store, required bool) func(http.Handler) http.Handler {
	// The session chain with the store, so a suspended tenant's session is
	// refused as its keys are (AccountBlock reads the tenant).
	userChain := middlewareWith(required, store)
	return func(next http.Handler) http.Handler {
		fallthroughHandler := userChain(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw := extractAPIToken(r); raw != "" {
				ctx := r.Context()
				if tok, err := store.GetAPITokenByHash(ctx, hashAPIToken(raw)); err == nil && tok != nil {
					if tok.ExpiresAt == nil || tok.ExpiresAt.After(time.Now()) {
						if user, err := store.GetUser(ctx, tok.UserID); err == nil && user != nil {
							// A token that matched is this request's credential:
							// an owner it refuses is refused here, never handed
							// to the session chain below.
							username, ok := admitTokenOwner(w, r, store, user, tok)
							if !ok {
								return
							}
							ctx = WithUser(ctx, user)
							ctx = WithToken(ctx, tok)
							ctx = WithTokenUser(ctx, username)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
				}
			}
			// Not a valid API token — defer to cookie/JWT/proxy-header auth.
			fallthroughHandler.ServeHTTP(w, r)
		})
	}
}

// admitTokenOwner asks everything a door must know about the owner of a
// valid, unexpired API token before the request may act as them, for both
// token middlewares (APITokenMiddleware: /api/ai, MCP, /api/ai/admin and
// /api/sharex; MiddlewareWithToken: /api/files, /api/admin, the thumbnails and
// the sockets). On a refusal it has written the answer and reports false; on
// success it returns the identity the request acts under.
//
//   - the account is on and its tenant is not suspended: RefuseBlockedAccount,
//     the session chain's own answer (403 `account_disabled`, 403
//     `tenant_suspended`);
//   - it may use API tokens at all: access.api / access.desktop (package perm,
//     TokenMayBeUsed), 401;
//   - the X-Filex-Token-User it names is one the token allows, 403. Falling
//     back to the default identity, or to the session chain on the hybrid door,
//     would silently ignore what the caller asked for.
//
// ⚠⚠ One function on purpose. Each middleware used to carry its own copy of
// these steps and the copies drifted: MiddlewareWithToken refused a disabled
// owner, APITokenMiddleware never asked, so switching an account off left every
// token it had minted working on /api/ai and /api/sharex - file reads and
// writes, MCP, public links, and for an administrator's token the admin surface,
// which can switch the account back on (GHSA-58wc-42xh-89v8). A rule a token's
// owner must meet goes here, and holds at every door a token opens; the
// api-token driver's owner is judged by Middleware (RefuseBlockedAccount).
// A suspended tenant's keys kept working the same way: suspension was asked at
// sign-in only.
func admitTokenOwner(w http.ResponseWriter, r *http.Request, store db.Store, user *model.User, tok *model.APIToken) (string, bool) {
	if RefuseBlockedAccount(w, r, store, user) {
		return "", false
	}
	ctx := r.Context()
	mayUse := TokenMayBeUsed(ctx, store, user, tok)
	if tok.Source == model.TokenSourceMCPOAuth && r.URL.Path == MCPResourcePath {
		// Reuse the ordinary API-use permission without letting any other
		// driver accept this resource-bound token by id or by its secret.
		ordinary := *tok
		ordinary.Source = ""
		mayUse = TokenMayBeUsed(ctx, store, user, &ordinary)
	}
	if !mayUse {
		writeAuthErr(w, http.StatusUnauthorized, "this account may not use API tokens")
		return "", false
	}
	username, ok := tok.ResolveUsername(r.Header.Get(tokenUserHeaderName))
	if !ok {
		writeAuthErr(w, http.StatusForbidden, "unknown token username")
		return "", false
	}
	_ = store.TouchAPIToken(ctx, tok.ID)
	return username, true
}

// AnnotateToken attaches a valid API token to the context and NOTHING else.
// It never authenticates a user, never consults the session drivers, and — the
// whole point — never rejects: an unknown, revoked, expired or malformed
// token, a disabled owner, a bogus X-Filex-Token-User all simply leave the
// context untouched and the request proceeds as anonymous.
//
// It exists for PUBLIC routes that must merely describe the caller —
// /api/files/capabilities reports `caller_kind` so the explorer knows whether
// to draw a person's surfaces. Reaching for MiddlewareWithToken there was
// wrong in a way that only shows up later: that chain answers 403 to a
// disabled account, so a disabled user's browser (which still holds a cookie)
// would have had the LOGIN page's capabilities probe fail closed. A public
// probe that can fail is a worse trade than a capability field.
//
// ⚠ Do not add a rejection path here. If a route needs one it needs a
// different middleware; this one's contract is that a public page can always
// get an answer.
func AnnotateToken(store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractAPIToken(r)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			tok, err := store.GetAPITokenByHash(ctx, hashAPIToken(raw))
			if err != nil || tok == nil {
				next.ServeHTTP(w, r)
				return
			}
			if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
				next.ServeHTTP(w, r)
				return
			}
			// Deliberately no TouchAPIToken: describing yourself is not use,
			// and a public probe should not write to the database.
			next.ServeHTTP(w, r.WithContext(WithToken(ctx, tok)))
		})
	}
}

// RequireScope rejects requests whose token does not grant `scope`. A token
// grants only what its list names; an empty list grants nothing. Must run
// after APITokenMiddleware.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := TokenFrom(r.Context())
			if tok == nil || !tok.HasScope(scope) {
				writeAuthErr(w, http.StatusForbidden, "token missing scope: "+scope)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractAPIToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(apiTokenHeaderName)); v != "" {
		return v
	}
	const bearer = "Bearer "
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearer) {
		return strings.TrimSpace(h[len(bearer):])
	}
	return ""
}

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HashMCPOAuthToken is the stored hash of an OAuth access token issued for
// resource. It is domain-separated from a general API key's (hashAPIToken),
// so only the MCP endpoint, which asks for it, can find the token: the file
// protocols and the hybrid API/session doors look a secret up under the
// plain hash and never resolve this one as a broader key. And it is bound to
// the resource - the address the token was issued for - so the same secret
// presented at another tenant's address, or at the platform's, matches no
// row there.
func HashMCPOAuthToken(resource, raw string) string {
	return hashAPIToken("filex-mcp-oauth\x00" + resource + "\x00" + raw)
}

// mcpChallengeWriter adds the OAuth challenge (RFC 6750 section 3, RFC 9728
// section 5.1) to a 401 from the MCP endpoint, so a client that has no
// credential, or one that no longer works, finds the authorization server.
type mcpChallengeWriter struct {
	http.ResponseWriter
	metadata string
	// presented: the request carried a credential, so the 401 says it is
	// invalid (the client refreshes or authorizes again).
	presented bool
}

func (w *mcpChallengeWriter) WriteHeader(status int) {
	if status == http.StatusUnauthorized {
		v := `Bearer resource_metadata="` + w.metadata + `", scope="read mcp"`
		if w.presented {
			v += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", v)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *mcpChallengeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *mcpChallengeWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func writeAuthErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
