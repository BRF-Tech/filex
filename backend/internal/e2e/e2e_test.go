package e2e

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// fakeLookup answers GetNodeByPath from a set of known path hashes.
type fakeLookup struct {
	storageID int64
	paths     map[string]bool // relative marker paths that "exist"
}

func (f *fakeLookup) GetNodeByPath(_ context.Context, storageID int64, hash string) (*model.Node, error) {
	if storageID != f.storageID {
		return nil, nil
	}
	for p, ok := range f.paths {
		if ok && pathkey.Hash(storageID, p) == hash {
			return &model.Node{ID: 1, StorageID: storageID, Path: "/" + p}, nil
		}
	}
	return nil, nil
}

func TestHasMagicPrefix(t *testing.T) {
	if !HasMagicPrefix([]byte("filexe2e\x01rest")) {
		t.Fatal("expected magic to match")
	}
	if HasMagicPrefix([]byte("filexe2")) {
		t.Fatal("short buffer must not match")
	}
	if HasMagicPrefix([]byte("PK\x03\x04....")) {
		t.Fatal("zip prefix must not match")
	}
}

func TestFindRootAndUnderEncrypted(t *testing.T) {
	ctx := context.Background()
	lk := &fakeLookup{
		storageID: 7,
		paths: map[string]bool{
			"vault/" + MarkerName: true,
		},
	}

	// The marked dir itself.
	if root, ok := FindRoot(ctx, lk, 7, "vault"); !ok || root != "vault" {
		t.Fatalf("FindRoot(vault) = %q,%v", root, ok)
	}
	// A nested subdir inherits the root.
	if root, ok := FindRoot(ctx, lk, 7, "vault/sub/deep"); !ok || root != "vault" {
		t.Fatalf("FindRoot(vault/sub/deep) = %q,%v", root, ok)
	}
	// An unrelated dir has none.
	if _, ok := FindRoot(ctx, lk, 7, "public/docs"); ok {
		t.Fatal("public/docs must not resolve a root")
	}

	// Files under the subtree are flagged; the sibling world is not.
	if !UnderEncrypted(ctx, lk, 7, "/vault/secret.txt") {
		t.Fatal("vault/secret.txt must be under encryption")
	}
	if !UnderEncrypted(ctx, lk, 7, "vault/sub/deep/x.bin") {
		t.Fatal("nested file must be under encryption")
	}
	if UnderEncrypted(ctx, lk, 7, "/public/readme.md") {
		t.Fatal("outside file must not be under encryption")
	}
	// The vault dir row itself viewed from the parent: its parent ("") has no
	// marker, so UnderEncrypted is false — the dir badge comes from FindRoot.
	if UnderEncrypted(ctx, lk, 7, "/vault") {
		t.Fatal("the encrypted root itself is not 'under' encryption")
	}
	// Wrong storage never matches.
	if _, ok := FindRoot(ctx, lk, 8, "vault"); ok {
		t.Fatal("other storage must not match")
	}
}

func TestFindRootAtStorageRoot(t *testing.T) {
	ctx := context.Background()
	lk := &fakeLookup{storageID: 3, paths: map[string]bool{MarkerName: true}}
	if root, ok := FindRoot(ctx, lk, 3, ""); !ok || root != "" {
		t.Fatalf("FindRoot(root) = %q,%v", root, ok)
	}
	if !UnderEncrypted(ctx, lk, 3, "/anything.txt") {
		t.Fatal("file at marked storage root must be under encryption")
	}
}
