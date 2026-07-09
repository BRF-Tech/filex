package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
}

// NewAITokens constructs the admin token handler.
func NewAITokens(store db.Store) *AITokens {
	return &AITokens{store: store}
}

// List returns every token row WITHOUT the secret (only the hash is stored
// and even that is omitted via the model's json:"-" tag).
func (h *AITokens) List(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.store.ListAPITokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// createTokenBody is the POST body. user_id defaults to the calling admin;
// scopes is a comma-separated allow-list (empty == all). expires_in_days,
// when > 0, sets an expiry. usernames is the identity allow-list the caller
// may act under per request (first = default; empty → the label).
type createTokenBody struct {
	Label         string   `json:"label"`
	UserID        int64    `json:"user_id,omitempty"`
	Scopes        string   `json:"scopes,omitempty"`
	Usernames     []string `json:"usernames,omitempty"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
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
	if _, err := h.store.GetUser(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown user_id"})
		return
	}

	scopes, serr := normalizeScopes(body.Scopes)
	if serr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": serr.Error()})
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

	row := &model.APIToken{
		UserID:    userID,
		Label:     strings.TrimSpace(body.Label),
		TokenHash: apitoken.HashToken(plain),
		Scopes:    scopes,
		Usernames: usernames,
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
		"row":   created,
	})
}

// updateTokenBody is the PATCH body — display metadata only (the credential
// is immutable). A nil field is left unchanged; usernames: [] clears the list
// (back to label-only).
type updateTokenBody struct {
	Label     *string   `json:"label,omitempty"`
	Usernames *[]string `json:"usernames,omitempty"`
}

// Update edits a token's label / username allow-list.
//
//	PATCH /api/admin/ai-tokens/{id}
func (h *AITokens) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body updateTokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var label, usernames *string
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
	if err := h.store.UpdateAPITokenMeta(r.Context(), id, label, usernames); err != nil {
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
	if err := h.store.DeleteAPIToken(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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

// normalizeScopes trims whitespace around each comma-separated scope, drops
// empties, and rejects any scope outside the canonical apitoken.ValidScopes
// allow-list. An empty/blank input stays empty (== all scopes).
func normalizeScopes(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !apitoken.IsValidScope(p) {
			return "", fmt.Errorf("unknown scope %q (valid: %s)", p, strings.Join(apitoken.ValidScopes, ", "))
		}
		out = append(out, p)
	}
	return strings.Join(out, ","), nil
}
