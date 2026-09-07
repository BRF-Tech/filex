package handlers

import (
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Thumb serves cached thumbnail JPEGs.
//
// ⚠⚠ Every request is authorized, by one of exactly two proofs:
//
//   - a live signature on the URL (`?exp=…&sig=…`), stamped by a listing that
//     had already cleared tenancy + ACL for that node. This is what lets a bare
//     `<img src>` — which carries no Authorization header, and no cookie either
//     when it sits in a third-party embed, because the session cookie is
//     SameSite=Lax — render at all; or
//   - an authenticated caller (session cookie, bearer/API token) who passes the
//     same tenancy, root-confinement and ACL checks the file's own listing
//     applies. This is the path every in-repo consumer actually takes: the SPA,
//     the desktop app and the embedded explorer all fetch thumbnails through
//     useThumbs, i.e. `fetch()` with credentials and auth headers.
//
// Neither proof → 401. It used to be neither proof → 200: `checkSig` returned
// true when `sig` was absent, `manager.go` emitted `thumb_url` with no
// signature, and the signing key was never seeded, so the whole scheme was
// inert and anyone could walk the dense node-id range and collect the rendered
// first page of every file on the instance. That was true of single-tenant
// installs too.
//
// ⚠ The public folder-share page does NOT come through here: it serves the same
// cached artefact via `/s/{token}/f/<path>?thumb=1`, scoped to the share token
// (see share_browse.go). An anonymous share viewer is therefore unaffected by
// this gate, and must stay that way.
type Thumb struct {
	Store    db.Store
	Pipeline *thumb.Pipeline
	// ACL gates the authenticated path. Nil = unwired (tests) and, per the
	// aclAllowID contract, allows — the tenancy and confinement checks above it
	// still run.
	ACL *acl.Resolver
	// Signer verifies the URL stamp. Nil is the fail-CLOSED state: unsigned and
	// signed URLs alike then need an authenticated caller.
	Signer *thumb.Signer
}

// NewThumb constructs a Thumb handler.
func NewThumb(store db.Store, p *thumb.Pipeline) *Thumb {
	return &Thumb{Store: store, Pipeline: p}
}

// AttachACL wires the RBAC resolver used by the authenticated path.
func (h *Thumb) AttachACL(r *acl.Resolver) { h.ACL = r }

// AttachSigner wires the URL signature verifier.
func (h *Thumb) AttachSigner(s *thumb.Signer) { h.Signer = s }

// Serve writes the JPEG bytes from the cache.
func (h *Thumb) Serve(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if !h.authorize(w, r, id) {
		return
	}

	t, err := h.Store.GetThumbnail(r.Context(), id)
	if err != nil || t.State != "ready" {
		http.Error(w, "not ready", http.StatusNotFound)
		return
	}
	path := h.Pipeline.CachePath(id)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = copyFileToResponse(w, f)
}

// authorize implements the two-proof contract described on Thumb. It writes
// the refusal itself and reports whether the caller may see the bytes.
func (h *Thumb) authorize(w http.ResponseWriter, r *http.Request, id int64) bool {
	q := r.URL.Query()
	if h.Signer.Verify(id, q.Get("exp"), q.Get("sig")) {
		return true
	}

	ctx := r.Context()
	user := auth.UserFrom(ctx)
	if user == nil {
		// ⚠ 401, not 403: an expired stamp on an otherwise fine URL should tell
		// the client to re-fetch the listing, not that the file is forbidden.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}

	n, err := h.Store.GetNode(ctx, id)
	if err != nil || n == nil || n.DeletedAt != nil {
		http.Error(w, "not ready", http.StatusNotFound)
		return false
	}

	// Tenancy. Same 404 body as the genuine miss above, so the endpoint is not
	// an existence oracle for another tenant's ids.
	if scope, confined := confinedScope(ctx); confined && !scope.CanAccessStorage(n.StorageID) {
		http.Error(w, "not ready", http.StatusNotFound)
		return false
	}

	// Root confinement. A `root:`-scoped token (the host app proxying an
	// embedded explorer) must not see previews of files outside its subtree —
	// the node id is not a path, so confine.Middleware's path rewriting cannot
	// reach this route and the check has to be made here.
	if root, ok := confine.RootFrom(ctx); ok {
		st, serr := h.Store.GetStorage(ctx, n.StorageID)
		if serr != nil || st == nil || !root.Within(st.Name, n.Path) {
			http.Error(w, "not ready", http.StatusNotFound)
			return false
		}
	}

	// RBAC. A rendered preview is readable content, so viewer is the bar.
	if !aclAllowID(ctx, h.ACL, h.Store, n.StorageID, n.Path, acl.LevelViewer) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// copyFileToResponse streams file contents to the writer.
func copyFileToResponse(w http.ResponseWriter, f *os.File) (int64, error) {
	stat, err := f.Stat()
	if err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return written, nil
			}
			return written, err
		}
	}
}
