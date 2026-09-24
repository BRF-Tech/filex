package plugintest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── findings ───────────────────────────────────────────────────────────

// TB is the part of *testing.T the kit uses. Taking an interface keeps
// `testing` out of the plugin's production build.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// Severity separates "this is broken" from "look at this".
type Severity string

// The two severities.
const (
	// SevError fails the test: filex would refuse or mis-draw this.
	SevError Severity = "error"
	// SevWarn is reported but does not fail: it is probably a mistake, and
	// the author is the one who can tell.
	SevWarn Severity = "warning"
)

// Finding is one thing the kit noticed, with where it is.
type Finding struct {
	Severity Severity
	Where    string
	Message  string
}

func (f Finding) String() string { return string(f.Severity) + " " + f.Where + ": " + f.Message }

// Report is a list of findings.
type Report []Finding

func (r Report) add(sev Severity, where, format string, args ...any) Report {
	return append(r, Finding{Severity: sev, Where: where, Message: fmt.Sprintf(format, args...)})
}

func (r Report) err(where, format string, args ...any) Report {
	return r.add(SevError, where, format, args...)
}

func (r Report) warn(where, format string, args ...any) Report {
	return r.add(SevWarn, where, format, args...)
}

// Errors is the subset that fails a test.
func (r Report) Errors() Report {
	var out Report
	for _, f := range r {
		if f.Severity == SevError {
			out = append(out, f)
		}
	}
	return out
}

// Warnings is the subset that is only reported.
func (r Report) Warnings() Report {
	var out Report
	for _, f := range r {
		if f.Severity == SevWarn {
			out = append(out, f)
		}
	}
	return out
}

// Report hands the findings to the test: errors fail it, warnings are
// logged. `strict` turns warnings into failures too.
func (r Report) Report(t TB, strict bool) {
	t.Helper()
	for _, f := range r {
		if f.Severity == SevError || strict {
			t.Errorf("%s: %s", f.Where, f.Message)
			continue
		}
		t.Logf("warning — %s: %s", f.Where, f.Message)
	}
}

// ── the catalogue ──────────────────────────────────────────────────────

// NodeTypes is filex's node catalogue — the components a surface may ask
// for. A type that is not here is drawn by nothing: the screen comes up
// with a hole in it. Kept in step with the table in docs/PLUGIN-KIT.md.
var NodeTypes = map[string]bool{
	"text": true, "divider": true, "row": true, "form": true, "steps": true,
	"list": true, "progress": true, "people-picker": true, "pin-input": true,
	"file-chooser": true, "preview": true, "pdf-fields": true, "signature-pad": true,
}

// LayoutNodes are the node types that may carry children.
var LayoutNodes = map[string]bool{"row": true}

// FieldTypes are the input types a form field may declare.
var FieldTypes = map[string]bool{
	"string": true, "password": true, "int": true, "bool": true,
	"select": true, "text": true, "date": true,
}

// Permissions is the closed set a manifest may ask for; the parameterised
// ones (`http:<host>`, `engines:<name>`) are checked by prefix. `events:<name>`
// is refused — see manifest.go.
// ⚠ It must track wasmplugin's own set (internal/wasmplugin/permissions.go).
// `schedule` was missing here for a release after the host accepted it, so a
// plugin asking for the hourly wake-up had its manifest REFUSED by its own
// tests while the server installed it happily — the kit, not the app, was
// wrong. Add the name here in the same change that adds it there.
var Permissions = map[string]bool{
	"files:read": true, "files:write": true, "files:lock": true, "sign": true,
	"mail:send": true, "notify:send": true, "users:lookup": true,
	"settings": true, "state": true, "public_pages": true, "schedule": true,
}

