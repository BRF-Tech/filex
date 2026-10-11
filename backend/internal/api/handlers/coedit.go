// Package handlers — coedit.go
//
// Editing together (task #189): the server half of an app interface's
// `coedit.*` bridge methods, for an app granted `files:co-edit`
// (docs/APP-PLUGINS-API.md → Editing together). The interface runs in a
// sandboxed frame with no network; filex's frame (AppFrame, lib/coeditSession)
// seals what the app appends with the session's key and calls these with the
// PERSON's session:
//
//	POST /api/files/plugins/ui/{plugin}/{view}/coedit/join              — join (or start) the file's session
//	POST /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/append      — a sealed entry, at a place
//	POST /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/lease       — the changes lease
//	POST /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/ticket      — a socket ticket (reconnect)
//	POST /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/leave       — leave
//	PUT  /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/blob/{name} — a sealed blob, once
//	GET  /api/files/plugins/ui/{plugin}/{view}/coedit/{sid}/blob/{name} — read one
//	GET  /api/coedit/ws?ticket=                                         — the session's entries, live
//
// and a save over the file (UISave) records which part of the log it holds
// (coeditSaved).
//
// ⚠⚠ Every check is made here, whatever the frame decided: the app runs, the
// view is its interface, it was granted files:co-edit (and files:read), the
// person may read the file (confinement, ACL), the file is not one the
// server holds only as ciphertext (an encrypted folder needs the plaintext
// door of files:e2e-plaintext first), and the view applies to it. Who a
// member is and whether it may write are the SERVER's answer - the account,
// the person behind a shared proxy token, files.modify on the file, the
// app's files:write, a storage that is not read-only - never the request's.
// A request that names a member must come from that member's account AND
// carry the member's token (coedit.Relay.Authorise): two people behind one
// proxy token are one account, not one member.
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/coedit"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// CoEdit serves the editing-together relay to the apps' interfaces.
type CoEdit struct {
	Apps *AppPlugins
	// Relay is nil when editing together is off (FILEX_COEDIT=0): every
	// route answers 503 coedit_off and the app edits alone.
	Relay *coedit.Relay
	// WS gives the socket's public address (tenant-aware, under the base
	// path) and whether the install is multi-tenant.
	WS *WS
	// tickets are single-use, short-lived socket tickets, in memory: the
	// socket is opened right after the join (or a ticket request) on the
	// same process - the arrangement of the realtime tickets.
	tickets realtime.OneShot[coeditTicket]
}

// NewCoEdit builds the handler. relay nil = off.
func NewCoEdit(apps *AppPlugins, relay *coedit.Relay, ws *WS) *CoEdit {
	return &CoEdit{Apps: apps, Relay: relay, WS: ws}
}

// coeditTicketTTL is how long a socket ticket may wait to be spent.
const coeditTicketTTL = 60 * time.Second

// coeditTouchEvery is how often an open socket says its member is still
// there, and checks the person still may read (and write) the file.
const coeditTouchEvery = 15 * time.Second

// coeditCursorGap is the least time between two cursors a socket passes on.
const coeditCursorGap = 100 * time.Millisecond

type coeditTicket struct {
	SID     string
	Client  string
	UserID  int64
	CanEdit bool
}

// on answers 503 when editing together is off; true = go on.
func (h *CoEdit) on(w http.ResponseWriter, r *http.Request) bool {
	if h == nil || h.Relay == nil || h.Apps == nil || h.Apps.Registry == nil {
		writeError(w, r, http.StatusServiceUnavailable, "coedit_off", nil)
		return false
	}
	return true
}

// app resolves {plugin}/{view} to a running app's interface that holds
// files:co-edit (and files:read); on failure the answer is written.
func (h *CoEdit) app(w http.ResponseWriter, r *http.Request) (*wasmplugin.Installed, *wire.View, bool) {
	if !h.on(w, r) {
		return nil, nil, false
	}
	p, v, ok := h.Apps.uiView(w, r)
	if !ok {
		return nil, nil, false
	}
	for _, need := range []wasmplugin.Permission{wasmplugin.PermFilesCoEdit, wasmplugin.PermFilesRead} {
		if !p.Grants.Has(need) {
			writeError(w, r, http.StatusForbidden, "not_granted", apierr.Params{"permission": string(need)})
			return nil, nil, false
		}
	}
	return p, v, true
}

