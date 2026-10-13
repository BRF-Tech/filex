package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/diskfree"
	"github.com/brf-tech/filex/backend/internal/storage"
)

var _ storage.SpaceReporter = (*Driver)(nil)

// What `df` on the storage's own folder says is what the storage says (issue
// #116: SFTP answered 1 PiB with nothing used for a local RAID volume).
func TestSpace_IsTheRootsDisk(t *testing.T) {
	d := newDriver(t)
	want, err := diskfree.Measure(d.Root())
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	sp, err := d.Space(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, want.Total, sp.Total)
	assert.Equal(t, want.Volume, sp.Volume)
	assert.NotZero(t, sp.Free)
	assert.LessOrEqual(t, sp.Free, sp.Total)
}

// A client asks about the folder it is about to upload into, which may not
// exist yet.
func TestSpace_APathNotThereYetIsMeasuredAtItsFolder(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	root, err := d.Space(ctx, "")
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	sp, err := d.Space(ctx, "not/there/yet.bin")
	require.NoError(t, err)
	assert.Equal(t, root.Total, sp.Total)
	assert.Equal(t, root.Volume, sp.Volume)
}

// The size of a disk outside the root is not the storage's to tell.
func TestSpace_ALinkThatLeavesTheRootIsNotMeasured(t *testing.T) {
	f := newLinkFixture(t)
	_, err := f.d.Space(context.Background(), "escape")
	require.ErrorIs(t, err, ErrLinkLeavesRoot)
}
