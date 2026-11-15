package handlers_test

// Issues #351 and #353 on the admin storage surface:
//
//   - a new storage nobody chose a mode for gets the driver's default - live
//     watching (`lazy`) for a local storage, poll for the rest - and the
//     storage form learns that default from the driver descriptor;
//   - an existing local storage that polls is offered the switch
//     (`sync_suggestion`), and one save makes it;
//   - the scan-interval advice the form asks for while a person types.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syncadvice"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Break: default Create's empty mode to model.SyncModePoll again - the local
// storage comes back `poll`.
func TestStorages_CreateWithoutAModeUsesTheDriversDefault(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, out := postStorage(t, client, srv.URL, map[string]any{
		"name": "nas", "driver": "local", "enabled": false,
		"config": map[string]any{"path": t.TempDir()},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "lazy", out["sync_mode"], "a local storage is born with live watching")
	assert.EqualValues(t, model.DefaultSyncIntervalS, out["sync_interval_s"])

	code, out = postStorage(t, client, srv.URL, map[string]any{
		"name": "kova", "driver": "s3", "enabled": false,
		"config": map[string]any{"bucket": "b", "prefix": "p", "access_key": "a", "secret_key": "s"},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "poll", out["sync_mode"], "a remote storage still polls")

	// A mode somebody chose is kept.
	code, out = postStorage(t, client, srv.URL, map[string]any{
		"name": "eski", "driver": "local", "enabled": false, "sync_mode": "poll",
		"config": map[string]any{"path": t.TempDir()},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "poll", out["sync_mode"])
}

// The form starts from the descriptor's default_sync_mode, so it cannot
// disagree with the API about it.
//
// Break: leave DefaultSyncMode unset in StorageDrivers.List.
func TestAdmin_StorageDrivers_SayTheirDefaultSyncMode(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []storage.Descriptor
	testutil.ReadJSON(t, resp, &got)
	require.NotEmpty(t, got)
	for _, d := range got {
		assert.Equal(t, string(model.DefaultSyncModeFor(d.Driver)), d.DefaultSyncMode, d.Driver)
	}
}

// An install from before #351 keeps its polling local storage (no migration),
// and the storage list and page offer the switch; one save makes it, after
// which nothing is offered.
//
// Break: return "" from model.SyncModeSuggestion - the list never offers it.
func TestStorages_APollingLocalStorageIsOfferedLiveWatching(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, out := postStorage(t, client, srv.URL, map[string]any{
		"name": "eski", "driver": "local", "enabled": false, "sync_mode": "poll",
		"config": map[string]any{"path": t.TempDir()},
	})
	require.Equal(t, http.StatusOK, code, out)
	id := strconv.FormatInt(int64(out["id"].(float64)), 10)
	code, out = postStorage(t, client, srv.URL, map[string]any{
		"name": "kova", "driver": "s3", "enabled": false, "sync_mode": "poll",
		"config": map[string]any{"bucket": "b", "prefix": "p", "access_key": "a", "secret_key": "s"},
	})
	require.Equal(t, http.StatusOK, code, out)

	suggestions := func() map[string]string {
		resp, err := client.Get(srv.URL + "/api/admin/storages")
		require.NoError(t, err)
		var rows []struct {
			Name           string `json:"name"`
			SyncSuggestion string `json:"sync_suggestion"`
		}
		testutil.ReadJSON(t, resp, &rows)
		_ = resp.Body.Close()
		got := map[string]string{}
		for _, r := range rows {
			got[r.Name] = r.SyncSuggestion
		}
		return got
	}
	assert.Equal(t, map[string]string{"eski": "lazy", "kova": ""}, suggestions())

	resp, err := client.Get(srv.URL + "/api/admin/storages/" + id)
	require.NoError(t, err)
	one := map[string]any{}
	testutil.ReadJSON(t, resp, &one)
	_ = resp.Body.Close()
	assert.Equal(t, "lazy", one["sync_suggestion"], "the storage page offers it too")

	// The one click: the mode alone.
	raw, _ := json.Marshal(map[string]any{"sync_mode": "lazy"})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/storages/"+id, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	require.NoError(t, err)
	saved := map[string]any{}
	testutil.ReadJSON(t, resp, &saved)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, saved)
	assert.Equal(t, "lazy", saved["sync_mode"])
	assert.Equal(t, "eski", saved["name"], "the rest of the row is kept")

	assert.Equal(t, map[string]string{"eski": "", "kova": ""}, suggestions())
}

// adviceStore answers the two reads IntervalAdvice makes.
type adviceStore struct {
	db.Store
	st  *model.Storage
	run *model.SyncRun
}

func (s adviceStore) GetStorage(_ context.Context, id int64) (*model.Storage, error) {
	if s.st == nil || id != s.st.ID {
		return nil, sql.ErrNoRows
	}
	return s.st, nil
}

func (s adviceStore) LastMeasuredSyncRun(context.Context, int64) (*model.SyncRun, error) {
	if s.run == nil {
		return nil, sql.ErrNoRows
	}
	return s.run, nil
}

func askAdvice(t *testing.T, store db.Store, path string) (int, syncadvice.Advice) {
	t.Helper()
	h := handlers.NewStorages(store, nil)
	r := chi.NewRouter()
	r.Get("/storages/interval-advice", h.IntervalAdvice)
	r.Get("/storages/{id}/interval-advice", h.IntervalAdvice)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var a syncadvice.Advice
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	}
	return rec.Code, a
}

// #353: the form sends what the person typed; the server judges it against
// the storage's last finished scan. A 4-minute scan every 5 minutes keeps the
// disk busy 80 % of the time, and 50 minutes is suggested.
//
// Break: judge the storage's saved interval instead of interval_s - the
// answer is about 15 minutes, whatever the person types.
func TestStorages_IntervalAdviceJudgesWhatIsTyped(t *testing.T) {
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Minute)
	store := adviceStore{
		st:  &model.Storage{ID: 7, Name: "nas", Driver: "local", SyncMode: model.SyncModePoll, SyncIntervalS: 900},
		run: &model.SyncRun{ID: 1, StorageID: 7, StartedAt: start, FinishedAt: &end, SeenCount: 120000, Status: "ok"},
	}

	code, a := askAdvice(t, store, "/storages/7/interval-advice?interval_s=300")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, syncadvice.StateBusy, a.State)
	assert.Equal(t, 80, a.BusyPercent)
	assert.Equal(t, 3000, a.SuggestedIntervalS)
	require.NotNil(t, a.LastScan)
	assert.Equal(t, 240, a.LastScan.DurationS)
	assert.Equal(t, 120000, a.LastScan.Entries)

	// Nothing typed: the storage's own interval.
	_, a = askAdvice(t, store, "/storages/7/interval-advice")
	assert.Equal(t, 900, a.IntervalS)
	assert.Equal(t, 27, a.BusyPercent)

	// The mode the form shows wins over the saved one.
	_, a = askAdvice(t, store, "/storages/7/interval-advice?interval_s=300&mode=lazy")
	assert.Equal(t, syncadvice.StateLazy, a.State)
	assert.Zero(t, a.SuggestedIntervalS)

	code, _ = askAdvice(t, store, "/storages/7/interval-advice?interval_s=soon")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = askAdvice(t, store, "/storages/8/interval-advice")
	assert.Equal(t, http.StatusNotFound, code)
}

