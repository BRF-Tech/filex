package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func TestJobText_KeepsEveryLanguage_ReadsOldRowsAsTheyWere(t *testing.T) {
	both := EncodeJobText(wire.Text{"en": "3 files converted", "tr": "3 dosya dönüştürüldü"})
	assert.Equal(t, "3 dosya dönüştürüldü", JobText(both, "tr"))
	assert.Equal(t, "3 files converted", JobText(both, "en"))
	assert.Equal(t, "3 files converted", JobText(both, "de"), "a language the app did not write falls back to English")

	// One language, or the same words in each: a plain column, as before.
	assert.Equal(t, "ok", EncodeJobText(wire.Text{"en": "ok"}))
	assert.Equal(t, "applied", EncodeJobText(wire.Text{"en": "applied", "tr": "applied"}))
	assert.Equal(t, "", EncodeJobText(nil))

	// A row written before this — and a progress line — reads as it is.
	for _, old := range []string{"done", "{stem}.pdf is ready", "i18n:not json", ""} {
		assert.Equal(t, old, JobText(old, "tr"))
	}
}

// DecorateOps says the row in the language on the READER's screen.
func TestDecorateOps_SaysTheJobInTheReadersLanguage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reg, err := New(Options{Store: store, Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(ctx) })

	okID, failedID, oldID := int64(51), int64(52), int64(53)
	label := EncodeJobText(wire.Text{"en": "Convert…", "tr": "Dönüştür…"})
	require.NoError(t, store.CreateAppPluginJob(ctx, &model.AppPluginJob{
		ID: "job-ok", OpID: &okID, PluginName: "convert", ActionID: "convert", Label: label,
		Status: model.AppPluginJobOK, Locale: "en",
		Message: EncodeJobText(wire.Text{"en": "1 file converted", "tr": "1 dosya dönüştürüldü"}),
	}))
	require.NoError(t, store.CreateAppPluginJob(ctx, &model.AppPluginJob{
		ID: "job-failed", OpID: &failedID, PluginName: "convert", ActionID: "convert", Label: label,
		Status: model.AppPluginJobFailed, Locale: "en",
		Error:   "nothing converted — kirik.png: conversion failed",
		Message: EncodeJobText(wire.Text{"en": "nothing converted — kirik.png: conversion failed", "tr": "hiçbir şey dönüştürülmedi — kirik.png: dönüştürme başarısız"}),
	}))
	require.NoError(t, store.CreateAppPluginJob(ctx, &model.AppPluginJob{
		ID: "job-old", OpID: &oldID, PluginName: "convert", ActionID: "convert", Label: "Convert…",
		Status: model.AppPluginJobOK, Message: "done",
	}))
	rows := func() []*ops.Op {
		return []*ops.Op{
			{ID: okID, Kind: ops.OpPluginAction, Status: "ok"},
			{ID: failedID, Kind: ops.OpPluginAction, Status: "failed", Error: "nothing converted — kirik.png: conversion failed"},
			{ID: oldID, Kind: ops.OpPluginAction, Status: "ok"},
		}
	}

	tr := rows()
	reg.DecorateOps(srvtext.WithReader(ctx, "tr"), tr)
	assert.Equal(t, "Dönüştür…", tr[0].Label)
	assert.Equal(t, "1 dosya dönüştürüldü", tr[0].Message)
	assert.Equal(t, "app", tr[1].ErrorCode)
	assert.Equal(t, "hiçbir şey dönüştürülmedi — kirik.png: dönüştürme başarısız", tr[1].Error,
		"the app said why in Turkish too; the Turkish tray reads that")
	assert.Equal(t, "Convert…", tr[2].Label, "a row written before this reads as it was")
	assert.Equal(t, "done", tr[2].Message)

	en := rows()
	reg.DecorateOps(srvtext.WithReader(ctx, "en"), en)
	assert.Equal(t, "Convert…", en[0].Label)
	assert.Equal(t, "1 file converted", en[0].Message)
	assert.Equal(t, "nothing converted — kirik.png: conversion failed", en[1].Error)

	// A background read (no reader) is English, as the job row used to be.
	none := rows()
	reg.DecorateOps(ctx, none)
	assert.Equal(t, "Convert…", none[0].Label)
}
