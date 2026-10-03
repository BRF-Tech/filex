package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
)

// The office engine's input is a file in a job's run directory, not a
// catalogue document. The document server downloads it through the editor's
// door (/api/files/onlyoffice/fetch) with an offer's address
// (onlyoffice.OfferFile): served while on offer, refused once withdrawn,
// never as ciphertext.
//
// Red before 0.50: the door knew only `n=<node>`, and an `o=` address was a
// 400 "bad n".
func TestOOFetch_AnOfferedFileIsServedForItsOneConversion(t *testing.T) {
	svc := onlyoffice.New(nil, nil, "http://ds.test", "offer-secret", "https://filex.test", 0)
	h := &OnlyOffice{Service: svc}
	dir := t.TempDir()
	fetch := func(raw string) (int, string) {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		h.Fetch(rec, httptest.NewRequest(http.MethodGet, onlyoffice.FetchPath+"?"+u.RawQuery, nil))
		b, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(b)
	}

	p := filepath.Join(dir, "in.csv")
	require.NoError(t, os.WriteFile(p, []byte("a,b\n1,2\n"), 0o600))
	raw, withdraw, err := svc.OfferFile(context.Background(), p, "in.csv")
	require.NoError(t, err)
	status, body := fetch(raw)
	assert.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "a,b\n1,2\n", body)

	forged := strings.Replace(raw, "sig=", "sig=x", 1)
	status, _ = fetch(forged)
	assert.Equal(t, http.StatusUnauthorized, status, "a forged offer")

	withdraw()
	status, _ = fetch(raw)
	assert.Equal(t, http.StatusNotFound, status, "withdrawn when the conversion ended")

	// Ciphertext never goes to the document server, whatever offered it.
	enc := filepath.Join(dir, "secret.docx")
	require.NoError(t, os.WriteFile(enc, append(append([]byte(nil), e2e.MagicPrefix...), []byte("ciphertext")...), 0o600))
	raw, withdraw, err = svc.OfferFile(context.Background(), enc, "secret.docx")
	require.NoError(t, err)
	defer withdraw()
	status, body = fetch(raw)
	assert.Equal(t, http.StatusUnsupportedMediaType, status)
	assert.NotContains(t, body, "ciphertext")
}
