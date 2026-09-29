package s3api_test

// A key minted FROM a token is that token projected into S3
// (protocolauth.IssueRequest.Token): its folder, its expiry — and its verbs. A
// key whose token holds only `read` is a read-only key; one without `delete`
// cannot remove. A key minted with no token is its owner's, unchanged.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/testutil"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
)

func TestKeyInheritsItsTokensVerbs(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	for name, body := range map[string]string{"keep.txt": "keep", "gone.txt": "gone"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	keyFor := func(scopes string) *protocolauth.IssuedKey {
		secret := testutil.NewAPIToken(t, hz.store, u.ID, scopes)
		tok, err := hz.store.GetAPITokenByHash(context.Background(), apitoken.HashToken(secret))
		if err != nil || tok == nil {
			t.Fatalf("token lookup: %v", err)
		}
		return hz.key(t, u, protocolauth.IssueRequest{Label: scopes, Token: tok})
	}
	const base = "https://s3.filex.test/main/"

	ro := keyFor("read")
	if rec := hz.do(t, ro, http.MethodGet, base+"keep.txt"); rec.Code != http.StatusOK {
		t.Fatalf("read key: GET = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := hz.put(t, ro, base+"ro-new.txt", []byte("x")); rec.Code == http.StatusOK || exists("ro-new.txt") {
		t.Errorf("read key: PUT = %d, want a refusal", rec.Code)
	}
	if rec := recorderFor(hz, signed(t, ro, http.MethodDelete, base+"keep.txt", hz.at)); rec.Code == http.StatusNoContent || !exists("keep.txt") {
		t.Errorf("read key: DELETE = %d, want a refusal", rec.Code)
	}

	rw := keyFor("read,write")
	if rec := hz.put(t, rw, base+"rw-new.txt", []byte("x")); rec.Code != http.StatusOK || !exists("rw-new.txt") {
		t.Errorf("read,write key: PUT = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := recorderFor(hz, signed(t, rw, http.MethodDelete, base+"gone.txt", hz.at)); rec.Code == http.StatusNoContent || !exists("gone.txt") {
		t.Errorf("read,write key: DELETE = %d, want a refusal", rec.Code)
	}

	rd := keyFor("read,delete")
	if rec := recorderFor(hz, signed(t, rd, http.MethodDelete, base+"gone.txt", hz.at)); rec.Code != http.StatusNoContent || exists("gone.txt") {
		t.Errorf("read,delete key: DELETE = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := hz.put(t, rd, base+"rd-new.txt", []byte("x")); rec.Code == http.StatusOK || exists("rd-new.txt") {
		t.Errorf("read,delete key: PUT = %d, want a refusal", rec.Code)
	}

	blind := keyFor("write,delete")
	if rec := hz.do(t, blind, http.MethodGet, base+"keep.txt"); rec.Code == http.StatusOK {
		t.Errorf("a key without read: GET = %d, want a refusal", rec.Code)
	}

	// A key with no parent token is its owner's own credential, unchanged.
	own := hz.key(t, u, protocolauth.IssueRequest{Label: "own"})
	if rec := hz.put(t, own, base+"own.txt", []byte("x")); rec.Code != http.StatusOK {
		t.Errorf("unparented key: PUT = %d: %s", rec.Code, rec.Body.String())
	}
}
