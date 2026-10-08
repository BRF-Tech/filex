package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// OnlyOffice exposes the editor config + fetch + callback endpoints.
type OnlyOffice struct {
	Service         *onlyoffice.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	ACL             *acl.Resolver
	// Body resolves where the document's bytes are: the driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe.
	Body *filebody.Resolver
	// FrameURL is the editor's frame on another origin (task #92):
	// FILEX_ONLYOFFICE_FRAME_ORIGIN + onlyoffice.FrameHostPath, else
	// FILEX_APP_UI_ORIGIN + base path + onlyoffice.FramePath. Handed out with
	// every editor config so the explorer runs api.js there. Empty: neither
	// is set, and api.js runs in filex's own page.
	FrameURL string
}

// AttachBody wires the byte-source resolver so a document that is still being
// transferred opens in the editor.
func (h *OnlyOffice) AttachBody(b *filebody.Resolver) { h.Body = b }

// AttachACL wires the RBAC resolver: opening a document needs ≥viewer; an
// editable config additionally needs ≥editor (else it's downgraded to view).
func (h *OnlyOffice) AttachACL(r *acl.Resolver) { h.ACL = r }

// NewOnlyOffice constructs the handler.
//
// ⚠ The handler is ALWAYS wired now, even when nothing is configured yet: what
// "configured" means is a question for the `external_services` row at request
// time, not for whatever env carried at boot. Every entry point asks
// Service.EnabledCtx, which is nil-safe, so a nil service still answers 503.
func NewOnlyOffice(svc *onlyoffice.Service, store db.Store, resolver func(int64) (storage.Driver, error)) *OnlyOffice {
	return &OnlyOffice{Service: svc, Store: store, StorageResolver: resolver}
}

