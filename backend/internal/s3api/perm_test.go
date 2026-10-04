package s3api_test

// Per-user permissions (internal/perm) over S3: access.s3 at signature
// verification, and each operation on the action it performs.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
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

// Who may encrypt (internal/e2epolicy) over S3. With the policy off, a PUT, a
// multipart upload or a CopyObject that would CREATE an encrypted folder's key
// file or a `.fxe` is AccessDenied and nothing lands; rewriting a key file
// that is there — a password change — and an ordinary object still work.
func TestPerm_S3EncryptionPolicy(t *testing.T) {
	hz, key, root := permS3(t)
	if err := os.MkdirAll(filepath.Join(root, "Kasa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	refused := func(what string, code int, body []byte, landed string) {
		t.Helper()
		if code != http.StatusForbidden || errorCode(t, body) != "AccessDenied" {
			t.Fatalf("%s: %d %s", what, code, body)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(landed))); !os.IsNotExist(err) {
			t.Fatalf("%s: %s landed", what, landed)
		}
	}
	rec := hz.put(t, key, s3Base+"Acik/.filex-e2e.json", []byte("{}"))
	refused("PUT of a key file", rec.Code, rec.Body.Bytes(), "Acik/.filex-e2e.json")
	rec = hz.put(t, key, s3Base+"rapor.pdf.fxe", []byte("x"))
	refused("PUT of a .fxe", rec.Code, rec.Body.Bytes(), "rapor.pdf.fxe")
	rec = recorderFor(hz, signedBody(t, key, http.MethodPost, s3Base+"buyuk.fxe?uploads", nil, hz.at))
	refused("a multipart upload of a .fxe", rec.Code, rec.Body.Bytes(), "buyuk.fxe")
	if n := hz.stagingDirs(t); n != 0 {
		t.Fatalf("a refused multipart upload left %d staging directories", n)
	}
	rec = hz.copy(t, key, s3Base+"kopya.fxe", "/main/report.txt")
	refused("CopyObject onto a new .fxe", rec.Code, rec.Body.Bytes(), "kopya.fxe")

	if rec := hz.put(t, key, s3Base+"Kasa/.filex-e2e.json", []byte(`{"v":2,"rewritten":true}`)); rec.Code != http.StatusOK {
		t.Fatalf("rewriting a key file that is there: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Kasa", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}
	if rec := hz.put(t, key, s3Base+"notlar.txt", []byte("plain")); rec.Code != http.StatusOK {
		t.Fatalf("an ordinary object: %d %s", rec.Code, rec.Body.String())
	}
}

// A folder named like an encryption is not the file. On an object store a
// "folder" is a prefix, and a PUT of the bare key puts the object beside it:
// uploading a directory marker named like a key file or a `.fxe` and then the
// object was a way to create one without asking (the Task 6 review, read from
// the s3 driver). A PUT onto such a folder is a create, so the rule is asked
// and, with the policy off, refuses — AccessDenied in the rule's words, not
// the local driver's failure to write over a folder.
func TestPerm_S3AFolderWithTheNameIsNotTheFile(t *testing.T) {
	hz, key, root := permS3(t)
	rels := []string{"Acik/.filex-e2e.json", "Acik/x.fxe"}
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	for _, rel := range rels {
		rec := hz.put(t, key, s3Base+rel, []byte("x"))
		if rec.Code != http.StatusForbidden || errorCode(t, rec.Body.Bytes()) != "AccessDenied" || !strings.Contains(rec.Body.String(), "encrypted") {
			t.Fatalf("PUT onto the folder %s: %d %s", rel, rec.Code, rec.Body.String())
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil || !fi.IsDir() {
			t.Fatalf("%s is no longer the folder: %v", rel, err)
		}
	}
}

// A rule that cannot be decided is the server's failure, not a refusal. With
// the policy unreadable (the store fails), a PUT, a multipart upload and a
// CopyObject that would create a `.fxe` answer InternalError (500) — not
// AccessDenied, which a client and an operator read as the policy doing its
// job — and nothing lands.
func TestPerm_S3UndecidedEncryptionIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, false, func(c *s3api.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	u := hz.user(t, "p@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("q3 numbers"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	failed := func(what string, code int, body []byte, landed string) {
		t.Helper()
		if code != http.StatusInternalServerError || errorCode(t, body) != "InternalError" || !strings.Contains(string(body), "could not check the encryption policy") {
			t.Fatalf("%s: %d %s", what, code, body)
		}
		if _, err := os.Stat(filepath.Join(root, landed)); !os.IsNotExist(err) {
			t.Fatalf("%s: %s landed", what, landed)
		}
	}
	rec := hz.put(t, key, s3Base+"yeni.fxe", []byte("x"))
	failed("PUT of a .fxe", rec.Code, rec.Body.Bytes(), "yeni.fxe")
	rec = recorderFor(hz, signedBody(t, key, http.MethodPost, s3Base+"buyuk.fxe?uploads", nil, hz.at))
	failed("a multipart upload of a .fxe", rec.Code, rec.Body.Bytes(), "buyuk.fxe")
	if n := hz.stagingDirs(t); n != 0 {
		t.Fatalf("an undecided multipart upload left %d staging directories", n)
	}
	rec = hz.copy(t, key, s3Base+"kopya.fxe", "/main/report.txt")
	failed("CopyObject onto a new .fxe", rec.Code, rec.Body.Bytes(), "kopya.fxe")

	if rec := hz.put(t, key, s3Base+"notlar.txt", []byte("plain")); rec.Code != http.StatusOK {
		t.Fatalf("an ordinary object: %d %s", rec.Code, rec.Body.String())
	}
}

// A handler built without the router's rule still asks one: NewHandler builds
// it from its store. Nil used to mean "not wired, allow", so losing the line
// in routes.go that hands the rule over switched it off for S3 under every
// policy, and nothing said so.
func TestPerm_S3AsksTheRuleWhenNobodyWiredIt(t *testing.T) {
	hz := newHarnessCfg(t, false, func(c *s3api.Config) { c.E2EPolicy = nil })
	u := hz.user(t, "p@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	rec := hz.put(t, key, s3Base+"yeni.fxe", []byte("x"))
	if rec.Code != http.StatusForbidden || errorCode(t, rec.Body.Bytes()) != "AccessDenied" {
		t.Fatalf("a .fxe with the policy off and no rule handed over: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
}

// approvalFor puts the install under the approval policy and approves one
// `.fxe` at the root of st for u — what an administrator's "approve" leaves.
func approvalFor(t *testing.T, hz *harness, u *model.User, st *model.Storage) *model.E2ERequest {
	t.Helper()
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyApproval); err != nil {
		t.Fatal(err)
	}
	return dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "", model.E2ERequestFile)
}

// A request a cheap, fixed check refuses anyway does not spend an approval:
// the rule's question spends it, and a write that fails afterwards does not
// give it back. CopyObject checks its source — there, and not a folder —
// CreateMultipartUpload its staging area, and a PUT whether the storage takes
// writes at all, each before the question. Every such refusal leaves the
// approval as it was; the copy that goes through then spends it.
func TestPerm_S3ARequestRefusedAnywaySpendsNoApproval(t *testing.T) {
	ctx := context.Background()
	still := func(t *testing.T, hz *harness, r *model.E2ERequest, what string) {
		t.Helper()
		if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestApproved {
			t.Fatalf("%s spent the approval (%s)", what, got)
		}
	}

	t.Run("CopyObject", func(t *testing.T) {
		hz, key, root := permS3(t)
		if err := os.MkdirAll(filepath.Join(root, "Klasor"), 0o755); err != nil {
			t.Fatal(err)
		}
		u, err := hz.store.GetUserByEmail(ctx, "p@example.com")
		if err != nil {
			t.Fatal(err)
		}
		st, err := hz.store.GetStorageByName(ctx, "main")
		if err != nil {
			t.Fatal(err)
		}
		r := approvalFor(t, hz, u, st)
		for _, c := range []struct{ what, source string }{
			{"a copy from a source that is not there", "/main/yok.txt"},
			{"a copy from a folder", "/main/Klasor"},
		} {
			rec := hz.copy(t, key, s3Base+"kopya.fxe", c.source)
			if rec.Code != http.StatusNotFound || errorCode(t, rec.Body.Bytes()) != "NoSuchKey" {
				t.Fatalf("%s: %d %s", c.what, rec.Code, rec.Body.String())
			}
			still(t, hz, r, c.what)
		}
		if rec := hz.copy(t, key, s3Base+"kopya.fxe", "/main/report.txt"); rec.Code != http.StatusOK {
			t.Fatalf("the copy that goes through: %d %s", rec.Code, rec.Body.String())
		}
		if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
			t.Fatalf("the copy that went through left the approval %s", got)
		}
	})
	t.Run("CreateMultipartUpload with nowhere to stage", func(t *testing.T) {
		hz := newHarnessCfg(t, false, func(c *s3api.Config) { c.Staging = nil })
		u := hz.user(t, "p@example.com", model.RoleUser)
		st := hz.storage(t, "main")
		key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
		r := approvalFor(t, hz, u, st)
		rec := recorderFor(hz, signedBody(t, key, http.MethodPost, s3Base+"buyuk.fxe?uploads", nil, hz.at))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("a multipart upload with no staging area: %d %s", rec.Code, rec.Body.String())
		}
		still(t, hz, r, "a multipart upload with no staging area")
	})
	t.Run("PutObject to a storage that takes no writes", func(t *testing.T) {
		hz := newHarness(t, false)
		u := hz.user(t, "p@example.com", model.RoleUser)
		caps := testplugin.FullCaps()
		caps.Write, caps.Delete = false, false
		st := hz.pluginBucket(t, testplugin.Start(t, testplugin.WithCaps(caps)), "eklenti")
		key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
		r := approvalFor(t, hz, u, st)
		rec := hz.put(t, key, "https://s3.filex.test/eklenti/yeni.fxe", []byte("x"))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("a PUT to a read-only plugin: %d %s", rec.Code, rec.Body.String())
		}
		still(t, hz, r, "a PUT the storage cannot take")
	})
}

