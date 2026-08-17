// Package handlers — s3keys.go
//
// Self-service management of S3 access keys (migration 00026).
//
//	GET    /api/auth/s3-keys            — list the caller's own keys
//	POST   /api/auth/s3-keys            — mint one (the secret is shown ONCE)
//	POST   /api/auth/s3-keys/{id}/state — enable / disable without deleting
//	DELETE /api/auth/s3-keys/{id}       — revoke permanently
//
// Self-service and not admin-only on purpose: the requirement is that every
// credential the product issues can connect at exactly its own permission, and
// a key that only an administrator can mint is a key most people never get. The
// permission ceiling is enforced by protocolauth.Issue, not by who is asking —
// a key can only ever narrow what its owner already has.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
)

// S3Keys is the self-service access-key handler.
type S3Keys struct {
	Store db.Store
	Auth  *protocolauth.Resolver
	// Endpoint is the S3 URL to hand back with a freshly minted key, so the
	// UI can render a working `aws configure` / rclone block instead of a
	// placeholder the user has to fill in. Empty = the UI falls back to the
	// page origin.
	//
	// ⚠ It is the ENDPOINT, not the application URL: with a dedicated host
	// they differ, and a client pointed at the application root reaches the web
	// app. See s3api.EndpointURL.
	Endpoint string
	// Enabled mirrors the FILEX_S3 kill switch, so the UI can say "your
	// operator has turned this off" instead of handing out a key for an
	// endpoint that answers 404.
	Enabled bool
	// PathStyle is true when clients must be told to force path-style
	// addressing (no dedicated domain, so `bucket.host` does not resolve).
	PathStyle bool
}

// NewS3Keys constructs the handler.
func NewS3Keys(store db.Store, res *protocolauth.Resolver, cfg config.S3Config, publicURL string) *S3Keys {
	return &S3Keys{
		Store:     store,
		Auth:      res,
		Endpoint:  s3api.EndpointURL(publicURL, cfg.Domain),
		Enabled:   cfg.Enabled,
		PathStyle: s3api.PathStyleRequired(cfg.Domain),
	}
}

// connection is the deployment half of every answer: where to point a client
// and how to address it. It travels with the keys because a key without an
// endpoint is not a usable credential.
func (h *S3Keys) connection() map[string]any {
	return map[string]any{
		"endpoint":   h.Endpoint,
		"enabled":    h.Enabled,
		"path_style": h.PathStyle,
	}
}

type s3KeyReq struct {
	Label string `json:"label"`
	// APITokenID mints the key FROM an API token, so it inherits that token's
	// scopes, confinement and expiry. Absent = minted from the account.
	APITokenID *int64 `json:"api_token_id,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	// ExpiresAt is RFC3339. Clamped down to the parent token's expiry.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// List returns the caller's own keys. The secret is not among them and cannot
// be: only its sealed form is stored, and `json:"-"` keeps even that off the
// wire.
func (h *S3Keys) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	keys, err := h.Store.ListS3AccessKeys(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := h.connection()
	out["keys"] = keys
	writeJSON(w, http.StatusOK, out)
}

// Create mints a key and returns the secret exactly once.
func (h *S3Keys) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	if h.Auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "access keys are not available on this install"})
		return
	}
	var req s3KeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	var tok *model.APIToken
	if req.APITokenID != nil {
		t, err := h.Store.GetAPITokenByID(r.Context(), *req.APITokenID)
		// ⚠ The token must belong to the CALLER. Without this check, anyone
		// could mint a key against someone else's token and inherit their
		// permissions — the exact opposite of what inheritance is for.
		if err != nil || t == nil || t.UserID != u.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such API token"})
			return
		}
		tok = t
	}

	issued, err := h.Auth.Issue(r.Context(), protocolauth.IssueRequest{
		User: u, Token: tok, Label: req.Label,
		Bucket: req.Bucket, Prefix: req.Prefix, ExpiresAt: req.ExpiresAt,
	})
	switch {
	case errors.Is(err, protocolauth.ErrNoSecretBox):
		// 503, not 500: the install is missing configuration, and the message
		// says which — an operator reading a generic 500 would go looking for
		// a bug that is not there.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, protocolauth.ErrWidensParent):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := h.connection()
	out["key"] = issued.Key
	// The one and only time this leaves the server.
	out["secret"] = issued.Secret
	writeJSON(w, http.StatusCreated, out)
}

type s3KeyStateReq struct {
	Disabled bool `json:"disabled"`
}

// SetState disables or re-enables a key without deleting it, so a leaked
// credential can be stopped while still being visible in the audit trail.
func (h *S3Keys) SetState(w http.ResponseWriter, r *http.Request) {
	u, k, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	var req s3KeyStateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Store.SetS3AccessKeyDisabled(r.Context(), k.ID, req.Disabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.Disabled {
		protocolauth.KickCredential(h.Auth, protocolauth.KickAccessKey, k.ID)
	}
	updated, _ := h.Store.GetS3AccessKeyByID(r.Context(), k.ID)
	_ = u
	writeJSON(w, http.StatusOK, map[string]any{"key": updated})
}

// Delete revokes a key permanently.
func (h *S3Keys) Delete(w http.ResponseWriter, r *http.Request) {
	u, k, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	// Scoped to the owner in the query too, not just in the check above: the
	// store method takes the user id so a handler that ever forgets the check
	// still cannot delete another account's key.
	if err := h.Store.DeleteS3AccessKey(r.Context(), k.ID, u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	protocolauth.KickCredential(h.Auth, protocolauth.KickAccessKey, k.ID)
	w.WriteHeader(http.StatusNoContent)
}

// ownedKey resolves {id} and confirms the caller owns it.
//
// ⚠ It answers 404 — not 403 — for a key belonging to someone else. Telling a
// caller "that key exists but is not yours" turns the endpoint into a way to
// enumerate other accounts' credentials.
func (h *S3Keys) ownedKey(w http.ResponseWriter, r *http.Request) (*model.User, *model.S3AccessKey, bool) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return nil, nil, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil, nil, false
	}
	k, err := h.Store.GetS3AccessKeyByID(r.Context(), id)
	if err != nil || k == nil || k.UserID != u.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such access key"})
		return nil, nil, false
	}
	return u, k, true
}
