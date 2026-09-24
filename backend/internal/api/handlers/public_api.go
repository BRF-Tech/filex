// Package handlers — public_api.go
//
// ONE PUBLIC SURFACE. Everything a stranger reaches through a filex link — a
// download share, a file request, an app plugin's page — is answered here as
// JSON, so the SPA can render all three with one shell, one PIN gate, one
// expiry story and the instance's own branding.
//
//	GET  /api/public/branding                  — who this instance says it is
//	GET  /api/public/ui-locales/{code}         — one language an app adds: its strings
//	GET  /api/public/s/{token}                 — a share's state
//	POST /api/public/s/{token}/pin             — answer the PIN → unlock cookie
//	POST /api/public/s/{token}/event           — an app page's surface event
//	GET  /api/public/s/{token}/file/{ref}      — a copy an app page exposed
//	GET  /api/public/d/{token}                 — a file request's state
//	POST /api/public/d/{token}/pin             — answer the PIN → unlock cookie
//	POST /api/public/d/{token}/upload          — the drop itself (multipart)
//
// ⚠ NOTHING HERE IS AUTHENTICATED, and nothing here leaks. The token is the
// only capability; a wrong or unknown one is 404 with no other detail, and a
// dead one is 410 with the same shape for every reason it could be dead. The
// creator's address, the storage's name and the file's path never appear.
//
// ⚠ Every answer is `Cache-Control: no-store` and `X-Robots-Tag: noindex`
// EXCEPT /branding and /ui-locales, the two things here that are not about a
// link: who the instance is and the words it speaks. Both are deliberately
// cacheable (every public page fetches them).
//
// ⚠ The PIN gate is internal/share's — five strikes then ten minutes, counted
// on the share row, answered with an HMAC cookie that carries no PIN. It is
// the same gate the no-JS pages use, and the same one the app pages used to
// have to themselves.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
	"slices"
)

// PublicAPI serves /api/public/*.
type PublicAPI struct {
	Store   db.Store
	Service *share.Service
	// Brand answers /api/public/branding. Nil-safe: unwired yields the stock
	// filex identity, which is what an instance that never opened the
	// Corporate identity page has anyway.
	Brand *BrandingSource
	// Apps answers the app-plugin half. Nil (or app plugins disabled) turns
	// every /event and /file call into a 404 and leaves /s/{token} answering
	// about the share alone.
	Apps *AppPlugins
	// Drop reuses the file-request ingest so the JSON upload path and the
	// no-JS form path are the same code — limits, quota, notifications and
	// all.
	Drop *Drop
	// DefaultLocale is the instance's fallback language, reported in
	// /branding so the shell can pick one before a person has.
	DefaultLocale string
}

// NewPublicAPI constructs the handler.
func NewPublicAPI(store db.Store, svc *share.Service) *PublicAPI {
	return &PublicAPI{Store: store, Service: svc}
}

// AttachBranding wires the shared branding source.
func (h *PublicAPI) AttachBranding(b *BrandingSource) { h.Brand = b }

// AttachApps wires the app-plugin handler (nil = app pages unavailable).
func (h *PublicAPI) AttachApps(a *AppPlugins) { h.Apps = a }

// AttachDrop wires the file-request ingest.
func (h *PublicAPI) AttachDrop(d *Drop) { h.Drop = d }

// AttachLocale sets the instance's fallback language.
func (h *PublicAPI) AttachLocale(def string) { h.DefaultLocale = def }

func (h *PublicAPI) headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

// ─────────────────── GET /api/public/branding ───────────────────

// PublicBranding is what the public shell needs to look like this instance
// rather than like filex. It is the SAME record the admin "Corporate
// identity" page writes — whitelabel means whitelabel: a signature request
// from a renamed instance does not say "filex".
//
// ⚠ Deliberately NOT the same payload as /api/branding: that one also carries
// the SSO button label, which a stranger has no business receiving.
//
// ⚠ It no longer differs by the custom stylesheet, and the sentence that said
// so was left standing for a while after it stopped being true. Since v0.43.0
// the sheet is on NEITHER payload: it is served from GET /api/me/custom-css
// behind authentication, and the field was REMOVED from /api/branding rather
// than blanked, so an old client fails loudly instead of quietly rendering
// nothing. A comment that describes a retired boundary is worse than no
// comment, because the next person reasons from it.
type PublicBranding struct {
	Name          string `json:"name"`
	LogoURL       string `json:"logo_url"`
	Accent        string `json:"accent"`
	FooterText    string `json:"footer_text"`
	HidePoweredBy bool   `json:"hide_powered_by"`
	// Theme is the appearance a visitor gets before they choose one:
	// "system" (follow the device), "light" or "dark". Settings key
	// `branding.theme`; anything else is read as "system".
	Theme string `json:"theme"`
	// Locale is the instance's own default language, and Locales are the
	// languages the public pages can be rendered in.
	Locale  string   `json:"locale"`
	Locales []string `json:"locales"`
	// UILocales are the languages installed apps add to filex itself — one
	// row each (tag, the app that brought it, whether it is right to left),
	// and NO strings. The strings of one language are GET
	// /api/public/ui-locales/{code}, fetched when somebody picks it.
	//
	// ⚠⚠ This field used to be `{tag: {key: text}}` — every string of every
	// added language, on the one answer every public page and every panel
	// start reads — while the browser read it as a LIST (`ui_locales?.length`)
	// and so never saw a single string: an installed language joined the
	// picker and then spoke English (measured 2026-09-21). It is a list now,
	// the same PublicLocaleOption rows the browser types, and the wire fixture
	// the web tests read is written by this package (public_wire_test.go).
	UILocales []wasmplugin.UILocaleInfo `json:"ui_locales,omitempty"`
}

