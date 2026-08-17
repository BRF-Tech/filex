package handlers_test

/* The download cap, measured where it is spent: at the download.

   The existing coverage proved the link STORED max_downloads=3. Nobody had
   ever asked the shipped handler how many files it actually hands out, and the
   answer was four — the counter is bumped after the bytes leave, so the last
   allowed download and the first refused one are the same request. */

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// newLimitFixture shares ONE file with a download cap of max.
func newLimitFixture(t *testing.T, max int) (*handlers.Share, db.Store, *model.Share) {
	t.Helper()
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	const body = "capped payload\n"
	require.NoError(t, drv.Write(ctx, "report.txt", strings.NewReader(body), int64(len(body))))
	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "report.txt", Path: "/report.txt",
		PathHash: mutTestPathHash(st.ID, "/report.txt"), Type: model.NodeTypeFile,
		Size: int64(len(body)),
	})
	require.NoError(t, err)

	svc := share.NewService(store)
	opts := share.CreateOpts{NodeID: node.ID}
	if max > 0 {
		opts.MaxDownloads = &max
	}
	sh, err := svc.Create(ctx, opts)
	require.NoError(t, err)

	return handlers.NewShare(svc, store, resolver, "", sharezip.New(t.TempDir())), store, sh
}

func limitGet(h *handlers.Share, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)
	return rec
}

// TestShareLimit_ThreeMeansThree is the whole complaint in one loop: a link
// capped at three downloads must hand out three files, not four.
func TestShareLimit_ThreeMeansThree(t *testing.T) {
	h, store, sh := newLimitFixture(t, 3)

	served := 0
	for i := 0; i < 6; i++ {
		rec := limitGet(h, sh.Token)
		if rec.Code == 200 && strings.Contains(rec.Body.String(), "capped payload") {
			served++
		}
	}
	require.Equal(t, 3, served, "a 3-download link served %d files", served)

	cur, err := store.GetShareByID(context.Background(), sh.ID)
	require.NoError(t, err)
	require.Equal(t, 3, cur.DownloadCount, "the counter must not run past the cap")
}

// TestShareLimit_OneMeansOne — the same measurement at the size where being
// off by one is the whole feature ("send this to exactly one person").
func TestShareLimit_OneMeansOne(t *testing.T) {
	h, _, sh := newLimitFixture(t, 1)

	first := limitGet(h, sh.Token)
	require.Equal(t, 200, first.Code)
	require.Contains(t, first.Body.String(), "capped payload")

	second := limitGet(h, sh.Token)
	require.NotEqual(t, 200, second.Code, "a 1-download link served a second file")
	require.NotContains(t, second.Body.String(), "capped payload")
}

// slowDriver makes every read take long enough for a second request to arrive
// while the first is still streaming — the window real downloads live in and
// the one a fast local fixture never opens.
type slowDriver struct {
	storage.Driver
	delay time.Duration
}

func (d slowDriver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	rc, err := d.Driver.Read(ctx, p)
	if err != nil {
		return nil, err
	}
	time.Sleep(d.delay)
	return rc, nil
}

// TestShareLimit_ConcurrentDownloadsCannotExceedCap is the bug as it actually
// bites: nothing about it needs a "clever" client, just a file big enough that
// the next click lands before the last one finished.
//
// Measured against the shipped fm.example.com build before the fix: a link capped at
// ONE download handed three complete 400 KB files to three overlapping curls.
func TestShareLimit_ConcurrentDownloadsCannotExceedCap(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	slow := slowDriver{Driver: drv, delay: 150 * time.Millisecond}
	resolver := func(int64) (storage.Driver, error) { return slow, nil }

	const body = "one download only\n"
	require.NoError(t, drv.Write(ctx, "single.txt", strings.NewReader(body), int64(len(body))))
	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "single.txt", Path: "/single.txt",
		PathHash: mutTestPathHash(st.ID, "/single.txt"), Type: model.NodeTypeFile,
		Size: int64(len(body)),
	})
	require.NoError(t, err)

	svc := share.NewService(store)
	one := 1
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: node.ID, MaxDownloads: &one})
	require.NoError(t, err)
	h := handlers.NewShare(svc, store, resolver, "", sharezip.New(t.TempDir()))

	var mu sync.Mutex
	served := 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := limitGet(h, sh.Token)
			if rec.Code == 200 && strings.Contains(rec.Body.String(), body) {
				mu.Lock()
				served++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, served, "a 1-download link served %d overlapping downloads", served)
}

// TestShareLimit_FailedReadRefundsTheSlot — claiming up front must not let a
// storage error quietly burn one of the owner's downloads.
func TestShareLimit_FailedReadRefundsTheSlot(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	// A node the index knows about but the storage does not.
	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "ghost.txt", Path: "/ghost.txt",
		PathHash: mutTestPathHash(st.ID, "/ghost.txt"), Type: model.NodeTypeFile, Size: 3,
	})
	require.NoError(t, err)

	svc := share.NewService(store)
	three := 3
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: node.ID, MaxDownloads: &three})
	require.NoError(t, err)
	h := handlers.NewShare(svc, store, resolver, "", sharezip.New(t.TempDir()))

	rec := limitGet(h, sh.Token)
	require.Equal(t, 500, rec.Code)

	cur, err := store.GetShareByID(ctx, sh.ID)
	require.NoError(t, err)
	require.Equal(t, 0, cur.DownloadCount, "a serve that never happened must not spend a download")
}

// TestShareLimit_UncappedStaysUncapped guards the fix from over-reaching: a
// link with no cap must keep serving.
func TestShareLimit_UncappedStaysUncapped(t *testing.T) {
	h, _, sh := newLimitFixture(t, 0)
	for i := 0; i < 4; i++ {
		rec := limitGet(h, sh.Token)
		require.Equal(t, 200, rec.Code, "uncapped link refused download %d", i+1)
		require.Contains(t, rec.Body.String(), "capped payload")
	}
}
