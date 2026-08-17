// Package handlers — nfsexports.go
//
//	GET    /api/auth/nfs-exports            — list the caller's own exports
//	POST   /api/auth/nfs-exports            — mint one (the PATH is shown ONCE)
//	POST   /api/auth/nfs-exports/{id}/state — enable / disable
//	DELETE /api/auth/nfs-exports/{id}       — revoke permanently
//
// ⚠⚠ The path returned by POST is the whole credential. Anyone who can reach
// the NFS port and knows it can mount the export as this account — NFSv3 has no
// other way to bind an identity that does not require Kerberos. It is shown
// once, stored hashed, and the UI says all of this plainly rather than letting
// somebody discover it from a mount table.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// NFSExports is the self-service export handler.
type NFSExports struct {
	Store db.Store
	Auth  *protocolauth.Resolver
	// Enabled mirrors the FILEX_NFS kill switch.
	Enabled bool
	// Host and Port are what a mount line needs.
	Host string
	Port int
}

// NewNFSExports constructs the handler.
func NewNFSExports(store db.Store, res *protocolauth.Resolver, enabled bool, host string, port int) *NFSExports {
	return &NFSExports{Store: store, Auth: res, Enabled: enabled, Host: host, Port: port}
}

func (h *NFSExports) connection() map[string]any {
	return map[string]any{
		"enabled": h.Enabled,
		"host":    h.Host,
		"port":    h.Port,
	}
}

// List returns the caller's own exports. The paths are NOT among them and
// cannot be: only their hashes are stored.
func (h *NFSExports) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	list, err := h.Store.ListNFSExports(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := h.connection()
	out["exports"] = list
	writeJSON(w, http.StatusOK, out)
}

type nfsExportReq struct {
	Label      string     `json:"label"`
	APITokenID *int64     `json:"api_token_id,omitempty"`
	Storage    string     `json:"storage,omitempty"`
	Prefix     string     `json:"prefix,omitempty"`
	ReadOnly   bool       `json:"read_only,omitempty"`
	AllowCIDRs string     `json:"allow_cidrs,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// Create mints an export and returns its path exactly once.
func (h *NFSExports) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req nfsExportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	var tok *model.APIToken
	if req.APITokenID != nil {
		t, err := h.Store.GetAPITokenByID(r.Context(), *req.APITokenID)
		// ⚠ The token must belong to the CALLER — otherwise anyone could mint
		// an export against somebody else's token and inherit their access.
		if err != nil || t == nil || t.UserID != u.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such API token"})
			return
		}
		tok = t
	}

	issued, err := h.Auth.IssueExport(r.Context(), protocolauth.IssueExportRequest{
		User: u, Token: tok, Label: req.Label,
		Storage: req.Storage, Prefix: req.Prefix,
		ReadOnly: req.ReadOnly, AllowCIDRs: req.AllowCIDRs,
		ExpiresAt: req.ExpiresAt,
	})
	switch {
	case errors.Is(err, protocolauth.ErrWidensParent):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	out := h.connection()
	out["export"] = issued.Export
	// The one and only time this leaves the server.
	out["path"] = issued.Path
	writeJSON(w, http.StatusCreated, out)
}

// SetState disables or re-enables an export.
func (h *NFSExports) SetState(w http.ResponseWriter, r *http.Request) {
	_, e, ok := h.owned(w, r)
	if !ok {
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Store.SetNFSExportDisabled(r.Context(), e.ID, req.Disabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.Disabled {
		protocolauth.KickCredential(h.Auth, protocolauth.KickExport, e.ID)
	}
	updated, _ := h.Store.GetNFSExportByID(r.Context(), e.ID)
	writeJSON(w, http.StatusOK, map[string]any{"export": updated})
}

// Delete revokes an export.
//
// ⚠ It takes effect on the mount's next request rather than by tearing the
// mount down: NFS has no session to end — the client holds a file handle and
// keeps sending RPCs, and there is no connection the server owns. So the live
// mount is MARKED revoked (protocolauth.Kick) and every operation on it starts
// refusing from that moment. The client sees an access error and its mount goes
// stale, which is the honest end state and worth saying in the UI.
func (h *NFSExports) Delete(w http.ResponseWriter, r *http.Request) {
	u, e, ok := h.owned(w, r)
	if !ok {
		return
	}
	if err := h.Store.DeleteNFSExport(r.Context(), e.ID, u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	protocolauth.KickCredential(h.Auth, protocolauth.KickExport, e.ID)
	w.WriteHeader(http.StatusNoContent)
}

// owned resolves {id} and confirms the caller owns it — 404 for somebody
// else's, like every other credential surface here.
func (h *NFSExports) owned(w http.ResponseWriter, r *http.Request) (*model.User, *model.NFSExport, bool) {
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
	e, err := h.Store.GetNFSExportByID(r.Context(), id)
	if err != nil || e == nil || e.UserID != u.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such export"})
		return nil, nil, false
	}
	return u, e, true
}
