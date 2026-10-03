package plugintest_test

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// A wizard written the way filex-sign 0.1.0 wrote its office screen: the
// primary "Convert to PDF" button is handled on `action` alone. In filex a
// click on it arrives as `submit` and redraws the same screen - the button
// is dead. The kit has to show that, not paper over it.
func pressManifest() wire.Manifest {
	return wire.Manifest{
		ManifestVersion: wire.ProtocolVersion,
		Name:            "presser",
		Version:         "1.0.0",
		Label:           text("Presser", "Basıcı"),
		Languages:       []string{"en", "tr"},
		Permissions:     []string{"files:read", "files:write"},
		Actions: []wire.Action{{
			ID: "convert", Label: text("Convert", "Dönüştür"), Hidden: true,
			Applies: wire.Applies{Kind: "file", Ext: []string{"docx"}},
			Output:  wire.Output{Mode: "sibling", Name: "{stem}.pdf"},
		}},
		Views: []wire.View{
			{ID: "dead", Placement: "page", Label: text("Dead", "Ölü")},
			{ID: "alive", Placement: "page", Label: text("Alive", "Canlı")},
			{ID: "form", Placement: "modal", Label: text("Form", "Form")},
		},
	}
}

// officeScreen is the screen both wizards draw: one primary button and one
// secondary, and a state the next event must bring back.
func officeScreen() *wire.Surface {
	return &wire.Surface{
		Title: text("This has to be a PDF first", "Önce PDF olması gerekiyor"),
		State: map[string]any{"step": "office"},
		Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": text("Convert it", "Dönüştürün")}}},
		Actions: []wire.SurfaceAction{
			{ID: "convert", Label: text("Convert to PDF", "PDF'e dönüştür"), Primary: true},
			{ID: "later", Label: text("Later", "Sonra")},
			{ID: "locked", Label: text("Locked", "Kilitli"), Disabled: true},
		},
	}
}

type pressLog struct {
	events []wire.ViewEventInput
}

func (l *pressLog) record(in *wire.ViewEventInput) { l.events = append(l.events, *in) }

func (l *pressLog) last(t *testing.T) wire.ViewEventInput {
	t.Helper()
	if len(l.events) == 0 {
		t.Fatal("the view was never asked")
	}
	return l.events[len(l.events)-1]
}

func convertJob() *wire.Surface {
	return &wire.Surface{Job: &wire.JobRequest{ActionID: "convert"}}
}

func presser(log *pressLog) *pluginkit.Plugin {
	return &pluginkit.Plugin{
		Manifest: pressManifest(),
		Views: map[string]pluginkit.ViewFunc{
			// The 0.1.0 bug: listens for the EVENT, not the button.
			"dead": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				log.record(in)
				if in.Event == "action" && in.ActionID == "convert" {
					return convertJob(), nil
				}
				return officeScreen(), nil
			},
			// The fix: asks for the button on both events.
			"alive": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				log.record(in)
				if in.ActionID == "convert" && (in.Event == "action" || in.Event == "submit") {
					return convertJob(), nil
				}
				return officeScreen(), nil
			},
			"form": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				log.record(in)
				return &wire.Surface{
					Title: text("Form", "Form"),
					State: map[string]any{"n": 1},
					Nodes: []wire.Node{{Type: "form", Props: map[string]any{
						"fields": []wire.Field{
							{Key: "name", Type: "string", Label: "Name", Required: true},
							{Key: "mode", Type: "select", Label: "Mode", Default: "plain", Options: []wire.FieldOption{
								{Value: "plain", Label: "Plain"}, {Value: "fancy", Label: "Fancy"},
							}},
							{Key: "flourish", Type: "string", Label: "Flourish",
								ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"fancy"}}},
						},
						"values": map[string]any{"name": ""},
					}}},
					Actions: []wire.SurfaceAction{
						{ID: "next", Label: text("Next", "İleri"), Primary: true},
						{ID: "back", Label: text("Back", "Geri")},
					},
				}, nil
			},
		},
	}
}

func pressHarness(t *testing.T, log *pressLog) *plugintest.Harness {
	t.Helper()
	h := plugintest.New(nil)
	h.Host = plugintest.NewHost(pressManifest())
	h.Plugin = presser(log)
	h.Select(plugintest.File{Name: "contract.docx", Data: []byte("PK")})
	return h
}

