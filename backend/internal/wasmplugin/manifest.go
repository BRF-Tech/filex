package wasmplugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Manifest is a validated filex-app.json.
type Manifest struct {
	wire.Manifest
	// Perms is Permissions parsed and validated.
	Perms []Permission
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// reservedAppNames are the host's own directories under the plugins dir.
var reservedAppNames = map[string]bool{"cache": true, "spool": true, "public": true, "assets": true}
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// Limits the host imposes on what a manifest may ask for.
const (
	DefaultMemoryPages = 1024 // 64 MiB
	MaxMemoryPages     = 4096 // 256 MiB
	DefaultCallTimeout = 15   // seconds, views/pages/describe
	MaxCallTimeout     = 60
	DefaultJobTimeout  = 300 // seconds, action_run
	MaxJobTimeout      = 900
	// MaxTickTimeout caps the hourly wake-up. It is deliberately tighter
	// than a screen's ceiling: a screen has a person waiting and 60 s is
	// defensible, while a wake-up runs unattended and one that hangs holds
	// up every other app's schedule behind it.
	MaxTickTimeout = 30
)

// Messages (wire.Manifest.Messages) are short sentences filex says on an
// app's behalf; the limits keep a manifest from carrying a book in them.
const (
	maxMessages     = 64
	maxMessageBytes = 300
)

// ParseManifest decodes and validates a manifest document.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m.Manifest); err != nil {
		// Unknown fields are a warning in spirit, an error in practice: a typo
		// in "permisions" would otherwise install a plugin with no grants at all
		// and every host call refused — a confusing failure. Say it up front.
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks the shape and fills Perms.
func (m *Manifest) Validate() error {
	if m.ManifestVersion != wire.ProtocolVersion {
		return fmt.Errorf("manifest: manifest_version %d is not supported (this filex speaks %d)", m.ManifestVersion, wire.ProtocolVersion)
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("manifest: name %q must match %s", m.Name, nameRe)
	}
	// ⚠ An app's name is also its directory beside the host's own ones
	// (registry.go: cache, spool, public, and assets for asset_fetch). An app
	// named "spool" would share the spool, and uninstalling it would remove
	// every call's working files — so those names are not app names.
	if reservedAppNames[m.Name] {
		return fmt.Errorf("manifest: name %q is reserved by the host", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if m.Label["en"] == "" {
		return fmt.Errorf("manifest: label.en is required")
	}
	m.Perms = m.Perms[:0]
	seen := map[Permission]bool{}
	for _, raw := range m.Permissions {
		p, err := ParsePermission(raw)
		if err != nil {
			return fmt.Errorf("manifest: %w", err)
		}
		if !seen[p] {
			seen[p] = true
			m.Perms = append(m.Perms, p)
		}
	}
	if len(m.Languages) == 0 {
		m.Languages = []string{"en"}
	}
	seenLang := map[string]bool{}
	for i, code := range m.Languages {
		c := strings.ToLower(strings.TrimSpace(code))
		if !langRe.MatchString(c) {
			return fmt.Errorf("manifest: languages[%d] %q is not a language tag", i, code)
		}
		if seenLang[c] {
			return fmt.Errorf("manifest: languages: duplicate %q", c)
		}
		seenLang[c] = true
		m.Languages[i] = c
	}
	if !seenLang["en"] {
		return fmt.Errorf("manifest: languages must include \"en\" — it is what every other language falls back to")
	}
	if err := m.checkUILocales(); err != nil {
		return err
	}
	fieldKeys := map[string]bool{}
	for i := range m.Settings {
		f := &m.Settings[i]
		if f.Key == "" {
			return fmt.Errorf("manifest: settings[%d]: key is required", i)
		}
		if f.Type == "" {
			f.Type = "string"
		}
		switch f.Style {
		case "", "switch", "choice":
		default:
			return fmt.Errorf("manifest: settings[%d] %q: style %q must be switch or choice", i, f.Key, f.Style)
		}
		if f.Type == "select" && len(f.Options) == 0 {
			return fmt.Errorf("manifest: settings[%d] %q: a choice with no options", i, f.Key)
		}
		fieldKeys[f.Key] = true
	}
	for i := range m.Settings {
		f := &m.Settings[i]
		for _, c := range []*wire.Condition{f.ShowWhen, f.RequiredWhen} {
			if c == nil {
				continue
			}
			if !fieldKeys[c.Key] {
				return fmt.Errorf("manifest: settings[%d] %q: depends on %q, which is not a field here", i, f.Key, c.Key)
			}
			if len(c.Equals) == 0 {
				return fmt.Errorf("manifest: settings[%d] %q: a condition with no values", i, f.Key)
			}
		}
	}
	if err := m.checkLanguages(); err != nil {
		return err
	}
	viewIDs := map[string]bool{}
	for i := range m.Views {
		v := &m.Views[i]
		if !idRe.MatchString(v.ID) {
			return fmt.Errorf("manifest: views[%d]: id %q must match %s", i, v.ID, idRe)
		}
		if viewIDs[v.ID] {
			return fmt.Errorf("manifest: views: duplicate id %q", v.ID)
		}
		viewIDs[v.ID] = true
		switch v.Placement {
		case "modal", "page", "inspector", "home":
		case "":
			v.Placement = "modal"
		default:
			return fmt.Errorf("manifest: views[%d]: placement %q must be modal, page, inspector or home", i, v.Placement)
		}
		if v.Label["en"] == "" {
			return fmt.Errorf("manifest: views[%d]: label.en is required", i)
		}
		if err := validateApplies(&v.Applies); err != nil {
			return fmt.Errorf("manifest: views[%d]: %w", i, err)
		}
	}
	actionIDs := map[string]bool{}
	for i := range m.Actions {
		a := &m.Actions[i]
		if !idRe.MatchString(a.ID) {
			return fmt.Errorf("manifest: actions[%d]: id %q must match %s", i, a.ID, idRe)
		}
		if actionIDs[a.ID] {
			return fmt.Errorf("manifest: actions: duplicate id %q", a.ID)
		}
		actionIDs[a.ID] = true
		if a.Label["en"] == "" {
			return fmt.Errorf("manifest: actions[%d]: label.en is required", i)
		}
		if err := validateApplies(&a.Applies); err != nil {
			return fmt.Errorf("manifest: actions[%d]: %w", i, err)
		}
		if a.View != "" && !viewIDs[a.View] {
			return fmt.Errorf("manifest: actions[%d]: view %q is not declared in views", i, a.View)
		}
		switch a.MinRole {
		case "", "viewer", "editor", "owner":
		default:
			return fmt.Errorf("manifest: actions[%d]: min_role %q must be viewer, editor or owner", i, a.MinRole)
		}
		switch a.Output.Mode {
		case "":
			a.Output.Mode = "none"
		case "sibling", "version", "none":
		case "folder":
			// A folder is the person's choice, made on the screen for one
			// job (JobRequest.Output) — a manifest cannot know it.
			return fmt.Errorf("manifest: actions[%d]: output.mode \"folder\" is chosen per job; declare sibling with output.elsewhere instead", i)
		default:
			return fmt.Errorf("manifest: actions[%d]: output.mode %q must be sibling, version or none", i, a.Output.Mode)
		}
		if a.Output.Mode != "none" && !seen[PermFilesWrite] {
			return fmt.Errorf("manifest: actions[%d]: output.mode %q needs the files:write permission", i, a.Output.Mode)
		}
		if a.Output.Elsewhere && a.Output.Mode != "sibling" {
			return fmt.Errorf("manifest: actions[%d]: output.elsewhere is for a NEW file (mode sibling), not %q", i, a.Output.Mode)
		}
		if a.Output.Dir != "" {
			return fmt.Errorf("manifest: actions[%d]: output.dir is chosen per job, not declared", i)
		}
		if a.Applies.Writable && !seen[PermFilesWrite] {
			return fmt.Errorf("manifest: actions[%d]: applies.writable needs the files:write permission", i)
		}
		if a.Output.Mode == "sibling" && a.Output.Name == "" {
			a.Output.Name = "{stem}-" + a.ID + "{ext}"
		}
	}
	for i := range m.PublicPages {
		p := &m.PublicPages[i]
		if !idRe.MatchString(p.ID) {
			return fmt.Errorf("manifest: public_pages[%d]: id %q must match %s", i, p.ID, idRe)
		}
		switch p.PIN {
		case "", "optional", "required", "none":
		default:
			return fmt.Errorf("manifest: public_pages[%d]: pin %q must be optional, required or none", i, p.PIN)
		}
		if p.MaxTTLDays > 0 && p.DefaultTTLDays > p.MaxTTLDays {
			return fmt.Errorf("manifest: public_pages[%d]: default_ttl_days exceeds max_ttl_days", i)
		}
	}
	if len(m.PublicPages) > 0 && !seen[PermPublicPages] {
		return fmt.Errorf("manifest: public_pages need the public_pages permission")
	}
	if len(m.Messages) > maxMessages {
		return fmt.Errorf("manifest: at most %d messages", maxMessages)
	}
	for key, t := range m.Messages {
		if !idRe.MatchString(key) {
			return fmt.Errorf("manifest: messages: key %q must match %s", key, idRe)
		}
		if strings.TrimSpace(t["en"]) == "" {
			return fmt.Errorf("manifest: messages.%s: en is required", key)
		}
		for lang, s := range t {
			if len(s) > maxMessageBytes {
				return fmt.Errorf("manifest: messages.%s.%s is over %d bytes", key, lang, maxMessageBytes)
			}
		}
	}
	if m.Limits.MemoryPages < 0 || m.Limits.CallTimeoutS < 0 {
		return fmt.Errorf("manifest: limits must not be negative")
	}
	if m.Wasm != nil && m.Wasm.SHA256 != "" && len(m.Wasm.SHA256) != 64 {
		return fmt.Errorf("manifest: wasm.sha256 must be 64 hex characters")
	}
	return nil
}

// checkUILocales validates `ui_locales` — the languages an app adds to filex
// itself — and lower-cases their tags.
//
// ⚠⚠ The limit is BYTES, not a count of strings. It used to be "at most 2000
// strings per language", with the comment "a plugin adds a language, it does
// not ship a dictionary" — but a language IS the dictionary: filex has 3 593
// keys (explorer + admin panel + the text the server writes), so the cap made
// a complete translation impossible to install (measured 2026-09-21:
// 2 500 keys → "at most 2000").
// A byte ceiling per language and per manifest bounds what the server holds
// without deciding how many strings a language may need. The numbers and why
// they are what they are: wire/langpack.go.
func (m *Manifest) checkUILocales() error {
	total := 0
	norm := make(map[string]map[string]string, len(m.UILocales))
	for code, strs := range m.UILocales {
		c := strings.ToLower(strings.TrimSpace(code))
		if !langRe.MatchString(c) {
			return fmt.Errorf("manifest: ui_locales: %q is not a language tag", code)
		}
		if _, dup := norm[c]; dup {
			// "ES" and "es" would otherwise both land on `es` and one would
			// silently eat the other, depending on map order.
			return fmt.Errorf("manifest: ui_locales: %q is given twice (language tags are not case-sensitive)", c)
		}
		if len(strs) == 0 {
			return fmt.Errorf("manifest: ui_locales[%s] is empty — a language with no strings would be offered and then speak English", c)
		}
		size := 0
		for k, v := range strs {
			if !wire.UILocaleKeyOK(k) {
				return fmt.Errorf("manifest: ui_locales[%s]: %q is not a filex string key (dotted segments of letters, digits, _ and -, at most %d bytes)", c, clip(k, 80), wire.MaxUILocaleKeyBytes)
			}
			if len(v) > wire.MaxUILocaleValueBytes {
				return fmt.Errorf("manifest: ui_locales[%s]: %s is %d bytes, at most %d", c, k, len(v), wire.MaxUILocaleValueBytes)
			}
			size += len(k) + len(v)
		}
		if size > wire.MaxUILocaleBytes {
			return fmt.Errorf("manifest: ui_locales[%s] is %d bytes of strings, at most %d (a complete translation of filex is about 250 KB)", c, size, wire.MaxUILocaleBytes)
		}
		total += size
		norm[c] = strs
	}
	if total > wire.MaxUILocalesBytes {
		return fmt.Errorf("manifest: ui_locales is %d bytes of strings across its languages, at most %d", total, wire.MaxUILocalesBytes)
	}
	if len(norm) > 0 {
		m.UILocales = norm
	}
	return nil
}

func validateApplies(a *wire.Applies) error {
	switch a.Kind {
	case "":
		a.Kind = "file"
	case "file", "dir", "any":
	default:
		return fmt.Errorf("applies.kind %q must be file, dir or any", a.Kind)
	}
	for i, e := range a.Ext {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e == "" {
			return fmt.Errorf("applies.ext[%d] is empty", i)
		}
		a.Ext[i] = e
	}
	for i, mt := range a.Mime {
		mt = strings.ToLower(strings.TrimSpace(mt))
		if mt == "" {
			return fmt.Errorf("applies.mime[%d] is empty", i)
		}
		a.Mime[i] = mt
	}
	if a.Min < 0 || a.Max < 0 || (a.Max > 0 && a.Min > a.Max) {
		return fmt.Errorf("applies.min/max are inconsistent")
	}
	for _, list := range [][]string{a.State, a.NoState} {
		for i, k := range list {
			k = strings.TrimSpace(k)
			if k == "" || len(k) > 64 || strings.ContainsAny(k, ": \t\n") {
				return fmt.Errorf("applies.state[%d] %q must be a bare key (no colon, no spaces, at most 64 chars)", i, k)
			}
			// A personal key is named for "the person asking" and nobody
			// else: `todo@me`, never one person's number.
			if at := strings.Index(k, "@"); at >= 0 && (at == 0 || k[at:] != wire.PersonalStateSuffix) {
				return fmt.Errorf("applies.state[%d] %q: a personal key is named `<key>@me`", i, k)
			}
			list[i] = k
		}
	}
	if !a.Multi && a.Max > 1 {
		return fmt.Errorf("applies.max > 1 needs multi: true")
	}
	if len(a.EngineExt) > 0 && len(a.Ext) == 0 && len(a.Mime) == 0 {
		// An empty ext/mime list means "any file", so there would be nothing
		// for an engine's extensions to add to.
		return fmt.Errorf("applies.engine_ext needs a non-empty ext or mime list to add to")
	}
	for name, exts := range a.EngineExt {
		if !knownEngine(name) {
			return fmt.Errorf("applies.engine_ext: unknown engine %q (known: %s)", name, strings.Join(enginebin.Names(), ", "))
		}
		for i, e := range exts {
			e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
			if e == "" {
				return fmt.Errorf("applies.engine_ext[%s][%d] is empty", name, i)
			}
			exts[i] = e
		}
	}
	return nil
}

// withEngineExt is `a` with the extensions of every engine that is present
// (and granted) folded into Ext, and EngineExt dropped — what a client and
// the run check see. The manifest's own slices are never written to.
func withEngineExt(a wire.Applies, engines map[string]bool) wire.Applies {
	if len(a.EngineExt) == 0 {
		return a
	}
	ext := append([]string(nil), a.Ext...)
	names := make([]string, 0, len(a.EngineExt))
	for name := range a.EngineExt {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if engines[name] {
			ext = append(ext, a.EngineExt[name]...)
		}
	}
	a.Ext = ext
	a.EngineExt = nil
	return a
}

// Grants is the full permission set the manifest asks for.
func (m *Manifest) Grants() Grants { return NewGrants(m.Perms) }

// MemoryPages is the clamped wasm memory ceiling.
func (m *Manifest) MemoryPages() int {
	p := m.Limits.MemoryPages
	if p <= 0 {
		return DefaultMemoryPages
	}
	if p > MaxMemoryPages {
		return MaxMemoryPages
	}
	return p
}

// CallTimeout is the clamped per-call budget for describe/view/page calls.
func (m *Manifest) CallTimeout() int {
	t := m.Limits.CallTimeoutS
	if t <= 0 {
		return DefaultCallTimeout
	}
	if t > MaxCallTimeout {
		return MaxCallTimeout
	}
	return t
}

// JobTimeout is the clamped budget for one action_run of an action.
func JobTimeout(l wire.Limits) int {
	t := l.TimeoutS
	if t <= 0 {
		return DefaultJobTimeout
	}
	if t > MaxJobTimeout {
		return MaxJobTimeout
	}
	return t
}

// TickTimeout is the clamped budget for one `tick` call: whatever the
// manifest asked for as a call timeout, never more than MaxTickTimeout.
func TickTimeout(m *Manifest) int {
	t := m.CallTimeout()
	if t > MaxTickTimeout {
		return MaxTickTimeout
	}
	return t
}

// WritesElsewhere reports whether any action may put its result into a
// folder the person chooses (output.elsewhere) — what makes the host tell
// the app where that person's results go by default (CallContext.Home).
func (m *Manifest) WritesElsewhere() bool {
	for _, a := range m.Actions {
		if a.Output.Elsewhere {
			return true
		}
	}
	return false
}

// Action finds an action by id.
func (m *Manifest) Action(id string) (*wire.Action, bool) {
	for i := range m.Actions {
		if m.Actions[i].ID == id {
			return &m.Actions[i], true
		}
	}
	return nil, false
}

// View finds a view by id.
func (m *Manifest) View(id string) (*wire.View, bool) {
	for i := range m.Views {
		if m.Views[i].ID == id {
			return &m.Views[i], true
		}
	}
	return nil, false
}

// Matches reports whether an Applies rule accepts a selection described by
// (kind, ext, mime) per item. It is the one implementation the server uses
// for every check; the explorer mirrors it client-side for the menu.
func Matches(a wire.Applies, items []Item) bool {
	n := len(items)
	if n == 0 {
		return false
	}
	if n > 1 && !a.Multi {
		return false
	}
	if a.Min > 0 && n < a.Min {
		return false
	}
	if a.Max > 0 && n > a.Max {
		return false
	}
	for _, it := range items {
		if a.Kind != "any" && a.Kind != it.Kind {
			return false
		}
		if len(a.State) > 0 {
			hit := false
			for _, k := range a.State {
				if it.HasState(k) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		for _, k := range a.NoState {
			if it.HasState(k) {
				return false
			}
		}
		if len(a.Ext) == 0 && len(a.Mime) == 0 {
			continue
		}
		ok := false
		ext := strings.ToLower(it.Ext)
		for _, e := range a.Ext {
			if e == ext {
				ok = true
				break
			}
		}
		if !ok {
			mime := strings.ToLower(it.Mime)
			for _, m := range a.Mime {
				if m == mime || (strings.HasSuffix(m, "/*") && strings.HasPrefix(mime, strings.TrimSuffix(m, "*"))) {
					ok = true
					break
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Item is what Matches sees of a selected entry.
type Item struct {
	Kind string // file | dir
	Ext  string // lower-case, no dot
	Mime string
	// State is the set of state keys the plugin under consideration keeps
	// on this item (bare keys). Filled per plugin by the caller.
	State []string
}

// HasState reports whether the item carries key.
func (it Item) HasState(key string) bool {
	for _, k := range it.State {
		if k == key {
			return true
		}
	}
	return false
}

var langRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

// checkLanguages refuses a manifest whose visible text does not carry every
// language the plugin claims. A screen that is half Turkish under an English
// heading is the bug this catches, at install, before anyone sees it.
func (m *Manifest) checkLanguages() error {
	missing := func(what string, t wire.Text) error {
		for _, lang := range m.Languages {
			if strings.TrimSpace(t[lang]) == "" {
				return fmt.Errorf("manifest: %s has no %q text, but the plugin declares that language", what, lang)
			}
		}
		return nil
	}
	if err := missing("label", m.Label); err != nil {
		return err
	}
	for i := range m.Actions {
		if err := missing(fmt.Sprintf("actions[%d] (%s) label", i, m.Actions[i].ID), m.Actions[i].Label); err != nil {
			return err
		}
		if len(m.Actions[i].Confirm) > 0 {
			if err := missing(fmt.Sprintf("actions[%d] (%s) confirm", i, m.Actions[i].ID), m.Actions[i].Confirm); err != nil {
				return err
			}
		}
	}
	for i := range m.Views {
		if err := missing(fmt.Sprintf("views[%d] (%s) label", i, m.Views[i].ID), m.Views[i].Label); err != nil {
			return err
		}
	}
	// A setting written as ONE string is the author's choice (a plain string
	// is still accepted, so every manifest from before this keeps
	// installing); a setting written as a {lang: …} map promises every
	// language, exactly like an action's label.
	for i := range m.Settings {
		f := m.Settings[i]
		if f.I18n == nil {
			continue
		}
		for part, t := range map[string]wire.Text{"label": f.I18n.Label, "help": f.I18n.Help, "placeholder": f.I18n.Placeholder} {
			if len(t) == 0 {
				continue
			}
			if err := missing(fmt.Sprintf("settings[%d] (%s) %s", i, f.Key, part), t); err != nil {
				return err
			}
		}
	}
	for i := range m.PublicPages {
		if err := missing(fmt.Sprintf("public_pages[%d] (%s) label", i, m.PublicPages[i].ID), m.PublicPages[i].Label); err != nil {
			return err
		}
		if err := checkPurpose(m.PublicPages[i].Purpose, m.Languages); err != nil {
			return fmt.Errorf("manifest: public_pages[%d] (%s) %w", i, m.PublicPages[i].ID, err)
		}
	}
	for key, t := range m.Messages {
		if err := missing("messages."+key, t); err != nil {
			return err
		}
	}
	return nil
}

// checkPurpose is the ONE check of a link's purpose (wire.PagePurpose),
// whether it comes from a manifest page or from share_create itself — the
// page-less link of a finished document has no page to declare one. A label
// is required and, like the revoke sentence when there is one, carries every
// language the app declares; the section is an id. Nil is no purpose at all.
func checkPurpose(p *wire.PagePurpose, langs []string) error {
	if p == nil {
		return nil
	}
	if len(p.Label) == 0 {
		return fmt.Errorf("purpose.label is required")
	}
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	for _, lang := range langs {
		if strings.TrimSpace(p.Label[lang]) == "" {
			return fmt.Errorf("purpose.label has no %q text, but the app declares that language", lang)
		}
		if len(p.Revoke) > 0 && strings.TrimSpace(p.Revoke[lang]) == "" {
			return fmt.Errorf("purpose.revoke has no %q text, but the app declares that language", lang)
		}
	}
	if p.Section != "" && !idRe.MatchString(p.Section) {
		return fmt.Errorf("purpose.section %q must match %s", p.Section, idRe)
	}
	return nil
}