// writeCoEditError answers a relay error with a code of its own and the
// server's sentence. Anything the relay did not name is a 500 whose detail
// stays in the log.
func writeCoEditError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *coedit.ConflictError
	switch {
	case errors.As(err, &conflict):
		writeError(w, r, http.StatusConflict, "coedit_conflict", nil, "head", conflict.Head)
	case errors.Is(err, coedit.ErrNoSession), errors.Is(err, coedit.ErrNotMember):
		writeError(w, r, http.StatusGone, "coedit_gone", nil)
	case errors.Is(err, coedit.ErrReadOnly):
		writeError(w, r, http.StatusForbidden, "coedit_read_only", nil)
	case errors.Is(err, coedit.ErrNoLease):
		writeError(w, r, http.StatusConflict, "coedit_no_lease", nil)
	case errors.Is(err, coedit.ErrTooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, "coedit_too_large", nil)
	case errors.Is(err, coedit.ErrLogFull):
		writeError(w, r, http.StatusConflict, "coedit_log_full", nil)
	case errors.Is(err, coedit.ErrBuild):
		writeError(w, r, http.StatusConflict, "coedit_build", nil)
	case errors.Is(err, coedit.ErrFileChanged):
		writeError(w, r, http.StatusConflict, "coedit_file_changed", nil)
	case errors.Is(err, coedit.ErrNotReady):
		writeError(w, r, http.StatusConflict, "coedit_not_ready", nil)
	case errors.Is(err, coedit.ErrNothingToWatch):
		writeError(w, r, http.StatusConflict, "coedit_nothing_to_watch", nil)
	case errors.Is(err, coedit.ErrFull):
		writeError(w, r, http.StatusConflict, "coedit_full", nil)
	case errors.Is(err, coedit.ErrBlobSet):
		writeError(w, r, http.StatusConflict, "coedit_blob_exists", nil)
	case errors.Is(err, coedit.ErrNoBlob):
		writeError(w, r, http.StatusNotFound, "coedit_no_blob", nil)
	case errors.Is(err, coedit.ErrSaveBehind), errors.Is(err, coedit.ErrSaveAhead):
		writeError(w, r, http.StatusConflict, "coedit_save_order", nil)
	case errors.Is(err, coedit.ErrNoServerKey):
		writeError(w, r, http.StatusServiceUnavailable, "coedit_no_secret_key", nil)
	case errors.Is(err, coedit.ErrBusy):
		writeError(w, r, http.StatusServiceUnavailable, "coedit_busy", nil)
	case errors.Is(err, coedit.ErrNoIdentity):
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", nil)
	case errors.Is(err, coedit.ErrKind), errors.Is(err, coedit.ErrEmpty), errors.Is(err, coedit.ErrCounter),
		errors.Is(err, coedit.ErrBlobName), errors.Is(err, coedit.ErrBadRequest), errors.Is(err, coedit.ErrKeySet):
		writeError(w, r, http.StatusBadRequest, "coedit_invalid", nil, "detail", err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
	}
}

// coeditBuild is the build a session's members must share: the app's version
// and its interface package's digest.
func coeditBuild(p *wasmplugin.Installed) string {
	b := p.Manifest.Version
	if p.Manifest.UI != nil {
		sum := strings.ToLower(p.Manifest.UI.Bundle.SHA256)
		if len(sum) > 16 {
			sum = sum[:16]
		}
		if sum != "" {
			b += "+" + sum
		}
	}
	return b
}

// coeditSig is the file as the server reads it: a session goes on from the
// file it started from or from its last save.
func coeditSig(obj *storage.Object) string {
	return fmt.Sprintf("%d:%d:%s", obj.Size, obj.Mtime.UnixNano(), obj.Etag)
}

type coeditMemberWire struct {
	Client    string `json:"client"`
	User      string `json:"user"`
	Name      string `json:"name"`
	IndexUser int    `json:"index_user"`
	CanEdit   bool   `json:"can_edit"`
}

func coeditWireMember(m coedit.Member) coeditMemberWire {
	return coeditMemberWire{Client: m.Client, User: m.User, Name: m.Name, IndexUser: m.IndexUser, CanEdit: m.CanEdit}
}

// readCoEditBody decodes a small JSON body (at most limit bytes).
func readCoEditBody(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(into); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "coedit_too_large", nil)
			return false
		}
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return false
	}
	return true
}

