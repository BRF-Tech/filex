package dav

import (
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/diskfree"
)

// quotaPropfind is the request Windows' WebDAV client sends for a mapped
// drive's free space.
const quotaPropfind = `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:"><D:prop><D:quota-available-bytes/><D:quota-used-bytes/></D:prop></D:propfind>`

// quotaOf reads the two properties off a 207 for one resource.
func quotaOf(t *testing.T, resp *http.Response) (avail, used uint64) {
	t.Helper()
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	var ms struct {
		Responses []struct {
			Propstat []struct {
				Prop struct {
					Avail string `xml:"quota-available-bytes"`
					Used  string `xml:"quota-used-bytes"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	require.NoError(t, xml.Unmarshal([]byte(bodyString(t, resp)), &ms))
	require.NotEmpty(t, ms.Responses)
	var a, u string
	for _, ps := range ms.Responses[0].Propstat {
		if ps.Prop.Avail != "" {
			a = ps.Prop.Avail
		}
		if ps.Prop.Used != "" {
			u = ps.Prop.Used
		}
	}
	avail, err := strconv.ParseUint(a, 10, 64)
	require.NoError(t, err, "quota-available-bytes = %q", a)
	used, err = strconv.ParseUint(u, 10, 64)
	require.NoError(t, err, "quota-used-bytes = %q", u)
	return avail, used
}

// Issue #116, the /dav side: it answered no quota at all, so a mapped drive
// showed whatever the client guessed.
func TestPropfind_AStoragesQuotaIsItsDisk(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "main", false, false)
	// The measurement rests on the test's own data: on an empty file system
	// the disk's used is 0 and that is true (the build host's -race job runs
	// on a fresh tmpfs: "used = 0 with 3221225472 available, on a
	// 3221225472-byte disk"). A megabyte the storage holds is on its disk
	// whatever else is - random bytes, so no file system compresses it away.
	var cfg struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	const held = 1 << 20
	fill := make([]byte, held)
	_, err := rand.Read(fill)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Path, "fill.bin"), fill, 0o644))
	disk, err := diskfree.Measure(cfg.Path)
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	resp := ha.req(t, "PROPFIND", "/dav/main/", ha.adminEmail, ha.adminPass, quotaPropfind, map[string]string{"Depth": "0"})
	avail, used := quotaOf(t, resp)
	if avail == 0 || avail > disk.Total {
		t.Errorf("available = %d, the disk is %d bytes", avail, disk.Total)
	}
	if used < held || avail+used > disk.Total {
		t.Errorf("used = %d with %d available, on a %d-byte disk holding a %d-byte file", used, avail, disk.Total, held)
	}
}

// A drive mapped to /dav itself: the storages added up, one disk counted once.
func TestPropfind_TheRootsQuotaCountsADiskOnce(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "main", false, false)
	ha.addStorage(t, "docs", false, false)
	disk, err := diskfree.Measure(t.TempDir())
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	resp := ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, ha.adminPass, quotaPropfind, map[string]string{"Depth": "0"})
	avail, used := quotaOf(t, resp)
	if avail+used > disk.Total {
		t.Errorf("available %d + used %d exceed the one disk (%d): it was counted twice", avail, used, disk.Total)
	}
}

// RFC 4331 §3: the quota SHOULD NOT be in an allprop answer, and a listing that
// did not ask for it must not pay for measuring it.
func TestPropfind_AllpropDoesNotCarryTheQuota(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "main", false, false)
	for name, body := range map[string]string{
		"allprop": `<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`,
		"no body": "",
	} {
		resp := ha.req(t, "PROPFIND", "/dav/main/", ha.adminEmail, ha.adminPass, body, map[string]string{"Depth": "1"})
		require.Equal(t, http.StatusMultiStatus, resp.StatusCode, name)
		if got := bodyString(t, resp); strings.Contains(got, "quota-available-bytes") {
			t.Errorf("%s: the answer carries the quota:\n%s", name, got)
		}
	}
}
