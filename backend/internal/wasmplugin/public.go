package wasmplugin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── An app's public page IS a share ────────────────────────────────────
//
// A plugin running an action for a signed-in user may open a link for
// somebody who has no filex account (the person asked to sign a document).
// From v3 that link is a REAL SHARE — the same row, the same /s/<token>
// address, the same Shares list the administrator revokes from.
//
// Why a share and not a look-alike: one revoke list, one expiry policy, one
// PIN implementation to get right, one visit counter, one set of audit rows.
// v2 had a second table (app_plugin_pages, migration 00043) with its own copy
// of every one of those, and an administrator could not see a signature
// request in **Shares** because it was not one.
//
// What is still the plugin's, and nothing else is:
//
//   - `plugin_id` / `page_id` — which app answers this link and which page
//     of its manifest it renders;
//   - `state_json` — the app's own durable record for this link;
//   - the exposed file COPIES: the only bytes an anonymous visitor reaches
//     through a plugin surface. No storage driver is opened for a page event,
//     exactly as before.
//
// Everything else comes from the share machine: token (`shares.token`), PIN
// (`pin_hash` + the strike counter below), expiry (`expires_at`; revoke sets
// it to now), the visit ceiling (`max_downloads`) and the visit counter
// (`visit_count`, 00052 — `download_count` counts only the exposed files
// actually fetched). The job a visitor's surface asks for still runs as the
// share's creator, with that user's ACL.

const (
	pagePinLength     = 6
	pageMaxFiles      = 16
	pageMaxFileBytes  = 64 << 20
	pageMaxTotalBytes = 128 << 20
	pageMaxStateBytes = 64 << 10
	pageDefaultTTL    = 30 // days
	pageMaxTTL        = 365
	pageSweepAfter    = 7 * 24 * time.Hour
)

// PageFile is one exposed copy, as files_json stores it.
//
// ⚠ `File` is a BASENAME inside the share's own directory, not an absolute
// path. v2 stored the absolute path, so moving or restoring the data
// directory silently broke every exposed copy while the row looked healthy.
type PageFile struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
	File string `json:"file"`
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

// Page errors the HTTP layer maps to status codes. The PIN-gate errors are
// internal/share's (ErrBadPIN / ErrLocked): there is one gate now.
var (
	ErrPageNotFound = errors.New("page not found")
	ErrPageGone     = errors.New("page is no longer available")
)

func (r *Registry) publicRoot() string { return filepath.Join(r.opts.Dir, "public") }

// pageDir is where one share's exposed copies live. Keyed by the share's ROW
// ID rather than by a token hash, so the sweeper can decide a directory's fate
// from its name alone and no secret ends up in a path.
func (r *Registry) pageDir(shareID int64) string {
	return filepath.Join(r.publicRoot(), strconv.FormatInt(shareID, 10))
}

// PublicURL builds the visitor link; base is the instance's public URL.
func PublicURL(base, token string) string {
	return strings.TrimRight(base, "/") + "/s/" + token
}

// SetPublicURL records the base the visitor link is built on. It is the
// fallback; SetOrigins is what a multi-tenant install answers with.
func (r *Registry) SetPublicURL(base string) { r.publicBase = strings.TrimRight(base, "/") }

// SetOrigins records how an absolute link's origin is resolved when NO request
// is in scope — a plugin job runs on the queue, long after the browser left.
// The tenant then comes from the data (the share's storage), never from a
// guess: internal/tenanturl.Resolver.ForStorage is what is wired here. Without
// it a customer's signature request would carry the OPERATOR's hostname.
func (r *Registry) SetOrigins(fn func(ctx context.Context, storageID int64) string) {
	r.originFor = fn
}

// origin answers the base a link for storageID is built on.
func (r *Registry) origin(ctx context.Context, storageID int64) string {
	if r.originFor != nil {
		if base := r.originFor(ctx, storageID); base != "" {
			return base
		}
	}
	return r.publicBase
}

// linkCeiling is what every call input tells an app about the life of the
// links it can open: the instance's share ceiling, read from the SAME place
// share.Service.Create clamps with, so what a screen offers and what a link
// gets cannot come from two different numbers. nil for an app without the
// `public_pages` grant — it opens no links, so it is told nothing about them.
func (r *Registry) linkCeiling(ctx context.Context, p *Installed) *int {
	if r.opts.Share == nil || p == nil || !p.Grants.Has(PermPublicPages) {
		return nil
	}
	n := r.opts.Share.MaxTTLDays(ctx)
	return &n
}

// ── host functions ─────────────────────────────────────────────────────

