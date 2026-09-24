package wasmplugin

// An administrator's change to an action's `applies` rule is stored as what
// CHANGED against the manifest — never as a copy of the rule.
//
// ⚠⚠ Why (v0.43.0 release-candidate sweep, 2026-09-21, found beside the
// signing work): an override used to be a whole wire.Applies that REPLACED
// the manifest's rule, which froze whatever the rule was when Save was
// pressed:
//
//   - engine-gated extensions (`applies.engine_ext`, feat/043-signing
//     92f3c50b: `{"libreoffice": ["docx", "odt"]}` folded into `ext` only
//     while LibreOffice is there) were either lost from the copy — never
//     offered again once LibreOffice was installed — or copied flat and
//     offered on a .docx after LibreOffice was gone;
//   - the manifest's `state` / `no_state` gates, which the editor never
//     showed, were dropped from the copy, so an overridden "Sign" was offered
//     on files with no pending request;
//   - an upgrade that added an extension to the rule never reached an
//     overridden action.
//
// So the row keeps the delta — extensions and MIME types added and removed,
// and the scalars the admin changed — and every read resolves it against the
// CURRENT manifest and the engines present NOW (effectiveActions). The admin
// API still speaks whole rules (the editor shows a list and sends a list);
// the conversion is here, in one place.

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// appliesDeltaVersion marks a stored override as a delta. A row without it
// is a legacy whole rule (see legacyDelta).
const appliesDeltaVersion = 2

// appliesDelta is one action's stored change. Nil pointers and empty lists
// mean "as the manifest says".
type appliesDelta struct {
	V     int     `json:"v"`
	Kind  *string `json:"kind,omitempty"`
	Multi *bool   `json:"multi,omitempty"`
	Min   *int    `json:"min,omitempty"`
	Max   *int    `json:"max,omitempty"`
	// AnyFile: the admin cleared every extension and MIME type — the action
	// is offered on any file of its kind. Said explicitly, because "nothing
	// left after the removals" must NOT mean "any file" (see resolve).
	AnyFile    bool     `json:"any_file,omitempty"`
	ExtAdd     []string `json:"ext_add,omitempty"`
	ExtRemove  []string `json:"ext_remove,omitempty"`
	MimeAdd    []string `json:"mime_add,omitempty"`
	MimeRemove []string `json:"mime_remove,omitempty"`
}

// manifestEngineExt is an action's engine-gated extensions, by engine
// (`applies.engine_ext`, feat/043-signing 92f3c50b).
//
// ⚠⚠ The merge seam of feat/043-apps-polish × feat/043-signing, connected:
// the override branch was written before wire.Applies had EngineExt and
// answered nil here, which would have made every override engine-free — an
// override saved without LibreOffice would never offer .docx once it is
// installed. A var only so the tests can hand an action engine-gated
// extensions the echo fixture's manifest does not declare.
var manifestEngineExt = func(a wire.Applies) map[string][]string { return a.EngineExt }

// clearEngineExt drops the engine map from a rule resolve has already folded.
// ⚠ It must be cleared: effectiveActions folds EngineExt into Ext again
// after the override (`withEngineExt`, for the rows nobody overrode), which
// would bring back an engine extension the admin removed. Cleared here, that
// second fold is a no-op for an overridden row.
func clearEngineExt(a wire.Applies) wire.Applies {
	a.EngineExt = nil
	return a
}

// engineExtAll is every extension any engine adds, engines in name order.
func engineExtAll(g map[string][]string) []string {
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		out = append(out, g[n]...)
	}
	return out
}

