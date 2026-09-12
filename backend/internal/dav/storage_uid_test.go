package dav

// A mount written against the storage's uid survives a rename; one written
// against its name does not, and never could.
//
// This is the measurement behind issue #21's second half: the reporter renamed
// a storage and his client went on asking for the old path, which 404s because
// the name IS the address. The uid is the address that does not move, so the
// test renames the storage under a live client and checks both halves — the
// uid still answers, the old name does not, and the new name does.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDav_UIDSurvivesARename(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "Garage S3", false, false)
	require.NotEmpty(t, st.UID, "a storage must be given a uid when it is created")

	propfind := func(path string) int {
		resp := ha.req(t, "PROPFIND", path, ha.adminEmail, ha.adminPass, "",
			map[string]string{"Depth": "1"})
		defer resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusMultiStatus, propfind("/dav/"+st.UID+"/"),
		"the uid must address the storage from the start, not only after a rename")
	require.Equal(t, http.StatusMultiStatus, propfind("/dav/Garage%20S3/"))

	// The rename the reporter did.
	st.Name = "ps-hot"
	require.NoError(t, ha.store.UpdateStorage(context.Background(), st))

	require.Equal(t, http.StatusMultiStatus, propfind("/dav/"+st.UID+"/"),
		"a mount written against the uid broke on a rename — which is the whole point of the uid")
	require.Equal(t, http.StatusMultiStatus, propfind("/dav/ps-hot/"),
		"the new name must work too")
	require.Equal(t, http.StatusNotFound, propfind("/dav/Garage%20S3/"),
		"the old name must stop resolving: keeping it alive is a second address nobody declared")
}

// A name that happens to look like a uid still resolves as a name. Nothing
// forbids it, and an install that worked before the uid existed has to keep
// working after it.
func TestDav_AUIDShapedNameStillResolves(t *testing.T) {
	ha := newHarness(t)
	const looksLikeOne = "11111111-2222-4333-8444-555555555555"
	st := ha.addStorage(t, looksLikeOne, false, false)
	require.NotEqual(t, looksLikeOne, st.UID)

	resp := ha.req(t, "PROPFIND", "/dav/"+looksLikeOne+"/", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Depth": "1"})
	defer resp.Body.Close()
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode,
		"a storage whose NAME is uuid-shaped became unreachable")
}
