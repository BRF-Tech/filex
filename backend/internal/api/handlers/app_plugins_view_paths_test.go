package handlers_test

// A modal view opened on a SELECTION keeps the whole selection for every
// event of its conversation (filex #64).
//
// The run that opens the screen has always carried `paths`; the browser then
// echoed only `path` — the first row — on `change` / `submit` / `action`, and
// the host did exactly what it was told: the second screen said "the file
// a.txt" where the first had said "3 files", and the job the submit queued ran
// on one file. The host half measured here is what the browser now relies on:
//
//   - `paths` on a view event reaches the plugin as the event's inputs, all of
//     them, in order (the `picks` fixture view prints them);
//   - the submit queues the job on every one of them;
//   - EVERY path is judged again for the person asking — a path they cannot
//     see is a 403, not a silently shorter selection — and at submit the
//     action's `applies` is re-checked on each file;
//   - an older client that still sends only `path` keeps working, on one file.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

var viewSelection = []string{"main://docs/a.txt", "main://docs/b.txt", "main://docs/c.txt"}

func seedSelection(t *testing.T, f *appFixture) {
	t.Helper()
	f.writeFile(t, "docs/a.txt", "alpha")
	f.writeFile(t, "docs/b.txt", "bravo")
	f.writeFile(t, "docs/c.txt", "charlie")
}

func viewEventURL(f *appFixture) string {
	return f.srv.URL + "/api/files/plugins/views/echo/picks/event"
}

func TestAppPlugins_ViewEvent_KeepsTheWholeSelection(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	seedSelection(t, f)
	all := "n=3: " + strings.Join(viewSelection, ", ")

	// The menu click: the opening screen sees the three files.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/gather/run",
		map[string]any{"paths": viewSelection})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "picks open "+all)

	// A form edit and a secondary button: still the three files, in order.
	for _, ev := range []string{"change", "action"} {
		status, raw = doReq(t, f.admin, http.MethodPost, viewEventURL(f), map[string]any{
			"path": viewSelection[0], "paths": viewSelection, "event": ev, "action_id": "other",
			"data": map[string]any{"values": map[string]any{"note": "hi"}},
		})
		require.Equal(t, http.StatusOK, status, string(raw))
		assert.Contains(t, string(raw), "picks "+ev+" "+all, "a %s event keeps the selection", ev)
	}

	// The submit queues ONE job on all three.
	before := f.census(t)
	status, raw = doReq(t, f.admin, http.MethodPost, viewEventURL(f), map[string]any{
		"path": viewSelection[0], "paths": viewSelection, "event": "submit", "action_id": "gather",
		"data": map[string]any{"values": map[string]any{"note": "hi"}},
	})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	assert.Equal(t, queueCensus{jobs: before.jobs + 1, ops: before.ops + 1}, f.census(t))
	var queued struct {
		Op struct {
			ID      int64    `json:"id"`
			Sources []string `json:"sources"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &queued))
	assert.Equal(t, []string{"docs/a.txt", "docs/b.txt", "docs/c.txt"}, queued.Op.Sources)

	op := f.drain(t, queued.Op.ID)
	require.Equal(t, "ok", op["status"], fmt.Sprint(op))
	for name, body := range map[string]string{"a": "alpha", "b": "bravo", "c": "charlie"} {
		got, err := os.ReadFile(filepath.Join(f.root, "docs", name+"-gathered.txt"))
		require.NoError(t, err, "the job ran on %s.txt", name)
		assert.Equal(t, "hi:"+body, string(got))
	}
}

// An older browser echoes only `path`. It still gets an answer — on that one
// file, which is what it asked for.
func TestAppPlugins_ViewEvent_PathAloneStillWorks(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	seedSelection(t, f)
	status, raw := doReq(t, f.admin, http.MethodPost, viewEventURL(f), map[string]any{
		"path": viewSelection[1], "event": "change", "data": map[string]any{"values": map[string]any{}},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "picks change n=1: main://docs/b.txt")
}

// Every path in `paths` is the PERSON's to show: one they cannot see refuses
// the event outright rather than being dropped from the selection.
func TestAppPlugins_ViewEvent_EveryPathIsJudgedForThePerson(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	seedSelection(t, f)
	f.writeFile(t, "private/x.txt", "secret")
	u := seedSharedUser(t, f.store, "picker@test.local", "PickerPass1!")
	grant(t, f.store, f.st, u, "docs", model.GrantViewer, true)
	client := freshClient(t)
	testutil.LoginAs(t, f.srv, client, "picker@test.local", "PickerPass1!")

	status, raw := doReq(t, client, http.MethodPost, viewEventURL(f), map[string]any{
		"paths": viewSelection[:2], "event": "change", "data": map[string]any{"values": map[string]any{}},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "picks change n=2")

	for _, ev := range []string{"change", "submit"} {
		before := f.census(t)
		status, raw = doReq(t, client, http.MethodPost, viewEventURL(f), map[string]any{
			"path": viewSelection[0], "paths": []string{viewSelection[0], "main://private/x.txt", viewSelection[1]},
			"event": ev, "data": map[string]any{"values": map[string]any{}},
		})
		assert.Equal(t, http.StatusForbidden, status, "%s: %s", ev, raw)
		assert.Contains(t, string(raw), "permission_denied")
		assert.Contains(t, string(raw), "private/x.txt", "the refusal names the path")
		assert.NotContains(t, string(raw), "picks", "the plugin was never asked")
		assert.Equal(t, before, f.census(t), "%s queued nothing", ev)
	}
}

// At submit the action's `applies` is measured on EVERY file again: a file it
// does not apply to, slipped into `paths` after the screen opened, refuses the
// job and queues nothing.
func TestAppPlugins_ViewEvent_SubmitRechecksAppliesOnEveryPath(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	seedSelection(t, f)
	f.writeFile(t, "docs/pic.png", "not really")
	before := f.census(t)
	status, raw := doReq(t, f.admin, http.MethodPost, viewEventURL(f), map[string]any{
		"paths": append(append([]string{}, viewSelection...), "main://docs/pic.png"), "event": "submit",
		"action_id": "gather", "data": map[string]any{"values": map[string]any{"note": "x"}},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
	assert.Contains(t, string(raw), "not_applicable")
	assert.Equal(t, before, f.census(t), "nothing queued")
	_, err := os.Stat(filepath.Join(f.root, "docs", "a-gathered.txt"))
	assert.True(t, os.IsNotExist(err))
}

// The event door takes no longer a selection than the run that opens the
// screen does (500): each path costs an ACL walk, and a crafted body must not
// be able to buy thousands of them.
func TestAppPlugins_ViewEvent_RefusesAnOversizedSelection(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	seedSelection(t, f)
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf("main://docs/f%03d.txt", i)
	}
	status, raw := doReq(t, f.admin, http.MethodPost, viewEventURL(f), map[string]any{
		"paths": many, "event": "change", "data": map[string]any{"values": map[string]any{}},
	})
	head := string(raw)
	if len(head) > 200 {
		head = head[:200]
	}
	assert.Equal(t, http.StatusBadRequest, status, head)
	assert.Contains(t, head, "too many paths")
}
