package dav

// A PROPPATCH must never touch a file's bytes.
//
// ⚠⚠ Measured 2026-09-25 with Windows 11's own WebDAV client (a drive mapped
// with the desktop app's "Mount as a drive"): every file saved through the
// drive landed on the server EMPTY. Windows writes a new file as
//
//	PUT (0 bytes) → LOCK → PUT (the bytes) → PROPPATCH (Win32 times) → UNLOCK
//
// and the PROPPATCH was the one that emptied it: x/net/webdav opens the target
// with os.O_RDWR to look for a DeadPropsHolder, OpenFile handed back the
// upload spool, and its Close committed that empty spool over the file that
// had just been written. The PUT answered 201, the PROPPATCH 207 — nothing on
// the wire said anything was wrong.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The exact body Windows 11 (Microsoft-WebDAV-MiniRedir/10.0.26200) sent.
const win32PropPatch = `<?xml version="1.0" encoding="utf-8" ?><D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop><Z:Win32CreationTime>Fri, 25 Sep 2026 09:40:11 GMT</Z:Win32CreationTime><Z:Win32LastAccessTime>Fri, 25 Sep 2026 09:40:11 GMT</Z:Win32LastAccessTime><Z:Win32LastModifiedTime>Fri, 25 Sep 2026 09:40:11 GMT</Z:Win32LastModifiedTime><Z:Win32FileAttributes>00000020</Z:Win32FileAttributes></D:prop></D:set></D:propertyupdate>`

func TestDav_PropPatchAfterPutKeepsTheBytes(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)

	put := ha.req(t, http.MethodPut, "/dav/depo/report.txt", ha.adminEmail, ha.adminPass, "saved through the drive", nil)
	put.Body.Close()
	require.Equal(t, http.StatusCreated, put.StatusCode)

	pp := ha.req(t, "PROPPATCH", "/dav/depo/report.txt", ha.adminEmail, ha.adminPass, win32PropPatch,
		map[string]string{"Content-Type": `text/xml; charset="utf-8"`})
	pp.Body.Close()
	require.Equal(t, http.StatusMultiStatus, pp.StatusCode)

	got, err := os.ReadFile(filepath.Join(root, "report.txt"))
	require.NoError(t, err)
	require.Equal(t, "saved through the drive", string(got), "PROPPATCH emptied the file it was only meant to describe")
}

// The guard on the other side of the fix: a PROPPATCH on a path that does not
// exist must not become a way to create an empty file.
func TestDav_PropPatchDoesNotCreateAFile(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)

	pp := ha.req(t, "PROPPATCH", "/dav/depo/never-uploaded.txt", ha.adminEmail, ha.adminPass, win32PropPatch,
		map[string]string{"Content-Type": `text/xml; charset="utf-8"`})
	pp.Body.Close()

	_, err := os.Stat(filepath.Join(root, "never-uploaded.txt"))
	require.True(t, os.IsNotExist(err), "PROPPATCH created a file that was never uploaded (status %d)", pp.StatusCode)
}
