package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/fakemagick"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// A Thumbnail repair on an install whose ImageMagick cannot decode HEVC
// (Ubuntu 24.04 without libheif-plugin-libde265), the way the 0.50 test phase
// ran it: every HEIC of the thumbnails scene came back "failed". It is a
// missing program, not a broken file: the repair counts the HEIC as skipped,
// and its row says what to install. A row an earlier run left `failed` with
// ImageMagick's "Unsupported codec" is turned into that skip by the same
// repair (Fix retries failed rows).
func TestRepair_HEICWithoutADecoderIsSkippedNotFailed(t *testing.T) {
	t.Cleanup(enginebin.SetForTest(map[string]string{enginebin.ImageMagick: fakemagick.NoHEVC(t)}))
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	synced := time.Now().Add(-time.Hour)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "fotolar", Driver: "local", MountPath: "/fotolar", Enabled: true, LastSyncAt: &synced,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`),
	})
	require.NoError(t, err)
	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true, SVG: true, HEIF: true})
	pipe.AttachStorage(st.ID, drv)
	s := &Server{store: store, pipeline: pipe, resolver: func(int64) (storage.Driver, error) { return drv, nil }}

	// A phone photo's head: an ISO box of brand heic.
	heic := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0, 'm', 'i', 'f', '1', 'h', 'e', 'i', 'c'}, make([]byte, 64)...)
	file := func(name string, body []byte) *model.Node {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), body, 0o644))
		mt := time.Now().UTC().Truncate(time.Millisecond)
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: name, Path: "/" + name,
			PathHash: pathkey.Hash(st.ID, "/"+name), Type: model.NodeTypeFile, Size: int64(len(body)),
			Mime: "application/octet-stream", BackendMtime: &mt})
		require.NoError(t, err)
		return n
	}
	fresh := file("IMG_0001.heic", heic)
	old := file("IMG_0002.heic", heic)
	photo := file("deniz.png", pngOf(t))
	// What 0.50 recorded for it before the probe, a day ago.
	dayAgo := time.Now().Add(-24 * time.Hour)
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{
		NodeID: old.ID, State: "failed", SourceSig: thumb.SourceSig(old), AttemptedAt: &dayAgo,
		Error: "thumb: heif: exit status 1 (convert-im6.q16: Unsupported feature: Unsupported codec (4.3000))",
	}))

	counts, err := thumbRepairer{s: s}.RunRepair(ctx, ops.ThumbRepairJob{StorageID: st.ID, Mode: ops.ThumbRepairFix}, func(ops.ThumbRepairCounts) {})
	require.NoError(t, err)
	require.Equal(t, 0, counts.Failed, "a HEIC this ImageMagick cannot decode is not a failed file")
	require.Equal(t, 2, counts.Skipped)
	require.Equal(t, 1, counts.OK, "the PNG")

	for _, n := range []*model.Node{fresh, old} {
		row, err := store.GetThumbnail(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, "skipped", row.State, n.Name)
		require.Equal(t, "no_tool:heic_codec", row.Error, n.Name)
	}
	row, err := store.GetThumbnail(ctx, photo.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
}
