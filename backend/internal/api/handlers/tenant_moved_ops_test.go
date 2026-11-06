package handlers_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// An operation queued by alpha's member and still waiting when the storage
// went to bravo does not run into it.
func TestTenantMoved_AQueuedOperationDoesNotRunIntoIt(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "kendi.txt", "alpha's own file")
	require.NoError(t, os.MkdirAll(filepath.Join(f.RootA, "hedef"), 0o755))
	copyIt := func() map[string]any {
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
			"source": []string{"alpha://kendi.txt"}, "target": "alpha://hedef",
		})
		require.Equal(t, http.StatusAccepted, status, "%v", body)
		return body
	}
	lastStatus := func() (string, string) {
		list, err := f.Ops.List(context.Background(), "")
		require.NoError(t, err)
		require.NotEmpty(t, list)
		var last *ops.Op
		for _, op := range list {
			if last == nil || op.ID > last.ID {
				last = op
			}
		}
		return last.Status, last.Error
	}

	copyIt()
	f.moveStorage(t, f.StA.ID, f.ProvA, f.ProvB)
	f.drainOps(t)
	status, why := lastStatus()
	assert.Equal(t, ops.StatusFailed, status)
	assert.Contains(t, why, "no longer in the tenant")
	_, err := os.Stat(filepath.Join(f.RootA, "hedef", "kendi.txt"))
	assert.True(t, os.IsNotExist(err), "nothing copied into bravo's storage")

	// Control: the storage back with alpha, the same copy runs.
	f.moveStorage(t, f.StA.ID, f.ProvB, f.ProvA)
	copyIt()
	f.drainOps(t)
	status, why = lastStatus()
	require.Equal(t, ops.StatusOK, status, why)
	_, err = os.Stat(filepath.Join(f.RootA, "hedef", "kendi.txt"))
	assert.NoError(t, err)
}
