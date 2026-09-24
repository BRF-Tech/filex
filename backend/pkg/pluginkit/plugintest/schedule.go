package plugintest

// The scheduled wake-up, in the test kit.
//
// A `tick` is the one call nobody watches: it runs at 03:00 with no person
// in front of it, and a mistake in it shows up as work that silently never
// happened. That is the worst kind of thing to first try in a wasm module on
// a running instance, so the kit runs it here — with the same read-only
// scope the host gives it, and with Check applying the host's published
// bounds to the answer before `plugin.wasm` exists.

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

var scheduleKeyRe = regexp.MustCompile(wire.ScheduleKeyPattern)

// Wake runs the hourly wake-up as filex would at `now`: the window runs from
// `now` to the next hourly boundary, the settings and engines are this
// harness's, and the call is READ-ONLY — a `StateSet` from a tick is refused
// here exactly as the real host refuses it.
//
//	out, err := h.Wake(time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC))
//	require.NoError(t, err)
//	require.NoError(t, plugintest.CheckSchedule(h.Manifest(), h.TickInput(now), out))
func (h *Harness) Wake(now time.Time) (*wire.TickOutput, error) {
	return h.Tick(h.TickInput(now))
}

// TickInput is the wake-up filex would hand the plugin at `now`.
func (h *Harness) TickInput(now time.Time) *wire.TickInput {
	now = now.UTC()
	return &wire.TickInput{
		Now:         now,
		WindowStart: now,
		WindowEnd:   now.Truncate(time.Hour).Add(time.Hour),
		MaxItems:    wire.ScheduleMaxItems,
		MaxPaths:    wire.ScheduleMaxPaths,
		Locale:      h.Locale,
		Settings:    h.Host.CallSettings(),
		Engines:     h.Host.InstalledEngines(),
	}
}

// Tick runs the wake-up with an input you built yourself — a shorter window,
// a different clock, settings the administrator has not entered yet.
func (h *Harness) Tick(in *wire.TickInput) (*wire.TickOutput, error) {
	if !hasPermission(h.Manifest(), "schedule") {
		return nil, errors.New("plugintest: the manifest does not ask for the schedule permission, so filex would never call tick")
	}
	if h.Plugin.Tick == nil {
		return nil, errors.New("plugintest: no Tick registered, but the manifest asks for the schedule permission")
	}
	if in == nil {
		in = h.TickInput(time.Now())
	}
	// A wake-up gets a screen's scope, not a job's: it decides, the action
	// it schedules acts.
	h.Host.EnterScreen()
	out, err := h.Plugin.Tick(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.TickOutput{}
	}
	return out, nil
}

// CheckSchedule applies the bounds filex publishes to an answer, and returns
// every complaint at once rather than the first.
//
// It is a warning, not the enforcement — the host is what refuses — but it
// is the same set of numbers (wire.Schedule*) and the same shapes, checked
// where a failing `go test` costs seconds instead of an hour of wall clock.
func CheckSchedule(m wire.Manifest, in *wire.TickInput, out *wire.TickOutput) error {
	if out == nil {
		return errors.New("tick answered nothing (return an empty TickOutput when there is nothing to do)")
	}
	if in == nil {
		return errors.New("CheckSchedule needs the TickInput the answer was made for")
	}
	maxItems, maxPaths := in.MaxItems, in.MaxPaths
	if maxItems <= 0 {
		maxItems = wire.ScheduleMaxItems
	}
	if maxPaths <= 0 {
		maxPaths = wire.ScheduleMaxPaths
	}

	var bad []string
	add := func(msg string) { bad = append(bad, msg) }
	if len(out.Items) > maxItems {
		add(strconv.Itoa(len(out.Items)) + " items: at most " + strconv.Itoa(maxItems) +
			" a wake-up, and the rest are DROPPED — order them so the urgent work comes first")
	}
	seen := map[string]bool{}
	for _, it := range out.Items {
		where := "item " + quote(it.Key)
		if !scheduleKeyRe.MatchString(it.Key) {
			add(where + ": a key must match " + wire.ScheduleKeyPattern)
		}
		if seen[it.Key] {
			add(where + ": returned twice in one answer — one key is one piece of work")
		}
		seen[it.Key] = true
		if !hasAction(m, it.ActionID) {
			add(where + ": the manifest declares no action " + quote(it.ActionID))
		}
		switch {
		case it.DueAt.IsZero():
			add(where + ": due_at is missing")
		case it.DueAt.After(in.WindowEnd):
			add(where + ": due " + it.DueAt.UTC().Format(time.RFC3339) + ", after this window ends at " +
				in.WindowEnd.Format(time.RFC3339) + " — it will NOT be scheduled; return it at the wake-up whose window contains it")
		}
		switch {
		case len(it.Paths) == 0:
			add(where + ": no paths — scheduled work runs as a job, and a job runs on files")
		case len(it.Paths) > maxPaths:
			add(where + ": " + strconv.Itoa(len(it.Paths)) + " paths, at most " + strconv.Itoa(maxPaths))
		}
		adapter := ""
		for _, p := range it.Paths {
			i := strings.Index(p, "://")
			if i <= 0 {
				add(where + ": path " + quote(p) + " is not adapter-qualified (docs://reports/nda.pdf, as StateList answers)")
				continue
			}
			if adapter == "" {
				adapter = p[:i]
			} else if adapter != p[:i] {
				add(where + ": paths span " + quote(adapter) + " and " + quote(p[:i]) + " — one item, one storage")
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return errors.New("plugintest: this wake-up would not be accepted as returned:\n  - " + strings.Join(bad, "\n  - "))
}

func hasPermission(m wire.Manifest, want string) bool {
	for _, p := range m.Permissions {
		if p == want {
			return true
		}
	}
	return false
}

func quote(s string) string { return `"` + s + `"` }