func normExt(e string) string  { return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")) }
func normMime(m string) string { return strings.ToLower(strings.TrimSpace(m)) }

// union keeps order and drops repeats (after norm) and removals.
func union(norm func(string) string, remove []string, lists ...[]string) []string {
	gone := map[string]bool{}
	for _, r := range remove {
		gone[norm(r)] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, x := range l {
			x = norm(x)
			if x == "" || seen[x] || gone[x] {
				continue
			}
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// minus is a − b, in a's order.
func minus(norm func(string) string, a, b []string) []string {
	return union(norm, b, a)
}

// appliesKindOf is a rule's kind, "file" when unset. (Not kindOf, which is
// an APP's kind — a module or a language pack, langpack.go.)
func appliesKindOf(k string) string {
	if k == "" {
		return "file"
	}
	return k
}

// editorRule is the rule the admin edits: the manifest's extensions plus
// EVERY engine's — whether that engine is on this server or not, because the
// admin decides about .docx once, not once per install of LibreOffice — with
// the delta applied. The admin API's OverrideRow.Applies is this.
func editorRule(m wire.Applies, g map[string][]string, d *appliesDelta) wire.Applies {
	out := m
	out.Ext = union(normExt, nil, m.Ext, engineExtAll(g))
	out.Mime = union(normMime, nil, m.Mime)
	out.Kind = appliesKindOf(m.Kind)
	if d == nil {
		return clearEngineExt(out)
	}
	d.applyScalars(&out)
	if d.AnyFile {
		out.Ext, out.Mime = nil, nil
		return clearEngineExt(out)
	}
	out.Ext = union(normExt, d.ExtRemove, out.Ext, d.ExtAdd)
	out.Mime = union(normMime, d.MimeRemove, out.Mime, d.MimeAdd)
	return clearEngineExt(out)
}

func (d *appliesDelta) applyScalars(out *wire.Applies) {
	if d.Kind != nil {
		out.Kind = *d.Kind
	}
	if d.Multi != nil {
		out.Multi = *d.Multi
	}
	if d.Min != nil {
		out.Min = *d.Min
	}
	if d.Max != nil {
		out.Max = *d.Max
	}
}

// resolve is the rule the explorer's menu and the run check see: the
// manifest's base extensions and those of the engines PRESENT (and granted)
// now, with the admin's removals taken out and additions put in; the
// manifest's state gates untouched.
//
// offerable is false when the admin's choice leaves NO extension and no MIME
// type although the rule had some — "only .docx" while LibreOffice is
// missing. An empty list means "any file" to Matches, so resolving that to
// empty would widen the action to everything; it is offered on nothing
// instead (effectiveActions turns it off).
func (d *appliesDelta) resolve(m wire.Applies, g map[string][]string, present map[string]bool) (a wire.Applies, offerable bool) {
	out := m
	out.Kind = appliesKindOf(m.Kind)
	d.applyScalars(&out)
	if d.AnyFile {
		out.Ext, out.Mime = nil, nil
		return clearEngineExt(out), true
	}
	var engineExt []string
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if present[n] {
			engineExt = append(engineExt, g[n]...)
		}
	}
	out.Ext = union(normExt, d.ExtRemove, m.Ext, d.ExtAdd, engineExt)
	out.Mime = union(normMime, d.MimeRemove, m.Mime, d.MimeAdd)
	had := len(m.Ext)+len(m.Mime)+len(d.ExtAdd)+len(d.MimeAdd)+len(engineExtAll(g)) > 0
	if had && len(out.Ext) == 0 && len(out.Mime) == 0 {
		return clearEngineExt(out), false
	}
	return clearEngineExt(out), true
}

// deltaFrom is what the admin changed: `edited` (validated) against the
// editor's starting point, editorRule(m, g, nil). Nil when nothing differs —
// the row then keeps no rule at all and follows the manifest entirely.
func deltaFrom(m wire.Applies, g map[string][]string, edited wire.Applies) *appliesDelta {
	base := editorRule(m, g, nil)
	d := &appliesDelta{V: appliesDeltaVersion}
	changed := false
	if k := appliesKindOf(edited.Kind); k != base.Kind {
		d.Kind, changed = &k, true
	}
	if edited.Multi != base.Multi {
		v := edited.Multi
		d.Multi, changed = &v, true
	}
	if edited.Min != base.Min {
		v := edited.Min
		d.Min, changed = &v, true
	}
	if edited.Max != base.Max {
		v := edited.Max
		d.Max, changed = &v, true
	}
	if len(edited.Ext) == 0 && len(edited.Mime) == 0 && len(base.Ext)+len(base.Mime) > 0 {
		d.AnyFile = true
		return d
	}
	d.ExtAdd = minus(normExt, edited.Ext, base.Ext)
	d.ExtRemove = minus(normExt, base.Ext, edited.Ext)
	d.MimeAdd = minus(normMime, edited.Mime, base.Mime)
	d.MimeRemove = minus(normMime, base.Mime, edited.Mime)
	if changed || len(d.ExtAdd)+len(d.ExtRemove)+len(d.MimeAdd)+len(d.MimeRemove) > 0 {
		return d
	}
	return nil
}

// legacyDelta converts a row written before deltas (a whole wire.Applies that
// replaced the manifest's rule) — the admin's explicit choice, so what it
// SAYS is kept:
//
//   - an extension or MIME type in the manifest's base list and not in the
//     row was removed by the admin → removed;
//   - one in the row and not in the manifest was added by the admin → added,
//     unless it is one an engine adds: the old editor never showed those, so
//     a row that holds one holds it because the host had folded it in —
//     it stays engine-gated rather than becoming "always";
//   - an engine's extension NOT in the row was never shown to the admin, so
//     it was not removed: it follows its engine;
//   - a row with no extension and no MIME type said "any file", and still
//     does.
func legacyDelta(m wire.Applies, g map[string][]string, legacy wire.Applies) *appliesDelta {
	d := &appliesDelta{V: appliesDeltaVersion}
	changed := false
	if k := appliesKindOf(legacy.Kind); k != appliesKindOf(m.Kind) {
		d.Kind, changed = &k, true
	}
	if legacy.Multi != m.Multi {
		v := legacy.Multi
		d.Multi, changed = &v, true
	}
	if legacy.Min != m.Min {
		v := legacy.Min
		d.Min, changed = &v, true
	}
	if legacy.Max != m.Max {
		v := legacy.Max
		d.Max, changed = &v, true
	}
	if len(legacy.Ext) == 0 && len(legacy.Mime) == 0 && len(m.Ext)+len(m.Mime) > 0 {
		d.AnyFile = true
		return d
	}
	d.ExtAdd = minus(normExt, legacy.Ext, union(normExt, nil, m.Ext, engineExtAll(g)))
	d.ExtRemove = minus(normExt, m.Ext, legacy.Ext)
	d.MimeAdd = minus(normMime, legacy.Mime, m.Mime)
	d.MimeRemove = minus(normMime, m.Mime, legacy.Mime)
	if changed || len(d.ExtAdd)+len(d.ExtRemove)+len(d.MimeAdd)+len(d.MimeRemove) > 0 {
		return d
	}
	return nil
}

// isDeltaJSON: a stored rule already in the delta shape.
func isDeltaJSON(raw string) bool {
	var probe struct {
		V int `json:"v"`
	}
	return json.Unmarshal([]byte(raw), &probe) == nil && probe.V == appliesDeltaVersion
}

// storedDelta reads an override row's rule as a delta — converting a legacy
// row on the fly (Load converts and rewrites them; this is the fallback for
// a row it could not). Nil = as the manifest says.
func storedDelta(raw string, m wire.Applies) *appliesDelta {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if isDeltaJSON(raw) {
		var d appliesDelta
		if json.Unmarshal([]byte(raw), &d) != nil {
			return nil
		}
		return &d
	}
	var legacy wire.Applies
	if json.Unmarshal([]byte(raw), &legacy) != nil {
		return nil
	}
	return legacyDelta(m, manifestEngineExt(m), legacy)
}

// encodeDelta is the stored form ("" for no change).
func encodeDelta(d *appliesDelta) string {
	if d == nil {
		return ""
	}
	d.V = appliesDeltaVersion
	b, _ := json.Marshal(d)
	return string(b)
}
