package wasmplugin

// state_list and who is asking.
//
// A PERSON's listing is narrowed by that person's ACL. The hourly WAKE-UP has
// no person, and running it through the ACL anyway answered "nobody may see
// anything" — acl.CanSee refuses a nil user — so a scheduled app looked out on
// an empty world every hour while the same rows were on a screen in front of
// somebody. A tick has no inputs, so there is no state_get to fall back on:
// the feature was inert.
//
// ⚠ These tests wire a visibility function shaped like the REAL one
// (internal/server/server.go: nil user → false, otherwise acl.CanSee). The
// harness leaves `visible` nil, which lets everything through — which is why
// the existing schedule tests were green while the module did nothing, and is
// the reason this file exists at all.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// realShapedACL wires the production check: no person, no rows — plus an
// allow-list so a person's listing can be narrowed the way an ACL narrows it.
func (h *harness) realShapedACL(t *testing.T, allowed ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, rel := range allowed {
		ok[strings.TrimPrefix(rel, "/")] = true
	}
	h.reg.SetVisibility(func(_ context.Context, u *model.User, _ int64, rel string) bool {
		if u == nil {
			return false
		}
		return ok[strings.TrimPrefix(rel, "/")]
	})
}

// stateList drives the host function directly, which is the only way to ask
// "what would THIS kind of call have been told?".
func stateList(t *testing.T, s *Scope, key string) []string {
	t.Helper()
	out, err := hfStateList(context.Background(), s, []byte(`{"key":"`+key+`","limit":50}`))
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok, "state_list answered %T", out)
	items, _ := m["items"].([]map[string]any)
	paths := make([]string, 0, len(items))
	for _, it := range items {
		paths = append(paths, it["path"].(string))
	}
	return paths
}

// TestStateList_TheWakeUpSeesItsOwnStateThroughTheRealFilter is the defect:
// with a production-shaped ACL wired, the hourly tick listed nothing, so a
// scheduled app could never find the work it was woken to do.
func TestStateList_TheWakeUpSeesItsOwnStateThroughTheRealFilter(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/a.txt", "hello")
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "state", "tick_due_in_s": "600"}, nil)

	// A person runs the app on a file, which leaves this app's own state on
	// it — a signing app's record of "waiting for a signature".
	_, err := h.runJob(t, p, "upper", []string{"docs/a.txt"}, "en")
	require.NoError(t, err)

	// ⚠ From here the instance has a real ACL, and nobody is behind the clock.
	h.realShapedACL(t, "docs/a.txt")

	h.pass(t, tickAt)
	item := h.row(t, p, "state-0")
	require.NotNil(t, item, "⭐ the wake-up found its own file; with the ACL applied to nobody it found none")
	assert.Equal(t, "expire", item.ActionID)
	assert.JSONEq(t, `["docs/a.txt"]`, item.PathsJSON)
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Contains(t, wake.Error, "from state", "the app's own note came back, so the tick ran")
}

// TestStateList_APersonIsStillNarrowedAndAVisitorStillSeesNothing guards the
// two things the fix must NOT have unlocked. The marker is on the call the
// host itself started — it is not "the actor happens to be nil", because a
// public page call is actor-less too and that one is a stranger.
func TestStateList_APersonIsStillNarrowedAndAVisitorStillSeesNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/mine.txt", "mine")
	h.writeFile(t, "gizli/theirs.txt", "not yours")
	p := h.install(t)
	u := h.actor(t)
	_, err := h.runJob(t, p, "upper", []string{"docs/mine.txt", "gizli/theirs.txt"}, "en")
	require.NoError(t, err)

	h.realShapedACL(t, "docs/mine.txt")

	// ⭐ A PERSON: the ACL still narrows the listing. An app may keep state on
	// a document this person was never given, and a listing is not a way
	// around that.
	mine := h.jobScope(t, p, u)
	got := stateList(t, mine, "runs")
	require.Len(t, got, 1, "got %v", got)
	assert.Contains(t, got[0], "docs/mine.txt")

	// ⭐⭐ A VISITOR on a public link: actor-less, exactly like the tick, and
	// told nothing. This is the trap the fix had to step around.
	visitor, err := newScope(p, h.reg, "", h.st.ID, h.drv, nil, "en", false)
	require.NoError(t, err)
	defer visitor.Close()
	assert.Empty(t, stateList(t, visitor, "runs"),
		"a page call is actor-less too — it must not inherit the system's reach")
	assert.False(t, visitor.system, "only the host's own clock sets this")

	// ⭐ And the system sees its own rows, whatever the ACL says about anyone.
	sys, err := newScope(p, h.reg, "", 0, nil, nil, "", false)
	require.NoError(t, err)
	defer sys.Close()
	sys.system = true
	assert.Len(t, stateList(t, sys, "runs"), 2, "the app's own bookkeeping is the app's")
}

// TestPermissions_TheKitKnowsEveryNameTheHostAccepts is why `schedule` was
// missing from the SDK's closed set for a release: nothing compared the two.
// A plugin asking for the wake-up had its manifest refused by its own tests
// while this server installed it happily — the kit was wrong, and the app
// author had no way to tell.
func TestPermissions_TheKitKnowsEveryNameTheHostAccepts(t *testing.T) {
	for p := range bare {
		assert.Truef(t, plugintest.Permissions[string(p)],
			"the host accepts %q and the kit refuses it: add it to plugintest.Permissions", p)
	}
	for name := range plugintest.Permissions {
		_, err := ParsePermission(name)
		assert.NoErrorf(t, err, "the kit accepts %q and the host refuses it", name)
	}
	// The one that was missing, said by name so the reason survives a rename.
	assert.True(t, plugintest.Permissions[string(PermSchedule)])
	var m wire.Manifest
	m.Permissions = []string{string(PermSchedule)}
	assert.NotPanics(t, func() { _, _ = ParsePermission(string(PermSchedule)) })
}
