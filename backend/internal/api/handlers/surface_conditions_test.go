package handlers

// The HOST half of `show_when` / `required_when`, measured against the same
// cases the browser half is measured against (`web/tests/lib/
// surfaceConditions.test.ts`).
//
// ⚠⚠ Why these matter more than the browser's. The contract tells a plugin
// author that the two rules are "re-checked by the host at submit", and the
// test kit (`pluginkit/plugintest`) states it as a fact: "the host DROPS it
// before the job runs". Until this file existed, the only thing dropping the
// value was the browser — so a plugin trusting the documented promise was
// trusting a client it does not control, and a crafted event could hand its
// job the answer to a question the person was never shown.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// screenOf is the answer a plugin gives when the host asks what the screen
// these values belong to looks like. It goes through JSON on purpose: on the
// wire a node's props are `map[string]any`, so the conditions arrive as nested
// maps and the decode in surfaceNodeFields is part of what is under test.
func screenOf(t *testing.T, fields ...wire.Field) *wire.Surface {
	t.Helper()
	s := wire.Surface{Nodes: []wire.Node{{
		Type:  "form",
		Props: map[string]any{"fields": fields},
	}}}
	b, err := json.Marshal(s)
	require.NoError(t, err)
	var out wire.Surface
	require.NoError(t, json.Unmarshal(b, &out))
	return &out
}

// drawnBy answers every redraw with the same screen and counts the asks.
func drawnBy(s *wire.Surface, calls *int) func(wire.ViewEventInput) (*wire.Surface, error) {
	return func(in wire.ViewEventInput) (*wire.Surface, error) {
		*calls++
		// The host must ask for a REDRAW, never replay the press.
		if in.Event != surfaceRedrawEvent {
			return nil, nil
		}
		return s, nil
	}
}

// outputField / nameField are the contract's own example (PLUGIN-KIT.md →
// "A form may not present a contradiction"): the name of the new file is asked
// for only when the output IS a new file, and is required exactly then.
func outputField() wire.Field {
	return wire.Field{Key: "output", Type: "select", Label: "Output", Options: []wire.FieldOption{
		{Value: "new", Label: "A new file"}, {Value: "version", Label: "A new version"},
	}}
}

func nameField() wire.Field {
	return wire.Field{Key: "new_name", Type: "string", Label: "Name of the new file",
		ShowWhen:     &wire.Condition{Key: "output", Equals: []string{"new"}},
		RequiredWhen: &wire.Condition{Key: "output", Equals: []string{"new"}},
	}
}

func valuesOf(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	v, ok := data["values"].(map[string]any)
	require.True(t, ok, "the event must still carry a values map")
	return v
}

// A value for a field the screen is NOT showing never reaches the plugin, so
// it cannot reach the job either.
func TestSurfaceGateDropsTheValueOfAHiddenField(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{
		"output":   "version",
		"new_name": "../../etc/passwd",
	}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing)
	assert.Equal(t, 1, calls, "the host asks the plugin once what the screen looks like")
	got := valuesOf(t, data)
	assert.NotContains(t, got, "new_name",
		"the field is hidden by its show_when: the key must be GONE, not blank — `{name: \"\"}` is an answer")
	assert.Equal(t, "version", got["output"], "the field that IS on screen keeps its answer")
}

// The other half of the same rule: a field the person can see keeps its value.
// A gate that dropped this would be worse than no gate.
func TestSurfaceGateKeepsTheValueOfAVisibleField(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{
		"output":   "new",
		"new_name": "rapor.pdf",
	}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing)
	got := valuesOf(t, data)
	assert.Equal(t, "rapor.pdf", got["new_name"], "the person answered a question they were asked")
	assert.Equal(t, "new", got["output"])
}

// An empty `required_when` field whose condition holds refuses the job: the
// caller answers 422 and never reaches the plugin, so nothing is queued.
func TestSurfaceGateRefusesAnEmptyRequiredWhenField(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{
		"output":   "new",
		"new_name": "   ",
	}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

	require.NoError(t, err)
	assert.Equal(t, []string{"new_name"}, missing,
		"the condition holds and the box is empty, so the job is refused and the field is named")
	assert.Equal(t, "   ", valuesOf(t, data)["new_name"],
		"a refused event is not also rewritten: the caller answers, the values are left as they came")
}

// The same field, empty, while its condition does NOT hold: not shown, not
// required, and its value dropped rather than demanded.
func TestSurfaceGateDoesNotDemandAFieldItIsHiding(t *testing.T) {
	calls := 0
	data := map[string]any{"values": map[string]any{"output": "version", "new_name": ""}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing, "a hidden field cannot be filled, so it cannot block the job")
	assert.NotContains(t, valuesOf(t, data), "new_name")
}

