package pluginkit

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The `ui_call` export's dispatcher: a registered method answers its result,
// an unknown one and a failing one answer an error the interface's promise
// rejects with — never a trap, which would read as "the app crashed".
func TestDispatchUICall(t *testing.T) {
	old := registered
	t.Cleanup(func() { registered = old })
	Run(&Plugin{UI: map[string]UICallFunc{
		"sum": func(in *wire.UICallInput) (any, error) {
			var p struct{ A, B int }
			_ = json.Unmarshal(in.Params, &p)
			return map[string]int{"sum": p.A + p.B}, nil
		},
		"no": func(*wire.UICallInput) (any, error) {
			return nil, &UIError{Text: wire.Text{"en": "No", "tr": "Hayır"}}
		},
		"plain": func(*wire.UICallInput) (any, error) { return nil, errors.New("plain failure") },
	}})
	call := func(method string, params string) wire.UICallOutput {
		in, _ := json.Marshal(wire.UICallInput{ViewID: "v", Method: method, Params: json.RawMessage(params)})
		out, err := dispatchUICall(in)
		if err != nil {
			t.Fatalf("%s: dispatch error %v", method, err)
		}
		var o wire.UICallOutput
		if err := json.Unmarshal(out, &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := call("sum", `{"A":2,"B":3}`); o.Error != nil || o.Result.(map[string]any)["sum"] != float64(5) {
		t.Fatalf("sum: %+v", o)
	}
	if o := call("no", `null`); o.Error["tr"] != "Hayır" {
		t.Fatalf("no: %+v", o)
	}
	if o := call("plain", `null`); o.Error["en"] != "plain failure" {
		t.Fatalf("plain: %+v", o)
	}
	if o := call("missing", `null`); o.Error == nil {
		t.Fatalf("missing: an unknown method must refuse, got %+v", o)
	}
}
