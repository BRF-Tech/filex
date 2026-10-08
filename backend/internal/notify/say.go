package notify

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// What a notification SAYS - composed here, on the server, in Go, and nowhere
// else (the maintainers' decision, 2026-10-08: "the browser sends a key and
// its values; the server builds the sentence and sends it where it goes").
//
// ⚠⚠ ONE code path for every channel. The bell list and the page's pop-up
// (GET /api/notifications, handlers/notifications.go → SayRows), the desktop
// app's native toast (it reads the same list), a Web Push (push.go), the
// emails (mailNow, mailDigest) and the webhooks' `title`/`body` (dispatch) all
// call Say. Before this, the sentence was composed by each reader's screen
// (packages/core lib/notificationText.ts) and a second time in Go for a push:
// a Turkish bell said "Yeni dosya: rapor.pdf" while the phone said "rapor.pdf
// - Rapor: 1 dosya eklendi", and the operator alarms went to the phone in
// their emitter's English.
//
// A row keeps what HAPPENED - its event and its meta (node, actor, share,
// target, counts, an app's per-language words) - and its emitter's own title
// and body, which are only a fallback. The sentence is made when it is READ,
// in the reader's language, so two people reading one row each read it in
// their own, and a language changed or a language pack installed changes
// every row at once.
//
// THE WORDS are the server catalogue's `server.notify.*` keys
// (internal/srvtext locales, one file per language; a language pack's
// `ui_locales[<lang>]` overlays or adds a language): `<event>.title` and
// `<event>.body`, a plural form as `<field>_<category>`, a single encrypted
// FILE's wording as `<field>_file`, a NO to an encryption request as
// `<field>_rejected`, words the server phrased per language at the time
// (`meta.title_<lang>`) as `<field>_noticed`; the digest's phrases under
// `digest.`; the words a sentence falls back on under `word.`.
//
// ⚠ Every placeholder below is a field the emitters ACTUALLY set - a sentence
// built on a field that is not there renders as a hole. An unresolved
// placeholder is removed together with the punctuation that held it ("{sig}
// - {path}" with no signature reads "/a/b.txt", never "- /a/b.txt").
//
// RIGHT TO LEFT. In a right-to-left language every value placed in a sentence
// (a name, a path, a reason, an app's own words) is wrapped in FIRST STRONG
// ISOLATE … POP DIRECTIONAL ISOLATE, the rule the explorer's own t() follows:
// its own letters decide its direction, and a path's leading slash cannot jump
// to the far side of the line. A catalogue cannot do it (a translation may not
// carry bidi controls), so the server does, once, for every channel.
//
// ⚠⚠ THE ONE THING THE SERVER CANNOT SAY. Inside an end-to-end encrypted
// folder whose names are encrypted, the server has only ciphertext names. The
// sentence is still made here; the name stands in it as "🔒 Encrypted item"
// (`word.locked`), and the row read through the API also carries where it
// stands (model.NotificationE2E) so a browser that has the folder unlocked
// swaps in the real name - it composes nothing. A push, an email, the desktop
// app's toast and a webhook have no key and say the lock word.

// Said is one notification as a reader reads it.
type Said struct {
	// Title and Body are the sentence, in the reader's language, with no
	// encrypted name in them (the lock word stands there).
	Title string
	Body  string
	// Lines is Body one line at a time - a digest's folders, which the bell
	// says on one line ("; ") and an email one under the other.
	Lines []string
	// E2E is where the encrypted names stand, for a reader that can name them;
	// nil when the sentence names none.
	E2E *model.NotificationE2E
}

// Say is row n in lang - any tag; srvtext.Pick decides which language serves
// it (a pack's, the instance default's, English).
func Say(lang string, n *model.Notification) Said {
	if n == nil {
		return Said{}
	}
	c := newComposer(lang)
	var meta map[string]any
	if len(n.MetaJSON) > 0 {
		_ = json.Unmarshal(n.MetaJSON, &meta)
	}
	title, body, lines := c.say(n.Event, n.Title, n.Body, meta, targetOf(n.Target, meta))
	return c.said(title, body, lines)
}

