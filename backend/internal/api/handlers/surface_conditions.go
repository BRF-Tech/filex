// Package handlers — surface_conditions.go
//
// The HOST half of `show_when` / `required_when`.
//
// The contract (APP-PLUGINS-API.md → Fields, PLUGIN-KIT.md → Screens) makes a
// plugin author two promises about a field that depends on another field:
//
//	"Both are checked on the client AND re-checked by the host at submit: a
//	 value belonging to a hidden field is dropped before the job runs, so it
//	 cannot arrive as a surprise, and a `required_when` field left empty
//	 refuses the job."
//
// The browser half is `packages/core/src/lib/surfaceConditions.ts`, called
// from `usePluginSurface` on every event it posts. This file is the other
// half, and it is a LINE-BY-LINE port of that file rather than a second
// opinion about the same rules: the two halves have to agree exactly, or a
// plugin gets one answer on screen and another in its job.
//
// ⚠ Why the host has to ask the plugin what the screen looks like. A surface
// is not in the manifest — the plugin DRAWS it per event — so the host has no
// standing list of fields to check against. At a submit it therefore asks the
// plugin to draw the screen these values belong to (one `change` call, the
// same question the browser asks on every keystroke), reads the form fields
// out of that answer, and applies the rules to the values before the real
// event is handed over. The conditions are then evaluated by the same cascade
// the browser uses, so neither side can be talked into a different answer by a
// crafted request.
//
// ⭐ Since 0.54 (#212) the same gate also judges the ANSWERS against the
// screen's declarations — a field's type, options and bounds, a PIN's
// length, a PDF box's rule — with wasmplugin/surface_values.go, the one
// judge every door shares (PutSettings included). Those rules have no
// browser twin to port: the browser only draws them, the host decides.
//
// ⚠ It strips the VALUES, not the job's params. The params are the plugin's
// own map — it may compute, rename or default anything in there — and dropping
// keys out of it would delete the plugin's own work. The values are the
// person's answers, which is exactly what the promise is about; a plugin that
// never sees a hidden field's answer cannot put it in a job.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// surfaceRedrawEvent is what the host asks the plugin when it needs to know
// what the screen these values came from looks like. `change` is the event the
// browser fires on every edit, so it is by contract a REDRAW: same step, same
// fields, no side effects. (`open` would restart the flow; `submit` is the
// event we are about to make for real.)
const surfaceRedrawEvent = "change"

// surfaceEventCanQueue says whether this event may come back carrying a job.
// `open` cannot be crafted into one (it carries no values) and `change` is the
// debounced redraw; the two deliberate presses are what the gate costs a call.
func surfaceEventCanQueue(event string) bool {
	return event == "submit" || event == "action"
}

// surfaceHasAnswer mirrors `hasAnswer`: is there anything in this value at
// all? `false` and `0` count as answers — an unticked box IS an answer. The
// host's one reading is wasmplugin.Answered (the value judge asks the same).
func surfaceHasAnswer(v any) bool { return wasmplugin.Answered(v) }

// surfaceValueStrings mirrors `valueStrings`: every string a value holds — one
// for a scalar, several for a multi-select — spelt the way the browser spells
// them (wasmplugin.ValueString).
func surfaceValueStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, wasmplugin.ValueString(x))
		}
		return out
	case []string:
		return append([]string(nil), t...)
	}
	return []string{wasmplugin.ValueString(v)}
}

// surfaceConditionMet mirrors `conditionMet`.
//
// ⚠ An absent condition HOLDS. `show_when: null` is "always", which is what
// every field without one means, so the two paths do not diverge.
func surfaceConditionMet(c *wire.Condition, values map[string]any) bool {
	if c == nil || c.Key == "" {
		return true
	}
	raw := values[c.Key]
	// No list of values: the condition is "that field has been answered".
	if len(c.Equals) == 0 {
		return surfaceHasAnswer(raw)
	}
	if !surfaceHasAnswer(raw) {
		return false
	}
	have := surfaceValueStrings(raw)
	for _, want := range c.Equals {
		for _, got := range have {
			if got == want {
				return true
			}
		}
	}
	return false
}

