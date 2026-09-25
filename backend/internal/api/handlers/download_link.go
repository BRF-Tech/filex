package handlers

// One-file download links — the browser's drag-out, for a page that signs its
// calls with a bearer (#71).
//
// # The gap
//
// Chromium lets a page drag ONE file out onto the desktop: `dataTransfer
// .setData('DownloadURL', 'mime:name:url')`, and on the drop the browser's own
// download stack fetches `url` into wherever the drop landed. That stack sends
// cookies but never an Authorization header. The admin SPA signs its calls
// with a bearer, so the explorer's download URL arrived there anonymous and
// the drag was not offered at all (lib/dragOut canDownloadUrlDrag).
//
// # Why this is not a new URL system
//
// The selection-archive tickets (archive_download.go) already are exactly this
// shape: an authenticated mint that decides, and a credential-free redeem the
// browser navigates to. A one-file link is the same mint asked with
// `"mode":"file"`, the same store, the same `/z/{ticket}` route and the same
// 410 / 404 / 409 answers. What differs is only what the ticket names (one
// file, streamed as itself through the manager's own download path) and how
// much the redeem trusts the mint:
//
//   - an ARCHIVE ticket carries a finished member list and is not re-judged at
//     the redeem — the walk that built it was the authorization;
//   - a FILE link is re-judged at the redeem as its OWNER: the account must
//     still be able to sign in, a token it was minted with must still exist,
//     the storage must still be the owner's tenant's, the request must arrive
//     on the tenant host it was minted on, and the owner's ≥viewer on the file
//     is asked again (Manager.ServeLinkedFile). A drag link is minted
//     speculatively — on hover, before anybody decided to drag — so "you could
//     read it a minute ago" is not good enough.
//
// # Why it is short
//
// `dragstart` has to fill the dataTransfer synchronously, so the client mints
// the link BEFORE the drag, when the pointer rests on a row (lib/dragOut
// createDragLinks). A link therefore exists for files nobody ends up dragging.
// A minute is long enough for a hover, a drag and a drop, and short enough
// that an unused link is worthless by the time anybody could find it. It is
// single-use besides: consumed by the download it starts.
//
// # Folders
//
// Refused at the mint (409 IS_FOLDER). A folder dragged out would be an
// archive, and minting one walks the whole subtree with the caller's grants
// (DownloadTicket) — work this client would start speculatively for every
// folder the pointer crossed. Download (the button) still gives a folder as
// one archive; the desktop app drags folders as real folders.

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

// fileLinkTTL is the longest a one-file link lives. A caller may ask for less
// (`expires_in_seconds`), never for more.
const fileLinkTTL = 60 * time.Second

// Audit actions of a redeem. Two, so a refusal never reads as a download in
// the Audit page (web/src/lib/auditLabel composes "<resource> <verb>").
const (
	auditFileLinkServed  = "file.download_link"
	auditFileLinkRefused = "file.download_link_refused"
)

// fileLink is what a one-file ticket names. Everything the redeem re-checks is
// here; nothing the client said after the mint is consulted.
type fileLink struct {
	StorageID int64
	// Path is the driver path (no adapter prefix).
	Path string
	// Target is `<storage>://<path>` — the audit row's readable name.
	Target string
	NodeID int64
	// OwnerID is the account the link acts for, re-read at the redeem.
	OwnerID int64
	// TokenID is the API token the mint was signed with, 0 for a session. A
	// revoked token takes its links with it.
	TokenID int64
	// HostProvider is the tenant the MINT's host resolved to (0: none, or a
	// single-tenant install). The redeem must resolve to the same one.
	HostProvider int64
}

// FileLinkStreamer serves the bytes of one file for a link. *Manager is the
// implementation: the link goes through the same download path as the
// explorer's own Download (Range, staging, MIME, disposition), not a copy of it.
type FileLinkStreamer interface {
	ServeLinkedFile(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string)
}

// AttachFileLinks wires one-file links. Without it `"mode":"file"` answers 503.
func (a *Archive) AttachFileLinks(s FileLinkStreamer, multiTenant bool) {
	a.Files = s
	a.MultiTenant = multiTenant
}

// hostProvider is the enabled tenant this request's Host names, 0 for none.
// Single-tenant installs never read the host at all.
func (a *Archive) hostProvider(r *http.Request) int64 {
	if !a.MultiTenant || a.Store == nil {
		return 0
	}
	host := tenanturl.RequestHost(r)
	if host == "" {
		return 0
	}
	p, err := a.Store.GetProviderByHost(r.Context(), host)
	if err != nil || p == nil {
		return 0
	}
	return p.ID
}

// mintFileLink answers POST /api/files/archive/download {"mode":"file"}.
//
//	400  not exactly one path, or a malformed one
//	403  outside the token's root, or not readable by this caller
//	404  no such file (or a storage that is not this tenant's)
//	409  IS_FOLDER — see the file header
func (a *Archive) mintFileLink(w http.ResponseWriter, r *http.Request, req archiveDownloadRequest) {
	if a.Tickets == nil || a.Files == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "download links unavailable"})
		return
	}
	if len(req.Paths) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a download link names exactly one file"})
		return
	}
	ctx := r.Context()
	owner := auth.UserFrom(ctx)
	if owner == nil || owner.ID <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	raw := req.Paths[0]
	storageID, rel, err := a.resolveStorage(ctx, 0, raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if rel == "" || pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	if syspath.Sealed(rel) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !ownsStorage(w, r, storageID, "storage") {
		return
	}
	if !rootAllows(ctx, a.Store, storageID, rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": confine.ErrOutOfRoot.Error()})
		return
	}
	if !aclAllowID(ctx, a.ACL, a.Store, storageID, rel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + raw})
		return
	}
	st, err := a.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}

	// File or folder, from the catalogue first: a mint happens on hover, and
	// asking a remote driver for every row the pointer rests on is a round
	// trip the catalogue usually answers for free. The driver decides only
	// for a path the catalogue has not met (a lazily catalogued storage).
	var nodeID, size int64
	isDir := false
	if n, err := a.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/"+rel)); err == nil && n != nil {
		nodeID, size, isDir = n.ID, n.Size, n.Type == model.NodeTypeDirectory
	} else {
		drv, err := a.StorageResolver(storageID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
			return
		}
		stat, err := drv.Stat(ctx, rel)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		size, isDir = stat.Size, stat.Kind == storage.KindDirectory
	}
	if isDir {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "a folder cannot travel as one file — download it as an archive",
			"code":  "IS_FOLDER",
		})
		return
	}

	ttl := fileLinkTTL
	if req.ExpiresInSeconds > 0 && time.Duration(req.ExpiresInSeconds)*time.Second < ttl {
		ttl = time.Duration(req.ExpiresInSeconds) * time.Second
	}
	var tokenID int64
	if tok := auth.TokenFrom(ctx); tok != nil {
		tokenID = tok.ID
	}
	name := path.Base(rel)
	expires := time.Now().Add(ttl)
	tok, err := a.Tickets.mint(&archiveTicket{
		Name:      name,
		Bytes:     size,
		Owner:     owner,
		ExpiresAt: expires,
		File: &fileLink{
			StorageID: storageID, Path: rel, Target: st.Name + "://" + rel, NodeID: nodeID,
			OwnerID: owner.ID, TokenID: tokenID, HostProvider: a.hostProvider(r),
		},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, archiveDownloadInfo{
		URL:        "/z/" + tok,
		Ticket:     tok,
		Name:       name,
		Files:      1,
		Bytes:      size,
		ExpiresAt:  expires.UTC().Format(time.RFC3339),
		Mode:       "file",
		TTLSeconds: int(ttl / time.Second),
	})
}

