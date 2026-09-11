package dav

// Issue #21 carried a second symptom nobody had explained: every PROPFIND of a
// storage whose name contains a SPACE answered 404.
//
//	PROPFIND path="/dav/Garage S3/" status=404
//
// This is the measurement. If a spaced name is reachable, the reporter's 404 is
// something else and the test simply guards the name shape.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDav_StorageNameWithASpaceIsReachable(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "Garage S3", false, false)

	// What a client sends on the wire: the space percent-encoded.
	resp := ha.req(t, "PROPFIND", "/dav/Garage%20S3/", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Depth": "1"})
	defer resp.Body.Close()
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode,
		"a storage whose name has a space must be reachable over WebDAV")

	// And the raw form some clients still send.
	resp2 := ha.req(t, "PROPFIND", "/dav/Garage S3/", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Depth": "1"})
	defer resp2.Body.Close()
	require.Equal(t, http.StatusMultiStatus, resp2.StatusCode,
		"an unencoded space must not 404 either")
}
