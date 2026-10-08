package wasmplugin

// surface_values.go — what an answer to an app's question may be, decided
// by the HOST.
//
// An app asks for values in three places: the fields of a `form` node (and
// the same Field declarations as its manifest settings), a `pin-input` node,
// and the boxes of a `pdf-fields` node a signer fills. The browser draws all
// three with the declared rules — a number box, buttons for the options, a
// code box of the declared length, a text box that drops letters from a
// number — but the browser is not the boundary: a crafted event, an outside
// signer on a public page, or an API key writing settings never ran that
// code. So the rules are judged here, once, for every door:
//
//   - the view and public page handlers judge an event's values against the
//     screen they belong to (handlers/surface_conditions.go gateSurfaceValues)
//     and refuse the event; on every answer they also mark the screen's
//     offending fields with these words (surface.errors), so a person sees the
//     host's verdict while typing;
//   - PutSettings judges an administrator's settings against the manifest.
//
// The words are the server's (srvtext `server.field.*`), in the reader's
// language. The browser keeps only what shapes input as it is typed (a
// number box's keyboard, a PIN box's width) and reads every bound it uses
// from the surface the host sent — SanitizeSurface normalises the two it
// reads (`pin-input` length, a `pdf-fields` text box's rule) on the way out,
// so the screen and this judge read the same numbers.

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The reasons an answer does not fit the field that asked for it. They are
// the API's codes (`reason` on a refused setting) and the last part of the
// srvtext key that says them (`server.field.<code>`).
const (
	FieldBadInt    = "int"       // not a whole number
	FieldBadBool   = "bool"      // not yes or no
	FieldBadText   = "text"      // not text (a list or an object where text was asked)
	FieldBadOption = "option"    // not one of the offered options
	FieldBelowMin  = "min"       // a number below the field's min
	FieldAboveMax  = "max"       // a number above the field's max
	FieldRequired  = "required"  // a required field left empty
	FieldBadPin    = "pin"       // a code that is not the declared length (Limit)
	FieldBadDate   = "date"      // a date box that does not hold YYYY-MM-DD
	FieldBadNumber = "number"    // a text box whose rule asks for a number
	FieldBadEmail  = "email"     // a text box whose rule asks for an address
	FieldTooShort  = "too_short" // a text box under its rule's min length (Limit)
	FieldTooLong   = "too_long"  // a text box over its rule's max length (Limit)
	FieldBadShape  = "shape"     // a node's answer is not the shape it sends
)

// FieldProblem is why one answer was refused.
type FieldProblem struct {
	// Key is where the answer sits: a form field's key, a node's id, or
	// `<node id>.<box id>` for one box of a `pdf-fields` node — the key the
	// screen marks (surface.errors) and a refusal names (`fields`).
	Key  string `json:"key"`
	Code string `json:"reason"`
	// Limit is the bound that was broken (min, max, a code's length, a
	// rule's length); 0 when the reason has none.
	Limit int `json:"limit,omitempty"`
}

// Say is the problem in the reader's language.
func (p FieldProblem) Say(lang string) string {
	key := "server.field." + p.Code
	switch p.Code {
	case FieldBelowMin:
		return srvtext.Text(lang, key, srvtext.Vars{"min": strconv.Itoa(p.Limit)})
	case FieldAboveMax:
		return srvtext.Text(lang, key, srvtext.Vars{"max": strconv.Itoa(p.Limit)})
	case FieldBadPin, FieldTooShort, FieldTooLong:
		return srvtext.Plural(lang, key, p.Limit, nil)
	}
	return srvtext.Text(lang, key, nil)
}

// SettingError is PutSettings' refusal of one value: the field, why, and the
// bound it broke. The admin handler answers it 400 with the problem said in
// the administrator's language.
type SettingError struct {
	FieldProblem
}

func (e *SettingError) Error() string {
	return "setting " + e.Key + ": " + e.Code
}