// BrandingSettingTheme is the settings key holding the public shell's default
// appearance.
const BrandingSettingTheme = "branding.theme"

// Branding answers the public shell's identity. Unauthenticated and cacheable
// — it is fetched by every public page and changes when an administrator says
// so, not per visitor.
func (h *PublicAPI) Branding(w http.ResponseWriter, r *http.Request) {
	cfg := h.Brand.ForRequest(r)
	out := PublicBranding{
		Name:          cfg.Name,
		LogoURL:       cfg.LogoURL,
		Accent:        cfg.Accent,
		FooterText:    cfg.FooterText,
		HidePoweredBy: cfg.HidePoweredBy,
		Theme:         "system",
		Locale:        publicLocale(r, h.DefaultLocale),
		Locales:       publicLocaleList(),
	}
	if h.Apps != nil && h.Apps.Registry != nil {
		out.UILocales = h.Apps.Registry.UILocaleList()
		for _, l := range out.UILocales {
			if !slices.Contains(out.Locales, l.Code) {
				out.Locales = append(out.Locales, l.Code)
			}
		}
		sort.Strings(out.Locales)
	}
	if h.Store != nil {
		if m, err := h.Store.ListSettings(r.Context()); err == nil {
			switch strings.ToLower(strings.TrimSpace(m[BrandingSettingTheme])) {
			case "light":
				out.Theme = "light"
			case "dark":
				out.Theme = "dark"
			}
		}
	}
	// ⚠ `locale` above is resolved from the VISITOR's Accept-Language, so one
	// URL has more than one body and a shared cache has to be told. Added, not
	// set: the CORS middleware has already put `Origin` there.
	w.Header().Add("Vary", "Accept-Language")
	// ⚠ Revalidated, not held for a minute: the offered languages are on
	// this answer, and a pack installed a moment ago has to be on the next
	// page load (public_cache.go).
	writePublicJSON(w, r, out)
}

// PublicUILocale is GET /api/public/ui-locales/{code}: the strings of one
// language the installed apps add, merged, by filex's own keys.
type PublicUILocale struct {
	Code    string            `json:"code"`
	Strings map[string]string `json:"strings"`
}

// UILocale answers one added language's strings. Unauthenticated for the
// same reason /branding is — a public page is offered the language too — and
// cacheable for the same minute. 404 when no running app ships the tag, so a
// language that left with its app stops being served at once rather than
// lingering as a table nothing lists.
func (h *PublicAPI) UILocale(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "code")))
	var strs map[string]string
	if h.Apps != nil && h.Apps.Registry != nil {
		strs = h.Apps.Registry.UILocale(code)
	}
	if len(strs) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	// ⚠ Revalidated: an upgraded pack served yesterday’s words for a minute,
	// and an unchanged one still costs nothing (public_cache.go).
	writePublicJSON(w, r, PublicUILocale{Code: code, Strings: strs})
}

// ─────────────────── the share's state ───────────────────

// PublicNode is the little a visitor learns about the file behind a link:
// what it is called, how big it is and what kind of thing it is. Never its
// path, never its storage, never who owns it.
type PublicNode struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

// PublicApp describes the app surface a link carries, once it is unlocked.
type PublicApp struct {
	Plugin string         `json:"plugin"`
	Page   string         `json:"page"`
	Title  wire.Text      `json:"title,omitempty"`
	Files  []wire.FileRef `json:"files"`
}

