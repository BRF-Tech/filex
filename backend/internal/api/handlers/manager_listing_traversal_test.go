package handlers_test

// The listing endpoint and `..`.
//
// ⚠⚠ RED PROOF (unfixed code), measured over HTTP against a live Windows
// instance: `GET /api/files/manager?action=index&adapter=depo&path=..\depo-gizli`
// answered **200 OK** with a listing of a directory OUTSIDE the storage root,
// while `path=../depo-gizli` (forward slash) was correctly refused and
// `action=download` on the very same backslash path answered 400 "bad path".
//
// Two independent defects met there: the local driver cleaned the path with
// path.Clean, which does not know `\` separates anything, and the listing
// branch was the one verb with no pathHasDotDot guard. Both are fixed; this
// file pins the handler half, and the driver half is pinned in
// internal/storage/drivers/local/pathsafety_test.go.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerIndex_RefusesTraversalWithEitherSeparator(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "belge.txt", "alpha's own file")

	// Precondition: the ordinary listing works, or the refusals below prove
	// nothing at all.
	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action=index&path="+
		url.QueryEscape("alpha://"))
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "belge.txt")

	for _, rel := range []string{
		`..\gizli`,               // ⚠ the payload that returned 200 on Windows
		`../gizli`,               // always refused; pinned so a rewrite cannot lose it
		`alt\..\..\gizli`,        // traversal from deeper in
		`..\..\Windows\System32`, // the shape an operator would recognise
	} {
		for _, action := range []string{"index", "subfolders"} {
			status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action="+action+"&path="+
				url.QueryEscape("alpha://"+rel))
			assert.Equal(t, http.StatusBadRequest, status,
				"action=%s path=%q was not refused; body=%s", action, rel, body)
			assert.NotContains(t, body, "belge.txt")
		}
	}
}

// A file whose NAME contains dots is not a traversal, and refusing it was a
// real outage once: a live deployment could not download an invoice called
// "… Gaz..pdf" because every preview and download answered 400 for a file the
// same API had happily stored. The listing guard must not reintroduce it.
func TestManagerIndex_DoesNotRefuseOrdinaryDottedNames(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "Gaz..pdf", "invoice bytes")

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action=index&path="+
		url.QueryEscape("alpha://"))
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Gaz..pdf", "a dotted FILENAME is not a traversal")
}
