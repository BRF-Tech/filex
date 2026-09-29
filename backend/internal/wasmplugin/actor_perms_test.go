package wasmplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The app is told which of its own permissions the person holds ──────
//
// wire.Actor.Permissions: the ids of the app's `user_permissions` the person
// a call runs as holds, as the HTTP layer decides them at the door
// (SetHeldPermissions). The signing app drew "…or Request signatures…" for
// everybody, including a person the menu no longer offered it to, because the
// call did not say (filex-sign docs/SIGN.md §20).

func withUserPerms(m map[string]any) {
	m["user_permissions"] = []any{
		map[string]any{"id": "request", "label": map[string]any{"en": "Request", "tr": "İste"}, "default": "user"},
		map[string]any{"id": "audit", "label": map[string]any{"en": "Audit", "tr": "Denetim"}, "default": "admin"},
	}
}

// heldRecorder answers SetHeldPermissions from a fixed table and records who
// was asked about.
type heldRecorder struct {
	mu    sync.Mutex
	held  map[int64][]string
	asked []int64
}

func (h *heldRecorder) answer(_ context.Context, u *model.User, p *Installed) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, u.ID)
	return h.held[u.ID]
}

func (h *heldRecorder) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.asked)
}

func wizardText(t *testing.T, s *wire.Surface) string {
	t.Helper()
	require.NotEmpty(t, s.Nodes)
	b, err := json.Marshal(s.Nodes[0].Props["text"])
	require.NoError(t, err)
	var txt map[string]string
	require.NoError(t, json.Unmarshal(b, &txt))
	return txt["en"]
}

func TestActor_PermissionsReachTheApp(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	in := echoInput(t, withUserPerms)
	st, _, err := h.reg.Install(ctx, in)
	require.NoError(t, err)
	p, _ := h.reg.ByID(st.ID)

	ada, err := h.store.CreateUser(ctx, "ada@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	bob, err := h.store.CreateUser(ctx, "bob@test.local", "x", model.RoleViewer, "en", "UTC")
	require.NoError(t, err)
	rec := &heldRecorder{held: map[int64][]string{ada.ID: {"request"}}}
	h.reg.SetHeldPermissions(rec.answer)

	// A view event: the person's own.
	s, err := h.reg.ViewEvent(ctx, "echo", "wizard", 0, nil, ada, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(wizardText(t, s), " perms=request"), wizardText(t, s))

	// The context is the host's: what a browser puts there is not read.
	s, err = h.reg.ViewEvent(ctx, "echo", "wizard", 0, nil, bob, "en", wire.ViewEventInput{Event: "open",
		Context: wire.CallContext{Actor: &wire.Actor{ID: bob.ID, Permissions: []string{"request", "audit"}}}})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(wizardText(t, s), " perms="), "bob holds none: %s", wizardText(t, s))

	// A job: the person it runs as, asked when it runs.
	h.writeFile(t, "a.txt", "x")
	runAs := func(actor *int64) string {
		pj, _ := json.Marshal([]string{"a.txt"})
		job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "facts", ActorID: actor,
			StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: "{}", Locale: "en", Label: "x", Status: model.AppPluginJobPending}
		require.NoError(t, h.store.CreateAppPluginJob(ctx, job))
		require.NoError(t, h.reg.RunPluginAction(ctx, &ops.Op{ID: 3, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"a.txt"}, Dest: job.ID}, nil))
		got, _ := h.store.GetAppPluginJob(ctx, job.ID)
		require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
		return got.Message
	}
	assert.Equal(t, "ro=false perms=request", runAs(&ada.ID))
	rec.held[ada.ID] = []string{"request", "audit"}
	assert.Equal(t, "ro=false perms=request,audit", runAs(&ada.ID), "decided when the job runs, not when it was queued")

	// Work nobody started (a wake-up's job) holds none, and nobody is asked.
	before := rec.count()
	assert.Equal(t, "ro=false", runAs(nil))
	assert.Equal(t, before, rec.count(), "no person, no question")

	// A public page's event: nobody signed in — never asked.
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	token, _, sh := h.invite(t, p, "")
	_, ins, err := h.reg.LoadPage(ctx, token)
	require.NoError(t, err)
	before = rec.count()
	_, err = h.reg.PageEvent(ctx, sh, ins, wire.ViewEventInput{Event: "open",
		Context: wire.CallContext{Locale: "en", Actor: &wire.Actor{ID: ada.ID, Permissions: []string{"request"}}}}, "203.0.113.9")
	require.NoError(t, err)
	assert.Equal(t, before, rec.count(), "a page event carries no actor")
}

// An interface's call to its module carries the same actor a view does.
func TestActor_PermissionsReachTheInterfacesCall(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	in := echoInput(t, func(m map[string]any) {
		withUserPerms(m)
		m["ui"] = map[string]any{"bundle": map[string]any{}}
		m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Panel", "tr": "Pano"}})
	})
	in.UI = bytes.NewReader(uiZip(t, uiDoc()))
	m, err := ParseManifest(in.Manifest)
	require.NoError(t, err)
	in.Granted = permStrings(m.Perms)
	_, _, err = h.reg.Install(ctx, in)
	require.NoError(t, err)
	ada := &model.User{ID: 7, Email: "ada@test.local"}
	h.reg.SetHeldPermissions((&heldRecorder{held: map[int64][]string{7: {"audit"}}}).answer)

	out, err := h.reg.UICall(ctx, "echo", "panel", 0, nil, ada, "en", "echo", nil)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, []any{"audit"}, got["perms"])
}

// An app that declares no user permissions is never asked about: its actor
// carries none, and the answer costs nothing.
func TestActor_AnAppWithoutUserPermissionsIsNotAsked(t *testing.T) {
	h := newHarness(t, nil)
	// The echo fixture declares one (`request`, since the role editor's
	// tests): take it and every `requires` out.
	_, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		delete(m, "user_permissions")
		for _, key := range []string{"actions", "views"} {
			list, _ := m[key].([]any)
			for _, e := range list {
				if row, ok := e.(map[string]any); ok {
					delete(row, "requires")
				}
			}
		}
	}))
	require.NoError(t, err)
	rec := &heldRecorder{held: map[int64][]string{7: {"request"}}}
	h.reg.SetHeldPermissions(rec.answer)
	s, err := h.reg.ViewEvent(context.Background(), "echo", "wizard", 0, nil, &model.User{ID: 7}, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(wizardText(t, s), " perms="))
	assert.Zero(t, rec.count())

	// On the wire an actor that holds nothing says nothing: an app built
	// before the field reads the same bytes it always did.
	b, err := json.Marshal(wire.Actor{ID: 7, Email: "ada@test.local"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "permissions")
}
