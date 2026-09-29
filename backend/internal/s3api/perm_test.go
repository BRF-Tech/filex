package s3api_test

// Per-user permissions (internal/perm) over S3: access.s3 at signature
// verification, and each operation on the action it performs.

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

const s3Base = "https://s3.filex.test/main/"

func (hz *harness) deny(t *testing.T, u *model.User, ps ...perm.Perm) {
	t.Helper()
	m := map[string]string{}
	for _, p := range ps {
		m[string(p)] = model.PermDeny
	}
	if err := hz.store.SetUserPermissionOverrides(context.Background(), u.ID, m, nil); err != nil {
		t.Fatalf("overrides: %v", err)
	}
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
}

func permS3(t *testing.T, ps ...perm.Perm) (*harness, *protocolauth.IssuedKey, string) {
	t.Helper()
	hz := newHarness(t, false)
	u := hz.user(t, "p@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("q3 numbers"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	hz.deny(t, u, ps...)
	return hz, key, root
}

func TestPerm_AccessS3GatesTheSignature(t *testing.T) {
	hz, key, _ := permS3(t, perm.AccessS3)
	rec := hz.do(t, key, http.MethodGet, s3Base+"report.txt")
	if rec.Code < 400 {
		t.Fatalf("an account without access.s3 read an object: %d", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code == "AccessDenied" || code == "NoSuchKey" {
		// A refused key must look like a key that does not exist — never
		// like a real key that lacks a permission.
		t.Fatalf("refusal leaks that the key is real: %s", code)
	}
}

func TestPerm_S3Operations(t *testing.T) {
	t.Run("download", func(t *testing.T) {
		hz, key, _ := permS3(t, perm.FilesDownload)
		if rec := hz.do(t, key, http.MethodGet, s3Base+"report.txt"); rec.Code != http.StatusForbidden {
			t.Fatalf("GET without files.download: %d %s", rec.Code, rec.Body.String())
		}
		if rec := hz.do(t, key, http.MethodHead, s3Base+"report.txt"); rec.Code != http.StatusOK {
			t.Fatalf("HEAD is metadata, not a download: %d", rec.Code)
		}
	})
	t.Run("delete", func(t *testing.T) {
		hz, key, root := permS3(t, perm.FilesDelete)
		if rec := hz.do(t, key, http.MethodDelete, s3Base+"report.txt"); rec.Code != http.StatusForbidden {
			t.Fatalf("DELETE without files.delete: %d", rec.Code)
		}
		if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
			t.Fatal("the object is gone")
		}
		if rec := hz.put(t, key, s3Base+"new.txt", []byte("x")); rec.Code != http.StatusOK {
			t.Fatalf("denying delete stopped a PUT: %d", rec.Code)
		}
	})
	t.Run("create", func(t *testing.T) {
		hz, key, _ := permS3(t, perm.FilesCreate)
		if rec := hz.put(t, key, s3Base+"new.txt", []byte("x")); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT of a new key without files.create: %d", rec.Code)
		}
		if rec := hz.put(t, key, s3Base+"report.txt", []byte("revised")); rec.Code != http.StatusOK {
			t.Fatalf("overwriting is files.modify, which is held: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("modify", func(t *testing.T) {
		hz, key, root := permS3(t, perm.FilesModify)
		if rec := hz.put(t, key, s3Base+"report.txt", []byte("tampered")); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT over an existing key without files.modify: %d", rec.Code)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "report.txt")); string(got) != "q3 numbers" {
			t.Fatalf("object changed: %q", got)
		}
		if rec := hz.put(t, key, s3Base+"fresh.txt", []byte("new")); rec.Code != http.StatusOK {
			t.Fatalf("a new key is files.create, which is held: %d", rec.Code)
		}
	})
}
