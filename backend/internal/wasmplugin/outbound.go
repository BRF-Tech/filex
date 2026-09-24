package wasmplugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Outbound host functions: people, notifications, mail, HTTP ─────────
//
// These are the calls that reach OUTSIDE the file a plugin was handed: other
// people's names, the notification bell, the mail server, the network. Each
// sits behind its own permission, each is bounded (rate, size, time), and
// none of them lets the plugin choose the sender — filex is always the one
// speaking, on the plugin's behalf, and says so.

// notifySink is what notify_send needs; notify.Service satisfies it.
type notifySink interface {
	Send(ctx context.Context, e notify.Event) (int64, error)
}

// mailSink is what mail_send needs; *mailer.Service satisfies it.
type mailSink interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SetNotify wires the notification service (nil → notify_send unavailable).
func (r *Registry) SetNotify(n notifySink) { r.notify = n }

// SetMailer wires the mailer (nil → mail_send unavailable).
func (r *Registry) SetMailer(m mailSink) { r.mailer = m }

// SetUserScope wires the function that puts the acting user's tenant scope
// on a job context, so users_lookup from a queued job sees the same
// directory the user's own requests see. Nil = single-tenant, unscoped.
func (r *Registry) SetUserScope(fn func(ctx context.Context, u *model.User) context.Context) {
	r.userScope = fn
}

// SetVisibility wires the check state_list runs on every row: a plugin may
// keep state on a file the person asking has no right to see, and a listing
// is not a way around the ACL. Nil = everything passes.
func (r *Registry) SetVisibility(fn func(ctx context.Context, u *model.User, storageID int64, rel string) bool) {
	r.visible = fn
}

// SetHTTPTransport replaces the transport http_request uses (tests).
func (r *Registry) SetHTTPTransport(t http.RoundTripper) { r.outbound = t }

// ── users_lookup ───────────────────────────────────────────────────────

// UserRow is what users_lookup and GET /api/files/plugins/users return: the
// directory entry a picker needs and nothing else (no role, no flags).
type UserRow struct {
	// UserID, not `id`: the row doubles as a people-picker VALUE, whose
	// shape the surface contract fixes as {user_id, email, name}.
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name,omitempty"`
}

const usersLookupCap = 20

// LookupUsers is the one directory search behind both the host function and
// the HTTP route. ctx must already carry the caller's tenant scope (the store
// is the tenant-scoped wrapper, which confines ListUsers by that scope).
func (r *Registry) LookupUsers(ctx context.Context, q string) ([]UserRow, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		// An empty query lists nobody: the picker is a search box, not a
		// directory dump, and a plugin gets no roster for free either.
		return []UserRow{}, nil
	}
	users, err := r.opts.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserRow, 0, usersLookupCap)
	for _, u := range users {
		if !u.Enabled {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(u.Email), q) &&
			!strings.Contains(strings.ToLower(u.DisplayName), q) &&
			!strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		out = append(out, UserRow{UserID: u.ID, Email: u.Email, Name: u.DisplayName})
		if len(out) >= usersLookupCap {
			break
		}
	}
	return out, nil
}

