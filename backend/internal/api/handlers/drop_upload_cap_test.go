package handlers_test

/* A file request's total cap (max_uploads), measured where it is spent: at the
   upload, with uploads that overlap (GHSA-4pc6-66f4-q2x4).

   The handler checked the cap against the row the request had loaded and
   added to the count only once the files were written, so every upload that
   arrived while another was still writing read the same count and passed: a
   link capped at ONE file took fifteen concurrent uploads, each a submission
   folder in the owner's storage. The download cap had the same hole and the
   same fix (share_limit_test.go). */

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// slowWriteDriver holds every write long enough for the next upload to arrive
// while this one is still writing - the window a real upload lives in, and one
// a local temp dir never opens by itself.
type slowWriteDriver struct {
	*local.Driver
	delay time.Duration
}

func (d slowWriteDriver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	time.Sleep(d.delay)
	return d.Driver.Write(ctx, p, r, size)
}

// failingWriteDriver takes no file at all: the storage broke after the claim.
type failingWriteDriver struct{ *local.Driver }

func (failingWriteDriver) Write(context.Context, string, io.Reader, int64) error {
	return errors.New("the disk went away")
}

// newUploadCapFixture is newDropFixture over a storage driver of the test's
// choosing, with a drop link on /inbox capped at max files (0 = no cap).
func newUploadCapFixture(t *testing.T, max int, wrap func(*local.Driver) storage.Driver) (http.Handler, db.Store, *model.Share, string) {
	t.Helper()
	_, store, drv, st, root := newMutateFixture(t)
	d := wrap(drv)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return d, nil
	}
	folder := mkdirNode(t, store, st, root, "inbox")
	svc := share.NewService(store)
	opts := share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop}
	if max > 0 {
		opts.MaxUploads = &max
	}
	sh, err := svc.Create(context.Background(), opts)
	require.NoError(t, err)

	dh := handlers.NewDrop(store, handlers.NewManager(store, resolver), svc, nil, nil, "")
	r := chi.NewRouter()
	r.Post("/d/{token}", dh.Upload)
	return r, store, sh, root
}

// capUploadRequest builds one upload of n small files. Built on the test's
// goroutine: only ServeHTTP runs concurrently.
func capUploadRequest(t *testing.T, token, who string, n int) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("uploader_name", who))
	for i := 0; i < n; i++ {
		part, err := mw.CreateFormFile("file[]", fmt.Sprintf("%s-%d.txt", who, i))
		require.NoError(t, err)
		_, err = io.WriteString(part, "payload of "+who)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", "/d/"+token, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// submissionFolders counts the submission folders the drop made in /inbox -
// what actually landed in the owner's storage, whatever the answers said.
func submissionFolders(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "inbox"))
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func uploadCount(t *testing.T, store db.Store, id int64) int {
	t.Helper()
	cur, err := store.GetShareByID(context.Background(), id)
	require.NoError(t, err)
	return cur.UploadCount
}

// TestDropCap_ConcurrentUploadsCannotExceedTheCap is the report itself: eight
// uploads of one file each, all in flight together, against a link capped at
// ONE file. One is taken; the rest are told the link has ended, and nothing
// of theirs reaches the storage.
func TestDropCap_ConcurrentUploadsCannotExceedTheCap(t *testing.T) {
	r, store, sh, root := newUploadCapFixture(t, 1, func(d *local.Driver) storage.Driver {
		return slowWriteDriver{Driver: d, delay: 150 * time.Millisecond}
	})

	const n = 8
	reqs := make([]*http.Request, n)
	for i := range reqs {
		reqs[i] = capUploadRequest(t, sh.Token, fmt.Sprintf("racer%d", i), 1)
	}
	codes := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, reqs[i])
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	taken := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			taken++
		case http.StatusGone:
		default:
			t.Fatalf("an upload over the cap was answered %d (want 200 or 410): %v", c, codes)
		}
	}
	require.Equal(t, 1, taken, "a link capped at one file took %d overlapping uploads: %v", taken, codes)
	require.Equal(t, 1, submissionFolders(t, root), "what landed in the storage")
	require.Equal(t, 1, uploadCount(t, store, sh.ID), "the count must not run past the cap")
}

// TestDropCap_ConcurrentUploadsShareWhatIsLeft: with room for three files,
// overlapping uploads of two each cannot together take four - the claim is
// for every file of an upload, not one per request.
func TestDropCap_ConcurrentUploadsShareWhatIsLeft(t *testing.T) {
	r, store, sh, root := newUploadCapFixture(t, 3, func(d *local.Driver) storage.Driver {
		return slowWriteDriver{Driver: d, delay: 100 * time.Millisecond}
	})

	const n = 6
	reqs := make([]*http.Request, n)
	for i := range reqs {
		reqs[i] = capUploadRequest(t, sh.Token, fmt.Sprintf("pair%d", i), 2)
	}
	codes := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, reqs[i])
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	taken := 0
	for _, c := range codes {
		if c == http.StatusOK {
			taken++
		}
	}
	require.Equal(t, 1, taken, "room for three files took %d uploads of two: %v", taken, codes)
	require.Equal(t, 1, submissionFolders(t, root))
	require.Equal(t, 2, uploadCount(t, store, sh.ID))
}

// TestDropCap_TheCapCountsInOrder walks a cap of three one upload at a time:
// what is left is said, and the last place is the last one.
func TestDropCap_TheCapCountsInOrder(t *testing.T) {
	r, store, sh, _ := newUploadCapFixture(t, 3, func(d *local.Driver) storage.Driver { return d })
	send := func(who string, n int) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, capUploadRequest(t, sh.Token, who, n))
		return rec.Code
	}

	require.Equal(t, http.StatusOK, send("first", 2))
	require.Equal(t, http.StatusUnprocessableEntity, send("second", 2), "two files where one is left")
	require.Equal(t, 2, uploadCount(t, store, sh.ID), "a refused upload takes nothing")
	require.Equal(t, http.StatusOK, send("third", 1))
	require.Equal(t, http.StatusGone, send("fourth", 1), "the link is spent")
	require.Equal(t, 3, uploadCount(t, store, sh.ID))
}

// TestDropCap_AFailedWriteHandsItsPlaceBack: claiming before the write must
// not let a storage error eat the owner's allowance.
func TestDropCap_AFailedWriteHandsItsPlaceBack(t *testing.T) {
	r, store, sh, _ := newUploadCapFixture(t, 1, func(d *local.Driver) storage.Driver {
		return failingWriteDriver{Driver: d}
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, capUploadRequest(t, sh.Token, "unlucky", 1))
	require.NotEqual(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 0, uploadCount(t, store, sh.ID), "an upload that wrote nothing must not spend the cap")
}

// TestDropCap_UncappedStillCounts guards the fix from over-reaching: a link
// with no cap takes every upload, and its count still says how many files
// arrived (the owner's Shares list shows it).
func TestDropCap_UncappedStillCounts(t *testing.T) {
	r, store, sh, _ := newUploadCapFixture(t, 0, func(d *local.Driver) storage.Driver { return d })
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, capUploadRequest(t, sh.Token, fmt.Sprintf("open%d", i), 2))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	require.Equal(t, 6, uploadCount(t, store, sh.ID))
}
