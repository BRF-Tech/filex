package handlers

// The gate judges ANSWERS too (0.54 audit B1, #212): a crafted event, or the
// anonymous signer on a public page, may not hand an app a value the screen's
// own declarations rule out — a type, an option, min/max, a PIN's length, a
// PDF text box's rule. Before #212 the gate only dropped hidden fields and
// refused empty required ones; everything else was the browser's word.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// screenJSON is a screen as a plugin answers it: through JSON, sanitised by
// the host exactly as ViewEvent / PageEvent do before the gate reads it.
func screenJSON(t *testing.T, raw string) *wire.Surface {
	t.Helper()
	var s wire.Surface
	require.NoError(t, json.Unmarshal([]byte(raw), &s))
	wasmplugin.SanitizeSurface(&s)
	return &s
}

func copiesField() wire.Field {
	one, ten := 1, 10
	return wire.Field{Key: "copies", Type: "int", Label: "Copies", Min: &one, Max: &ten}
}

func TestSurfaceGate_RefusesAnOptionTheScreenNeverOffered(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{"output": "../../elsewhere", "new_name": "x.pdf"}}

	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

	require.NoError(t, err)
	assert.True(t, verdict.refused(), "red before #212: the gate handed the made-up option to the app")
	require.Len(t, verdict.Invalid, 1)
	assert.Equal(t, wasmplugin.FieldProblem{Key: "output", Code: wasmplugin.FieldBadOption}, verdict.Invalid[0])
	assert.Equal(t, "../../elsewhere", valuesOf(t, data)["output"], "a refused event is not rewritten")
}

func TestSurfaceGate_RefusesANumberOutsideItsBoundsAndAWrongType(t *testing.T) {
	for _, tc := range []struct {
		value any
		code  string
	}{
		{float64(0), wasmplugin.FieldBelowMin},
		{float64(500), wasmplugin.FieldAboveMax},
		{"lots", wasmplugin.FieldBadInt},
		{[]any{float64(2)}, wasmplugin.FieldBadInt},
	} {
		calls := 0
		data := map[string]any{"values": map[string]any{"copies": tc.value}}
		verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, copiesField()), &calls))
		require.NoError(t, err)
		require.Len(t, verdict.Invalid, 1, "%v", tc.value)
		assert.Equal(t, tc.code, verdict.Invalid[0].Code, "%v", tc.value)
	}
	calls := 0
	data := map[string]any{"values": map[string]any{"copies": float64(3)}}
	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, copiesField()), &calls))
	require.NoError(t, err)
	assert.False(t, verdict.refused(), "a number inside the bounds goes")
}

// A value for a hidden field is dropped BEFORE it is judged: a person cannot
// answer a question they were not shown, so its value cannot refuse the job
// either.
func TestSurfaceGate_DoesNotJudgeAHiddenFieldsValue(t *testing.T) {
	calls := 0
	hidden := copiesField()
	hidden.ShowWhen = &wire.Condition{Key: "output", Equals: []string{"new"}}
	data := map[string]any{"values": map[string]any{"output": "version", "copies": "nonsense"}}

	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), hidden), &calls))

	require.NoError(t, err)
	assert.False(t, verdict.refused())
	assert.NotContains(t, valuesOf(t, data), "copies")
}

func TestSurfaceGate_RefusesAPinOfAnotherLength(t *testing.T) {
	screen := screenJSON(t, `{"nodes":[{"id":"code","type":"pin-input","props":{"length":6}}]}`)
	calls := 0
	data := map[string]any{"values": map[string]any{"code": "1234"}}

	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screen, &calls))

	require.NoError(t, err)
	require.Len(t, verdict.Invalid, 1, "red before #212: a pin-input's value was never looked at")
	assert.Equal(t, wasmplugin.FieldProblem{Key: "code", Code: wasmplugin.FieldBadPin, Limit: 6}, verdict.Invalid[0])

	data = map[string]any{"values": map[string]any{"code": "123456"}}
	verdict, err = gateSurfaceValues("submit", nil, data, drawnBy(screen, &calls))
	require.NoError(t, err)
	assert.False(t, verdict.refused())
}