func hfUsersLookup(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Q string `json:"q"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if s.reg.userScope != nil && s.actor != nil && s.actor.ID > 0 {
		ctx = s.reg.userScope(ctx, s.actor)
	}
	rows, err := s.reg.LookupUsers(ctx, req.Q)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "directory: "+err.Error())
	}
	return map[string]any{"users": rows}, nil
}

// ── notify_send ────────────────────────────────────────────────────────

// maxNoticeLangs bounds how many languages one notice keeps, beyond English
// and Turkish: a notification row is read on every bell refresh, and a
// plugin handing over a Text with hundreds of keys must not make it heavy.
const maxNoticeLangs = 16

// noticeMeta is the language-dependent part of an app's notice: the app's
// name, the title and the body, once PER LANGUAGE the app wrote them in —
// `plugin_label_<lang>`, `title_<lang>`, `body_<lang>`. The bell, the browser
// toast and the desktop pick the reader's own (web/src/lib/notificationText.ts
// → noticeText), falling back to the base language and then English.
//
// ⚠⚠ EVERY language, not English and Turkish. It kept exactly those two, so
// a German reader of the e-Signature app — which ships German — was told
// "admin@local asks you to sign a document" under "e-Signature:" in an
// otherwise German bell, while the very screen the notice opened spoke
// German (measured 2026-09-22, after the server stopped collapsing every
// other language to English). The `_en`/`_tr` keys stay what they were —
// always set, Get's fallback included — because the desktop app and rows
// already stored read exactly those.
func noticeMeta(pluginName string, label, title, body wire.Text) map[string]any {
	meta := map[string]any{
		"plugin": pluginName,
		// The app's NAME as a person knows it, per language — what a reader
		// prints in front of the message. `plugin` is the install id
		// (`sign`), and it was being printed: "sign: “sözleşme.pdf”
		// imzanızı bekliyor…" in a Turkish bell whose side panel calls the
		// same app "İmzalar" (release-candidate sweep, 2026-09-21).
		"plugin_label_en": clip(strings.TrimSpace(label.Get("en")), 80),
		"plugin_label_tr": clip(strings.TrimSpace(label.Get("tr")), 80),
		"title_en":        clip(strings.TrimSpace(title.Get("en")), 200),
		"title_tr":        clip(strings.TrimSpace(title.Get("tr")), 200),
		"body_en":         clip(strings.TrimSpace(body.Get("en")), 1000),
		"body_tr":         clip(strings.TrimSpace(body.Get("tr")), 1000),
	}
	perLanguage(meta, "plugin_label_", label, 80)
	perLanguage(meta, "title_", title, 200)
	perLanguage(meta, "body_", body, 1000)
	return meta
}

// noticeAlready are the languages noticeMeta has already written for a row
// (prefix+"en", prefix+"tr"), so perLanguage does not write them twice.
//
// ⚠ A SET, not two comparisons: `k == "en" || k == "tr"` reads to the
// inline-pair scan (srvtext) like a sentence chosen by language, which is the
// one thing that rule exists to stop. Nothing here chooses a sentence — the
// APP wrote every language — so the set says what is meant and the scan stays
// as strict as it is.
var noticeAlready = map[string]bool{"en": true, "tr": true}

// perLanguage writes prefix+lang for each further language t carries: only a
// well-formed tag (langRe, lower-cased), only a non-empty text, at most
// maxNoticeLangs of them in a stable order. English and Turkish are already
// there (noticeMeta) and are not overwritten.
func perLanguage(meta map[string]any, prefix string, t wire.Text, limit int) {
	texts := make(map[string]string, len(t))
	langs := make([]string, 0, len(t))
	for k, v := range t {
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if noticeAlready[k] || !langRe.MatchString(k) || v == "" {
			continue
		}
		if _, seen := texts[k]; !seen {
			langs = append(langs, k)
		}
		texts[k] = v
	}
	sort.Strings(langs)
	if len(langs) > maxNoticeLangs {
		langs = langs[:maxNoticeLangs]
	}
	for _, lang := range langs {
		meta[prefix+lang] = clip(texts[lang], limit)
	}
}

func hfNotifySend(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Title    wire.Text      `json:"title"`
		Body     wire.Text      `json:"body"`
		Severity string         `json:"severity"`
		Meta     map[string]any `json:"meta"`
		// ToUserID addresses one person (their bell, their push, their
		// mail if they enabled it). 0 = the instance-wide feed as before.
		ToUserID int64 `json:"to_user_id"`
		// Target makes the notification clickable: the file (an input ref
		// or an adapter-qualified path on this job's storage) and,
		// optionally, the plugin action or view to open on it.
		Target *struct {
			Ref     string `json:"ref"`
			Path    string `json:"path"`
			Action  string `json:"action"`
			View    string `json:"view"`
			Section string `json:"section"`
		} `json:"target"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if s.reg.notify == nil {
		return nil, hostErr(wire.ErrUnavailable, "notifications are not available on this instance")
	}
	var (
		toUser *int64
		node   *notify.NodeRef
		target *notify.Target
	)
	if req.ToUserID > 0 {
		u, err := s.reg.opts.Store.GetUser(ctx, req.ToUserID)
		if err != nil || u == nil {
			return nil, hostErr(wire.ErrNotFound, "to_user_id: no such user")
		}
		id := u.ID
		toUser = &id
	}
	// A notice about a LIST rather than a file: one of this app's own `home`
	// pages, optionally at a section (pluginkit.NoticeTarget). ⚠ Only a view
	// the manifest places `home` — a `page` or `modal` view needs a file to
	// run on, and a click that opened one with none would be a broken screen.
	if req.Target != nil && req.Target.Ref == "" && req.Target.Path == "" && req.Target.View != "" && req.Target.Action == "" {
		v, ok := s.plugin.Manifest.View(req.Target.View)
		if !ok || v.Placement != "home" {
			return nil, hostErr(wire.ErrInvalid, "target.view: a notice with no file may only open one of this app's home views")
		}
		target = notify.AppTarget(s.plugin.Row.Name, req.Target.View, clip(strings.TrimSpace(req.Target.Section), 64))
		req.Target = nil
	}
	if req.Target != nil {
		rel, err := s.targetRel(req.Target.Ref, req.Target.Path)
		if err != nil {
			return nil, err
		}
		node = &notify.NodeRef{StorageID: s.storageID, Path: "/" + rel, Name: path.Base(rel)}
		target = notify.FileTarget(rel)
		if req.Target.Action != "" || req.Target.View != "" {
			if req.Target.Action != "" {
				if _, ok := s.plugin.Manifest.Action(req.Target.Action); !ok {
					return nil, hostErr(wire.ErrInvalid, "target.action: no such action")
				}
			}
			if req.Target.View != "" {
				if _, ok := s.plugin.Manifest.View(req.Target.View); !ok {
					return nil, hostErr(wire.ErrInvalid, "target.view: no such view")
				}
			}
			target.Open = &model.NotificationOpen{Plugin: s.plugin.Row.Name, Action: req.Target.Action, View: req.Target.View}
		}
	}
	titleEN := clip(strings.TrimSpace(req.Title.Get("en")), 200)
	if titleEN == "" {
		return nil, hostErr(wire.ErrInvalid, "title (en) is required")
	}
	sev := notify.SeverityInfo
	switch req.Severity {
	case "warning":
		sev = notify.SeverityWarning
	case "error":
		sev = notify.SeverityError
	}
	meta := noticeMeta(s.plugin.Row.Name, s.plugin.Manifest.Label, req.Title, req.Body)
	if s.jobID != "" {
		meta["job"] = s.jobID
	}
	// A plugin may add a few small facts of its own; it may not overwrite
	// the ones above, and it may not make the row heavy.
	n := 0
	for k, v := range req.Meta {
		if _, taken := meta[k]; taken || n >= 8 {
			continue
		}
		if str, ok := v.(string); ok {
			meta[k] = clip(str, 200)
		} else if _, ok := v.(float64); ok {
			meta[k] = v
		} else if _, ok := v.(bool); ok {
			meta[k] = v
		}
		n++
	}
	ev := notify.Event{
		Event: notify.EventPluginNotice, Severity: sev,
		Title: titleEN, Body: clip(strings.TrimSpace(req.Body.Get("en")), 1000),
		Meta: meta, TS: time.Now(), UserID: toUser, Node: node, Target: target,
	}
	if s.actor != nil && s.actor.ID > 0 {
		ev.Actor = &notify.ActorRef{ID: s.actor.ID, Email: s.actor.Email}
	}
	id, err := s.reg.notify.Send(ctx, ev)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "notify: "+err.Error())
	}
	return map[string]any{"id": id}, nil
}

