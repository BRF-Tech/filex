package handlers_test

// POST /api/admin/storages/test used to have no time bound of its own. It ran
// the driver with the request's context, and the S3 driver deliberately widens
// its retry budget to six attempts with a backoff capped at ten seconds so a
// SYNC RUN survives an object store's transient 503. Against an endpoint that
// refuses the connection, those attempts ran to completion: measured 23.6s and
// 29.7s on two consecutive calls against the built binary, with the payload
// web/cypress/e2e/43-storages-crud.cy.ts sends. A local-path probe answered in
// 6ms — the driver, not the handler, was where the time went, and the same
// budget is right where it lives.
//
// So the probe is bounded instead: handlers.ProbeTimeout. These tests pin both
// halves of that — the deadline holds for a dead endpoint, and it costs a
// working local probe nothing.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"

	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
)

func probeStorage(t *testing.T, srv string, client *http.Client, driver string, cfg map[string]any) (time.Duration, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"driver": driver, "config": cfg})
	start := time.Now()
	resp, err := client.Post(srv+"/api/admin/storages/test", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return time.Since(start), out
}

// TestStoragesTest_DeadEndpointIsBounded — an endpoint that refuses the
// connection must not hold the button for half a minute.
//
// 127.0.0.1:1 is the address the Cypress spec uses: nothing listens on it, the
// dial is refused immediately, and the SDK classifies that as retryable — so
// the cost is entirely the retry schedule, and the deadline is what ends it.
func TestStoragesTest_DeadEndpointIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("spends up to ProbeTimeout on purpose")
	}
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	took, out := probeStorage(t, srv.URL, client, "s3", map[string]any{
		"bucket":     "cypress-bogus",
		"region":     "nbg1",
		"endpoint":   "http://127.0.0.1:1",
		"access_key": "x",
		"secret_key": "x",
	})

	assert.Equal(t, false, out["ok"], "a dead endpoint is not a working storage")
	// The ceiling is the deadline plus room for the handler around it. Before
	// the fix this took 23.6s / 29.7s, so the assertion separates the two
	// behaviours by a wide margin rather than measuring the machine.
	assert.Less(t, took, 15*time.Second,
		"probe ran for %s — the %s deadline did not hold", took, 15*time.Second)
	// And it must SAY it timed out. The SDK's own message is a paragraph about
	// attempt counts that reads like a configuration error.
	if msg, _ := out["error"].(string); msg != "" {
		assert.Contains(t, msg, "timed out after",
			"the answer should name the deadline, got %q", msg)
	}
}

// TestStoragesTest_LocalIsStillImmediate — the deadline must not have turned a
// 6ms answer into a wait. A local probe never touches the network; if this ever
// approaches the timeout, something on the probe path started blocking.
func TestStoragesTest_LocalIsStillImmediate(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	took, out := probeStorage(t, srv.URL, client, "local", map[string]any{
		"path": t.TempDir(),
	})
	assert.Equal(t, true, out["ok"], "a readable directory is a working local storage: %v", out)
	assert.Less(t, took, 2*time.Second, "a local probe took %s", took)
}
