package plugintest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// at is a fixed clock, so a window is a window and not "whatever hour the
// suite happens to run in".
var at = time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)

// scheduled is the demo plugin plus a wake-up: the shape a signing app takes
// when it closes its own lapsed requests.
func scheduled(tick pluginkit.TickFunc) *pluginkit.Plugin {
	m := manifest()
	m.Permissions = append(m.Permissions, "schedule")
	m.Actions = append(m.Actions, wire.Action{
		ID: "expire", Label: text("Expire", "Süresi doldu"),
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"}, Hidden: true,
	})
	return &pluginkit.Plugin{Manifest: m, Tick: tick}
}

func TestWake_RunsTheTickWithTheHoursWindow(t *testing.T) {
	var got *wire.TickInput
	h := plugintest.New(scheduled(func(in *wire.TickInput) (*wire.TickOutput, error) {
		got = in
		return &wire.TickOutput{Items: []wire.ScheduleItem{{
			Key: "expire:7f3a", DueAt: in.Now.Add(20 * time.Minute), ActionID: "expire",
			Paths: []string{"docs://reports/nda.pdf"},
		}}}, nil
	}))
	h.Host.SetSetting("deadline", "tonight")

	out, err := h.Wake(at.Add(37 * time.Minute))
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got == nil {
		t.Fatal("the tick was not called")
	}
	if !got.WindowEnd.Equal(at.Add(time.Hour)) {
		t.Fatalf("window ends at %s, want the next hour boundary %s", got.WindowEnd, at.Add(time.Hour))
	}
	if !got.WindowStart.Equal(at.Add(37 * time.Minute)) {
		t.Fatalf("the window starts now, not on the hour: %s", got.WindowStart)
	}
	if got.MaxItems != wire.ScheduleMaxItems || got.MaxPaths != wire.ScheduleMaxPaths {
		t.Fatalf("the bounds are not the published ones: %d/%d", got.MaxItems, got.MaxPaths)
	}
	if got.Settings["deadline"] != "tonight" {
		t.Fatalf("a wake-up decides on the settings; it got %v", got.Settings)
	}
	if err := plugintest.CheckSchedule(h.Manifest(), got, out); err != nil {
		t.Fatalf("a correct answer should pass the check: %v", err)
	}
}

func TestWake_RefusedWithoutThePermissionOrWithoutATick(t *testing.T) {
	// The manifest never asked to be woken, so filex would never call it.
	h := plugintest.New(&pluginkit.Plugin{Manifest: manifest(), Tick: func(*wire.TickInput) (*wire.TickOutput, error) {
		t.Fatal("a plugin without the schedule permission must never be ticked")
		return nil, nil
	}})
	if _, err := h.Wake(at); err == nil || !strings.Contains(err.Error(), "schedule permission") {
		t.Fatalf("want a refusal naming the permission, got %v", err)
	}

	// It asked, and registered nothing to answer with — the mistake the host
	// refuses at install when the export is missing entirely.
	h2 := plugintest.New(scheduled(nil))
	if _, err := h2.Wake(at); err == nil || !strings.Contains(err.Error(), "no Tick registered") {
		t.Fatalf("want a complaint about the missing Tick, got %v", err)
	}
}

func TestWake_IsReadOnly(t *testing.T) {
	var writeErr error
	h := plugintest.New(scheduled(nil))
	ref := h.Host.AddInput(plugintest.File{Name: "nda.pdf", Data: []byte("x")})
	h.Plugin.Tick = func(*wire.TickInput) (*wire.TickOutput, error) {
		writeErr = h.Host.StateSet(ref.Ref, "pending", "1")
		return &wire.TickOutput{}, nil
	}
	if _, err := h.Wake(at); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if writeErr == nil {
		t.Fatal("a wake-up decides; writing belongs to the job it schedules")
	}
	if !strings.Contains(writeErr.Error(), "permission_denied") {
		t.Fatalf("want the host's own refusal, got %v", writeErr)
	}
}

func TestCheckSchedule_NamesEveryThingTheHostWouldRefuse(t *testing.T) {
	m := scheduled(nil).Manifest
	in := plugintest.New(scheduled(nil)).TickInput(at)

	out := &wire.TickOutput{Items: []wire.ScheduleItem{
		{Key: "no spaces", DueAt: at, ActionID: "expire", Paths: []string{"docs://a.pdf"}},
		{Key: "ghost", DueAt: at, ActionID: "nope", Paths: []string{"docs://a.pdf"}},
		{Key: "late", DueAt: at.Add(3 * time.Hour), ActionID: "expire", Paths: []string{"docs://a.pdf"}},
		{Key: "nofiles", DueAt: at, ActionID: "expire"},
		{Key: "unqualified", DueAt: at, ActionID: "expire", Paths: []string{"a.pdf"}},
		{Key: "split", DueAt: at, ActionID: "expire", Paths: []string{"docs://a.pdf", "other://b.pdf"}},
		{Key: "whenever", ActionID: "expire", Paths: []string{"docs://a.pdf"}},
		{Key: "split", DueAt: at, ActionID: "expire", Paths: []string{"docs://a.pdf"}},
	}}
	err := plugintest.CheckSchedule(m, in, out)
	if err == nil {
		t.Fatal("none of that should have passed")
	}
	for _, want := range []string{
		"a key must match", "declares no action", "after this window ends",
		"no paths", "not adapter-qualified", "one item, one storage",
		"due_at is missing", "returned twice",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the complaint does not mention %q:\n%s", want, err)
		}
	}

	// Too many items is its own complaint, and it says what happens to them.
	many := &wire.TickOutput{}
	for i := 0; i <= wire.ScheduleMaxItems; i++ {
		many.Items = append(many.Items, wire.ScheduleItem{
			Key: "k" + strings.Repeat("x", i%3) + itoa(i), DueAt: at, ActionID: "expire",
			Paths: []string{"docs://a.pdf"},
		})
	}
	err = plugintest.CheckSchedule(m, in, many)
	if err == nil || !strings.Contains(err.Error(), "DROPPED") {
		t.Fatalf("want a warning that the extras are dropped, got %v", err)
	}

	// And an empty answer is the ordinary quiet hour, not a problem.
	if err := plugintest.CheckSchedule(m, in, &wire.TickOutput{}); err != nil {
		t.Fatalf("having nothing to do is not an error: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