type pageCreateReq struct {
	PageID string `json:"page_id"`
	// Ref / Path name the document the outsider sees. Empty means the call's
	// first input, which is what every v2 plugin relied on.
	Ref       string           `json:"ref"`
	Path      string           `json:"path"`
	Subject   string           `json:"subject"`
	PIN       string           `json:"pin"` // "auto" | "<digits>" | ""
	TTLDays   int              `json:"ttl_days"`
	MaxVisits int              `json:"max_visits"`
	State     json.RawMessage  `json:"state"`
	Files     []wire.OutputRef `json:"files"`
	// Purpose: what this link is, for a list of links (pluginkit.PageCreate).
	Purpose *wire.PagePurpose `json:"purpose"`
}

// hfShareCreate implements share_create (and the v1/v2 name
// public_page_create). It opens a REAL share — with one of this plugin's
// pages behind it, or with nothing behind it at all.
//
// ⚠ TWO SHAPES, ONE ROW:
//
//   - `page_id` names a page the manifest DECLARES → the link renders that
//     app screen and hands over the copies in `files`.
//   - `page_id` EMPTY → an ordinary download link of the document: no
//     surface, no copies, the node's own bytes. `IsApp()` is false for such a
//     row, so the public shell, the no-JS page and the administrator's Shares
//     list all treat it as exactly what it is — a share. It is how an app
//     hands a finished file to somebody with no account (the signed document
//     at the end of a signature round) without a second link machine being
//     invented for it.
//
// A `page_id` that names a page the manifest does NOT declare stays an error:
// that is a plugin asking for a screen nobody approved at install. Only "no
// page at all" stopped being one.
func hfShareCreate(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req pageCreateReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "public links are created from action jobs only")
	}
	if s.reg.opts.Share == nil {
		return nil, hostErr(wire.ErrInternal, "share service is not wired")
	}
	var spec *wire.PublicPage
	if req.PageID != "" {
		for i := range s.plugin.Manifest.PublicPages {
			if s.plugin.Manifest.PublicPages[i].ID == req.PageID {
				spec = &s.plugin.Manifest.PublicPages[i]
			}
		}
		if spec == nil {
			return nil, hostErr(wire.ErrInvalid, "the manifest declares no public page "+req.PageID)
		}
	}
	if err := checkPurpose(req.Purpose, s.plugin.Manifest.Languages); err != nil {
		return nil, hostErr(wire.ErrInvalid, err.Error())
	}
	if len(req.State) > pageMaxStateBytes {
		return nil, hostErr(wire.ErrTooLarge, "state over 64 KiB")
	}
	if len(req.Files) > pageMaxFiles {
		return nil, hostErr(wire.ErrTooLarge, "too many files for one link")
	}
	// ⚠⚠ An exposed COPY belongs to a SURFACE, and a page-less link has none:
	// it streams the node it points at, so a copy handed to one would be bytes
	// nobody could ever reach — and the visitor would be sent the DOCUMENT
	// while the plugin believed it had sent something else. For a delivery
	// link that is the worst failure there is, so `files` is REFUSED here
	// rather than quietly dropped. One exception: a SINGLE copy naming a file
	// this call knows — an input, or an output the job is writing — is the
	// document said a second way, and is honoured as `ref` instead of refused.
	if spec == nil && len(req.Files) > 0 {
		promoted := ""
		if len(req.Files) == 1 && req.Ref == "" && strings.TrimSpace(req.Path) == "" {
			if f, ok := s.file(req.Files[0].Ref); ok && (f.Rel != "" || f.Output) {
				promoted = req.Files[0].Ref
			}
		}
		if promoted == "" {
			return nil, hostErr(wire.ErrInvalid, "a link without a page hands over no copies — it serves the one file it points at. Name that file with ref or path: one of this job's inputs, or one of the outputs it is writing")
		}
		req.Ref, req.Files = promoted, nil
	}

	// The document the link is about. It has to be a node filex knows, because
	// a share points at one — and because that is what makes the link show up
	// on the file's own Shares list and under the administrator's eye.
	//
	// ⚠ …with ONE exception, and it is the whole of public_promised.go: a ref
	// naming an OUTPUT of this very job names a file that has no node YET and
	// will have one in a moment. That link is promised here and written when
	// the bytes are committed.
	rel := strings.TrimSpace(req.Path)
	outRef := ""
	if req.Ref != "" {
		f, ok := s.file(req.Ref)
		if !ok {
			// ⚠ Never fall through to "the first input". A ref the plugin
			// believes in and the host does not is a bug in the plugin, and
			// silently delivering a DIFFERENT file is how it stays one.
			return nil, hostErr(wire.ErrNotFound, "no such file ref "+req.Ref+": name one of this job's inputs, or one of the outputs it is writing")
		}
		switch {
		case f.Output:
			if rel != "" {
				// Two different documents in one ask. `path` used to win
				// silently; against an output that would mean promising a
				// link of one file and delivering another.
				return nil, hostErr(wire.ErrInvalid, "ref names an output of this job and path names "+rel+": a link is of ONE file — pass one or the other")
			}
			outRef = f.Ref
		case rel == "":
			rel = f.Rel
		}
	}
	if rel == "" && outRef == "" {
		if inputs := s.Inputs(); len(inputs) > 0 {
			rel = inputs[0].PathRel
		}
	}
	var node *model.Node
	if outRef == "" {
		if i := strings.Index(rel, "://"); i >= 0 {
			rel = rel[i+3:]
		}
		rel = strings.Trim(rel, "/")
		if rel == "" {
			return nil, hostErr(wire.ErrInvalid, "no document for this link: pass ref or path, or run the action on a file")
		}
		node, _ = s.reg.opts.Store.GetNodeByPath(ctx, s.storageID, pathkey.Hash(s.storageID, "/"+rel))
		if node == nil && s.hasInput(rel) {
			// ⚠⚠ THE FILE IS THERE; THE CATALOGUE HAS NOT SEEN IT YET. A share
			// row points at a node, and a file that reached the storage outside
			// filex (a local folder, another client, a mount) has none until the
			// next sync — yet the explorer already lists it (manager.go
			// vfIndexFromDriver: cache first, driver fallback), the action gate
			// already stat-ed it on the driver, and this very job has read its
			// bytes. So a person could pick the document and ask for signatures,
			// and the request died here with "no such file" (2026-09-21, measured
			// on a storage with a PDF on disk and no sync run yet: the job failed
			// "Could not open the signing link…: not_found: no such file: a.pdf").
			//
			// The cause is the lagging catalogue, so the catalogue is what is
			// fixed: the one file is recorded the way a write through filex would
			// record it (protocolsync — node row, parent rows, index, thumbnail)
			// and the link is opened on it. Only for one of this job's INPUTS:
			// those are the paths the ACL was checked against when the job was
			// queued, which is what makes the new row no more than the person was
			// already allowed to reach. A path the plugin merely names stays
			// "no such file".
			node = s.reg.catalogueInput(ctx, s.plugin, s.storageID, rel)
		}
		if node == nil {
			return nil, hostErr(wire.ErrNotFound, "no such file: "+rel)
		}
		// ⚠⚠ WHOSE FILE. A page-less link hands over the node's own bytes to
		// a stranger, so it may only be of a file this call was already
		// handed — one of its inputs. The job was queued by a person whose
		// ACL was checked against exactly those paths (and the link is minted
		// as that person), which is what makes the share no more powerful
		// than the Share dialog they could have used themselves. Without
		// this, `public_pages` would read "download any file on this storage,
		// through a link", because a `path` is otherwise just a string the
		// plugin chose.
		if spec == nil && !s.hasInput(rel) {
			return nil, hostErr(wire.ErrPermissionDenied, "a link without a page may only share a file this job was given: "+rel+" is not one of its inputs")
		}
	} else if s.outputMode == "none" {
		// The same ACL question, answered before it is asked: an output is a
		// file this job is MAKING for the person who queued it, out of the
		// inputs their ACL was already checked against, and the host is about
		// to write it to their storage on their behalf. Sharing it is no more
		// than the job itself does.
		//
		// What CAN go wrong is promising a link against an action that never
		// commits anything, which would be a token nothing ever answers. Said
		// plainly, now, rather than as silence when the job finishes.
		return nil, hostErr(wire.ErrInvalid, "this action keeps no outputs (output mode \"none\"), so "+outRef+" will never become a file: share one of the job's inputs, or give the action an output")
	}

	// PIN policy is the manifest page's when there is one; a page-less link
	// has no page to set a policy, so the plugin's ask stands — inside the
	// host's own 4–12 bound, which every share obeys.
	pin := strings.TrimSpace(req.PIN)
	if spec != nil {
		switch spec.PIN {
		case "none":
			pin = ""
		case "required":
			if pin == "" {
				pin = "auto"
			}
		}
	}
	if pin == "auto" {
		pin = randomDigits(pagePinLength)
	}
	if pin != "" && (len(pin) < 4 || len(pin) > 12) {
		return nil, hostErr(wire.ErrInvalid, "pin must be 4–12 characters")
	}

	// TTL: the plugin's ask, clamped by the manifest's ceiling and the host's.
	// ⚠ The instance's own share ceiling (share.max_ttl_days) is applied on
	// top by share.Service.Create — an app cannot mint a longer-lived link
	// than the administrator allows anybody else, with or without a page.
	ttl := req.TTLDays
	if ttl <= 0 && spec != nil {
		ttl = spec.DefaultTTLDays
	}
	if ttl <= 0 {
		ttl = pageDefaultTTL
	}
	maxTTL := pageMaxTTL
	if spec != nil && spec.MaxTTLDays > 0 && spec.MaxTTLDays < maxTTL {
		maxTTL = spec.MaxTTLDays
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	expires := time.Now().Add(time.Duration(ttl) * 24 * time.Hour)

	// Copy the exposed files out of the call's spool FIRST, into a staging
	// directory: the link outlives the call, and a share row whose copies
	// failed half-way would be a live link to nothing.
	stage, err := os.MkdirTemp(s.reg.publicRoot(), "new-")
	if err != nil {
		if mkErr := os.MkdirAll(s.reg.publicRoot(), 0o700); mkErr != nil {
			return nil, hostErr(wire.ErrInternal, "page dir: "+mkErr.Error())
		}
		if stage, err = os.MkdirTemp(s.reg.publicRoot(), "new-"); err != nil {
			return nil, hostErr(wire.ErrInternal, "page dir: "+err.Error())
		}
	}
	files, err := s.copyExposed(ctx, req.Files, stage)
	if err != nil {
		_ = os.RemoveAll(stage)
		return nil, err
	}

	state := "{}"
	if len(req.State) > 0 {
		state = string(req.State)
	}
	var createdBy *int64
	if s.actor != nil && s.actor.ID > 0 {
		id := s.actor.ID
		createdBy = &id
	}
	var maxVisits *int
	if req.MaxVisits > 0 {
		n := req.MaxVisits
		maxVisits = &n
	}
	opts := share.CreateOpts{
		PIN:          pin,
		ExpiresAt:    &expires,
		MaxDownloads: maxVisits,
		CreatedBy:    createdBy,
		CreatedVia:   s.plugin.Row.Name,
		PluginID:     s.plugin.Row.ID,
		PageID:       req.PageID,
		Subject:      clip(strings.TrimSpace(req.Subject), 190),
		StateJSON:    state,
		FilesJSON:    jsonOf(files),
	}
	if req.Purpose != nil {
		opts.PurposeJSON = jsonOf(req.Purpose)
	}
	what := "download link"
	if req.PageID != "" {
		what = "public page " + req.PageID
	}

	// ── the promise: a link to a file this job has not finished writing ──
	//
	// Every decision about the link is taken HERE, so what the plugin is
	// handed back is the truth and not a placeholder: the token it will carry,
	// the PIN it will ask for, the day it will lapse. Only the row waits —
	// for the node, which runJob creates when it commits the output.
	if outRef != "" {
		tok, terr := share.NewToken()
		if terr != nil {
			_ = os.RemoveAll(stage)
			return nil, hostErr(wire.ErrInternal, "share token: "+terr.Error())
		}
		opts.Token = tok
		// The instance's ceiling decides the date the plugin is told, not the
		// date it asked for — Create would clamp it later and the mail would
		// already have gone out saying something else.
		opts.ExpiresAt, _ = share.ClampExpiry(opts.ExpiresAt, s.reg.opts.Share.MaxTTLDays(ctx), time.Now())
		s.promise(&promisedShare{ref: outRef, opts: opts, stage: stage, hasPIN: pin != ""})
		s.plugin.log("info", what+" promised for "+outRef+", opens when the job writes it")
		out := map[string]any{
			"token":      tok,
			"url":        PublicURL(s.reg.origin(ctx, s.storageID), tok),
			"expires_at": opts.ExpiresAt,
		}
		if pin != "" {
			out["pin"] = pin
		}
		return out, nil
	}

	opts.NodeID = node.ID
	sh, err := s.reg.opts.Share.Create(ctx, opts)
	if err != nil {
		_ = os.RemoveAll(stage)
		return nil, hostErr(wire.ErrInternal, "share row: "+err.Error())
	}
	if err := os.Rename(stage, s.reg.pageDir(sh.ID)); err != nil {
		_ = os.RemoveAll(stage)
		_ = s.reg.opts.Store.DeleteShare(ctx, sh.ID)
		return nil, hostErr(wire.ErrInternal, "page dir: "+err.Error())
	}
	s.plugin.log("info", what+" created (share "+strconv.FormatInt(sh.ID, 10)+"), expires "+expires.Format(time.RFC3339))
	s.reg.auditShareCreate(ctx, s.plugin, sh, node.ID, pin != "", "")
	out := map[string]any{
		"token":      sh.Token,
		"url":        PublicURL(s.reg.origin(ctx, s.storageID), sh.Token),
		"expires_at": sh.ExpiresAt,
	}
	if pin != "" {
		// Handed back ONCE, to the plugin that asked, so it can show the
		// requester or send it by a second channel. Never stored in clear.
		out["pin"] = pin
	}
	return out, nil
}

