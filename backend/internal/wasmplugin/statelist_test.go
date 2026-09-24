package wasmplugin

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// An app can find the files it keeps state on. Without this a home screen
// ("the documents waiting for your signature") can only ever show the file
// the person happened to open, which is not a list.
func TestStateList_FindsItsOwnFilesAndNothingElse(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	h.writeFile(t, "docs/a.txt", "one")
	h.writeFile(t, "docs/b.txt", "two")
	h.writeFile(t, "docs/c.txt", "three")

	// `upper` records a `runs` counter on every file it touches; two files
	// get it, the third is left alone.
	for _, rel := range []string{"docs/a.txt", "docs/b.txt"} {
		job, err := h.runJob(t, p, "upper", []string{rel}, "en")
		require.NoError(t, err)
		require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	}

	job, err := h.runJob(t, p, "listing", []string{"docs/c.txt"}, "en")
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	listed := job.Message
	assert.Contains(t, listed, "main://docs/a.txt=1")
	assert.Contains(t, listed, "main://docs/b.txt=1")
	assert.NotContains(t, listed, "docs/c.txt", "a file the plugin never recorded is not in its list")

	// A file the catalogue knows about and has since deleted leaves the
	// listing. (The harness writes straight to disk, so the row is made
	// here — which is also the case the LEFT JOIN is for: a file with no
	// row at all still counts, because it may simply not be indexed yet.)
	node, err := h.store.CreateNode(ctx, &model.Node{
		StorageID: h.st.ID, Name: "a.txt", Path: "docs/a.txt",
		PathHash: pathkey.Hash(h.st.ID, "/docs/a.txt"), Type: model.NodeTypeFile,
	})
	require.NoError(t, err)
	job, err = h.runJob(t, p, "listing", []string{"docs/c.txt"}, "en")
	require.NoError(t, err)
	assert.Contains(t, job.Message, "docs/a.txt", "an indexed, live file stays in the list")
	require.NoError(t, h.reg.opts.Store.SoftDeleteNode(ctx, node.ID))

	job, err = h.runJob(t, p, "listing", []string{"docs/c.txt"}, "en")
	require.NoError(t, err)
	assert.NotContains(t, job.Message, "docs/a.txt", "a deleted file is not offered")
	assert.Contains(t, job.Message, "docs/b.txt")

	// And a listing is not a way around the ACL: what the asking person may
	// not see is not in the answer.
	h.reg.SetVisibility(func(_ context.Context, _ *model.User, _ int64, rel string) bool {
		return !strings.HasSuffix(rel, "b.txt")
	})
	job, err = h.runJob(t, p, "listing", []string{"docs/c.txt"}, "en")
	require.NoError(t, err)
	assert.NotContains(t, job.Message, "docs/b.txt", "a file this person cannot see is filtered out")
}
