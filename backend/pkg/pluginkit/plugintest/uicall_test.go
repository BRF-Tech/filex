package plugintest_test

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The kit calls an app's `ui_call` handlers the way filex does: on an
// interface view only, in screen mode, with the selection as refs — and says
// so when an answer would not cross the wire.
func TestUICall_TheKitRunsAnInterfaceCall(t *testing.T) {
	m := wire.Manifest{
		ManifestVersion: wire.ProtocolVersion, Name: "words", Version: "1.0.0",
		Label: wire.Text{"en": "Words"}, Permissions: []string{"files:read", "files:write"},
		UI: &wire.UISpec{},
		Views: []wire.View{{ID: "editor", Placement: "viewer", UI: "index.html", Label: wire.Text{"en": "Words"}},
			{ID: "form", Placement: "modal", Label: wire.Text{"en": "Form"}}},
	}
	var h *plugintest.Harness
	h = plugintest.NewFor(m, func(host *plugintest.Host) *pluginkit.Plugin {
		return &pluginkit.Plugin{Manifest: m, UI: map[string]pluginkit.UICallFunc{
			"count": func(in *wire.UICallInput) (any, error) {
				b, err := host.ReadInput(in.Context.Inputs[0].Ref)
				if err != nil {
					return nil, err
				}
				return map[string]int{"words": len(strings.Fields(string(b)))}, nil
			},
			"save": func(in *wire.UICallInput) (any, error) {
				_, err := host.WriteOutput("x.txt", []byte("x"))
				return nil, err
			},
			"chan": func(in *wire.UICallInput) (any, error) { return make(chan int), nil },
		}}
	})
	out, err := h.UICall("editor", "count", nil, plugintest.File{Name: "a.txt", Data: []byte("one two three")})
	if err != nil || out.Error != nil {
		t.Fatalf("count: %v %+v", err, out)
	}
	if out.Result.(map[string]any)["words"] != float64(3) {
		t.Fatalf("count: %+v", out.Result)
	}
	if out, _ := h.UICall("editor", "save", nil); out == nil || out.Error == nil {
		t.Fatalf("a screen-mode call must not write a file: %+v", out)
	}
	if _, err := h.UICall("editor", "chan", nil); err == nil {
		t.Fatal("an answer that is not JSON must be the author's error, not the browser's")
	}
	if _, err := h.UICall("form", "count", nil); err == nil {
		t.Fatal("a surface view is not an interface")
	}
}
