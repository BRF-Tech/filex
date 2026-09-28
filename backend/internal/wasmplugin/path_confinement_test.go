package wasmplugin

// "A plugin never names a storage path" (scope.go): wherever an app may pass
// a path it CHOSE — file_lock / file_unlock, notify_send's target, a page
// link's document, a scheduled item — the path is accepted only for a file
// the call was handed (an input) or one the app already keeps state on, and
// a refusal says the same thing whether or not the file exists.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func jsonArg(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func (h *harness) lockAt(t *testing.T, rel string) *model.AppPluginLock {
	t.Helper()
	l, _ := h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, pathkey.Hash(h.st.ID, "/"+rel))
	return l
}

func TestPathForm_FileLockReachesOnlyHandedFiles(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	h.writeFile(t, "inbox/contract.txt", "the terms")
	h.writeFile(t, "private/salaries.txt", "not yours")
	h.writeFile(t, "archive/old.txt", "recorded earlier")
	s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")
	s.storageName = "main"
	lock := func(path string) error {
		_, err := hfFileLock(ctx, s, jsonArg(t, map[string]any{"path": path, "ttl_days": 1}))
		return err
	}

	// A file nobody handed this job: refused, nothing frozen.
	assert.Equal(t, errPathNotHanded, lock("main://private/salaries.txt"))
	assert.Nil(t, h.lockAt(t, "private/salaries.txt"), "the file must not be frozen for everybody")
	// …and the answer does not say whether the file exists.
	assert.Equal(t, errPathNotHanded, lock("main://private/no-such-file.txt"))
	assert.Equal(t, errPathNotHanded, lock("private/salaries.txt"), "a bare path is the same path")
	assert.Equal(t, errPathNotHanded, lock("main://inbox/../private/salaries.txt"), "cleaned before it is judged")

	// The job's own input, by path: allowed, as by ref.
	require.NoError(t, lock("main://inbox/contract.txt"))
	assert.NotNil(t, h.lockAt(t, "inbox/contract.txt"))

	// A file this app keeps state on — handed to it by an earlier job.
	h.ownFile(t, p, "archive/old.txt")
	require.NoError(t, lock("main://archive/old.txt"))
	assert.NotNil(t, h.lockAt(t, "archive/old.txt"))
}

func TestPathForm_FileUnlockIsNoProbeOfOtherFiles(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	store := h.reg.opts.Store
	other, err := store.CreateAppPlugin(ctx, &model.AppPlugin{Name: "other", Version: "1", LabelJSON: `{"en":"Other"}`,
		ManifestJSON: `{}`, WasmPath: "x", SHA256: "y", Source: model.AppPluginSourceUpload, PermissionsJSON: `[]`, Enabled: true})
	require.NoError(t, err)
	put := func(rel string, pluginID int64, name string) {
		require.NoError(t, store.PutAppPluginLock(ctx, &model.AppPluginLock{StorageID: h.st.ID,
			PathHash: pathkey.Hash(h.st.ID, "/"+rel), Rel: rel, PluginID: pluginID, PluginName: name}))
	}
	put("private/theirs.txt", other.ID, "other")
	put("private/mine.txt", p.Row.ID, p.Row.Name)

	s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")
	s.storageName = "main"
	unlock := func(path string) (map[string]any, error) {
		out, err := hfFileUnlock(ctx, s, jsonArg(t, map[string]any{"path": path}))
		if err != nil {
			return nil, err
		}
		return out.(map[string]any), nil
	}

	// Another app's lock on a file this call was not handed, and no lock at
	// all on another such file, answer the SAME: neither the lock nor the
	// other app's name is told.
	_, errTheirs := unlock("main://private/theirs.txt")
	_, errNone := unlock("main://private/nothing.txt")
	assert.Equal(t, errPathNotHanded, errTheirs)
	assert.Equal(t, errPathNotHanded, errNone)
	assert.NotNil(t, h.lockAt(t, "private/theirs.txt"), "another app's lock stays")

	// Its OWN lock, the app may lift by path wherever the file is.
	out, err := unlock("main://private/mine.txt")
	require.NoError(t, err)
	assert.Equal(t, true, out["was_locked"])
	assert.Nil(t, h.lockAt(t, "private/mine.txt"))
}