// ⚠ The cascade — the case the browser's fixed point exists for. `stamp` is
// shown only while `mode` is "sign"; `stamp_text` only while `stamp` is "on".
// With mode = "read", `stamp` is hidden, a hidden field answers NOTHING, so
// `stamp_text` is hidden too — and BOTH values go, however hard the request
// insists that `stamp` is "on".
func TestSurfaceGateHidesAFieldWhoseParentIsHidden(t *testing.T) {
	calls := 0
	fields := []wire.Field{
		{Key: "mode", Type: "select", Label: "Mode", Options: []wire.FieldOption{
			{Value: "sign", Label: "Sign"}, {Value: "read", Label: "Read"}}},
		{Key: "stamp", Type: "select", Label: "Stamp", Options: []wire.FieldOption{
			{Value: "on", Label: "On"}, {Value: "off", Label: "Off"}},
			ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"sign"}}},
		{Key: "stamp_text", Type: "string", Label: "Stamp text",
			ShowWhen: &wire.Condition{Key: "stamp", Equals: []string{"on"}}},
	}
	data := map[string]any{"values": map[string]any{
		"mode": "read", "stamp": "on", "stamp_text": "PAID",
	}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, fields...), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing)
	got := valuesOf(t, data)
	assert.NotContains(t, got, "stamp", "hidden by its own show_when")
	assert.NotContains(t, got, "stamp_text",
		"its controller is hidden, so nobody could have answered it — one pass would have left this on")
	assert.Equal(t, "read", got["mode"])
}

// The cascade the other way round: with the chain intact every value stays.
func TestSurfaceGateKeepsAWholeVisibleChain(t *testing.T) {
	calls := 0
	fields := []wire.Field{
		{Key: "mode", Type: "select", Label: "Mode", Options: []wire.FieldOption{
			{Value: "sign", Label: "Sign"}, {Value: "read", Label: "Read"}}},
		{Key: "stamp", Type: "select", Label: "Stamp", Options: []wire.FieldOption{
			{Value: "on", Label: "On"}, {Value: "off", Label: "Off"}},
			ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"sign"}}},
		{Key: "stamp_text", Type: "string", Label: "Stamp text",
			ShowWhen: &wire.Condition{Key: "stamp", Equals: []string{"on"}}},
	}
	data := map[string]any{"values": map[string]any{
		"mode": "sign", "stamp": "on", "stamp_text": "PAID",
	}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, fields...), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing)
	assert.Equal(t, map[string]any{"mode": "sign", "stamp": "on", "stamp_text": "PAID"}, valuesOf(t, data))
}

// A field that is show_when-hidden and unconditionally `required` is the
// contradiction the contract warns about. It must not block the job — a person
// cannot fill in a box that is not drawn.
func TestSurfaceGateDoesNotBlockOnAHiddenRequiredField(t *testing.T) {
	calls := 0
	hidden := wire.Field{Key: "new_name", Type: "string", Label: "Name", Required: true,
		ShowWhen: &wire.Condition{Key: "output", Equals: []string{"new"}}}
	data := map[string]any{"values": map[string]any{"output": "version"}}

	missing, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, outputField(), hidden), &calls))

	require.NoError(t, err)
	assert.Empty(t, missing, "a hidden required field cannot be filled — use required_when (contract)")
}

// Value SHAPES, as the browser reads them: a multi select holds a list and any
// member satisfies the condition; a bool reads as "true"/"false"; a number
// reads the way JavaScript spells it.
func TestSurfaceGateReadsValuesTheWayTheBrowserDoes(t *testing.T) {
	cases := []struct {
		name  string
		cond  wire.Condition
		value any
		shown bool
	}{
		{"a multi select member", wire.Condition{Key: "k", Equals: []string{"pdf"}}, []any{"docx", "pdf"}, true},
		{"a multi select without it", wire.Condition{Key: "k", Equals: []string{"pdf"}}, []any{"docx"}, false},
		{"a true bool", wire.Condition{Key: "k", Equals: []string{"true"}}, true, true},
		{"a false bool", wire.Condition{Key: "k", Equals: []string{"true"}}, false, false},
		{"a whole number", wire.Condition{Key: "k", Equals: []string{"3"}}, float64(3), true},
		{"a fractional number", wire.Condition{Key: "k", Equals: []string{"1.5"}}, 1.5, true},
		// An empty `equals` is "that field has been answered at all" — and
		// `false` and `0` ARE answers.
		{"answered at all: false", wire.Condition{Key: "k"}, false, true},
		{"answered at all: zero", wire.Condition{Key: "k"}, float64(0), true},
		{"answered at all: empty string", wire.Condition{Key: "k"}, "", false},
		{"answered at all: empty list", wire.Condition{Key: "k"}, []any{}, false},
		{"answered at all: missing", wire.Condition{Key: "k"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			cond := tc.cond
			fields := []wire.Field{
				{Key: "k", Type: "string", Label: "K"},
				{Key: "dependent", Type: "string", Label: "Dependent", ShowWhen: &cond},
			}
			vals := map[string]any{"dependent": "kept?"}
			if tc.value != nil {
				vals["k"] = tc.value
			}
			data := map[string]any{"values": vals}

			_, err := gateSurfaceValues("submit", nil, data, drawnBy(screenOf(t, fields...), &calls))

			require.NoError(t, err)
			if tc.shown {
				assert.Contains(t, valuesOf(t, data), "dependent")
			} else {
				assert.NotContains(t, valuesOf(t, data), "dependent")
			}
		})
	}
}

