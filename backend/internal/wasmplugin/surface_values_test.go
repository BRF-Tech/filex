package wasmplugin

// The host's judge of an app's answers (surface_values.go): what a form
// field, a PIN box, a PDF box and a manifest setting may hold.
//
// ⚠⚠ Why these exist (0.54 audit B1). The browser drew every rule — a number
// box with min/max, buttons for the options, a code box of the declared
// length, a text box that drops letters from a number — and nothing else
// checked them: the view gate dropped hidden fields and refused empty
// required ones, and PutSettings stored every string it was sent. A crafted
// event, an outside signer on a public page, or an API key writing settings
// handed the app values its own declarations ruled out.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func intp(n int) *int { return &n }

func TestCheckFieldValue_ByTheFieldsType(t *testing.T) {
	count := wire.Field{Key: "copies", Type: "int", Min: intp(1), Max: intp(10)}
	format := wire.Field{Key: "format", Type: "select", Options: []wire.FieldOption{{Value: "pdf"}, {Value: "docx"}}}
	formats := wire.Field{Key: "formats", Type: "select", Multi: true, Options: []wire.FieldOption{{Value: "pdf"}, {Value: "docx"}}}
	stamp := wire.Field{Key: "stamp", Type: "bool"}
	note := wire.Field{Key: "note", Type: "string"}

	cases := []struct {
		name  string
		field wire.Field
		value any
		code  string // "" = fits
		limit int
	}{
		{"a whole number inside the bounds", count, float64(3), "", 0},
		{"the number as the app's own default spelt it", count, "4", "", 0},
		{"a fraction", count, 1.5, FieldBadInt, 0},
		{"words in a number box", count, "lots", FieldBadInt, 0},
		{"below min", count, float64(0), FieldBelowMin, 1},
		{"above max", count, float64(11), FieldAboveMax, 10},
		{"an offered option", format, "pdf", "", 0},
		{"an option the screen never offered", format, "exe", FieldBadOption, 0},
		{"a list for a one-of choice", format, []any{"pdf"}, FieldBadOption, 0},
		{"several offered options", formats, []any{"pdf", "docx"}, "", 0},
		{"one of several not offered", formats, []any{"pdf", "exe"}, FieldBadOption, 0},
		{"a single value for a several-of choice", formats, "docx", "", 0},
		{"yes", stamp, true, "", 0},
		{"no, as text", stamp, "false", "", 0},
		{"a bool that is neither", stamp, "maybe", FieldBadBool, 0},
		{"text", note, "hello", "", 0},
		{"a list where text was asked", note, []any{"a", "b"}, FieldBadText, 0},
		{"an object where text was asked", note, map[string]any{"x": 1}, FieldBadText, 0},
		// Unanswered always fits: whether it must be answered is the
		// conditions' question, asked separately.
		{"nothing", count, nil, "", 0},
		{"blank", format, "  ", "", 0},
		{"an empty several-of", formats, []any{}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := CheckFieldValue(tc.field, tc.value)
			if tc.code == "" {
				assert.Nil(t, p)
				return
			}
			require.NotNil(t, p)
			assert.Equal(t, tc.field.Key, p.Key)
			assert.Equal(t, tc.code, p.Code)
			assert.Equal(t, tc.limit, p.Limit)
		})
	}
}

// The words are the server's, in the reader's language, with the bound in
// them.
func TestFieldProblem_IsSaidInTheReadersLanguage(t *testing.T) {
	below := FieldProblem{Key: "copies", Code: FieldBelowMin, Limit: 3}
	assert.Equal(t, "Enter 3 or more.", below.Say("en"))
	assert.Equal(t, "3 ya da daha büyük bir sayı girin.", below.Say("tr"))

	long := FieldProblem{Key: "doc.tax", Code: FieldTooLong, Limit: 1}
	assert.Equal(t, "Enter at most 1 character.", long.Say("en"), "the counted forms are the catalogue's")
	pin := FieldProblem{Key: "code", Code: FieldBadPin, Limit: 6}
	assert.Equal(t, "Kodun 6 karakterinin hepsini girin.", pin.Say("tr"))
	for _, code := range []string{FieldBadInt, FieldBadBool, FieldBadText, FieldBadOption, FieldRequired,
		FieldBadDate, FieldBadNumber, FieldBadEmail, FieldBadShape} {
		said := FieldProblem{Code: code}.Say("en")
		assert.NotContains(t, said, "server.field.", "%s has words in the catalogue", code)
		assert.NotEmpty(t, said)
	}
}

func TestCheckPin_IsTheNodesLength(t *testing.T) {
	assert.Nil(t, CheckPin("code", 6, "123456"))
	assert.Nil(t, CheckPin("code", 6, ""), "an unanswered code is the app's question")
	for _, bad := range []any{"12345", "1234567", "123 456", float64(123456), []any{"123456"}} {
		p := CheckPin("code", 6, bad)
		require.NotNil(t, p, "%v", bad)
		assert.Equal(t, FieldBadPin, p.Code)
		assert.Equal(t, 6, p.Limit)
	}
}

