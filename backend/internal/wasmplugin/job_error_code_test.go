package wasmplugin

// A failed app job is classified for the client (ops row `error_code`), which
// says it in the person's language; the job's own English text stays for an
// administrator's second line.
//
// ⚠ QA, 2026-09-21: the operations centre printed "engine libreoffice is not
// installed on this host" to whoever ran a conversion.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestClassifyJobError(t *testing.T) {
	for _, tc := range []struct {
		status, text, code, engine string
	}{
		{model.AppPluginJobFailed, engineMissingMessage("libreoffice"), "engine_missing", "libreoffice"},
		// An app usually passes the host's words on inside its own.
		{model.AppPluginJobFailed, "convert: " + engineMissingMessage("ffmpeg") + " (docx → pdf)", "engine_missing", "ffmpeg"},
		{model.AppPluginJobFailed, msgJobTimeout, "timeout", ""},
		{model.AppPluginJobFailed, msgJobOOM, "out_of_memory", ""},
		{model.AppPluginJobFailed, msgJobTrap, "crashed", ""},
		{model.AppPluginJobFailed, msgJobPluginRemoved, "app_removed", ""},
		{model.AppPluginJobFailed, msgJobActionRemoved, "action_removed", ""},
		{model.AppPluginJobCancelled, msgJobCancelled, "cancelled", ""},
		{model.AppPluginJobFailed, "Bu belge zaten imzalanmış", "app", ""},
		{model.AppPluginJobFailed, "", "", ""},
	} {
		code, engine := classifyJobError(tc.status, tc.text)
		assert.Equal(t, tc.code, code, "%q", tc.text)
		assert.Equal(t, tc.engine, engine, "%q", tc.text)
	}
}

func TestDecorateOps_FailedJobCarriesItsCode(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reg, err := New(Options{Store: store, Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(ctx) })

	failed, ok := int64(41), int64(42)
	require.NoError(t, store.CreateAppPluginJob(ctx, &model.AppPluginJob{
		ID: "job-failed", OpID: &failed, PluginName: "convert", ActionID: "convert",
		Status: model.AppPluginJobFailed, Error: engineMissingMessage("libreoffice"),
	}))
	require.NoError(t, store.CreateAppPluginJob(ctx, &model.AppPluginJob{
		ID: "job-ok", OpID: &ok, PluginName: "convert", ActionID: "convert", Status: model.AppPluginJobOK,
	}))
	rows := []*ops.Op{
		{ID: failed, Kind: ops.OpPluginAction, Status: "failed"},
		{ID: ok, Kind: ops.OpPluginAction, Status: "done"},
	}
	reg.DecorateOps(ctx, rows)
	assert.Equal(t, "engine_missing", rows[0].ErrorCode)
	assert.Equal(t, "libreoffice", rows[0].ErrorEngine)
	assert.Empty(t, rows[1].ErrorCode, "a job that did not fail has nothing to say")
}