// SayEvent is an event that has not been read back from the store - an email
// sent as the row is written, a webhook's body - said exactly as its row will
// be.
func SayEvent(lang string, e Event) Said {
	raw, err := marshalMeta(e)
	if err != nil {
		raw = []byte("{}")
	}
	n := &model.Notification{Event: string(e.Event), Title: e.Title, Body: e.Body, MetaJSON: raw}
	if e.Target != nil && e.Target.Kind != "" && e.Target.Kind != TargetNone {
		t := *e.Target
		n.Target = &t
	}
	return Say(lang, n)
}

// PersonLang is the language a person is told in, on every channel - the
// bell, a push, an email, the desktop app, an agent reading their bell: their
// account's (one setting, Settings -> Language, whichever surface they set it
// on), else the instance's (FILEX_DEFAULT_LOCALE), else English. ⚠ Translated
// at the LAST stop: a notification travels as its facts and keys and is said
// only here, for the one who reads it (the maintainers' rule, 2026-10-08).
func PersonLang(u *model.User) string {
	if u == nil {
		return srvtext.Pick()
	}
	return srvtext.Pick(u.Locale)
}

// SayRows puts the reader's sentence on rows read for them, in place: Title,
// Body and, for a row that names an encrypted item, E2E. The API's bell
// (GET /api/notifications) and the administrators' history answer this.
func SayRows(lang string, rows []*model.Notification) {
	lang = srvtext.Pick(lang)
	for _, n := range rows {
		if n == nil {
			continue
		}
		s := Say(lang, n)
		n.Title, n.Body, n.E2E = s.Title, s.Body, s.E2E
		n.WebhookReason = WebhookReason(lang, n)
		opens := Opens(n.Target)
		n.Opens = &opens
	}
}

// Opens reports whether a click on a row with target t goes somewhere: a
// share with its token, an app's home page with its view, the Trash view, a
// file or a folder with its storage. ⚠ The ONE rule: the API says it on every
// row (`opens`) and a push carries it (`open`), so the bell, the page's pop-up
// and a phone agree; Send already downgrades half an address to `none`
// (resolveTarget), and this is the verdict on what is left.
func Opens(t *model.NotificationTarget) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TargetShare:
		return strings.TrimSpace(t.ID) != ""
	case TargetApp:
		return t.Open != nil && strings.TrimSpace(t.Open.Plugin) != "" && strings.TrimSpace(t.Open.View) != ""
	case TargetTrash:
		return true
	case TargetFile, TargetDir:
		return strings.TrimSpace(t.Storage) != ""
	}
	return false
}

// Message is a notification UNTRANSLATED: the catalogue keys its title and
// body are said with and the values they place - what a webhook receiver that
// translates for itself reads (the payload's `i18n`), beside the sentence
// already said in the receiver's language. Keys are those of the catalogue a
// translator gets (`server.notify.*`, filex-catalogue-en.json); a key's
// plural form is chosen by Count, by the CLDR category of the receiver's own
// language (`<key>_one`, `_few`...; the plain key is `other`).
type Message struct {
	// Lang is the language Title and Body of the same payload are said in.
	Lang  string       `json:"lang"`
	Title *MessagePart `json:"title,omitempty"`
	// Body is absent where the body is not one phrase - a digest's lines are
	// the phrases its meta lists (groups[].parts, `server.notify.digest.*`).
	Body *MessagePart `json:"body,omitempty"`
}

// MessagePart is one key and its values. An item inside an encrypted folder
// is the lock word in Vars, as in the sentence.
type MessagePart struct {
	Key   string            `json:"key"`
	Count *int              `json:"count,omitempty"`
	Vars  map[string]string `json:"vars,omitempty"`
}

// MessageOf is e untranslated (Message); nil for an event the catalogue has
// no phrase for. lang is the language the payload's own sentence is in.
func MessageOf(lang string, e Event) *Message {
	raw, err := marshalMeta(e)
	if err != nil {
		return nil
	}
	var meta map[string]any
	_ = json.Unmarshal(raw, &meta)
	var t *model.NotificationTarget
	if e.Target != nil && e.Target.Kind != "" && e.Target.Kind != TargetNone {
		tt := *e.Target
		t = &tt
	}
	c := newComposer(lang)
	return c.message(string(e.Event), e.Title, e.Body, meta, targetOf(t, meta))
}

