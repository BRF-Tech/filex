package search

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// The index counts every node, folders too; the Panel counts files. Stats
// splits the total so the Search page can report files the way the Panel does
// (release-candidate sweep, 2026-09-21: "İNDEKSLENMİŞ DOSYA 28" on the Panel
// next to "İNDEKSLENMİŞ DÖKÜMAN 37" on Search — 28 files and 9 folders).
func TestStats_SplitsFilesAndFolders(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "search.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	for _, n := range issueNodes() { // two files, one folder
		if err := idx.IndexNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	st := idx.Stats()
	if st.DocCount != 3 {
		t.Fatalf("doc count %d, want 3", st.DocCount)
	}
	if st.Files != 2 || st.Folders != 1 {
		t.Fatalf("files=%d folders=%d, want 2 and 1", st.Files, st.Folders)
	}
	// "Last built" is stamped when the index is made — the card read a field
	// nothing filled and said "—" on every install.
	if _, err := time.Parse(time.RFC3339, st.LastUpdated); err != nil {
		t.Fatalf("last built %q is not a time: %v", st.LastUpdated, err)
	}
}
