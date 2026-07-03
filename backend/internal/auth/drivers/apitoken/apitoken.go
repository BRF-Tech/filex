// Package apitoken implements bearer-token authentication for
// non-interactive callers: AI agents, the work.example.com FilexClient, and the
// embedded MCP server.
//
// A token is a 64-char hex string handed out once at create time
// (POST /api/admin/ai-tokens). Only its sha256 hash is stored in the
// api_tokens table. Every token is bound to a user, so a request
// authenticated by this driver inherits that user's role and flows through
// the same auth.Middleware / RequireAdmin checks as a cookie session.
//
// Credentials are read from either header:
//
//	X-Filex-Token: <token>
//	Authorization: Bearer <token>
//
// The matched *model.APIToken is attached to the request context so
// downstream handlers can enforce per-token scopes (see auth.TokenFrom).
package apitoken

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func init() {
	auth.Register("api-token", func() auth.Driver { return &Driver{} })
}

// HeaderName is the dedicated header AI/MCP callers send.
const HeaderName = "X-Filex-Token"

// bearerPrefix detects `Authorization: Bearer <token>`.
const bearerPrefix = "Bearer "

// Issuable token scopes. A token with an empty Scopes field grants every
// scope (full access for the bound user's role). RequireScope gates each
// verb against these; the admin token-issuer rejects anything not in the
// set so an operator can't mint a token carrying a typo'd scope that
// silently grants nothing.
//
//	read   — list / info / download / search (REST) — read-only file ops
//	write  — upload / mkdir / move (REST)            — file mutations
//	delete — delete (REST)                            — soft-delete files
//	mcp    — the streamable-HTTP MCP server at /api/ai/mcp
//	admin  — the full admin surface at /api/ai/admin/* and the admin_*
//	         MCP tools (users, storages, settings, replica, queue, …)
const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeDelete = "delete"
	ScopeMCP    = "mcp"
	ScopeAdmin  = "admin"
)

// ScopeRootPrefix marks a path-confinement scope: `root:<adapter>://<rel>`.
// Unlike the verb scopes it carries a value and is enforced by package confine
// as a hard path ceiling — across both /api/files and the AI surface. A token
// may combine verb scopes with at most one root scope.
const ScopeRootPrefix = "root:"

// ValidScopes is the canonical, ordered set of issuable VERB scopes. The
// `root:` confinement scope is validated separately (see IsValidScope).
var ValidScopes = []string{ScopeRead, ScopeWrite, ScopeDelete, ScopeMCP, ScopeAdmin}

// IsValidScope reports whether s is a known issuable scope — a verb scope or a
// well-formed `root:<adapter>://<rel>` confinement scope.
func IsValidScope(s string) bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeDelete, ScopeMCP, ScopeAdmin:
		return true
	}
	if strings.HasPrefix(s, ScopeRootPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(s, ScopeRootPrefix)) != ""
	}
	return false
}

// Driver is the API-token auth driver.
type Driver struct {
	store db.Store
}

// New constructs a driver bound to a store. Init must still be called.
func New(store db.Store) *Driver { return &Driver{store: store} }

// Name implements auth.Driver.
func (d *Driver) Name() string { return "api-token" }

// Init validates the driver has a store.
func (d *Driver) Init(_ context.Context, _ map[string]any) error {
	if d.store == nil {
		return errors.New("apitoken: nil store")
	}
	return nil
}

// Capabilities — token auth is headless; no interactive sign-in surface.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{}
}

// Authenticate resolves a bearer/X-Filex-Token credential to its bound
// user. Returns auth.ErrUnauthorized (so the middleware falls through to
// the next driver) when no credential is present or it doesn't validate.
func (d *Driver) Authenticate(r *http.Request) (*model.User, error) {
	raw := ExtractToken(r)
	if raw == "" {
		return nil, auth.ErrUnauthorized
	}
	ctx := r.Context()
	tok, err := d.store.GetAPITokenByHash(ctx, HashToken(raw))
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		return nil, auth.ErrUnauthorized
	}
	user, err := d.store.GetUser(ctx, tok.UserID)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	// Best-effort usage stamp — never fail the request on a write error.
	_ = d.store.TouchAPIToken(ctx, tok.ID)
	// This driver proves identity for the shared auth.Middleware chain, so
	// existing /api/files routes also accept AI tokens. Per-token scope
	// enforcement lives in the dedicated AI middleware
	// (auth.APITokenMiddleware), which additionally attaches the matched
	// token to the request context.
	return user, nil
}

// ExtractToken pulls the raw token from the X-Filex-Token header first,
// then falls back to `Authorization: Bearer <token>`.
func ExtractToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(HeaderName)); v != "" {
		return v
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearerPrefix) {
		return strings.TrimSpace(h[len(bearerPrefix):])
	}
	return ""
}

// HashToken returns the sha256 hex digest stored in api_tokens.token_hash.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