// The primary button arrives as `submit`, with its id, carrying the screen's
// state back - and a handler that listens for `action` alone is caught: the
// press answers the same screen again instead of the job.
func TestPress_PrimaryButtonIsSentAsSubmit(t *testing.T) {
	log := &pressLog{}
	h := pressHarness(t, log)

	s, err := h.Open("dead")
	if err != nil {
		t.Fatal(err)
	}
	next, err := h.Press("dead", s, "convert", nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := log.last(t)
	if ev.Event != "submit" || ev.ActionID != "convert" {
		t.Fatalf("a primary button must arrive as submit/convert, got %s/%s", ev.Event, ev.ActionID)
	}
	if ev.State["step"] != "office" {
		t.Fatalf("the press must bring the screen's state back, got %v", ev.State)
	}
	if next.Job != nil {
		t.Fatal("a handler that listens for `action` only queued the job on a primary press - filex would have redrawn the screen")
	}

	// The same press on the fixed handler queues the conversion.
	s, err = h.Open("alive")
	if err != nil {
		t.Fatal(err)
	}
	next, err = h.Press("alive", s, "convert", nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Job == nil || next.Job.ActionID != "convert" {
		t.Fatalf("the fixed handler must queue convert on a primary press, got %+v", next)
	}
}

// A button that is not the primary one arrives as `action`.
func TestPress_OtherButtonsAreSentAsAction(t *testing.T) {
	log := &pressLog{}
	h := pressHarness(t, log)
	s, err := h.Open("alive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Press("alive", s, "later", nil); err != nil {
		t.Fatal(err)
	}
	if ev := log.last(t); ev.Event != "action" || ev.ActionID != "later" {
		t.Fatalf("a secondary button must arrive as action/later, got %s/%s", ev.Event, ev.ActionID)
	}
}

// What filex never sends is refused with words, before the plugin is asked.
func TestPress_RefusesWhatFilexNeverSends(t *testing.T) {
	log := &pressLog{}
	h := pressHarness(t, log)
	s, err := h.Open("alive")
	if err != nil {
		t.Fatal(err)
	}
	asked := len(log.events)

	if _, err := h.Press("alive", s, "submit", nil); err == nil || !strings.Contains(err.Error(), "no footer button") {
		t.Fatalf("a button the screen does not draw must be refused, got %v", err)
	}
	if _, err := h.Press("alive", s, "locked", nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("a disabled button must be refused, got %v", err)
	}
	if _, err := h.Press("alive", nil, "convert", nil); err == nil {
		t.Fatal("a press with no screen must be refused")
	}
	if len(log.events) != asked {
		t.Fatalf("a refused press reached the plugin (%d events after the opening, want 0)", len(log.events)-asked)
	}

	// The primary button with a visible required field empty: the browser
	// stops it and points at the field.
	f, err := h.Open("form")
	if err != nil {
		t.Fatal(err)
	}
	asked = len(log.events)
	if _, err := h.Press("form", f, "next", nil); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("a primary press with the required name empty must be refused naming it, got %v", err)
	}
	if len(log.events) != asked {
		t.Fatal("a press filex would not send reached the plugin")
	}
	// Back is not the primary button: the browser sends it as it is.
	if _, err := h.Press("form", f, "back", nil); err != nil {
		t.Fatalf("a secondary press is sent whatever the form holds: %v", err)
	}
}

// A job's answer may send its person to what it made (filex #78); the kit
// says when filex would drop the request instead.
func TestInspectJobResult_SaysWhatFilexWouldDrop(t *testing.T) {
	m := pressManifest()
	outs := []wire.OutputRef{{Ref: "out:1", Name: "contract.pdf"}}
	good := []*wire.ActionRunOutput{
		{OK: true, Outputs: outs, Surface: &wire.Surface{Open: &wire.OpenRequest{View: "alive"}}},
		{OK: true, Outputs: outs, Surface: &wire.Surface{Open: &wire.OpenRequest{Path: "out:1", Action: "convert"}}},
		{OK: true, Outputs: outs},
	}
	for i, out := range good {
		if errs := plugintest.InspectJobResult(m, out).Errors(); len(errs) > 0 {
			t.Fatalf("good answer %d was refused:\n%s", i, join(errs))
		}
	}

	bad := map[string]*wire.ActionRunOutput{
		"is not the ref of one of this job's outputs": {OK: true, Outputs: outs,
			Surface: &wire.Surface{Open: &wire.OpenRequest{Path: "docs://other.pdf", View: "alive"}}},
		"produced no file to open": {OK: true,
			Surface: &wire.Surface{Open: &wire.OpenRequest{View: "alive"}}},
		"which the manifest does not declare": {OK: true, Outputs: outs,
			Surface: &wire.Surface{Open: &wire.OpenRequest{View: "elsewhere"}}},
		"both an action and a view": {OK: true, Outputs: outs,
			Surface: &wire.Surface{Open: &wire.OpenRequest{Action: "convert", View: "alive"}}},
	}
	for why, out := range bad {
		find(t, plugintest.InspectJobResult(m, out), plugintest.SevError, why)
	}

	// The rest of a job's screen is never drawn: said, not failed.
	drawn := &wire.ActionRunOutput{OK: true, Outputs: outs, Surface: &wire.Surface{Done: true, Toast: text("Done", "Bitti")}}
	find(t, plugintest.InspectJobResult(m, drawn), plugintest.SevWarn, "acts only on `open`")
}

// The values are what the person's screen holds: the form's own values and
// defaults, the test's entries over them, and a hidden field's value dropped.
func TestPress_SendsTheValuesTheScreenHolds(t *testing.T) {
	log := &pressLog{}
	h := pressHarness(t, log)
	f, err := h.Open("form")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Press("form", f, "next", map[string]any{"name": "Ada", "flourish": "hidden one"}); err != nil {
		t.Fatal(err)
	}
	ev := log.last(t)
	vals, _ := ev.Data["values"].(map[string]any)
	if vals["name"] != "Ada" {
		t.Fatalf("the entry must be sent, got %v", vals)
	}
	if vals["mode"] != "plain" {
		t.Fatalf("a field the person left alone sends its default, got %v", vals)
	}
	if _, ok := vals["flourish"]; ok {
		t.Fatalf("a field show_when hides must not send its value, got %v", vals)
	}
	if ev.State["n"] != 1 {
		t.Fatalf("the press must bring the screen's state back, got %v", ev.State)
	}
}