// ValueString is JavaScript's `String(v)` for the shapes JSON can produce —
// how the browser spells a value it compares (an option, a condition). ⚠ A
// number arrives as float64 through encoding/json, and `%v` would print a
// round million as `1e+06`; `strconv` with precision -1 gives the browser's
// own spelling back.
func ValueString(v any) string {
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
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// Answered says whether a value holds an answer at all: `false` and `0` do
// (an unticked box IS an answer), an empty or blank string and an empty list
// do not. Whether an answer is REQUIRED is a separate question.
func Answered(v any) bool {
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

// CheckFieldValue judges one answer against the Field that asked for it,
// by its type: `int` is a whole number inside min..max, `bool` is yes or no,
// `select` holds one of the options (each of them, for `multi`), and every
// other type (string, password, text, date) holds text. An unanswered value
// always fits here.
//
// ⚠ Lenient about SPELLING, strict about meaning: a whole number may arrive
// as `5` or `"5"` and a bool as `true` or `"true"`, because a plugin's own
// `default` reaches the browser and comes back exactly as the plugin wrote
// it — refusing an app's own default would refuse the app. What is refused
// is a value the field could never hold: `1.5` or `"abc"` for an int, a
// list for a text box, an option the screen did not offer.
func CheckFieldValue(f wire.Field, v any) *FieldProblem {
	if !Answered(v) {
		return nil
	}
	bad := func(code string, limit int) *FieldProblem {
		return &FieldProblem{Key: f.Key, Code: code, Limit: limit}
	}
	switch f.Type {
	case "int":
		n, ok := wholeNumber(v)
		if !ok {
			return bad(FieldBadInt, 0)
		}
		if f.Min != nil && n < int64(*f.Min) {
			return bad(FieldBelowMin, *f.Min)
		}
		if f.Max != nil && n > int64(*f.Max) {
			return bad(FieldAboveMax, *f.Max)
		}
	case "bool":
		if _, ok := yesNo(v); !ok {
			return bad(FieldBadBool, 0)
		}
	case "select":
		chosen, ok := choices(v, f.Multi)
		if !ok {
			return bad(FieldBadOption, 0)
		}
		if len(f.Options) > 0 {
			for _, c := range chosen {
				if !offered(f.Options, c) {
					return bad(FieldBadOption, 0)
				}
			}
		}
	default:
		if !scalar(v) {
			return bad(FieldBadText, 0)
		}
	}
	return nil
}

// CheckSetting is CheckFieldValue for a stored setting, which is always a
// string on the wire (the admin panel joins a multi select with commas),
// plus the one rule a setting has that a form field leaves to the
// conditions: a `required` field may not be saved empty. A field whose
// requirement depends on another (`show_when`, `required_when`) is not
// demanded here — the settings screen decides whether it is on screen.
func CheckSetting(f wire.Field, v string) *FieldProblem {
	if strings.TrimSpace(v) == "" {
		if f.Required && f.ShowWhen == nil && f.RequiredWhen == nil {
			return &FieldProblem{Key: f.Key, Code: FieldRequired}
		}
		return nil
	}
	var value any = v
	if f.Type == "select" && f.Multi {
		parts := strings.Split(v, ",")
		list := make([]any, 0, len(parts))
		for _, s := range parts {
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
		value = list
	}
	return CheckFieldValue(f, value)
}

func scalar(v any) bool {
	switch v.(type) {
	case string, bool, float64, float32, int, int64, json.Number:
		return true
	}
	return false
}

// wholeNumber reads an int field's answer: a JSON number with no fraction,
// or text that spells one.
func wholeNumber(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) || math.Abs(t) > 1<<53 {
			return 0, false
		}
		return int64(t), true
	case float32:
		return wholeNumber(float64(t))
	case int:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func yesNo(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.TrimSpace(t) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// choices reads a select's answer as the option values it names. A single
// select holds one value; a multi select a list — or one value, which the
// browser would have wrapped (surfaceValues multiSafe).
func choices(v any, multi bool) ([]string, bool) {
	switch t := v.(type) {
	case []any:
		if !multi {
			return nil, false
		}
		out := make([]string, 0, len(t))
		for _, x := range t {
			if !scalar(x) {
				return nil, false
			}
			out = append(out, ValueString(x))
		}
		return out, true
	case []string:
		if !multi {
			return nil, false
		}
		return t, true
	}
	if !scalar(v) {
		return nil, false
	}
	return []string{ValueString(v)}, true
}

func offered(opts []wire.FieldOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// ── pin-input ─────────────────────────────────────────────────────────

// The contract's bounds for a `pin-input`'s `length`, and what an absent one
// means.
const (
	pinMinLength     = 4
	pinMaxLength     = 8
	pinDefaultLength = 6
)

// PinLength is a `pin-input` node's length: its `length` prop clamped to
// 4..8, 6 when it names none. SanitizeSurface writes it back onto the node,
// so the browser draws the box this judge measures.
func PinLength(raw any) int {
	n, ok := wholeOrTruncated(raw)
	if !ok {
		return pinDefaultLength
	}
	if n < pinMinLength {
		return pinMinLength
	}
	if n > pinMaxLength {
		return pinMaxLength
	}
	return n
}

// CheckPin judges a `pin-input`'s answer: text of exactly the node's length
// with no space in it. An unanswered code fits (whether one is needed is the
// app's question).
func CheckPin(id string, length int, v any) *FieldProblem {
	if !Answered(v) {
		return nil
	}
	s, ok := v.(string)
	if !ok || strings.ContainsAny(s, " \t\r\n") || utf16Len(s) != length {
		return &FieldProblem{Key: id, Code: FieldBadPin, Limit: length}
	}
	return nil
}

// ── pdf-fields ────────────────────────────────────────────────────────

// PdfRule is what a `text` box on a PDF accepts (a `pdf-fields` box's
// `rule`): `any`, `number` or `email`, and a length in characters.
type PdfRule struct {
	Kind string `json:"kind"`
	Min  int    `json:"min,omitempty"`
	Max  int    `json:"max,omitempty"`
}

// NormalizePdfRule reads a box's `rule`; nil when it says nothing. An
// unknown kind reads as `any` — including the older `date` rule, which v3
// made a box TYPE: the box keeps taking text rather than refusing every
// signer of an older document. A bound that is not a positive whole number
// is no bound.
func NormalizePdfRule(raw any) *PdfRule {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	r := &PdfRule{Kind: "any"}
	if k, _ := m["kind"].(string); k == "number" || k == "email" {
		r.Kind = k
	}
	if n, ok := wholeOrTruncated(m["min"]); ok && n > 0 {
		r.Min = n
	}
	if n, ok := wholeOrTruncated(m["max"]); ok && n > 0 {
		r.Max = n
	}
	if r.Kind == "any" && r.Min == 0 && r.Max == 0 {
		return nil
	}
	return r
}

// props is the rule as a node prop / a fill entry carries it.
func (r *PdfRule) props() map[string]any {
	out := map[string]any{"kind": r.Kind}
	if r.Min > 0 {
		out["min"] = r.Min
	}
	if r.Max > 0 {
		out["max"] = r.Max
	}
	return out
}

var (
	pdfNumberRe = regexp.MustCompile(`^-?[0-9]+([.,][0-9]+)?$`)
	pdfEmailRe  = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]{2,}$`)
)

// pdfRuleProblem is the reason a text box's value breaks its rule, "" when
// it does not. Blank text breaks nothing — whether the box must be filled is
// the app's question. ⚠ Lengths count UTF-16 units, as the browser's input
// does, so the host never refuses what the box let the signer type.
func pdfRuleProblem(value string, r *PdfRule) (string, int) {
	v := strings.TrimSpace(value)
	if r == nil || v == "" {
		return "", 0
	}
	n := utf16Len(v)
	if r.Min > 0 && n < r.Min {
		return FieldTooShort, r.Min
	}
	if r.Max > 0 && n > r.Max {
		return FieldTooLong, r.Max
	}
	switch r.Kind {
	case "number":
		if !pdfNumberRe.MatchString(v) {
			return FieldBadNumber, 0
		}
	case "email":
		if !pdfEmailRe.MatchString(v) {
			return FieldBadEmail, 0
		}
	}
	return "", 0
}

// pdfBox is one declared box of a `pdf-fields` node, as the judge reads it.
type pdfBox struct {
	id, typ, assignee, label string
	rule                     *PdfRule
}

var pdfBoxTypes = map[string]bool{"signature": true, "initials": true, "date": true, "text": true, "checkbox": true}

func pdfBoxesOf(props map[string]any) map[string]pdfBox {
	out := map[string]pdfBox{}
	list, _ := props["fields"].([]any)
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := ""
		if m["id"] != nil {
			id = ValueString(m["id"])
		}
		typ, _ := m["type"].(string)
		if id == "" || !pdfBoxTypes[typ] {
			continue
		}
		if _, dup := out[id]; dup {
			continue
		}
		b := pdfBox{id: id, typ: typ}
		b.assignee, _ = m["assignee"].(string)
		b.label, _ = m["label"].(string)
		if typ == "text" {
			b.rule = NormalizePdfRule(m["rule"])
		}
		out[id] = b
	}
	return out
}

// JudgePdfFill judges what a `pdf-fields` node in `fill` mode sent back
// (`{fields: [{id, value, font?}]}`) and rewrites it to what the app may
// trust:
//
//   - only boxes on THIS screen that belong to THIS signer stay (a box with
//     no assignee is anybody's) — a box of another signer, or one the screen
//     never drew, is dropped, the same way a hidden field's value is: a
//     signer cannot fill in a box they were not shown;
//   - each box's value has its type's shape (a tick is a bool, a date is
//     YYYY-MM-DD, everything else text) and a text box obeys its rule;
//   - `label` and `rule` are the node's own, never the client's: the app
//     stamps and audits from this array alone, and a label the signer could
//     choose is an audit trail the signer could write.
//
// A value that breaks a rule refuses the event: the problems name the boxes
// (`<node id>.<box id>`), or the node itself when its answer is not the
// shape fill mode sends. A node that sent nothing (nil) stays nothing.
func JudgePdfFill(nodeID string, props map[string]any, v any) (clean any, problems []FieldProblem) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v, []FieldProblem{{Key: nodeID, Code: FieldBadShape}}
	}
	var list []any
	switch t := m["fields"].(type) {
	case nil:
	case []any:
		list = t
	default:
		return v, []FieldProblem{{Key: nodeID, Code: FieldBadShape}}
	}
	boxes := pdfBoxesOf(props)
	signer, _ := props["signer"].(string)
	seen := map[string]bool{}
	out := make([]any, 0, len(list))
	for _, raw := range list {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := e["id"].(string)
		b, known := boxes[id]
		if !known || seen[id] || (b.assignee != "" && b.assignee != signer) {
			continue
		}
		value := e["value"]
		if b.typ == "checkbox" {
			tick, isBool := value.(bool)
			if !isBool && Answered(value) {
				problems = append(problems, FieldProblem{Key: nodeID + "." + id, Code: FieldBadBool})
				continue
			}
			if !tick {
				continue
			}
		} else {
			if !Answered(value) {
				continue
			}
			s, isText := value.(string)
			if !isText {
				problems = append(problems, FieldProblem{Key: nodeID + "." + id, Code: FieldBadText})
				continue
			}
			if b.typ == "date" {
				if _, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err != nil {
					problems = append(problems, FieldProblem{Key: nodeID + "." + id, Code: FieldBadDate})
					continue
				}
			}
			if code, limit := pdfRuleProblem(s, b.rule); code != "" {
				problems = append(problems, FieldProblem{Key: nodeID + "." + id, Code: code, Limit: limit})
				continue
			}
		}
		seen[id] = true
		entry := map[string]any{"id": id, "value": value}
		if b.label != "" {
			entry["label"] = b.label
		}
		if font, ok := e["font"].(string); ok && font != "" {
			entry["font"] = font
		}
		if b.rule != nil {
			entry["rule"] = b.rule.props()
		}
		out = append(out, entry)
	}
	return map[string]any{"fields": out}, problems
}

// ── host normalisation of the props the browser measures with ───────

// normalizeValueProps writes back, on a node about to leave for a browser,
// the bounds the browser draws with and this file judges by: a `pin-input`'s
// length (always present, 4..8) and a `pdf-fields` text box's rule (kind and
// lengths as NormalizePdfRule reads them, or none). One reading, here, so the
// screen and the judge cannot measure different numbers.
func normalizeValueProps(n *wire.Node) {
	switch n.Type {
	case "pin-input":
		if n.Props == nil {
			n.Props = map[string]any{}
		}
		n.Props["length"] = PinLength(n.Props["length"])
	case "pdf-fields":
		list, _ := n.Props["fields"].([]any)
		for _, raw := range list {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, has := m["rule"]; !has {
				continue
			}
			if typ, _ := m["type"].(string); typ != "text" {
				delete(m, "rule")
				continue
			}
			if r := NormalizePdfRule(m["rule"]); r != nil {
				m["rule"] = r.props()
			} else {
				delete(m, "rule")
			}
		}
	}
}

// wholeOrTruncated is JavaScript's `Math.trunc(Number(v))` for a prop: a
// number or a numeric string, its fraction dropped.
func wholeOrTruncated(v any) (int, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case float32:
		f = float64(t)
	case int:
		return t, true
	case int64:
		f = float64(t)
	case json.Number:
		x, err := t.Float64()
		if err != nil {
			return 0, false
		}
		f = x
	case string:
		x, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		f = x
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1<<31 {
		return 0, false
	}
	return int(math.Trunc(f)), true
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }
