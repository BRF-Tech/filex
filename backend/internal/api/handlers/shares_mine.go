// Package handlers — shares_mine.go
//
// "Paylaştıklarım" / "My shares" — the USER-scoped half of the share surface,
// and the one audited door to a link's PIN.
//
//	GET /api/shares           the links the caller created (paginated)
//	GET /api/shares/{id}/pin  that link's PIN, to its creator or to an admin
//
// # Why this file exists
//
// Until now the only list of shares was /api/admin/shares behind the admin
// panel, so somebody without admin rights could not see their own links at
// all: they minted a link from the explorer, the dialog showed it once, and
// after that the only way back to it was to mint another one. Every other
// self-service credential surface in this product (API tokens, S3 keys, SSH
// keys, NFS exports) already has this half. Shares did not.
//
// ⚠ This is NOT a second admin listing. It reads the SAME store query
// (ListAllShares) with the caller pinned as the creator, the same tenant
// filter and the same row envelope. What it drops is the part that is about
// everybody rather than about the caller — the creator's address, which on
// this screen is always the person reading it.
package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// AuditActionPinReveal is the audit row every PIN read writes — success or
// refusal-after-authorization alike. Exported so a test can name it rather
// than repeat the string, and so an operator grepping the audit table has one
// spelling to grep for.
const AuditActionPinReveal = share.AuditActionPinReveal

// SharesMine handles the caller's own shares.
type SharesMine struct {
	Store db.Store
	// Service opens the sealed PIN (share.RevealPIN). Nil-safe: without it the
	// PIN endpoint answers "cannot be shown" rather than panicking, which is
	// also what an instance with no secret key gets.
	Service *share.Service
	// Tenants resolves the origin the listed links are built on — the same
	// resolver the share dialog and the admin listing use, never
	// window.location (issue #32).
	Tenants tenanturl.Resolver
	// Apps describes an app's link for what it is (db.AppLink). Nil when
	// app plugins are off: every row then lists as a plain share.
	Apps *wasmplugin.Registry
}

// AttachApps wires the app-plugin registry.
func (h *SharesMine) AttachApps(reg *wasmplugin.Registry) { h.Apps = reg }

// NewSharesMine constructs the handler.
func NewSharesMine(store db.Store, svc *share.Service) *SharesMine {
	return &SharesMine{Store: store, Service: svc}
}

// AttachTenants wires the shared per-request origin resolver (internal/tenanturl).
func (h *SharesMine) AttachTenants(rv tenanturl.Resolver) { h.Tenants = rv }

// List returns the shares the CALLER created, newest first.
//
// ⚠ The creator filter is applied in the STORE (`created_by = me`), not by
// dropping rows after the fact. A post-filter would have to paginate over
// everybody's links to fill one person's page, which means the total would be
// wrong and, worse, that the query the database ran was the admin one.
func (h *SharesMine) List(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	q := r.URL.Query()
	limit, offset := sharePaging(q)
	activeOnly := q.Get("active") == "true"

	me := user.ID
	rows, total, err := h.Store.ListAllShares(r.Context(), &me, activeOnly, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Multi-tenant: the same storage-name filter the admin listing applies.
	// A person's own link on a storage their tenant can no longer reach is
	// not theirs to manage from here.
	if kept, narrowed := sharesInTenant(r, h.Store, rows); narrowed {
		rows, total = kept, int64(len(kept))
	}
	// Root confinement: a `root:`-scoped token may only see the links inside
	// its folder. The row carries the TOKEN, so a link outside the root is a
	// working public link to a file the caller cannot otherwise reach — the
	// same reasoning as handlers.Share.HandleList, where it is already a fix.
	if root, confined := confine.RootFrom(r.Context()); confined {
		kept := rows[:0]
		for _, row := range rows {
			if row != nil && root.Within(row.StorageName, row.NodePath) {
				kept = append(kept, row)
			}
		}
		rows, total = kept, int64(len(kept))
	}
	if rows == nil {
		rows = []*db.ShareWithMeta{}
	}
	base := h.Tenants.FromRequest(r)
	for _, row := range rows {
		if row == nil || row.Share == nil {
			continue
		}
		// ⚠ The CREATOR's address is cleared. On this screen it is always the
		// person reading it, and `omitempty` then keeps it out of the body
		// entirely — a user-scoped listing that names an account is a listing
		// that can be made to name accounts.
		row.CreatorEmail = ""
		if row.Share.Token != "" {
			row.URL = base + shareLinkPath(row.Share) + row.Share.Token
		}
		// A signing link is a signing request, not a plain share of the
		// file (the owner's decision, 2026-09-21): say so, and where it lives.
		if h.Apps != nil {
			row.App = h.Apps.LinkOf(row.Share)
		}
	}
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     rows,
		"total":     total,
		"page":      (offset / pageSize) + 1,
		"page_size": pageSize,
	})
}

