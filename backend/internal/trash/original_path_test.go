package trash

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

// OriginalPath is the one rule the restore handler and the trash listing both
// authorise on. Pinned directly so a later caller cannot re-spell the
// storage_key fallback and forget the bin.
func TestOriginalPath(t *testing.T) {
	for _, c := range []struct {
		name      string
		key, path string
		want      string
		wantKnown bool
	}{
		{"retagged row", "/Ekip/notlar.md", "/.filex-trash/1-a__notlar.md", "/Ekip/notlar.md", true},
		{"legacy row at its own path", "", "/Ekip/notlar.md", "/Ekip/notlar.md", true},
		{"legacy row in the bin", "", "/.filex-trash/1-a__notlar.md", "/.filex-trash/1-a__notlar.md", false},
		{"key that points into the bin", ".filex-trash/1-a__x", "/.filex-trash/1-a__x", ".filex-trash/1-a__x", false},
		{"the bin itself", "", "/.filex-trash", "/.filex-trash", false},
		{"nothing at all", "", "", "", false},
	} {
		got, known := OriginalPath(&model.Node{StorageKey: c.key, Path: c.path})
		assert.Equal(t, c.want, got, c.name)
		assert.Equal(t, c.wantKnown, known, c.name)
	}
	_, known := OriginalPath(nil)
	assert.False(t, known)
}