// fetchProbe answers the reverse-path check that arrives at the fetch endpoint:
// node onlyoffice.ProbeNodeID plus a one-shot, unguessable token `t`, signed
// the way a document's URL is (see onlyoffice.CheckReversePath).
//
// ⚠ It used to have a door of its own, /api/files/onlyoffice/probe, which
// asked for no signature and did not look at the configuration in force, so a
// green Test could stand next to a fetch every document failed (issue #80).
// It now asks what a document's fetch asks. Unauthenticated by necessity, the
// caller is another container, so it serves a fixed sentence and nothing else.
// An unknown or expired token is a plain 404: a stranger who guesses at it
// learns nothing.
func (h *OnlyOffice) fetchProbe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	status, body := h.Service.ServeFetchProbe(r.Context(), q.Get("t"), exp, q.Get("sig"))
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		http.NotFound(w, r)
		return
	default:
		writeJSON(w, status, map[string]string{"error": "probe refused"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="filex-probe.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// Config returns the editor descriptor for an iframe to render.
//
// Accepts both forms:
//
//	GET  /api/files/onlyoffice/config?id=<node-id>&lang=tr
//	POST /api/files/onlyoffice/config  { "path": "adapter://rel", "mode": "edit"|"view" }
//
// The GET form is what the standalone Editor.vue route hands the SFC's
// PreviewModal when the embedder passes a numeric node id. The POST
// form is what the modal itself sends from inside the explore page —
// it has the adapter-qualified path handy but not the node id, so the
// handler must resolve path → node before continuing.
//
// `lang` (query or body) and the Accept-Language header are inputs to the
// editor's language, not the answer: the administrator's fixed language wins
// over both (onlyoffice/lang.go, docs/ONLYOFFICE.md → The editor's language).
func (h *OnlyOffice) Config(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var (
		node *model.Node
		lang string
		mode string
	)
	q := r.URL.Query()
	lang = q.Get("lang")
	mode = q.Get("mode")

	if r.Method == http.MethodPost {
		var body struct {
			Path   string `json:"path"`
			NodeID int64  `json:"node_id"`
			Mode   string `json:"mode"`
			Lang   string `json:"lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		if body.Lang != "" {
			lang = body.Lang
		}
		if body.Mode != "" {
			mode = body.Mode
		}
		if body.NodeID > 0 {
			n, err := h.Store.GetNode(r.Context(), body.NodeID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			node = n
		} else if body.Path != "" {
			n, err := h.resolveNodeByPath(r.Context(), body.Path)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			node = n
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or node_id"})
			return
		}
	} else {
		idStr := q.Get("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		n, err := h.Store.GetNode(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		node = n
	}

	// ⚠⚠ No mode IS edit. The document server's config builder treats anything
	// but "" or "edit" as view (onlyoffice.Service.Config), so a request that
	// simply left `mode` out was handed an editing session — and the two
	// downgrades below only looked for the literal "edit": a viewer, or a
	// trashed file, opened without `mode` came back editable, with a save
	// callback (found 2026-09-21 while adding the second downgrade). Spell the
	// default out once so every check below sees what the service will do.
	if mode == "" {
		mode = "edit"
	}

	// Whose node is it? Both id-taking shapes above (POST body `node_id`, GET
	// query `id`) call GetNode, which tenantstore does not confine, so a
	// client-supplied node id crossed tenants. The `path` shape is safe and
	// always was, because resolveNodeByPath goes through the CONFINED
	// ListEnabledStorages — the same "two shapes, different security
	// properties" split as /api/files/share.
	//
	// ⚠ docs/MULTI-TENANCY.md argued this endpoint was safe because "storage
	// is derived server-side from the node". That is true and it is not a
	// defence: the NODE ID is client-supplied, so deriving the storage from
	// it derives nothing about the caller. The RBAC block below is not a
	// boundary either — with storages.rbac_enabled off, which is the default,
	// Effective() returns the account-role base for every path.
	//
	// ⚠ What this handed over is BYTES, not metadata: the config carries an
	// HMAC-signed, credential-free fetch URL redeemed at the PUBLIC
	// /api/files/onlyoffice/fetch, for every extension OnlyOffice opens.
	// Refused with the same 404 an unknown node id already produced.
	if node != nil && !h.nodeVisible(r.Context(), node) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	// RBAC: must be able to view the doc at all; an edit request without
	// ≥editor is downgraded to a read-only view (viewers can preview office
	// files but never edit/convert).
	// A document the storage could not answer for is not opened, to view or
	// to edit (issue #104).
	if node != nil {
		if st, err := h.Store.GetStorage(r.Context(), node.StorageID); err == nil && st != nil &&
			refuseUnavailableNode(w, r, h.Store, st, node) {
			return
		}
	}
	if node != nil && h.ACL != nil {
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
		if mode == "edit" && !aclCanID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, perm.FilesModify).ok {
			mode = "view"
		}
	}
	// …and a token without `write` gets the view (auth/token_verbs.go): the
	// document server's save lands through the signed callback, where no token
	// is asked again, so an editing session here IS the write.
	if mode == "edit" && !auth.TokenAllows(r.Context(), auth.VerbWrite) {
		mode = "view"
	}
	// ⚠⚠ The desktop's open-with working copy IS edited here — its editor
	// window opens `.filex-open/<session>-<name>` and this save is how the
	// edit gets back to the person's own file (syspath.PutWorkCopy).
	// Anything else among filex's own (a trashed file, a version) opens
	// read-only, the same downgrade a viewer gets: a save would write into
	// the bin or the history behind their back. The one other thing edited
	// here is a draft of the caller's OWN (issue #71, syspath.OwnDraft): a
	// new office document lives in the drafts area until its first save.
	if node != nil && mode == "edit" && syspath.Refused(syspath.PutWorkCopy, node.Path) &&
		syspath.RefusedBy(syspath.OwnDraft, node.Path, user.ID) {
		mode = "view"
	}

	/* wiring:e2 — E2E-encrypted files can never open in OnlyOffice: the DS
	   would fetch ciphertext (and a save callback would clobber it). Sniff
	   the 'filexe2e' magic before building a config. Read errors fall
	   through — the fetch path will surface them as before. */
	// filex 0.51: the same first bytes say how a CSV is written (its
	// delimiter and encoding), which the config hands ONLYOFFICE so it opens
	// the file without asking (onlyoffice/csv.go). Read further for a CSV.
	var head []byte
	if node != nil && h.StorageResolver != nil {
		if drv, derr := h.StorageResolver(node.StorageID); derr == nil {
			src, serr := h.Body.Resolve(r.Context(), drv, node.StorageID, node.Path, node)
			if serr != nil {
				writeStagingGone(w, serr)
				return
			}
			if rc, rerr := src.Open(r.Context()); rerr == nil {
				want := len(e2e.MagicPrefix)
				if strings.EqualFold(path.Ext(node.Name), ".csv") {
					want = onlyoffice.CSVSniffBytes
				}
				buf := make([]byte, want)
				n, _ := io.ReadFull(rc, buf)
				_ = rc.Close()
				head = buf[:n]
				if n >= len(e2e.MagicPrefix) && e2e.HasEncryptedPrefix(head[:len(e2e.MagicPrefix)]) /* wiring:e2 fxe — a .fxe too */ {
					writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "file is e2e-encrypted"})
					return
				}
			}
		}
	}
	/* /wiring:e2 */
	// The editor's language is the server's choice (onlyoffice/lang.go): the
	// administrator's fixed one, else `lang`, the screen's (Accept-Language),
	// the account's, the instance's - one rule for every screen.
	cfg, err := h.Service.BuildConfigForNode(r.Context(), node, user, lang, mode, onlyoffice.WithHead(head),
		onlyoffice.WithAcceptLanguage(r.Header.Get("Accept-Language")))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Where the explorer runs the editor's api.js: a frame on the interface
	// origin when there is one (task #92). The config itself is unchanged,
	// whichever page loads it.
	cfg.Frame = h.FrameURL
	writeJSON(w, http.StatusOK, cfg)
}

// nodeVisible is the boundary every id-taking shape here needs: the node's
// storage belongs to the caller's tenant, and its path is inside the caller's
// root. Both id-taking shapes (GET ?id=, POST node_id) skip confine.Middleware;
// the config carries an HMAC-signed, credential-free fetch URL for the file's
// BYTES, so an out-of-root leak there is byte access. The `path` shapes were
// already confined by the middleware, and asking again changes nothing.
func (h *OnlyOffice) nodeVisible(ctx context.Context, node *model.Node) bool {
	if scope, confined := confinedScope(ctx); confined && !scope.CanAccessStorage(node.StorageID) {
		return false
	}
	return rootAllows(ctx, h.Store, node.StorageID, node.Path)
}

// Diagnose answers the editor's question after "Download failed": did the
// document server ask filex for this document since it was opened, and what
// did filex answer? (onlyoffice.Service.Diagnose, issue #80.)
//
//	GET /api/files/onlyoffice/diagnose?path=<adapter://rel>
//	GET /api/files/onlyoffice/diagnose?id=<node-id>
//
// ⚠ Only for a person who may open the document: the same tenant, root and
// viewer checks the editor configuration asks, with the same 404 for a
// document the caller cannot see. The answer names no path and no storage,
// only when, what status, and why.
//
// It is answered from this process's memory; with several replicas each one
// knows only the requests it served (docs/ONLYOFFICE.md).
//
// No MCP tool mirrors it on purpose: it explains a failure of the in-browser
// editor, which an agent never opens. An agent that needs the same facts
// reads the server log, where every refusal is written with its reason.
func (h *OnlyOffice) Diagnose(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	if auth.UserFrom(r.Context()) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	q := r.URL.Query()
	var (
		node *model.Node
		err  error
	)
	if p := q.Get("path"); p != "" {
		node, err = h.resolveNodeByPath(r.Context(), p)
	} else {
		id, perr := strconv.ParseInt(q.Get("id"), 10, 64)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or id"})
			return
		}
		node, err = h.Store.GetNode(r.Context(), id)
	}
	if err != nil || node == nil || !h.nodeVisible(r.Context(), node) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if h.ACL != nil && !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	writeJSON(w, http.StatusOK, h.Service.Diagnose(node.ID))
}

// Session answers the office editor about the editing session it has open,
// when the document changed outside it (#184, onlyoffice/session_base.go).
//
//	POST /api/files/onlyoffice/session
//	{ "path": "adapter://rel", "key": "<document.key>", "action": "state"|"mine"|"theirs",
//	  "token": "<the editor config's token>" }
//	→ { "stale": bool, "known": bool }
//
// `state` (the default) asks whether the session is still on the document's
// current version: stale means a save of it would be written beside the
// document, not over it. `mine` is the person's answer "write my version over
// the outside one" (the session's base moves to the version now), `theirs`
// "keep the outside version" (the session's save, when it comes, is not
// written). `known` is false when this process had no record of the session.
//
// ⚠ Asking needs what opening the document needs (the same tenant, root and
// viewer checks, the same 404). Answering decides what becomes of a save, so
// it needs what an editing session needs: files.modify and a token that may
// write - and, since 0.54, to be one of the session's own editors
// (onlyoffice.Service.MayAnswer: this process handed them an editing session
// of that key, or `token`, the editor configuration they were handed, says
// so). Anybody else is answered 403 `not_your_session`; who answered is
// recorded (audit file.office_session_answered).
func (h *OnlyOffice) Session(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	caller := auth.UserFrom(r.Context())
	if caller == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Path   string `json:"path"`
		Key    string `json:"key"`
		Action string `json:"action"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if body.Path == "" || key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or key"})
		return
	}
	node, err := h.resolveNodeByPath(r.Context(), body.Path)
	if err != nil || node == nil || !h.nodeVisible(r.Context(), node) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if h.ACL != nil && !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	switch body.Action {
	case "", "state":
	case "mine", "theirs":
		if h.ACL != nil && !aclCanID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, perm.FilesModify).ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
		// A token's verbs, like every other door: an answer is a write.
		if !auth.AllowVerb(w, r, auth.VerbWrite) {
			return
		}
		// Only the session's own editors answer for it.
		if !h.Service.MayAnswer(r.Context(), node, key, caller.ID, body.Token) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "not_your_session"})
			return
		}
		if body.Action == "mine" {
			if err := h.Service.RebaseSession(r.Context(), node, key); err != nil {
				slog.Warn("onlyoffice session: could not read the document now", slog.Int64("storage", node.StorageID), slog.Any("err", err))
				writeJSON(w, http.StatusConflict, map[string]string{"error": "the document cannot be read now"})
				return
			}
		} else if err := h.Service.DropSession(r.Context(), node, key); err != nil {
			slog.Warn("onlyoffice session: could not record the answer", slog.Int64("storage", node.StorageID), slog.Any("err", err))
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the answer could not be recorded"})
			return
		}
		h.Service.NoteAnswer(r.Context(), node, caller.ID, body.Action)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return
	}
	stale, known := h.Service.SessionState(r.Context(), node, key)
	writeJSON(w, http.StatusOK, map[string]bool{"stale": stale, "known": known})
}

