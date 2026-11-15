package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"

	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/ftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/smb"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/webdav"
)

// streamingLocal is a driver that streams its own changes (storage.Watcher),
// as a storage plugin declaring `watch` does.
type streamingLocal struct{ *local.Driver }

func (streamingLocal) Subscribe(context.Context) (<-chan storage.Event, error) { return nil, nil }

// A filex-watch agent serves a tree of real folders on a disk: every driver
// with folders, not an object store (it has none to watch), not a driver that
// streams its own changes (it tells filex itself) - read from the driver,
// never from a list of names.
func TestWatchAgentApplies(t *testing.T) {
	want := map[string]bool{"local": true, "sftp": true, "ftp": true, "smb": true, "webdav": true, "s3": false}
	for name, applies := range want {
		drv, err := storage.Get(name)
		require.NoError(t, err, name)
		assert.Equal(t, applies, storage.WatchAgentApplies(drv), name)
	}
	assert.False(t, storage.WatchAgentApplies(streamingLocal{&local.Driver{}}), "a driver that streams its own changes")
	assert.False(t, storage.WatchAgentApplies(nil))

	// The storage page reads the same answer from the descriptor.
	seen := map[string]bool{}
	for _, d := range storage.Descriptors() {
		if applies, ok := want[d.Driver]; ok {
			assert.Equal(t, applies, d.WatchAgent, "descriptor %s", d.Driver)
			seen[d.Driver] = true
		}
	}
	assert.Len(t, seen, len(want), "every built-in driver has a descriptor")
}