// Events that cannot come back carrying a job cost nothing: the plugin is not
// asked to redraw and the values are handed on untouched. `open` carries no
// answers yet and `change` is the debounced redraw itself.
func TestSurfaceGateLeavesNonSubmitEventsAlone(t *testing.T) {
	for _, event := range []string{"open", "change"} {
		t.Run(event, func(t *testing.T) {
			calls := 0
			data := map[string]any{"values": map[string]any{"output": "version", "new_name": "sneaky"}}

			missing, err := gateSurfaceValues(event, nil, data, drawnBy(screenOf(t, outputField(), nameField()), &calls))

			require.NoError(t, err)
			assert.Empty(t, missing)
			assert.Zero(t, calls, "no job can come out of this event, so it buys no extra plugin call")
			assert.Contains(t, valuesOf(t, data), "new_name")
		})
	}
}

// A press that carries no values, and a screen with no form on it, are both
// free: nothing to measure, nothing to ask.
func TestSurfaceGateSkipsWhatItCannotMeasure(t *testing.T) {
	t.Run("no values", func(t *testing.T) {
		calls := 0
		data := map[string]any{"row_id": "r1"}
		missing, err := gateSurfaceValues("action", nil, data, drawnBy(screenOf(t, outputField()), &calls))
		require.NoError(t, err)
		assert.Empty(t, missing)
		assert.Zero(t, calls)
	})
	t.Run("a screen with no fields", func(t *testing.T) {
		calls := 0
		data := map[string]any{"values": map[string]any{"note": "hello"}}
		blank := &wire.Surface{Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": "hi"}}}}
		missing, err := gateSurfaceValues("submit", nil, data, drawnBy(blank, &calls))
		require.NoError(t, err)
		assert.Empty(t, missing)
		assert.Equal(t, "hello", valuesOf(t, data)["note"], "nothing declared, nothing dropped")
	})
}

// A plugin that cannot draw its own screen is a plugin whose submit was going
// to fail anyway: the error travels, and the event does NOT go through
// unchecked. Failing open here would be the whole hole again, reachable by
// anyone who can make a plugin call time out.
func TestSurfaceGateCarriesADrawFailureRatherThanFailingOpen(t *testing.T) {
	data := map[string]any{"values": map[string]any{"output": "version", "new_name": "sneaky"}}
	boom := func(wire.ViewEventInput) (*wire.Surface, error) { return nil, assert.AnError }

	missing, err := gateSurfaceValues("submit", nil, data, boom)

	require.Error(t, err)
	assert.Empty(t, missing)
}

// Forms are collected across the WHOLE tree, layout nodes included — a form
// inside a `steps`/`row` is still a form on this screen, and its values arrive
// in the same flat map.
func TestSurfaceFormFieldsWalksNestedNodes(t *testing.T) {
	s := wire.Surface{Nodes: []wire.Node{{
		Type: "steps",
		Children: []wire.Node{{
			Type: "row",
			Children: []wire.Node{{
				Type:  "form",
				Props: map[string]any{"fields": []wire.Field{outputField(), nameField()}},
			}},
		}},
	}}}
	b, err := json.Marshal(s)
	require.NoError(t, err)
	var wired wire.Surface
	require.NoError(t, json.Unmarshal(b, &wired))

	got := surfaceFormFields(wired.Nodes)

	require.Len(t, got, 2)
	assert.Equal(t, "output", got[0].Key)
	require.NotNil(t, got[1].ShowWhen, "the condition must survive the trip through props")
	assert.Equal(t, "output", got[1].ShowWhen.Key)
}
