package handlers_test

// A job whose result goes into a folder the person chose carries that folder
// to the worker, and the worker writes there later. The folder is pinned to
// the storage it was checked on, by id: a storage deleted and created again
// under the same name while the job waited is another storage and takes no
// result, and a storage renamed while it waited keeps it.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueFolderJob installs the echo app with an "upper" action whose result may
// go elsewhere, adds the storage `dest` with an `out` folder, and queues one
// job whose result goes to dest://out. The job is queued, not run: the fixture
// runs its worker only in drain.
func queueFolderJob(t *testing.T) (f *appFixture, destID int64, destRoot string, op int64) {
	t.Helper()
	f = newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["view"] = "hello"
				a["output"] = map[string]any{"mode": "sibling", "name": "{stem}-upper{ext}", "elsewhere": true}
			}
		}
	})
	dest, root := f.addStorage(t, "dest")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "out"), 0o755))
	f.writeFile(t, "docs/a.txt", "hello")
	status, raw := f.helloSubmit(t, f.admin, "main://docs/a.txt", map[string]any{"output_mode": "folder", "output_dir": "dest://out"})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	return f, dest.ID, root, opID(t, raw)
}

func TestAppJobOutput_AStorageReplacedUnderTheSameNameTakesNoResult(t *testing.T) {
	f, destID, oldRoot, op := queueFolderJob(t)

	require.NoError(t, f.store.DeleteStorage(context.Background(), destID))
	_, newRoot := f.addStorage(t, "dest")
	require.NoError(t, os.MkdirAll(filepath.Join(newRoot, "out"), 0o755))

	got := f.drain(t, op)
	assert.NotEqual(t, "ok", got["status"], "the job's folder was on a storage that is gone: %v", got)
	_, err := os.Stat(filepath.Join(newRoot, "out", "a-upper.txt"))
	assert.True(t, os.IsNotExist(err), "nothing in the storage that took the name")
	_, err = os.Stat(filepath.Join(oldRoot, "out", "a-upper.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestAppJobOutput_ARenamedStorageKeepsTheResult(t *testing.T) {
	f, destID, root, op := queueFolderJob(t)

	ctx := context.Background()
	cur, err := f.store.GetStorage(ctx, destID)
	require.NoError(t, err)
	cur.Name = "dest-renamed"
	require.NoError(t, f.store.UpdateStorage(ctx, cur))

	got := f.drain(t, op)
	require.Equal(t, "ok", got["status"], "%v", got)
	b, err := os.ReadFile(filepath.Join(root, "out", "a-upper.txt"))
	require.NoError(t, err, "the result is in the chosen folder of the renamed storage")
	assert.Equal(t, "HELLO", string(b))
	assert.Contains(t, jsonString(got["outputs"]), "dest-renamed://out/a-upper.txt", "named as the storage reads now")
}
