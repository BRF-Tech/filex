package handlers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// A public link must never serve filex's own bookkeeping. The storage root has
// no row, so an ordinary share cannot reach `.versions/` — but a row INSIDE it
// can exist (earlier scans minted one per snapshot folder and file), and a
// share names a row by id. The folder-share surfaces filtered the trash and
// the thumbnails by name and forgot the version history, so such a link
// listed and streamed the previous contents of somebody's files.
func TestShare_NeverServesFilexsOwnTrees(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, ".versions"))
	require.NoError(t, drv.Mkdir(ctx, ".versions/42"))
	require.NoError(t, drv.Write(ctx, ".versions/42/1", strings.NewReader("eski surum"), 10))

	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "42", Path: ".versions/42",
		PathHash: mutTestPathHash(st.ID, ".versions/42"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	file, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, ParentID: &dir.ID, Name: "1", Path: ".versions/42/1",
		PathHash: mutTestPathHash(st.ID, ".versions/42/1"), Type: model.NodeTypeFile, Size: 10,
	})
	require.NoError(t, err)

	shareSvc := share.NewService(store)
	h := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir()))

	folder, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)
	rec := browseGet(h, folder.Token, "")
	require.Equal(t, 404, rec.Code, "the browse page of a folder inside .versions must not render")
	require.NotContains(t, rec.Body.String(), "eski surum")
	rec = browseGetFile(h, folder.Token, "1", "")
	require.Equal(t, 404, rec.Code, "a snapshot must not be streamed through a folder share")
	require.NotContains(t, rec.Body.String(), "eski surum")
	rec = browseGet(h, folder.Token, "?zip=1")
	require.Equal(t, 404, rec.Code, "nor zipped")

	single, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: file.ID})
	require.NoError(t, err)
	rec = browseGet(h, single.Token, "")
	require.Equal(t, 404, rec.Code, "a file share of a snapshot must not serve it")
	require.NotContains(t, rec.Body.String(), "eski surum")
}
