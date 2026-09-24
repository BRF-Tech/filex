package sftpsrv_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestFrozenFileIsNotChangedOverSFTP: the freeze holds for a session that was
// already open when the app took it (an editor that mounted the share first).
func TestFrozenFileIsNotChangedOverSFTP(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "imza/NDA.docx", []byte("the document under signature"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	if _, err := cl.ReadDir("/main/imza"); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	until := time.Now().Add(time.Hour)
	if err := hz.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Until: &until,
	}); err != nil {
		t.Fatal(err)
	}

	if w, err := cl.Create("/main/imza/NDA.docx"); err == nil {
		_, _ = w.Write([]byte("saved over the mount"))
		_ = w.Close()
		t.Error("an overwrite of the frozen file was accepted")
	}
	if err := cl.Rename("/main/imza/NDA.docx", "/main/NDA.docx"); err == nil {
		t.Error("the frozen file was renamed")
	}
	if err := cl.Remove("/main/imza/NDA.docx"); err == nil {
		t.Error("the frozen file was deleted")
	}
	if err := cl.Rename("/main/imza", "/main/imza-eski"); err == nil {
		t.Error("the folder holding the frozen file was renamed")
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "imza", "NDA.docx"))
	if err != nil || string(got) != "the document under signature" {
		t.Fatalf("the frozen document was changed: %q (%v)", got, err)
	}
}
