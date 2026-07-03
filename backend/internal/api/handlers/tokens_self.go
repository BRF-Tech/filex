package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// SelfTokens is the self-service API-token surface at /api/tokens. Any
// authenticated user (including non-admin user/viewer accounts) may mint
// tokens bound to THEMSELVES — capped so a token can never exceed the creator's
// account-role ceiling nor their own file grants. Admins get the richer
// /api/admin/ai-tokens surface; this is the safe capped subset for everyone.
type SelfTokens struct {
	store db.Store
	acl   *acl.Resolver
}

// NewSelfTokens constructs the self-service token handler.
func NewSelfTokens(store db.Store, resolver *acl.Resolver) *SelfTokens {
	return &SelfTokens{store: store, acl: resolver}
}

// List returns the caller's own tokens (never any secret).
func (h *SelfTokens) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tokens, err := h.store.ListAPITokensByUser(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

type selfTokenCreateReq struct {
	Label         string `json:"label"`
	Scopes        string `json:"scopes,omitempty"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

// Create mints a token bound to the caller with capped scopes.
func (h *SelfTokens) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req selfTokenCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	scopes, err := h.cappedScopes(r.Context(), u, req.Scopes)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	plain, gerr := generateToken()
	if gerr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
		return
	}
	row := &model.APIToken{
		UserID:    u.ID, // force self — ignore any client-supplied id
		Label:     strings.TrimSpace(req.Label),
		TokenHash: apitoken.HashToken(plain),
		Scopes:    scopes,
	}
	if req.ExpiresInDays > 0 {
		exp := time.Now().AddDate(0, 0, req.ExpiresInDays)
		row.ExpiresAt = &exp
	}
	created, cerr := h.store.CreateAPIToken(r.Context(), row)
	if cerr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": cerr.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": plain, // shown ONCE
		"row":   created,
	})
}

// Delete revokes one of the caller's own tokens (ownership enforced).
func (h *SelfTokens) Delete(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// Ownership: the token must belong to the caller.
	mine, err := h.store.ListAPITokensByUser(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	owned := false
	for _, t := range mine {
		if t.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	if err := h.store.DeleteAPIToken(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// cappedScopes validates the requested scopes against the caller's ceiling and
// returns the final, explicit scope string to persist.
//
// Hard rules (defense against privilege escalation):
//   - `admin` scope is NEVER allowed here.
//   - EMPTY scopes are NEVER stored (an empty Scopes means "all" and would let
//     RequireScope("admin") pass) — we fill a role-appropriate safe default.
//   - `write`/`delete` are rejected for viewer accounts (read-only).
//   - each `root:<adapter>://<rel>` scope must be within the caller's own
//     grants (≥viewer, or ≥editor when the token also carries write/delete).
func (h *SelfTokens) cappedScopes(ctx context.Context, u *model.User, raw string) (string, error) {
	var verbs, roots []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, apitoken.ScopeRootPrefix) {
			if strings.TrimSpace(strings.TrimPrefix(p, apitoken.ScopeRootPrefix)) == "" {
				return "", errors.New("malformed root scope")
			}
			roots = append(roots, p)
			continue
		}
		switch p {
		case apitoken.ScopeAdmin:
			return "", errors.New("admin scope is not allowed for self-service tokens")
		case apitoken.ScopeWrite, apitoken.ScopeDelete:
			if u.IsViewer() {
				return "", errors.New("viewer accounts can only mint read-only tokens")
			}
			verbs = append(verbs, p)
		case apitoken.ScopeRead, apitoken.ScopeMCP:
			verbs = append(verbs, p)
		default:
			return "", errors.New("unknown scope: " + p)
		}
	}
	// Never persist empty verb scopes (empty == all == includes admin).
	if len(verbs) == 0 {
		if u.IsViewer() {
			verbs = []string{apitoken.ScopeRead, apitoken.ScopeMCP}
		} else {
			verbs = []string{apitoken.ScopeRead, apitoken.ScopeWrite, apitoken.ScopeDelete, apitoken.ScopeMCP}
		}
	}
	// A root scope may not exceed the caller's own effective access there.
	need := acl.LevelViewer
	for _, v := range verbs {
		if v == apitoken.ScopeWrite || v == apitoken.ScopeDelete {
			need = acl.LevelEditor
		}
	}
	for _, rs := range roots {
		val := strings.TrimPrefix(rs, apitoken.ScopeRootPrefix)
		adapter, rel := splitAdapterPath(val)
		if adapter == "" {
			return "", errors.New("root scope must be <adapter>://<path>")
		}
		if h.acl != nil && !u.IsAdmin() {
			st, err := h.store.GetStorageByName(ctx, adapter)
			if err != nil || st == nil {
				return "", errors.New("root scope names an unknown storage")
			}
			set, err := h.acl.LoadSet(ctx, u, st)
			if err != nil || set == nil || set.Effective(acl.CleanRel(rel)) < need {
				return "", errors.New("root scope exceeds your own access to " + val)
			}
		}
	}
	return strings.Join(append(verbs, roots...), ","), nil
}