// KnownEngines are the engines a plugin may ask for.
var KnownEngines = map[string]bool{
	"ffmpeg": true, "imagemagick": true, "libreoffice": true,
	"ghostscript": true, "poppler": true, "rsvg": true,
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ── surface inspection ─────────────────────────────────────────────────

// CheckSurface runs every structural rule against a screen and fails t on
// a breach. Warnings are logged.
func CheckSurface(t TB, m wire.Manifest, s *wire.Surface) {
	t.Helper()
	InspectSurface(m, s).Report(t, false)
}

// InspectSurface measures one screen against the renderer's rules:
//
//   - every node type is in the catalogue;
//   - a `select` is a readable choice (it renders as a row of buttons, so
//     its options must be there, labelled and distinct) and `multi` is
//     only on a select;
//   - at most ONE primary button — a step asks one thing;
//   - `show_when` / `required_when` name a field of the same form;
//   - a hidden field's value is not submitted;
//   - ids and field keys are unique (values arrive flat).
func InspectSurface(m wire.Manifest, s *wire.Surface) Report {
	var r Report
	if s == nil {
		return r.err("surface", "the plugin answered no surface")
	}

	ids := map[string]string{}
	keys := map[string]string{}
	steps := 0
	WalkNodes(s.Nodes, func(where string, n wire.Node) {
		if n.Type == "" {
			r = r.err(where, "node has no type")
			return
		}
		if !NodeTypes[n.Type] {
			r = r.err(where, "unknown node type %q — filex has no component for it (catalogue: %s)", n.Type, catalogue())
		}
		if len(n.Children) > 0 && !LayoutNodes[n.Type] {
			r = r.warn(where, "node type %q carries children; only %s is a layout node", n.Type, "row")
		}
		if n.ID != "" {
			if prev, dup := ids[n.ID]; dup {
				r = r.err(where, "node id %q is already used at %s; the next event would carry one value for two nodes", n.ID, prev)
			}
			ids[n.ID] = where
		}
		switch n.Type {
		case "form":
			fields := FieldsOf(n)
			values := ValuesOf(n)
			if len(fields) == 0 {
				r = r.warn(where, "a form with no fields")
			}
			for _, f := range fields {
				if prev, dup := keys[f.Key]; dup {
					r = r.err(where, "field key %q is already used at %s; a form's values arrive FLAT, so one would overwrite the other", f.Key, prev)
				}
				keys[f.Key] = where
			}
			r = append(r, inspectFields(where, fields, values)...)
		case "steps":
			steps++
			r = append(r, inspectSteps(where, n)...)
		case "list":
			r = append(r, inspectList(where, n)...)
		}
	})

	primary := 0
	seenAction := map[string]bool{}
	for i, a := range s.Actions {
		where := fmt.Sprintf("surface.actions[%d]", i)
		if a.ID == "" {
			r = r.err(where, "a footer button with no id cannot be told apart in the event")
		}
		if seenAction[a.ID] {
			r = r.err(where, "two footer buttons share the id %q", a.ID)
		}
		seenAction[a.ID] = true
		if a.Primary {
			primary++
		}
	}
	if primary > 1 {
		r = r.err("surface.actions", "%d primary buttons — one step asks ONE thing, so at most one primary button plus Back", primary)
	}
	if len(s.Actions) == 0 && !s.Done && s.Job == nil {
		r = r.warn("surface.actions", "a screen with no buttons that neither closes (done) nor queues a job is a dead end")
	}
	for key := range s.Errors {
		if _, ok := keys[key]; !ok && key != "" {
			r = r.warn("surface.errors", "an error is addressed to %q, which is not a field on this screen", key)
		}
	}
	if s.Size != "" && !map[string]bool{"sm": true, "md": true, "lg": true, "xl": true, "full": true}[s.Size] {
		r = r.warn("surface.size", "unknown size %q (sm|md|lg|xl|full)", s.Size)
	}
	return r
}

func catalogue() string {
	out := make([]string, 0, len(NodeTypes))
	for k := range NodeTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// inspectFields is the shared rule set for a form's fields and for the
// manifest's settings form.
func inspectFields(where string, fields []wire.Field, values map[string]any) Report {
	var r Report
	present := map[string]wire.Field{}
	for _, f := range fields {
		present[f.Key] = f
	}
	for i, f := range fields {
		w := fmt.Sprintf("%s.fields[%d] (%s)", where, i, f.Key)
		if f.Key == "" {
			r = r.err(w, "a field with no key carries no value")
			continue
		}
		if f.Type == "" {
			r = r.err(w, "field has no type")
		} else if !FieldTypes[f.Type] {
			r = r.err(w, "unknown field type %q (%s)", f.Type, fieldTypeList())
		}
		if f.Type == "select" {
			switch {
			case len(f.Options) == 0:
				r = r.err(w, "a select with no options cannot render: filex draws a select as a ROW OF CHOICE BUTTONS, never a dropdown, so the options must be readable without clicking")
			case len(f.Options) == 1:
				r = r.warn(w, "a select with one option is not a choice; drop the field or add the other options")
			}
			seen := map[string]string{}
			labels := map[string]bool{}
			for j, o := range f.Options {
				ow := fmt.Sprintf("%s.options[%d]", w, j)
				if o.Value == "" {
					r = r.err(ow, "an option with no value cannot be chosen")
				}
				if o.Label == "" {
					r = r.err(ow, "an option with no label renders as an empty button")
				}
				if prev, dup := seen[o.Value]; dup {
					r = r.err(ow, "value %q is already option %s", o.Value, prev)
				}
				seen[o.Value] = fmt.Sprintf("[%d]", j)
				if o.Label != "" && labels[o.Label] {
					r = r.warn(ow, "two buttons carry the same label %q — the person cannot tell them apart", o.Label)
				}
				labels[o.Label] = true
			}
			if v, ok := values[f.Key]; ok && v != nil {
				if _, isList := v.([]string); f.Multi && !isList {
					if _, isAny := v.([]any); !isAny {
						r = r.warn(w, "multi select, but its value is %T — a multi select's value is a LIST of option values", v)
					}
				}
				if s, isStr := v.(string); !f.Multi && !isStr && v != nil {
					_ = s
					r = r.warn(w, "single select, but its value is %T — it should be one option value as a string", v)
				}
			}
		} else if f.Multi {
			r = r.err(w, "`multi` is a select thing; on a %s field it means nothing", f.Type)
		}
		r = append(r, inspectCondition(w, "show_when", f.ShowWhen, f.Key, present)...)
		r = append(r, inspectCondition(w, "required_when", f.RequiredWhen, f.Key, present)...)
		if f.ShowWhen != nil && f.Required && f.RequiredWhen == nil {
			r = r.warn(w, "the field is hidden by show_when but always required; a hidden required field cannot be filled — use required_when")
		}
	}
	// A hidden field's value is dropped before the job runs; a screen that
	// still sends one is asking for a surprise.
	if len(values) > 0 {
		for _, key := range HiddenKeys(fields, values) {
			if v, ok := values[key]; ok && v != nil && v != "" {
				r = r.err(where, "field %q is hidden by its show_when, but the form still carries a value for it (%v); the host DROPS it before the job runs", key, v)
			}
		}
	}
	return r
}

func fieldTypeList() string {
	out := make([]string, 0, len(FieldTypes))
	for k := range FieldTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " | ")
}

func inspectCondition(where, what string, c *wire.Condition, self string, present map[string]wire.Field) Report {
	var r Report
	if c == nil {
		return r
	}
	if c.Key == "" {
		return r.err(where, "%s names no field", what)
	}
	if c.Key == self {
		return r.err(where, "%s points at the field itself", what)
	}
	if _, ok := present[c.Key]; !ok {
		r = r.err(where, "%s looks at %q, which is not a field of this form — the rule can never be true", what, c.Key)
		return r
	}
	if len(c.Equals) == 0 {
		return r.err(where, "%s has no values to compare against", what)
	}
	other := present[c.Key]
	if other.Type == "select" && len(other.Options) > 0 {
		opts := map[string]bool{}
		for _, o := range other.Options {
			opts[o.Value] = true
		}
		for _, want := range c.Equals {
			if !opts[want] {
				r = r.err(where, "%s waits for %s == %q, which is not one of that select's options", what, c.Key, want)
			}
		}
	}
	if other.Type == "bool" {
		for _, want := range c.Equals {
			if want != "true" && want != "false" {
				r = r.err(where, "%s waits for the bool %s == %q; a bool is \"true\" or \"false\"", what, c.Key, want)
			}
		}
	}
	return r
}

func inspectSteps(where string, n wire.Node) Report {
	var r Report
	var items []struct {
		ID    string `json:"id"`
		Label any    `json:"label"`
		State string `json:"state"`
	}
	if err := remarshal(n.Props["items"], &items); err != nil || len(items) == 0 {
		return r.err(where, "a steps node needs items: [{id, label, state}]")
	}
	active := 0
	for i, it := range items {
		w := fmt.Sprintf("%s.items[%d]", where, i)
		if it.ID == "" {
			r = r.err(w, "a step with no id")
		}
		switch it.State {
		case "done", "active", "todo":
		case "":
			r = r.err(w, "a step with no state (done|active|todo)")
		default:
			r = r.err(w, "unknown step state %q (done|active|todo)", it.State)
		}
		if it.State == "active" {
			active++
		}
	}
	if active != 1 {
		r = r.err(where, "%d steps are active; exactly one step is the one the person is on", active)
	}
	return r
}

func inspectList(where string, n wire.Node) Report {
	var r Report
	// The optional fields (width, sortable, align, row.sort) are v0.43's: the
	// list is drawn by filex's one table (the explorer's), so a person can
	// resize, sort, hide and move its columns. They are checked here because a
	// wrong one fails SILENTLY in the browser — an unknown align is ignored and
	// a sort value keyed by a column that does not exist is never read.
	var cols []struct {
		Key      string   `json:"key"`
		Label    any      `json:"label"`
		Width    *float64 `json:"width"`
		Sortable *bool    `json:"sortable"`
		Align    string   `json:"align"`
		Format   string   `json:"format"`
	}
	if err := remarshal(n.Props["columns"], &cols); err != nil || len(cols) == 0 {
		return r.err(where, "a list node needs columns: [{key, label}]")
	}
	known := map[string]bool{}
	for i, c := range cols {
		cw := fmt.Sprintf("%s.columns[%d]", where, i)
		if c.Key == "" {
			r = r.err(cw, "a column with no key")
		}
		known[c.Key] = true
		if c.Width != nil && (*c.Width < 60 || *c.Width > 900) {
			r = r.warn(cw, "width %v is outside 60–900 px; it is clamped to what a person could drag it to", *c.Width)
		}
		if c.Align != "" && c.Align != "left" && c.Align != "right" && c.Align != "center" {
			r = r.err(cw, "align %q is not left, right or center", c.Align)
		}
		// A format the host does not know prints the value raw — the ISO
		// date the format exists to keep off a person's screen.
		if c.Format != "" && c.Format != "date" && c.Format != "datetime" {
			r = r.err(cw, "format %q is not date or datetime", c.Format)
		}
	}
	var rows []struct {
		ID    string         `json:"id"`
		Cells map[string]any `json:"cells"`
		Sort  map[string]any `json:"sort"`
	}
	if err := remarshal(n.Props["rows"], &rows); err != nil {
		return r.err(where, "rows must be [{id, cells}]")
	}
	for i, row := range rows {
		w := fmt.Sprintf("%s.rows[%d]", where, i)
		if row.ID == "" {
			r = r.err(w, "a row with no id cannot report which row an action was pressed on")
		}
		for k := range row.Cells {
			if !known[k] {
				r = r.warn(w, "cell %q has no column; it is not drawn", k)
			}
		}
		for k, v := range row.Sort {
			if !known[k] {
				r = r.warn(w, "sort value %q has no column; it is never read", k)
			}
			switch v.(type) {
			case string, float64:
			default:
				r = r.err(w, "sort value %q must be a string or a number", k)
			}
		}
	}
	return r
}

// ── reading a surface ──────────────────────────────────────────────────

// WalkNodes visits every node of a surface, depth first, with a readable
// path for messages.
func WalkNodes(nodes []wire.Node, fn func(where string, n wire.Node)) {
	var walk func(prefix string, ns []wire.Node)
	walk = func(prefix string, ns []wire.Node) {
		for i, n := range ns {
			where := fmt.Sprintf("%s[%d]", prefix, i)
			if n.Type != "" {
				where += "(" + n.Type + ")"
			}
			fn(where, n)
			if len(n.Children) > 0 {
				walk(where+".children", n.Children)
			}
		}
	}
	walk("surface.nodes", nodes)
}

// FieldsOf reads a form node's fields, whether the plugin built them as
// wire.Field values or as plain maps.
func FieldsOf(n wire.Node) []wire.Field {
	var out []wire.Field
	if err := remarshal(n.Props["fields"], &out); err != nil {
		return nil
	}
	return out
}

// ValuesOf reads a form node's current values.
func ValuesOf(n wire.Node) map[string]any {
	var out map[string]any
	if err := remarshal(n.Props["values"], &out); err != nil {
		return nil
	}
	return out
}

// Forms lists the form nodes of a surface.
func Forms(s *wire.Surface) []wire.Node {
	var out []wire.Node
	if s == nil {
		return nil
	}
	WalkNodes(s.Nodes, func(_ string, n wire.Node) {
		if n.Type == "form" {
			out = append(out, n)
		}
	})
	return out
}

// Field finds a field by key anywhere on the screen.
func Field(s *wire.Surface, key string) (wire.Field, bool) {
	for _, f := range Forms(s) {
		for _, fl := range FieldsOf(f) {
			if fl.Key == key {
				return fl, true
			}
		}
	}
	return wire.Field{}, false
}

// Choices answers the options a select renders as buttons. It is how a
// test reads a picker without knowing how it is drawn: `len(Choices(s,
// "target"))` is the number of buttons the person sees, and their labels
// are what the buttons say. The second result is false when there is no
// such field or it is not a select.
func Choices(s *wire.Surface, key string) ([]wire.FieldOption, bool) {
	f, ok := Field(s, key)
	if !ok || f.Type != "select" {
		return nil, false
	}
	return f.Options, true
}

// ChoiceValues is Choices reduced to the option values.
func ChoiceValues(s *wire.Surface, key string) []string {
	opts, _ := Choices(s, key)
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out
}

// PrimaryAction answers the step's one primary button.
func PrimaryAction(s *wire.Surface) (wire.SurfaceAction, bool) {
	if s == nil {
		return wire.SurfaceAction{}, false
	}
	for _, a := range s.Actions {
		if a.Primary {
			return a, true
		}
	}
	return wire.SurfaceAction{}, false
}

// Nodes lists every node of a type, in order.
func Nodes(s *wire.Surface, typ string) []wire.Node {
	var out []wire.Node
	if s == nil {
		return nil
	}
	WalkNodes(s.Nodes, func(_ string, n wire.Node) {
		if n.Type == typ {
			out = append(out, n)
		}
	})
	return out
}

// VisibleKeys is the set of field keys a person can see for those values,
// after `show_when` has had its say.
func VisibleKeys(fields []wire.Field, values map[string]any) []string {
	var out []string
	for _, f := range fields {
		if conditionHolds(f.ShowWhen, values) {
			out = append(out, f.Key)
		}
	}
	return out
}

// HiddenKeys is the other half of VisibleKeys: the fields `show_when` is
// keeping off the screen. The host drops their values before a job runs.
func HiddenKeys(fields []wire.Field, values map[string]any) []string {
	var out []string
	for _, f := range fields {
		if !conditionHolds(f.ShowWhen, values) {
			out = append(out, f.Key)
		}
	}
	return out
}

// RequiredKeys is the set of fields that must be filled for those values
// (`required`, plus `required_when` where the rule holds).
func RequiredKeys(fields []wire.Field, values map[string]any) []string {
	var out []string
	for _, f := range fields {
		if !conditionHolds(f.ShowWhen, values) {
			continue
		}
		if f.Required || conditionSet(f.RequiredWhen, values) {
			out = append(out, f.Key)
		}
	}
	return out
}

// Submitted is what the host would hand the job: the values of the fields
// that are visible, with everything else dropped — the same rule the host
// re-applies at submit, so a test can prove a hidden box cannot smuggle a
// value through.
func Submitted(fields []wire.Field, values map[string]any) map[string]any {
	out := map[string]any{}
	visible := map[string]bool{}
	for _, k := range VisibleKeys(fields, values) {
		visible[k] = true
	}
	for k, v := range values {
		if visible[k] {
			out[k] = v
		}
	}
	return out
}

// conditionHolds is true when there is no condition (always shown) or the
// condition is met.
func conditionHolds(c *wire.Condition, values map[string]any) bool {
	if c == nil {
		return true
	}
	return conditionSet(c, values)
}

func conditionSet(c *wire.Condition, values map[string]any) bool {
	if c == nil {
		return false
	}
	got := asCompare(values[c.Key])
	for _, want := range c.Equals {
		if got == want {
			return true
		}
	}
	return false
}

func asCompare(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

// remarshal moves a value through JSON into out, so props built as typed
// Go values and props that came back as maps read the same.
func remarshal(v any, out any) error {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