// Pin hands back ONE link's PIN, in plain, to the person who created it or to
// an administrator — and to nobody else.
//
// Owner's decision, 2026-09-20: *"paylaşımın sahibi ve admin alabilir
// şifreyi… Admin değilse göremez, kopyalayamaz."*
//
// # The shape of the answer
//
// An authorized caller always gets 200, with a body that either carries the
// PIN or says in one machine-readable word why it cannot:
//
//	{"pin":"834595"}
//	{"pin":null,"reason":"no_pin"}            this link has no PIN
//	{"pin":null,"reason":"not_recoverable"}   minted before 00049 / key rotated
//	{"pin":null,"reason":"no_secret_key"}     this instance has no FILEX_SECRET_KEY
//
// The three refusals are 200 rather than an error status because they are not
// failures of the request: the caller is allowed, the link exists, and the
// honest answer is a sentence the screen must show. An error status here would
// be indistinguishable — to a client's interceptor — from "you may not".
//
// # Why every read is audited
//
// A PIN is the second factor on a public link. Reading one is not like reading
// a row of a table, and the audit row is what makes "the owner and an admin"
// a statement somebody can CHECK afterwards rather than a claim about code.
// The row is written whatever the answer was, because an attempt that found
// nothing is still an attempt.
func (h *SharesMine) Pin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sh, err := h.Store.GetShareByID(r.Context(), id)
	if err != nil || sh == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// ⚠⚠ The tenant boundary is checked BEFORE the admin short-cut below, and
	// an admin does NOT skip it. Under multi-tenancy "an admin" is the admin of
	// every tenant, and that exact early-out is the bug HandleDelete documents:
	// it handed every share on the instance to any tenant's administrator.
	// Refusal is the 404 an unknown id already produces, so a foreign id cannot
	// be told apart from one that was never minted.
	if scope, confined := confinedScope(r.Context()); confined {
		n, nerr := h.Store.GetNode(r.Context(), sh.NodeID)
		if nerr != nil || n == nil || !scope.CanAccessStorage(n.StorageID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
	}
	if _, confined := confine.RootFrom(r.Context()); confined {
		n, nerr := h.Store.GetNode(r.Context(), sh.NodeID)
		if nerr != nil || n == nil || !rootAllows(r.Context(), h.Store, n.StorageID, n.Path) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
	}
	owner := sh.CreatedBy != nil && *sh.CreatedBy == user.ID
	if !owner && !user.IsAdmin() {
		// 403, not 404: inside their own tenant the caller is allowed to know
		// the link exists — this is the same distinction HandleDelete draws,
		// and it is what makes "you are not the owner" readable instead of
		// looking like a broken link.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	pin, reason := "", ""
	switch revealed, rerr := h.reveal(sh); {
	case rerr == nil:
		pin = revealed
	case errors.Is(rerr, share.ErrNoPIN):
		reason = "no_pin"
	case errors.Is(rerr, share.ErrNoSecretKey):
		reason = "no_secret_key"
	default:
		reason = "not_recoverable"
	}
	h.auditReveal(r, sh, user, owner, pin != "", reason)

	// ⚠ Never cached, anywhere. This is a GET carrying a secret, and a GET is
	// exactly what a browser, a proxy and a service worker all feel entitled to
	// keep.
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	body := map[string]any{"pin": nil}
	if pin != "" {
		body["pin"] = pin
	} else {
		body["reason"] = reason
	}
	writeJSON(w, http.StatusOK, body)
}

// reveal is the nil-safe call into the share service.
func (h *SharesMine) reveal(sh *model.Share) (string, error) {
	if h.Service == nil {
		// No service wired (a hand-built handler in a test, a bootstrap that
		// skipped it). "Cannot be shown" is true and says nothing false.
		return "", share.ErrPinNotRecoverable
	}
	return h.Service.RevealPIN(sh)
}

// auditReveal writes the trail row for one PIN read.
//
// ⚠ Neither the PIN nor the share TOKEN is in it. The PIN for the obvious
// reason; the token because a live public link sitting in an audit table is a
// live public link for everybody who can read audit rows (the same choice
// PublicAPI.auditPinLock made). `target_id` is the share's row id, which is
// what an operator follows back to the link.
func (h *SharesMine) auditReveal(r *http.Request, sh *model.Share, user *model.User, owner, revealed bool, reason string) {
	if h.Store == nil {
		return
	}
	uid := user.ID
	meta := map[string]any{
		"revealed": revealed,
		// WHICH principal was used matters more than who: "the owner read
		// their own PIN" and "an administrator read somebody else's" are
		// different events and the audit table should not make a reader
		// reconstruct which one this was.
		"as_admin": !owner,
		"kind":     sh.Kind,
	}
	if reason != "" {
		meta["reason"] = reason
	}
	if tok := auth.TokenFrom(r.Context()); tok != nil && tok.ID > 0 {
		meta["token_id"] = tok.ID
		if tu := auth.TokenUserFrom(r.Context()); tu != "" {
			meta["token_username"] = tu
		}
	}
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID:     &uid,
		Action:     AuditActionPinReveal,
		TargetType: "share",
		TargetID:   strconv.FormatInt(sh.ID, 10),
		Metadata:   meta,
		IP:         clientIP(r),
		CreatedAt:  time.Now(),
	})
}

