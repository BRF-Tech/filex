// Package wire holds the JSON shapes that cross the host ↔ guest boundary of
// a filex app plugin. It is imported by the host (internal/wasmplugin) and by
// the guest SDK (pkg/pluginkit), so the two can never drift: a field added
// here is a field on both sides.
//
// Everything here is plain data. No behaviour, no dependencies beyond the
// standard library, so it compiles unchanged for GOOS=wasip1.
package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ProtocolVersion is the manifest_version / ABI generation this package
// describes. A host refuses a plugin whose describe answer names another.
const ProtocolVersion = 1

// Text is a label in one or more languages. `en` is required everywhere a
// Text appears; other languages fall back to it.
type Text map[string]string

// Get returns the text for lang, falling back to its base language (`pt`
// for `pt-br`), then to English, then to any — per string (textGet,
// langpack.go).
func (t Text) Get(lang string) string { return textGet(t, lang) }

// Field is one input of a settings or action form. It mirrors the storage
// driver descriptor field the admin panel already renders (storage.Field), so
// a plugin's settings form is drawn by the same component.
type Field struct {
	Key         string        `json:"key"`
	Type        string        `json:"type"` // string | password | int | bool | select | text
	Label       string        `json:"label"`
	Help        string        `json:"help,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Secret      bool          `json:"secret,omitempty"`
	Default     any           `json:"default,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	Options     []FieldOption `json:"options,omitempty"`
	Min         *int          `json:"min,omitempty"`
	Max         *int          `json:"max,omitempty"`
	// Multi turns a `select` into a several-of choice. The value is then a
	// list of option values instead of one.
	Multi bool `json:"multi,omitempty"`
	// Style says how a `bool` is drawn: `switch` for an on/off setting a
	// person flips in passing, `choice` for a decision they must read
	// before answering (two buttons, Yes and No). Empty lets the client
	// decide, which it does by guessing — say it when the answer matters.
	Style string `json:"style,omitempty"`
	// ShowWhen hides the field until another field in the same form holds
	// one of the given values; RequiredWhen makes it required only then.
	// Both are re-checked by the host at submit: a value belonging to a
	// hidden field is dropped, and a required-when field that is empty
	// refuses the job. A form may not present a contradiction (a file name
	// asked for while the output is a new version of the same file).
	ShowWhen     *Condition `json:"show_when,omitempty"`
	RequiredWhen *Condition `json:"required_when,omitempty"`
	// I18n is the per-language spelling of Label, Help and Placeholder, when
	// they were written as {"en": …, "tr": …} instead of one string. Label,
	// Help and Placeholder then hold the English (or the only) spelling, so
	// code that reads them as strings keeps working, and the field is
	// written back out as the maps it arrived as.
	//
	// ⚠⚠ Why (2026-09-21, a tester): an app's settings were English inside
	// the Turkish admin panel — "Add a time stamp to every signature",
	// "Time-stamping authority" — because a manifest setting could only be
	// ONE string, while every other text an app shows (its name, its
	// actions, its screens) is a {en, tr} map the client already resolves.
	I18n *FieldI18n `json:"-"`
}

// FieldI18n holds a Field's texts in every language the manifest gave.
type FieldI18n struct {
	Label       Text
	Help        Text
	Placeholder Text
}

// textOrString reads a JSON value that is either one string or a {lang: …}
// map: the string, and the map when it was one.
func textOrString(raw json.RawMessage) (string, Text, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil, nil
	}
	switch raw[0] {
	case '"':
		var s string
		err := json.Unmarshal(raw, &s)
		return s, nil, err
	case '{':
		var t Text
		if err := json.Unmarshal(raw, &t); err != nil {
			return "", nil, err
		}
		return t.Get("en"), t, nil
	}
	return "", nil, fmt.Errorf("expected a string or a {\"en\": …} map, got %s", raw)
}