// Who may encrypt lets through what it should, and spends an approval once.
// Under the default policy (permitted) a plain member PUTs a `.fxe`: an
// upgrade changes nobody's access. Under the approval policy one approval for
// a folder lets exactly one key file be made there: the request turns used,
// the next key file — one folder down, which the same approval would have
// covered — is AccessDenied, and rewriting the key file that is there is not
// asked at all (were it asked, nothing is left to spend). A multipart upload
// is asked once, when it is created: its parts and its completion are not.
func TestPerm_S3EncryptionPolicyLetsThePermittedThrough(t *testing.T) {
	hz, key, root := permS3(t)
	ctx := context.Background()
	u, err := hz.store.GetUserByEmail(ctx, "p@example.com")
	if err != nil {
		t.Fatal(err)
	}
	st, err := hz.store.GetStorageByName(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Acik", "Alt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rec := hz.put(t, key, s3Base+"Acik/izinli.fxe", []byte("x")); rec.Code != http.StatusOK {
		t.Fatalf("a .fxe under the default policy: %d %s", rec.Code, rec.Body.String())
	}

	if err := hz.store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval); err != nil {
		t.Fatal(err)
	}
	r := dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)
	if rec := hz.put(t, key, s3Base+"Acik/.filex-e2e.json", []byte(`{"v":2}`)); rec.Code != http.StatusOK {
		t.Fatalf("the approved key file: %d %s", rec.Code, rec.Body.String())
	}
	if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
		t.Fatalf("the approval was not spent (%s)", got)
	}
	rec := hz.put(t, key, s3Base+"Acik/Alt/.filex-e2e.json", []byte(`{"v":2}`))
	if rec.Code != http.StatusForbidden || errorCode(t, rec.Body.Bytes()) != "AccessDenied" {
		t.Fatalf("a second key file on one approval: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Acik", "Alt", ".filex-e2e.json")); !os.IsNotExist(err) {
		t.Fatal("the second key file landed")
	}
	if rec := hz.put(t, key, s3Base+"Acik/.filex-e2e.json", []byte(`{"v":2,"rewritten":true}`)); rec.Code != http.StatusOK {
		t.Fatalf("rewriting the key file that is there: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Acik", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}

	r = dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "Acik", model.E2ERequestFile)
	url := s3Base + "Acik/buyuk.fxe"
	uploadID := initiate(t, hz, key, url)
	if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
		t.Fatalf("creating the multipart upload did not spend the approval (%s)", got)
	}
	part := uploadPart(t, hz, key, url, uploadID, 1, []byte("cipher"))
	if part.Code != http.StatusOK {
		t.Fatalf("a part of the approved upload was asked again: %d %s", part.Code, part.Body.String())
	}
	if rec := complete(t, hz, key, url, uploadID, map[int]string{1: part.Header().Get("ETag")}); rec.Code != http.StatusOK {
		t.Fatalf("completing the approved upload was asked again: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Acik", "buyuk.fxe")); string(got) != "cipher" {
		t.Fatalf("buyuk.fxe = %q", got)
	}
}
