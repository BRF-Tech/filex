package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// __output decides where a job's result is written and runJob reads it back
// straight from the job's params. It is the HOST's — the folder-mode ACL
// check and the output.elsewhere gate both live in the submit-time validation
// (authorise → checkOutputFolder). A `__output` inside the caller's own
// params is not that choice: it is dropped, and the action's declared output
// stands.
func TestAppPlugins_RawOutputOverrideIsIgnored(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/note.txt", "hello world")
	other, otherRoot := f.addStorage(t, "vault")

	// upper is declared `sibling` (writes {stem}-upper.txt beside the input).
	// Inject a folder override at another storage the run never asked about.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{
			"paths": []string{"main://docs/note.txt"},
			"params": map[string]any{
				"__output": map[string]any{"mode": "folder", "dir": "vault://", "name": "elsewhere.txt"},
			},
		})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))

	op := f.drain(t, ans.Op.ID)
	require.Equal(t, "ok", op["status"], op)

	// The output landed BESIDE the input, as the action declares — not in the
	// injected folder.
	_, err := os.Stat(filepath.Join(otherRoot, "elsewhere.txt"))
	assert.True(t, os.IsNotExist(err), "the injected folder override must be ignored")
	entries, _ := os.ReadDir(otherRoot)
	assert.Empty(t, entries, "nothing was written to the other storage")

	job, err := f.store.GetAppPluginJob(context.Background(), ans.JobID)
	require.NoError(t, err)
	assert.NotContains(t, job.ParamsJSON, "__output", "the reserved key never reached the job row")
	_ = other
}
