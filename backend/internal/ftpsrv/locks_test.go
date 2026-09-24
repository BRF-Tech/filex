package ftpsrv_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestFrozenFileIsNotChangedOverFTP: the freeze holds for a session that was
// already open when the app took it.
func TestFrozenFileIsNotChangedOverFTP(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "imza/NDA.docx", []byte("the document under signature"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	if _, err := c.List("/main/imza"); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	until := time.Now().Add(time.Hour)
	if err := hz.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Until: &until,
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Stor("/main/imza/NDA.docx", strings.NewReader("saved over the mount")); err == nil {
		t.Error("an overwrite of the frozen file was accepted")
	}
	if err := c.Rename("/main/imza/NDA.docx", "/main/NDA.docx"); err == nil {
		t.Error("the frozen file was renamed")
	}
	if err := c.Delete("/main/imza/NDA.docx"); err == nil {
		t.Error("the frozen file was deleted")
	}
	if err := c.Rename("/main/imza", "/main/imza-eski"); err == nil {
		t.Error("the folder holding the frozen file was renamed")
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "imza", "NDA.docx"))
	if err != nil || string(got) != "the document under signature" {
		t.Fatalf("the frozen document was changed: %q (%v)", got, err)
	}
}
