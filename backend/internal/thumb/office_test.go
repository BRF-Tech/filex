package thumb_test

// Office documents drawn by the OnlyOffice document server (thumb/office.go):
// the pipeline side, with a fake document server behind thumb.OfficeDrawer.
// The client's own side (the request, the key, the classes) is measured in
// internal/onlyoffice, the two together in internal/server.

import (
	"context"
	"errors"
	"image/color"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// fakeOffice is a document server: ready or not, and what it answers each
// page it is asked for.
type fakeOffice struct {
	ready  atomic.Bool
	mu     sync.Mutex
	pages  []thumb.OfficePage
	answer func(pg thumb.OfficePage) ([]byte, error)
}

func newFakeOffice(t *testing.T) *fakeOffice {
	f := &fakeOffice{}
	f.ready.Store(true)
	page := solidPNG(t, 226, 320, color.White)
	f.answer = func(thumb.OfficePage) ([]byte, error) { return page, nil }
	return f
}

func (f *fakeOffice) Ready(context.Context) bool { return f.ready.Load() }

func (f *fakeOffice) DrawPage(_ context.Context, pg thumb.OfficePage) ([]byte, error) {
	f.mu.Lock()
	f.pages = append(f.pages, pg)
	answer := f.answer
	f.mu.Unlock()
	return answer(pg)
}

func (f *fakeOffice) asked() []thumb.OfficePage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]thumb.OfficePage(nil), f.pages...)
}

// docxBody is enough of an OOXML package for the sniff and the drawer: the
// fake document server does not read it.
var docxBody = append([]byte("PK\x03\x04"), make([]byte, 200)...)

func TestOOThumb_DrawsAnOfficeDocumentThroughTheDocumentServer(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, PDF: true})
	ds := newFakeOffice(t)
	pipe.AttachOffice(ds)
	n := writeFileAs(t, store, st, root, "Bütçe.xlsx", "application/zip", docxBody)

	assert.Equal(t, thumb.Render, pipe.Assess(n, nil, time.Now()), "a listing draws an office document")
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", row.State, row.Error)
	assert.Equal(t, "onlyoffice", row.Generator, "the repair tab's \"who drew it\"")
	assert.Equal(t, []thumb.Attempt{{H: "onlyoffice", R: "ok"}}, thumb.ParseAttempts(row.Attempts))
	_, err = os.Stat(pipe.CachePath(n.ID))
	require.NoError(t, err, "filex wrote its own JPEG of the page")

	asked := ds.asked()
	require.Len(t, asked, 1)
	assert.Equal(t, n.ID, asked[0].NodeID)
	assert.Equal(t, "Bütçe.xlsx", asked[0].Name)
	assert.Equal(t, thumb.SourceSig(n), asked[0].ContentSig, "the content is part of the document server's key")
	assert.Equal(t, 1, asked[0].Attempt)
}

// OnlyOffice not configured: the file says so (no_tool:office), no
// placeholder card, and nobody asks again until it is configured - then the
// next listing draws it.
func TestOOThumb_NotConfiguredSaysSoUntilItIs(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, PDF: true})
	ds := newFakeOffice(t)
	ds.ready.Store(false)
	pipe.AttachOffice(ds)
	n := writeFileAs(t, store, st, root, "sunum.pptx", "application/zip", docxBody)

	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "skipped", row.State)
	assert.Equal(t, "no_tool:office", row.Error)
	assert.Empty(t, ds.asked())
	_, statErr := os.Stat(pipe.CachePath(n.ID))
	assert.True(t, os.IsNotExist(statErr), "no placeholder card")

	later := time.Now().Add(time.Hour)
	assert.Equal(t, thumb.Leave, pipe.Assess(n, row, later), "still not configured: asking again would say the same")
	ds.ready.Store(true)
	assert.Equal(t, thumb.Render, pipe.Assess(n, row, later), "configured now")
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", row.State)
}