// PublicShare is GET /api/public/s/{token}.
//
// `expired` and `revoked` are both reported because they are different
// sentences to a visitor, and they mean exactly this:
//
//   - expired — the clock ran out (`expires_at` has passed). An
//     administrator's Revoke is expressed by moving the expiry to the moment
//     it happened, so it lands here too; there is no second column and one
//     that could disagree with this would be worse than one word doing both.
//   - revoked — the link is dead for a reason that is NOT the clock: its
//     visit/download ceiling is spent, or the app that answers it has been
//     stopped or removed.
//
// A live link has both false. A caller that only wants "can I use this"
// reads `expired || revoked`.
type PublicShare struct {
	Kind     string `json:"kind"` // "file" | "folder" | "app"
	NeedsPIN bool   `json:"needs_pin"`
	Unlocked bool   `json:"unlocked"`
	Expired  bool   `json:"expired"`
	Revoked  bool   `json:"revoked"`
	// Locked reports the PIN gate being shut after five wrong answers. It is
	// NOT a reason the link is dead — it lifts by itself.
	Locked     bool       `json:"locked"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	VisitsLeft *int       `json:"visits_left"`
	Subject    string     `json:"subject,omitempty"`
	// Node is the file or folder behind the link, once unlocked. ⚠ Omitted
	// for an app link: there the node is the ANCHOR the app's state and its
	// follow-up job hang on, and the only bytes a visitor may have are the
	// copies the app exposed. No storage driver is opened for an anonymous
	// request, which is the property the v2 app pages had and this keeps.
	Node *PublicNode `json:"node,omitempty"`
	App  *PublicApp  `json:"app,omitempty"`
}

// Share answers a share link's state.
func (h *PublicAPI) Share(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDownload)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.describe(r, sh, h.unlocked(r, sh)))
}

// describe builds the answer for one share.
func (h *PublicAPI) describe(r *http.Request, sh *model.Share, unlocked bool) PublicShare {
	now := time.Now()
	out := PublicShare{
		Kind:      "file",
		NeedsPIN:  sh.PinHash != "",
		Unlocked:  unlocked,
		Expired:   sh.ExpiresAt != nil && now.After(*sh.ExpiresAt),
		Locked:    sh.PinLocked(now),
		ExpiresAt: sh.ExpiresAt,
		Subject:   sh.Subject,
	}
	if sh.MaxDownloads != nil {
		left := *sh.MaxDownloads - sh.CappedCount()
		if left < 0 {
			left = 0
		}
		out.VisitsLeft = &left
		out.Revoked = left == 0
	}
	if sh.IsApp() {
		out.Kind = "app"
		app := &PublicApp{Page: sh.PageID, Files: []wire.FileRef{}}
		// ⚠ `revoked`, not `expired`: nothing ran out. This is the same
		// class of death as "the app that answers the link was stopped or
		// removed" (the type's own doc above) — the link cannot do what it
		// was made for any more, for a reason that is not the clock. Saying
		// so HERE and not only at the surface is what keeps the shell honest:
		// a state object that calls the link live while every event on it
		// answers 410 is two screens disagreeing about one link.
		if _, live := linkCreator(r.Context(), h.Store, sh); !live {
			out.Revoked = true
		}
		if h.Apps != nil && h.Apps.Registry != nil {
			if p, live := h.Apps.Registry.ByID(sh.PluginID); live {
				if info := h.Apps.Registry.PageInfo(sh, p, unlocked); info != nil {
					app.Plugin, app.Title, app.Files = info.Plugin, info.Title, info.Files
				}
				if state, _ := p.State(); state != wasmplugin.StateRunning {
					out.Revoked = true
				}
			} else {
				out.Revoked = true
			}
		} else {
			out.Revoked = true
		}
		out.App = app
		return out
	}
	if node, err := h.Store.GetNode(r.Context(), sh.NodeID); err == nil && node != nil {
		if node.Type == model.NodeTypeDirectory {
			out.Kind = "folder"
		}
		if unlocked {
			out.Node = &PublicNode{Name: node.Name, Size: node.Size, Mime: node.Mime}
			if out.Subject == "" {
				out.Subject = node.Name
			}
		}
		// ⚠ The file's name is BEHIND the PIN, like its bytes. A subject the
		// sharer wrote is theirs to reveal; a name the sharer never chose is
		// not, and "teklif.pdf" over a PIN box tells a stranger what they
		// are knocking on. Before the gate opens the link says only that it
		// is a link.
	} else {
		// The file behind the link is gone. Not "expired" — nothing ran out.
		out.Revoked = true
	}
	return out
}

// linkCreator resolves the person a public link ACTS AS, and answers whether
// they can still stand behind it.
//
// ⭐ ONE reading of that question, for every door an app link has: the state
// the shell renders (describe), the surface a visitor presses buttons on
// (loadApp), the job those presses queue (enqueueAsCreator), the bytes of the
// document the app exposed (PublicAPI.File) and the no-JS page that lists them
// (Share.renderAppPage). They used to have no reading of it at all, and the
// moment one of them grows a second opinion the link is shut on one screen and
// open on the next — a door that says "closed" beside a window that is not.
//
// ⚠ A free function, not a method, precisely because the last of those doors
// hangs off a DIFFERENT handler (*Share, the no-JS surface) than the others
// (*PublicAPI). A method would have been copied across rather than shared,
// which is the one outcome this must not have.
//
// Two ways to fail, one answer:
//
//   - the account is GONE. The rights the job would spend went with it.
//   - the account is DISABLED. `Enabled` is explicitly not a soft delete
//     elsewhere in filex (model.User: "files, quota and grants are
//     untouched"), which is exactly why this had to be DECIDED rather than
//     assumed: the owner's call is that turning an account off also closes
//     the doors that account left open, because somebody who has left must
//     not keep an outside party's link into the company's documents alive.
//     ⚠ The accepted cost is the other half of that: a temporarily disabled
//     account PAUSES its signature flows rather than killing them, and
//     re-enabling the account makes every one of its outstanding links work
//     again. Nothing about the link is rewritten here, which is what makes
//     the pause reversible — do not "tidy up" by revoking the share row.
//
// ⚠⚠ And ONE way that is not a failure at all: a link that names NOBODY
// (`created_by` NULL) is live. That is not a hypothetical to be tidied into
// "refuse by default" — an app's scheduled wake-up runs with no actor ("the
// job runs with actor_id null because nobody asked for it"), so a link a
// `tick` minted to deliver a renewal has no account behind it and never did.
// There is nobody to have left, so there is nothing to close. Refusing those
// was caught by TestNoJSFallback_AppLinkListsExposedCopies, which had been
// serving exactly such a link since before any of this existed. The JOB door
// still refuses them, for its own older reason: a job needs an ACL to run
// under, and a link with no creator has none.
//
// ⚠ One extra row read per public app-link call. It is a primary-key lookup
// on the users table, on a path that already reads the share and the node,
// and it is the only way to ask a question whose answer changes AFTER the
// link was minted.
func linkCreator(ctx context.Context, store db.Store, sh *model.Share) (*model.User, bool) {
	if sh == nil || store == nil {
		return nil, false
	}
	if sh.CreatedBy == nil {
		return nil, true
	}
	u, err := store.GetUser(ctx, *sh.CreatedBy)
	if err != nil || u == nil || !u.Enabled {
		return nil, false
	}
	return u, true
}

// PIN answers the PIN and, on a hit, mints the unlock cookie.
func (h *PublicAPI) PIN(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, "")
	if !ok {
		return
	}
	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	switch err := h.Service.CheckPIN(r.Context(), sh, req.PIN); {
	case errors.Is(err, share.ErrLocked):
		h.auditPinLock(r, sh)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "locked", "message": "too many wrong PINs; try again in a few minutes"})
		return
	case err != nil:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pin_wrong"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: share.CookieName(sh.Token), Value: h.Service.MintUnlock(sh.Token),
		Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
		MaxAge: int(share.UnlockTTL().Seconds()),
	})
	writeJSON(w, http.StatusOK, h.describe(r, sh, true))
}

// auditPinLock records a lock. ⚠ The token itself never reaches the audit
// row — a live link in an audit table is a live link for everybody who can
// read audit rows.
func (h *PublicAPI) auditPinLock(r *http.Request, sh *model.Share) {
	if h.Store == nil {
		return
	}
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		Action: "share.pin_locked", TargetType: "share", TargetID: share.TokenHash(sh.Token)[:12],
		IP: clientIP(r), Metadata: map[string]any{"kind": sh.Kind, "plugin_id": sh.PluginID},
	})
}

// ─────────────────── an app page's surface ───────────────────

// Event runs the app's page_event for an unlocked visitor. `GET`-shaped opens
// are POSTs with `{"event":"open"}` — one endpoint, because the opening
// surface and every later one are the same call to the app.
func (h *PublicAPI) Event(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	// ⚠ The PIN gate comes BEFORE the app is consulted. Asking the registry
	// first means a locked link answers "no such app" when the plugin happens
	// to be stopped, and "pin required" when it is not — which tells a caller
	// who never got past the gate something about the instance behind it.
	gate, ok := h.loadShare(w, r, model.ShareKindDownload)
	if !ok {
		return
	}
	if !gate.IsApp() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if !h.unlocked(r, gate) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pin_required"})
		return
	}
	sh, p, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	var req struct {
		State    map[string]any `json:"state"`
		Event    string         `json:"event"`
		ActionID string         `json:"action_id"`
		Data     map[string]any `json:"data"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
	}
	if req.Event == "" {
		req.Event = "open"
	}
	// The visitor's language — the page sends the one it shows as
	// Accept-Language — by the rule every public surface uses (publicLocale):
	// a language pack's too, so an app that ships it answers in it.
	locale := publicLocale(r, h.DefaultLocale)
	// ⚠⚠ The same host half of show_when / required_when the view handler runs
	// (surface_conditions.go), for the same reason and with the same code: an
	// outside visitor is the LAST caller whose word about which fields were on
	// their screen should be taken. A hidden field's value is dropped before
	// the app sees it; an empty `required_when` field refuses the event, so it
	// cannot become a job queued as the link's creator.
	if missing, gerr := gateSurfaceValues(req.Event, req.State, req.Data, func(gin wire.ViewEventInput) (*wire.Surface, error) {
		gin.Context = wire.CallContext{Locale: locale}
		return h.Apps.Registry.PageEvent(r.Context(), sh, p, gin, clientIP(r))
	}); gerr != nil {
		h.Apps.callFail(w, gerr)
		return
	} else if len(missing) > 0 {
		writeSurfaceRequired(w, missing)
		return
	}
	in := wire.ViewEventInput{Event: req.Event, ActionID: req.ActionID, State: req.State, Data: req.Data,
		Context: wire.CallContext{Locale: locale}}
	s, err := h.Apps.Registry.PageEvent(r.Context(), sh, p, in, clientIP(r))
	if err != nil {
		h.Apps.callFail(w, err)
		return
	}
	if s.Job != nil {
		h.enqueueAsCreator(w, r, sh, p, s.Job)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"surface": s})
}

