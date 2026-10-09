package handlers

// #189 P1b: ONE rule says how a file row is end-to-end encrypted, wherever
// the row comes from (encryptedKind): the folder listing reads its two facts
// from the listing, every other row from e2eRoots. Red before 0.55's
// completion: encryptedKind, encryptedOf and stampRow did not exist (the
// listing had its own copy and no other row was stamped).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// fakeVaultRoots answers VaultRoot from a fixed list of vault folders.
type fakeVaultRoots struct {
	roots []string
	asked int
}

func (f *fakeVaultRoots) VaultRoot(_ context.Context, _ int64, rel string) (string, bool) {
	f.asked++
	for _, r := range f.roots {
		if rel == r || strings.HasPrefix(rel, r+"/") {
			return r, true
		}
	}
	return "", false
}

func TestEncryptedKind_TheOneRule(t *testing.T) {
	cases := []struct {
		isFile          bool
		name            string
		inFolder, vault bool
		want            string
	}{
		{true, "ek.bin", true, true, "vault"},
		{true, "rapor.docx", true, false, "folder"},
		{true, "acik.txt", false, false, ""},
		{false, "Kasa", true, true, ""},
		{false, "alt", true, false, ""},
		// sec055 S16: a single encrypted file outside every encrypted folder
		// is ciphertext too - "file"; where it sits wins when it is inside one.
		{true, "rapor.docx.fxe", false, false, "file"},
		{true, "RAPOR.FXE", false, false, "file"},
		{true, "rapor.docx.fxe", true, false, "folder"},
		{true, "rapor.docx.fxe", true, true, "vault"},
		{false, "klasor.fxe", false, false, ""},
		{true, ".fxe", false, false, ""},
	}
	for _, c := range cases {
		if got := encryptedKind(c.isFile, c.name, c.inFolder, c.vault); got != c.want {
			t.Errorf("encryptedKind(%v, %q, %v, %v) = %q, want %q", c.isFile, c.name, c.inFolder, c.vault, got, c.want)
		}
	}
}

// sec055 S16: a `.fxe` outside every encrypted folder says "file" on every
// row - the listing's and the others' (encryptedOf).
func TestEncryptedKind_ASingleEncryptedFileSaysFile(t *testing.T) {
	ctx := context.Background()
	roots := newE2eRoots(fakeMarkers{"sifreli/.filex-e2e.json": true})
	if got := roots.encryptedOf(ctx, 7, "/acik/rapor.docx.fxe", true); got != "file" {
		t.Errorf("a .fxe outside: got %q", got)
	}
	if got := roots.encryptedOf(ctx, 7, "/sifreli/rapor.docx.fxe", true); got != "folder" {
		t.Errorf("a .fxe inside an encrypted folder: got %q", got)
	}
	listing := []map[string]any{{"type": "file", "basename": "rapor.docx.fxe"}, {"type": "file", "basename": "acik.txt"}}
	stampListingEncrypted(listing, map[string]any{})
	if listing[0]["encrypted"] != "file" {
		t.Errorf("listing .fxe row: %v", listing[0])
	}
	if _, has := listing[1]["encrypted"]; has {
		t.Errorf("listing plain row: %v", listing[1])
	}
}

func TestE2eRoots_EncryptedOfTellsAVaultFromAFolder(t *testing.T) {
	ctx := context.Background()
	vaults := &fakeVaultRoots{roots: []string{"kasa"}}
	roots := newE2eRoots(fakeMarkers{"kasa/.filex-e2e.json": true, "sifreli/.filex-e2e.json": true}).withVaults(vaults)
	cases := map[string]string{
		"/kasa/ek.bin":           "vault",
		"/kasa/alt/derin.bin":    "vault",
		"/sifreli/rapor.docx":    "folder",
		"/sifreli/alt/not.txt":   "folder",
		"/acik/x.txt":            "",
		"/kasa-not-really/x.txt": "",
	}
	for p, want := range cases {
		if got := roots.encryptedOf(ctx, 7, p, true); got != want {
			t.Errorf("encryptedOf(%q) = %q, want %q", p, got, want)
		}
	}
	if got := roots.encryptedOf(ctx, 7, "/kasa/alt", false); got != "" {
		t.Errorf("a folder row: got %q", got)
	}
	// The vault question is asked once per encrypted folder, not per row.
	before := vaults.asked
	_ = roots.encryptedOf(ctx, 7, "/kasa/ikinci.bin", true)
	if vaults.asked != before {
		t.Errorf("the vault answer for kasa was not kept: asked %d more times", vaults.asked-before)
	}

	// No vault rule attached: an encrypted file is "folder", never a guess.
	plain := newE2eRoots(fakeMarkers{"kasa/.filex-e2e.json": true})
	if got := plain.encryptedOf(ctx, 7, "/kasa/ek.bin", true); got != "folder" {
		t.Errorf("without vaults: got %q", got)
	}
}

