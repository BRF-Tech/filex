package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tokenperm"
)

// AITokens is the admin handler for issuing / listing / revoking API tokens
// used by AI agents, the MCP server, and the work.example.com FilexClient.
//
// Routes (admin-only):
//
//	GET    /api/admin/ai-tokens          → {tokens:[…]}      (no secret)
//	POST   /api/admin/ai-tokens          → {token:"<plain>", row:{…}}  (plaintext ONCE)
//	DELETE /api/admin/ai-tokens/{id}     → {ok:true}
type AITokens struct {
	store db.Store
	// auth is the shared credential resolver, held only so revoking a token
	// reaches the SFTP/FTPS sessions that token already opened.
	auth *protocolauth.Resolver
}

// NewAITokens constructs the admin token handler.
func NewAITokens(store db.Store, pauth *protocolauth.Resolver) *AITokens {
	return &AITokens{store: store, auth: pauth}
}

// List returns every token row WITHOUT the secret (only the hash is stored
// and even that is omitted via the model's json:"-" tag).
func (h *AITokens) List(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.store.ListAPITokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// A tenant admin sees only the tokens belonging to its own users. The
	// rows carry no secret — only the sha256 hash is stored — but the
	// inventory is still a map of another customer's integrations, and it is
	// the input to the Update/Delete crossings below.
	if scope, confined := confinedScope(r.Context()); confined {
		kept := tokens[:0]
		for _, t := range tokens {
			if u, uerr := h.store.GetUser(r.Context(), t.UserID); uerr == nil && u != nil &&
				u.ProviderID != nil && *u.ProviderID == scope.ProviderID {
				kept = append(kept, t)
			}
		}
		tokens = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokenList(tokens)})
}

// tokenList is the `tokens` field of a list answer: [] when there are none,
// never null. The store hands back a nil slice for no rows, which encodes as
// `"tokens": null`, and a client reading the list (`tokens.map(...)`, the
// Cypress token-kinds spec) failed on an instance that simply had no tokens.
//
// Each row carries `permissions` - the level of every permission of package
// tokenperm it holds (model.APIToken.WithPermissions).
func tokenList(tokens []*model.APIToken) []*model.APIToken {
	if tokens == nil {
		return []*model.APIToken{}
	}
	for _, t := range tokens {
		t.WithPermissions()
	}
	return tokens
}