// Join joins the caller to the session of a file the interface was opened
// with, starting it when there is none.
//
//	POST …/coedit/join  {"path": "<qualified>"}
//	→ 200 {session, created, me, head, changes_head, saved_through,
//	       key_mode, key | sealed_key, token, socket: {url, ticket}, limits}
//
// `created`: this member started the session and stores the base document
// (`blob/base`) before anybody else may join; until then another opener is
// answered 409 coedit_not_ready, and asks again in a moment.
func (h *CoEdit) Join(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.app(w, r)
	if !ok {
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	var body struct {
		Path string `json:"path"`
		// E2E: the explorer holds the folder key of an encrypted folder's
		// file and seals the session's key with it (filex 0.56).
		E2E bool `json:"e2e"`
		// Unsent: this browser kept changes of an earlier opening that never
		// reached the relay; the answer carries the question's words.
		Unsent *struct {
			Count int `json:"count"`
		} `json:"unsent"`
	}
	if !readCoEditBody(w, r, 16<<10, &body) {
		return
	}
	target := strings.TrimSpace(body.Path)
	if target == "" {
		writeError(w, r, http.StatusBadRequest, "bad_path", nil)
		return
	}
	name := path.Base(strings.ReplaceAll(target, "\\", "/"))
	ctx := r.Context()
	storageID, rels, err := h.Apps.resolvePaths(ctx, 0, []string{target})
	if errors.Is(err, errOutsideRoot) || errors.Is(err, errSealedPath) {
		writePathsRefused(w, r, err)
		return
	}
	if err != nil || len(rels) != 1 {
		writeError(w, r, http.StatusBadRequest, "bad_path", nil)
		return
	}
	rel := rels[0]
	if !ownsStorage(w, r, storageID, "storage") {
		return
	}
	st, err := h.Apps.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil || !st.Enabled {
		notFound(w, "storage")
		return
	}
	if !rootAllows(ctx, h.Apps.Store, storageID, rel) {
		refuseOutsideRoot(w, r)
		return
	}
	// Reading the file is what joining it takes; whoever may not read it is
	// told nothing about its session.
	if !aclAllowName(ctx, h.Apps.ACL, h.Apps.Store, st.Name, rel, acl.LevelViewer) {
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "path_missing", nil)
		return
	}
	// A file the server holds only as ciphertext (filex 0.56): edited
	// together only by an explorer that holds its folder's key (`e2e`) and an
	// app the encrypted folder's door lets in (files:e2e-plaintext, the
	// administrator's switch, not a vault, not a `.fxe` - app_ui_e2e.go).
	// The session's key is then the members': their pages seal it with the
	// folder key (KeyFolder, …/key) and the server can open neither it nor
	// anything sealed with it.
	keyMode := coedit.KeyServer
	kind := encryptedAtDoor(ctx, h.Apps.Store, storageID, rel)
	if kind != "" {
		// A vault is one person's at a time (its app edits in the browser
		// alone, 0.56): never edited together, whatever the page says.
		if !body.E2E || kind == encryptedInVault {
			refuseEncryptedAtDoor(w, r, kind, rel, false)
			return
		}
		if !h.Apps.e2eDoor(w, r, p, storageID, rel, kind, false) {
			return
		}
		keyMode = coedit.KeyFolder
	}
	drv, err := h.Apps.StorageResolver(storageID)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "storage_unavailable", nil)
		return
	}
	// The kind, wherever the server can read it - the administrator's rule
	// for it and the kinds the view opens - as the read and save doors ask
	// it: over a file whose stored name is ciphertext (a folder with
	// encrypted names, level 2) the browser chose the app by the decrypted
	// name (app_ui_e2e.go e2eKindUnreadable).
	kindUnread := kind == encryptedInFolder && e2eKindUnreadable(ctx, h.Apps.Store, drv, storageID, rel)
	if !kindUnread && !h.Apps.openAllowed(w, r, p, v, name) {
		return
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil || obj.Kind == storage.KindDirectory {
		notFound(w, "file")
		return
	}
	// Only a file of a kind the view opens: what the app could not save it
	// could not edit together either.
	if !kindUnread && !viewSaves(w, r, v, rel, obj.Mime) {
		return
	}
	// An API key without `write` reads along, as the document server's
	// editor opens in view for it (onlyoffice.go): its appends are refused
	// at the route, so the others must not be shown an editor whose
	// edits never land, nor its cursor (auth/token_verbs.go).
	canEdit := p.Grants.Has(wasmplugin.PermFilesWrite) && !st.ReadOnly &&
		auth.TokenAllows(ctx, auth.VerbWrite) &&
		aclCanID(ctx, h.Apps.ACL, h.Apps.Store, storageID, rel, perm.FilesModify).ok
	name, _, person := presenceOf(r, u)
	who := strconv.FormatInt(u.ID, 10)
	if person != "" {
		who += "/" + person
	}
	token, err := coedit.NewToken()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	doc := coedit.DocKey(storageID, rel)
	hello, err := h.Relay.Open(ctx, coedit.OpenRequest{
		Doc: doc, StorageID: storageID, Path: rel,
		App: p.Row.Name, Build: coeditBuild(p), FileSig: coeditSig(&obj), KeyMode: keyMode,
	}, coedit.Identity{User: u.ID, Person: who, Name: name, CanEdit: canEdit, TokenHash: coedit.TokenHash(token)})
	if err != nil {
		writeCoEditError(w, r, err)
		return
	}
	ticket, err := h.tickets.Mint(coeditTicket{SID: hello.SID, Client: hello.Me.Client, UserID: u.ID, CanEdit: hello.Me.CanEdit}, coeditTicketTTL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	// A person who may only read the file follows along (filex 0.56): the
	// relay refuses their writes, and the audit says they watched.
	action := AuditActionCoEditJoin
	if !hello.Me.CanEdit {
		action = AuditActionCoEditWatch
	}
	h.audit(r, p, action, map[string]any{
		"storage_id": storageID, "path": rel, "session": hello.SID, "client": hello.Me.Client,
		"can_edit": hello.Me.CanEdit, "created": hello.Created, "e2e": keyMode == coedit.KeyFolder,
	})
	opts := h.Relay.Options()
	out := map[string]any{
		"session":       hello.SID,
		"doc":           doc,
		"owner":         coeditOwner(who, doc),
		"created":       hello.Created,
		"me":            coeditWireMember(hello.Me),
		"head":          hello.Head,
		"changes_head":  hello.ChangesHead,
		"saved_through": hello.SavedThrough,
		"key_mode":      hello.KeyMode,
		"token":         token,
		"socket":        map[string]string{"url": h.socketURL(r), "ticket": ticket},
		"limits": map[string]any{
			"max_entry_bytes": opts.MaxEntryBytes,
			"max_eph_bytes":   coedit.MaxEphBytes,
			"max_blob_bytes":  opts.MaxBlobBytes,
			"lease_ms":        opts.LeaseTTL.Milliseconds(),
			"member_ttl_ms":   opts.MemberTTL.Milliseconds(),
		},
	}
	if hello.Key != nil {
		out["key"] = base64.StdEncoding.EncodeToString(hello.Key)
	}
	if hello.KeyMode == coedit.KeyFolder {
		out["sealed_key"] = hello.SealedKey
	}
	if body.Unsent != nil && body.Unsent.Count > 0 {
		out["unsent_words"] = coeditUnsentWords(langOf(r), body.Unsent.Count, path.Base(rel))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *CoEdit) socketURL(r *http.Request) string {
	if h.WS == nil {
		return "/api/coedit/ws"
	}
	return h.WS.socketURL(r, "/api/coedit/ws")
}

// member resolves the session a request names ({sid}) and the member it
// speaks for (client + token), checking it is the caller's own and this
// app's; on failure the answer is written.
func (h *CoEdit) member(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, client, token string) (*model.CoEditSession, *model.CoEditMember, bool) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", nil)
		return nil, nil, false
	}
	sid := chi.URLParam(r, "sid")
	m, err := h.Relay.Authorise(r.Context(), sid, client, token, u.ID)
	if err != nil {
		writeCoEditError(w, r, err)
		return nil, nil, false
	}
	s, err := h.Relay.Session(r.Context(), sid)
	if err != nil {
		writeCoEditError(w, r, err)
		return nil, nil, false
	}
	if s.App != p.Row.Name {
		// Another app's session is not this app's to touch: the same answer
		// as a session that is not there.
		writeCoEditError(w, r, coedit.ErrNoSession)
		return nil, nil, false
	}
	return s, m, true
}

