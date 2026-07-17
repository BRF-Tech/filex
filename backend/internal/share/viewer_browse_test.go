package share

/* wiring:d2 — tests for the public folder-share browse page: entry
   classification, the ≥60% gallery rule, and the HTML template's two
   layouts (gallery grid vs plain list). */

import (
	"html/template"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestRenderFolderPage_GalleryAndList(t *testing.T) {
	base := FolderPageData{
		Style:    template.HTML("<style>/*px*/</style>"),
		Footer:   template.HTML(`<footer class="brand">filex ile paylaşıldı</footer>`),
		Name:     "Tatil Fotoğrafları",
		ZipHref:  "/s/tok?zip=1",
		DirCount: 1,
		FileCnt:  2,
		Entries: []FolderPageEntry{
			{Name: "alt", Kind: EntryDir, Href: "/s/tok?dir=alt"},
			{Name: "a.jpg", Kind: EntryImage, Href: "/s/tok/f/a.jpg", ThumbSrc: "/s/tok/f/a.jpg?thumb=1", SizeLabel: "1.0 KB", DateLabel: "01.01.2026 10:00"},
			{Name: "clip.mp4", Kind: EntryVideo, Href: "/s/tok/f/clip.mp4", SizeLabel: "2.0 MB"},
		},
	}

	// Gallery layout: tile grid + <img> for the thumb-backed image, play
	// badge for the video, and the shared footer intact.
	g := base
	g.Gallery = true
	rec := httptest.NewRecorder()
	require.NoError(t, RenderFolderPage(rec, g))
	body := rec.Body.String()
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, body, `class="ggrid"`)
	assert.NotContains(t, body, `class="flist"`)
	assert.Contains(t, body, `<img src="/s/tok/f/a.jpg?thumb=1"`)
	assert.Contains(t, body, `class="gbadge"`)
	assert.Contains(t, body, "Tatil Fotoğrafları")
	assert.Contains(t, body, "filex ile paylaşıldı")
	assert.Contains(t, body, `href="/s/tok?zip=1"`)
	assert.Contains(t, body, "1 klasör · 2 dosya")

	// List layout: rows, no tile grid, size/date labels visible.
	l := base
	l.Gallery = false
	rec = httptest.NewRecorder()
	require.NoError(t, RenderFolderPage(rec, l))
	body = rec.Body.String()
	assert.Contains(t, body, `class="flist"`)
	assert.NotContains(t, body, `class="ggrid"`)
	assert.Contains(t, body, "1.0 KB")
	assert.Contains(t, body, "01.01.2026 10:00")
	assert.Contains(t, body, "filex ile paylaşıldı")
}

func TestRenderFolderPage_EscapesNames(t *testing.T) {
	d := FolderPageData{
		Name:    `<script>alert(1)</script>`,
		ZipHref: "/s/tok?zip=1",
		Entries: []FolderPageEntry{
			{Name: `<img onerror=x>`, Kind: EntryFile, Href: "/s/tok/f/x"},
		},
	}
	rec := httptest.NewRecorder()
	require.NoError(t, RenderFolderPage(rec, d))
	body := rec.Body.String()
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.NotContains(t, body, "<img onerror=x>")
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "512 B", HumanSize(512))
	assert.Equal(t, "1.0 KB", HumanSize(1024))
	assert.Equal(t, "2.0 MB", HumanSize(2*1024*1024))
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
