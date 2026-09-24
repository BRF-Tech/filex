package handlers_test

// POST /api/admin/trash/empty answers before anything in front of it gives up.
//
// ⚠⚠ It used to purge inside the request and answer when the purge was done.
// Emptying a trash of 61,844 files needed the better part of an hour; nginx
// allowed sixty seconds and answered 504, the admin page's HTTP client had
// already given up at thirty, and the purge died with the request's context.
// From the admin's chair the button did nothing, so it was pressed again —
// three purges over the same rows. Now the endpoint starts the purge, waits a
// moment (an ordinary trash is gone by then and the answer is the final count,
// as it always was), and otherwise answers 202 with the run's progress, which
// GET on the same path reports until it is over.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// heldPurge holds every purge until release is closed — a trash that takes
// longer to empty than the endpoint is willing to wait.
type heldPurge struct {
	db.Store
	once     sync.Once
	freeOnce sync.Once
	reached  chan struct{}
	release  chan struct{}
}

func (h *heldPurge) HardDeleteNode(ctx context.Context, id int64) error {
	h.once.Do(func() { close(h.reached) })
	<-h.release
	return h.Store.HardDeleteNode(ctx, id)
}

// free lets every held purge through, once.
func (h *heldPurge) free() { h.freeOnce.Do(func() { close(h.release) }) }

// slowTrash is a store with n trashed files in one storage, behind a router
// carrying only the two trash-empty routes and a short wait.
func slowTrash(t *testing.T, n int) (*httptest.Server, db.Store, *heldPurge, int64) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "big", Driver: "local", MountPath: "big", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		node := seedNodeIn(t, store, st.ID, fmt.Sprintf("copy-%d.bin", i))
		require.NoError(t, store.SoftDeleteNode(context.Background(), node.ID))
	}
	held := &heldPurge{Store: store, reached: make(chan struct{}), release: make(chan struct{})}
	h := handlers.NewTrash(trash.New(held, nil, nil), store)
	h.EmptyWait = 20 * time.Millisecond
	r := chi.NewRouter()
	r.Post("/api/admin/trash/empty", h.AdminEmpty)
	r.Get("/api/admin/trash/empty", h.EmptyStatus)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	// Registered after Close, so it runs first: a held purge would otherwise
	// keep its request open and Close waits for it.
	t.Cleanup(held.free)
	return srv, store, held, st.ID
}

// patient is the admin page's side of the wire, with a timeout — its own is
// thirty seconds, and the old endpoint outwaited it.
func patient() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

// untilEmptied polls the status the admin page polls, until the run is over.
func untilEmptied(t *testing.T, client *http.Client, base string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body := doJSON(t, client, http.MethodGet, base+"/api/admin/trash/empty", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		if body["running"] == false {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the empty never finished")
	return nil
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestTrashEmpty_AnswersBeforeTheProxyGivesUp(t *testing.T) {
	srv, store, held, sid := slowTrash(t, 3)
	client := patient()

	began := time.Now()
	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty", map[string]any{})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	assert.Less(t, time.Since(began), 5*time.Second, "the answer does not wait for the purge")
	assert.Equal(t, true, body["ok"])
	assert.Equal(t, true, body["running"])
	assert.Equal(t, float64(3), body["total"], "the page can draw progress against this")

	waitClosed(t, held.reached, "the purge to be under way")
	status, body = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/trash/empty", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, body["running"], "the status says it is still going")

	held.free()
	done := untilEmptied(t, client, srv.URL)
	assert.Equal(t, float64(3), done["purged"])
	assert.Equal(t, float64(0), done["failed"])
	assert.NotEmpty(t, done["finished_at"])

	left, _, err := store.ListTrashed(context.Background(), &sid, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, left, "the purge finished after the request that started it had been answered")
}

// The admin who saw nothing pressed the button again. The second press is
// told what is already happening instead of starting a second purge.
func TestTrashEmpty_ASecondPressWhileItRunsIsTurnedAway(t *testing.T) {
	srv, _, held, _ := slowTrash(t, 2)
	client := patient()

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty", map[string]any{})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	waitClosed(t, held.reached, "the first purge")

	status, body = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty", map[string]any{})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "BUSY", body["code"])
	job, _ := body["job"].(map[string]any)
	require.NotNil(t, job, "the caller's own run comes back with the refusal: %v", body)
	assert.Equal(t, true, job["running"])

	held.free()
	done := untilEmptied(t, client, srv.URL)
	assert.Equal(t, float64(2), done["purged"], "one purge, not two")
}

// A narrowing the server cannot read is refused, and nothing is purged.
//
// ⚠⚠ It used to be dropped. The body was decoded into one struct and, on any
// decode error, ignored whole — so `{"storage_id":2,"older_than_days":""}`
// (what the admin page sent once the days box had been typed in and cleared)
// lost its storage_id with its bad field and emptied every storage the caller
// could reach. On the most destructive request in the admin surface a value
// that cannot be read has to fail closed.
func TestTrashEmpty_ANarrowingItCannotReadPurgesNothing(t *testing.T) {
	srv, client, store := ownershipServer(t, false)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	named := seedStorageFor(t, store, 1, "named")
	other := seedStorageFor(t, store, 1, "other")
	a := seedNodeIn(t, store, named.ID, "a.txt")
	b := seedNodeIn(t, store, other.ID, "b.txt")
	require.NoError(t, store.SoftDeleteNode(t.Context(), a.ID))
	require.NoError(t, store.SoftDeleteNode(t.Context(), b.ID))

	for _, tc := range []struct {
		name string
		path string
		body any
	}{
		{"a cleared days box", "", map[string]any{"storage_id": named.ID, "older_than_days": ""}},
		{"negative days", "", map[string]any{"storage_id": named.ID, "older_than_days": -1}},
		{"a storage named, not numbered", "", map[string]any{"storage_id": "named"}},
		{"negative storage", "", map[string]any{"storage_id": -4}},
		{"a misspelt narrowing", "", map[string]any{"storageId": named.ID}},
		{"not an object", "", []int{1}},
		{"unreadable days in the query", "?older_than_days=soon", map[string]any{}},
		{"unreadable storage in the query", "?storage_id=named", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty"+tc.path, tc.body)
			require.Equal(t, http.StatusBadRequest, status, "%v", body)
		})
	}

	for _, id := range []int64{a.ID, b.ID} {
		n, err := store.GetNode(t.Context(), id)
		require.NoError(t, err, "node %d was purged by a request the server could not read", id)
		assert.NotNil(t, n.DeletedAt, "and it is still in the trash")
	}
}

// A run's progress is its tenant's: the admin of another tenant asks and is
// told there is none.
func TestTrashEmptyStatus_IsTheTenantsOwn(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	mine := seedFullTenant(t, store, "diyetlif")
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty", map[string]any{})
	require.Equal(t, http.StatusOK, status, "a one-file trash is finished within the wait: %v", body)
	assert.Equal(t, false, body["running"])
	assert.Equal(t, float64(1), body["total"], "the tenant's own trash, not the instance's")

	status, body = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/trash/empty", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, body["running"])
	assert.NotEmpty(t, body["started_at"], "the tenant sees how its own run went")

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	stranger := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, stranger, theirs.adminEmail, theirs.adminPass)
	status, body = doJSON(t, stranger, http.MethodGet, srv.URL+"/api/admin/trash/empty", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{"running": false}, body, "another tenant's admin sees no run at all")
}