// createTokenBody is the POST body. user_id defaults to the calling admin;
// scopes is a comma-separated allow-list and must name at least one verb —
// an empty one is refused, never "all" (apitoken.ParseIssued); `admin` is
// granted only when it is in the list. expires_in_days,
// when > 0, sets an expiry. usernames is the identity allow-list the caller
// may act under per request (first = default; empty → the label).
type createTokenBody struct {
	Label     string   `json:"label"`
	UserID    int64    `json:"user_id,omitempty"`
	Scopes    string   `json:"scopes,omitempty"`
	Usernames []string `json:"usernames,omitempty"`
	// Kind defaults to "app" on this surface — this is where a host app's
	// proxy, a bot or an MCP client gets its credential, and none of those is
	// a person. An admin minting a personal token for somebody passes "user"
	// explicitly (or the person mints it themselves at /api/tokens).
	Kind          string `json:"kind,omitempty"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

// Create issues a new token. The plaintext value is returned ONCE — only its
// sha256 hash is persisted.
func (h *AITokens) Create(w http.ResponseWriter, r *http.Request) {
	var body createTokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	userID := body.UserID
	if userID == 0 {
		if u := auth.UserFrom(r.Context()); u != nil {
			userID = u.ID
		}
	}
	if userID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	// ⚠⚠ The token is bound to `user_id`, and a token authenticates AS that
	// user — with that user's tenant scope. So an unchecked `user_id` here is
	// not "an admin managing somebody else's token", it is identity takeover:
	// a tenant admin mints a credential that reads and writes every storage of
	// the tenant they named. This is the same class that was found and fixed
	// on POST /api/admin/users (handlers/users.go, a production report, 2026-08-05) — the user
	// surface got the check and the token surface beside it did not, which is
	// the worse half, because the token needs no password and no login.
	//
	// 404 rather than 403, and before the existence check, so a foreign
	// user_id is indistinguishable from one that does not exist.
	if !ownsUser(w, r, h.store, userID, "user") {
		return
	}
	if _, err := h.store.GetUser(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown user_id"})
		return
	}

	scopes, serr := normalizeScopes(body.Scopes)
	if serr != nil {
		writeScopeRefusal(w, r, serr)
		return
	}
	usernames, uerr := normalizeUsernames(body.Usernames)
	if uerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": uerr.Error()})
		return
	}

	plain, err := generateToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	kind, kerr := normalizeTokenKind(body.Kind, model.TokenKindApp)
	if kerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": kerr.Error()})
		return
	}

	row := &model.APIToken{
		UserID:    userID,
		Label:     strings.TrimSpace(body.Label),
		TokenHash: apitoken.HashToken(plain),
		Scopes:    scopes,
		Usernames: usernames,
		Kind:      kind,
	}
	if body.ExpiresInDays > 0 {
		exp := time.Now().AddDate(0, 0, body.ExpiresInDays)
		row.ExpiresAt = &exp
	}

	created, err := h.store.CreateAPIToken(r.Context(), row)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": plain, // shown ONCE
		"row":   created.WithPermissions(),
	})
}

// updateTokenBody is the PATCH body — display metadata, and the levels of the
// permissions of package tokenperm; the credential, its verbs and its `root:`
// are immutable. A nil field is left unchanged; usernames: [] clears the list
// (back to label-only).
type updateTokenBody struct {
	Label     *string   `json:"label,omitempty"`
	Usernames *[]string `json:"usernames,omitempty"`
	// Kind — "user" or "app". The escape hatch for migration 00030, which
	// defaulted every token that existed before the split to "app": a personal
	// token that predates it becomes a person's again with one PATCH.
	// Admin-only on purpose; see SelfTokens.Update.
	Kind *string `json:"kind,omitempty"`
	// Permissions sets levels - {"comments": "rw"} lets the token add and
	// delete comments, {"comments": "read"} takes that back (task #157). A
	// permission it does not name keeps its level.
	Permissions map[string]string `json:"permissions,omitempty"`
}

// parseLevelChanges reads a PATCH body's `permissions`: every key a
// permission of package tokenperm, every level one it has (400 otherwise, in
// words - an integration's typo must not pass for "no change").
func parseLevelChanges(raw map[string]string) (map[string]tokenperm.Level, error) {
	out := make(map[string]tokenperm.Level, len(raw))
	for key, val := range raw {
		d, ok := tokenperm.Lookup(key)
		if !ok {
			return nil, fmt.Errorf("unknown permission %q", key)
		}
		l, ok := tokenperm.ParseLevel(val)
		if ok {
			ok = false
			for _, x := range d.Levels {
				if x == l {
					ok = true
				}
			}
		}
		if !ok {
			return nil, &tokenperm.LevelError{Key: key, Level: val}
		}
		out[key] = l
	}
	return out, nil
}

// setTokenLevels writes changes into token id's list (tokenperm.Replace: the
// verbs and the `root:` as they were). Nothing is written when nothing
// changes - a live SFTP or FTPS session on the token is ended by a changed
// list (protocolauth), and an unchanged one must not end it.
func setTokenLevels(r *http.Request, store db.Store, id int64, changes map[string]tokenperm.Level) error {
	if len(changes) == 0 {
		return nil
	}
	tok, err := store.GetAPITokenByID(r.Context(), id)
	if err != nil || tok == nil {
		return fmt.Errorf("token not found")
	}
	next := tokenperm.Replace(tok.Scopes, changes)
	if next == tok.Scopes {
		return nil
	}
	return store.UpdateAPITokenScopes(r.Context(), id, next)
}

// Update edits a token's label / username allow-list / kind and the levels
// of its permissions (`permissions`, package tokenperm).
//
//	PATCH /api/admin/ai-tokens/{id}
func (h *AITokens) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !h.ownsToken(w, r, id) {
		return
	}
	var body updateTokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var label, usernames, kind *string
	if body.Label != nil {
		l := strings.TrimSpace(*body.Label)
		label = &l
	}
	if body.Usernames != nil {
		u, uerr := normalizeUsernames(*body.Usernames)
		if uerr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": uerr.Error()})
			return
		}
		usernames = &u
	}
	if body.Kind != nil {
		k, kerr := normalizeTokenKind(*body.Kind, "")
		if kerr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": kerr.Error()})
			return
		}
		kind = &k
	}
	levels, lerr := parseLevelChanges(body.Permissions)
	if lerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": lerr.Error()})
		return
	}
	if err := h.store.UpdateAPITokenMeta(r.Context(), id, label, usernames, kind); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := setTokenLevels(r, h.store, id, levels); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete revokes a token by id.
func (h *AITokens) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !h.ownsToken(w, r, id) {
		return
	}
	if err := h.store.DeleteAPIToken(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// See the note on the self-service delete: the row is only half of it.
	protocolauth.KickCredential(h.auth, protocolauth.KickToken, id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// generateToken returns a 64-char hex secret (32 random bytes).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// usernameRe is the slug alphabet for token usernames: they travel in HTTP
// headers and land in audit/presence, so keep them plain ASCII identifiers.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`)