// enqueueAsCreator queues the job a page asked for, on the link's document, as
// the person who created the link. The creator's ACL is what the worker runs
// under, and the visitor never gains a user of their own.
//
// ⚠⚠ EVERY submit-time check the authenticated door makes (app_plugins.go:
// authorise → enqueue) has to be made here too, and until 2026-09-20 it was
// not: this path resolved the action straight out of the raw manifest and
// looked only at the read-only flag. An outside visitor could therefore start
// an action an administrator had DISABLED, or one restricted to
// administrators, or one belonging to a plugin that was stopped, with no
// ceiling on the parameters and no re-reading of the creator's file access —
// and the job then ran as the creator. The gap was structural, not a slip:
// whatever the authenticated path learns to refuse, a second door that
// resolves its own action learns nothing. So every check below is expressed in
// the SAME helper that door uses (Registry.ResolveAction, jobOutputMode,
// pluginACLNeed, aclAllowForPluginAs, maxJobParamsBytes) and none of them is
// re-spelled here.
func (h *PublicAPI) enqueueAsCreator(w http.ResponseWriter, r *http.Request, sh *model.Share, p *wasmplugin.Installed, job *wire.JobRequest) {
	storageID, rel := h.Apps.Registry.PageAnchor(r.Context(), sh)
	if h.Apps.Ops == nil || sh.CreatedBy == nil || storageID == 0 || rel == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no_job_context", "message": "this link cannot start a job"})
		return
	}
	// ⭐ WHO the job will run as. Everything below is judged about this
	// person and not about the visitor: the queue row carries their id, the
	// worker opens their storage, the output lands in their folder.
	//
	// ⚠ Deleted or disabled, the link stops — the same reading loadApp made
	// at the door (linkCreator), asked again here because THIS is the write.
	// A door that queues a row on somebody's storage does not get to assume
	// an earlier gate ran; the two checks are one function precisely so they
	// can never answer differently.
	// ⚠ `creator == nil` as well as `!live`: linkCreator calls a link that
	// names nobody LIVE, because a page a scheduled tick minted has no
	// account behind it and still has a document to hand over. A JOB is the
	// one thing such a link cannot do — there is no ACL to run it under — so
	// this door, and only this door, refuses that case too. (The guard at the
	// top of this function already turns most of them away for the same
	// reason; this is the one that survives a future edit to it.)
	creator, live := linkCreator(r.Context(), h.Store, sh)
	if !live || creator == nil {
		h.jobRefused(w, http.StatusForbidden, "no_access")
		return
	}
	// ⚠⚠ The registry, NOT p.Manifest.Action: the manifest is what the app
	// author shipped, the registry is what the ADMINISTRATOR runs. Only the
	// registry knows that this action was switched off, that it was reserved
	// for administrators, or that the plugin is no longer running — the three
	// answers a raw manifest lookup cannot give, and therefore the three an
	// outside visitor used to be able to walk straight past.
	//
	// ⚠⚠ isAdmin is the LINK'S CREATOR's admin status, never the visitor's,
	// and that is a decision rather than a convenience: the job runs as the
	// creator with the creator's ACL, so the creator's authority is what is
	// being spent. The visitor is a courier — they press a button on a screen
	// the app drew for them — and an administrator who minted a link into an
	// admin-restricted action did so deliberately. Reading the visitor here
	// would break every such link (an anonymous caller is never an admin);
	// reading nobody at all, which is what the manifest lookup amounted to,
	// handed the visitor the administrator's actions.
	_, action, applies, rerr := h.Apps.Registry.ResolveAction(r.Context(), p.Row.Name, job.ActionID, creator.IsAdmin())
	if rerr != nil {
		// ⚠ ResolveAction's own words ("action is disabled by the
		// administrator", "action is restricted to administrators") describe
		// how this instance is CONFIGURED, which is what the file header's
		// "NOTHING HERE LEAKS" forbids telling a stranger holding a token.
		// The visitor gets one sentence for all of it; the operator reads
		// which one it was in the plugin's own log.
		code, kind := http.StatusConflict, "link_unavailable"
		if wasmplugin.IsCode(rerr, wasmplugin.CodeRefused) {
			code, kind = http.StatusForbidden, "permission_denied"
		}
		h.jobRefused(w, code, kind)
		return
	}
	// ⭐ The output the SURFACE asked for, read exactly as the authenticated
	// path reads it (jobOutputMode / applyJobOutput in app_plugins.go).
	//
	// ⚠⚠ This was missing, and it is not a cosmetic drift: the signing wizard
	// lets the requester choose where the signed document goes — a new version
	// of the file, or a new file beside it with a name they type. A submit
	// from INSIDE filex honoured that choice; the same submit from an outside
	// signer's link dropped it and fell back to whatever the manifest
	// declares, so "a new file called X" silently overwrote the original as a
	// new version. One job, two answers, depending on which door it came
	// through.
	mode, writes, okMode := jobOutputMode(action, job.Output)
	if !okMode {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad output mode: " + job.Output.Mode})
		return
	}
	// A visitor on a link chooses no folder in the creator's storages.
	if mode == "folder" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder_not_offered", "message": "a page cannot choose where the result goes"})
		return
	}
	writes = writes || applies.Writable
	// The same refusal the authenticated path makes, for the same reason: a
	// writing mode on a read-only depo is a job that can only fail, and it
	// fails in the worker where the visitor never sees it.
	if writes {
		if st, err := h.Store.GetStorage(r.Context(), storageID); err == nil && st != nil && st.ReadOnly {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "read_only", "message": "this storage is read-only"})
			return
		}
	}
	// ⚠⚠ The creator's access to the anchor, RE-READ at submit — never
	// trusted from the day the link was minted. A link outlives the grant
	// that justified it: the requester moves teams, the folder's sharing is
	// tidied up, the account is demoted to viewer. Without this, a month-old
	// link in a stranger's mailbox went on writing into a folder its sender
	// could no longer open, and the audit row said the sender did it.
	//
	// ⭐ The consequence the product owner accepted, written down because it
	// is the visible half of the fix: when the requester loses access, the
	// links they already sent STOP — and they stop with a sentence the
	// visitor can act on (jobRefused) instead of queueing a job that dies
	// unseen in the worker.
	//
	// ⚠ aclAllowForPluginAs, not aclAllowForPlugin: the actor in THIS
	// request's context is the anonymous visitor, who has no grants at all
	// (acl_guard.go says why the two spellings exist).
	need := pluginACLNeed(action, writes)
	if !aclAllowForPluginAs(r.Context(), h.Apps.ACL, h.Store, creator, storageID, rel, need, p.Row.ID) {
		h.jobRefused(w, http.StatusForbidden, "no_access")
		return
	}
	// The encrypted-folder boundary, drawn where authorise draws it: filex
	// holds no key for an end-to-end encrypted folder, so a plugin reading one
	// is handed ciphertext and produces nonsense. The obstacle is on the
	// creator's side of the link, so it wears the creator's sentence.
	if lk, ok := h.Store.(e2e.NodeByPathLookup); ok && lk != nil && e2e.UnderEncrypted(r.Context(), lk, storageID, "/"+rel) {
		h.jobRefused(w, http.StatusForbidden, "no_access")
		return
	}
	params := applyJobOutput(job.Params, job.Output)
	// ⚠ The share's ID, not its token: the parameters travel into a queue row
	// an administrator can read, and the token is the link itself.
	params["share_id"] = sh.ID
	// ⭐ WHICH VISITOR is acting. The plugin minted this link and kept
	// sha256(token) beside the one person it was sent to; the same hash comes
	// back here, so the job can bind the submission to that person and refuse
	// anybody acting as somebody else. Without it a page job arrives
	// anonymous — it runs as the link's CREATOR, so an outside signer looks
	// like the requester and is told they are not a signer at all.
	//
	// ⚠ The HASH, never the token, for the reason above: these parameters are
	// a queue row an administrator reads. share.TokenHash is the same function
	// the PIN-lock audit row uses, so the two can never drift apart, and it is
	// the same sha256-of-the-token the plugin computed when the link was
	// minted — APP-PLUGINS-API.md: "queued on that document as the link's
	// creator (their ACL, their storage), with `params.page_token_hash`
	// added".
	params["page_token_hash"] = share.TokenHash(sh.Token)
	pb, _ := json.Marshal(params)
	// ⚠ Measured on the FINAL bytes — after the surface's override, share_id
	// and page_token_hash have been stamped in — because those bytes are what
	// the column has to hold. Measuring job.Params instead would let a surface
	// sit just under the line and cross it on the way to the INSERT.
	//
	// ⚠⚠ maxJobParamsBytes is the authenticated door's own ceiling, shared
	// rather than re-typed (app_plugins.go). This path had no ceiling at all,
	// which made the ANONYMOUS door the generous one.
	if len(pb) > maxJobParamsBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "params too large"})
		return
	}
	locale := publicLocale(r, h.DefaultLocale)
	j := &model.AppPluginJob{
		ID: wasmplugin.NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: action.ID,
		StorageID: storageID, PathsJSON: jsonString([]string{rel}), ParamsJSON: string(pb), ActorID: sh.CreatedBy, Locale: locale,
		Label: action.Label.Get(locale), Status: model.AppPluginJobPending,
	}
	if err := h.Store.CreateAppPluginJob(r.Context(), j); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "job: " + err.Error()})
		return
	}
	op, err := h.Apps.Ops.SubmitTo(r.Context(), ops.OpPluginAction, storageID, storageID, []string{rel}, j.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "queue: " + err.Error()})
		return
	}
	// ⚠ One column, not the row — see SetAppPluginJobOp.
	opID := op.ID
	j.OpID = &opID
	_ = h.Store.SetAppPluginJobOp(r.Context(), j.ID, opID)
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID: sh.CreatedBy, Action: "app_plugin.page_job", TargetType: "app_plugin", TargetID: p.Row.Name, IP: clientIP(r),
		Metadata: map[string]any{"action": action.ID, "share": sh.ID, "job": j.ID, "op": opID},
	})
	// The visitor learns that the job was accepted, not the queue row: the row
	// carries the creator's storage paths.
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job_id": j.ID})
}