// visibleSurfaceFields mirrors `visibleFields`: the fields the person may see,
// conditions CASCADED.
//
// ⚠ The fixed point is the point. A field can be shown by a field that is
// itself hidden, and a hidden controller answers nothing — so visibility is
// resolved until it stops changing rather than in one pass. Without it, hiding
// the middle of a three-field chain leaves the last one on screen (and its
// value unstripped) asking about an answer nobody can see.
func visibleSurfaceFields(fields []wire.Field, values map[string]any) []wire.Field {
	all := make([]wire.Field, 0, len(fields))
	for _, f := range fields {
		if f.Key != "" {
			all = append(all, f)
		}
	}
	if len(all) == 0 {
		return nil
	}
	shown := all
	for pass := 0; pass <= len(all); pass++ {
		scope := scopedSurfaceValues(all, shown, values)
		next := make([]wire.Field, 0, len(shown))
		for _, f := range all {
			if surfaceConditionMet(f.ShowWhen, scope) {
				next = append(next, f)
			}
		}
		if len(next) == len(shown) {
			return next
		}
		shown = next
	}
	return shown
}

// scopedSurfaceValues mirrors `scopedValues`: the values as the VISIBLE fields
// answer them — a hidden field answers nothing. Values that belong to nodes
// rather than form fields (a people-picker, a file-chooser) are left alone;
// they are not what the conditions are about.
func scopedSurfaceValues(all, shown []wire.Field, values map[string]any) map[string]any {
	visible := make(map[string]bool, len(shown))
	for _, f := range shown {
		visible[f.Key] = true
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	for _, f := range all {
		if !visible[f.Key] {
			delete(out, f.Key)
		}
	}
	return out
}

// surfaceFieldRequired mirrors `fieldRequired`.
//
// ⚠ A field with no `required_when` is NOT required by default, even though an
// absent condition otherwise "holds": the browser returns false before it ever
// asks, and a host that asked would make every optional field mandatory.
func surfaceFieldRequired(f wire.Field, values map[string]any) bool {
	if f.Required {
		return true
	}
	if f.RequiredWhen == nil {
		return false
	}
	return surfaceConditionMet(f.RequiredWhen, values)
}

// surfaceFormFields mirrors `formFields`: every `form` node's fields in a
// surface, flat, in the order they are drawn. Rows and steps are transparent —
// a form nested inside a layout node is still a form on this screen, and its
// values arrive in the same flat map.
func surfaceFormFields(nodes []wire.Node) []wire.Field {
	var out []wire.Field
	var walk func([]wire.Node)
	walk = func(ns []wire.Node) {
		for _, n := range ns {
			if n.Type == "form" {
				for _, f := range surfaceNodeFields(n) {
					if f.Key != "" {
						out = append(out, f)
					}
				}
			}
			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(nodes)
	return out
}

// surfaceNodeFields reads a form node's `fields` prop back into the typed
// shape. The props are `map[string]any` on the wire, so the conditions arrive
// as nested maps; a re-marshal is the one decode that cannot drift from the
// struct tags.
func surfaceNodeFields(n wire.Node) []wire.Field {
	raw, ok := n.Props["fields"]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []wire.Field
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// hiddenSurfaceKeys mirrors `hiddenKeys`: the keys this screen is NOT showing.
func hiddenSurfaceKeys(fields []wire.Field, values map[string]any) []string {
	if len(fields) == 0 {
		return nil
	}
	shown := make(map[string]bool)
	for _, f := range visibleSurfaceFields(fields, values) {
		shown[f.Key] = true
	}
	var out []string
	for _, f := range fields {
		if f.Key != "" && !shown[f.Key] {
			out = append(out, f.Key)
		}
	}
	return out
}

// stripHiddenSurfaceValues mirrors `stripHiddenValues`.
//
// ⚠ Dropped, not blanked. `{name: ""}` is an answer ("call it nothing"); the
// contract says the value "is dropped before the job runs", so the key must
// not be in the map at all.
func stripHiddenSurfaceValues(fields []wire.Field, values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	for _, k := range hiddenSurfaceKeys(fields, values) {
		delete(out, k)
	}
	return out
}

// missingRequiredSurfaceFields mirrors `missingRequired`: the visible-and-
// required fields nobody answered. Empty means the job may go.
func missingRequiredSurfaceFields(fields []wire.Field, values map[string]any) []wire.Field {
	if len(fields) == 0 {
		return nil
	}
	all := make([]wire.Field, 0, len(fields))
	for _, f := range fields {
		if f.Key != "" {
			all = append(all, f)
		}
	}
	shown := visibleSurfaceFields(all, values)
	scope := scopedSurfaceValues(all, shown, values)
	var out []wire.Field
	for _, f := range shown {
		if surfaceFieldRequired(f, scope) && !surfaceHasAnswer(scope[f.Key]) {
			out = append(out, f)
		}
	}
	return out
}

// surfaceVerdict is what the gate decided about an event's values: the
// visible, required fields nobody answered, or the answers that do not fit
// the field, PIN box or PDF box that asked for them. Empty means the event
// may go.
type surfaceVerdict struct {
	Missing []string
	Invalid []wasmplugin.FieldProblem
}

func (v surfaceVerdict) refused() bool { return len(v.Missing) > 0 || len(v.Invalid) > 0 }

// gateSurfaceValues is the whole promise in one call, shared by the view
// handler and the public page handler so the two cannot enforce different
// rules.
//
// It draws the screen these values belong to (`draw`), and then either
//
//   - refuses: the visible, required fields that are empty (Missing), or the
//     answers the screen's own declarations do not accept (Invalid: a type,
//     an option, min/max, a PIN's length, a PDF text box's rule) — the
//     caller answers 422, so no job is queued; or
//   - rewrites `data["values"]` with the hidden fields' values dropped and
//     a `pdf-fields` fill answer reduced to the signer's own boxes (with the
//     node's labels and rules), and the caller carries on into the real
//     event.
//
// An event that cannot queue a job or one carrying no values costs nothing:
// `draw` is not called at all.
//
// ⚠ The redraw is asked with the RAW values, because that is what the browser
// was holding when the person pressed the button. A plugin whose field LIST
// differs between the raw and the stripped values would see one extra pass of
// the cascade here; the visibility answer itself is the browser's, computed by
// the shared cascade above.
func gateSurfaceValues(event string, state, data map[string]any, draw func(wire.ViewEventInput) (*wire.Surface, error)) (surfaceVerdict, error) {
	if !surfaceEventCanQueue(event) || data == nil {
		return surfaceVerdict{}, nil
	}
	values, ok := data["values"].(map[string]any)
	if !ok || len(values) == 0 {
		return surfaceVerdict{}, nil
	}
	screen, err := draw(wire.ViewEventInput{Event: surfaceRedrawEvent, State: state, Data: data})
	if err != nil {
		return surfaceVerdict{}, err
	}
	if screen == nil {
		return surfaceVerdict{}, nil
	}
	fields := surfaceFormFields(screen.Nodes)
	if missing := missingRequiredSurfaceFields(fields, values); len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for _, f := range missing {
			keys = append(keys, f.Key)
		}
		return surfaceVerdict{Missing: keys}, nil
	}
	kept, invalid := judgeSurfaceValues(screen.Nodes, fields, stripHiddenSurfaceValues(fields, values))
	if len(invalid) > 0 {
		return surfaceVerdict{Invalid: invalid}, nil
	}
	data["values"] = kept
	return surfaceVerdict{}, nil
}

// judgeSurfaceValues measures values (hidden fields already dropped) against
// the screen they belong to: every visible form field by its declaration
// (wasmplugin.CheckFieldValue), every `pin-input` by its length, every
// `pdf-fields` node in fill mode by its boxes (wasmplugin.JudgePdfFill). It
// answers the values as the app may receive them — a copy; the fill answer
// rewritten — and the problems. Values that belong to nothing on this screen
// are left as they came: the rules are about the screen's own questions.
func judgeSurfaceValues(nodes []wire.Node, fields []wire.Field, values map[string]any) (map[string]any, []wasmplugin.FieldProblem) {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	var problems []wasmplugin.FieldProblem
	for _, f := range visibleSurfaceFields(fields, out) {
		if p := wasmplugin.CheckFieldValue(f, out[f.Key]); p != nil {
			problems = append(problems, *p)
		}
	}
	var walk func([]wire.Node)
	walk = func(ns []wire.Node) {
		for _, n := range ns {
			if n.ID != "" {
				switch n.Type {
				case "pin-input":
					if p := wasmplugin.CheckPin(n.ID, wasmplugin.PinLength(n.Props["length"]), out[n.ID]); p != nil {
						problems = append(problems, *p)
					}
				case "pdf-fields":
					if mode, _ := n.Props["mode"].(string); mode == "fill" {
						if raw, has := out[n.ID]; has {
							clean, probs := wasmplugin.JudgePdfFill(n.ID, n.Props, raw)
							out[n.ID] = clean
							problems = append(problems, probs...)
						}
					}
				}
			}
			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(nodes)
	return out, problems
}

// markSurfaceProblems puts the host's verdict on the answer to a `change`:
// every value the person sent that the redrawn screen's declarations do not
// accept gets its reason under its key in `surface.errors`, in the reader's
// language — unless the app already said something there (its words win).
//
// ⚠ This is what lets the browser stop judging. A form posts `change` as it
// is edited, and the answer to every change is redrawn here, so a number
// outside min/max or an option the screen does not offer is marked while
// the person is still on the field, in the server's words; the submit is
// refused by gateSurfaceValues with the same words. An empty required field
// is not marked here — a person who has not reached it yet is not wrong.
//
// ⚠ ONLY a `change`: that answer is the same screen the values were typed
// into. The answer to a submit or a row action is usually the NEXT step,
// and judging the last step's answers against its fields would mark boxes
// the person has not touched yet (a step that reuses a key with other
// options). Those values were judged by the gate, against their own screen.
func markSurfaceProblems(event string, s *wire.Surface, data map[string]any, lang string) {
	if event != surfaceRedrawEvent || s == nil || data == nil {
		return
	}
	values, ok := data["values"].(map[string]any)
	if !ok || len(values) == 0 {
		return
	}
	fields := surfaceFormFields(s.Nodes)
	_, problems := judgeSurfaceValues(s.Nodes, fields, stripHiddenSurfaceValues(fields, values))
	for _, p := range problems {
		if s.Errors == nil {
			s.Errors = map[string]wire.Text{}
		}
		if len(s.Errors[p.Key]) > 0 {
			continue
		}
		s.Errors[p.Key] = wire.Text{lang: p.Say(lang)}
	}
}

// writeSurfaceRefused is the 422 a refused event earns, in the reader's
// language: `required` with the empty fields' keys, or `invalid` with every
// refused answer's key and reason (`invalid[key]` is the sentence the screen
// marks the field with), so a client points at the fields rather than says
// "no".
func writeSurfaceRefused(w http.ResponseWriter, lang string, v surfaceVerdict) {
	if len(v.Missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "required",
			"message": srvtext.Text(lang, "server.field.required_fields", nil),
			"fields":  v.Missing,
		})
		return
	}
	keys := make([]string, 0, len(v.Invalid))
	said := make(map[string]string, len(v.Invalid))
	for _, p := range v.Invalid {
		keys = append(keys, p.Key)
		said[p.Key] = p.Say(lang)
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":   "invalid",
		"message": srvtext.Text(lang, "server.field.invalid", nil),
		"fields":  keys,
		"invalid": said,
		"reasons": v.Invalid,
	})
}
