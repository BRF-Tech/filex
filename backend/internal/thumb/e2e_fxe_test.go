package thumb_test

// wiring:e2 fxe — the thumbnail pipeline never renders ciphertext: a single
// encrypted file (.fxe) is skipped by its name before a byte is read, and a
// file whose CONTENT starts with either encrypted magic is skipped whatever
// it is called (a .fxe renamed to .jpg, a folder file that escaped the
// marker walk). docs/E2E-ENCRYPTION.md → "What the server knows".

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

func TestGenerateThumb_SkipsEncryptedFiles(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	p := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	p.AttachStorage(st.ID, drv)

	cases := []struct {
		name    string
		content []byte
	}{
		// a .fxe by name — content need not even be read
		{"Rapor.pdf.fxe", append([]byte("filexfxe\x01"), make([]byte, 64)...)},
		// a .fxe renamed to look like an image
		{"photo.jpg", append([]byte("filexfxe\x01"), make([]byte, 64)...)},
		// a folder file outside its folder, named like an image
		{"escaped.png", append([]byte("filexe2e\x02"), make([]byte, 120)...)},
	}
	for _, c := range cases {
		require.NoError(t, os.WriteFile(filepath.Join(root, c.name), c.content, 0o600))
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: c.name, Path: "/" + c.name,
			PathHash: pathkey.Hash(st.ID, "/"+c.name), Type: model.NodeTypeFile, Size: int64(len(c.content)),
		})
		require.NoError(t, err)
		require.ErrorIs(t, p.GenerateThumb(ctx, n), thumb.ErrSkipped, c.name)
		th, err := store.GetThumbnail(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, "skipped", th.State, c.name)
		_, statErr := os.Stat(p.CachePath(n.ID))
		require.True(t, os.IsNotExist(statErr), "%s: nothing cached", c.name)
	}
}