// resolveNodeByPath looks up a node from a `<adapter>://<rel>` or bare
// `<rel>` path. The bare form falls back to the first enabled storage,
// matching the SFC's path-stripping convention.
func (h *OnlyOffice) resolveNodeByPath(ctx context.Context, raw string) (*model.Node, error) {
	adapter, rel := splitAdapterPath(raw)
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "" {
		return nil, errors.New("empty path")
	}
	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil || len(storages) == 0 {
		return nil, errors.New("no storages")
	}
	var st *model.Storage
	if adapter != "" {
		for _, s := range storages {
			if s.Name == adapter {
				st = s
				break
			}
		}
	}
	if st == nil {
		st = storages[0]
	}
	hash := pathkey.Hash(st.ID, rel)
	node, err := h.Store.GetNodeByPath(ctx, st.ID, hash)
	if err != nil || node == nil {
		return nil, errors.New("not found")
	}
	return node, nil
}

// Fetch streams document bytes back to the OnlyOffice document server.
//
// Public: no session required, but the URL must be HMAC-signed via the
// onlyoffice service.
//
// GET /api/files/onlyoffice/fetch?n=<id>&exp=<unix>&sig=<b64url>
//
// Every refusal here is logged with its reason. The only person who ever sees
// this endpoint fail is an operator reading "Download failed" in the editor —
// a message from the document server that says nothing about which of the five
// things went wrong. Without a line naming the reason, the status code in the
// access log is all they have, and a 500 from a bad signature and a 500 from
// an unreachable bucket look identical (issue #17).
func (h *OnlyOffice) Fetch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// The reverse-path probe walks this same door (fetchProbe). It is told
	// apart by its node id, which no document has, and its token; it checks
	// the configuration itself, because the Test may be measuring values that
	// are not saved yet.
	if q.Get("t") != "" && q.Get("n") == strconv.FormatInt(onlyoffice.ProbeNodeID, 10) {
		h.fetchProbe(w, r)
		return
	}
	// A file on offer for one conversion (the office engine's input), not a
	// catalogue document: onlyoffice_offer.go.
	if q.Get("o") != "" {
		h.fetchOffered(w, r)
		return
	}
	if !h.Service.EnabledCtx(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	id, err := strconv.ParseInt(q.Get("n"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad n"})
		return
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		if q.Get("p") == "" {
			h.fetchFailed(id, 0, http.StatusBadRequest, onlyoffice.FetchBadLink, "the link is malformed", err, false)
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad exp"})
		return
	}
	// A conversion's download (a thumbnail, an app's): its own signature,
	// its own record, and no ciphertext (onlyoffice/purpose.go).
	if p := q.Get("p"); p != "" {
		h.fetchForPurpose(w, r, id, exp, p)
		return
	}
	if err := h.Service.VerifyFetchSignatureCtx(r.Context(), id, exp, q.Get("sig")); err != nil {
		// Expiry and a wrong secret are the two shapes: a link the editor held
		// on to for too long, or a JWT secret changed under a running editor.
		code := onlyoffice.FetchSignatureBad
		if errors.Is(err, onlyoffice.ErrSignatureExpired) {
			code = onlyoffice.FetchSignatureExpired
		}
		h.fetchFailed(id, 0, http.StatusUnauthorized, code, "signature refused", err, false)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	// From here on the request is the document server's: its signature is
	// filex's own.
	node, src, rc, refused := h.openForDocumentServer(r.Context(), id)
	if refused != nil {
		h.fetchFailed(id, refused.storageID, refused.status, refused.code, refused.why, refused.err, true)
		refused.write(w)
		return
	}
	defer rc.Close()
	h.serveToDocumentServer(r.Context(), w, node, src)
	// Served: what the editor's "Download failed" diagnosis reads as "the
	// document server got the document; the failure is after this".
	h.Service.NoteFetch(id, http.StatusOK, "", "", true)
	_, _ = io.Copy(w, rc)
}

// fetchRefusal is why the document server's download of a document was
// refused: the status it is answered with, the code and the words the record
// and the log carry, and how the answer is written.
type fetchRefusal struct {
	storageID int64
	status    int
	code, why string
	err       error
	write     func(w http.ResponseWriter)
}

func refuseJSON(storageID int64, status int, code, why string, err error, body map[string]string) *fetchRefusal {
	return &fetchRefusal{storageID: storageID, status: status, code: code, why: why, err: err,
		write: func(w http.ResponseWriter) { writeJSON(w, status, body) }}
}

func refuseStaging(storageID int64, code, why string, err error) *fetchRefusal {
	return &fetchRefusal{storageID: storageID, status: stagingGoneStatusOf(err), code: code, why: why, err: err,
		write: func(w http.ResponseWriter) { writeStagingGone(w, err) }}
}

// openForDocumentServer opens a document's bytes for the document server's
// download, after its signature checked out: the catalogue row, the storage,
// the body (the staged copy while an upload is still transferring). The ONE
// way both doors read a document, so an editor's download and a conversion's
// cannot drift apart.
func (h *OnlyOffice) openForDocumentServer(ctx context.Context, id int64) (*model.Node, *filebody.Source, io.ReadCloser, *fetchRefusal) {
	node, err := h.Store.GetNode(ctx, id)
	if err != nil {
		return nil, nil, nil, refuseJSON(0, http.StatusNotFound, onlyoffice.FetchNotFound, "no catalogue row for this document", err,
			map[string]string{"error": "not found"})
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		// The storage this document lives on could not be opened at all -
		// wrong endpoint, wrong credentials, backend down.
		return nil, nil, nil, refuseJSON(node.StorageID, http.StatusInternalServerError, onlyoffice.FetchStorageUnavailable,
			"the storage could not be opened", err, map[string]string{"error": "no driver"})
	}
	src, err := h.Body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err != nil {
		return nil, nil, nil, refuseStaging(node.StorageID, onlyoffice.FetchBodyUnavailable, "the document body could not be located", err)
	}
	rc, err := src.Open(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// The catalogue has the row, the storage does not have the object:
			// a stale catalogue, or the file moved behind filex's back.
			return nil, nil, nil, refuseJSON(node.StorageID, http.StatusNotFound, onlyoffice.FetchObjectMissing,
				"the object is not on the storage", err, map[string]string{"error": "not found"})
		}
		return nil, nil, nil, refuseStaging(node.StorageID, onlyoffice.FetchReadFailed, "reading the object failed", err)
	}
	return node, src, rc, nil
}

// serveToDocumentServer writes the headers of a document's download.
func (h *OnlyOffice) serveToDocumentServer(ctx context.Context, w http.ResponseWriter, node *model.Node, src *filebody.Source) {
	mime := node.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	// Belt-and-suspenders: legacy rows scanned before the sniff fix
	// still carry "application/zip" for office files. OnlyOffice DS
	// rejects pptx with that Content-Type even when fileType matches
	// in the JWT config - refine on the way out so existing demos
	// don't need a full rescan after the deploy.
	mime = storage.RefineOfficeMime(mime, node.Name)
	w.Header().Set("Content-Type", mime)
	declareBodyLength(ctx, w, src, node)
}

// fetchForPurpose serves the document server's download for a conversion (a
// thumbnail, an app's): the address names its purpose and is signed with it
// (onlyoffice/purpose.go).
//
//   - What it answered is recorded for the purpose (NotePurposeFetch), never
//     in the editor's record: the editor's "Download failed" diagnosis is
//     about the editor's own request.
//   - An end-to-end encrypted file is refused, 415, by its folder, by its
//     name and by its first bytes: the server cannot read it, and a document
//     server must never be handed its ciphertext to draw. The thumbnail
//     pipeline does not ask for one; this is the second defence.
//   - An address whose signature does not check out is refused and recorded
//     nowhere: a stranger cannot fill the record.
func (h *OnlyOffice) fetchForPurpose(w http.ResponseWriter, r *http.Request, id, exp int64, purpose string) {
	ctx := r.Context()
	if err := h.Service.VerifyPurposeFetchCtx(ctx, id, exp, purpose, r.URL.Query().Get("sig")); err != nil {
		slog.Warn("onlyoffice: a conversion download was refused",
			slog.Int64("node", id), slog.String("purpose", purpose), slog.String("err", err.Error()))
		status := http.StatusUnauthorized
		if errors.Is(err, onlyoffice.ErrUnknownPurpose) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	refuse := func(rf *fetchRefusal) {
		attrs := []any{slog.Int64("node", id), slog.String("purpose", purpose), slog.String("reason", rf.why)}
		if rf.err != nil {
			attrs = append(attrs, slog.String("err", rf.err.Error()))
		}
		slog.Warn("onlyoffice: the document server could not download this file for a conversion", attrs...)
		h.Service.NotePurposeFetch(id, purpose, rf.status, rf.code)
		rf.write(w)
	}
	encrypted := func(storageID int64) *fetchRefusal {
		return refuseJSON(storageID, http.StatusUnsupportedMediaType, onlyoffice.FetchEncrypted,
			"the file is end-to-end encrypted", nil, map[string]string{"error": "file is e2e-encrypted"})
	}
	node, src, rc, refused := h.openForDocumentServer(ctx, id)
	if refused != nil {
		refuse(refused)
		return
	}
	defer rc.Close()
	/* wiring:e2 - by its folder and by its name, before a byte goes out. */
	if e2e.LooksEncryptedFile(node.Name) || e2e.UnderEncrypted(ctx, h.Store, node.StorageID, node.Path) {
		refuse(encrypted(node.StorageID))
		return
	}
	/* wiring:e2 fxe - and by its first bytes (either encrypted magic). */
	head := make([]byte, len(e2e.MagicPrefix))
	n, _ := io.ReadFull(rc, head)
	if n == len(head) && e2e.HasEncryptedPrefix(head) {
		refuse(encrypted(node.StorageID))
		return
	}
	h.serveToDocumentServer(ctx, w, node, src)
	h.Service.NotePurposeFetch(id, purpose, http.StatusOK, "")
	_, _ = io.Copy(w, io.MultiReader(bytes.NewReader(head[:n]), rc))
}

// stagingGoneStatusOf is the status writeStagingGone answers err with, so the
// fetch log records what the document server was actually told.
func stagingGoneStatusOf(err error) int {
	if errors.Is(err, filebody.ErrStagingGone) {
		return stagingGoneStatus
	}
	return http.StatusInternalServerError
}

// fetchFailed writes the one line an operator needs to tell five different
// "Download failed" errors apart, and records the refusal for the editor's
// diagnosis (onlyoffice.Service.NoteFetch). signed says the request's
// signature checked out; an unsigned refusal only annotates a document filex
// opened, so a stranger cannot fill the record.
func (h *OnlyOffice) fetchFailed(nodeID, storageID int64, status int, code, why string, err error, signed bool) {
	attrs := []any{slog.Int64("node", nodeID), slog.String("reason", why)}
	if storageID != 0 {
		attrs = append(attrs, slog.Int64("storage", storageID))
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	slog.Warn("onlyoffice: the document server could not download this file", attrs...)
	h.Service.NoteFetch(nodeID, status, code, why, signed)
}

// Callback receives save events from the OnlyOffice document server.
//
// POST /api/files/onlyoffice/callback?node=<id>
//
// Public — relies on the JWT in the body or Authorization header for
// integrity.
func (h *OnlyOffice) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": 1, "message": "onlyoffice not configured"})
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("node"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": 1, "message": "bad node"})
		return
	}
	resp, err := h.Service.HandleCallback(r, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": 1, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
