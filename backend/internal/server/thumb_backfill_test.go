package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// `filex thumb backfill` renders thumbnails for catalogue rows. Against a
// storage nobody had synced it walked an empty catalogue and printed
// `{processed: 0, ok: 0, failed: 0, skipped: 0}` with exit 0 (measured
// 2026-09-14) — the way a screenshot run ended with a grid of generic icons
// while every step said ok. These pin the rule that now refuses instead.

type fakeCatalogue struct {
	run   *model.SyncRun
	files int64
}

func (f fakeCatalogue) GetLastSyncRun(context.Context, int64) (*model.SyncRun, error) {
	if f.run == nil {
		return nil, sql.ErrNoRows
	}
	return f.run, nil
}

func (f fakeCatalogue) StorageStats(context.Context, int64) (int64, int64, error) {
	return f.files, 0, nil
}

type fakeBackend struct{ root []string }

func (fakeBackend) Init(context.Context, map[string]any) error { return nil }
func (fakeBackend) Name() string                               { return "fake" }
func (b fakeBackend) List(context.Context, string) ([]storage.Object, error) {
	out := make([]storage.Object, 0, len(b.root))
	for _, n := range b.root {
		out = append(out, storage.Object{Name: n, Path: "/" + n})
	}
	return out, nil
}
func (fakeBackend) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, errors.New("unused")
}
func (fakeBackend) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (fakeBackend) Capabilities() storage.Capabilities { return storage.Capabilities{} }

func TestCatalogueGap(t *testing.T) {
	ctx := context.Background()
	synced := time.Now().Add(-time.Hour)
	never := &model.Storage{ID: 7, Name: "unsynced"}
	done := &model.Storage{ID: 8, Name: "My files", LastSyncAt: &synced}

	t.Run("never synced, files on the backend, none in the catalogue: refused", func(t *testing.T) {
		reason := catalogueGap(ctx, fakeCatalogue{}, never, fakeBackend{root: []string{"Pics"}})
		assert.Contains(t, reason, "never been synced")
		assert.Contains(t, reason, "/api/admin/storages/7/sync", "the way out has to be in the message")
	})

	t.Run("a sync still running: refused, whatever the catalogue holds", func(t *testing.T) {
		run := &model.SyncRun{Status: "running", StartedAt: time.Date(2026, 9, 14, 3, 30, 0, 0, time.UTC)}
		reason := catalogueGap(ctx, fakeCatalogue{run: run, files: 22}, done, fakeBackend{root: []string{"Photos"}})
		assert.Contains(t, reason, "still running")
		assert.Contains(t, reason, "2026-09-14 03:30:00")
	})

	t.Run("synced and finished: allowed", func(t *testing.T) {
		run := &model.SyncRun{Status: "ok"}
		assert.Empty(t, catalogueGap(ctx, fakeCatalogue{run: run, files: 3}, done, fakeBackend{root: []string{"Photos"}}))
	})

	t.Run("never synced but filled through uploads: allowed", func(t *testing.T) {
		assert.Empty(t, catalogueGap(ctx, fakeCatalogue{files: 5}, never, fakeBackend{root: []string{"Pics"}}))
	})

	t.Run("an empty storage: nothing to do is the truth", func(t *testing.T) {
		assert.Empty(t, catalogueGap(ctx, fakeCatalogue{}, never, fakeBackend{root: []string{".filex-trash", ".thumbs"}}))
		assert.Empty(t, catalogueGap(ctx, fakeCatalogue{}, never, fakeBackend{}))
	})
}