type coeditCaller struct {
	Client string `json:"client"`
	Token  string `json:"token"`
}

// Append places a member's sealed entry.
//
//	POST …/coedit/{sid}/append {client, token, kind, base, ctr, ct: "<base64>"}
//	→ 200 {seq} · 409 coedit_conflict {head} · 409 coedit_no_lease · 403 coedit_read_only
func (h *CoEdit) Append(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	var body struct {
		coeditCaller
		Kind string `json:"kind"`
		Base uint64 `json:"base"`
		Ctr  uint64 `json:"ctr"`
		CT   []byte `json:"ct"`
	}
	limit := int64(h.Relay.Options().MaxEntryBytes)*4/3 + 4096
	if !readCoEditBody(w, r, limit, &body) {
		return
	}
	s, _, ok := h.member(w, r, p, body.Client, body.Token)
	if !ok {
		return
	}
	seq, err := h.Relay.Append(r.Context(), s.SID, body.Client, coedit.AppendRequest{
		Kind: coedit.Kind(body.Kind), Base: body.Base, Ctr: body.Ctr, CT: body.CT,
	})
	if err != nil {
		writeCoEditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seq": seq})
}

// Lease takes, renews or gives back the changes lease.
//
//	POST …/coedit/{sid}/lease {client, token, op: acquire|release, changes_seen} → 200 {granted}
func (h *CoEdit) Lease(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	var body struct {
		coeditCaller
		Op          string `json:"op"`
		ChangesSeen uint64 `json:"changes_seen"`
	}
	if !readCoEditBody(w, r, 4<<10, &body) {
		return
	}
	s, _, ok := h.member(w, r, p, body.Client, body.Token)
	if !ok {
		return
	}
	granted, err := h.Relay.Lease(r.Context(), s.SID, body.Client, coedit.LeaseOp(body.Op), body.ChangesSeen)
	if err != nil {
		writeCoEditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"granted": granted})
}

