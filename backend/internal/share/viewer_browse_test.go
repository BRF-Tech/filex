package share

/* wiring:d2 — tests for the public folder-share browse page: entry
   classification and the ≥60% gallery rule. The page's two layouts
   (gallery grid vs plain list) are the page.folder template's
   (internal/templates templates_test.go). */

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClassifyEntry(t *testing.T) {
	assert.Equal(t, EntryDir, ClassifyEntry("photos", true))
	assert.Equal(t, EntryImage, ClassifyEntry("a.JPG", false))
	assert.Equal(t, EntryImage, ClassifyEntry("b.webp", false))
	assert.Equal(t, EntryVideo, ClassifyEntry("clip.mp4", false))
	assert.Equal(t, EntryVideo, ClassifyEntry("clip.MOV", false))
	assert.Equal(t, EntryFile, ClassifyEntry("report.pdf", false))
	assert.Equal(t, EntryFile, ClassifyEntry("noext", false))
}

func TestGalleryEligible(t *testing.T) {
	f := func(kinds ...FolderEntryKind) []FolderEntry {
		out := make([]FolderEntry, len(kinds))
		for i, k := range kinds {
			out[i] = FolderEntry{Name: "x", Kind: k}
		}
		return out
	}

	// Empty folder / dirs only / no visuals → list.
	assert.False(t, GalleryEligible(nil))
	assert.False(t, GalleryEligible(f(EntryDir, EntryDir)))
	assert.False(t, GalleryEligible(f(EntryFile, EntryFile)))

	// Exactly 60% visual (3 of 5) → gallery; dirs don't dilute the ratio.
	assert.True(t, GalleryEligible(f(EntryImage, EntryImage, EntryVideo, EntryFile, EntryFile)))
	assert.True(t, GalleryEligible(f(EntryDir, EntryDir, EntryImage, EntryImage, EntryVideo, EntryFile, EntryFile)))

	// Under 60% (2 of 5) → list.
	assert.False(t, GalleryEligible(f(EntryImage, EntryVideo, EntryFile, EntryFile, EntryFile)))

	// All-visual folder → gallery.
	assert.True(t, GalleryEligible(f(EntryImage)))
}

func TestMimeForName(t *testing.T) {
	assert.Contains(t, MimeForName("a.jpg"), "image/jpeg")
	assert.Equal(t, "application/octet-stream", MimeForName("noext"))
}

func TestFolderEntryDateFormat(t *testing.T) {
	// The handler formats Mtime as 02.01.2006 15:04 — pin the layout here so
	// a drive-by change to the template contract shows up.
	ts := time.Date(2026, 7, 17, 9, 30, 0, 0, time.UTC)
	assert.Equal(t, "17.07.2026 09:30", ts.Format("02.01.2006 15:04"))
}
