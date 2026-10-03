package handlers_test

// Issue #80: "Test now" tested the SAVED row while the box held another
// address. An operator typed a new document server URL (or callback URL),
// pressed Test, and was told about the old one.
//
// The page now sends what is in the form, and the server tests exactly that
// without saving it: saving is what switches every open editor over, and an
// address nobody has checked yet must not reach them because somebody pressed
// Test. The answer says `unsaved: true`, the row and the configuration in
// force are untouched.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func (h *extHarness) PostJSON(t *testing.T, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// unreachableDocServer is an address nothing answers on.
const unreachableDocServer = "http://127.0.0.1:1"

func TestExternalAdmin_TestNowTestsTheFormWithoutSavingIt(t *testing.T) {
	h, nodeID := liveExternalServer(t, publishedAt)
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": unreachableDocServer, "secret": "saved",
	}).StatusCode)

	// What the operator typed and has not saved: a document server that
	// works, a new secret, and the callback address it can reach filex at.
	ds := convertingStub(t, convertStub{enforceJWT: true})
	status, out := h.PostJSON(t, "/api/admin/external/onlyoffice/test", map[string]any{
		"url": ds, "secret": "candidate", "callback_url": h.srv.URL,
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["unsaved"], "the answer must say it is about values that are not saved")
	require.Equal(t, ds, out["url"], "the form's address was probed, not the saved one")
	require.Equal(t, true, out["server_reachable"])
	require.Equal(t, h.srv.URL, out["callback_url"])
	leg, _ := out["service_to_filex"].(map[string]any)
	require.Equal(t, true, leg["ok"],
		"the probe is signed with the form's secret and verified against it: %v", leg["detail"])

	// Nothing was saved: the row and the configuration in force are as before.
	row, err := h.Store.GetExternalService(context.Background(), "onlyoffice")
	require.NoError(t, err)
	require.Equal(t, unreachableDocServer, row.URL)
	require.Equal(t, "saved", row.SecretEnc)
	require.NotEqual(t, "ok", row.LastState, "the row's verdict stays about the row")

	resp := h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=view")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var cfg struct {
		DocumentServerURL string `json:"documentServerUrl"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cfg))
	resp.Body.Close()
	require.Equal(t, unreachableDocServer, cfg.DocumentServerURL, "open editors keep the saved address")
}

// The form holding what is saved is the saved test: stored as before.
func TestExternalAdmin_TestNowWithTheSavedValuesStoresTheVerdict(t *testing.T) {
	h, _ := liveExternalServer(t, publishedAt)
	ds := docServerStub(t)
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": ds, "secret": "s3cr3t",
	}).StatusCode)

	// The page sends the boxes; the secret box is empty (the stored one is kept).
	status, out := h.PostJSON(t, "/api/admin/external/onlyoffice/test", map[string]any{
		"enabled": true, "url": ds, "callback_url": "",
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, false, out["unsaved"])
	require.Equal(t, true, out["server_reachable"])
	row, err := h.Store.GetExternalService(context.Background(), "onlyoffice")
	require.NoError(t, err)
	require.Equal(t, "ok", row.LastState, "a Test of the saved values records its verdict, as it always did")
}

// An address that can never work is refused before anything is probed, as
// Save refuses it.
func TestExternalAdmin_TestNowRefusesAFormAddressThatIsNotOne(t *testing.T) {
	h, _ := liveExternalServer(t, publishedAt)
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": docServerStub(t), "secret": "s3cr3t",
	}).StatusCode)
	status, out := h.PostJSON(t, "/api/admin/external/onlyoffice/test", map[string]any{"url": "bu-bir-adres-degil"})
	require.Equal(t, http.StatusBadRequest, status, "%v", out)
	require.Equal(t, "url_invalid", out["error"])
}
