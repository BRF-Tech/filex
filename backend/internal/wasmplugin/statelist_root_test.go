package wasmplugin

// state_list and a `root:` token (filex Roadmap #154, found in the
// GHSA-8gvc-6w52-6c7j review, 2026-10-04).
//
// A listing went through the asking person's ACL only. A call made with a
// token confined to one folder - the token a host hands an embed - was told
// about every file the app keeps state on, on every storage: its path, its
// name and the app's value for it. These drive the host function through the
// calls a token reaches it by: an interface's call to its module (ui_call),
// and the job a token queued, whose door stamps the root on the row.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// rootedCtx is a request context carrying an API token confined to root.
func rootedCtx(root string) context.Context {
	return auth.WithToken(context.Background(), &model.APIToken{Scopes: "read,write,root:" + root})
}

// seedRunsOn has `upper` keep its `runs` counter on each file.
func (h *harness) seedRunsOn(t *testing.T, p *Installed, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		h.writeFile(t, rel, "x")
		job, err := h.runJob(t, p, "upper", []string{rel}, "en")
		require.NoError(t, err)
		require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	}
}

func TestStateList_AnInterfaceCallOfARootTokenListsOnlyInsideTheRoot(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installEchoWithUI(t)
	h.seedRunsOn(t, p, "docs/a.txt", "secret/s.txt")
	listed := func(ctx context.Context) []string {
		out, err := h.reg.UICall(ctx, "echo", "panel", h.st.ID, nil, nil, "en", "state_list", nil)
		require.NoError(t, err)
		var got struct {
			Paths []string `json:"paths"`
		}
		require.NoError(t, json.Unmarshal(out, &got))
		return got.Paths
	}

	assert.ElementsMatch(t, []string{"main://docs/a.txt", "main://secret/s.txt"}, listed(context.Background()), "unconfined, both")
	assert.Equal(t, []string{"main://docs/a.txt"}, listed(rootedCtx("main://docs")), "confined to main://docs")
	assert.Empty(t, listed(rootedCtx("yan://docs")), "confined to another storage")
}

// runJobWith is harness.runJob with the params the job row carries.
func (h *harness) runJobWith(t *testing.T, p *Installed, action string, paths []string, params map[string]any) *model.AppPluginJob {
	t.Helper()
	pj, _ := json.Marshal(paths)
	pb, _ := json.Marshal(params)
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: action,
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pb), Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	err := h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: paths, Dest: job.ID}, nil)
	require.NoError(t, err)
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	return got
}

// A job runs on the ops worker, long after the request that queued it: the
// root travels on the row, stamped by the door (SetJobRoot), and holds the
// job's scope when it runs.
func TestStateList_AJobStampedWithARootListsOnlyInsideIt(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.seedRunsOn(t, p, "docs/a.txt", "secret/s.txt")
	h.writeFile(t, "docs/b.txt", "b")

	stamped := map[string]any{}
	SetJobRoot(stamped, confine.Root{Adapter: "main", Rel: "docs"})
	msg := h.runJobWith(t, p, "listing", []string{"docs/b.txt"}, stamped).Message
	assert.Contains(t, msg, "main://docs/a.txt=1")
	assert.NotContains(t, msg, "secret", "a stamped job is told nothing outside its root")

	// A stamp that does not read as a root confines the job to nothing, never
	// to everything: the host wrote it, so it is a broken row. Its own input
	// is not inside that nothing either, so the job is refused before it runs
	// (filex #185) and tells nobody anything.
	broken, err := h.runStampedJob(t, p, "listing", []string{"docs/b.txt"}, map[string]any{rootParamKey: 7})
	require.Error(t, err)
	assert.Equal(t, model.AppPluginJobFailed, broken.Status)
	assert.NotContains(t, broken.Message, "docs/a.txt")
	assert.NotContains(t, broken.Message, "secret")

	// No stamp, no root.
	msg = h.runJobWith(t, p, "listing", []string{"docs/b.txt"}, map[string]any{}).Message
	assert.Contains(t, msg, "main://secret/s.txt=1")

	// Nobody but the host writes the stamp: every door strips it from what a
	// caller, a surface or a tick sends.
	assert.NotContains(t, StripHostParams(map[string]any{rootParamKey: "main://", "k": "v"}), rootParamKey)
}

func TestJobRoot_ReadsBackWhatSetJobRootWrote(t *testing.T) {
	for _, r := range []confine.Root{{Adapter: "main", Rel: ""}, {Adapter: "main", Rel: "projeler/acme"}} {
		params := map[string]any{}
		SetJobRoot(params, r)
		got, ok := JobRoot(params)
		assert.True(t, ok)
		assert.Equal(t, r, got)
	}
	_, ok := JobRoot(map[string]any{})
	assert.False(t, ok)
	got, ok := JobRoot(map[string]any{rootParamKey: ""})
	assert.True(t, ok, "a stamp that is there confines, even when it is empty")
	assert.False(t, got.Within("main", "x"), "...to nothing")

	// The page door stamps the root a link recorded (model.Share.AppRoot):
	// none for a link opened with no root, a broken one confines to nothing.
	params := map[string]any{}
	StampJobRoot(params, "")
	assert.NotContains(t, params, rootParamKey)
	StampJobRoot(params, "no storage named")
	got, ok = JobRoot(params)
	assert.True(t, ok)
	assert.False(t, got.Within("main", "no storage named"))
}

// state_list's limit is how many files the caller is TOLD about. It was
// applied to the rows read from the store, before the person's permissions
// and the token's root narrowed them, so a caller confined to a folder whose
// files sort after a few dozen others was told about none of its own.
func TestStateList_ANarrowedListIsFilledUpToItsLimit(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installEchoWithUI(t)
	ctx := context.Background()
	keep := func(rel string) {
		require.NoError(t, h.reg.opts.Store.SetAppPluginState(ctx, p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/"+rel), rel, "runs", "1"))
	}
	// 120 files outside main://kutu that sort before it, then three inside.
	for i := 0; i < 120; i++ {
		keep(fmt.Sprintf("aaa/f%03d.txt", i))
	}
	inside := []string{"main://kutu/a.txt", "main://kutu/b.txt", "main://kutu/c.txt"}
	for _, q := range inside {
		keep(strings.TrimPrefix(q, "main://"))
	}
	listed := func(ctx context.Context) []string {
		out, err := h.reg.UICall(ctx, "echo", "panel", h.st.ID, nil, nil, "en", "state_list", nil)
		require.NoError(t, err)
		var got struct {
			Paths []string `json:"paths"`
		}
		require.NoError(t, json.Unmarshal(out, &got))
		return got.Paths
	}

	// The fixture asks for 50.
	assert.Len(t, listed(context.Background()), 50, "unconfined, the first 50")
	assert.ElementsMatch(t, inside, listed(rootedCtx("main://kutu")), "a token confined to main://kutu is told about its own three")

	// The person's permissions narrow it the same way.
	h.reg.SetVisibility(func(_ context.Context, _ *model.User, _ int64, rel string) bool {
		return strings.HasPrefix(rel, "kutu/") || rel == "aaa/f119.txt"
	})
	assert.ElementsMatch(t, append([]string{"main://aaa/f119.txt"}, inside...), listed(context.Background()),
		"a person who may see four files is told about the four")
}