func TestStampRow_RootAndKindOnTheSameRow(t *testing.T) {
	ctx := context.Background()
	roots := newE2eRoots(fakeMarkers{"kasa/.filex-e2e.json": true, "sifreli/.filex-e2e.json": true}).
		withVaults(&fakeVaultRoots{roots: []string{"kasa"}})
	rows := []map[string]any{
		{"path": "alpha://kasa/ek.bin", "type": "file"},
		{"path": "alpha://sifreli/rapor.docx", "type": "file"},
		{"path": "alpha://sifreli/alt", "type": "dir"},
		{"path": "alpha://acik/plain.txt", "type": "file"},
	}
	annotateRowsE2e(ctx, roots, 7, "alpha", rows)
	if rows[0]["encrypted"] != "vault" || rows[0]["e2e_root"] != "alpha://kasa" {
		t.Errorf("vault row: %v", rows[0])
	}
	if rows[1]["encrypted"] != "folder" || rows[1]["e2e_root"] != "alpha://sifreli" {
		t.Errorf("folder row: %v", rows[1])
	}
	if _, has := rows[2]["encrypted"]; has || rows[2]["e2e_root"] != "alpha://sifreli" {
		t.Errorf("a folder inside says where it is, never `encrypted`: %v", rows[2])
	}
	if _, has := rows[3]["encrypted"]; has {
		t.Errorf("a plain row: %v", rows[3])
	}
}

func TestStampListingEncrypted_FromTheListingsOwnAnswer(t *testing.T) {
	files := func() []map[string]any {
		return []map[string]any{{"type": "file"}, {"type": "dir"}}
	}
	vault := files()
	stampListingEncrypted(vault, map[string]any{"e2e_root": "a://Kasa", "e2e_vault_root": "a://Kasa"})
	if vault[0]["encrypted"] != "vault" {
		t.Errorf("listing in a vault: %v", vault[0])
	}
	folder := files()
	stampListingEncrypted(folder, map[string]any{"e2e_root": "a://Sifreli"})
	if folder[0]["encrypted"] != "folder" {
		t.Errorf("listing in an encrypted folder: %v", folder[0])
	}
	for _, rows := range [][]map[string]any{vault, folder} {
		if _, has := rows[1]["encrypted"]; has {
			t.Errorf("a folder row: %v", rows[1])
		}
	}
	plain := files()
	stampListingEncrypted(plain, map[string]any{})
	if _, has := plain[0]["encrypted"]; has {
		t.Errorf("a plain listing: %v", plain[0])
	}
}

// typedNodes answers GetNodeByPath with a node of the given type.
type typedNodes map[string]model.NodeType

func (f typedNodes) GetNodeByPath(_ context.Context, storageID int64, pathHash string) (*model.Node, error) {
	for p, typ := range f {
		if pathkey.Hash(storageID, p) == pathHash {
			return &model.Node{Path: "/" + p, Type: typ}, nil
		}
	}
	return nil, db.ErrNoRows
}

// sec055 S16 follow-up: every app door asks one question of a path - the
// row rule, asked of the path (encryptedAtDoor) - and says one refusal
// (refuseEncryptedAtDoor). Red before: neither existed; the doors asked
// e2e.UnderEncrypted alone and let a `.fxe` through.
func TestEncryptedAtDoor_TheDoorsQuestion(t *testing.T) {
	ctx := context.Background()
	store := typedNodes{
		"kasa/.filex-e2e.json": model.NodeTypeFile,
		"klasor.fxe":           model.NodeTypeDirectory,
	}
	cases := map[string]string{
		"kasa/x.txt":     "folder",
		"kasa/alt":       "folder", // a folder inside: the doors always refused it
		"kasa":           "",       // the encrypted folder's own path is not inside itself
		"tek.docx.fxe":   "file",
		"/tek.docx.fxe/": "file",
		"klasor.fxe":     "", // a folder named like a .fxe is a folder
		"acik.txt":       "",
	}
	for rel, want := range cases {
		if got := encryptedAtDoor(ctx, store, 7, rel); got != want {
			t.Errorf("encryptedAtDoor(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestRefuseEncryptedAtDoor_OneRefusalTwoSentences(t *testing.T) {
	cases := []struct {
		kind    string
		writing bool
		said    string
		params  apierr.Params
	}{
		{"folder", false, "app_encrypted_read", apierr.Params{"name": "kasa/x.txt"}},
		{"vault", false, "app_encrypted_read", apierr.Params{"name": "kasa/x.txt"}},
		{"file", false, "app_encrypted_file_read", apierr.Params{"name": "kasa/x.txt"}},
		{"folder", true, "app_encrypted_write", nil},
		{"file", true, "app_encrypted_file_write", apierr.Params{"name": "kasa/x.txt"}},
	}
	for _, c := range cases {
		require.True(t, apierr.Known(c.said), c.said)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Accept-Language", "en")
		require.True(t, refuseEncryptedAtDoor(rec, req, c.kind, "kasa/x.txt", c.writing), c.kind)
		require.Equal(t, http.StatusForbidden, rec.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "encrypted", body["error"], "%+v", c)
		require.Equal(t, apierr.Text("en", c.said, c.params), body["message"], "%+v", c)
	}
	rec := httptest.NewRecorder()
	require.False(t, refuseEncryptedAtDoor(rec, httptest.NewRequest(http.MethodGet, "/x", nil), "", "acik.txt", false))
	require.Equal(t, 0, rec.Body.Len(), "a plaintext path is not refused")
}