func TestPinLength_IsTheContractsBounds(t *testing.T) {
	assert.Equal(t, 6, PinLength(nil))
	assert.Equal(t, 6, PinLength("nope"))
	assert.Equal(t, 4, PinLength(float64(2)))
	assert.Equal(t, 8, PinLength(float64(12)))
	assert.Equal(t, 5, PinLength(5.7))
	assert.Equal(t, 7, PinLength("7"))
}

// The browser draws what the host sends: every pin-input leaves with its
// length written (4..8), and a pdf-fields text box with its rule read the
// way the judge reads it. Red before #212: `length` stayed 12 / absent and
// the rule stayed as the app wrote it.
func TestSanitizeSurface_WritesTheBoundsTheBrowserDrawsWith(t *testing.T) {
	var s wire.Surface
	require.NoError(t, json.Unmarshal([]byte(`{"nodes":[
		{"id":"a","type":"pin-input","props":{"length":12}},
		{"id":"b","type":"pin-input"},
		{"id":"doc","type":"pdf-fields","props":{"mode":"fill","fields":[
			{"id":"t1","type":"text","rule":{"kind":"date","max":"10"}},
			{"id":"t2","type":"text","rule":{"kind":"any"}},
			{"id":"s1","type":"signature","rule":{"kind":"number"}}
		]}}
	]}`), &s))

	SanitizeSurface(&s)

	assert.Equal(t, 8, s.Nodes[0].Props["length"])
	assert.Equal(t, 6, s.Nodes[1].Props["length"])
	fields := s.Nodes[2].Props["fields"].([]any)
	assert.Equal(t, map[string]any{"kind": "any", "max": 10}, fields[0].(map[string]any)["rule"],
		"the old `date` rule reads as text of at most 10")
	assert.NotContains(t, fields[1].(map[string]any), "rule", "a rule that says nothing is no rule")
	assert.NotContains(t, fields[2].(map[string]any), "rule", "only a text box carries a rule")
}