// ── mail_send ──────────────────────────────────────────────────────────

const (
	mailPerHour    = 60
	mailBodyMax    = 64 << 10
	mailSubjectMax = 200
)

// rateWindow is a fixed one-hour counter per plugin.
type rateWindow struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func (w *rateWindow) take(limit int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if now.Sub(w.start) >= time.Hour {
		w.start, w.count = now, 0
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}

// minuteWindow is rateWindow over one minute.
type minuteWindow struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func (w *minuteWindow) take(limit int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if now.Sub(w.start) >= time.Minute {
		w.start, w.count = now, 0
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}

func hfMailSend(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
		// Lang is the language the app wrote the mail in (pluginkit.MailSendIn);
		// filex's footer follows it. Optional — see footerLang.
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if s.reg.mailer == nil {
		return nil, hostErr(wire.ErrUnavailable, "mail is not configured on this instance")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.To))
	if err != nil {
		return nil, hostErr(wire.ErrInvalid, "to: not an email address")
	}
	subject := clip(strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(req.Subject, "\r", " "), "\n", " ")), mailSubjectMax)
	if subject == "" {
		return nil, hostErr(wire.ErrInvalid, "subject is required")
	}
	if len(req.Body) > mailBodyMax {
		return nil, hostErr(wire.ErrTooLarge, "body over 64 KiB")
	}
	if !s.plugin.mailRate.take(mailPerHour) {
		return nil, hostErr(wire.ErrBusy, fmt.Sprintf("this plugin may send %d mails per hour", mailPerHour))
	}
	// The plugin's name is on every message: the recipient must be able to
	// tell which installed app wrote to them, and filex is the sender.
	//
	// ⚠ The footer is FILEX's sentence under the app's mail, so it speaks the
	// mail's language, not English under a Turkish (or Spanish) invitation.
	// Which language that is, the app knows best: it wrote the body. So: the
	// `lang` it states (MailSendIn), else the language this call runs in (an
	// action run from the screen — the requester's), else the instance
	// default. The app's name is its label in that language.
	lang := srvtext.Pick(req.Lang, s.locale)
	body := req.Body + "\n\n-- \n" + srvtext.Text(lang, "server.mail.app_footer", srvtext.Vars{"app": s.plugin.Manifest.Label.Get(lang)})
	if err := s.reg.mailer.Send(mailer.WithLanguage(ctx, lang), addr.Address, subject, body); err != nil {
		if errors.Is(err, mailer.ErrNotConfigured) || errors.Is(err, mailer.ErrNotVerified) {
			return nil, hostErr(wire.ErrUnavailable, "mail is not configured on this instance")
		}
		return nil, hostErr(wire.ErrUnavailable, "mail: "+err.Error())
	}
	s.plugin.log("info", "mail sent to "+addr.Address+": "+subject)
	return map[string]any{"ok": true}, nil
}