// Key stores the sealed key of an encrypted folder's session (filex 0.56):
// the member that started it made the key in its page and sealed it with the
// folder key; the server keeps it as it is and cannot open it. Once.
//
//	POST …/coedit/{sid}/key {client, token, sealed} → 200 {stored: true}
func (h *CoEdit) Key(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	var body struct {
		coeditCaller
		Sealed string `json:"sealed"`
	}
	if !readCoEditBody(w, r, 16<<10, &body) {
		return
	}
	s, _, ok := h.member(w, r, p, body.Client, body.Token)
	if !ok {
		return
	}
	if err := h.Relay.SetKey(r.Context(), s.SID, body.Client, body.Sealed); err != nil {
		writeCoEditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stored": true})
}

// coeditOwner is the opaque id of the person for this file, the same in
// every session of it: the explorer keeps the changes that did not reach
// the relay under it (lib/coeditOutbox), so another account in the same
// browser is never offered them. Not an account id.
func coeditOwner(who, doc string) string {
	sum := sha256.Sum256([]byte("filex-coedit-owner:" + who + ":" + doc))
	return hex.EncodeToString(sum[:16])
}

// coeditUnsentWords is what the explorer asks a person who has changes of an
// earlier opening that never reached the relay: in the reader's language,
// from the server (the question for a session others changed since, the one
// for a session that ended, and the two answers).
func coeditUnsentWords(lang string, count int, name string) map[string]string {
	vars := srvtext.Vars{"name": name}
	return map[string]string{
		"behind":  srvtext.Plural(lang, "server.coedit.unsent.behind", count, vars),
		"ended":   srvtext.Plural(lang, "server.coedit.unsent.ended", count, vars),
		"apply":   srvtext.Text(lang, "server.coedit.unsent.apply", nil),
		"discard": srvtext.Text(lang, "server.coedit.unsent.discard", nil),
	}
}

// Ticket mints a fresh socket ticket for a member (its socket dropped).
//
//	POST …/coedit/{sid}/ticket {client, token} → 200 {url, ticket}
func (h *CoEdit) Ticket(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	var body coeditCaller
	if !readCoEditBody(w, r, 4<<10, &body) {
		return
	}
	s, m, ok := h.member(w, r, p, body.Client, body.Token)
	if !ok {
		return
	}
	ticket, err := h.tickets.Mint(coeditTicket{SID: s.SID, Client: m.Client, UserID: m.UserID, CanEdit: m.CanEdit}, coeditTicketTTL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": h.socketURL(r), "ticket": ticket})
}

// Leave takes a member out of the session (its page closed the editor).
//
//	POST …/coedit/{sid}/leave {client, token} → 200 {left: true}
func (h *CoEdit) Leave(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	var body coeditCaller
	if !readCoEditBody(w, r, 4<<10, &body) {
		return
	}
	s, _, ok := h.member(w, r, p, body.Client, body.Token)
	if !ok {
		return
	}
	if err := h.Relay.Leave(r.Context(), s.SID, body.Client, "left"); err != nil && !errors.Is(err, coedit.ErrNotMember) {
		writeCoEditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"left": true})
}

// PutBlob stores a sealed blob of the session, once per name.
//
//	PUT …/coedit/{sid}/blob/{name}?client=&token=  (body: the sealed bytes) → 201 {size}
func (h *CoEdit) PutBlob(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	s, m, ok := h.member(w, r, p, q.Get("client"), q.Get("token"))
	if !ok {
		return
	}
	max := h.Relay.Options().MaxBlobBytes
	if r.ContentLength > max {
		writeError(w, r, http.StatusRequestEntityTooLarge, "coedit_too_large", nil)
		return
	}
	n, err := h.Relay.PutBlob(r.Context(), s.SID, m.Client, chi.URLParam(r, "name"), http.MaxBytesReader(w, r.Body, max+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			err = coedit.ErrTooLarge
		}
		writeCoEditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"size": n})
}

