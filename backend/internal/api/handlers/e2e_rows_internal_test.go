package handlers

// wiring:e2 names — rows that arrive outside a folder listing say which
// encrypted folder they sit in, so the client can name them (their names may
// be ciphertext) instead of printing what the server stores.

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

type fakeMarkers map[string]bool

func (f fakeMarkers) GetNodeByPath(_ context.Context, storageID int64, pathHash string) (*model.Node, error) {
	for p := range f {
		if pathkey.Hash(storageID, p) == pathHash {
			return &model.Node{Path: "/" + p}, nil
		}
	}
	return nil, db.ErrNoRows
}

func TestE2eRoots_NameTheFolderARowSitsIn(t *testing.T) {
	ctx := context.Background()
	roots := newE2eRoots(fakeMarkers{"kasa/.filex-e2e.json": true})
	cases := map[string]string{
		"/kasa/RK_chqfshg00TF_YAhrf6nAQFJIkmjUbmdrmCcSjV9Y": "alpha://kasa",
		"/kasa/a/b/c":            "alpha://kasa",
		"/kasa/.filex-e2e.json":  "alpha://kasa",
		"/kasa":                  "", // the root's own row is not inside itself
		"/acik/x.txt":            "",
		"/kasa-not-really/x.txt": "",
	}
	for p, want := range cases {
		if got := roots.of(ctx, 7, "alpha", p); got != want {
			t.Errorf("of(%q) = %q, want %q", p, got, want)
		}
	}
	// No storage name → no wire path to give; say nothing rather than guess.
	if got := roots.of(ctx, 7, "", "/kasa/x"); got != "" {
		t.Errorf("empty storage name: got %q", got)
	}
}

func TestAnnotateRowsE2e_OnlyRowsInsideAnEncryptedFolder(t *testing.T) {
	rows := []map[string]any{
		{"path": "alpha://kasa/ugkmCcOceKUyA7tsdZxzltk"},
		{"path": "alpha://acik/plain.txt"},
	}
	annotateRowsE2e(context.Background(), newE2eRoots(fakeMarkers{"kasa/.filex-e2e.json": true}), 7, "alpha", rows)
	if rows[0]["e2e_root"] != "alpha://kasa" {
		t.Errorf("row inside: e2e_root = %v", rows[0]["e2e_root"])
	}
	if _, has := rows[1]["e2e_root"]; has {
		t.Errorf("row outside must carry no e2e_root: %v", rows[1])
	}
}
