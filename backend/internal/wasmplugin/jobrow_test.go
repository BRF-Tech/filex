package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Writing the ops-row id onto a job must not touch anything else.
//
// The handler queues a job and then records which ops row it became. The
// worker is awake by then and a short action can already have finished, so
// a whole-row write from the handler's stale copy erases the status, the
// message and the outputs the worker just wrote — leaving an ops row that
// says "ok" with nothing to show, which is how a plugin's public link and
// PIN went missing.
func TestSetAppPluginJobOp_TouchesOnlyTheOpID(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	p := h.install(t)

	job := &model.AppPluginJob{
		ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "upper",
		StorageID: h.st.ID, PathsJSON: `["a.txt"]`, ParamsJSON: "{}", Locale: "en",
		Label: "Upper-case", Status: model.AppPluginJobPending,
	}
	require.NoError(t, h.store.CreateAppPluginJob(ctx, job))

	// The worker gets there first and writes the result.
	done := *job
	done.Status = model.AppPluginJobOK
	done.Message = "here is the link and its PIN"
	done.OutputsJSON = `[{"path":"a-upper.txt"}]`
	require.NoError(t, h.reg.opts.Store.UpdateAppPluginJob(ctx, &done))

	// Now the handler records the ops row, from its own older copy.
	require.NoError(t, h.reg.opts.Store.SetAppPluginJobOp(ctx, job.ID, 4242))

	got, err := h.store.GetAppPluginJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, got.OpID)
	assert.EqualValues(t, 4242, *got.OpID)
	assert.Equal(t, model.AppPluginJobOK, got.Status, "the worker's status survives")
	assert.Equal(t, "here is the link and its PIN", got.Message, "the worker's message survives")
	assert.Contains(t, got.OutputsJSON, "a-upper.txt", "the worker's outputs survive")
}