// hasInput reports whether rel (storage-relative, no leading slash) is one of
// THIS call's inputs — the files the job was handed, and so the only ones the
// person who queued it is known to have been allowed to reach.
func (s *Scope) hasInput(rel string) bool {
	for _, f := range s.Inputs() {
		if strings.Trim(f.PathRel, "/") == rel {
			return true
		}
	}
	return false
}

// copyExposed copies the refs a plugin wants the visitor to see into dir and
// describes them. Sizes are checked against the per-file and per-link ceilings
// as they are copied, not afterwards.
func (s *Scope) copyExposed(ctx context.Context, refs []wire.OutputRef, dir string) ([]PageFile, error) {
	files := []PageFile{}
	var total int64
	for i, f := range refs {
		src, err := s.spool(ctx, f.Ref)
		if err != nil {
			return nil, err
		}
		sf, _ := s.file(f.Ref)
		name := safeName(f.Name)
		if name == "" && sf != nil {
			name = sf.Name
		}
		st, err := os.Stat(src)
		if err != nil {
			return nil, hostErr(wire.ErrInternal, "stat: "+err.Error())
		}
		if st.Size() > pageMaxFileBytes || total+st.Size() > pageMaxTotalBytes {
			return nil, hostErr(wire.ErrTooLarge, "exposed files exceed the link limit")
		}
		total += st.Size()
		base := strconv.Itoa(i) + "-" + name
		if err := copyFile(src, filepath.Join(dir, base)); err != nil {
			return nil, hostErr(wire.ErrInternal, "copy: "+err.Error())
		}
		mime := ""
		if sf != nil {
			mime = sf.Mime
		}
		files = append(files, PageFile{Ref: "pub:" + strconv.Itoa(i), Name: name, File: base, Size: st.Size(), Mime: mime})
	}
	return files, nil
}