// normalizeUsernames validates + dedupes the identity allow-list (order kept —
// the FIRST entry is the default). Returns the comma-joined storage form.
func normalizeUsernames(raw []string) (string, error) {
	if len(raw) > 16 {
		return "", fmt.Errorf("too many usernames (max 16)")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, u := range raw {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if !usernameRe.MatchString(u) {
			return "", fmt.Errorf("invalid username %q (allowed: letters, digits, . _ -; max 32 chars)", u)
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return strings.Join(out, ","), nil
}

// normalizeTokenKind validates the requested token kind, falling back to
// `def` when the caller said nothing (pass "" for def to require a value).
//
// ⚠ An unknown value is REJECTED rather than folded into "app". Silently
// downgrading a typo'd "users" to an app token would mint a credential whose
// owner cannot manage their own keys and give them no error to read; the model
// normalizes unknown values to "app" only where it is reading rows nobody can
// fix any more (pre-00030 data).
func normalizeTokenKind(raw, def string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if def == "" {
			return "", fmt.Errorf("kind is required (%q or %q)", model.TokenKindUser, model.TokenKindApp)
		}
		return def, nil
	}
	switch raw {
	case model.TokenKindUser, model.TokenKindApp:
		return raw, nil
	}
	return "", fmt.Errorf("unknown token kind %q (valid: %s, %s)", raw, model.TokenKindUser, model.TokenKindApp)
}

// normalizeScopes is the admin door's form of the one issuance rule
// (apitoken.ParseIssued): it trims whitespace around each comma-separated
// scope, drops empties and duplicates, rejects any scope outside the canonical
// allow-list, and REFUSES a list that names no verb — empty, blank or a `root:`
// on its own (apitoken.ErrScopesRequired → 400 `scopes_required`). There is no
// "empty means all" any more: that was the rule before v0.43.0, and a row that
// is empty anyway grants nothing (model.APIToken.HasScope).
// token_scopes_doors_test.go holds both halves.
func normalizeScopes(raw string) (string, error) {
	verbs, roots, perms, err := apitoken.ParseIssued(raw)
	if err != nil {
		return "", err
	}
	return apitoken.JoinScopes(verbs, roots, perms), nil
}

// writeScopeRefusal answers a refused scope list in the reader's language:
// every door that mints a token says it the same way.
func writeScopeRefusal(w http.ResponseWriter, r *http.Request, err error) {
	lang := langOf(r)
	var unknown *apitoken.UnknownScopeError
	switch {
	case errors.Is(err, apitoken.ErrScopesRequired):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scopes_required",
			"message": srvtext.Text(lang, "server.token.scopes_required", nil)})
	case errors.As(err, &unknown):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope_unknown",
			"message": srvtext.Text(lang, "server.token.scope_unknown", srvtext.Vars{"scope": unknown.Scope})})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// ownsToken resolves a token to its bound user and asks whether that user is
// in the caller's tenant. A token id is a small integer, so without this a
// tenant admin could relabel — or revoke — every integration on the platform.
func (h *AITokens) ownsToken(w http.ResponseWriter, r *http.Request, id int64) bool {
	// ⚠ Unconditional lookup, for the same reason as SharesAdmin.ownsShare:
	// update and delete answered {"ok":true} for an id that names nothing, so
	// a confined-only 404 would have been an existence oracle.
	t, err := h.store.GetAPITokenByID(r.Context(), id)
	if err != nil || t == nil {
		return notFound(w, "token")
	}
	return ownsUser(w, r, h.store, t.UserID, "token")
}
