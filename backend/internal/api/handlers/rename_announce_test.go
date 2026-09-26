package handlers_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/realtime"
)

// frames records what the handlers announce to the realtime hub.
type frames struct {
	mu  sync.Mutex
	got []string
}

func (f *frames) EmitChange(storageID int64, dir string, ev realtime.ChangeEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, realtime.RoomKey(storageID, dir)+" "+ev.Action+" "+ev.Name+" -> "+ev.NewName)
}

func (f *frames) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.got
	f.got = nil
	return out
}

// The explorer's rename and a queued one announce the same thing, to the
// folder that holds the item — one function (finishRename) says it for both.
//
// RED PROOF (before, 2026-09-26): vfRename told the folder the REQUEST named
// (`path`), so a rename of Ekip/a.txt sent from the root's listing was
// announced to the root, and whoever watched Ekip heard nothing; the queued
// rename (SyncRename) announced it to Ekip.
func TestRename_TheExplorersAndAQueuedOneAnnounceTheSame(t *testing.T) {
	rec := &frames{}
	handlers.SetChangeEmitter(rec)
	t.Cleanup(func() { handlers.SetChangeEmitter(nil) })

	r := newRenameRig(t)
	r.dir(t, "Ekip")
	r.file(t, "Ekip/a.txt", "a")
	r.file(t, "Ekip/c.txt", "c")

	res := callMutate(t, r.mh, "rename", map[string]any{"path": "main://", "item": "main://Ekip/a.txt", "name": "b.txt"})
	require.Equal(t, 200, res.Code, res.Body.String())
	explorer := rec.take()

	r.mh.SyncRename(context.Background(), r.st.ID, "Ekip/c.txt", "Ekip/d.txt")
	queued := rec.take()

	room := realtime.RoomKey(r.st.ID, "Ekip")
	assert.Contains(t, explorer, room+" rename a.txt -> b.txt", "the explorer's rename was not announced to the item's folder: %v", explorer)
	assert.Contains(t, queued, room+" rename c.txt -> d.txt", "%v", queued)
}
