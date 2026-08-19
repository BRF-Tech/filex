package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Conformance exists for one reason: a plugin that claims a capability it
// cannot perform makes filex look broken. The user meets an upload button
// that fails and has no way to know the fault is in a plugin somebody
// installed. So a claim is probed, and a plugin that fails its own claims is
// not offered at all.
//
// These tests are that promise, held in place.

func TestConformancePassesForAnHonestPlugin(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	_ = f
	rep := plugin.RunConformance(context.Background(), d, fullCaps(), "", "test")
	if !rep.Verified {
		t.Fatalf("an honest plugin failed conformance: %s\n%s", rep.Summary(), formatReport(rep))
	}
	// The probes must actually have run — a report of nothing but skips would
	// "pass" and prove nothing.
	passes := 0
	for _, r := range rep.Results {
		if r.Status == plugin.ProbePass {
			passes++
		}
	}
	if passes < 8 {
		t.Fatalf("only %d probes passed; the suite is not exercising the driver:\n%s", passes, formatReport(rep))
	}
}

// The headline case: the plugin says it can write, and cannot.
func TestConformanceCatchesAWriteThatDoesNotWrite(t *testing.T) {
	f := newFakePlugin("liar", fullCaps())
	defer f.Close()
	cl := f.client()
	defer cl.Close()
	f.mu.Lock()
	f.swallowWrites = true // accepts the PUT, stores nothing
	f.mu.Unlock()

	d := plugin.NewDriver(staticHandle{c: cl, name: "liar"}, fullCaps())
	if err := d.Init(context.Background(), map[string]any{"root": "/data"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	rep := plugin.RunConformance(context.Background(), d, fullCaps(), "", "test")
	if rep.Verified {
		t.Fatalf("a plugin whose write silently drops the bytes passed conformance:\n%s", formatReport(rep))
	}
	if err := rep.FailureError(); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("the failure should name the probe that caught it, got %v", err)
	}
}

// A plugin that answers a plain error for a missing path breaks the
// difference between 404 and 500 for every surface above it.
func TestConformanceCatchesAWrongNotFound(t *testing.T) {
	f := newFakePlugin("vague", fullCaps())
	defer f.Close()
	cl := f.client()
	defer cl.Close()
	f.mu.Lock()
	f.notFoundIs500 = true
	f.mu.Unlock()

	d := plugin.NewDriver(staticHandle{c: cl, name: "vague"}, fullCaps())
	if err := d.Init(context.Background(), map[string]any{"root": "/data"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	rep := plugin.RunConformance(context.Background(), d, fullCaps(), "", "test")
	if rep.Verified {
		t.Fatalf("a plugin that answers 500 for a missing path passed conformance:\n%s", formatReport(rep))
	}
	found := false
	for _, r := range rep.Results {
		if r.Name == "not_found" && r.Status == plugin.ProbeFail {
			found = true
			if !strings.Contains(r.Detail, "storage.ErrNotFound") {
				t.Errorf("the failure should tell the author what to return, got %q", r.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("the not_found probe did not fail:\n%s", formatReport(rep))
	}
}

// set_mtime is the capability where "accepted and dropped" is indistinguishable
// from "applied" unless somebody checks — so conformance checks.
func TestConformanceCatchesAnMtimeThatIsDropped(t *testing.T) {
	caps := fullCaps()
	f := newFakePlugin("dropmtime", caps)
	defer f.Close()
	cl := f.client()
	defer cl.Close()
	f.mu.Lock()
	f.ignoreMtime = true
	f.mu.Unlock()

	d := plugin.NewDriver(staticHandle{c: cl, name: "dropmtime"}, caps)
	if err := d.Init(context.Background(), map[string]any{"root": "/data"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	rep := plugin.RunConformance(context.Background(), d, caps, "", "test")
	if rep.Verified {
		t.Fatalf("a plugin that accepts an mtime and drops it passed conformance:\n%s", formatReport(rep))
	}
}

// A read-only plugin is not "incomplete" — it is honest, and must pass.
func TestConformancePassesForAnHonestReadOnlyPlugin(t *testing.T) {
	caps := plugin.Capabilities{Range: true}
	f, d := newDriver(t, caps)
	f.seed("already-here.txt", "content")
	rep := plugin.RunConformance(context.Background(), d, caps, "", "test")
	if !rep.Verified {
		t.Fatalf("a read-only plugin failed conformance:\n%s", formatReport(rep))
	}
	// …and it must SAY that the write half was not probed rather than imply
	// it passed.
	skipped := false
	for _, r := range rep.Results {
		if r.Name == "write" && r.Status == plugin.ProbeSkip {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("the report should mark write as skipped for a read-only plugin:\n%s", formatReport(rep))
	}
}

// The whole point, at the manager level: a plugin that fails its claims is
// REFUSED, its driver is never registered, and the report says which probe
// caught it.
func TestManagerRefusesAPluginThatFailsItsOwnClaims(t *testing.T) {
	f := newFakePlugin("badwrite", fullCaps())
	defer f.Close()
	f.mu.Lock()
	f.swallowWrites = true
	f.selfTestOK = true
	f.mu.Unlock()

	m, _, _ := newManager(t)
	ctx := context.Background()
	st, err := m.InstallRemote(ctx, "badwrite", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := waitState(t, m, st.ID, plugin.StateRefused)

	if _, err := storage.Get("plugin:badwrite"); err == nil {
		t.Fatal("the driver of a plugin that failed conformance is registered — a storage could be built on it")
	}
	if !strings.Contains(got.StateError, "fails its own claims") {
		t.Fatalf("the refusal should say what happened, got %q", got.StateError)
	}
	if got.Conformance == nil || got.Conformance.Verified {
		t.Fatal("the status should carry the failing report so the admin page can show which probe caught it")
	}
	if len(got.Conformance.Failures()) == 0 {
		t.Fatal("the report has no failures but the plugin was refused")
	}
}

// An honest plugin with a selftest area is verified at install, and the
// report travels with its status.
func TestManagerVerifiesAnHonestPluginAtInstall(t *testing.T) {
	f := newFakePlugin("honest", fullCaps())
	defer f.Close()
	f.mu.Lock()
	f.selfTestOK = true
	f.mu.Unlock()

	m, _, _ := newManager(t)
	st, err := m.InstallRemote(context.Background(), "honest", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := waitState(t, m, st.ID, plugin.StateRunning)
	if got.Conformance == nil {
		t.Fatal("a plugin with a selftest endpoint should carry a conformance report")
	}
	if !got.Conformance.Verified {
		t.Fatalf("an honest plugin was not verified: %s", formatReport(got.Conformance))
	}
	if got.Conformance.Scratch != "selftest" {
		t.Errorf("the report should say where it ran, got %q", got.Conformance.Scratch)
	}
}

// A plugin with no selftest endpoint is not refused — it is registered and
// reported as unverified, so the operator knows the probes are still owed.
func TestPluginWithoutSelfTestIsRegisteredButUnverified(t *testing.T) {
	f := newFakePlugin("nosel", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	st, err := m.InstallRemote(context.Background(), "nosel", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := waitState(t, m, st.ID, plugin.StateRunning)
	if got.Conformance != nil {
		t.Fatalf("a plugin with no selftest area cannot have a report: %+v", got.Conformance)
	}
	if _, err := storage.Get("plugin:nosel"); err != nil {
		t.Fatalf("it should still be usable (with the probes owed): %v", err)
	}
}

func formatReport(r *plugin.Report) string {
	if r == nil {
		return "(no report)"
	}
	var b strings.Builder
	for _, p := range r.Results {
		b.WriteString("  " + p.Status + " " + p.Name)
		if p.Detail != "" {
			b.WriteString(" — " + p.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}
