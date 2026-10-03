package handlers_test

// Issue #80: "Download failed" is the editor's one sentence for two failures,
// and filex knows which. GET /api/files/onlyoffice/diagnose answers, for a
// person who may open the document, whether the document server asked filex
// for it since it was opened and what filex answered:
//
//   - served:        it downloaded the document; the failure is the browser
//                    loading the converted copy from the document server;
//   - not_requested: it never asked;
//   - refused:       filex refused it, and the reason is named.
//
// Driven through the real router: the editor configuration records the
// opening, the public fetch endpoint records its answers, and the diagnosis is
// refused to a caller who cannot open the document.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// xtFetch calls the PUBLIC fetch endpoint the way the document server does,
// with no session.
func xtFetch(t *testing.T, f *xtFixture, rawQuery string) int {
	t.Helper()
	resp, err := http.Get(f.srv.URL + "/api/files/onlyoffice/fetch?" + rawQuery) //nolint:noctx // test
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// signedFetchQuery signs a fetch for nodeID the way onlyoffice.Service does,
// with the secret newXTServer configures.
func signedFetchQuery(nodeID int64) string {
	exp := time.Now().Add(time.Hour).Unix()
	mac := hmac.New(sha256.New, []byte("onlyoffice-test-secret"))
	_, _ = fmt.Fprintf(mac, "n=%d&exp=%d", nodeID, exp)
	v := url.Values{}
	v.Set("n", strconv.FormatInt(nodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("sig", base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}

func diagnosis(t *testing.T, f *xtFixture, query string) (int, map[string]any) {
	t.Helper()
	return f.do(t, http.MethodGet, "/api/files/onlyoffice/diagnose?"+query, nil)
}

func TestOnlyOfficeDiagnose_TellsTheThreeCasesApart(t *testing.T) {
	f := newXTFixture(t, false)
	id := xtItoa(f.mine.node.ID)

	// Opened, and the document server has not asked: not_requested.
	status, cfg := f.do(t, http.MethodGet, "/api/files/onlyoffice/config?id="+id+"&mode=view", nil)
	require.Equal(t, http.StatusOK, status, "%v", cfg)
	status, d := diagnosis(t, f, "id="+id)
	require.Equal(t, http.StatusOK, status, "%v", d)
	require.Equal(t, "not_requested", d["verdict"])
	require.NotEmpty(t, d["opened_at"], "the opening is what a later fetch is compared with")
	require.Equal(t, "this_process", d["scope"], "the answer says whose memory it is")

	// The document server's request arrives with a forged signature: filex
	// refuses it, and the diagnosis names why.
	forged := url.Values{}
	forged.Set("n", id)
	forged.Set("exp", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	forged.Set("sig", "forged")
	require.Equal(t, http.StatusUnauthorized, xtFetch(t, f, forged.Encode()))
	status, d = diagnosis(t, f, "id="+id)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "refused", d["verdict"])
	fetch, _ := d["fetch"].(map[string]any)
	require.NotNil(t, fetch)
	require.Equal(t, "signature_bad", fetch["reason_code"])
	require.EqualValues(t, http.StatusUnauthorized, fetch["status"])

	// The document server downloads it with the URL from the configuration:
	// served, so a "Download failed" now is the browser's leg.
	doc, _ := cfg["config"].(map[string]any)["document"].(map[string]any)
	u, err := url.Parse(doc["url"].(string))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, xtFetch(t, f, u.RawQuery))
	status, d = diagnosis(t, f, "id="+id)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "served", d["verdict"])
	fetch, _ = d["fetch"].(map[string]any)
	require.EqualValues(t, http.StatusOK, fetch["status"])

	// The path shape the editor sends answers the same.
	status, d = diagnosis(t, f, "path="+url.QueryEscape(f.mine.storage.Name+"://"+f.mine.node.Path))
	require.Equal(t, http.StatusOK, status, "%v", d)
	require.Equal(t, "served", d["verdict"])

	// Opened again after that download, and not asked again (the document
	// server's own copy): not_requested, not a stale "served".
	status, _ = f.do(t, http.MethodGet, "/api/files/onlyoffice/config?id="+id+"&mode=view", nil)
	require.Equal(t, http.StatusOK, status)
	_, d = diagnosis(t, f, "id="+id)
	require.Equal(t, "not_requested", d["verdict"])
}

func TestOnlyOfficeDiagnose_OnlyForWhoeverMayOpenTheDocument(t *testing.T) {
	f := newXTFixture(t, false)

	// The control: the caller's own document answers. Without it a 404 below
	// would prove nothing (an unknown route is a 404 too).
	require.Equal(t, http.StatusOK, xtFetch(t, f, signedFetchQuery(f.mine.node.ID)))
	status, d := diagnosis(t, f, "id="+xtItoa(f.mine.node.ID))
	require.Equal(t, http.StatusOK, status, "%v", d)
	require.Equal(t, "served", d["verdict"])

	// The other tenant's document has a record: the document server fetched it.
	require.Equal(t, http.StatusOK, xtFetch(t, f, signedFetchQuery(f.theirs.node.ID)))

	status, d = diagnosis(t, f, "id="+xtItoa(f.theirs.node.ID))
	require.Equal(t, http.StatusNotFound, status,
		"another tenant's document must answer like an unknown one: %v", d)
	require.Nil(t, d["verdict"], "nothing about it may come back")
	require.Nil(t, d["fetch"])

	// Without a session there is no answer at all.
	resp, err := http.Get(f.srv.URL + "/api/files/onlyoffice/diagnose?id=" + xtItoa(f.mine.node.ID)) //nolint:noctx // test
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// A refusal of a request that is not the document server's (no valid
// signature) must not invent a document in the record: only an opening or a
// signed fetch adds one, so a stranger cannot fill it.
func TestOnlyOfficeDiagnose_UnsignedRequestsAddNothing(t *testing.T) {
	f := newXTFixture(t, false)
	id := xtItoa(f.mine.node.ID)

	forged := url.Values{}
	forged.Set("n", id)
	forged.Set("exp", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	forged.Set("sig", "forged")
	require.Equal(t, http.StatusUnauthorized, xtFetch(t, f, forged.Encode()))

	status, d := diagnosis(t, f, "id="+id)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "not_requested", d["verdict"])
	require.Nil(t, d["fetch"], "a forged request for a document filex never opened leaves no trace")
}
