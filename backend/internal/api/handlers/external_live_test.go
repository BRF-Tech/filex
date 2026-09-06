package handlers_test

// Issue #17 — "external services test fine and then do not work".
//
// The reporter configured OnlyOffice, drawio and the converter from the admin
// UI (their document server lives in a separate compose file, so there is no
// other way), watched the Test button answer 200 for each, saw
// GET /api/admin/external come back with exactly what they had saved — and then
// got "OnlyOffice is not configured" the moment they opened a .ods.
//
// Both halves were true at once because they came from different places: the
// admin API and the capability probe read the `external_services` TABLE, while
// the OnlyOffice service was constructed once at boot from env/YAML and the
// converter URL was baked into the AI handlers the same way. Nothing an
// operator did in the UI could reach the running process.
//
// This file measures the property that fixes it: a service configured through
// the admin API works on the very next request, with no restart.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// extHarness is the small surface these tests need: an authenticated client and
// the store behind it.
type extHarness struct {
	srv    *httptest.Server
	client *http.Client
	Store  db.Store
}

func (h *extHarness) Get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := h.client.Get(h.srv.URL + path)
	require.NoError(t, err)
	return resp
}

func (h *extHarness) Patch(t *testing.T, path string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, h.srv.URL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	return resp
}

// liveExternalServer mirrors how internal/server.New wires OnlyOffice: the
// service is built unconditionally and resolves its URL + secret from the
// external-services table on every call.
func liveExternalServer(t *testing.T, cfgMutate func(*config.Config)) (*extHarness, int64) {
	t.Helper()
	var nodeID int64
	srv, client, store := testutil.NewTestServerWith(t, cfgMutate, func(d *api.Deps) {
		d.External = external.New(d.Store)
		oo := onlyoffice.New(d.Store, nil, d.Cfg.ExternalServices.OnlyOffice.URL,
			d.Cfg.ExternalServices.OnlyOffice.JWTSecret, d.Cfg.PublicURL, 0)
		oo.Live = func(ctx context.Context) (string, string) {
			st := d.External.Get(ctx, external.OnlyOffice)
			if !st.Enabled {
				return "", ""
			}
			return st.URL, st.Secret
		}
		d.OnlyOffice = oo
	})
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s3", Driver: "s3", MountPath: "s3", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "book.ods", Path: "/book.ods",
		PathHash: pathkey.Hash(st.ID, "/book.ods"), Type: model.NodeTypeFile, Size: 10,
	})
	require.NoError(t, err)
	nodeID = n.ID
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return &extHarness{srv: srv, client: client, Store: store}, nodeID
}

// The regression itself: save in the admin API, open a document, no restart.
func TestOnlyOffice_ConfiguredThroughTheAdminAPIWorksWithoutARestart(t *testing.T) {
	h, nodeID := liveExternalServer(t, nil)

	// Boot state: env carries nothing, so the editor is genuinely unavailable.
	resp := h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// What the operator does in Ayarlar → Dış servisler.
	resp = h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "s3cr3t",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// ⚠ The assertion the old code could not pass: same process, next request.
	resp = h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		DocumentServerURL string         `json:"documentServerUrl"`
		Config            map[string]any `json:"config"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.Equal(t, "https://docs.example", out.DocumentServerURL)
	require.NotEmpty(t, out.Config["token"], "the descriptor is signed with the secret we just saved")

	// And turning it off is live in the same way.
	resp = h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// ⚠ GET redacts the secret to "***". The admin UI re-sends what it was shown,
// so storing that string verbatim would replace a working JWT secret with six
// characters of asterisk — and the only symptom would be the document server
// rejecting every descriptor, days later.
func TestExternalAdmin_PatchIgnoresTheRedactedSecretPlaceholder(t *testing.T) {
	h, nodeID := liveExternalServer(t, nil)

	h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "s3cr3t",
	})
	first := configToken(t, h, nodeID)

	// A save that only changed the URL, with the redaction echoed back.
	resp := h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "***",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, first, configToken(t, h, nodeID),
		"the descriptor is still signed with the real secret")
}

// An env-pinned service is re-asserted from the environment at every boot, so a
// UI edit applies now and is reverted on restart. The API says so rather than
// letting the operator find out afterwards.
func TestExternalAdmin_ReportsWhichServicesTheEnvironmentPins(t *testing.T) {
	h, _ := liveExternalServer(t, func(c *config.Config) {
		c.ExternalServices.Drawio.URL = "https://draw.env"
	})
	ctx := context.Background()
	require.NoError(t, h.Store.UpsertExternalService(ctx, "drawio", true,
		"https://draw.env", "", "{}", time.Time{}, "ok"))
	require.NoError(t, h.Store.UpsertExternalService(ctx, "onlyoffice", true,
		"https://docs.ui", "s", "{}", time.Time{}, "ok"))

	resp := h.Get(t, "/api/admin/external")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()

	byName := map[string]map[string]any{}
	for _, e := range out.Entries {
		byName[e["Name"].(string)] = e
	}
	require.Equal(t, true, byName["drawio"]["env_managed"])
	require.Equal(t, false, byName["onlyoffice"]["env_managed"])
	require.Equal(t, "***", byName["onlyoffice"]["SecretEnc"], "secrets are never returned")
}

func configToken(t *testing.T, h *extHarness, nodeID int64) string {
	t.Helper()
	resp := h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Config map[string]any `json:"config"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	tok, _ := out.Config["token"].(string)
	require.NotEmpty(t, tok)
	return tok
}
