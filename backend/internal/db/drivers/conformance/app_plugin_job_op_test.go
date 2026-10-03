package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The handler queues an app's job and then records which ops row it became
// (SetAppPluginJobOp). The worker is awake by then: it can read the job
// BEFORE that write and update it AFTER, from a copy with no op id. Until
// 0.50 that update wrote op_id=NULL, the job lost its ops row for good, and
// the queue drew a nameless "plugin-action" with no message, outputs or
// "open" link. (The other order, the worker finishing first, is
// wasmplugin's TestSetAppPluginJobOp_TouchesOnlyTheOpID.)
func TestUpdateAppPluginJob_KeepsAnOpIDItNeverSaw(t *testing.T) {
	each(t, keepsAnOpIDItNeverSaw)
}

func keepsAnOpIDItNeverSaw(t *testing.T, _ *sql.DB, store db.Store, _ string) {
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "jobop-" + suffix, Driver: "local", MountPath: "/j", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	p, err := store.CreateAppPlugin(ctx, &model.AppPlugin{
		Name: "jobop-" + suffix, Version: "1", LabelJSON: `{"en":"Job op"}`, ManifestJSON: `{}`,
		WasmPath: "x", SHA256: "y", Source: model.AppPluginSourceUpload, PermissionsJSON: `[]`, Enabled: true,
	})
	require.NoError(t, err)
	job := &model.AppPluginJob{
		ID: "job-" + suffix, PluginID: p.ID, PluginName: p.Name, ActionID: "sign",
		StorageID: st.ID, PathsJSON: `["a.txt"]`, ParamsJSON: "{}", Locale: "en",
		Label: "Sign", Status: model.AppPluginJobPending,
	}
	require.NoError(t, store.CreateAppPluginJob(ctx, job))

	// The worker reads the job before the handler has recorded its ops row…
	stale, err := store.GetAppPluginJob(ctx, job.ID)
	require.NoError(t, err)
	require.Nil(t, stale.OpID)
	// …the handler records it…
	const opID = 4242
	require.NoError(t, store.SetAppPluginJobOp(ctx, job.ID, opID))
	// …and the worker writes what it learned, from the copy it read.
	stale.Status = model.AppPluginJobOK
	stale.Message = "signed"
	require.NoError(t, store.UpdateAppPluginJob(ctx, stale))

	got, err := store.GetAppPluginJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, got.OpID, "the ops row the handler recorded survives the worker's write")
	require.EqualValues(t, opID, *got.OpID)
	require.Equal(t, model.AppPluginJobOK, got.Status, "the worker's own write still lands")
	require.Equal(t, "signed", got.Message)

	byOp, err := store.ListAppPluginJobsByOp(ctx, []int64{opID})
	require.NoError(t, err)
	require.NotNil(t, byOp[opID], "the queue can still name the job behind its row")
	require.Equal(t, "sign", byOp[opID].ActionID)
}