// hfShareRevoke implements share_revoke: the link stops working now. Revoking
// is the share's own revoke (expires_at := now), so the row stays in the
// administrator's Shares list with its history.
func hfShareRevoke(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	sh, err := s.reg.opts.Store.GetShareByToken(ctx, strings.ToLower(strings.TrimSpace(req.Token)))
	if err != nil || sh == nil || sh.PluginID != s.plugin.Row.ID {
		return nil, hostErr(wire.ErrNotFound, "no such link")
	}
	if !sh.IsExpired(time.Now()) {
		if err := s.reg.opts.Store.RevokeShare(ctx, sh.ID); err != nil {
			return nil, hostErr(wire.ErrInternal, err.Error())
		}
	}
	return map[string]any{"ok": true}, nil
}

// hfShareState implements share_state: read or replace the plugin's durable
// record for one link. A page event already has its link bound (s.page); a job
// names the token.
func hfShareState(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Token string          `json:"token"`
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	sh := s.page
	if sh == nil {
		if req.Token == "" {
			return nil, hostErr(wire.ErrInvalid, "token is required outside a page call")
		}
		var err error
		sh, err = s.reg.opts.Store.GetShareByToken(ctx, strings.ToLower(strings.TrimSpace(req.Token)))
		if err != nil || sh == nil || sh.PluginID != s.plugin.Row.ID {
			return nil, hostErr(wire.ErrNotFound, "no such link")
		}
	}
	if len(req.State) == 0 {
		return map[string]any{"state": json.RawMessage(orJSON(sh.StateJSON, "{}")), "page": pageFacts(sh)}, nil
	}
	if len(req.State) > pageMaxStateBytes {
		return nil, hostErr(wire.ErrTooLarge, "state over 64 KiB")
	}
	sh.StateJSON = string(req.State)
	if err := s.reg.opts.Store.UpdateShareAppState(ctx, sh.ID, sh.StateJSON); err != nil {
		return nil, hostErr(wire.ErrInternal, err.Error())
	}
	return map[string]any{"ok": true}, nil
}

