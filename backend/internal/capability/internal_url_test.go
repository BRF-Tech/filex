package capability

// FILEX_ONLYOFFICE_INTERNAL_URL / FILEX_DRAWIO_INTERNAL_URL (GitHub #143): the
// health probe asks the service where filex reaches it, and the capabilities
// keep the row's URL, which is the browser's - the draw.io frame and api.js
// load from it, so it is the one the explorer has to be handed.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestInternalURL_TheProbeAsksTheInternalAddress(t *testing.T) {
	var hits atomic.Int32
	drawio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer drawio.Close()

	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	// The browser's address: nothing here can reach it.
	const browser = "https://draw.invalid"
	require.NoError(t, store.UpsertExternalService(ctx, "drawio", true, browser, "", "{}", time.Time{}, "unknown"))

	s := New(store)
	s.SetInternalURLs(map[string]string{"drawio": drawio.URL + "/"})

	caps, err := s.Get(ctx)
	require.NoError(t, err)
	got := caps.External["drawio"]
	assert.Equal(t, "ok", got.State, "probed at the internal address")
	assert.Equal(t, browser, got.URL, "the capabilities carry the browser's address, never the internal one")
	assert.NotZero(t, hits.Load())

	st, err := s.ProbeExternal(ctx, "drawio")
	require.NoError(t, err)
	assert.Equal(t, "ok", st.State, "Test now, on the saved row")
	assert.Equal(t, browser, st.URL)

	// Test now on the admin page's unsaved address: the server's leg is still
	// filex's own address for the service.
	st = s.ProbeExternalValues("drawio", true, "https://draw-new.invalid", "")
	assert.Equal(t, "ok", st.State)
	assert.Equal(t, "https://draw-new.invalid", st.URL)

	// Without one, the row's URL is probed, as before.
	plain := New(store)
	st, err = plain.ProbeExternal(ctx, "drawio")
	require.NoError(t, err)
	assert.Equal(t, "unreachable", st.State)
	assert.Contains(t, st.Detail, "GET "+browser)
}

func TestInternalURL_AnAddressForAnotherServiceChangesNothingHere(t *testing.T) {
	s := New(nil)
	s.SetInternalURLs(map[string]string{"onlyoffice": "http://onlyoffice", "drawio": "  "})
	assert.Equal(t, "https://draw.example", s.probeAddress("drawio", "https://draw.example"), "a blank value is no address")
	assert.Equal(t, "http://onlyoffice", s.probeAddress("onlyoffice", "https://office.example"))
}
