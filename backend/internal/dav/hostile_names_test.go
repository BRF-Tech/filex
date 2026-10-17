package dav

// The hostile names (task #238, internal/testutil/hostilenames) over WebDAV:
// every name a local disk holds goes in, is listed under its own name, comes
// back byte for byte and moves to a new name; the names no door may create
// are refused with a 4xx; a name too long for the disk is the client's
// mistake (400), not "method not allowed". And the gate and the FileSystem
// read one path the same way.

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// hnServer mounts the harness's handler the way production does - on chi,
// which neither cleans nor redirects a path - so a request reaches /dav with
// exactly the path the client sent.
func hnServer(t *testing.T, ha *harness) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Mount(Prefix, ha.h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// hnDo sends one request to srv with path written as it goes on the wire
// (already escaped) and returns the status and the body.
func hnDo(t *testing.T, srv *httptest.Server, method, rawPath, user, pass, body string, hdr map[string]string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+rawPath, rdr)
	require.NoError(t, err)
	req.SetBasicAuth(user, pass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

// hnPath is /dav/<storage>/<name> with the name escaped as one segment.
func hnPath(storage, name string) string {
	return Prefix + "/" + storage + "/" + url.PathEscape(name)
}

// propfindNames is every entry name a Depth: 1 PROPFIND of dir lists, read
// from the hrefs the way a client reads them (percent-decoded).
func propfindNames(t *testing.T, srv *httptest.Server, ha *harness, dir string) map[string]bool {
	t.Helper()
	status, body := hnDo(t, srv, "PROPFIND", dir, ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, status, body)
	var ms struct {
		Responses []struct {
			Href string `xml:"href"`
		} `xml:"response"`
	}
	require.NoError(t, xml.Unmarshal([]byte(body), &ms), "the listing is not valid XML")
	out := map[string]bool{}
	for _, r := range ms.Responses {
		p, err := url.PathUnescape(r.Href)
		require.NoError(t, err, r.Href)
		if p = strings.TrimPrefix(p, dir); p != "" && p != "/" {
			out[strings.TrimSuffix(p, "/")] = true
		}
	}
	return out
}

func TestDav_HostileNamesRoundTrip(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	srv := hnServer(t, ha)
	names := hostilenames.ForLocalDisk()

	for _, n := range names {
		body := "body of " + n.ID
		status, msg := hnDo(t, srv, http.MethodPut, hnPath("depo", n.Name), ha.adminEmail, ha.adminPass, body, nil)
		require.Equal(t, http.StatusCreated, status, "%s: PUT %q: %s", n.ID, n.Name, msg)
	}
	listed := propfindNames(t, srv, ha, Prefix+"/depo/")
	// Each name then moves into a folder of its own under the same name, so
	// the destination of the MOVE is the hostile name too (and a name at the
	// 255-byte limit stays inside it).
	status, msg := hnDo(t, srv, "MKCOL", Prefix+"/depo/moved", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, status, msg)
	for _, n := range names {
		t.Run(n.ID, func(t *testing.T) {
			assert.True(t, listed[n.Name], "%q is not in the listing under its own name", n.Name)

			status, got := hnDo(t, srv, http.MethodGet, hnPath("depo", n.Name), ha.adminEmail, ha.adminPass, "", nil)
			require.Equal(t, http.StatusOK, status, got)
			assert.Equal(t, "body of "+n.ID, got)
			_, err := os.Stat(filepath.Join(root, n.Name))
			require.NoError(t, err, "not on disk under its own bytes")

			moved := Prefix + "/depo/moved/" + url.PathEscape(n.Name)
			status, msg := hnDo(t, srv, "MOVE", hnPath("depo", n.Name), ha.adminEmail, ha.adminPass, "",
				map[string]string{"Destination": srv.URL + moved})
			require.Equal(t, http.StatusCreated, status, "MOVE into moved/: %s", msg)
			status, got = hnDo(t, srv, http.MethodGet, moved, ha.adminEmail, ha.adminPass, "", nil)
			require.Equal(t, http.StatusOK, status, got)
			assert.Equal(t, "body of "+n.ID, got)
			status, _ = hnDo(t, srv, http.MethodGet, hnPath("depo", n.Name), ha.adminEmail, ha.adminPass, "", nil)
			assert.Equal(t, http.StatusNotFound, status, "the old name is still there after the MOVE")
			assert.NotNil(t, ha.nodeByPath(t, st.ID, "moved/"+n.Name), "the catalogue did not follow the move")
		})
	}
}

// The deep path: 64 folders, made one MKCOL at a time, and the file at the
// bottom read back.
func TestDav_HostileDeepPath(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)
	srv := hnServer(t, ha)
	d := hostilenames.Deep()
	esc := func(rel string) string {
		segs := strings.Split(rel, "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		return Prefix + "/depo/" + strings.Join(segs, "/")
	}
	for _, dir := range d.Dirs() {
		status, msg := hnDo(t, srv, "MKCOL", esc(dir), ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusCreated, status, "MKCOL %d bytes deep: %s", len(dir), msg)
	}
	status, msg := hnDo(t, srv, http.MethodPut, esc(d.Path), ha.adminEmail, ha.adminPass, "at the bottom", nil)
	require.Equal(t, http.StatusCreated, status, msg)
	status, got := hnDo(t, srv, http.MethodGet, esc(d.Path), ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, status, got)
	assert.Equal(t, "at the bottom", got)
}

// The names no door may create (internal/leafname) are refused, and nothing
// lands on the disk. `..` is not a name to WebDAV at all - the path is
// cleaned, and sub/.. is the storage's root, which no PUT, MKCOL, MOVE or
// COPY may replace - so for it only "a 4xx and nothing new" is asked; every
// other one is a 400 that says why.
func TestDav_RefusedNamesAreRefused(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	srv := hnServer(t, ha)
	status, msg := hnDo(t, srv, "MKCOL", Prefix+"/depo/sub", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, status, msg)
	status, msg = hnDo(t, srv, http.MethodPut, Prefix+"/depo/sub/a.txt", ha.adminEmail, ha.adminPass, "a", nil)
	require.Equal(t, http.StatusCreated, status, msg)
	in := func(name string) string { return Prefix + "/depo/sub/" + url.PathEscape(name) }

	before := append(hnEntries(t, root), hnEntries(t, filepath.Join(root, "sub"))...)
	for _, n := range hostilenames.Refused() {
		t.Run(n.ID, func(t *testing.T) {
			for _, c := range []struct {
				method string
				path   string
				hdr    map[string]string
			}{
				{http.MethodPut, in(n.Name), nil},
				{"MKCOL", in(n.Name), nil},
				{"MOVE", in("a.txt"), map[string]string{"Destination": srv.URL + in(n.Name)}},
				{"COPY", in("a.txt"), map[string]string{"Destination": srv.URL + in(n.Name)}},
			} {
				body := ""
				if c.method == http.MethodPut {
					body = "x"
				}
				status, msg := hnDo(t, srv, c.method, c.path, ha.adminEmail, ha.adminPass, body, c.hdr)
				if n.ID == "dot-dot" {
					assert.True(t, status >= 400 && status < 500, "%s %q = %d: %s", c.method, n.Name, status, msg)
				} else {
					assert.Equal(t, http.StatusBadRequest, status, "%s %q: %s", c.method, n.Name, msg)
				}
			}
		})
	}
	after := append(hnEntries(t, root), hnEntries(t, filepath.Join(root, "sub"))...)
	assert.Equal(t, before, after, "a refused name reached the disk, or the storage lost an entry")
}

// hnEntries is the storage root's entries, filex's own left out.
func hnEntries(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	require.NoError(t, err)
	var out []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// A name longer than the disk holds is the client's to fix: 400 "too long",
// never the 405 x/net/webdav picks for a failed PUT or MKCOL (which a client
// reads as "uploads are not allowed here") or the 403 of a failed MOVE.
func TestDav_TooLongNameIsTheClientsMistake(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this host's file system holds every name in the list")
	}
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)
	srv := hnServer(t, ha)
	status, msg := hnDo(t, srv, http.MethodPut, hnPath("depo", "a.txt"), ha.adminEmail, ha.adminPass, "a", nil)
	require.Equal(t, http.StatusCreated, status, msg)

	for _, n := range long {
		t.Run(n.ID, func(t *testing.T) {
			for _, c := range []struct {
				method, path, body string
				hdr                map[string]string
			}{
				{http.MethodPut, hnPath("depo", n.Name), "x", nil},
				{"MKCOL", hnPath("depo", n.Name), "", nil},
				{"MOVE", hnPath("depo", "a.txt"), "", map[string]string{"Destination": srv.URL + hnPath("depo", n.Name)}},
				{"COPY", hnPath("depo", "a.txt"), "", map[string]string{"Destination": srv.URL + hnPath("depo", n.Name)}},
			} {
				status, msg := hnDo(t, srv, c.method, c.path, ha.adminEmail, ha.adminPass, c.body, c.hdr)
				assert.Equal(t, http.StatusBadRequest, status, "%s of a %d-byte name: %s", c.method, n.Bytes(), msg)
				assert.Contains(t, msg, "too long", "%s: the answer does not say why", c.method)
			}
		})
	}
	status, got := hnDo(t, srv, http.MethodGet, hnPath("depo", "a.txt"), ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "a", got, "the source of the refused MOVE and COPY was lost")
}

// The pre-gate and the FileSystem read one path the same way. A `..` that
// climbs above the storage segment used to have the gate judge the storage
// it was written under while the FileSystem acted on the one it reached, so
// what only the gate asks - files.download - was asked about another file.
func TestDav_GateJudgesTheFileTheRequestReaches(t *testing.T) {
	ha, _ := permHarness(t, perm.FilesDownload)
	srv := hnServer(t, ha)
	for _, p := range []string{
		"/dav/nope/%2e%2e/depo/report.txt",
		"/dav/nope/..%2fdepo/report.txt",
		"/dav/depo/sub/%2e%2e/%2e%2e/depo/report.txt",
	} {
		status, body := hnDo(t, srv, http.MethodGet, p, permUser, permPass, "", nil)
		assert.Equal(t, http.StatusForbidden, status, "GET %s without files.download: %s", p, body)
		assert.NotContains(t, body, "q3 numbers", "GET %s handed the bytes over", p)
	}
}

// The same for a folder's app lock, which only the gate asks: a DELETE of the
// folder through a `..` spelling is refused like the plain one.
func TestDav_FolderLockHoldsForEverySpelling(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	srv := hnServer(t, ha)
	for _, c := range []struct{ method, path, body string }{
		{"MKCOL", "/dav/depo/imza", ""},
		{http.MethodPut, "/dav/depo/imza/NDA.docx", "the document under signature"},
	} {
		status, msg := hnDo(t, srv, c.method, c.path, ha.adminEmail, ha.adminPass, c.body, nil)
		require.Less(t, status, 300, "precondition %s %s: %s", c.method, c.path, msg)
	}
	until := time.Now().Add(time.Hour)
	require.NoError(t, ha.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Reason: "signatures are being collected", Until: &until,
	}))
	for _, p := range []string{"/dav/depo/%2e%2e/depo/imza", "/dav/depo/x/..%2f..%2fdepo/imza"} {
		status, msg := hnDo(t, srv, http.MethodDelete, p, ha.adminEmail, ha.adminPass, "", nil)
		assert.Equal(t, http.StatusLocked, status, "DELETE %s: %s", p, msg)
	}
	got, err := os.ReadFile(filepath.Join(root, "imza", "NDA.docx"))
	require.NoError(t, err, "the frozen document is gone")
	assert.Equal(t, "the document under signature", string(got))
}