// A failure that may pass (the document server's -4, its network, its own
// trouble) is asked again after a back-off - 2 minutes, then 8, 32, 128, 512
// - with the try counted in the document server's key, and left alone after
// the sixth try. A listing in between does not ask. On a new version of the
// file the count starts again.
func TestAssess_TransientFailureRetriedAfterBackoff(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	ds := newFakeOffice(t)
	ds.answer = func(thumb.OfficePage) ([]byte, error) {
		return nil, &thumb.OfficeError{Class: thumb.OfficeTransient, What: "ds-4"}
	}
	pipe.AttachOffice(ds)
	n := writeFileAs(t, store, st, root, "rapor.docx", "application/zip", docxBody)

	backoff := []time.Duration{2 * time.Minute, 8 * time.Minute, 32 * time.Minute, 128 * time.Minute, 512 * time.Minute}
	for try := 1; try <= 6; try++ {
		require.Error(t, pipe.GenerateThumb(ctx, n))
		row, err := store.GetThumbnail(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, "failed", row.State)
		require.Equal(t, "oo_retry:"+itoa(try)+":ds-4", row.Error)
		require.Equal(t, "", thumb.NoteOf(row), "a failure that may pass gets no marker")
		asked := ds.asked()
		require.Equal(t, try, asked[len(asked)-1].Attempt, "the try is in the document server's key")

		at := *row.AttemptedAt
		if try == 6 {
			assert.Equal(t, thumb.Leave, pipe.Assess(n, row, at.Add(30*24*time.Hour)), "six tries: left alone")
			break
		}
		wait := backoff[try-1]
		assert.Equal(t, thumb.Leave, pipe.Assess(n, row, at.Add(wait-time.Second)), "try %d: not before its back-off", try)
		assert.Equal(t, thumb.Render, pipe.Assess(n, row, at.Add(wait)), "try %d: after %s", try, wait)
	}

	// A new version of the file starts again from the first try.
	require.NoError(t, os.WriteFile(root+"/rapor.docx", append(docxBody, 'x'), 0o644))
	mt := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)
	n.Size++
	n.BackendMtime = &mt
	require.Error(t, pipe.GenerateThumb(ctx, n))
	asked := ds.asked()
	assert.Equal(t, 1, asked[len(asked)-1].Attempt)
}

// What the document server's answers mean on the file: a damaged document,
// a password and a size limit are the file's - final for this content, and
// marked - while the document server not being configured mid-way is the
// same as never configured.
func TestOOThumb_FinalAnswersAreTheFiles(t *testing.T) {
	cases := []struct {
		class, what   string
		state, reason string
		note          string
	}{
		{thumb.OfficeCorrupt, "ds-3", "failed", "oo_corrupt:ds-3", thumb.NoteCorrupt},
		{thumb.OfficePassword, "ds-5", "skipped", "oo_password", thumb.NoteEncrypted},
		{thumb.OfficeTooLarge, "ds-10", "skipped", "oo_too_large:0", thumb.NoteTooLarge},
		{thumb.OfficeUnconfigured, "", "skipped", "no_tool:office", ""},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			ctx := context.Background()
			store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
			ds := newFakeOffice(t)
			ds.answer = func(thumb.OfficePage) ([]byte, error) {
				return nil, &thumb.OfficeError{Class: c.class, What: c.what, Err: errors.New("answer")}
			}
			pipe.AttachOffice(ds)
			n := writeFileAs(t, store, st, root, "belge.docx", "application/zip", docxBody)
			_ = pipe.GenerateThumb(ctx, n)
			row, err := store.GetThumbnail(ctx, n.ID)
			require.NoError(t, err)
			assert.Equal(t, c.state, row.State)
			assert.Equal(t, c.reason, row.Error)
			assert.Equal(t, c.note, thumb.NoteOf(row))
			if c.class != thumb.OfficeUnconfigured {
				assert.Equal(t, thumb.Leave, pipe.Assess(n, row, row.AttemptedAt.Add(30*24*time.Hour)),
					"the same bytes would fail again")
			}
		})
	}
}