func TestPathForm_NoticeTargetsOnlyHandedFiles(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")
	s.storageName = "main"
	send := func(path string) error {
		_, err := hfNotifySend(context.Background(), s, jsonArg(t, map[string]any{
			"title": map[string]string{"en": "look"}, "target": map[string]any{"path": path},
		}))
		return err
	}
	assert.Equal(t, errPathNotHanded, send("main://private/salaries.txt"))
	assert.Empty(t, nf.events)
	require.NoError(t, send("main://inbox/contract.txt"))
	require.Len(t, nf.events, 1)
}

func TestShareCreate_APageLinkAnchorsOnlyOnAFileTheCallWasHanded(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	h.writeFile(t, "inbox/contract.txt", "the terms")
	h.writeFile(t, "private/salaries.txt", "not yours")
	h.writeFile(t, "archive/old.txt", "recorded earlier")
	for _, rel := range []string{"inbox/contract.txt", "private/salaries.txt", "archive/old.txt"} {
		_, err := h.store.CreateNode(ctx, &model.Node{StorageID: h.st.ID, Name: rel, Path: "/" + rel,
			PathHash: pathkey.Hash(h.st.ID, "/"+rel), Type: model.NodeTypeFile, SyncState: model.SyncStateSynced})
		require.NoError(t, err)
	}
	s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")

	// A catalogued file the job was not handed: the link used to be minted on
	// it, and a visitor's state writes then landed on somebody else's file.
	_, err := shareCreate(t, s, map[string]any{"page_id": "signer", "path": "private/salaries.txt"})
	assert.Equal(t, errPathNotHanded, err)
	node, err := h.store.GetNodeByPath(ctx, h.st.ID, pathkey.Hash(h.st.ID, "/private/salaries.txt"))
	require.NoError(t, err)
	shares, err := h.store.ListSharesByNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Empty(t, shares, "no link hangs on the file")

	// The job's input, and a file the app keeps state on, are fine.
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "path": "inbox/contract.txt"})
	require.NoError(t, err)
	h.ownFile(t, p, "archive/old.txt")
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "path": "archive/old.txt"})
	require.NoError(t, err)
}

func TestSchedule_AWakeUpSchedulesOnlyFilesTheAppKeepsStateOn(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	h.writeFile(t, "private/salaries.txt", "not yours")
	// The tick names a file nobody ever handed the app — installScheduled
	// records state only for a main:// tick_path it is GIVEN, so point it
	// elsewhere after the install.
	p, q := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
	require.NoError(t, h.reg.PutSettings(context.Background(), p.Row.ID, map[string]string{
		"tick_mode": "due", "tick_path": "main://private/salaries.txt", "tick_due_in_s": "600",
	}))

	h.pass(t, tickAt)
	assert.Nil(t, h.row(t, p, "due"), "work on a file the app was never handed is not scheduled")
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "keeps state on", "the refusal is said where the admin reads it")
	assert.Equal(t, 0, h.pass(t, tickAt.Add(10*time.Minute)))
	assert.Empty(t, q.all(), "nothing reached the queue")
}

func TestSchedule_AnItemWhoseStateIsGoneByItsTimeDoesNotRun(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))

	// Between the wake-up and the due time the app's record of the file is
	// removed: what made the file the app's business is gone.
	require.NoError(t, h.reg.opts.Store.DeleteAppPluginState(ctx, p.Row.ID, h.st.ID,
		pathkey.Hash(h.st.ID, "/docs/contract.txt"), "tracked"))
	h.pass(t, tickAt.Add(10*time.Minute))
	assert.Empty(t, q.all(), "the item did not become a job")
	item := h.row(t, p, "due")
	require.NotNil(t, item)
	assert.Equal(t, model.AppPluginScheduleSkipped, item.Status)
}