// RFC 4918: a COPY or MOVE onto itself is 403. Another spelling of the same
// path is the same resource - it used to pass the library's own test (which
// compares the paths as written), and the destination was removed before the
// source was read: the file went to the trash, or became empty.
func TestDav_CopyOrMoveOntoAnotherSpellingOfItself(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	srv := hnServer(t, ha)
	status, msg := hnDo(t, srv, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "original", nil)
	require.Equal(t, http.StatusCreated, status, msg)

	for _, method := range []string{"COPY", "MOVE"} {
		for _, dst := range []string{"/dav/depo/./a.txt", "/dav/depo//a.txt", "/dav/depo/sub/../a.txt"} {
			status, msg := hnDo(t, srv, method, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "",
				map[string]string{"Destination": srv.URL + dst, "Overwrite": "T"})
			assert.Equal(t, http.StatusForbidden, status, "%s onto %s: %s", method, dst, msg)
			got, err := os.ReadFile(filepath.Join(root, "a.txt"))
			require.NoError(t, err, "%s onto %s lost the file", method, dst)
			require.Equal(t, "original", string(got), "%s onto %s changed the file", method, dst)
		}
	}
}

// hnPlanted is every hostile name no door may GIVE, that a file can still
// HAVE on a Linux disk - planted there by something else (a script, an
// rsync, another program): the control characters but NUL.
func hnPlanted(t *testing.T) []hostilenames.Name {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot hold a control character in a file name")
	}
	out := hostilenames.Filter(func(n hostilenames.Name) bool {
		return n.Has("control") && !strings.ContainsRune(n.Name, 0)
	})
	require.NotEmpty(t, out, "no planted name to try")
	return out
}