// The new-storage form has no storage and no measurement: no warning, and the
// mode is the driver's default when the form sends none.
func TestStorages_IntervalAdviceForANewStorage(t *testing.T) {
	store := adviceStore{}
	_, a := askAdvice(t, store, "/storages/interval-advice?interval_s=60&mode=poll")
	assert.Equal(t, syncadvice.StateUnmeasured, a.State)
	assert.Nil(t, a.LastScan)

	_, a = askAdvice(t, store, "/storages/interval-advice?driver=local")
	assert.Equal(t, "lazy", a.Mode)
	assert.Equal(t, syncadvice.StateLazy, a.State)
	assert.Equal(t, int((15 * time.Minute).Seconds()), a.IntervalS, "the server's default interval")
	assert.True(t, a.DefaultInterval)

	_, a = askAdvice(t, store, "/storages/interval-advice?driver=s3")
	assert.Equal(t, "poll", a.Mode)
	assert.Equal(t, syncadvice.StateUnmeasured, a.State)
}

// #353: a save answers with what its scan interval would cost, from the same
// judgement the form asks for - so the REST caller and an agent through the
// admin_storages_create / _update MCP tools (which run these handlers) hear it
// without asking.
//
// Break: answer storageShown(created) again - no interval_advice.
func TestStorages_ASaveAnswersWithTheIntervalAdvice(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	advice := func(out map[string]any) map[string]any {
		a, _ := out["interval_advice"].(map[string]any)
		require.NotNil(t, a, "the answer carries interval_advice: %v", out)
		return a
	}
	code, out := postStorage(t, client, srv.URL, map[string]any{
		"name": "eski", "driver": "local", "enabled": false, "sync_mode": "poll", "sync_interval_s": 300,
		"config": map[string]any{"path": t.TempDir()},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "unmeasured", advice(out)["state"], "nothing scanned yet")
	assert.EqualValues(t, 300, advice(out)["interval_s"])
	id := strconv.FormatInt(int64(out["id"].(float64)), 10)

	raw, _ := json.Marshal(map[string]any{"sync_interval_s": 600})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/storages/"+id, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	saved := map[string]any{}
	testutil.ReadJSON(t, resp, &saved)
	_ = resp.Body.Close()
	assert.EqualValues(t, 600, advice(saved)["interval_s"], "the saved interval is the one judged")

	code, out = postStorage(t, client, srv.URL, map[string]any{
		"name": "canli", "driver": "local", "enabled": false,
		"config": map[string]any{"path": t.TempDir()},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "lazy", advice(out)["state"])
}
