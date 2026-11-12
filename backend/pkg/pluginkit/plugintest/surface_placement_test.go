package plugintest_test

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// placedManifest is the demo manifest with one view of every placement a
// surface is drawn in.
func placedManifest() wire.Manifest {
	m := manifest()
	m.Views = append(m.Views,
		wire.View{ID: "links", Placement: "home", Label: text("Links", "Bağlantılar")},
		wire.View{ID: "board", Placement: "page", Label: text("Board", "Pano")},
		wire.View{ID: "panel", Placement: "inspector", Label: text("Panel", "Panel")},
		wire.View{ID: "wizard", Placement: wire.PlacementSetup, Label: text("Set up", "Kur")},
	)
	return m
}

// linkList is a home screen as a link shortener draws it: a list of what
// the app keeps, no footer buttons, nothing queued, never done.
func linkList() *wire.Surface {
	return &wire.Surface{
		Title: text("Links", "Bağlantılar"),
		Nodes: []wire.Node{{Type: "list", Props: map[string]any{
			"columns": []map[string]any{{"key": "code", "label": text("Code", "Kod")}},
			"rows":    []map[string]any{{"id": "a1", "cells": map[string]any{"code": "a1"}}},
		}}},
	}
}

// A screen with no buttons is a dead end only where something must end it:
// a modal step, the setup wizard. An app's home, a full page and a section
// of the details panel stay where they are drawn and the person goes on from
// them, so the warning there was noise (#327). InspectSurface cannot tell a
// view's placement and keeps reading every screen as a step.
func TestInspectSurfaceFor_ADeadEndIsAStepsAlone(t *testing.T) {
	m := placedManifest()
	s := linkList()

	for _, view := range []string{"links", "board", "panel"} {
		r := plugintest.InspectSurfaceFor(m, view, s)
		none(t, r, plugintest.SevWarn, "dead end")
		if errs := r.Errors(); len(errs) > 0 {
			t.Fatalf("%s: the list is a good screen:\n%s", view, join(errs))
		}
	}
	for _, view := range []string{"options", "wizard"} {
		f := find(t, plugintest.InspectSurfaceFor(m, view, s), plugintest.SevWarn, "dead end")
		if f.Where != "surface.actions" {
			t.Fatalf("%s: the dead end is the footer's, got %q", view, f.Where)
		}
	}
	// The placement-blind call is the one it always was.
	find(t, plugintest.InspectSurface(m, s), plugintest.SevWarn, "dead end")

	// The rest of the rules hold in every placement: a home screen with two
	// primary buttons is still refused.
	two := linkList()
	two.Actions = []wire.SurfaceAction{
		{ID: "a", Label: text("A", "A"), Primary: true},
		{ID: "b", Label: text("B", "B"), Primary: true},
	}
	find(t, plugintest.InspectSurfaceFor(m, "links", two), plugintest.SevError, "primary buttons")

	// A view the manifest does not declare is an error, not a guess.
	find(t, plugintest.InspectSurfaceFor(m, "linsk", s), plugintest.SevError, `declares no view "linsk"`)
}

// The way an app's test reaches it: the harness draws the home view, and
// CheckSurfaceFor logs nothing about a dead end, while the same screen drawn
// as the modal is logged.
func TestCheckSurfaceFor_TheHarnessHomeScreen(t *testing.T) {
	m := placedManifest()
	h := plugintest.New(&pluginkit.Plugin{
		Manifest: m,
		Views: map[string]pluginkit.ViewFunc{
			"links":   func(*wire.ViewEventInput) (*wire.Surface, error) { return linkList(), nil },
			"options": func(*wire.ViewEventInput) (*wire.Surface, error) { return linkList(), nil },
		},
	})
	for view, wantDeadEnd := range map[string]bool{"links": false, "options": true} {
		s, err := h.Open(view)
		if err != nil {
			t.Fatal(err)
		}
		rec := &recorder{}
		plugintest.CheckSurfaceFor(rec, h.Manifest(), view, s)
		if len(rec.errors) > 0 {
			t.Fatalf("%s: %v", view, rec.errors)
		}
		logged := strings.Contains(strings.Join(rec.logs, "\n"), "dead end")
		if logged != wantDeadEnd {
			t.Fatalf("%s: dead end logged = %v, want %v (logs: %q)", view, logged, wantDeadEnd, rec.logs)
		}
	}
}