// jobRefused is the ONE sentence a visitor gets when their submit is turned
// away for a reason that lives on the CREATOR's side of the link: the action
// was switched off or reserved for administrators, the plugin was stopped, the
// creator's own access to the document is gone, the folder is encrypted.
//
// ⚠⚠ One sentence for all of them, on purpose. The visitor is not a filex
// user and has no account here: "the administrator disabled sign.finalise" is
// not something they can act on, and telling a stranger holding a token WHICH
// of these is true hands them a probe into how this instance is configured and
// who may reach what (the file header's "NOTHING HERE LEAKS"). What they CAN
// act on is the last clause — go back to the person who sent the link — so
// that is where the sentence spends its words. `error` still separates the
// cases for a client that wants to log them; the human half does not.
//
// ⚠ English, like every other API string in this repo, and deliberately so
// here: the public shell renders the server's `message` verbatim when there is
// one (packages/core usePublicLink — PublicLinkError carries it into the
// surface's failure line, and its own words are only the fallback for a body
// without a message), so this text IS what the person reads. There is no
// per-code translation table on the client to route this through, and
// inventing one would leave half the public surface's words in Go and half in
// the package.
func (h *PublicAPI) jobRefused(w http.ResponseWriter, code int, kind string) {
	writeJSON(w, code, map[string]string{"error": kind,
		"message": "this link can no longer be used: the person who sent it can no longer start this step on the document — please ask them for a new link"})
}