// ⚠ An end-to-end encrypted file is never sent to the document server, under
// any name: its first bytes give it away before the document server is
// asked to download anything.
func TestOOThumb_EncryptedNeverReachesDS(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	ds := newFakeOffice(t)
	pipe.AttachOffice(ds)
	for _, name := range []string{"gizli.docx", "tablo.xlsx"} {
		n := writeFileAs(t, store, st, root, name, "application/octet-stream", append([]byte("filexe2e"), make([]byte, 64)...))
		require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
		row, err := store.GetThumbnail(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, "skipped", row.State)
		assert.True(t, strings.HasPrefix(row.Error, "e2e-encrypted"), row.Error)
		assert.Equal(t, thumb.NoteEncrypted, thumb.NoteOf(row), "marked as encrypted")
	}
	assert.Empty(t, ds.asked(), "the document server was never asked")
}

// Without OnlyOffice too: ciphertext under an office name is the file's
// reason (encrypted), not "OnlyOffice is not configured".
func TestOOThumb_EncryptedSaysSoWithoutOnlyOffice(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	n := writeFileAs(t, store, st, root, "gizli.docx", "application/octet-stream", append([]byte("filexe2e"), make([]byte, 64)...))
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "e2e-encrypted content", row.Error)
	assert.Equal(t, thumb.NoteEncrypted, thumb.NoteOf(row))
}

// Over the size setting: not sent, marked too large, and drawn once the
// setting is raised past the file.
func TestOOThumb_TooLargeUntilTheLimitIsRaised(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	pipe.AttachSettings(store)
	require.NoError(t, store.UpsertSetting(ctx, thumb.OfficeMaxMBSetting.Key, "1"))
	ds := newFakeOffice(t)
	pipe.AttachOffice(ds)
	n := writeFileAs(t, store, st, root, "büyük.pptx", "application/zip", append(docxBody, make([]byte, 2<<20)...))

	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "oo_too_large:1048576", row.Error)
	assert.Equal(t, thumb.NoteTooLarge, thumb.NoteOf(row))
	assert.Empty(t, ds.asked(), "not sent")
	later := row.AttemptedAt.Add(time.Hour)
	assert.Equal(t, thumb.Leave, pipe.Assess(n, row, later))

	require.NoError(t, store.UpsertSetting(ctx, thumb.OfficeMaxMBSetting.Key, "5"))
	pipe.ForgetSettings()
	assert.Equal(t, thumb.Render, pipe.Assess(n, row, later), "the limit was raised past the file")
}

// The pictures LibreOffice drew before 0.50 are kept while nobody here can
// draw an office document, and are stale the moment OnlyOffice is
// configured; a PDF's picture, filex's own, is not touched.
func TestOOThumb_MakesLibreOfficeRowsStale(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, PDF: true})
	ds := newFakeOffice(t)
	ds.ready.Store(false)
	pipe.AttachOffice(ds)
	doc := writeFileAs(t, store, st, root, "eski.docx", "application/zip", docxBody)
	pdf := writeFileAs(t, store, st, root, "eski.pdf", "application/pdf", []byte("%PDF-1.4\n"))
	drawn := time.Now().Add(-48 * time.Hour)
	legacy := func(n *model.Node, attempts string) *model.Thumbnail {
		return &model.Thumbnail{NodeID: n.ID, State: "ready", StorageKey: "k", SourceSig: thumb.SourceSig(n),
			GeneratedAt: &drawn, AttemptedAt: &drawn, Attempts: attempts}
	}
	now := time.Now()
	rows := map[string]*model.Thumbnail{
		"drawn before 0.50 (no attempts)":   legacy(doc, ""),
		"drawn by the built-in LibreOffice": legacy(doc, `[{"h":"builtin","r":"ok"}]`),
	}
	for name, row := range rows {
		assert.Equal(t, thumb.Leave, pipe.Assess(doc, row, now), "%s: kept while OnlyOffice is not configured", name)
	}
	assert.Equal(t, thumb.Leave, pipe.Assess(pdf, legacy(pdf, ""), now))

	ds.ready.Store(true)
	for name, row := range rows {
		assert.Equal(t, thumb.Render, pipe.Assess(doc, row, now), "%s: OnlyOffice is configured, the page is redrawn", name)
	}
	assert.Equal(t, thumb.Leave, pipe.Assess(pdf, legacy(pdf, ""), now), "a PDF stays filex's own")
	assert.Equal(t, thumb.Leave, pipe.Assess(doc, legacy(doc, `[{"h":"onlyoffice","r":"ok"}]`), now),
		"OnlyOffice's own picture of the same content is fresh")
}

