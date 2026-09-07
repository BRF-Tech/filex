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

// ─────────────────────────────────────────────────────────────────────────────
// Issue #17, second round. The fix above made the Test button tell the truth
// about the thing it measured. It still measured only ONE of the three
// addresses that have to work, and its green badge was read as an answer to
// all three: the reporter typed his container name `http://onlyoffice`, filex
// reached it, Test went green, and his browser could not resolve that name at
// all. These tests pin the response shape that stops a reader assuming more
// than was checked.
// ─────────────────────────────────────────────────────────────────────────────

// docServerStub is a document server as far as the health probe is concerned.
func docServerStub(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func (h *extHarness) Post(t *testing.T, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, nil)
	require.NoError(t, err)
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func advisoryCodes(v any) map[string]string {
	out := map[string]string{}
	list, _ := v.([]any)
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		code, _ := m["code"].(string)
		sev, _ := m["severity"].(string)
		out[code] = sev
	}
	return out
}

// The Test result must name its vantage point and the legs it did not cover.
func TestExternalAdmin_TestSaysWhereItProbedFromAndWhatItDidNotCheck(t *testing.T) {
	h, _ := liveExternalServer(t, func(c *config.Config) {
		c.PublicURL = "https://files.example.com"
		c.PublicURLSet = true
	})
	ds := docServerStub(t)
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": ds, "secret": "s3cr3t",
	}).StatusCode)

	out := h.Post(t, "/api/admin/external/onlyoffice/test")
	require.Equal(t, true, out["server_reachable"], "the stub answers /healthcheck")
	require.Equal(t, "filex-server", out["checked_from"],
		"the response must say which machine did the probing")
	require.Equal(t, "https://files.example.com", out["public_url"],
		"the third address is reported: nothing else shows it to the operator")

	legs, _ := out["not_checked"].([]any)
	require.ElementsMatch(t, []any{"browser-to-service", "service-to-filex"}, legs,
		"a green probe must not stand for the legs it never made")
}

// The reporter's shape, and the pair that proves the warning is not noise.
func TestExternalAdmin_WarnsWhenTheAddressCannotWorkInABrowser(t *testing.T) {
	t.Run("published install, loopback document server", func(t *testing.T) {
		h, _ := liveExternalServer(t, func(c *config.Config) {
			c.PublicURL = "https://files.example.com"
			c.PublicURLSet = true
		})
		// httptest listens on 127.0.0.1, so filex reaches it and the browser
		// of anybody who opens files.example.com cannot. Green AND wrong.
		ds := docServerStub(t)
		h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
			"enabled": true, "url": ds, "secret": "s3cr3t",
		})
		out := h.Post(t, "/api/admin/external/onlyoffice/test")
		require.Equal(t, true, out["server_reachable"])
		require.Equal(t, true, out["has_warnings"],
			"reachable from the server is not the same as configured")
		require.Equal(t, "warning", advisoryCodes(out["advisories"])["browser_loopback_host"])
	})

	t.Run("container name", func(t *testing.T) {
		h, _ := liveExternalServer(t, func(c *config.Config) {
			c.PublicURL = "https://files.example.com"
			c.PublicURLSet = true
		})
		h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
			"enabled": true, "url": "http://onlyoffice", "secret": "s3cr3t",
		})
		out := h.Post(t, "/api/admin/external/onlyoffice/test")
		require.Equal(t, "warning", advisoryCodes(out["advisories"])["browser_bare_host"])
	})

	// ⚠ The red proof on the other side: a warning that fires on a working
	// setup is worse than none. Everything on loopback is the developer
	// running filex and the document server on one machine, and it works.
	t.Run("everything on loopback stays quiet", func(t *testing.T) {
		h, _ := liveExternalServer(t, func(c *config.Config) {
			c.PublicURL = "http://localhost:5212"
			c.PublicURLSet = true
		})
		ds := docServerStub(t)
		h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
			"enabled": true, "url": ds, "secret": "s3cr3t",
		})
		out := h.Post(t, "/api/admin/external/onlyoffice/test")
		require.Equal(t, true, out["server_reachable"])
		require.Equal(t, false, out["has_warnings"],
			"filex is reached over localhost, so the browser IS on this host")
	})
}

// ⚠ The badge has to be honest before anyone presses anything. The whole
// defect was a control that read as verified without being asked.
func TestExternalAdmin_ListCarriesAdvisoriesWithoutPressingTest(t *testing.T) {
	h, _ := liveExternalServer(t, func(c *config.Config) {
		c.PublicURL = "https://files.example.com"
		c.PublicURLSet = true
	})
	ctx := context.Background()
	require.NoError(t, h.Store.UpsertExternalService(ctx, "onlyoffice", true,
		"http://onlyoffice", "s3cr3t", "{}", time.Time{}, "ok"))

	resp := h.Get(t, "/api/admin/external")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Entries   []map[string]any `json:"entries"`
		PublicURL string           `json:"public_url"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.Equal(t, "https://files.example.com", out.PublicURL)
	for _, e := range out.Entries {
		if e["Name"] == "onlyoffice" {
			require.Equal(t, "warning", advisoryCodes(e["advisories"])["browser_bare_host"])
			return
		}
	}
	t.Fatal("onlyoffice row missing from the list response")
}
