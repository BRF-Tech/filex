package handlers_test

// Where a job's result lands, measured on the job row both doors write.
//
// A surface may override the action's manifest output for one job
// (`wire.JobRequest.Output`, APP-PLUGINS-API.md): "a new version of this file"
// or "a new file beside it, called X". The host stashes that choice in the
// job's params under `__output`, and the worker reads it back there — so the
// params ARE the decision, and a path that forgets to stamp them silently
// runs the manifest's mode instead.
//
// ⚠ The fixture app's public page asks for no override of its own, so the
// link-side test below pins the no-override half end to end; the override
// itself is pinned on the authenticated door here and, for both doors at once,
// in plugin_job_output_test.go (jobOutputMode / applyJobOutput are the only
// reading either path has).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// jobParams reads the params a queued job row carries.
func (f *appFixture) jobParams(t *testing.T, jobID string) map[string]any {
	t.Helper()
	job, err := f.store.GetAppPluginJob(context.Background(), jobID)
	require.NoError(t, err)
	require.NotNil(t, job)
	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(job.ParamsJSON), &p))
	return p
}

// A submit from INSIDE filex carries the choice the wizard made.
func TestPluginViewJob_CarriesTheSurfacesOutputIntoTheJobRow(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/nda.txt", "draft")

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"paths": []string{"main://docs/nda.txt"}, "event": "submit",
			"data": map[string]any{"output_mode": "version"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	require.NotEmpty(t, ans.JobID)

	out, _ := f.jobParams(t, ans.JobID)["__output"].(map[string]any)
	require.NotNil(t, out, "the surface chose a mode and the job row must carry it")
	assert.Equal(t, "version", out["mode"],
		"the action's manifest says `sibling`; the surface's choice is what runs")
	assert.Equal(t, "custom-{stem}{ext}", out["name"], "and the name the surface asked for travels with it")
}

// A page job whose surface asked for nothing leaves the manifest's output
// alone — an empty override would be a different claim, not a missing one.
func TestPublicPageJob_WithoutAnOverrideKeepsTheManifestsOutput(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	ctx := context.Background()

	f.writeFile(t, "docs/terms.txt", "the terms")
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "terms.txt", Path: "/docs/terms.txt",
		PathHash: pathkey.Hash(f.st.ID, "/docs/terms.txt"), Type: model.NodeTypeFile, Size: 9, Mime: "text/plain",
	})
	require.NoError(t, err)

	params := f.jobParams(t, f.submitAsVisitor(t, f.openSigningLink(t, id, "docs/terms.txt")))

	assert.NotContains(t, params, "__output",
		"no override means the manifest's own output mode stands")
	// The parameters the page path owes the job are still all there: which
	// link, which visitor, and the plugin's own.
	assert.NotNil(t, params["share_id"])
	assert.NotEmpty(t, params["page_token_hash"])
	assert.Equal(t, true, params["from_page"])
}
