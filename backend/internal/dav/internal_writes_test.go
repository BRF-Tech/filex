package dav

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestInternalDirsAreNotWritable: no MKCOL, PUT, MOVE or COPY lands anything
// in filex's own directories over WebDAV.
func TestInternalDirsAreNotWritable(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	resp := ha.req(t, http.MethodPut, "/dav/depo/doc.txt", ha.adminEmail, ha.adminPass, "x", nil)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("precondition: PUT doc.txt = %d", resp.StatusCode)
	}

	for _, d := range syspath.Dirs() {
		for _, c := range []struct {
			method, path string
			hdr          map[string]string
		}{
			{"MKCOL", "/dav/depo/" + d, nil},
			{http.MethodPut, "/dav/depo/" + d + "/x.txt", nil},
			{"MOVE", "/dav/depo/doc.txt", map[string]string{"Destination": "/dav/depo/" + d}},
			{"COPY", "/dav/depo/doc.txt", map[string]string{"Destination": "/dav/depo/" + d + "/doc.txt"}},
		} {
			resp := ha.req(t, c.method, c.path, ha.adminEmail, ha.adminPass, "x", c.hdr)
			resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Errorf("%s %s %v = %d", c.method, c.path, c.hdr, resp.StatusCode)
			}
		}
		if _, err := os.Stat(filepath.Join(root, d)); err == nil {
			t.Errorf("%s exists on the storage", d)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "doc.txt")); err != nil {
		t.Errorf("the document moved: %v", err)
	}
}
