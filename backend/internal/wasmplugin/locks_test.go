package wasmplugin

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// file_lock / file_unlock through the guest, and notify_send addressed to
// one person with a deep link (to_user_id + target).
func TestLocks_LockNotifyUnlock_ThroughTheGuest(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	dbtest.SeedRegularUser(t, h.reg.opts.Store, "ada@test.local", "AdaPass1!")
	users, err := h.reg.opts.Store.ListUsers(context.Background())
	require.NoError(t, err)
	var ada int64
	for _, u := range users {
		if u.Email == "ada@test.local" {
			ada = u.ID
		}
	}
	require.NotZero(t, ada)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	h.writeFile(t, "docs/nda.txt", "x")

	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "lock",
		StorageID: h.st.ID, PathsJSON: `["docs/nda.txt"]`, ParamsJSON: `{"to":` + strconv.FormatInt(ada, 10) + `}`, Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	require.NoError(t, h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"docs/nda.txt"}, Dest: job.ID}, nil))
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	assert.True(t, strings.HasPrefix(got.Message, "locked:"), got.Message)
	assert.True(t, strings.HasSuffix(got.Message, "|notify:ok"), got.Message)

	// The lock row: this plugin, one day, the reason kept.
	ph := pathkey.Hash(h.st.ID, "/docs/nda.txt")
	l, err := h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, ph)
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.Equal(t, p.Row.ID, l.PluginID)
	assert.Equal(t, "echo", l.PluginName)
	assert.Equal(t, "under signature", l.Reason)
	assert.Equal(t, "docs/nda.txt", l.Rel)
	require.NotNil(t, l.Until)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), *l.Until, time.Minute)
	assert.True(t, l.Live(time.Now()))
	locks, err := h.reg.Locks(context.Background(), 0)
	require.NoError(t, err)
	assert.Len(t, locks, 1)

	// The notification went to ada, about the file, with the sign action to open.
	require.Len(t, nf.events, 1)
	ev := nf.events[0]
	assert.Equal(t, notify.EventPluginNotice, ev.Event)
	require.NotNil(t, ev.UserID)
	assert.Equal(t, ada, *ev.UserID)
	require.NotNil(t, ev.Node)
	assert.Equal(t, h.st.ID, ev.Node.StorageID)
	assert.Equal(t, "/docs/nda.txt", ev.Node.Path)
	require.NotNil(t, ev.Target)
	assert.Equal(t, notify.TargetFile, ev.Target.Kind)
	assert.Equal(t, "docs/nda.txt", ev.Target.Path)
	require.NotNil(t, ev.Target.Open)
	assert.Equal(t, "echo", ev.Target.Open.Plugin)
	assert.Equal(t, "sign", ev.Target.Open.Action)

	// Unlock from a job of the same plugin.
	ujob, err := h.runJob(t, p, "unlock", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, ujob.Status, ujob.Error)
	l, err = h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, ph)
	require.NoError(t, err)
	assert.Nil(t, l)

	// A lock held by another plugin cannot be lifted by this one, and a
	// second lock on it is refused as busy.
	other := &model.AppPluginLock{StorageID: h.st.ID, PathHash: ph, Rel: "docs/nda.txt", PluginID: p.Row.ID + 100, PluginName: "someone-else"}
	require.NoError(t, h.reg.opts.Store.PutAppPluginLock(context.Background(), other))
	ujob, err = h.runJob(t, p, "unlock", []string{"docs/nda.txt"}, "en")
	require.Error(t, err)
	assert.Contains(t, ujob.Error, "someone-else")
	ljob, err := h.runJob(t, p, "lock", []string{"docs/nda.txt"}, "en")
	require.Error(t, err)
	assert.Contains(t, ljob.Error, "busy")

	// The administrator's override lifts anything; a forgotten lock expires.
	was, err := h.reg.Unlock(context.Background(), h.st.ID, "docs/nda.txt")
	require.NoError(t, err)
	assert.True(t, was)
	was, err = h.reg.Unlock(context.Background(), h.st.ID, "docs/nda.txt")
	require.NoError(t, err)
	assert.False(t, was)
	past := time.Now().Add(-time.Minute)
	require.NoError(t, h.reg.opts.Store.PutAppPluginLock(context.Background(), &model.AppPluginLock{StorageID: h.st.ID, PathHash: ph, Rel: "docs/nda.txt", PluginID: p.Row.ID, PluginName: "echo", Until: &past}))
	n, err := h.reg.opts.Store.DeleteExpiredAppPluginLocks(context.Background(), time.Now())
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	// A view may not lock (not writable).
	_, err = h.reg.ViewEvent(context.Background(), "echo", "hello", h.st.ID, []string{"docs/nda.txt"}, nil, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	// Removing the plugin takes its locks along.
	require.NoError(t, h.reg.opts.Store.PutAppPluginLock(context.Background(), &model.AppPluginLock{StorageID: h.st.ID, PathHash: ph, Rel: "docs/nda.txt", PluginID: p.Row.ID, PluginName: "echo"}))
	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	locks, err = h.reg.opts.Store.ListAppPluginLocks(context.Background(), h.st.ID)
	require.NoError(t, err)
	assert.Empty(t, locks)
}

// State keys reach the listing as <plugin>:<key>, and a job of the plugin
// sees the manifest output replaced when the surface asked for it.
func TestStateKeys_ListedPerFile_AndOutputOverride(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/a.txt", "hello")
	h.writeFile(t, "docs/b.txt", "other")
	job, err := h.runJob(t, p, "upper", []string{"docs/a.txt"}, "en")
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, job.Status)

	ha, hb := pathkey.Hash(h.st.ID, "/docs/a.txt"), pathkey.Hash(h.st.ID, "/docs/b.txt")
	keys, err := h.reg.opts.Store.ListAppPluginStateKeys(context.Background(), h.st.ID, []string{ha, hb})
	require.NoError(t, err)
	assert.Equal(t, []string{"echo:runs"}, keys[ha])
	assert.Empty(t, keys[hb])

	// Output override: version mode writes back into the input instead of a sibling.
	ov := map[string]any{}
	SetOutputOverride(ov, &wire.Output{Mode: "version"})
	require.NotNil(t, OutputOverride(ov))
	assert.Equal(t, "version", OutputOverride(ov).Mode)
	assert.Nil(t, OutputOverride(map[string]any{outputOverrideKey: map[string]any{"mode": "elsewhere"}}), "an unknown mode is ignored")

	pj := `["docs/b.txt"]`
	job = &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "upper",
		StorageID: h.st.ID, PathsJSON: pj, ParamsJSON: `{"__output":{"mode":"version"}}`, Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	require.NoError(t, h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 2, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"docs/b.txt"}, Dest: job.ID}, nil))
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	assert.Contains(t, got.OutputsJSON, `"docs/b.txt"`, "the output is the input itself")
	assert.NotContains(t, got.OutputsJSON, "b-upper.txt")
	assert.Equal(t, []string{"docs/b.txt"}, h.sink.versions)
	data, err := h.drv.Read(context.Background(), "docs/b.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(data)
	data.Close()
	assert.Equal(t, "OTHER", string(b))
}
