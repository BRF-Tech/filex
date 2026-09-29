package nfssrv_test

// An export minted FROM a token inherits that token — its folder, its expiry
// and its verbs (protocolauth.IssueExportRequest.Token). A read-only token's
// export is a read-only mount; one without `delete` cannot remove.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestExportInheritsItsTokensVerbs(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "keep.txt", []byte("keep"))
	hz.writeFile(t, st, "gone.txt", []byte("gone"))
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	parent := func(scopes string) *model.APIToken {
		secret := testutil.NewAPIToken(t, hz.store, u.ID, scopes)
		tok, err := hz.store.GetAPITokenByHash(context.Background(), hashOf(secret))
		if err != nil || tok == nil {
			t.Fatalf("token lookup: %v", err)
		}
		return tok
	}

	ro := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main", Token: parent("read")}))
	if f, err := ro.Open("/keep.txt"); err != nil {
		t.Fatalf("read token export: open: %v", err)
	} else {
		f.Close()
	}
	if _, err := ro.Mkdir("/ro-dir", 0o755); err == nil || exists("ro-dir") {
		t.Errorf("a read token's export created a folder (err=%v)", err)
	}
	if w, err := ro.OpenFile("/ro-new.txt", 0o644); err == nil {
		_, _ = w.Write([]byte("x"))
		_ = w.Close()
	}
	if exists("ro-new.txt") {
		t.Error("a read token's export wrote a file")
	}
	if err := ro.Remove("/keep.txt"); err == nil || !exists("keep.txt") {
		t.Errorf("a read token's export deleted a file (err=%v)", err)
	}

	rw := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main", Token: parent("read,write")}))
	if _, err := rw.Mkdir("/rw-dir", 0o755); err != nil || !exists("rw-dir") {
		t.Errorf("read,write export: mkdir: %v", err)
	}
	if err := rw.Remove("/gone.txt"); err == nil || !exists("gone.txt") {
		t.Errorf("a read,write token's export deleted a file (err=%v)", err)
	}

	rd := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main", Token: parent("read,delete")}))
	if err := rd.Remove("/gone.txt"); err != nil || exists("gone.txt") {
		t.Errorf("read,delete export: remove: %v", err)
	}
	if _, err := rd.Mkdir("/rd-dir", 0o755); err == nil || exists("rd-dir") {
		t.Errorf("a read,delete token's export created a folder (err=%v)", err)
	}
}