// File serves a copy an app page exposed (`pub:N`), Range-capable.
func (h *PublicAPI) File(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDownload)
	if !ok {
		return
	}
	if !sh.IsApp() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if !h.unlocked(r, sh) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pin_required"})
		return
	}
	// ⚠⚠ THE DOCUMENT ITSELF. Stopping the surface while this route kept
	// handing the file over would be a closed door beside an open window:
	// the exposed copy IS what the outside participant was sent, and "the
	// account that shared it was switched off" has to reach the bytes or it
	// has not reached anything.
	//
	// ⚠ Deliberately NOT the same judgement the copies get about a stopped
	// APP. There the copies survive on purpose (see Share.renderAppPage and
	// wasmplugin.ExposedFiles: a visitor must be able to read the document
	// they were sent even on an instance whose plugin runtime is unavailable)
	// — the copies belong to the share, not to the running app. The person
	// who shared them is a different axis, and that one they do not survive.
	//
	// ⚠ After the PIN gate, like every other answer on this route: a locked
	// link must not become a way to find out anything about the instance.
	if _, live := linkCreator(r.Context(), h.Store, sh); !live {
		writeJSON(w, http.StatusGone, map[string]string{"error": "gone"})
		return
	}
	h.serveExposed(w, r, sh, pathParam(r, "ref"))
}

// PublicEntry is one row of a shared folder: what a visitor needs to decide
// whether to open it, and nothing about where it lives on the server.
type PublicEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // relative to the share's root
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
	Mime  string `json:"mime,omitempty"`
}