// redeemFileLink streams a one-file link's file as its owner, after asking
// again everything the owner's reach depends on. Called from DownloadArchive
// once the ticket is claimed.
//
// A refusal CONSUMES the link: nothing about a revoked grant or a disabled
// account gets better by asking again, and a link that keeps answering 403 is
// a probe. A server-side failure (5xx) RELEASES it, so a retry of the same
// drop works while the link lives.
func (a *Archive) redeemFileLink(w http.ResponseWriter, r *http.Request, tok string, t *archiveTicket) {
	fl := t.File
	ctx := r.Context()
	refuse := func(status int, reason, msg string) {
		a.Tickets.consume(tok)
		a.auditFileLink(r, fl, auditFileLinkRefused, reason)
		http.Error(w, msg, status)
	}
	if a.Files == nil {
		a.Tickets.release(tok)
		http.Error(w, "download links unavailable", http.StatusServiceUnavailable)
		return
	}

	owner, err := a.Store.GetUser(ctx, fl.OwnerID)
	if err != nil || owner == nil || !auth.LoginAllowed(ctx, a.Store, a.MultiTenant, owner) {
		refuse(http.StatusForbidden, "account", "this download link is no longer valid")
		return
	}
	var token *model.APIToken
	if fl.TokenID != 0 {
		token, err = a.Store.GetAPITokenByID(ctx, fl.TokenID)
		if err != nil || token == nil || token.UserID != owner.ID ||
			(token.ExpiresAt != nil && !token.ExpiresAt.After(time.Now())) {
			refuse(http.StatusForbidden, "token", "this download link is no longer valid")
			return
		}
	}
	if a.hostProvider(r) != fl.HostProvider {
		refuse(http.StatusForbidden, "host", "this download link belongs to another address")
		return
	}

	// From here on the request IS the owner — the same context the explorer's
	// own Download runs with.
	uctx := auth.WithUser(ctx, owner)
	if token != nil {
		uctx = auth.WithToken(uctx, token)
	}
	if a.MultiTenant {
		uctx = tenant.WithScope(uctx, auth.ScopeForUser(uctx, a.Store, owner))
	}
	ur := r.WithContext(uctx)

	st, err := a.Store.GetStorage(uctx, fl.StorageID)
	if err != nil || st == nil || !st.Enabled || !ownsStorageQuiet(ur, st.ID) {
		refuse(http.StatusNotFound, "storage", "not found")
		return
	}
	if !aclAllowID(uctx, a.ACL, a.Store, st.ID, fl.Path, acl.LevelViewer) {
		refuse(http.StatusForbidden, "acl", "you can no longer read this file")
		return
	}

	rec := &linkStatus{ResponseWriter: w}
	rec.onHeader = func(status int) {
		if status < 400 {
			a.auditFileLink(r, fl, auditFileLinkServed, "")
		} else if status < 500 {
			a.auditFileLink(r, fl, auditFileLinkRefused, "stream-"+strconv.Itoa(status))
		}
	}
	a.Files.ServeLinkedFile(rec, ur, st, fl.Path)
	if rec.status >= 500 {
		a.Tickets.release(tok)
		return
	}
	a.Tickets.consume(tok)
}

// auditFileLink writes the redeem's audit row. ⚠ The link itself never goes
// into the row: a live link in an audit table is a live link for everybody who
// can read audit rows (the same rule as public_api.auditPinLock).
func (a *Archive) auditFileLink(r *http.Request, fl *fileLink, action, reason string) {
	if a.Store == nil || fl == nil {
		return
	}
	uid := fl.OwnerID
	meta := map[string]any{"target_name": fl.Target, "storage_id": fl.StorageID}
	if reason != "" {
		meta["reason"] = reason
	}
	if fl.TokenID != 0 {
		meta["token_id"] = fl.TokenID
	}
	target := ""
	if fl.NodeID > 0 {
		target = strconv.FormatInt(fl.NodeID, 10)
	}
	// Not the request's context: a download that the browser abandons half-way
	// has still happened, and its row must not be lost to the cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	_ = a.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID: &uid, Action: action, TargetType: "node", TargetID: target,
		IP: clientIP(r), Metadata: meta,
	})
}

// linkStatus records the status the streamer answered and reports it once, the
// moment it is known — so the audit row is written when the download STARTS,
// not after a multi-gigabyte body has finished.
type linkStatus struct {
	http.ResponseWriter
	status   int
	onHeader func(int)
}

func (s *linkStatus) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
		if s.onHeader != nil {
			s.onHeader(code)
		}
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *linkStatus) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (s *linkStatus) Unwrap() http.ResponseWriter { return s.ResponseWriter }
