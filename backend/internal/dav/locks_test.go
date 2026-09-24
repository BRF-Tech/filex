package dav

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestFrozenFileIsNotChangedOverWebDAV: a document an app has frozen (the
// signing app's `files:lock`) is not overwritten, moved or deleted — nor is
// the folder that holds it — through a WebDAV mount. The classic case: Word
// opened it through a mapped drive before the freeze and saves after it.
func TestFrozenFileIsNotChangedOverWebDAV(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	for _, c := range []struct{ method, path, body string }{
		{"MKCOL", "/dav/depo/imza", ""},
		{http.MethodPut, "/dav/depo/imza/NDA.docx", "the document under signature"},
	} {
		resp := ha.req(t, c.method, c.path, ha.adminEmail, ha.adminPass, c.body, nil)
		resp.Body.Close()
		require.Less(t, resp.StatusCode, 300, "precondition %s %s", c.method, c.path)
	}
	until := time.Now().Add(time.Hour)
	require.NoError(t, ha.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Reason: "imzalar toplanıyor", Until: &until,
	}))

	for _, c := range []struct {
		method, path, body string
		hdr                map[string]string
	}{
		{http.MethodPut, "/dav/depo/imza/NDA.docx", "saved over the mount", nil},
		{"MOVE", "/dav/depo/imza/NDA.docx", "", map[string]string{"Destination": "/dav/depo/NDA.docx"}},
		{"DELETE", "/dav/depo/imza/NDA.docx", "", nil},
		{"DELETE", "/dav/depo/imza", "", nil},
		{"MOVE", "/dav/depo/imza", "", map[string]string{"Destination": "/dav/depo/imza-eski"}},
	} {
		resp := ha.req(t, c.method, c.path, ha.adminEmail, ha.adminPass, c.body, c.hdr)
		resp.Body.Close()
		// 423 Locked — WebDAV's own answer, which clients show as "the file is
		// in use" rather than as a permission problem.
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("%s %s = %d, want 423: the freeze did not hold", c.method, c.path, resp.StatusCode)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, "imza", "NDA.docx"))
	require.NoError(t, err, "the frozen document is gone")
	require.Equal(t, "the document under signature", string(got), "the frozen document was changed")
}
