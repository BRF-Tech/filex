package dav

// sec055: a WebDAV MOVE waits for the storage's row gate on its request and
// asks its preconditions again once it holds it. x/net/webdav checks the
// destination (Overwrite: F, or a destination it removed for Overwrite: T)
// before it calls Rename; a MOVE that then waited behind a storage judgement
// used to make the move on that old answer, and the local driver's Move (like
// every driver's) replaces what holds the name - a file that landed on the
// destination meanwhile was overwritten.
//
// Break: drop the protocolsync.StillFree check from davFS.Rename's Relocate
// call - the late MOVE replaces the new file.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/rowgate"
)

func TestALateMoveOverDAVDoesNotReplaceAFileThatLandedOnItsDestination(t *testing.T) {
	ha, gated := gatedHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	d := gated(st) // disarmed: nothing stops half way here

	resp := ha.req(t, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "old", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	row := liveRowAt(ha.store, st.ID, "a.txt")
	require.NotNil(t, row, "the PUT did not catalogue the file")

	// A storage scan is judging the storage: it holds the row gate.
	judged := rowgate.Judge(st.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			judged()
		}
	}
	t.Cleanup(letGo)

	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := ha.davDo("MOVE", "/dav/depo/a.txt", map[string]string{
			"Destination": ha.srv.URL + "/dav/depo/b.txt",
			"Overwrite":   "F",
		})
		done <- answer{code, err}
	}()
	// The MOVE found b.txt free and now waits for the gate. A file lands on
	// the name meanwhile (another client, written straight to the storage).
	time.Sleep(300 * time.Millisecond)
	ctx := context.Background()
	require.NoError(t, d.Write(ctx, "b.txt", strings.NewReader("new"), 3))
	letGo()

	var got answer
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the MOVE never answered")
	}
	require.NoError(t, got.err)
	require.NotEqual(t, http.StatusCreated, got.code, "the late MOVE answered as if it had moved")
	require.NotEqual(t, http.StatusNoContent, got.code, "the late MOVE answered as if it had replaced")

	read := func(rel string) string {
		rc, err := d.Read(ctx, rel)
		require.NoError(t, err)
		defer rc.Close()
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		return string(b)
	}
	require.Equal(t, "new", read("b.txt"), "the late MOVE replaced the file that landed on its destination")
	require.Equal(t, "old", read("a.txt"), "the refused MOVE moved its source")
	n := liveRowAt(ha.store, st.ID, "a.txt")
	require.NotNil(t, n, "the refused MOVE moved the source's row")
	require.Equal(t, row.ID, n.ID)
}
