package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// The whole chain as internal/server wires it: the thumbnail pipeline, the
// adapter (officeThumbs), the onlyoffice service's conversion client, and a
// document server (httptest) that answers what a real one answers. What each
// answer becomes on the thumbnail row is decided across the three packages;
// this is where it is measured end to end.
func TestOfficeThumbs_TheDocumentServersAnswerOnTheRow(t *testing.T) {
	page := func() []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 226, 320))
		for y := 0; y < 320; y++ {
			for x := 0; x < 226; x++ {
				img.Set(x, y, color.White)
			}
		}
		var b bytes.Buffer
		require.NoError(t, png.Encode(&b, img))
		return b.Bytes()
	}()
	cases := []struct {
		name   string
		answer map[string]any
		state  string
		reason string
		gen    string
	}{
		{"drawn", nil, "ready", "", "onlyoffice"},
		{"a password", map[string]any{"error": -5}, "skipped", "oo_password", ""},
		{"damaged", map[string]any{"error": -3}, "failed", "oo_corrupt:ds-3", ""},
		{"its download failed", map[string]any{"error": -4}, "failed", "oo_retry:1:ds-4", ""},
		{"its own limit", map[string]any{"error": -10}, "skipped", "oo_too_large:0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			var asked atomic.Int32
			var ds *httptest.Server
			ds = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cache/files/page.png" {
					_, _ = w.Write(page)
					return
				}
				asked.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["token"] == nil {
					_ = json.NewEncoder(w).Encode(map[string]any{"error": -8})
					return
				}
				if c.answer != nil {
					_ = json.NewEncoder(w).Encode(c.answer)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"endConvert": true, "fileType": "png", "fileUrl": ds.URL + "/cache/files/page.png"})
			}))
			defer ds.Close()

			_, store := dbtest.NewTestDB(t)
			root := t.TempDir()
			drv := &local.Driver{}
			require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
			st, err := store.CreateStorage(ctx, &model.Storage{Name: "belgeler", Driver: "local", MountPath: "/belgeler", Enabled: true,
				ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
			require.NoError(t, err)
			body := append([]byte("PK\x03\x04"), make([]byte, 128)...)
			require.NoError(t, os.WriteFile(filepath.Join(root, "rapor.docx"), body, 0o644))
			n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "rapor.docx", Path: "/rapor.docx",
				PathHash: pathkey.Hash(st.ID, "/rapor.docx"), Type: model.NodeTypeFile, Size: int64(len(body)), Mime: "application/zip"})
			require.NoError(t, err)

			oo := onlyoffice.New(store, nil, ds.URL, "shared-secret", "http://filex.test", 0)
			pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true, SVG: true})
			pipe.AttachStorage(st.ID, drv)
			pipe.AttachOffice(&officeThumbs{svc: oo})

			_ = pipe.GenerateThumb(ctx, n)
			row, err := store.GetThumbnail(ctx, n.ID)
			require.NoError(t, err)
			assert.Equal(t, c.state, row.State, row.Error)
			assert.Equal(t, c.reason, row.Error)
			assert.Equal(t, c.gen, row.Generator)
			assert.GreaterOrEqual(t, asked.Load(), int32(1), "the document server was asked")
		})
	}

	t.Run("not configured", func(t *testing.T) {
		assert.Equal(t, thumb.OfficeUnconfigured, officeError(onlyoffice.ErrNotConfigured).Class)
		assert.False(t, (&officeThumbs{svc: onlyoffice.New(nil, nil, "", "", "", 0)}).Ready(context.Background()))
	})
}