func (c *composer) message(event, title, body string, meta map[string]any, target rowTarget) *Message {
	if event == string(EventNotificationDigest) {
		item := obj(meta["item"])
		if ev := str(item["event"]); numText(meta["count"]) == "1" && ev != "" && ev != event {
			return c.message(ev, str(item["title"]), str(item["body"]), obj(item["meta"]), target)
		}
		out := &Message{Lang: c.lang, Title: &MessagePart{Key: notifyKey + "notification.digest.title"}}
		if n, err := strconv.Atoi(numText(meta["count"])); err == nil {
			out.Title.Count = &n
			out.Title.Vars = map[string]string{"count": strconv.Itoa(n)}
		}
		return out
	}
	tk, bk, ok := c.phraseKeys(event, meta)
	if !ok {
		return nil
	}
	v, count := c.vars(event, title, body, meta, target)
	part := func(k phraseKey) *MessagePart {
		if k.key == "" {
			return nil
		}
		p := &MessagePart{Key: k.key}
		if k.counted {
			p.Count = count
		}
		for _, name := range srvtext.Placeholders(srvtext.Template("en", k.key)) {
			if val := c.locked(v[name]); val != "" {
				if p.Vars == nil {
					p.Vars = map[string]string{}
				}
				p.Vars[name] = val
			}
		}
		return p
	}
	return &Message{Lang: c.lang, Title: part(tk), Body: part(bk)}
}

// ToastBody is a notification as a TOAST lays it out under the instance's name
// (the toast's title): the sentence, then its detail - "New file: report.pdf -
// Reports/report.pdf". The page's own pop-up lays the same two strings out the
// same way (web lib/browserNotify.ts brandedNotification), so a push that
// replaces it says what it said.
func ToastBody(s Said) string {
	if s.Body == "" {
		return s.Title
	}
	return s.Title + " - " + s.Body
}

const notifyKey = "server.notify."

// The marks that stand for an encrypted name while a sentence is composed.
// Private-use characters: they never leave this file - said() turns them into
// the lock word, or into model.NotificationE2E parts.
var (
	markOpen  = string(rune(0xE000))
	markClose = string(rune(0xE001))
	isoOpen   = string(rune(0x2068)) // FIRST STRONG ISOLATE
	isoClose  = string(rune(0x2069)) // POP DIRECTIONAL ISOLATE
)

type composer struct {
	lang  string
	rtl   bool
	names []model.NotificationE2EName
}

func newComposer(lang string) *composer {
	l := srvtext.Pick(lang)
	return &composer{lang: l, rtl: srvtext.IsRTL(l)}
}

// word is one of the words a sentence falls back on (`server.notify.word.*`).
func (c *composer) word(name string) string {
	return srvtext.Text(c.lang, notifyKey+"word."+name, nil)
}

// isolate wraps a value placed in a right-to-left sentence (see the header).
func (c *composer) isolate(v string) string {
	if !c.rtl || v == "" {
		return v
	}
	return isoOpen + v + isoClose
}

// template is a phrase in the reader's language - its plural form for count
// when there is one.
func (c *composer) template(key string, count *int) string {
	if count != nil {
		return srvtext.TemplateN(c.lang, key, *count)
	}
	return srvtext.Template(c.lang, key)
}

var placeholderRe = regexp.MustCompile(`\{(\w+)\}`)

// Repairs after the fill: a separator a missing value left at either end.
// The long dash is built from its code point (\x{2014}); the catalogues write
// a spaced hyphen since 2026-09-30, and a pack written before may not.
var (
	trailSep  = regexp.MustCompile(`\s*(\x{2014}|\x{2192}|:)\s*$`)
	leadSep   = regexp.MustCompile(`^\s*(\x{2014}|\x{2192}|:)\s*`)
	trailDash = regexp.MustCompile(`\s+-\s*$`)
	leadDash  = regexp.MustCompile(`^\s*-\s+`)
	manySpace = regexp.MustCompile(`\s{2,}`)
)

// fill puts vars into tpl in ONE pass - a value is never read for
// placeholders of its own - and repairs what a missing value left: an
// unresolved placeholder goes with the punctuation that was holding it.
func (c *composer) fill(tpl string, vars map[string]string) string {
	out := placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		return c.isolate(vars[m[1:len(m)-1]])
	})
	out = trailSep.ReplaceAllString(out, "")
	out = leadSep.ReplaceAllString(out, "")
	out = trailDash.ReplaceAllString(out, "")
	out = leadDash.ReplaceAllString(out, "")
	out = manySpace.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// say is one row's sentence: the title, the body and the body's lines, with