// GetBlob reads a sealed blob of the session.
//
//	GET …/coedit/{sid}/blob/{name}?client=&token= → 200 (the sealed bytes)
func (h *CoEdit) GetBlob(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.app(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	s, m, ok := h.member(w, r, p, q.Get("client"), q.Get("token"))
	if !ok {
		return
	}
	body, size, err := h.Relay.Blob(r.Context(), s.SID, m.Client, chi.URLParam(r, "name"))
	if err != nil {
		writeCoEditError(w, r, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// The audit actions of joining a session (an editor, or a watcher who may
// only read). Constants: the audit panel's label check
// (web/tests/lib/auditLabel.test.ts) reads AuditAction constants and Action
// literals, and these two are chosen at run time.
const (
	AuditActionCoEditJoin  = "app_plugin.coedit_join"
	AuditActionCoEditWatch = "app_plugin.coedit_watch"
)

func (h *CoEdit) audit(r *http.Request, p *wasmplugin.Installed, action string, meta map[string]any) {
	var uid *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		uid = &id
	}
	_ = h.Apps.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID: uid, Action: action, TargetType: "app_plugin", TargetID: p.Row.Name, Metadata: meta, IP: clientIP(r),
	})
}

// coeditSaved records, after an interface saved over the file it was opened
// with, which part of the session's log the save holds:
// `…/save?path=…&coedit=<sid>&client=&token=&through=<seq>`. The file's
// signature after the write is the server's own reading; a request that is
// not the member's, or a session of another file or app, records nothing.
// The save itself stands either way (it is written already).
func (h *AppPlugins) coeditSaved(r *http.Request, p *wasmplugin.Installed, drv storage.Driver, storageID int64, rel string) {
	q := r.URL.Query()
	sid := q.Get("coedit")
	if h.CoEditRelay == nil || sid == "" {
		return
	}
	through, err := strconv.ParseUint(q.Get("through"), 10, 64)
	if err != nil {
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		return
	}
	ctx := r.Context()
	m, err := h.CoEditRelay.Authorise(ctx, sid, q.Get("client"), q.Get("token"), u.ID)
	if err != nil {
		return
	}
	s, err := h.CoEditRelay.Session(ctx, sid)
	if err != nil || s.App != p.Row.Name || s.StorageID != storageID || s.Path != rel {
		return
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return
	}
	if _, err := h.CoEditRelay.Saved(ctx, sid, m.Client, through, coeditSig(&obj)); err != nil {
		slog.Warn("coedit: a save was written but not recorded in its session",
			slog.String("sid", sid), slog.String("client", m.Client), slog.String("err", err.Error()))
		return
	}
	_ = h.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID: &u.ID, Action: "app_plugin.coedit_saved", TargetType: "app_plugin", TargetID: p.Row.Name,
		Metadata: map[string]any{"storage_id": storageID, "path": rel, "session": sid, "client": m.Client, "through": through},
		IP:       clientIP(r),
	})
}

