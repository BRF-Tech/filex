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
// ⚠ It strips the VALUES, not the job's params. The params are the plugin's
// own map — it may compute, rename or default anything in there — and dropping
// keys out of it would delete the plugin's own work. The values are the
// person's answers, which is exactly what the promise is about; a plugin that
// never sees a hidden field's answer cannot put it in a job.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
// all? `false` and `0` count as answers — an unticked box IS an answer.
func surfaceHasAnswer(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	case []string:
		return len(t) > 0
	}
	return true
}

// surfaceValueStrings mirrors `valueStrings`: every string a value holds — one
// for a scalar, several for a multi-select.
func surfaceValueStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, surfaceScalarString(x))
		}
		return out
	case []string:
		return append([]string(nil), t...)
	}
	return []string{surfaceScalarString(v)}
}

// surfaceScalarString is JavaScript's `String(v)` for the shapes JSON can
// produce. ⚠ A number arrives as float64 through encoding/json, and `%v` would
// print `3` as `3` but `1e+06` for a round million — `strconv` with precision
// -1 is what gives the browser's own spelling back.
func surfaceScalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case json.Number:
		return t.String()
	}
	return fmt.Sprint(v)
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

// gateSurfaceValues is the whole promise in one call, shared by the view
// handler and the public page handler so the two cannot enforce different
// rules.
//
// It draws the screen these values belong to (`draw`), and then either
//
//   - returns the keys of the visible, required fields that are empty — the
//     caller refuses the event, so no job is queued; or
//   - rewrites `data["values"]` with the hidden fields' values dropped, and
//     the caller carries on into the real event.
//
// A screen with no form fields, an event that cannot queue a job, or an event
// carrying no values all cost nothing: `draw` is not called at all.
//
// ⚠ The redraw is asked with the RAW values, because that is what the browser
// was holding when the person pressed the button. A plugin whose field LIST
// differs between the raw and the stripped values would see one extra pass of
// the cascade here; the visibility answer itself is the browser's, computed by
// the shared cascade above.
func gateSurfaceValues(event string, state, data map[string]any, draw func(wire.ViewEventInput) (*wire.Surface, error)) ([]string, error) {
	if !surfaceEventCanQueue(event) || data == nil {
		return nil, nil
	}
	values, ok := data["values"].(map[string]any)
	if !ok || len(values) == 0 {
		return nil, nil
	}
	screen, err := draw(wire.ViewEventInput{Event: surfaceRedrawEvent, State: state, Data: data})
	if err != nil {
		return nil, err
	}
	if screen == nil {
		return nil, nil
	}
	fields := surfaceFormFields(screen.Nodes)
	if len(fields) == 0 {
		return nil, nil
	}
	if missing := missingRequiredSurfaceFields(fields, values); len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for _, f := range missing {
			keys = append(keys, f.Key)
		}
		return keys, nil
	}
	data["values"] = stripHiddenSurfaceValues(fields, values)
	return nil, nil
}

// writeSurfaceRequired is the refusal a `required_when` field earns: 422 with
// the field keys, so a client can point at them rather than say "no".
func writeSurfaceRequired(w http.ResponseWriter, keys []string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":   "required",
		"message": "these fields are required and empty: " + strings.Join(keys, ", "),
		"fields":  keys,
	})
}
