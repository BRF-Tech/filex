package ops_test

// The `upload-commit` op kind: the queue owns the retry/progress bookkeeping,
// the injected UploadCommitter owns the bytes and every post-write hook. Same
// division as DBSync, and for the same reason — the DB mirror, search index,
// thumbnails and the writehook live in the handler layer, which cannot be
// imported from here without a cycle.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

type fakeCommitter struct {
	mu   sync.Mutex
	got  []string
	err  error
	fail bool
}

func (c *fakeCommitter) CommitUpload(_ context.Context, uploadID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, uploadID)
	if c.fail {
		return c.err
	}
	return nil
}

func TestOps_UploadCommit_DispatchesToCommitter(t *testing.T) {
	f := newOpsFixture(t)
	c := &fakeCommitter{}
	f.svc.SetUploadCommitter(c)

	op := f.runOp(t, ops.OpUploadCommit, []string{"11111111-2222-3333-4444-555555555555"}, "")
	assert.Equal(t, ops.StatusOK, op.Status)
	assert.Equal(t, 1, op.Done)

	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Equal(t, []string{"11111111-2222-3333-4444-555555555555"}, c.got,
		"the op's single source is the staged upload id, not a path")
}

// A failing transfer must surface as a failed op — that is what tells the UI
// (and a retry) that the bytes did not move.
func TestOps_UploadCommit_FailurePropagates(t *testing.T) {
	f := newOpsFixture(t)
	f.svc.SetUploadCommitter(&fakeCommitter{fail: true, err: errors.New("backend refused")})

	op := f.runOp(t, ops.OpUploadCommit, []string{"aaaaaaaa-2222-3333-4444-555555555555"}, "")
	assert.Equal(t, ops.StatusFailed, op.Status)
	assert.Contains(t, op.Error, "backend refused")
}

// With no committer wired the op fails loudly. Silently succeeding would tell
// the user their file is saved when nothing ever moved it.
func TestOps_UploadCommit_NoCommitterFails(t *testing.T) {
	f := newOpsFixture(t)
	op := f.runOp(t, ops.OpUploadCommit, []string{"bbbbbbbb-2222-3333-4444-555555555555"}, "")
	assert.Equal(t, ops.StatusFailed, op.Status)
	assert.Contains(t, op.Error, "upload committer")
}

func TestOps_Submit_RejectsUnknownKind(t *testing.T) {
	f := newOpsFixture(t)
	_, err := f.svc.Submit(context.Background(), "not-a-kind", f.st.ID, []string{"x"}, "")
	require.Error(t, err)
}
