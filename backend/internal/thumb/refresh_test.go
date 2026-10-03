package thumb_test

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// The refresher is what a listing hands a missing or stale thumbnail to. It
// must never make the listing wait, must not draw one file twice at once, and
// must say when it is done so the folder can reload.

func TestRefresher_DrawsWhatAListingFoundMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "yeni.png", solidPNG(t, 30, 30, color.RGBA{10, 200, 10, 255}))

	done := make(chan int64, 4)
	r := thumb.NewRefresher(pipe, store, 16)
	r.OnRendered(func(n *model.Node) { done <- n.ID })
	go r.Run(ctx, 2)

	require.Equal(t, thumb.Render, r.Consider(n, nil))
	select {
	case id := <-done:
		require.Equal(t, n.ID, id)
	case <-time.After(10 * time.Second):
		t.Fatal("the refresher never drew the file")
	}
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State)
	require.Equal(t, thumb.Leave, r.Consider(n, row), "a fresh row is not queued again")
}

// Consider never blocks: with no worker running and the queue full, the extra
// files are dropped (the next listing asks again) and the call returns at once.
func TestRefresher_NeverBlocksTheListing(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	r := thumb.NewRefresher(pipe, store, 2)
	var nodes []*model.Node
	for i := 0; i < 12; i++ {
		name := string(rune('a'+i)) + ".png"
		nodes = append(nodes, writeFile(t, store, st, root, name, solidPNG(t, 8, 8, color.RGBA{1, 2, 3, 255})))
	}
	start := time.Now()
	queued := 0
	for _, n := range nodes {
		if r.Consider(n, nil) == thumb.Render && r.Queued(n.ID) {
			queued++
		}
	}
	require.Less(t, time.Since(start), time.Second, "ten files that did not fit must not have made the listing wait")
	require.Equal(t, 2, queued, "a bounded queue holds what it can and drops the rest")
}

// The same file asked for twice before its render is queued once.
func TestRefresher_OneEntryPerFile(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	r := thumb.NewRefresher(pipe, store, 8)
	n := writeFile(t, store, st, root, "tek.png", solidPNG(t, 8, 8, color.RGBA{1, 2, 3, 255}))
	r.Consider(n, nil)
	r.Consider(n, nil)
	require.Equal(t, 1, r.Len())
}

// A listing of a folder whose catalogue is behind the disk (the lazy overlay)
// sees the disk's size and date. The render records THAT signature, so the
// next listing does not see the same file as stale again, and again.
func TestRefresher_RecordsWhatTheListingSaw(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "diskte.png", solidPNG(t, 8, 8, color.RGBA{1, 2, 3, 255}))
	require.NoError(t, pipe.GenerateThumb(ctx, n))

	bigger := solidPNG(t, 64, 64, color.RGBA{200, 2, 3, 255})
	require.NoError(t, os.WriteFile(filepath.Join(root, "diskte.png"), bigger, 0o644))
	seen := *n
	seen.Size = int64(len(bigger))
	mt := time.Now().Add(3 * time.Second).UTC().Truncate(time.Millisecond)
	seen.BackendMtime = &mt

	done := make(chan struct{}, 1)
	r := thumb.NewRefresher(pipe, store, 8)
	r.OnRendered(func(*model.Node) { done <- struct{}{} })
	go r.Run(ctx, 1)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	// The loop guard: this row was attempted a moment ago. A clock an hour on
	// stands in for "later"; the refresher's own clock is moved the same way.
	r.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	require.Equal(t, thumb.Render, r.Consider(&seen, row))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no render")
	}
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, seen.ContentFingerprint(), row.SourceSig)
	require.Equal(t, thumb.Leave, r.Consider(&seen, row))
}

// A file that was moved between the listing and the render is drawn from
// where it is now, not from where the listing saw it.
func TestRefresher_FollowsAMove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "eski.png", solidPNG(t, 8, 8, color.RGBA{1, 2, 3, 255}))
	stale := *n
	require.NoError(t, os.Rename(filepath.Join(root, "eski.png"), filepath.Join(root, "yeni.png")))
	require.NoError(t, store.MoveNode(ctx, n.ID, nil, "yeni.png", "/yeni.png", pathkey.Hash(st.ID, "/yeni.png")))

	var mu sync.Mutex
	var got *model.Node
	done := make(chan struct{}, 1)
	r := thumb.NewRefresher(pipe, store, 8)
	r.OnRendered(func(x *model.Node) { mu.Lock(); got = x; mu.Unlock(); done <- struct{}{} })
	go r.Run(ctx, 1)
	r.Consider(&stale, nil)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no render")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/yeni.png", got.Path)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, "drawn from its new place: %s", row.Error)
}

// What the sync hands over is judged against the rows in one query, and
// filex's own folders (version snapshots, the open-with working area) are
// never drawn: nobody can see them.
func TestRefresher_ConsiderAllSkipsFilexsOwnFolders(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	r := thumb.NewRefresher(pipe, store, 8)
	shown := writeFile(t, store, st, root, "gorunen.png", solidPNG(t, 8, 8, color.RGBA{1, 2, 3, 255}))
	hidden := *shown
	hidden.ID = shown.ID + 1000
	hidden.Path = "/.versions/gorunen.png.1"
	r.ConsiderAll(context.Background(), []*model.Node{shown, &hidden})
	require.True(t, r.Queued(shown.ID))
	require.False(t, r.Queued(hidden.ID), "a file under .versions is never drawn")
}

// A nil refresher (thumbnails off) is safe to ask.
func TestRefresher_NilIsSafe(t *testing.T) {
	var r *thumb.Refresher
	require.Equal(t, thumb.Leave, r.Consider(&model.Node{Type: model.NodeTypeFile}, nil))
}
