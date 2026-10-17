package sftpsrv_test

import (
	"context"
	"io"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A whole storage given to a person (#117) is a grant on the storage root
// (path prefix ""). SFTP asks the same acl.Set as every other door, so the
// storage appears at `/` once given and every folder in it opens, at the
// grant's level - the same answer the web interface and WebDAV give.
func TestStorageRootGrantOpensTheWholeStorage(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "member@example.com")
	st := hz.storage(t, "shared")
	st.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("update storage: %v", err)
	}
	hz.writeFile(t, st, "Team/deep/plan.txt", []byte("the deep plan"))

	if _, err := hz.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: "", IsDir: true, UserID: u.ID, Level: model.GrantViewer,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	cl := hz.mustDial(t, "member@example.com", testPassword)
	entries, err := cl.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir /: %v", err)
	}
	if got := names(entries); len(got) != 1 || got[0] != "shared" {
		t.Fatalf("root = %v, want the storage that was given", got)
	}

	f, err := cl.Open("/shared/Team/deep/plan.txt")
	if err != nil {
		t.Fatalf("a file two folders down, with no grant of its own: %v", err)
	}
	body, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(body) != "the deep plan" {
		t.Fatalf("read = %q, %v", body, err)
	}

	if _, err := cl.Create("/shared/Team/nope.txt"); err == nil {
		t.Fatal("the storage was given as viewer, and a file was created in it")
	}
}
