package handlers_test

// A conversion's download (filex 0.50, onlyoffice/purpose.go): the document
// server fetching a document to draw its thumbnail (or for an app's
// conversion) walks the editor's door with an address that names its purpose.
// It is signed with the purpose, it is refused for ciphertext, and what it was
// answered never reaches the editor's "Download failed" diagnosis.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// purposeFetchQuery signs a fetch for nodeID and a purpose the way
// onlyoffice.Service.PurposeFetchURL does, with newXTServer's secret.
func purposeFetchQuery(nodeID int64, purpose string) string {
	exp := time.Now().Add(10 * time.Minute).Unix()
	mac := hmac.New(sha256.New, []byte("onlyoffice-test-secret"))
	_, _ = fmt.Fprintf(mac, "n=%d&exp=%d&p=%s", nodeID, exp, purpose)
	v := url.Values{}
	v.Set("n", strconv.FormatInt(nodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("p", purpose)
	v.Set("sig", base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}

func xtFetchBody(t *testing.T, f *xtFixture, rawQuery string) (int, string) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + "/api/files/onlyoffice/fetch?" + rawQuery) //nolint:noctx // test
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// A thumbnail's download is served - and the editor's diagnosis still says
// the document server has not asked for the document: it was another request,
// for another reason. On the old door the purpose was not part of the
// signature, the download counted as the editor's, and an editor that said
// "Download failed" was told the document server had got the document.
func TestOOFetch_ThumbFetchDoesNotFeedEditorDiagnosis(t *testing.T) {
	f := newXTFixture(t, false)
	id := f.mine.node.ID

	status, cfg := f.do(t, http.MethodGet, "/api/files/onlyoffice/config?id="+xtItoa(id)+"&mode=view", nil)
	require.Equal(t, http.StatusOK, status, "%v", cfg)

	status, body := xtFetchBody(t, f, purposeFetchQuery(id, "thumb"))
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "LIVE-globex", body, "the document's bytes")

	status, d := diagnosis(t, f, "id="+xtItoa(id))
	require.Equal(t, http.StatusOK, status, "%v", d)
	require.Equal(t, "not_requested", d["verdict"], "a thumbnail's download is not the editor's")
	require.Nil(t, d["fetch"], "nothing of it in the editor's record")

	// A refused thumbnail download leaves the editor's record alone too.
	forged := strings.Replace(purposeFetchQuery(id, "thumb"), "sig=", "sig=x", 1)
	status, _ = xtFetchBody(t, f, forged)
	require.Equal(t, http.StatusUnauthorized, status)
	_, d = diagnosis(t, f, "id="+xtItoa(id))
	require.Equal(t, "not_requested", d["verdict"])

	// The editor's own download still counts.
	require.Equal(t, http.StatusOK, xtFetch(t, f, signedFetchQuery(id)))
	_, d = diagnosis(t, f, "id="+xtItoa(id))
	require.Equal(t, "served", d["verdict"])
}

// The purpose is signed: an editor's address with a purpose added, an
// address of one purpose relabelled as another, and an unknown purpose are
// all refused.
func TestOOFetch_PurposeIsPartOfTheSignature(t *testing.T) {
	f := newXTFixture(t, false)
	id := f.mine.node.ID

	editor := signedFetchQuery(id)
	status, _ := xtFetchBody(t, f, editor+"&p=thumb")
	require.Equal(t, http.StatusUnauthorized, status, "an editor's address is not a thumbnail's")

	thumb := purposeFetchQuery(id, "thumb")
	status, _ = xtFetchBody(t, f, strings.Replace(thumb, "p=thumb", "p=convert", 1))
	require.Equal(t, http.StatusUnauthorized, status, "a thumbnail's address is not a conversion's")

	stripped := url.Values{}
	q, _ := url.ParseQuery(thumb)
	for k, v := range q {
		if k != "p" {
			stripped[k] = v
		}
	}
	status, _ = xtFetchBody(t, f, stripped.Encode())
	require.Equal(t, http.StatusUnauthorized, status, "nor, without its purpose, an editor's")

	status, _ = xtFetchBody(t, f, purposeFetchQuery(id, "anything"))
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = xtFetchBody(t, f, purposeFetchQuery(id, "convert"))
	require.Equal(t, http.StatusOK, status, "the control: a conversion's own address")
}

// An end-to-end encrypted file is never handed to a document server for a
// conversion, whatever its name: its first bytes give it away.
func TestOOFetch_EncryptedNeverServedForAConversion(t *testing.T) {
	f := newXTFixture(t, false)
	id := f.mine.node.ID
	path := filepath.Join(f.mine.root, filepath.FromSlash(strings.TrimPrefix(f.mine.node.Path, "/")))
	require.NoError(t, os.WriteFile(path, append([]byte("filexe2e"), make([]byte, 64)...), 0o644))

	for _, p := range []string{"thumb", "convert"} {
		status, body := xtFetchBody(t, f, purposeFetchQuery(id, p))
		require.Equal(t, http.StatusUnsupportedMediaType, status, p)
		require.NotContains(t, body, "filexe2e", "not a byte of the ciphertext")
	}
}