// the encrypted names still marked.
func (c *composer) say(event, title, body string, meta map[string]any, target rowTarget) (string, string, []string) {
	if event == string(EventNotificationDigest) {
		return c.digest(title, meta, target)
	}
	v, count := c.vars(event, title, body, meta, target)
	if tk, bk, ok := c.phraseKeys(event, meta); ok {
		t := c.fill(c.templateOf(tk, count), v)
		b := c.fill(c.templateOf(bk, count), v)
		if t != "" {
			return t, b, lineOf(b)
		}
	}
	// No phrase: the emitter's own title (an alarm a later version adds),
	// never the event id while there is anything else to say.
	fallbackBody := v["path"]
	if fallbackBody == "" {
		fallbackBody = body
	}
	if t := strings.TrimSpace(title); t != "" && t != event {
		b := body
		if b == "" {
			b = fallbackBody
		}
		return c.isolate(t), c.isolate(b), lineOf(c.isolate(b))
	}
	return event, c.isolate(fallbackBody), lineOf(c.isolate(fallbackBody))
}

// phraseKey is one catalogue key a sentence is said with; counted: its plural
// form is chosen by the row's count (a variant's key is not).
type phraseKey struct {
	key     string
	counted bool
}

// phraseKeys picks the keys an event is said with - ok=false when the
// catalogue has no phrase for it. The one variant that applies, if any:
// words the server phrased per language when it wrote the row (`noticed`), a
// NO (`rejected`), a single encrypted file (`file`); a variant field the
// catalogue lacks falls back to the plain one.
func (c *composer) phraseKeys(event string, meta map[string]any) (phraseKey, phraseKey, bool) {
	base := notifyKey + event
	if !srvtext.Has(base + ".title") {
		return phraseKey{}, phraseKey{}, false
	}
	variant := ""
	switch {
	case srvtext.Has(base+".title_noticed") && noticeText(meta, "title_", c.lang) != "":
		variant = "noticed"
	case str(meta["decision"]) == "rejected" && srvtext.Has(base+".title_rejected"):
		variant = "rejected"
	case str(meta["kind"]) == "file" && srvtext.Has(base+".title_file"):
		variant = "file"
	}
	field := func(name string) phraseKey {
		if variant != "" && srvtext.Has(base+"."+name+"_"+variant) {
			return phraseKey{key: base + "." + name + "_" + variant}
		}
		if !srvtext.Has(base + "." + name) {
			return phraseKey{}
		}
		return phraseKey{key: base + "." + name, counted: true}
	}
	return field("title"), field("body"), true
}

// templateOf is k's text in the reader's language ("" for no key).
func (c *composer) templateOf(k phraseKey, count *int) string {
	if k.key == "" {
		return ""
	}
	if k.counted {
		return c.template(k.key, count)
	}
	return c.template(k.key, nil)
}