// One document at a time per process (the default slot): a Community
// Edition document server runs one converter, shared with the editors.
func TestOOThumb_OneDocumentAtATime(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	ds := newFakeOffice(t)
	page := solidPNG(t, 226, 320, color.White)
	var inside, most atomic.Int32
	ds.answer = func(thumb.OfficePage) ([]byte, error) {
		now := inside.Add(1)
		for {
			m := most.Load()
			if now <= m || most.CompareAndSwap(m, now) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		inside.Add(-1)
		return page, nil
	}
	pipe.AttachOffice(ds)
	var nodes []*model.Node
	for i := 0; i < 4; i++ {
		nodes = append(nodes, writeFileAs(t, store, st, root, "belge"+itoa(i)+".docx", "application/zip", docxBody))
	}
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n *model.Node) {
			defer wg.Done()
			assert.NoError(t, pipe.GenerateThumb(ctx, n))
		}(n)
	}
	wg.Wait()
	assert.EqualValues(t, 1, most.Load(), "never two at once")
	assert.Len(t, ds.asked(), 4)
}

func TestOnlyOfficeDraws(t *testing.T) {
	for _, c := range []struct {
		name, mime string
		want       bool
	}{
		{"a.docx", "", true}, {"a.DOC", "application/octet-stream", true}, {"a.xlsm", "", true},
		{"a.ppsx", "", true}, {"a.odg", "", true}, {"a.vsdx", "", true}, {"a.rtf", "", true},
		{"a.epub", "", true}, {"a.pages", "application/zip", true},
		{"deck.key", "application/zip", true},
		{"server.key", "text/plain; charset=utf-8", false},
		{"server.key", "", false},
		{"a.pdf", "application/pdf", false}, {"a.csv", "text/csv", false}, {"a.txt", "", false},
		{"a.png", "image/png", false}, {"README", "", false},
	} {
		assert.Equal(t, c.want, thumb.OnlyOfficeDraws(c.name, c.mime), "%s %s", c.name, c.mime)
	}
}

// The ONE table of which reason gets which marker.
func TestNoteForReason(t *testing.T) {
	for reason, want := range map[string]string{
		"oo_corrupt:ds-3":       thumb.NoteCorrupt,
		"oo_password":           thumb.NoteEncrypted,
		"archive_encrypted":     thumb.NoteEncrypted,
		"e2e-encrypted folder":  thumb.NoteEncrypted,
		"e2e-encrypted file":    thumb.NoteEncrypted,
		"e2e-encrypted content": thumb.NoteEncrypted,
		"oo_too_large:26214400": thumb.NoteTooLarge,
		"oo_too_large:0":        thumb.NoteTooLarge,
		"svg_too_large:5242880": thumb.NoteTooLarge,
		"archive_too_large":     thumb.NoteTooLarge,
		"app_too_large:pkg:100": thumb.NoteTooLarge,
		"oo_retry:2:ds-4":       "",
		"no_tool:office":        "",
		"no_handler":            "",
		"svg_timeout:10000":     "",
		"app_failed:pkg":        "",
		"thumb: decode failed":  "",
	} {
		assert.Equal(t, want, thumb.NoteForReason(reason), reason)
	}
	assert.Equal(t, "", thumb.NoteOf(&model.Thumbnail{State: "ready", Error: "oo_password"}), "a picture needs no marker")
	assert.Equal(t, "", thumb.NoteOf(&model.Thumbnail{State: "pending"}))
	assert.Equal(t, "", thumb.NoteOf(nil))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