// coeditSaveHolds is a save's `expect` in an editing session (filex 0.56).
// A member's page cannot know the date another member's save gave the file,
// so the session answers instead: the file must still be what the session
// holds - the version it started from or its last save - or another version
// was written outside it, and the save is refused (`changed`). inSession is
// false when the save names no session of this file (or not the member's
// own): the caller asks `expect` as before.
func (h *AppPlugins) coeditSaveHolds(r *http.Request, p *wasmplugin.Installed, drv storage.Driver, storageID int64, rel string) (inSession, holds bool) {
	q := r.URL.Query()
	sid := q.Get("coedit")
	if h.CoEditRelay == nil || sid == "" {
		return false, false
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		return false, false
	}
	ctx := r.Context()
	if _, err := h.CoEditRelay.Authorise(ctx, sid, q.Get("client"), q.Get("token"), u.ID); err != nil {
		return false, false
	}
	s, err := h.CoEditRelay.Session(ctx, sid)
	if err != nil || s.App != p.Row.Name || s.StorageID != storageID || s.Path != rel {
		return false, false
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return true, false
	}
	current := s.BaseSig
	if s.SavedSig != "" {
		current = s.SavedSig
	}
	return true, coeditSig(&obj) == current
}

// ---------------------------------------------------------------------------
// The socket
// ---------------------------------------------------------------------------

// coeditOut is a frame the socket sends.
type coeditOut struct {
	Type   string        `json:"type"`
	Entry  *coedit.Entry `json:"entry,omitempty"`
	Client string        `json:"client,omitempty"`
	Ctr    uint64        `json:"ctr,omitempty"`
	CT     []byte        `json:"ct,omitempty"`
	Reason string        `json:"reason,omitempty"`
	Head   uint64        `json:"head,omitempty"`
}

// coeditIn is a frame the socket reads.
type coeditIn struct {
	Type string `json:"type"` // subscribe | cursor | ping
	From uint64 `json:"from"`
	Ctr  uint64 `json:"ctr"`
	CT   []byte `json:"ct"`
}