// pageFacts is what a plugin may learn about its own link: never the token,
// never the PIN hash, never who else it was sent to.
func pageFacts(sh *model.Share) map[string]any {
	return map[string]any{
		"page":       sh.PageID,
		"subject":    sh.Subject,
		"visits":     sh.VisitCount,
		"expires_at": sh.ExpiresAt,
		"revoked":    sh.IsExpired(time.Now()),
		// Whether the PIN can still be SHOWN — never the PIN itself. An app
		// that offers "copy the PIN" has to know beforehand whether the verb
		// will work, or it offers a button that answers "cannot be shown"
		// (public_pin.go, share.RevealPIN).
		"has_pin":         sh.PinHash != "",
		"pin_recoverable": sh.PinRecoverable || sh.PinEnc != "",
	}
}

func orJSON(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// ── the visitor's side ─────────────────────────────────────────────────

// PageInfo describes an app link without opening it.
type PageInfo struct {
	Plugin      string         `json:"plugin"`
	Page        string         `json:"page"`
	Title       wire.Text      `json:"title"`
	Subject     string         `json:"subject,omitempty"`
	RequiresPIN bool           `json:"requires_pin"`
	Unlocked    bool           `json:"unlocked"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	Files       []wire.FileRef `json:"files"`
}

// LoadPage resolves a token to the live app share behind it
// (ErrPageNotFound / ErrPageGone).
//
// ⚠ A token that names an ordinary share is ErrPageNotFound here, not an
// error about kinds: this is an unauthenticated lookup and "wrong sort of
// link" is a fact about somebody else's data.
func (r *Registry) LoadPage(ctx context.Context, token string) (*model.Share, *Installed, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return nil, nil, ErrPageNotFound
	}
	sh, err := r.opts.Store.GetShareByToken(ctx, token)
	if err != nil || sh == nil || !sh.IsApp() {
		return nil, nil, ErrPageNotFound
	}
	if sh.IsExpired(time.Now()) {
		return sh, nil, ErrPageGone
	}
	p, ok := r.ByID(sh.PluginID)
	if !ok {
		return sh, nil, ErrPageGone
	}
	if state, _ := p.State(); state != StateRunning {
		return sh, p, ErrPageGone
	}
	return sh, p, nil
}

func pageFiles(sh *model.Share) []PageFile {
	var files []PageFile
	_ = json.Unmarshal([]byte(orJSON(sh.FilesJSON, "[]")), &files)
	return files
}

// ExposedFiles lists the copies an app link exposed, straight off the row.
//
// ⚠ Deliberately a free function and not a Registry method: the no-JS page
// must be able to hand a visitor the document they were sent even on an
// instance where the plugin runtime is unavailable (an architecture without
// wasm, a plugin that failed to load). The copies are already on disk and
// belong to the share, not to the running app.
func ExposedFiles(sh *model.Share) []wire.FileRef {
	out := []wire.FileRef{}
	for _, f := range pageFiles(sh) {
		out = append(out, wire.FileRef{Ref: f.Ref, Name: f.Name, Size: f.Size, Mime: f.Mime})
	}
	return out
}

// PageInfo describes a link without opening it.
func (r *Registry) PageInfo(sh *model.Share, p *Installed, unlocked bool) *PageInfo {
	info := &PageInfo{Plugin: "", Page: sh.PageID, Subject: sh.Subject, RequiresPIN: sh.PinHash != "", Unlocked: unlocked || sh.PinHash == "", ExpiresAt: sh.ExpiresAt, Files: []wire.FileRef{}}
	if p != nil {
		info.Plugin = p.Row.Name
		for _, spec := range p.Manifest.PublicPages {
			if spec.ID == sh.PageID {
				info.Title = spec.Label
			}
		}
	}
	if info.Unlocked {
		info.Files = ExposedFiles(sh)
	}
	return info
}

// CheckPIN verifies a PIN with the strike counter. A one-line forward: the
// gate itself is internal/share/pin.go, so /s/, /d/ and an app page cannot
// drift apart — which is exactly what v2 did (only the app page had a lock).
func (r *Registry) CheckPIN(ctx context.Context, sh *model.Share, pin string) error {
	return r.opts.Share.CheckPIN(ctx, sh, pin)
}

// MintUnlock / VerifyUnlock forward to the same one implementation.
func (r *Registry) MintUnlock(token string) string { return r.opts.Share.MintUnlock(token) }

// VerifyUnlock reports whether an unlock cookie belongs to this link.
func (r *Registry) VerifyUnlock(token, value string) bool {
	return r.opts.Share.VerifyUnlock(token, value)
}

// PageEvent runs the link's page_event export for an unlocked visitor.
// "open" spends one visit off the share's own counter. A surface carrying a
// job is returned as-is; the HTTP layer queues it as the share's creator.
func (r *Registry) PageEvent(ctx context.Context, sh *model.Share, p *Installed, in wire.ViewEventInput, visitorIP string) (*wire.Surface, error) {
	c, err := p.running()
	if err != nil {
		return nil, err
	}
	storageID, rel := r.pageAnchor(ctx, sh)
	scope, err := newScope(p, r, "", storageID, nil, nil, in.Context.Locale, false)
	if err != nil {
		return nil, err
	}
	defer scope.Close()
	scope.page = sh
	// The context file is an anchor for state_*, never readable here.
	if rel != "" {
		scope.AddInput(rel, 0, "")
	}
	dir := r.pageDir(sh.ID)
	for _, f := range pageFiles(sh) {
		scope.addPublic(f, dir)
	}
	if in.Event == "open" {
		// ⚠ The share's own VISIT counter (00052), which is what the link's
		// ceiling (`max_downloads`, holding the page's `max_visits`) is
		// measured against for an app page — model.Share.CappedCount.
		// ⚠⚠ Not the download counter any more: a signing link somebody had
		// only opened twice said "İndirme 2" in My shares (2026-09-21), and
		// the owner ruled that page views are not downloads.
		if err := r.opts.Store.IncrementShareVisit(ctx, sh.ID); err == nil {
			sh.VisitCount++
		}
	}
	in.ViewID = sh.PageID
	var state map[string]any
	_ = json.Unmarshal([]byte(orJSON(sh.StateJSON, "{}")), &state)
	in.Context = wire.CallContext{Inputs: scope.Inputs(), Locale: in.Context.Locale, Settings: r.publicSettings(ctx, p), Engines: r.enginesFor(p),
		ShareMaxTTLDays: r.linkCeiling(ctx, p)}
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	in.Data["page"] = map[string]any{"subject": sh.Subject, "state": state, "visits": sh.VisitCount, "visitor_ip": visitorIP}
	inb, _ := json.Marshal(in)
	outb, err := c.Call(WithScope(ctx, scope), "page_event", inb, 0)
	if err != nil {
		return nil, err
	}
	var s wire.Surface
	if err := json.Unmarshal(outb, &s); err != nil {
		return nil, &CallError{Code: CodePluginError, Message: "page_event returned malformed JSON"}
	}
	SanitizeSurface(&s)
	// A public page has no explorer to send anybody to.
	s.Open = nil
	return &s, nil
}

// PageAnchor reports the storage and storage-relative path the link is about
// — what per-file state and the follow-up job hang on. Zero/"" when the node
// has gone.
func (r *Registry) PageAnchor(ctx context.Context, sh *model.Share) (int64, string) {
	return r.pageAnchor(ctx, sh)
}

func (r *Registry) pageAnchor(ctx context.Context, sh *model.Share) (int64, string) {
	node, err := r.opts.Store.GetNode(ctx, sh.NodeID)
	if err != nil || node == nil {
		return 0, ""
	}
	return node.StorageID, strings.TrimPrefix(node.Path, "/")
}

// PageFile opens an exposed copy for the visitor (Range-capable).
func (r *Registry) PageFile(sh *model.Share, ref string) (*PageFile, *os.File, error) {
	for _, f := range pageFiles(sh) {
		if f.Ref == ref {
			fh, err := os.Open(filepath.Join(r.pageDir(sh.ID), f.File))
			if err != nil {
				return nil, nil, ErrPageNotFound
			}
			return &f, fh, nil
		}
	}
	return nil, nil, ErrPageNotFound
}

// addPublic registers an exposed copy as a readable scope file.
func (s *Scope) addPublic(f PageFile, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[f.Ref] = &scopeFile{Ref: f.Ref, Name: f.Name, Size: f.Size, Mime: f.Mime, Path: filepath.Join(dir, f.File)}
	s.order = append(s.order, f.Ref)
}

// stateAnchor lets state_get/state_set work on a page scope: the page's
// context file is the anchor even though it cannot be read here.
func (s *Scope) stateAnchor(ref string) (string, bool) {
	f, ok := s.file(ref)
	if !ok || f.Rel == "" {
		return "", false
	}
	return pathkey.Hash(s.storageID, "/"+strings.TrimPrefix(f.Rel, "/")), true
}

// SweepPages removes the exposed copies of links that are gone. Called hourly
// by the server; safe to call any time.
//
// ⚠ It walks the DIRECTORIES rather than querying for dead rows, because a
// directory can outlive its row: DeleteShare (the administrator's hard delete)
// leaves nothing to join against, and those copies would then sit on disk
// until somebody noticed. A directory is removed when its share is gone, or
// when the share has been dead for longer than the grace period.
func (r *Registry) SweepPages(ctx context.Context) int {
	entries, err := os.ReadDir(r.publicRoot())
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-pageSweepAfter)
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			// A staging directory a crashed create left behind; it is only
			// ever seconds old in the happy path.
			if strings.HasPrefix(e.Name(), "new-") {
				if info, ierr := e.Info(); ierr == nil && info.ModTime().Before(cutoff) {
					_ = os.RemoveAll(filepath.Join(r.publicRoot(), e.Name()))
					n++
				}
			}
			continue
		}
		sh, err := r.opts.Store.GetShareByID(ctx, id)
		dead := err != nil || sh == nil
		if !dead && sh.ExpiresAt != nil && sh.ExpiresAt.Before(cutoff) {
			dead = true
		}
		if dead {
			_ = os.RemoveAll(r.pageDir(id))
			n++
		}
	}
	if n > 0 {
		r.log.Info("app-plugins: swept public link files", slog.Int("removed", n))
	}
	return n
}

// StartSweeper runs SweepPages every hour until ctx ends.
func (r *Registry) StartSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.SweepPages(ctx)
				_, _ = r.opts.Store.DeleteExpiredAppPluginLocks(ctx, time.Now())
			}
		}
	}()
}

// Locks lists every live app-plugin lock (admin surface). storageID 0 = all.
func (r *Registry) Locks(ctx context.Context, storageID int64) ([]*model.AppPluginLock, error) {
	rows, err := r.opts.Store.ListAppPluginLocks(ctx, storageID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]*model.AppPluginLock, 0, len(rows))
	for _, l := range rows {
		if l.Live(now) {
			out = append(out, l)
		}
	}
	return out, nil
}

// AppLabel is an installed app's own label, in every language its manifest
// wrote it in ({"en": "e-Signature", "tr": "e-İmza"}), or nil when no app
// of that name is installed here.
//
// ⚠ A lock, an action row and a job all carry the app's manifest NAME
// (`sign`), because that is what addresses it; a person reading "sign locked
// this file" is reading an identifier the rest of the product never shows
// them — every list, the install review and the app's own page say
// "e-Signature" (v0.43.0). So the words a person reads resolve the name to
// this, and fall back to the name only where the app is gone.
func (r *Registry) AppLabel(name string) wire.Text {
	p, ok := r.ByName(name)
	if !ok || p == nil {
		return nil
	}
	return p.Manifest.Label
}

// LockReason is a lock's reason as words: the plain text (English, for a
// consumer with no reader — a log, a protocol server) and, for a reason the
// app named by message key, the same words in every language the app wrote
// them in. A plain reason has no Text; a message whose app or key is gone
// has no words at all (never the raw key).
func (r *Registry) LockReason(l *model.AppPluginLock) (string, wire.Text) {
	if l == nil {
		return "", nil
	}
	if !strings.HasPrefix(l.Reason, lockMsgPrefix) {
		return l.Reason, nil
	}
	key, raw, _ := strings.Cut(strings.TrimPrefix(l.Reason, lockMsgPrefix), " ")
	var args map[string]string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &args)
	}
	p, ok := r.ByName(l.PluginName)
	if !ok {
		return "", nil
	}
	t, ok := p.Manifest.Messages[key]
	if !ok {
		return "", nil
	}
	t = wire.FillMessage(t, args)
	return t.Get("en"), t
}

// StorageName is a storage's name, or "" when it is gone (or unreadable) —
// what the panel prints beside a lock instead of the storage's id.
func (r *Registry) StorageName(ctx context.Context, id int64) string {
	if st, err := r.opts.Store.GetStorage(ctx, id); err == nil && st != nil {
		return st.Name
	}
	return ""
}

// Unlock lifts a lock by force — the administrator's override for a plugin
// that will never come back to lift it. Returns false when nothing was
// locked there.
func (r *Registry) Unlock(ctx context.Context, storageID int64, rel string) (bool, error) {
	rel = strings.Trim(rel, "/")
	ph := pathkey.Hash(storageID, "/"+rel)
	cur, err := r.opts.Store.GetAppPluginLock(ctx, storageID, ph)
	if err != nil {
		return false, err
	}
	if cur == nil {
		return false, nil
	}
	if err := r.opts.Store.DeleteAppPluginLock(ctx, storageID, ph); err != nil {
		return false, err
	}
	if p, ok := r.ByID(cur.PluginID); ok {
		p.log("warn", "lock on "+rel+" lifted by an administrator")
	}
	return true, nil
}

func randomDigits(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%10]
	}
	return string(b)
}

// LinkOf says what an app's link IS, for a list of links (My shares, the
// admin's Shares): the purpose the app gave the link when it opened it, else
// the one its page declares, and the app's home view the row opens. Nil for
// an ordinary share, for an app that is gone, and for a link with no purpose
// — the row then lists as a plain share.
//
// ⚠ Page-less links too (an app's plain share of a file: PluginID set, no
// PageID). The finished document's delivery link listed as a share somebody
// made by hand, beside signing links marked as what they are.
func (r *Registry) LinkOf(sh *model.Share) *db.AppLink {
	if r == nil || sh == nil || sh.PluginID <= 0 {
		return nil
	}
	p, ok := r.ByID(sh.PluginID)
	if !ok {
		return nil
	}
	var purpose *wire.PagePurpose
	if sh.PurposeJSON != "" {
		var own wire.PagePurpose
		if json.Unmarshal([]byte(sh.PurposeJSON), &own) == nil && len(own.Label) > 0 {
			purpose = &own
		}
	}
	if purpose == nil && sh.PageID != "" {
		for i := range p.Manifest.PublicPages {
			if p.Manifest.PublicPages[i].ID == sh.PageID {
				purpose = p.Manifest.PublicPages[i].Purpose
				break
			}
		}
	}
	if purpose == nil {
		return nil
	}
	out := &db.AppLink{
		Plugin: p.Row.Name, Page: sh.PageID,
		Label: purpose.Label, Revoke: purpose.Revoke,
	}
	// The app's first home view: where its own list of these lives. With
	// none, the row is still named for what it is, and opens nothing.
	for _, v := range p.Manifest.Views {
		if v.Placement == "home" {
			out.View, out.Section = v.ID, purpose.Section
			break
		}
	}
	return out
}
