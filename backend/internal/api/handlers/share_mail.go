package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// "Send by e-mail" for a public link, and the same words for the OS share
// sheet.
//
// ⚠⚠ The server writes the whole message from the LINK. The request names a
// link (its token) and who it goes to; the link's address, its expiry, the
// item's name, kind and size, a file request's limits and the language's
// sentences are all read here, from the share row, its node and the server
// catalogue. Until 0.54 the request carried the address, the PIN, the expiry,
// the kind and the size and the server mailed them as given; those fields are
// not read any more, and a request that still sends them is answered from the
// link exactly like one that does not.
//
// ⚠ A PIN is never in this mail. The server keeps a link's PIN only as a hash
// (and a sealed copy reserved for the audited "show the PIN again" endpoint),
// so a PIN here could only be one the request named, and a PIN next to the
// link in the same message would let whoever reads the mail open the link —
// the PIN would guard nothing. A link that has one says so, and the sender
// gives the PIN another way. (Grants.Invite is the one mail with a PIN: the
// server made that link, and its PIN, a moment before.)

const (
	// shareMailMaxRecipients caps the addresses of one send.
	shareMailMaxRecipients = 20
	// shareMailHourlyRecipients caps the addresses one account mails links to
	// in an hour, every send counted address by address.
	shareMailHourlyRecipients = 100
)

// shareMailReq is all a share mail carries. `share` is the link's token (what
// POST /api/files/share answers as `token`), `share_id` its numeric id (what
// GET /api/files/share lists as `uuid`) — one of the two.
type shareMailReq struct {
	Share   string   `json:"share"`
	ShareID int64    `json:"share_id,omitempty"`
	Email   string   `json:"email"`            // single recipient (back-compat)
	Emails  []string `json:"emails,omitempty"` // multiple recipients
	// Locale is the language a person PICKED for the recipients in the form
	// - the mail's for an address no account owns; absent: the server's
	// (FILEX_DEFAULT_LOCALE). ⚠ Never the sender's screen language (#191).
	Locale string `json:"locale,omitempty"`
}

// mailableShare is a link the caller may mail, with what the message is
// built from.
type mailableShare struct {
	share *model.Share
	node  *model.Node
	rel   string
}

// parseRecipients merges the single `email` + `emails[]` inputs, splitting each
// on commas/semicolons/whitespace/newlines, lowercasing, validating (@) and
// deduping — so one textarea of addresses or a chips array both work.
func parseRecipients(single string, list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, chunk := range append([]string{single}, list...) {
		for _, part := range strings.FieldsFunc(chunk, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			e := strings.ToLower(strings.TrimSpace(part))
			if e == "" || !strings.Contains(e, "@") || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// publicLinkOf is a link's public address on base (internal/tenanturl): /d/
// for a file request, /s/ for every other link — the address the link was
// given when it was made (Share.HandleCreate).
func publicLinkOf(base string, sh *model.Share) string {
	if sh.IsDrop() {
		return base + "/d/" + sh.Token
	}
	return base + "/s/" + sh.Token
}

// linkDaysLeft is how many days a link still has, for a mail's validity line:
// 0 for a link that does not expire, and at least 1 for one that does — a
// link that ends in five hours is "valid for 1 day", never "does not expire".
func linkDaysLeft(expires *time.Time, now time.Time) int {
	if expires == nil {
		return 0
	}
	days := int(expires.Sub(now).Hours()/24 + 0.5)
	if days < 1 {
		days = 1
	}
	return days
}

// mailLimiter is the per-account hourly budget of share-mail addresses.
func (h *Grants) mailLimiter() *ipLimiter {
	h.mailRateOnce.Do(func() { h.mailRate = newIPLimiter(shareMailHourlyRecipients, time.Hour) })
	return h.mailRate
}

// mailableShare loads the link a share mail (or its message) names and
// answers whether the caller may send it, writing the refusal when not.
//
// The link has to be one the caller MANAGES — the rule that decides who may
// revoke it (shareRevokeRefusal: its creator or an administrator, inside the
// caller's tenant and token root) — still working, and the caller has to hold
// what making it took on its item today: editor there and share.links
// (share.upload_links for a file request).
func (h *Grants) mailableShare(w http.ResponseWriter, r *http.Request, token string, id int64, lang string) (*mailableShare, bool) {
	ctx := r.Context()
	var (
		sh  *model.Share
		err error
	)
	switch token = strings.ToLower(strings.TrimSpace(token)); {
	case token != "":
		sh, err = h.Store.GetShareByToken(ctx, token)
	case id > 0:
		sh, err = h.Store.GetShareByID(ctx, id)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing share"})
		return nil, false
	}
	if err != nil || sh == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	switch err := shareRevokeRefusal(ctx, h.Store, sh); {
	case errors.Is(err, errShareHidden):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	case err != nil:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return nil, false
	}
	// An app's page is the app's to send (a signing request mails its own).
	if sh.IsApp() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "share_not_mailable"})
		return nil, false
	}
	if sh.IsExpired(time.Now()) {
		writeJSON(w, http.StatusGone, map[string]string{
			"error": "link_ended", "message": srvtext.Text(lang, "server.share_mail.link_ended", nil),
		})
		return nil, false
	}
	node, err := h.Store.GetNode(ctx, sh.NodeID)
	if err != nil || node == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	st, err := h.Store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil || !scopeOf(ctx).CanAccessStorage(st.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	rel := acl.CleanRel(node.Path)
	need := perm.ShareLinks
	if sh.IsDrop() {
		need = perm.ShareUploadLinks
	}
	if !h.requireEditor(w, r, st, rel, need) {
		return nil, false
	}
	return &mailableShare{share: sh, node: node, rel: rel}, true
}

// composeShareMail is the message for m in lang: subject, body, and whether
// the link has a PIN the message leaves out. ONE builder for the e-mail and
// the OS share sheet (ShareMessage), so the two never say different things.
func (h *Grants) composeShareMail(ctx context.Context, r *http.Request, lang string, m *mailableShare) (string, string, bool) {
	sh, node := m.share, m.node
	link := publicLinkOf(h.Tenants.FromRequest(r), sh)
	days := linkDaysLeft(sh.ExpiresAt, time.Now())
	withheld := sh.PinHash != "" || sh.HasPin
	site := h.siteName(ctx)
	name := node.Name
	if name == "" {
		name = baseName(m.rel)
	}
	if sh.IsDrop() {
		ds := parseDropSettings(sh.DropSettings)
		subject, body := dropInviteMailText(lang, site, name, link, "", withheld, days, ds.MaxFiles, ds.MaxFileSizeMB, ds.AllowedExt)
		return subject, body, withheld
	}
	isDir := node.Type == model.NodeTypeDirectory
	size := int64(0)
	if !isDir {
		size = node.Size
	}
	subject, body := shareMailText(lang, site, name, isDir, size, link, "", withheld, days)
	return subject, body, withheld
}

// ShareMail e-mails a link the caller made to one or many addresses, each in
// its RECIPIENT's language (recipientLang - translated at the last stop, #191):
// an address that is somebody's account reads its owner's language, a bare
// address the language the form sent (`locale`), else the instance's.
// Best-effort: {emailed:false} with 503 when SMTP is not verified, so the
// dialog keeps showing the link for manual delivery. Every answer carries
// `message`, the sentence for the composer, in their language.
//
//	POST /api/files/permissions/share-mail {share | share_id, email | emails[], locale?}
func (h *Grants) ShareMail(w http.ResponseWriter, r *http.Request) {
	var req shareMailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// The composer's answers (`message`) are in the composer's language (the
	// account's); the mails below are in each recipient's - `locale` is the
	// recipients', not the composer's.
	lang := requestLang(r)
	m, ok := h.mailableShare(w, r, req.Share, req.ShareID, lang)
	if !ok {
		return
	}
	recipients := parseRecipients(req.Email, req.Emails)
	if len(recipients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "valid email required", "message": srvtext.Text(lang, "server.share_mail.no_recipient", nil),
		})
		return
	}
	if len(recipients) > shareMailMaxRecipients {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "too_many_recipients", "max": shareMailMaxRecipients,
			"message": srvtext.Plural(lang, "server.share_mail.too_many", shareMailMaxRecipients, nil),
		})
		return
	}
	if h.Mailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"emailed": false, "error": "not_configured", "message": srvtext.Text(lang, "server.share_mail.not_configured", nil),
		})
		return
	}
	who := "share-mail:"
	if u := auth.UserFrom(r.Context()); u != nil {
		who += strconv.FormatInt(u.ID, 10)
	}
	if !h.mailLimiter().allowN(who, len(recipients)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"emailed": false, "error": "rate_limited", "message": srvtext.Text(lang, "server.share_mail.rate_limited", nil),
		})
		return
	}
	type mailText struct{ subject, body string }
	texts := map[string]mailText{}
	withheld := false
	textIn := func(mailLang string) mailText {
		if t, ok := texts[mailLang]; ok {
			return t
		}
		var t mailText
		t.subject, t.body, withheld = h.composeShareMail(r.Context(), r, mailLang, m)
		texts[mailLang] = t
		return t
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(m.share.ID, 10), m.node.Path)

	var sent, failed []string
	reason := ""
	for _, email := range recipients {
		mailLang := recipientLang(r.Context(), h.Store, email, req.Locale)
		t := textIn(mailLang)
		if err := h.Mailer.Send(mailer.WithLanguage(r.Context(), mailLang), email, t.subject, t.body); err != nil {
			// Distinguish "SMTP not set up / not verified" (show the link) from a
			// transient send failure (worth retrying) so the UI can say which.
			reason = "send_failed"
			if errors.Is(err, mailer.ErrNotConfigured) || errors.Is(err, mailer.ErrNotVerified) {
				reason = "not_configured"
			}
			failed = append(failed, email)
			continue
		}
		sent = append(sent, email)
	}
	if len(sent) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"emailed": false, "error": reason, "failed": failed,
			"message": srvtext.Text(lang, "server.share_mail."+reason, nil),
		})
		return
	}
	said := srvtext.Plural(lang, "server.share_mail.sent", len(sent), nil)
	if len(failed) > 0 {
		said = srvtext.Text(lang, "server.share_mail.partial", srvtext.Vars{
			"sent": strconv.Itoa(len(sent)), "failed": strconv.Itoa(len(failed)),
		})
	}
	if withheld {
		said += " " + srvtext.Text(lang, "server.share_mail.pin_withheld", nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"emailed": true, "sent": sent, "failed": failed, "pin_withheld": withheld, "message": said,
	})
}

// ShareMessage answers the share mail's words without sending anything — what
// the dialog hands the OS share sheet (WhatsApp, Mail, …), so a forwarded link
// reads exactly like an e-mailed one, and like it carries no PIN.
//
// The words are for whoever the sheet sends them to - nobody the server
// knows - so they are in the language PICKED for the recipient (`lang`), else
// the server's (FILEX_DEFAULT_LOCALE), never the sender's screen (#191). A
// refusal is for the sender, in the sender's language.
//
//	GET /api/files/permissions/share-message?share=<token>&lang=<tag>   (or ?share_id=<id>)
func (h *Grants) ShareMessage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lang := requestLang(r)
	id, _ := strconv.ParseInt(q.Get("share_id"), 10, 64)
	m, ok := h.mailableShare(w, r, q.Get("share"), id, lang)
	if !ok {
		return
	}
	subject, body, withheld := h.composeShareMail(r.Context(), r, srvtext.Pick(q.Get("lang")), m)
	writeJSON(w, http.StatusOK, map[string]any{"subject": subject, "body": body, "pin_withheld": withheld})
}