// The public signing page's case: the anonymous signer fills HER boxes. A
// value that breaks a box's rule refuses the event; a box of another signer
// is dropped before the app sees the answer, and the label and rule the app
// receives are the node's own.
const signerScreen = `{"nodes":[{"id":"doc","type":"pdf-fields","props":{"mode":"fill","signer":"ayse","fields":[
	{"id":"tax","type":"text","assignee":"ayse","label":"Vergi no","rule":{"kind":"number","min":10,"max":10}},
	{"id":"boss","type":"signature","assignee":"mehmet"}
]}}],"actions":[{"id":"sign","label":{"en":"Sign"},"primary":true}]}`

func TestSurfaceGate_PdfFill_ARuleBreakIsRefused(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{"doc": map[string]any{"fields": []any{
		map[string]any{"id": "tax", "value": "not a number"},
	}}}}

	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenJSON(t, signerScreen), &calls))

	require.NoError(t, err)
	require.Len(t, verdict.Invalid, 1, "red before #212: pdfFieldRules ran only in the browser")
	assert.Equal(t, "doc.tax", verdict.Invalid[0].Key)
}

func TestSurfaceGate_PdfFill_AnotherSignersBoxNeverReachesTheApp(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{"doc": map[string]any{"fields": []any{
		map[string]any{"id": "tax", "value": "1234567890", "label": "forged", "rule": map[string]any{"kind": "any"}},
		map[string]any{"id": "boss", "value": "aGVsbG8="},
	}}}}

	verdict, err := gateSurfaceValues("submit", nil, data, drawnBy(screenJSON(t, signerScreen), &calls))

	require.NoError(t, err)
	assert.False(t, verdict.refused())
	fill := valuesOf(t, data)["doc"].(map[string]any)["fields"].([]any)
	require.Len(t, fill, 1, "mehmet's signature box is not ayse's to fill")
	tax := fill[0].(map[string]any)
	assert.Equal(t, "tax", tax["id"])
	assert.Equal(t, "Vergi no", tax["label"])
	assert.Equal(t, map[string]any{"kind": "number", "min": 10, "max": 10}, tax["rule"])
}

// While a person edits, the answer to every `change` carries the host's
// verdict in the reader's language; the app's own words for a field win.
// The answer to a submit (usually the next step) is not marked.
func TestMarkSurfaceProblems_OnAChangeOnly(t *testing.T) {
	data := map[string]any{"values": map[string]any{"copies": float64(50), "output": "exe"}}

	s := screenOf(t, copiesField(), outputField())
	markSurfaceProblems("change", s, data, "tr")
	require.NotNil(t, s.Errors)
	assert.Equal(t, "10 ya da daha küçük bir sayı girin.", s.Errors["copies"].Get("tr"))
	assert.Equal(t, "Sunulan seçeneklerden birini seçin.", s.Errors["output"].Get("tr"))

	own := screenOf(t, copiesField())
	own.Errors = map[string]wire.Text{"copies": {"en": "The app's own words"}}
	markSurfaceProblems("change", own, data, "en")
	assert.Equal(t, "The app's own words", own.Errors["copies"].Get("en"))

	next := screenOf(t, copiesField(), outputField())
	markSurfaceProblems("submit", next, data, "en")
	assert.Empty(t, next.Errors, "a submit's answer is the next step; the last step's values are not its business")
}

func TestWriteSurfaceRefused_SaysItInTheReadersLanguage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSurfaceRefused(rec, "tr", surfaceVerdict{Invalid: []wasmplugin.FieldProblem{
		{Key: "copies", Code: wasmplugin.FieldAboveMax, Limit: 10},
	}})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var body struct {
		Error   string            `json:"error"`
		Message string            `json:"message"`
		Fields  []string          `json:"fields"`
		Invalid map[string]string `json:"invalid"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid", body.Error)
	assert.Equal(t, "Bazı yanıtlar kabul edilemiyor. İşaretli alanları düzeltip yeniden deneyin.", body.Message)
	assert.Equal(t, []string{"copies"}, body.Fields)
	assert.Equal(t, "10 ya da daha küçük bir sayı girin.", body.Invalid["copies"])

	rec = httptest.NewRecorder()
	writeSurfaceRefused(rec, "en", surfaceVerdict{Missing: []string{"new_name"}})
	assert.Contains(t, rec.Body.String(), `"error":"required"`)
	assert.Contains(t, rec.Body.String(), "Fill in the required fields.")
	assert.NotContains(t, rec.Body.String(), "these fields are required and empty", "the English-only refusal is gone")
}
