package nfssrv_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// TestFrozenFileIsNotChangedOverNFS: the freeze holds for a mount that was
// already up when the app took it.
func TestFrozenFileIsNotChangedOverNFS(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "imza/NDA.docx", []byte("the document under signature"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	if _, err := target.ReadDirPlus("/imza"); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	until := time.Now().Add(time.Hour)
	if err := hz.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Until: &until,
	}); err != nil {
		t.Fatal(err)
	}

	if wr, err := target.OpenFile("/imza/NDA.docx", 0o644); err == nil {
		_, _ = wr.Write([]byte("saved over the mount"))
		_ = wr.Close()
	}
	if err := target.Rename("/imza/NDA.docx", "/NDA.docx"); err == nil {
		t.Error("the frozen file was renamed")
	}
	if err := target.Remove("/imza/NDA.docx"); err == nil {
		t.Error("the frozen file was deleted")
	}
	if err := target.Rename("/imza", "/imza-eski"); err == nil {
		t.Error("the folder holding the frozen file was renamed")
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "imza", "NDA.docx"))
	if err != nil || string(got) != "the document under signature" {
		t.Fatalf("the frozen document was changed: %q (%v)", got, err)
	}
}