// Entries lists one directory of a shared FOLDER:
// GET /api/public/s/{token}/entries?path=<relative>
//
// A folder share that can only be downloaded whole is not a folder share —
// the no-JS page has browsed since the beginning, and the shell needs the
// same thing as data. The path is relative to the share's root and cannot
// leave it; the PIN gate, the expiry and the visit ceiling are the share's
// own, checked before anything is read.
func (h *PublicAPI) Entries(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDownload)
	if !ok {
		return
	}
	if sh.IsApp() {
		// An app page hands out copies, not a directory.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if !h.unlocked(r, sh) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pin_required"})
		return
	}
	node, err := h.Store.GetNode(r.Context(), sh.NodeID)
	if err != nil || node == nil || node.Type != model.NodeTypeDirectory {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	rel, ok := cleanShareRel(r.URL.Query().Get("path"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if h.Apps == nil || h.Apps.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	drv, err := h.Apps.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	objs, err := drv.List(r.Context(), joinShareRel(node.Path, rel))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	out := make([]PublicEntry, 0, len(objs))
	for _, o := range objs {
		if browseSkip(o.Name) {
			continue
		}
		child := o.Name
		if rel != "" {
			child = rel + "/" + o.Name
		}
		e := PublicEntry{Name: o.Name, Path: child, IsDir: o.Kind == storage.KindDirectory}
		if !e.IsDir {
			e.Size, e.Mime = o.Size, o.Mime
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": out})
}

// serveExposed streams one exposed copy. Shared with the no-JS page, which
// links straight at this route.
func (h *PublicAPI) serveExposed(w http.ResponseWriter, r *http.Request, sh *model.Share, ref string) {
	if !strings.HasPrefix(ref, "pub:") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	pf, fh, err := h.Apps.Registry.PageFile(sh, ref)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if pf.Mime != "" {
		w.Header().Set("Content-Type", pf.Mime)
	}
	// ⚠ Through httpx, never hand-built: a Turkish or CJK name puts bytes
	// above 127 into the header and a strict client (Electron's undici)
	// throws inside the response event, where the caller cannot catch it.
	// `?download=1` is a person TAKING the file (the page's Download button,
	// the no-JS page's link); without it the page is SHOWING it (its own
	// document viewer). Only the first is a download of an app link — its
	// visits are counted apart (00052_share_app_links), and the owner ruled on 2026-09-21
	// that looking at a signing link is not downloading it.
	download := r.URL.Query().Get("download") == "1"
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", httpx.ContentDisposition(disposition, pf.Name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Counted once per download, not per piece: a download manager may fetch
	// the same file in several Range requests, and only the one that starts
	// at the beginning (or asks for the whole) is a new download. Not capped:
	// the page's ceiling is on visits.
	if rg := r.Header.Get("Range"); download && r.Method == http.MethodGet && (rg == "" || strings.HasPrefix(rg, "bytes=0-")) {
		_ = h.Store.IncrementShareDownload(context.WithoutCancel(r.Context()), sh.ID)
	}
	http.ServeContent(w, r, pf.Name, st.ModTime(), fh)
}

// ─────────────────── the file request ───────────────────

// PublicDrop is GET /api/public/d/{token}: everything the upload screen needs
// and nothing about what is already in the folder ("blind drop").
type PublicDrop struct {
	Kind     string `json:"kind"` // always "drop"
	NeedsPIN bool   `json:"needs_pin"`
	Unlocked bool   `json:"unlocked"`
	Expired  bool   `json:"expired"`
	Revoked  bool   `json:"revoked"`
	Locked   bool   `json:"locked"`
	// Folder is the destination's NAME — never its path, and never a listing.
	Folder      string     `json:"folder,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	UploadsLeft *int       `json:"uploads_left"`
	Limits      dropLimits `json:"limits"`
}

type dropLimits struct {
	MaxFiles      int      `json:"max_files"`
	MaxFileSizeMB int      `json:"max_file_size_mb"`
	AllowedExt    []string `json:"allowed_ext"`
	AskName       bool     `json:"ask_name"`
}

// DropState answers a file-request link's state.
func (h *PublicAPI) DropState(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDrop)
	if !ok {
		return
	}
	now := time.Now()
	ds := parseDropSettings(sh.DropSettings)
	if ds.AllowedExt == nil {
		ds.AllowedExt = []string{}
	}
	out := PublicDrop{
		Kind:      "drop",
		NeedsPIN:  sh.PinHash != "",
		Unlocked:  h.unlocked(r, sh),
		Expired:   sh.ExpiresAt != nil && now.After(*sh.ExpiresAt),
		Locked:    sh.PinLocked(now),
		ExpiresAt: sh.ExpiresAt,
		Limits:    dropLimits{MaxFiles: ds.MaxFiles, MaxFileSizeMB: ds.MaxFileSizeMB, AllowedExt: ds.AllowedExt, AskName: ds.AskName},
	}
	if sh.MaxUploads != nil {
		left := *sh.MaxUploads - sh.UploadCount
		if left < 0 {
			left = 0
		}
		out.UploadsLeft = &left
		out.Revoked = left == 0
	}
	if node, err := h.Store.GetNode(r.Context(), sh.NodeID); err == nil && node != nil {
		out.Folder = node.Name
	} else {
		out.Revoked = true
	}
	writeJSON(w, http.StatusOK, out)
}

// DropUpload receives the files. The body, the limits, the quota accounting
// and the owner's notification are the file-request handler's — there is one
// ingest path, and the JSON screen does not get a second one.
func (h *PublicAPI) DropUpload(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	if h.Drop == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	h.Drop.handleDrop(w, r, pathParam(r, "token"))
}

// ─────────────────── shared plumbing ───────────────────

// pathParam reads a route parameter as the value the client MEANT. Every
// parameter of the public link surfaces — /api/public/*, /s/*, /d/* — is read
// through it, so no reader on this surface can be the one that forgot.
//
// ⚠⚠ chi.URLParam is not that value whenever the path carries an escape Go
// would not have written itself. chi routes on r.URL.RawPath when it is set,
// and net/url keeps RawPath exactly when the client's escaping differs from
// Go's own path encoding — so EVERY parameter of such a request comes back
// still encoded. `encodeURIComponent("pub:0")` is `pub%3A0`; Go's encoding
// leaves `:` alone, so RawPath is kept and the copy the signing page asked
// for was looked up as the literal string "pub%3A0". Measured 2026-09-21:
// GET …/file/pub%3A0 → 404, GET …/file/pub:0 → 200, and the outside signer's
// "See and approve" step said "The document could not be loaded". A file in a
// shared FOLDER whose name carries & + , ; = : @ $ met the same 404 through
// both of its links: the SPA encodes each segment with encodeURIComponent,
// the no-JS page with url.PathEscape, and both escape characters Go's path
// encoding does not.
//
// The CLIENT is right to encode a path segment — the segment grammar asks it
// to — so this is decided on the server, once. Only when chi really routed on
// RawPath, though: with RawPath empty the parameter is already decoded, and
// unescaping it a second time would turn a file literally named "100%41.pdf"
// into "100A.pdf".
//
// A malformed escape cannot reach a handler (net/http refuses the request
// line); if one ever did, the answer is "" and every caller already treats an
// empty or unknown value as "not found".
func pathParam(r *http.Request, key string) string {
	v := chi.URLParam(r, key)
	if r.URL.RawPath == "" {
		return v
	}
	d, err := url.PathUnescape(v)
	if err != nil {
		return ""
	}
	return d
}

// loadShare resolves a token to a live share of the wanted kind. It has
// answered when ok is false.
//
// ⚠ An unknown token, a token of the OTHER kind and a token whose node has
// gone are all 404 with the same body: which of the three it was is a fact
// about somebody else's data, and an anonymous caller must not be able to
// separate them. A link that existed and ran out is 410 — that one the
// visitor is entitled to, because they were given the link.
func (h *PublicAPI) loadShare(w http.ResponseWriter, r *http.Request, wantKind string) (*model.Share, bool) {
	token := strings.ToLower(strings.TrimSpace(pathParam(r, "token")))
	sh, err := h.Store.GetShareByToken(r.Context(), token)
	if err != nil || sh == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return nil, false
	}
	switch wantKind {
	case model.ShareKindDrop:
		if !sh.IsDrop() {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return nil, false
		}
	case model.ShareKindDownload:
		if sh.IsDrop() {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return nil, false
		}
	}
	if sh.IsExpired(time.Now()) {
		// The shape is the live one so a client renders "this link is gone"
		// from the same object it renders everything else from.
		writeJSON(w, http.StatusGone, h.describe(r, sh, false))
		return nil, false
	}
	return sh, true
}

// loadApp resolves a token to a live APP share and its running plugin.
func (h *PublicAPI) loadApp(w http.ResponseWriter, r *http.Request) (*model.Share, *wasmplugin.Installed, bool) {
	if h.Apps == nil || h.Apps.Registry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return nil, nil, false
	}
	sh, p, err := h.Apps.Registry.LoadPage(r.Context(), pathParam(r, "token"))
	switch {
	case errors.Is(err, wasmplugin.ErrPageGone):
		writeJSON(w, http.StatusGone, map[string]string{"error": "gone"})
		return nil, nil, false
	case err != nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return nil, nil, false
	}
	// ⚠⚠ The creator's account is checked HERE, at the door, and not only
	// where the job is queued — which is where the first draft of this put
	// it. Two reasons, and the second is not cosmetic:
	//
	//  1. A refusal that waits for the submit makes the signer read the
	//     document, fill the form and draw a signature before being told the
	//     link was dead the whole time.
	//  2. ⭐ `page_event` RUNS before it can answer with a job, and a signing
	//     app writes as it goes — the fixture app does exactly what a real
	//     one does: `PublicPageStateSet` + `StateSet("in:0", "signed_by", ...)`
	//     on submit, THEN asks for the job. Gate only the job and a dead
	//     link still records that the visitor signed, and only the step that
	//     would finalise it is refused. The app's record then says a
	//     signature was made that no job will ever land.
	//
	// The answer is the one every other dead app link gets (410 `gone`, no
	// detail), so the shell draws its own "this link is not available"
	// screen IN THE VISITOR'S LANGUAGE rather than an English sentence from
	// the server, and a stranger holding a token cannot tell this reason
	// apart from a stopped app or a lapsed link.
	if _, live := linkCreator(r.Context(), h.Store, sh); !live {
		writeJSON(w, http.StatusGone, map[string]string{"error": "gone"})
		return nil, nil, false
	}
	return sh, p, true
}

// unlocked reports whether this browser has answered the link's PIN. A link
// with no PIN is always unlocked.
func (h *PublicAPI) unlocked(r *http.Request, sh *model.Share) bool {
	return shareUnlocked(h.Service, r, sh)
}

// shareUnlocked is the ONE reader of the unlock cookie — the no-JS pages ask
// the same question and must get the same answer.
func shareUnlocked(svc *share.Service, r *http.Request, sh *model.Share) bool {
	if sh == nil || sh.PinHash == "" {
		return true
	}
	if svc == nil {
		return false
	}
	c, err := r.Cookie(share.CookieName(sh.Token))
	return err == nil && svc.VerifyUnlock(sh.Token, c.Value)
}