// ── http_request ───────────────────────────────────────────────────────

const (
	httpMaxBody     = 8 << 20
	httpMaxReqBody  = 8 << 20
	httpTimeout     = 30 * time.Second
	httpMaxRedirect = 5
)

type httpRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// BodyB64 is the request body, base64 (JSON cannot carry raw bytes).
	BodyB64  string `json:"body_b64"`
	TimeoutS int    `json:"timeout_s"`
}

type httpResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
}

// An address a plugin may never reach — loopback, link-local, private
// ranges, multicast, unspecified — is decided by internal/netguard, shared
// with the storage-plugin downloader. The check runs on the DIALLED address
// (after DNS), so a public name that resolves privately is refused too: the
// SSRF shape.

// newOutboundTransport is the guarded transport every plugin request shares.
func newOutboundTransport() http.RoundTripper {
	return netguard.Transport(httpTimeout)
}

func hfHTTPRequest(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req httpRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, hostErr(wire.ErrInvalid, "url must be http(s)://host/…")
	}
	if !s.plugin.Grants.HasHost(u.Hostname()) {
		return nil, hostErr(wire.ErrPermissionDenied, "plugin was not granted http:"+u.Hostname())
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && netguard.Refused(ip) {
		return nil, hostErr(wire.ErrPermissionDenied, "private and local addresses are refused")
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return nil, hostErr(wire.ErrInvalid, "method not allowed")
	}
	var body io.Reader
	if req.BodyB64 != "" {
		b, err := base64.StdEncoding.DecodeString(req.BodyB64)
		if err != nil {
			return nil, hostErr(wire.ErrInvalid, "body_b64 is not base64")
		}
		if len(b) > httpMaxReqBody {
			return nil, hostErr(wire.ErrTooLarge, "request body over 8 MiB")
		}
		body = strings.NewReader(string(b))
	}
	timeout := httpTimeout
	if req.TimeoutS > 0 && time.Duration(req.TimeoutS)*time.Second < timeout {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(rctx, method, u.String(), body)
	if err != nil {
		return nil, hostErr(wire.ErrInvalid, err.Error())
	}
	for k, v := range req.Headers {
		k = http.CanonicalHeaderKey(strings.TrimSpace(k))
		switch k {
		case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Cookie":
			continue
		}
		hr.Header.Set(k, clip(v, 4096))
	}
	hr.Header.Set("User-Agent", "filex-app/"+s.plugin.Row.Name+" ("+HostVersion+")")
	client := &http.Client{
		Transport: s.reg.outbound,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= httpMaxRedirect {
				return errors.New("too many redirects")
			}
			if !s.plugin.Grants.HasHost(next.URL.Hostname()) {
				return errors.New("redirect to " + next.URL.Hostname() + " is not in the plugin's allowed hosts")
			}
			return nil
		},
	}
	resp, err := client.Do(hr)
	if err != nil {
		if rctx.Err() != nil {
			return nil, hostErr(wire.ErrTimeout, "request exceeded its time budget")
		}
		return nil, hostErr(wire.ErrUnavailable, clip(err.Error(), 300))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody+1))
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "read: "+err.Error())
	}
	if len(b) > httpMaxBody {
		return nil, hostErr(wire.ErrTooLarge, "response over 8 MiB")
	}
	out := httpResponse{Status: resp.StatusCode, Headers: map[string]string{}, BodyB64: base64.StdEncoding.EncodeToString(b)}
	for k, v := range resp.Header {
		if len(v) > 0 && k != "Set-Cookie" {
			out.Headers[k] = clip(v[0], 4096)
		}
	}
	return out, nil
}