// UnmarshalJSON accepts label, help and placeholder as a string or as a
// {lang: …} map.
func (f *Field) UnmarshalJSON(b []byte) error {
	type plain Field
	var aux struct {
		plain
		Label       json.RawMessage `json:"label"`
		Help        json.RawMessage `json:"help,omitempty"`
		Placeholder json.RawMessage `json:"placeholder,omitempty"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*f = Field(aux.plain)
	var i18n FieldI18n
	var err error
	if f.Label, i18n.Label, err = textOrString(aux.Label); err != nil {
		return fmt.Errorf("field %q label: %w", f.Key, err)
	}
	if f.Help, i18n.Help, err = textOrString(aux.Help); err != nil {
		return fmt.Errorf("field %q help: %w", f.Key, err)
	}
	if f.Placeholder, i18n.Placeholder, err = textOrString(aux.Placeholder); err != nil {
		return fmt.Errorf("field %q placeholder: %w", f.Key, err)
	}
	f.I18n = nil
	if i18n.Label != nil || i18n.Help != nil || i18n.Placeholder != nil {
		f.I18n = &i18n
	}
	return nil
}

// MarshalJSON writes the maps back when the field has them.
func (f Field) MarshalJSON() ([]byte, error) {
	type plain Field
	if f.I18n == nil {
		return json.Marshal(plain(f))
	}
	pick := func(s string, t Text) any {
		if len(t) > 0 {
			return t
		}
		if s == "" {
			return nil
		}
		return s
	}
	return json.Marshal(struct {
		plain
		Label       any `json:"label"`
		Help        any `json:"help,omitempty"`
		Placeholder any `json:"placeholder,omitempty"`
	}{plain(f), pick(f.Label, f.I18n.Label), pick(f.Help, f.I18n.Help), pick(f.Placeholder, f.I18n.Placeholder)})
}

// Localized is the field with every text in `lang` (falling back to
// English) and no map left — for a client that reads plain strings.
func (f Field) Localized(lang string) Field {
	if f.I18n != nil {
		if s := f.I18n.Label.Get(lang); s != "" {
			f.Label = s
		}
		if s := f.I18n.Help.Get(lang); s != "" {
			f.Help = s
		}
		if s := f.I18n.Placeholder.Get(lang); s != "" {
			f.Placeholder = s
		}
		f.I18n = nil
	}
	if len(f.Options) > 0 {
		opts := make([]FieldOption, len(f.Options))
		for i, o := range f.Options {
			opts[i] = o.Localized(lang)
		}
		f.Options = opts
	}
	return f
}

// Condition is "that other field holds one of these values".
type Condition struct {
	Key    string   `json:"key"`
	Equals []string `json:"equals"`
}

// FieldOption is one choice of a select field. Its label, like a Field's,
// may be one string or a {lang: …} map (then kept in LabelI18n).
type FieldOption struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	LabelI18n Text   `json:"-"`
}

// UnmarshalJSON accepts the label as a string or a {lang: …} map.
func (o *FieldOption) UnmarshalJSON(b []byte) error {
	var aux struct {
		Value json.RawMessage `json:"value"`
		Label json.RawMessage `json:"label"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	// A value is a string by contract; a number or a bool written by hand is
	// kept as its JSON spelling rather than refused.
	o.Value = ""
	if v := bytes.TrimSpace(aux.Value); len(v) > 0 && !bytes.Equal(v, []byte("null")) {
		if v[0] == '"' {
			if err := json.Unmarshal(v, &o.Value); err != nil {
				return err
			}
		} else {
			o.Value = string(v)
		}
	}
	var err error
	o.Label, o.LabelI18n, err = textOrString(aux.Label)
	return err
}

// MarshalJSON writes the map back when the option has one.
func (o FieldOption) MarshalJSON() ([]byte, error) {
	var label any = o.Label
	if len(o.LabelI18n) > 0 {
		label = o.LabelI18n
	}
	return json.Marshal(struct {
		Value string `json:"value"`
		Label any    `json:"label"`
	}{o.Value, label})
}

// Localized is the option with its label in `lang`.
func (o FieldOption) Localized(lang string) FieldOption {
	if s := o.LabelI18n.Get(lang); s != "" {
		o.Label = s
	}
	o.LabelI18n = nil
	return o
}

// Applies says which selections an action or view is offered for.
type Applies struct {
	Kind  string   `json:"kind,omitempty"` // file | dir | any (default file)
	Ext   []string `json:"ext,omitempty"`  // lower-case, no dot
	Mime  []string `json:"mime,omitempty"` // exact or prefix glob "image/*"
	Multi bool     `json:"multi,omitempty"`
	Min   int      `json:"min,omitempty"`
	Max   int      `json:"max,omitempty"`
	// State narrows the offer to files on which THIS plugin keeps at least
	// one of the named state keys (`state_set`), NoState to files that
	// carry none of them. A signing app offers "Sign" only where a key
	// `pending` is set and "Request signatures" only where it is not. Keys
	// are the plugin's own; the listing exposes them as `<plugin>:<key>`.
	State   []string `json:"state,omitempty"`
	NoState []string `json:"no_state,omitempty"`
	// EngineExt adds extensions that are offered only while the named
	// engine is installed and granted: `{"libreoffice": ["docx", "odt"]}`
	// on top of `ext: ["pdf"]` offers the action on a .docx exactly when
	// the host can turn one into a PDF. The host folds the list into Ext
	// before anybody sees the action (the explorer's menu, the run check),
	// so a client never evaluates it. It only ADDS to a non-empty ext/mime
	// list — an empty one already means "any file".
	//
	// ⚠⚠ Why (2026-09-21, a tester): "İmzala…" and "İmza iste…" were
	// offered on a .docx on an installation without LibreOffice, and the
	// click opened a page saying it is not a PDF and there is no LibreOffice
	// to convert it — while the Signatures screen itself said only PDFs can
	// be signed. An action that can never work must not be offered.
	EngineExt map[string][]string `json:"engine_ext,omitempty"`
	// Writable says the action can only be completed where its file can be
	// written, even though its own output is `none`: "Request signatures"
	// writes nothing now, but the flow it starts ends in writing the signed
	// document. filex hides such an action on a read-only storage (and
	// refuses it there with 409 read_only), exactly as it hides an action
	// whose output writes — instead of letting the app open a screen whose
	// only possible answer is "this cannot be done here". It needs the
	// files:write permission, and it counts as a write for the level the
	// caller needs (editor).
	Writable bool `json:"writable,omitempty"`
}

// PersonalStateSuffix marks a PERSONAL state key: `<key>@<user id>` is kept
// on a file for one person, and filex shows it to that person — and to
// nobody else — as `<key>@me`. A rule names it that way (`state:
// ["todo@me"]`), so the signing app offers "Sign / Fill" only to the people
// who have something to sign on that file, while the key a listing exposes
// never says who else has. See PersonalState.
const PersonalStateSuffix = "@me"

// PersonalState is the key an app writes for one person (`state_set`):
// PersonalState("todo", 7) is "todo@7", which user 7's listings show as
// "todo@me".
func PersonalState(key string, userID int64) string {
	return fmt.Sprintf("%s@%d", key, userID)
}

// Output says where an action's result files land.
type Output struct {
	Mode string `json:"mode"`           // sibling | version | none | folder (per job)
	Name string `json:"name,omitempty"` // pattern: {stem}, {ext}, {name}
	// Elsewhere (manifest, with mode sibling) says the result may go into a
	// folder the PERSON chooses instead of beside its source: where the
	// source's storage is read-only, filex still offers the action and the
	// app asks where the result should go (a `file-chooser` with kind "dir",
	// starting at CallContext.Home), then answers the job with Output{Mode:
	// "folder", Dir: <the chosen folder>}. filex checks that folder — the
	// storage, the person's level on it, locks and filex's own folders —
	// before the job is queued and again when the file is written.
	Elsewhere bool `json:"elsewhere,omitempty"`
	// Dir is the chosen folder for mode "folder", adapter-qualified
	// (`docs://reports`). Only a JobRequest carries it.
	Dir string `json:"dir,omitempty"`
}

// Limits are what a plugin asks for; the host clamps them.
type Limits struct {
	MemoryPages   int `json:"memory_pages,omitempty"`
	CallTimeoutS  int `json:"call_timeout_s,omitempty"`
	MaxInputBytes int `json:"max_input_bytes,omitempty"`
	TimeoutS      int `json:"timeout_s,omitempty"`
}

// Action is one entry a plugin adds to the file menu.
type Action struct {
	ID      string  `json:"id"`
	Label   Text    `json:"label"`
	Icon    string  `json:"icon,omitempty"`
	Applies Applies `json:"applies"`
	View    string  `json:"view,omitempty"`
	Confirm Text    `json:"confirm,omitempty"`
	MinRole string  `json:"min_role,omitempty"` // viewer | editor | owner
	Danger  bool    `json:"danger,omitempty"`
	Output  Output  `json:"output"`
	Limits  Limits  `json:"limits,omitempty"`
	// Hidden keeps the action out of every menu: it is only ever started by
	// a surface (`job`) or a public page — the second half of a flow, not a
	// thing a person picks.
	Hidden bool `json:"hidden,omitempty"`
}

// View is a declarative screen the plugin can be asked to draw.
//
// Placement: `modal` opens over the explorer; `page` opens as a full page
// in a new tab (a wizard with a document beside it — anything a modal's
// double scroll would cramp); `inspector` is a section of the details
// panel; `home` is a standalone screen listed under Apps.
type View struct {
	ID        string  `json:"id"`
	Placement string  `json:"placement"` // modal | page | inspector | home
	Label     Text    `json:"label"`
	Applies   Applies `json:"applies,omitempty"`
	Size      string  `json:"size,omitempty"`
}

// PublicPage is a flow an outside participant reaches through a share link,
// /s/<token> (the retired /p/<token> answers a 301 to it).
type PublicPage struct {
	ID             string `json:"id"`
	Label          Text   `json:"label"`
	PIN            string `json:"pin,omitempty"` // optional | required | none
	DefaultTTLDays int    `json:"default_ttl_days,omitempty"`
	MaxTTLDays     int    `json:"max_ttl_days,omitempty"`
	// Purpose says what a link of this page IS, for the person who sees it
	// in a list of links (My shares, the admin's Shares): it belongs to
	// something the app keeps, ending it ends that, and there is a place in
	// the app that shows it. Optional; without it a link lists as a share.
	Purpose *PagePurpose `json:"purpose,omitempty"`
}

// PagePurpose names what an app's link is part of.
//
// ⚠⚠ Why (2026-09-21, the owner's decision): signing links appeared in My
// shares as plain `/teklif.pdf` shares offering "Copy PIN" and "Revoke",
// with no hint they belonged to a signing request — and revoking one broke
// the request. They stay listed, but marked: named for what they are,
// opening the app's page for them, and saying before a revoke what the
// revoke does.
type PagePurpose struct {
	// Label names the kind of link in a list: "Signing request".
	Label Text `json:"label"`
	// Revoke is said before the link is revoked, plainly: "Revoking this
	// link cancels the signing request."
	Revoke Text `json:"revoke,omitempty"`
	// Section is the section of the app's `home` view the row opens
	// (Surface.Sections); empty opens the home view as it comes.
	Section string `json:"section,omitempty"`
}

// WasmSource says where the module comes from when installing from a
// manifest fetched off a repository (GitHub URL install).
type WasmSource struct {
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Manifest is filex-app.json — and what `describe` must echo.
type Manifest struct {
	ManifestVersion int      `json:"manifest_version"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Label           Text     `json:"label"`
	Description     Text     `json:"description,omitempty"`
	Icon            string   `json:"icon,omitempty"`
	Homepage        string   `json:"homepage,omitempty"`
	MinFilex        string   `json:"min_filex,omitempty"`
	Permissions     []string `json:"permissions"`
	// Languages the plugin promises to speak. Every Text it returns must
	// carry all of them, or the host refuses it at install: a screen half in
	// one language is a bug the author should see before a person does.
	// Empty means ["en"].
	Languages []string `json:"languages,omitempty"`
	// UILocales lets a plugin add a language to FILEX ITSELF — the key is a
	// language tag, the value that language's strings by filex's own keys.
	// They join the language list the interface offers (public pages
	// included) and leave with the plugin. A missing key falls back to
	// English, as everywhere else.
	UILocales         map[string]map[string]string `json:"ui_locales,omitempty"`
	PermissionReasons map[string]Text              `json:"permission_reasons,omitempty"`
	Settings          []Field                      `json:"settings,omitempty"`
	Actions           []Action                     `json:"actions,omitempty"`
	Views             []View                       `json:"views,omitempty"`
	PublicPages       []PublicPage                 `json:"public_pages,omitempty"`
	Limits            Limits                       `json:"limits,omitempty"`
	Wasm              *WasmSource                  `json:"wasm,omitempty"`
	// Messages are texts FILEX says on the app's behalf, long after the call
	// that caused them — today the reason beside a file lock ("signatures
	// are being collected"), shown in the admin panel, the details panel
	// and a refused rename, each in its READER's language. The app names
	// one by key when it locks (pluginkit.FileLockMessage); `{placeholders}`
	// are filled from the arguments it passes. Every declared language is
	// required, like every other Text.
	Messages map[string]Text `json:"messages,omitempty"`
}

// FillMessage fills `{name}` placeholders in every language of t.
func FillMessage(t Text, args map[string]string) Text {
	if len(args) == 0 {
		return t
	}
	out := make(Text, len(t))
	for lang, s := range t {
		for k, v := range args {
			s = strings.ReplaceAll(s, "{"+k+"}", v)
		}
		out[lang] = s
	}
	return out
}

// DescribeInput is what the host hands `describe`.
type DescribeInput struct {
	HostVersion string `json:"host_version"`
	Locale      string `json:"locale,omitempty"`
}

// Actor is the person a call runs as.
type Actor struct {
	ID    int64  `json:"id"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role,omitempty"`
	// IP is the address the person's request came from, as filex's own
	// access log would write it (behind a trusted proxy, the forwarded
	// client). Set on a VIEW event — the screen somebody is looking at — and
	// empty on a job, which runs later and from nowhere in particular.
	//
	// ⚠ It exists for the same reason `data.page.visitor_ip` does on a
	// public page: an app that records WHO acted (the signing app's audit
	// trail, the IP line it can print under a signature) had that fact for
	// a stranger on a link and not for a signed-in person doing the same
	// thing — so the same box printed an address for one signer and "—" for
	// the next. Personal data: an app that records it must show it to the
	// person before it keeps it.
	IP string `json:"ip,omitempty"`
}

// FileRef is one file the call may read through file_* host functions. Ref
// is an opaque, call-scoped handle — never a storage path.
type FileRef struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime,omitempty"`
	PathRel string `json:"path_rel,omitempty"`
	// Path is the adapter-qualified form (`docs://reports/nda.pdf`) — what
	// the explorer navigates by and what a `pdf-fields`/`preview` node's
	// `src.path` takes. Filled by the host whenever the storage is known.
	Path string `json:"path,omitempty"`
	// ReadOnly says the file's storage takes no writes: no new version, no
	// file beside it. Filled by the host whenever the storage is known.
	//
	// ⚠⚠ Why an app needs to know BEFORE it writes (2026-09-21, a tester):
	// a signing request on a read-only storage was accepted and "sent" —
	// the file frozen, the signer notified — and the signer's answer then
	// failed with 409 read_only, for ever, because the request writes
	// nothing itself (its output mode is `none`) and the host's own
	// read-only refusal only guards jobs that write. Only the app knows
	// that the flow it starts ends in a write, so the app has to be told,
	// and has to refuse at the start with a sentence.
	ReadOnly bool `json:"read_only,omitempty"`
}

// ActionRunInput is what the host hands `action_run`.
type ActionRunInput struct {
	JobID    string            `json:"job_id"`
	ActionID string            `json:"action_id"`
	Params   map[string]any    `json:"params,omitempty"`
	Inputs   []FileRef         `json:"inputs"`
	Output   Output            `json:"output"`
	Actor    Actor             `json:"actor"`
	Locale   string            `json:"locale,omitempty"`
	Settings map[string]string `json:"settings,omitempty"` // non-secret only
	Engines  map[string]bool   `json:"engines,omitempty"`
	// ShareMaxTTLDays: see CallContext.ShareMaxTTLDays. A job needs it for
	// the same reason a screen does — whatever it records about a link's
	// life (the request, the freeze beside it, the mail it sends) has to be
	// the life the link will actually get.
	ShareMaxTTLDays *int `json:"share_max_ttl_days,omitempty"`
}

// OutputRef names a file the plugin produced through file_create.
type OutputRef struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

// ActionRunOutput is what `action_run` returns.
type ActionRunOutput struct {
	OK      bool        `json:"ok"`
	Outputs []OutputRef `json:"outputs,omitempty"`
	Message Text        `json:"message,omitempty"`
	Surface *Surface    `json:"surface,omitempty"`
}

// ViewEventInput is what the host hands `view_event`.
type ViewEventInput struct {
	ViewID   string         `json:"view_id"`
	Event    string         `json:"event"` // open | change | submit | action
	ActionID string         `json:"action_id,omitempty"`
	State    map[string]any `json:"state,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Context  CallContext    `json:"context"`
}

// CallContext is the shared context of a view or page call.
type CallContext struct {
	Inputs   []FileRef         `json:"inputs,omitempty"`
	Actor    *Actor            `json:"actor,omitempty"`
	Locale   string            `json:"locale,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
	Engines  map[string]bool   `json:"engines,omitempty"`
	// ShareMaxTTLDays is the longest life, in days, this installation gives
	// ANY new share link — the administrator's ceiling (Protection → share
	// links, `share.max_ttl_days`; 7 unless it was changed). 0 means no
	// ceiling; nil means the host did not say (older than v0.43.0) or this
	// app holds no `public_pages` grant and so opens no links.
	//
	// ⚠⚠ share_create clamps every link to the LOWEST of this, the page's own
	// `max_ttl_days` and 365. A screen that offers a longer life than that
	// promises something the link will not have: the signing app's wizard
	// said "links valid 14 days" while the link it opened lived 7, because
	// the ceiling was invisible from inside the app. Read it, and offer no
	// more than it allows.
	ShareMaxTTLDays *int `json:"share_max_ttl_days,omitempty"`
	// Home is where this person's results go by default when they cannot
	// go beside their source (Output.Elsewhere): the root of the first
	// storage, in the administrator's order, that the person may write.
	// Adapter-qualified (`depo://`); empty when they may write nowhere.
	Home string `json:"home,omitempty"`
}

// Surface is a declarative screen: nodes drawn by filex's own components.
type Surface struct {
	Title   Text            `json:"title,omitempty"`
	Size    string          `json:"size,omitempty"`
	State   map[string]any  `json:"state,omitempty"`
	Nodes   []Node          `json:"nodes"`
	Actions []SurfaceAction `json:"actions,omitempty"`
	Toast   Text            `json:"toast,omitempty"`
	Done    bool            `json:"done,omitempty"`
	Job     *JobRequest     `json:"job,omitempty"`
	Errors  map[string]Text `json:"errors,omitempty"`
	// Open sends the person to a FILE — the one thing a screen could not do
	// before. A home screen lists documents; a row has to be able to open
	// the one that was clicked, and open it on the right screen. The host
	// checks the path and the target before the answer leaves.
	Open *OpenRequest `json:"open,omitempty"`

	// Sections turn a `home` screen into a page with a MENU: one entry per
	// section, the frame drawing the menu and the plugin drawing whichever
	// section is open. Section says which one this surface is.
	//
	// ⚠⚠ Why the frame and not a node. The owner, 2026-09-21: the
	// Signatures screen should be "its own page … each in a separate menu",
	// with the browser's Back button working and a notification landing on
	// the right section. A node can draw tabs, but only the FRAME owns the
	// address bar: it keeps the open section in the URL (`?section=`), so
	// Back walks the sections and a link can name one. Opening a view with a
	// section in its address hands the plugin `data.section` on the `open`
	// event; everything after that is an ordinary conversation.
	Sections []Section `json:"sections,omitempty"`
	Section  string    `json:"section,omitempty"`
}

// Section is one entry of a home page's menu.
type Section struct {
	ID    string `json:"id"`
	Label Text   `json:"label"`
	// Count is drawn beside the label (a number of rows waiting there);
	// nil draws none.
	Count *int `json:"count,omitempty"`
}

// OpenRequest is "go to this file, and start this there". Path is
// adapter-qualified (`docs://reports/nda.pdf`). Action and View are this
// plugin's own; naming neither just opens the file.
type OpenRequest struct {
	Path   string `json:"path"`
	Action string `json:"action,omitempty"`
	View   string `json:"view,omitempty"`
}

// Node is one component. Type is from the host's catalogue; Props are that
// component's properties; Children only for layout nodes.
type Node struct {
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type"`
	Props    map[string]any `json:"props,omitempty"`
	Children []Node         `json:"children,omitempty"`
}

// SurfaceAction is a footer button.
type SurfaceAction struct {
	ID       string `json:"id"`
	Label    Text   `json:"label"`
	Primary  bool   `json:"primary,omitempty"`
	Danger   bool   `json:"danger,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// JobRequest asks the host to enqueue an action with these params.
type JobRequest struct {
	ActionID string         `json:"action_id"`
	Params   map[string]any `json:"params,omitempty"`
	// Output, when set, replaces the action's manifest output for this one
	// job — a wizard's "same file as a new version / new file beside it /
	// custom name" choice. Mode is sibling | version | none; Name takes the
	// same {stem}/{ext}/{name} pattern as the manifest (a literal name works
	// too). Widening `none` to a writing mode is allowed only if the caller
	// holds editor on the inputs — the host re-checks at submit.
	Output *Output `json:"output,omitempty"`
}

// ── The scheduled wake-up (`tick`) ─────────────────────────────────────
//
// An app that holds the `schedule` permission is woken once an hour and
// asked one question: what do you want done, and WHEN. It answers with
// items, each carrying a due time; filex runs each one AT that time, inside
// the window it was given. An app with nothing to do answers with no items
// and costs one short call.
//
// The point is to be exact without polling. A signing app cannot expire a
// request on its own today — it only notices when somebody opens the status
// screen — so a request that lapses at 03:00 notifies nobody until a human
// happens to look. With a wake-up at 03:00 it schedules the closure for
// 03:00 and filex runs it then: not at 02:00, not at 09:00.

// The bounds a wake-up plans around. They are part of the CONTRACT, not the
// host's private business, because the guest is told them in every TickInput
// and the test kit checks an answer against them before a module is built —
// one set of numbers, named once.
const (
	// ScheduleMaxItems is how many items one wake-up may schedule.
	ScheduleMaxItems = 64
	// ScheduleMaxPaths is how many files one item may name.
	ScheduleMaxPaths = 16
	// ScheduleKeyPattern is what an item key must match. It cannot match the
	// empty string, which is how the host keeps its own wake-up row out of
	// reach of the apps it wakes.
	ScheduleKeyPattern = `^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,63}$`
)

// TickInput is what the host hands `tick`.
type TickInput struct {
	// Now is the host's clock when the wake-up fired, in UTC.
	Now time.Time `json:"now"`
	// WindowStart and WindowEnd bound the due times this answer may name.
	// WindowStart equals Now; WindowEnd is the next hourly boundary, which
	// is when the next wake-up comes. An item due outside the window is not
	// scheduled and no error is raised — say it again at the wake-up whose
	// window contains it. After a restart at 03:37 the window is 03:37 to
	// 04:00, so it is shorter than an hour and the app is told so.
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// MaxItems is how many items this answer may carry. Extra ones are
	// dropped (the app's log says how many), so put the urgent ones first.
	MaxItems int `json:"max_items"`
	// MaxPaths is how many files one item may name.
	MaxPaths int               `json:"max_paths"`
	Locale   string            `json:"locale,omitempty"`
	Settings map[string]string `json:"settings,omitempty"` // non-secret only
	Engines  map[string]bool   `json:"engines,omitempty"`
}

// ScheduleItem is one piece of work an app wants run at a named minute.
//
// It is an action of the app's own — usually a `hidden` one, the half of a
// flow nobody picks from a menu — run as an ordinary plugin job: the same
// ops row, the same sandbox, the same output rules. The wake-up decides
// WHAT and WHEN; the job does the work.
type ScheduleItem struct {
	// Key is the app's own name for this piece of work, and the reason a
	// restart cannot run it twice: the host keeps at most one item per
	// (app, key), so naming the same key again moves the item rather than
	// adding a second one. A signing app keys by envelope
	// (`expire:7f3a…`). 1–64 characters of [A-Za-z0-9] plus `_.:@-`.
	Key string `json:"key"`
	// DueAt is when the work runs, to the minute (to the second, in fact).
	// A time already past runs at once; a time beyond WindowEnd is not
	// scheduled.
	DueAt time.Time `json:"due_at"`
	// ActionID is the app's action to run. It must exist in the manifest
	// and not be switched off by the administrator.
	ActionID string `json:"action_id"`
	// Paths are the files the job runs on, adapter-qualified
	// (`docs://reports/nda.pdf`) — the spelling `state_list` answers with.
	// At least one, at most MaxPaths, all on the same storage.
	Paths  []string       `json:"paths"`
	Params map[string]any `json:"params,omitempty"`
}

// TickOutput is what `tick` returns.
type TickOutput struct {
	Items []ScheduleItem `json:"items,omitempty"`
	// Note is a line for the app's log in the admin panel — what this
	// wake-up decided, in the app's own words. It is not shown to anyone
	// else; there is nobody there.
	Note Text `json:"note,omitempty"`
}

// HostError is the error envelope every host function may return.
type HostError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Host function error codes.
const (
	ErrPermissionDenied = "permission_denied"
	ErrNotFound         = "not_found"
	ErrTooLarge         = "too_large"
	ErrTimeout          = "timeout"
	ErrUnavailable      = "unavailable"
	ErrInvalid          = "invalid"
	ErrBusy             = "busy"
	ErrInternal         = "internal"
	// ErrIntegrity: a downloaded asset did not match the sha256 the app
	// pinned (asset_fetch). Nothing was stored; nothing may use it.
	ErrIntegrity = "integrity"
)