// Socket is a member's live connection to its session: after
// `{"type":"subscribe","from":N}` it sends every entry after N in order, each
// once (`{"type":"entry","entry":…}`), then each new one as it lands; the
// other members' cursors (`{"type":"eph",…}`); and `{"type":"ended",
// "reason":…}` when the membership ends. It takes the member's own cursors
// (`{"type":"cursor","ctr":…,"ct":…}`, at most ten a second) and pings.
//
//	GET /api/coedit/ws?ticket=<from the join or …/ticket>
//
// While it is open, the member is kept (Touch, every 15 s) and the person's
// rights on the file are read again: a person who may no longer read it is
// taken out (and one who may no longer change it, if it may write).
func (h *CoEdit) Socket(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Relay == nil {
		http.Error(w, "editing together is off", http.StatusServiceUnavailable)
		return
	}
	t, ok := h.tickets.Consume(r.URL.Query().Get("ticket"))
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}
	baseCtx := context.WithoutCancel(r.Context())
	u, err := h.Apps.Store.GetUser(baseCtx, t.UserID)
	if err != nil || u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	baseCtx = auth.WithUser(baseCtx, u)
	if h.WS != nil && h.WS.MultiTenant {
		baseCtx = tenant.WithScope(baseCtx, auth.ScopeForUser(baseCtx, h.Apps.Store, u))
	}
	if m, err := h.Relay.Member(baseCtx, t.SID, t.Client); err != nil || m.UserID != t.UserID {
		http.Error(w, "not a member", http.StatusGone)
		return
	}
	// The ticket is the authentication (it was minted for this member on an
	// authenticated request), and the explorer is embedded in other sites:
	// no same-origin check, as for the realtime socket's tickets.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(int64(coedit.MaxEphBytes)*2 + 4096)

	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()
	watcher := h.Relay.Watch(t.SID, t.Client)
	defer watcher.Close()
	_ = h.Relay.Touch(ctx, t.SID, t.Client)

	in := make(chan coeditIn, 8)
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var m coeditIn
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			select {
			case in <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	send := func(f coeditOut) bool {
		b, err := json.Marshal(f)
		if err != nil {
			return false
		}
		wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, b) == nil
	}
	var (
		last       uint64
		subscribed bool
		lastCursor time.Time
		// The other members' cursor counters this socket handed over: a
		// cursor reaches it from this process (watcher.Eph) and from the
		// database (cursors, for another process's members); each is sent
		// once, and only a newer one.
		cursorSeen = map[string]uint64{}
	)
	sendCursor := func(e coedit.Eph) bool {
		if e.Client == t.Client || e.Ctr <= cursorSeen[e.Client] {
			return true
		}
		cursorSeen[e.Client] = e.Ctr
		return send(coeditOut{Type: "eph", Client: e.Client, Ctr: e.Ctr, CT: e.CT})
	}
	// cursors reads the members' latest cursors from the database: the
	// members connected to another filex process on the same database
	// (coedit cursors.go). One row per member, so the whole set is read.
	cursors := func() bool {
		es, err := h.Relay.Cursors(ctx, t.SID, 0)
		if err != nil {
			return true
		}
		for _, e := range es {
			if !sendCursor(e) {
				return false
			}
		}
		return true
	}
	// deliver sends what the log holds after `last`, in pages.
	deliver := func() bool {
		for {
			es, err := h.Relay.Entries(ctx, t.SID, last, 256)
			if err != nil {
				return false
			}
			for i := range es {
				e := es[i]
				if e.Seq != last+1 {
					// Never skip: the reader would take a hole for a forgery.
					send(coeditOut{Type: "ended", Reason: "broken"})
					return false
				}
				if !send(coeditOut{Type: "entry", Entry: &e}) {
					return false
				}
				last = e.Seq
			}
			if len(es) < 256 {
				return true
			}
		}
	}
	poll := time.NewTicker(h.Relay.PollEvery())
	defer poll.Stop()
	touch := time.NewTicker(coeditTouchEvery)
	defer touch.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-in:
			switch m.Type {
			case "subscribe":
				s, err := h.Relay.Session(ctx, t.SID)
				if err != nil {
					send(coeditOut{Type: "ended", Reason: "closed"})
					return
				}
				if m.From > uint64(s.Head) {
					send(coeditOut{Type: "error", Reason: "from_ahead", Head: uint64(s.Head)})
					continue
				}
				last, subscribed = m.From, true
				// The log after `from`, and where the others' cursors are.
				if !deliver() || !cursors() {
					return
				}
			case "cursor":
				if !t.CanEdit || time.Since(lastCursor) < coeditCursorGap {
					continue
				}
				lastCursor = time.Now()
				_ = h.Relay.SendEph(t.SID, coedit.Eph{Client: t.Client, Ctr: m.Ctr, CT: m.CT})
			case "ping":
				_ = h.Relay.Touch(ctx, t.SID, t.Client)
				if !send(coeditOut{Type: "pong"}) {
					return
				}
			}
		case <-watcher.Notify:
			if subscribed && !deliver() {
				return
			}
		case <-poll.C:
			if subscribed && (!deliver() || !cursors()) {
				return
			}
		case e := <-watcher.Eph:
			if !sendCursor(e) {
				return
			}
		case why := <-watcher.Ended:
			if subscribed {
				deliver()
			}
			send(coeditOut{Type: "ended", Reason: why})
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-touch.C:
			if err := h.Relay.Touch(ctx, t.SID, t.Client); err != nil {
				send(coeditOut{Type: "ended", Reason: "gone"})
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			if !h.stillAllowed(ctx, t) {
				_ = h.Relay.Leave(ctx, t.SID, t.Client, "revoked")
			}
		}
	}
}

// stillAllowed reads the person's rights on the session's file again: read,
// and change when the member may write.
func (h *CoEdit) stillAllowed(ctx context.Context, t coeditTicket) bool {
	s, err := h.Relay.Session(ctx, t.SID)
	if err != nil {
		return false
	}
	st, err := h.Apps.Store.GetStorage(ctx, s.StorageID)
	if err != nil || st == nil || !st.Enabled {
		return false
	}
	if !aclAllowName(ctx, h.Apps.ACL, h.Apps.Store, st.Name, s.Path, acl.LevelViewer) {
		return false
	}
	if t.CanEdit && (st.ReadOnly || !aclCanID(ctx, h.Apps.ACL, h.Apps.Store, s.StorageID, s.Path, perm.FilesModify).ok) {
		return false
	}
	return true
}
