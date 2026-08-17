// Package handlers — sshkeys.go
//
//	GET    /api/auth/ssh-keys            — list the caller's own keys
//	POST   /api/auth/ssh-keys            — register one (authorized_keys line)
//	POST   /api/auth/ssh-keys/{id}/state — enable / disable without deleting
//	DELETE /api/auth/ssh-keys/{id}       — remove permanently
//
// ⚠ This screen is a PREREQUISITE for the SFTP endpoint, not a follow-up:
// `ssh-copy-id` cannot work against filex. It appends to
// `~/.ssh/authorized_keys` over a shell, and filex has no shell — so without a
// place to paste a key, public-key authentication is unreachable and everybody
// falls back to sending their account password to a file server.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/sftpsrv"
)

// SSHKeys is the self-service SSH key handler.
//
// It also reports the FTPS endpoint, because the two are one screen for the
// user: "how do I reach this server from a program that is not a browser?".
// Splitting the facts across two endpoints would mean the guide had to make
// two calls to answer one question.
type SSHKeys struct {
	Store db.Store
	// Auth is the shared credential resolver. Held only so revoking a key can
	// reach an SFTP session that key already opened — without it, deleting a
	// key means "cannot log in again", not "is logged out".
	Auth *protocolauth.Resolver
	// Enabled mirrors the FILEX_SFTP kill switch, so the UI can say the
	// endpoint is off instead of collecting keys for a port that is closed.
	Enabled bool
	// Addr and Host are what a client needs to connect, echoed back so the
	// instructions name this deployment rather than a placeholder.
	Host string
	Port int
	// FTPS is the other non-HTTP endpoint's shape.
	FTPS FTPSFacts
}

// FTPSFacts is what an FTP client needs to be told, computed on the server.
type FTPSFacts struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	// ⚠ The passive range matters as much as the port: a firewall that blocks
	// it makes every transfer HANG with no error on either side, which is the
	// classic FTP failure and impossible to guess at from the client end.
	PasvMin int `json:"pasv_min"`
	PasvMax int `json:"pasv_max"`
	// SelfSigned is true when no certificate was configured, so the guide can
	// say the channel is encrypted but unverified rather than letting somebody
	// discover it from a client warning.
	SelfSigned bool `json:"self_signed"`
}

// NewSSHKeys constructs the handler.
func NewSSHKeys(store db.Store, res *protocolauth.Resolver, enabled bool, host string, port int, ftps FTPSFacts) *SSHKeys {
	return &SSHKeys{Store: store, Auth: res, Enabled: enabled, Host: host, Port: port, FTPS: ftps}
}

func (h *SSHKeys) connection() map[string]any {
	return map[string]any{
		"enabled": h.Enabled,
		"host":    h.Host,
		"port":    h.Port,
		"ftps":    h.FTPS,
	}
}

// List returns the caller's own keys.
func (h *SSHKeys) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	keys, err := h.Store.ListSSHPublicKeys(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := h.connection()
	out["keys"] = keys
	// The login name a client must use. It is the username when the account has
	// one, because an `@` in an SSH login has to be quoted in every client's
	// config file.
	out["login"] = u.Username
	if out["login"] == "" {
		out["login"] = u.Email
	}
	writeJSON(w, http.StatusOK, out)
}

type sshKeyReq struct {
	// Key is one authorized_keys line: `<type> <base64> [comment]`.
	Key string `json:"key"`
	// Name overrides the comment from the key.
	Name string `json:"name"`
}

// Create registers a key.
func (h *SSHKeys) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req sshKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	fingerprint, normalized, comment, err := sftpsrv.ParseAuthorizedKey(req.Key)
	if err != nil {
		// The parser's own words: "not a valid public key" and "no
		// authorized_keys options" are different mistakes with different fixes.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = comment
	}
	created, err := h.Store.CreateSSHPublicKey(r.Context(), &model.SSHPublicKey{
		UserID: u.ID, Name: name, Fingerprint: fingerprint, PublicKey: normalized,
	})
	if err != nil {
		// ⚠ The fingerprint is unique across the install, so this also fires
		// when SOMEBODY ELSE registered the same key. The message must not say
		// which — that would confirm another account holds it.
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "that key cannot be registered; it may already be in use",
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": created})
}

// SetState disables or re-enables a key without deleting it.
func (h *SSHKeys) SetState(w http.ResponseWriter, r *http.Request) {
	_, k, ok := h.ownedKey(w, r)
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
	if err := h.Store.SetSSHPublicKeyDisabled(r.Context(), k.ID, req.Disabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.Disabled {
		protocolauth.KickCredential(h.Auth, protocolauth.KickSSHKey, k.ID)
	}
	updated, _ := h.Store.GetSSHPublicKeyByID(r.Context(), k.ID)
	writeJSON(w, http.StatusOK, map[string]any{"key": updated})
}

// Delete removes a key.
func (h *SSHKeys) Delete(w http.ResponseWriter, r *http.Request) {
	u, k, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	if err := h.Store.DeleteSSHPublicKey(r.Context(), k.ID, u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	protocolauth.KickCredential(h.Auth, protocolauth.KickSSHKey, k.ID)
	w.WriteHeader(http.StatusNoContent)
}

// ownedKey resolves {id} and confirms the caller owns it.
//
// ⚠ 404, not 403, for somebody else's key — the same rule the access-key
// surface follows, for the same reason: distinguishing them turns the endpoint
// into a way to enumerate other accounts' credentials.
func (h *SSHKeys) ownedKey(w http.ResponseWriter, r *http.Request) (*model.User, *model.SSHPublicKey, bool) {
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
	k, err := h.Store.GetSSHPublicKeyByID(r.Context(), id)
	if err != nil || k == nil || k.UserID != u.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such key"})
		return nil, nil, false
	}
	return u, k, true
}
