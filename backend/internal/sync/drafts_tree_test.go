package sync_test

// The drafts area (issue #71, syspath.Drafts) is one of filex's own trees, and
// the scanner treats it like the trash: it does not walk it, does not count
// it, and — the part a well-meant change breaks — never tombstones the rows
// the draft feature wrote there. A pass that walked past the drafts area and
// then found their rows "unseen" would put every person's unfinished documents
// in the trash on the next scan.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestSyncLeavesTheDraftsAreaAlone(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)

	// A draft, written the way the draft endpoint writes one: bytes, then its
	// rows (protocolsync.WriteRows, parent chain included).
	const draft = ".filex-drafts/7/0123456789abcdef/Plan.txt"
	writeUnder(t, root, draft, "an unfinished document")
	_, _, ok := protocolsync.New(store, nil, nil, "test").WriteRows(ctx, st, draft, 22, "text/plain")
	require.True(t, ok)
	// A stray file in the area nobody recorded: the walk must not mint a row.
	writeUnder(t, root, ".filex-drafts/8/fedcba9876543210/stray.txt", "x")
	writeUnder(t, root, "belgeler/rapor.txt", "gercek dosya")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Equal(t, 2, run.SeenCount, "objects in the drafts area are not what a pass saw")

	n := node(t, store, st.ID, "/"+draft)
	require.NotNil(t, n, "the scan dropped a draft's row")
	assert.Nil(t, n.DeletedAt, "the scan put a draft in the trash")
	assert.Nil(t, rowAnywhere(t, store, st.ID, "/.filex-drafts/8/fedcba9876543210/stray.txt"),
		"the walk catalogued a file inside the drafts area")
	assert.Empty(t, trashedPaths(t, store, st.ID))

	// A second pass, now that every row is "old": still nothing tombstoned.
	waitPastSecondBoundary()
	_, run = runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	n = node(t, store, st.ID, "/"+draft)
	require.NotNil(t, n)
	assert.Nil(t, n.DeletedAt)
	for _, p := range []string{"/.filex-drafts", "/.filex-drafts/7", "/.filex-drafts/7/0123456789abcdef"} {
		d := node(t, store, st.ID, p)
		require.NotNil(t, d, "the scan dropped the draft's folder row %s", p)
		assert.Equal(t, model.NodeTypeDirectory, d.Type)
	}
}