// shareLinkPath is the public prefix a link of this kind is served under — a
// download share is /s/, a file request is /d/.
func shareLinkPath(sh *model.Share) string {
	if sh.IsDrop() {
		return "/d/"
	}
	return "/s/"
}

// sharePaging reads the `limit`/`offset` pair BOTH share listings accept, with
// the same ceiling and the same fallbacks. One reader, because two listings
// that disagree about what `limit=0` means is a paginator that works on one
// page of the product and not the other.
func sharePaging(q url.Values) (limit, offset int) {
	limit, offset = 50, 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

// sharesInTenant drops the rows whose storage the caller's tenant cannot
// reach, reporting whether it narrowed anything (docs/MULTI-TENANCY.md §9).
//
// The scoped store's ListStorages is already tenant-confined, so its name set
// IS the allowed set; a row whose storage name does not resolve stays hidden
// (fail closed). Shared by both share listings so the admin page and the
// person's own page cannot come to disagree about what a tenant may see.
func sharesInTenant(r *http.Request, store db.Store, rows []*db.ShareWithMeta) ([]*db.ShareWithMeta, bool) {
	scope, ok := tenant.FromContext(r.Context())
	if !ok || scope.IsSupertenant {
		return rows, false
	}
	allowed := map[string]bool{}
	if sts, err := store.ListStorages(r.Context()); err == nil {
		for _, st := range sts {
			allowed[st.Name] = true
		}
	}
	kept := rows[:0]
	for _, row := range rows {
		if row != nil && row.StorageName != "" && allowed[row.StorageName] {
			kept = append(kept, row)
		}
	}
	return kept, true
}