// A file that already has a name no door may give it now stays reachable:
// listed under its own name, read, overwritten and deleted. Only a NEW name
// is refused (the owner's rule for 0.57, internal/leafname).
func TestDav_PlantedControlNamesStayReachable(t *testing.T) {
	planted := hnPlanted(t)
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	srv := hnServer(t, ha)
	for _, n := range planted {
		require.NoError(t, os.WriteFile(filepath.Join(root, n.Name), []byte("planted "+n.ID), 0o644))
	}
	listed := propfindNames(t, srv, ha, Prefix+"/depo/")
	for _, n := range planted {
		t.Run(n.ID, func(t *testing.T) {
			assert.True(t, listed[n.Name], "%q is not in the listing under its own name", n.Name)
			p := hnPath("depo", n.Name)
			status, got := hnDo(t, srv, http.MethodGet, p, ha.adminEmail, ha.adminPass, "", nil)
			require.Equal(t, http.StatusOK, status, got)
			assert.Equal(t, "planted "+n.ID, got)

			status, msg := hnDo(t, srv, http.MethodPut, p, ha.adminEmail, ha.adminPass, "rewritten", nil)
			require.Less(t, status, 300, "the file that has the name could not be overwritten: %s", msg)
			_, got = hnDo(t, srv, http.MethodGet, p, ha.adminEmail, ha.adminPass, "", nil)
			assert.Equal(t, "rewritten", got)

			status, msg = hnDo(t, srv, http.MethodPut, hnPath("depo", n.Name+" new"), ha.adminEmail, ha.adminPass, "x", nil)
			assert.Equal(t, http.StatusBadRequest, status, "a NEW name with the character was let through: %s", msg)

			status, msg = hnDo(t, srv, http.MethodDelete, p, ha.adminEmail, ha.adminPass, "", nil)
			require.Equal(t, http.StatusNoContent, status, msg)
			_, err := os.Stat(filepath.Join(root, n.Name))
			assert.True(t, os.IsNotExist(err), "still on the disk after DELETE")
		})
	}
}
