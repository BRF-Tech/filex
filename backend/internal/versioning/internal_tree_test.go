package versioning_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/versioning"
)

// IsInternalTree is the one answer to "is this filex's own bookkeeping?" for
// every surface that walks a storage. Its shape is load-bearing in both
// directions: a miss catalogues (and lets a purge reach) version history, and
// a false hit hides a user's file.
func TestIsInternalTree(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		// the three trees, in every spelling a driver or a row produces
		{".versions", true},
		{"/.versions", true},
		{".versions/", true},
		{".versions/221093/1", true},
		{"/.versions/221093/1", true},
		{".thumbs", true},
		{"/.thumbs/ab/cd.jpg", true},
		{".filex-trash", true},
		{"/.filex-trash/1700000000-abc123__rapor.txt", true},
		{"a/../.versions/7/1", true}, // cleaned before it is judged

		// anchored at the storage ROOT: filex writes these nowhere else, so a
		// user folder that happens to carry the name is the user's
		{"docs/.versions", false},
		{"/docs/.versions/notes.txt", false},
		{"/proje/.thumbs/a.jpg", false},

		// look-alikes are not the tree
		{".versions-old", false},
		{".versionsx/1", false},
		{".thumbsdb", false},
		{".filex-trash-backup/x", false},
		{".Versions/1/1", false}, // storages compare names byte for byte

		// the root itself, and names that are not trees
		{"", false},
		{"/", false},
		{".keepdir", false},
		{"docs/.keepdir", false},
		{".filex-open/a1-rapor.docx", false}, // the desktop's scratch folder holds real files
		{".filex-e2e.json", false},           // an encrypted folder's marker must be catalogued
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, versioning.IsInternalTree(c.rel), "IsInternalTree(%q)", c.rel)
	}
}
