package handlers_test

// The marker on a file that has no thumbnail (0.50, thumb.NoteOf): a listing
// says why when the reason is the file's own - damaged, encrypted, too large
// - and says nothing for a reason that may pass, a missing program, or a file
// that has its picture.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

func TestListing_ThumbNoteSaysWhyTheFileHasNone(t *testing.T) {
	ctx := context.Background()
	cacheDir := t.TempDir()
	srv, client, store := testutil.NewTestServerWith(t,
		func(c *config.Config) { c.Thumbs.CacheDir = cacheDir },
		func(d *api.Deps) {
			st := d.Store
			d.StorageResolver = func(id int64) (storage.Driver, error) {
				row, err := st.GetStorage(context.Background(), id)
				if err != nil || row == nil {
					return nil, fmt.Errorf("unknown storage %d", id)
				}
				var cfg map[string]any
				if err := json.Unmarshal(row.ConfigJSON, &cfg); err != nil {
					return nil, err
				}
				drv := &local.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			d.Thumbs = thumb.New(d.Store, cacheDir, thumb.Capabilities{Image: true})
		})
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "notlar", Driver: "local", MountPath: "/notlar", ConfigJSON: cfg,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	now := time.Now().Add(-time.Hour)
	mk := func(name string, row *model.Thumbnail) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644))
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: name, Type: model.NodeTypeFile,
			Size: 1, Etag: "e-" + name, PathHash: mutTestPathHash(st.ID, name), SeenAt: time.Now(),
		})
		require.NoError(t, err)
		row.NodeID = n.ID
		row.SourceSig = n.ContentFingerprint()
		row.AttemptedAt = &now
		if row.State == "ready" {
			row.StorageKey, row.GeneratedAt = "k", &now
			require.NoError(t, os.WriteFile(filepath.Join(cacheDir, fmt.Sprint(n.ID)+".jpg"), []byte("\xff\xd8jpeg"), 0o644))
		}
		require.NoError(t, store.UpsertThumbnail(ctx, row))
	}
	mk("cizildi.docx", &model.Thumbnail{State: "ready", Generator: "onlyoffice"})
	mk("bozuk.docx", &model.Thumbnail{State: "failed", Error: "oo_corrupt:ds-3"})
	mk("parolali.xlsx", &model.Thumbnail{State: "skipped", Error: "oo_password"})
	mk("sifreli.pptx", &model.Thumbnail{State: "skipped", Error: "e2e-encrypted content"})
	mk("buyuk.pptx", &model.Thumbnail{State: "skipped", Error: "oo_too_large:26214400"})
	mk("harita.svg", &model.Thumbnail{State: "skipped", Error: "svg_too_large:5242880"})
	mk("bekleyen.docx", &model.Thumbnail{State: "failed", Error: "oo_retry:2:ds-4"})
	mk("yapilandirilmamis.docx", &model.Thumbnail{State: "skipped", Error: "no_tool:office"})

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/manager?action=index&path=notlar://", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	notes := map[string]any{}
	urls := map[string]bool{}
	for _, raw := range body["files"].([]any) {
		f := raw.(map[string]any)
		name := f["basename"].(string)
		notes[name] = f["thumb_note"]
		_, urls[name] = f["thumb_url"]
	}
	assert.Equal(t, map[string]any{
		"cizildi.docx":           nil,
		"bozuk.docx":             "corrupt",
		"parolali.xlsx":          "encrypted",
		"sifreli.pptx":           "encrypted",
		"buyuk.pptx":             "too_large",
		"harita.svg":             "too_large",
		"bekleyen.docx":          nil,
		"yapilandirilmamis.docx": nil,
	}, notes)
	assert.True(t, urls["cizildi.docx"], "a file with its picture has a URL and no marker")
}