// ONE list of cases for the host's judge and the browser's hint
// (web/tests/lib/pdfFieldRules.test.ts reads the same file).
func TestPdfRule_TheHostReadsTheSameCasesAsTheHint(t *testing.T) {
	raw, err := os.ReadFile("testdata/pdf_rule_cases.json")
	require.NoError(t, err)
	var file struct {
		Cases []struct {
			Value string `json:"value"`
			Rule  any    `json:"rule"`
			Want  string `json:"want"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Greater(t, len(file.Cases), 10)
	hostCode := map[string]string{"": "", "min": FieldTooShort, "max": FieldTooLong, "number": FieldBadNumber, "email": FieldBadEmail}
	for _, c := range file.Cases {
		got, _ := pdfRuleProblem(c.Value, NormalizePdfRule(c.Rule))
		want, ok := hostCode[c.Want]
		require.True(t, ok, "unknown want %q", c.Want)
		assert.Equal(t, want, got, "%q under %v", c.Value, c.Rule)
	}
}

// fillNode is a pdf-fields node in fill mode for signer `ayse`: her text box
// (a tax number, digits, 10 long), her date and tick, a box anybody may fill,
// and a box of another signer.
func fillNode() map[string]any {
	var props map[string]any
	_ = json.Unmarshal([]byte(`{"mode":"fill","signer":"ayse","fields":[
		{"id":"tax","type":"text","assignee":"ayse","label":"Vergi no","rule":{"kind":"number","min":10,"max":10}},
		{"id":"day","type":"date","assignee":"ayse"},
		{"id":"ok","type":"checkbox","assignee":"ayse"},
		{"id":"note","type":"text"},
		{"id":"boss","type":"signature","assignee":"mehmet"}
	]}`), &props)
	return props
}

// The anonymous signer's answer, as the app may trust it: only her boxes,
// each with the NODE's label and rule. Red before #212: the app received
// mehmet's box, the made-up box and the label and rule the browser sent.
func TestJudgePdfFill_KeepsTheSignersOwnBoxesWithTheNodesWords(t *testing.T) {
	sent := map[string]any{"fields": []any{
		map[string]any{"id": "tax", "value": "1234567890", "label": "Anything I like", "rule": map[string]any{"kind": "any"}},
		map[string]any{"id": "day", "value": "2026-10-08"},
		map[string]any{"id": "ok", "value": true},
		map[string]any{"id": "note", "value": "hi", "font": "caveat"},
		map[string]any{"id": "boss", "value": "aGVsbG8="},
		map[string]any{"id": "ghost", "value": "not on this screen"},
	}}

	clean, problems := JudgePdfFill("doc", fillNode(), sent)

	assert.Empty(t, problems)
	got := clean.(map[string]any)["fields"].([]any)
	ids := []string{}
	for _, e := range got {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	assert.Equal(t, []string{"tax", "day", "ok", "note"}, ids, "another signer's box and a box the screen never drew are dropped")
	tax := got[0].(map[string]any)
	assert.Equal(t, "Vergi no", tax["label"], "the audit trail says the node's name for the box")
	assert.Equal(t, map[string]any{"kind": "number", "min": 10, "max": 10}, tax["rule"], "the stamper formats by the node's rule")
	assert.Equal(t, "caveat", got[3].(map[string]any)["font"], "the face the signer chose rides along")
}

func TestJudgePdfFill_RefusesWhatABoxCannotHold(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
		code  string
		limit int
	}{
		{"letters in a number box", map[string]any{"id": "tax", "value": "12345abcde"}, FieldBadNumber, 0},
		{"too short for its rule", map[string]any{"id": "tax", "value": "123"}, FieldTooShort, 10},
		{"too long for its rule", map[string]any{"id": "tax", "value": "123456789012"}, FieldTooLong, 10},
		{"a date that is not one", map[string]any{"id": "day", "value": "08/10/2026"}, FieldBadDate, 0},
		{"a tick that is text", map[string]any{"id": "ok", "value": "yes"}, FieldBadBool, 0},
		{"a picture that is not text", map[string]any{"id": "note", "value": map[string]any{"x": 1}}, FieldBadText, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := JudgePdfFill("doc", fillNode(), map[string]any{"fields": []any{tc.entry}})
			require.Len(t, problems, 1)
			assert.Equal(t, "doc."+tc.entry["id"].(string), problems[0].Key)
			assert.Equal(t, tc.code, problems[0].Code)
			assert.Equal(t, tc.limit, problems[0].Limit)
		})
	}
	_, problems := JudgePdfFill("doc", fillNode(), "not the fill shape")
	require.Len(t, problems, 1)
	assert.Equal(t, FieldProblem{Key: "doc", Code: FieldBadShape}, problems[0])
}

// One rule for the menu and the run: offered on a read-only storage only
// when it writes nothing there, or when it opens a screen that asks where
// the result goes.
func TestOffersOnReadOnly(t *testing.T) {
	assert.True(t, OffersOnReadOnly(&wire.Action{Output: wire.Output{Mode: "none"}}, wire.Applies{}))
	assert.False(t, OffersOnReadOnly(&wire.Action{Output: wire.Output{Mode: "sibling"}}, wire.Applies{}))
	assert.False(t, OffersOnReadOnly(&wire.Action{Output: wire.Output{Mode: "version"}}, wire.Applies{}))
	assert.False(t, OffersOnReadOnly(&wire.Action{Output: wire.Output{Mode: "none"}}, wire.Applies{Writable: true}),
		"a flow that ends in a write")
	assert.True(t, OffersOnReadOnly(&wire.Action{View: "options", Output: wire.Output{Mode: "sibling", Elsewhere: true}}, wire.Applies{}),
		"its screen asks where the result goes")
	assert.False(t, OffersOnReadOnly(&wire.Action{Output: wire.Output{Mode: "sibling", Elsewhere: true}}, wire.Applies{}),
		"elsewhere with no screen to ask where: the run refuses it, so the menu does not offer it (B9)")
}

// PutSettings judges what an administrator (or an API key) writes against
// the manifest, and refuses the whole save on the first value that does not
// fit — nothing is stored. Red before #212: every string was stored.
func TestPutSettings_RefusesAValueItsFieldCannotHold(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["settings"] = []map[string]any{
			{"key": "copies", "type": "int", "label": "Copies", "min": 1, "max": 5},
			{"key": "format", "type": "select", "label": "Format", "options": []map[string]any{{"value": "pdf", "label": "PDF"}, {"value": "docx", "label": "DOCX"}}},
			{"key": "formats", "type": "select", "multi": true, "label": "Formats", "options": []map[string]any{{"value": "pdf", "label": "PDF"}, {"value": "docx", "label": "DOCX"}}},
			{"key": "stamp", "type": "bool", "label": "Stamp"},
			{"key": "tsa_url", "type": "string", "label": "TSA", "required": true},
		}
	}))
	require.NoError(t, err)
	good := map[string]string{"copies": "2", "format": "pdf", "formats": "pdf,docx", "stamp": "true", "tsa_url": "https://tsa"}
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, good))

	cases := []struct {
		key, value, code string
	}{
		{"copies", "many", FieldBadInt},
		{"copies", "9", FieldAboveMax},
		{"copies", "0", FieldBelowMin},
		{"format", "exe", FieldBadOption},
		{"formats", "pdf,exe", FieldBadOption},
		{"stamp", "maybe", FieldBadBool},
		{"tsa_url", "", FieldRequired},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			next := map[string]string{"copies": "3", tc.key: tc.value}
			err := h.reg.PutSettings(context.Background(), st.ID, next)
			var se *SettingError
			require.True(t, errors.As(err, &se), "want a SettingError, got %v", err)
			assert.Equal(t, tc.key, se.Key)
			assert.Equal(t, tc.code, se.Code)
			vals, _, err := h.reg.Settings(context.Background(), st.ID)
			require.NoError(t, err)
			assert.Equal(t, "2", vals["copies"], "a refused save stores nothing, not even its valid values")
		})
	}
	// Leaving a field out keeps what it held; an empty optional one is fine.
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, map[string]string{"copies": "", "stamp": ""}))
}