func lineOf(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// rowTarget is what a row's click target says about where it is.
type rowTarget struct {
	storage, path string
}

func targetOf(t *model.NotificationTarget, meta map[string]any) rowTarget {
	if t != nil {
		return rowTarget{storage: t.Storage, path: t.Path}
	}
	m := obj(meta["target"])
	return rowTarget{storage: str(m["storage"]), path: str(m["path"])}
}

// vars are the values a phrase may place, resolved from the row, and the
// count its plural form is chosen by (nil: none).
//
// ⚠ Every one of them is a chain rather than a single source, because the
// emitters are not uniform: `node` is absent on share.created when the row
// did not resolve, `meta.trash_path` can be empty, a replica alarm carries
// only `meta.path`.
func (c *composer) vars(event, title, body string, meta map[string]any, target rowTarget) (map[string]string, *int) {
	node := obj(meta["node"])
	// An "open with filex" save has no path by design: the document lives on
	// the person's own computer (personview.go). Checked FIRST, so the body -
	// whatever the emitter wrote there - is never taken for a path.
	var p string
	if b, ok := meta["open_with"].(bool); ok && b {
		p = c.word("openWith")
	} else {
		p = first(str(node["path"]), str(meta["path"]), target.path, body)
	}
	name := first(str(node["name"]), baseName(p), c.word("unnamed"))
	from, to := str(meta["from"]), str(meta["to"])
	// Never print a ciphertext name: inside an encrypted folder the server
	// has nothing else.
	if root := str(meta["e2e_root"]); root != "" {
		h := c.hider(root)
		p, name = h.item(p, name)
		from, to = h.path(from), h.path(to)
	}
	// A FOLDER event at the top of a storage (an encryption request for its
	// root) has no path at all: node.path is "" and node.name the storage's.
	_, hasPath := node["path"]
	nodeAtRoot := hasPath && str(node["path"]) == "" && str(node["name"]) != ""
	countText := first(numText(meta["count"]), numText(meta["queued"]))
	var count *int
	if n, err := strconv.Atoi(countText); err == nil {
		count = &n
	}
	folder := str(meta["folder"])
	if folder == "" {
		if nodeAtRoot {
			folder = name
		} else {
			folder = first(c.baseOf(p), p)
		}
	}
	noticeTitle := noticeText(meta, "title_", c.lang)
	if noticeTitle == "" && title != event {
		noticeTitle = title
	}
	if noticeTitle == "" {
		noticeTitle = c.word("appNotice")
	}
	return map[string]string{
		"name":      name,
		"path":      p,
		"count":     countText,
		"uploader":  first(strings.TrimSpace(str(meta["uploader"])), c.word("someone")),
		"folder":    folder,
		"file":      first(str(meta["file"]), c.baseOf(p), p),
		"storage":   first(str(meta["storage"]), target.storage),
		"reason":    first(str(meta["reason"]), body),
		"signature": str(meta["signature"]),
		"from":      from,
		"to":        first(to, p),
		// A comment's excerpt, and an approver's note: one line - a toast
		// and a bell row both clip, and a newline in a toast is a gap.
		"body":      oneLine(str(meta["body"])),
		"note":      oneLine(str(meta["note"])),
		"version":   str(meta["version"]),
		"current":   str(meta["current"]),
		"added":     str(meta["added"]),
		"requester": first(str(meta["requester"]), c.word("someone")),
		"provider":  str(meta["provider"]),
		"op":        str(meta["op"]),
		"error":     first(str(meta["error"]), str(meta["primary_error"])),
		"failed":    numText(meta["failed_count"]),
		"repaired":  numText(meta["repaired_count"]),
		// An app's LABEL in the reader's language, never meta.plugin (its
		// install id): a row from before the labels has none, and then the
		// "label:" prefix is left out.
		"plugin":       noticeText(meta, "plugin_label_", c.lang),
		"notice_title": noticeTitle,
		"notice_body":  first(noticeText(meta, "body_", c.lang), body),
		// ⚠ Never a person's e-mail address: it is an identity, and a toast
		// is read over a shoulder. No phrase names who did something.
	}, count
}

// noticeText is one of a row's per-language texts (`<prefix><lang>`, written
// by the app or by the server's catalogue when the row was made) in the
// reader's language: their tag, its base language, then English.
func noticeText(meta map[string]any, prefix, lang string) string {
	tags := []string{lang}
	if i := strings.IndexByte(lang, '-'); i > 0 {
		tags = append(tags, lang[:i])
	}
	tags = append(tags, "en")
	for _, t := range tags {
		if v := strings.TrimSpace(str(meta[prefix+t])); v != "" {
			return v
		}
	}
	return ""
}

// ── the digest ─────────────────────────────────────────────────────────

// digest is a digest row (digest.go): "{count} notifications" over one line
// per folder - "Reports: 12 files added; Photos: 3 files moved to the trash,
// 1 comment". A digest of one says what that one row says.
func (c *composer) digest(title string, meta map[string]any, target rowTarget) (string, string, []string) {
	item := obj(meta["item"])
	if numText(meta["count"]) == "1" {
		if ev := str(item["event"]); ev != "" && ev != string(EventNotificationDigest) {
			return c.say(ev, str(item["title"]), str(item["body"]), obj(item["meta"]), target)
		}
	}
	countText := numText(meta["count"])
	var count *int
	if n, err := strconv.Atoi(countText); err == nil {
		count = &n
	}
	t := c.fill(c.template(notifyKey+"notification.digest.title", count), map[string]string{"count": countText})
	if t == "" {
		t = c.isolate(title)
	}
	var lines []string
	for _, g := range arr(meta["groups"]) {
		group := obj(g)
		name := first(str(group["name"]), str(group["path"]), str(group["storage"]))
		if b, ok := group["encrypted"].(bool); ok && b {
			// A folder inside an encrypted folder whose names are encrypted:
			// its name is ciphertext, so it is never printed.
			if root := str(group["e2e_root"]); root != "" {
				name = c.slot(str(group["storage"])+"://"+strings.TrimLeft(str(group["path"]), "/"), root, "name", c.word("locked"))
			} else {
				name = c.word("locked")
			}
		}
		if parts := c.digestParts(group["parts"]); parts != "" {
			lines = append(lines, c.isolate(name)+": "+parts)
		}
	}
	if more := intOf(meta["more_folders"]); more > 0 {
		lines = append(lines, c.digestPart("more", more))
	}
	if other := c.digestParts(meta["other_parts"]); other != "" {
		lines = append(lines, other)
	}
	return t, strings.Join(lines, "; "), lines
}

// digestParts says a digest's parts ([{key, count}]) as one phrase.
func (c *composer) digestParts(v any) string {
	var out []string
	for _, p := range arr(v) {
		part := obj(p)
		if n := intOf(part["count"]); n > 0 {
			out = append(out, c.digestPart(str(part["key"]), n))
		}
	}
	return strings.Join(out, ", ")
}

// digestPart is one phrase of a digest line; a key the catalogue does not
// have (a kind a later version adds) is said as "other", never as its key.
func (c *composer) digestPart(key string, n int) string {
	if key == "" || !srvtext.Has(notifyKey+"digest."+key) {
		key = "other"
	}
	return srvtext.Plural(c.lang, notifyKey+"digest."+key, n, nil)
}

// ── names inside an encrypted folder ───────────────────────────────────

// e2eStoredRe is a stored name the browser encrypted, by its spelling alone
// (`S`, a folder's `S.D`, a long `<H>.fxl` / `<H>.fxl.D`; packages/core
// lib/e2enames). Inside an encrypted folder a name spelled like this is
// ciphertext.
var e2eStoredRe = regexp.MustCompile(`^(?:[A-Za-z0-9_-]{23,}|[A-Za-z0-9_-]{43}\.fxl)(?:\.[A-Za-z0-9_-]{22})?$`)

type e2eHider struct {
	c       *composer
	root    string // "<storage>://<root>", as the row carries it
	storage string
	rootRel string
}

func (c *composer) hider(root string) *e2eHider {
	h := &e2eHider{c: c, root: root, rootRel: root}
	if i := strings.Index(root, "://"); i >= 0 {
		h.storage, h.rootRel = root[:i], root[i+3:]
	}
	h.rootRel = strings.Trim(h.rootRel, "/")
	return h
}

// below is p's segments under the encrypted folder; nil outside it.
func (h *e2eHider) below(p string) []string {
	clean := strings.Trim(p, "/")
	if h.rootRel == "" || clean == h.rootRel || !strings.HasPrefix(clean, h.rootRel+"/") {
		return nil
	}
	return strings.Split(clean[len(h.rootRel)+1:], "/")
}

func encryptedSegs(segs []string) bool {
	for _, s := range segs {
		if e2eStoredRe.MatchString(s) {
			return true
		}
	}
	return false
}

func (h *e2eHider) wire(p string) string { return h.storage + "://" + strings.TrimLeft(p, "/") }

// path is p with its encrypted part as one name: "<root>/…/🔒 Encrypted item"
// for a reader without the key. A path outside the folder, or whose names
// below it are readable (a level-1 folder), is left as it is.
func (h *e2eHider) path(p string) string {
	segs := h.below(p)
	if !encryptedSegs(segs) {
		return p
	}
	locked := h.rootRel + "/"
	if len(segs) > 1 {
		locked += "…/"
	}
	locked += h.c.word("locked")
	lead := ""
	if strings.HasPrefix(p, "/") {
		lead = "/"
	}
	return lead + h.c.slot(h.wire(p), h.root, "path", locked)
}

// item is an item's path and name, each hidden where it is ciphertext.
func (h *e2eHider) item(p, name string) (string, string) {
	segs := h.below(p)
	if !encryptedSegs(segs) {
		return p, name
	}
	if e2eStoredRe.MatchString(segs[len(segs)-1]) {
		name = h.c.slot(h.wire(p), h.root, "name", h.c.word("locked"))
	}
	return h.path(p), name
}

// slot marks one encrypted name in the sentence being composed.
func (c *composer) slot(wire, root, part, locked string) string {
	for i, n := range c.names {
		if n.Wire == wire && n.Root == root && n.Part == part {
			return markOf(i)
		}
	}
	c.names = append(c.names, model.NotificationE2EName{Wire: wire, Root: root, Part: part, Locked: locked})
	return markOf(len(c.names) - 1)
}

func markOf(i int) string { return markOpen + strconv.Itoa(i) + markClose }

// baseOf is p's last segment; for a hidden path, the hidden item's NAME.
func (c *composer) baseOf(p string) string {
	b := baseName(p)
	if i, ok := markIndex(b); ok && i < len(c.names) && c.names[i].Part == "path" {
		n := c.names[i]
		return c.slot(n.Wire, n.Root, "name", c.word("locked"))
	}
	return b
}

// markIndex reads a string that is exactly one mark.
func markIndex(s string) (int, bool) {
	if !strings.HasPrefix(s, markOpen) || !strings.HasSuffix(s, markClose) {
		return 0, false
	}
	n, err := strconv.Atoi(s[len(markOpen) : len(s)-len(markClose)])
	return n, err == nil
}

// said turns a composed sentence into what readers get: the lock word in
// place of every mark, and - when there are marks - where they stand.
func (c *composer) said(title, body string, lines []string) Said {
	out := Said{Title: c.locked(title), Body: c.locked(body)}
	for _, l := range lines {
		if s := c.locked(l); s != "" {
			out.Lines = append(out.Lines, s)
		}
	}
	if strings.Contains(title, markOpen) || strings.Contains(body, markOpen) {
		e := &model.NotificationE2E{}
		used := map[int]int{}
		e.Title = c.parts(title, used, e)
		e.Body = c.parts(body, used, e)
		out.E2E = e
	}
	return out
}

// locked is s with every mark replaced by what its name reads as without the
// key.
func (c *composer) locked(s string) string {
	var b strings.Builder
	c.walk(s, func(text string) { b.WriteString(text) }, func(i int) { b.WriteString(c.names[i].Locked) })
	return b.String()
}

// parts is s cut at its marks, the names renumbered in order of use.
func (c *composer) parts(s string, used map[int]int, e *model.NotificationE2E) []model.NotificationTextPart {
	out := []model.NotificationTextPart{}
	c.walk(s, func(text string) {
		if text != "" {
			out = append(out, model.NotificationTextPart{Text: text})
		}
	}, func(i int) {
		j, ok := used[i]
		if !ok {
			j = len(e.Names)
			used[i] = j
			e.Names = append(e.Names, c.names[i])
		}
		out = append(out, model.NotificationTextPart{Name: &j})
	})
	return out
}

// walk calls text for the words of s and name for each mark, in order.
func (c *composer) walk(s string, text func(string), name func(int)) {
	for {
		i := strings.Index(s, markOpen)
		if i < 0 {
			text(s)
			return
		}
		j := strings.Index(s[i:], markClose)
		if j < 0 {
			text(s)
			return
		}
		n, err := strconv.Atoi(s[i+len(markOpen) : i+j])
		if err != nil || n < 0 || n >= len(c.names) {
			text(s[:i+j+len(markClose)])
			s = s[i+j+len(markClose):]
			continue
		}
		text(s[:i])
		name(n)
		s = s[i+j+len(markClose):]
	}
}

// ── small readers of a decoded meta ────────────────────────────────────

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// numText is a JSON number as the text a sentence prints ("3"), "" for
// anything else.
func numText(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case json.Number:
		return n.String()
	}
	return ""
}

func intOf(v any) int {
	n, err := strconv.Atoi(numText(v))
	if err != nil {
		return 0
	}
	return n
}

// first is the first non-empty of vs.
func first(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// baseName is p's last segment: "Docs/a.pdf" → "a.pdf".
func baseName(p string) string {
	clean := strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(clean, `/\`); i >= 0 {
		return clean[i+1:]
	}
	return clean
}
