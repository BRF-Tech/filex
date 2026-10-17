package s3api_test

// CopyObject puts the source's bytes at another key. Where a role's part
// refuses files.download on the source, a copy to a key where the same caller
// could download them is AccessDenied (protoperm.DownloadLifted); a copy that
// stays where download is refused, and a copy of any other object, work.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

func TestS3_CopyObjectKeepsADownloadRule(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "p@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Locked/secret.txt", []byte("kept"))
	hz.writeFile(t, st, "report.txt", []byte("q3 numbers"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	ctx := context.Background()
	rule, err := hz.store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Locked stays", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{st.ID}, Paths: []string{"Locked"}},
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := hz.store.SetUserCustomRole(ctx, u.ID, rule.ID); err != nil {
		t.Fatalf("give the role: %v", err)
	}
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
	root := hz.rootOf(t, st)

	// The rule holds on the object itself.
	if rec := hz.do(t, key, http.MethodGet, s3Base+"Locked/secret.txt"); rec.Code != http.StatusForbidden {
		t.Fatalf("GET under Locked: %d %s", rec.Code, rec.Body.String())
	}

	rec := hz.copy(t, key, s3Base+"out.txt", "/main/Locked/secret.txt")
	if rec.Code != http.StatusForbidden || errorCode(t, rec.Body.Bytes()) != "AccessDenied" {
		t.Fatalf("a copy out of Locked: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("the refused copy landed")
	}

	if rec := hz.copy(t, key, s3Base+"Locked/again.txt", "/main/Locked/secret.txt"); rec.Code != http.StatusOK {
		t.Fatalf("a copy inside Locked: %d %s", rec.Code, rec.Body.String())
	}
	if rec := hz.copy(t, key, s3Base+"report-copy.txt", "/main/report.txt"); rec.Code != http.StatusOK {
		t.Fatalf("a copy of an ordinary object: %d %s", rec.Code, rec.Body.String())
	}
}
