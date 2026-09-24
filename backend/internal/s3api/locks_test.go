package s3api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// TestFrozenObjectIsNotChangedOverS3: no PUT, copy or DELETE changes an
// object an app has frozen.
func TestFrozenObjectIsNotChangedOverS3(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "imza/NDA.docx", []byte("the document under signature"))
	hz.writeFile(t, st, "other.txt", []byte("x"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	until := time.Now().Add(time.Hour)
	if err := hz.store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/imza/NDA.docx"), Rel: "imza/NDA.docx",
		PluginID: 991, PluginName: "sign", Until: &until,
	}); err != nil {
		t.Fatal(err)
	}

	if rec := hz.put(t, key, "https://s3.filex.test/main/imza/NDA.docx", []byte("saved over")); rec.Code == http.StatusOK {
		t.Error("a PUT over the frozen object = 200")
	}
	if rec := hz.copy(t, key, "https://s3.filex.test/main/imza/NDA.docx", "/main/other.txt"); rec.Code == http.StatusOK {
		t.Error("a copy onto the frozen object = 200")
	}
	if rec := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main/imza/NDA.docx"); rec.Code < 300 {
		t.Errorf("a DELETE of the frozen object = %d", rec.Code)
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "imza", "NDA.docx"))
	if err != nil || string(got) != "the document under signature" {
		t.Fatalf("the frozen document was changed: %q (%v)", got, err)
	}
}
