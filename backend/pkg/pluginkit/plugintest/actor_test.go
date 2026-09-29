package plugintest_test

import (
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The harness runs as an administrator, and filex gives an administrator
// every app permission: the default actor holds all of the manifest's
// user_permissions, on a job and on a screen. A test clears them to draw the
// screen for somebody who holds fewer.
func TestHarness_ActorHoldsTheAppsPermissionsUntilToldOtherwise(t *testing.T) {
	m := manifest()
	m.UserPermissions = []wire.UserPermission{
		{ID: "request", Label: text("Request", "İste"), Default: "user"},
		{ID: "audit", Label: text("Audit", "Denetim"), Default: "admin"},
	}
	var seenJob, seenView []bool
	h := plugintest.New(&pluginkit.Plugin{
		Manifest: m,
		Actions: map[string]pluginkit.ActionFunc{"shout": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
			seenJob = append(seenJob, in.Actor.Can("request"))
			return &wire.ActionRunOutput{OK: true}, nil
		}},
		Views: map[string]pluginkit.ViewFunc{"options": func(in *wire.ViewEventInput) (*wire.Surface, error) {
			seenView = append(seenView, in.Context.Actor.Can("audit"))
			return &wire.Surface{Done: true}, nil
		}},
	})
	if got := h.Actor.Permissions; len(got) != 2 || got[0] != "request" || got[1] != "audit" {
		t.Fatalf("default actor permissions: %v", got)
	}
	if _, err := h.Do("shout", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Open("options"); err != nil {
		t.Fatal(err)
	}
	h.Actor.Permissions = nil
	if _, err := h.Do("shout", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Open("options"); err != nil {
		t.Fatal(err)
	}
	if len(seenJob) != 2 || !seenJob[0] || seenJob[1] {
		t.Fatalf("job saw %v, want [true false]", seenJob)
	}
	if len(seenView) != 2 || !seenView[0] || seenView[1] {
		t.Fatalf("view saw %v, want [true false]", seenView)
	}

	// An app that declares none: the actor holds none.
	if p := plugintest.New(&pluginkit.Plugin{Manifest: manifest()}).Actor.Permissions; p != nil {
		t.Fatalf("no user_permissions, yet %v", p)
	}
}
