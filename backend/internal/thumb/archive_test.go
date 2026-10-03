package thumb

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// An archive's thumbnail is the list of what is in it, read from the ZIP's
// central directory or the tar's headers, never unpacked, and every read is
// bounded (docs/thumbnails.md, Generators).

type archiveFixture struct {
	p    *Pipeline
	st   *model.Storage
	root string
	put  func(name string, body []byte) *model.Node
	row  func(id int64) *model.Thumbnail
}

// newArchiveFixture: a pipeline over a local storage; wrap, when not nil,
// wraps its driver (a slow one, one without ranged reads).
func newArchiveFixture(t *testing.T, wrap func(storage.Driver) storage.Driver) *archiveFixture {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "yerel", Driver: "local", MountPath: "/yerel", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	require.NoError(t, err)
	p := New(store, t.TempDir(), Capabilities{Image: true, SVG: true})
	var d storage.Driver = drv
	if wrap != nil {
		d = wrap(drv)
	}
	p.AttachStorage(st.ID, d)
	f := &archiveFixture{p: p, st: st, root: root}
	f.put = func(name string, body []byte) *model.Node {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), body, 0o644))
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: name, Path: "/" + name,
			PathHash: pathkey.Hash(st.ID, "/"+name), Type: model.NodeTypeFile, Size: int64(len(body)), Mime: "application/zip"})
		require.NoError(t, err)
		return n
	}
	f.row = func(id int64) *model.Thumbnail {
		r, err := store.GetThumbnail(ctx, id)
		require.NoError(t, err)
		return r
	}
	return f
}

func zipOf(t *testing.T, names []string, encrypt bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		if encrypt {
			h.Flags |= 0x1
		}
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, _ = w.Write([]byte("x"))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestArchiveLines_TheTopLevel(t *testing.T) {
	lines := archiveLines([]string{"README.md", "docs/", "docs/a.md", "src/main.go", "src/util/x.go", "./LICENSE", "/abs.txt"})
	var got []string
	for _, l := range lines {
		if l.Bold {
			got = append(got, "*"+l.Text)
		} else {
			got = append(got, l.Text)
		}
	}
	require.Equal(t, []string{"README.md", "*docs/", "*src/", "LICENSE", "abs.txt"}, got)

	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
	}
	lines = archiveLines(many)
	require.Len(t, lines, archiveNames)
	require.Equal(t, "+28", lines[len(lines)-1].Text, "what did not fit is counted")
	require.True(t, lines[len(lines)-1].Muted)
}

// A ZIP is drawn from its central directory: a real picture, not the card.
func TestArchive_ZipIsItsList(t *testing.T) {
	f := newArchiveFixture(t, nil)
	n := f.put("yedek.zip", zipOf(t, []string{"README.md", "docs/a.md", "src/main.go"}, false))
	require.NoError(t, f.p.GenerateThumb(context.Background(), n))
	row := f.row(n.ID)
	require.Equal(t, "ready", row.State, row.Error)
	b, err := os.ReadFile(f.p.CachePath(n.ID))
	require.NoError(t, err)
	img, err := jpeg.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	require.Equal(t, linesW, img.Bounds().Dx())
}

// An encrypted ZIP keeps its type icon: skipped, with the reason.
func TestArchive_EncryptedZipIsSkipped(t *testing.T) {
	f := newArchiveFixture(t, nil)
	n := f.put("gizli.zip", zipOf(t, []string{"a.txt", "b.txt"}, true))
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	row := f.row(n.ID)
	require.Equal(t, "skipped", row.State)
	require.Equal(t, SkipArchiveEncrypted, row.Error)
}

// A damaged ZIP fails with the reader's own words, not the placeholder card.
func TestArchive_DamagedZipFails(t *testing.T) {
	f := newArchiveFixture(t, nil)
	n := f.put("bozuk.zip", []byte("PK this is not a zip at all, only its first two letters"))
	require.Error(t, f.p.GenerateThumb(context.Background(), n))
	row := f.row(n.ID)
	require.Equal(t, "failed", row.State)
	require.Contains(t, row.Error, "archive")
}

// A central directory past the budget is not read to its end: skipped as
// too large, however many entries a machine packed into it.
func TestArchive_DirectoryPastTheBudgetIsSkipped(t *testing.T) {
	defer func(v int64) { archiveDirBudget = v }(archiveDirBudget)
	archiveDirBudget = 4 << 10
	var names []string
	for i := 0; i < 2000; i++ {
		names = append(names, "klasor/alt-klasor/dosya-numarasi-"+string(rune('a'+i%26))+time.Duration(i).String()+".txt")
	}
	f := newArchiveFixture(t, nil)
	n := f.put("dev.zip", zipOf(t, names, false))
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	require.Equal(t, SkipArchiveTooLarge, f.row(n.ID).Error)
}

// More entries than archiveMaxEntries: skipped as too large.
func TestArchive_TooManyEntriesIsSkipped(t *testing.T) {
	defer func(v int) { archiveMaxEntries = v }(archiveMaxEntries)
	archiveMaxEntries = 10
	var names []string
	for i := 0; i < 11; i++ {
		names = append(names, time.Duration(i).String()+".txt")
	}
	f := newArchiveFixture(t, nil)
	n := f.put("cok.zip", zipOf(t, names, false))
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	require.Equal(t, SkipArchiveTooLarge, f.row(n.ID).Error)
}

// slowDriver answers every ranged read late: an archive on a storage that
// stalls is given up at archiveTimeout, and the next file is not held up.
type slowDriver struct {
	storage.Driver
	delay time.Duration
}

func (s slowDriver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Driver.(storage.RangeReader).ReadRange(ctx, p, off, length)
}

func TestArchive_TimeoutSkips(t *testing.T) {
	defer func(v time.Duration) { archiveTimeout = v }(archiveTimeout)
	archiveTimeout = 150 * time.Millisecond
	f := newArchiveFixture(t, func(d storage.Driver) storage.Driver { return slowDriver{Driver: d, delay: 2 * time.Second} })
	n := f.put("yavas.zip", zipOf(t, []string{"a.txt"}, false))
	start := time.Now()
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	require.Less(t, time.Since(start), time.Second, "the stalled read was not given up")
	require.Equal(t, SkipArchiveTooLarge, f.row(n.ID).Error)
}

// noRange hides the local driver's ranged reads and counts whole reads: the
// ZIP is copied to a temporary file up to archiveCopyLimit, and a larger one
// is skipped without a byte of it read.
type noRange struct {
	storage.Driver
	reads *int
}

func (n noRange) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	*n.reads++
	return n.Driver.Read(ctx, p)
}

func TestArchive_WithoutRangedReads(t *testing.T) {
	reads := 0
	f := newArchiveFixture(t, func(d storage.Driver) storage.Driver { return noRange{Driver: d, reads: &reads} })
	n := f.put("kucuk.zip", zipOf(t, []string{"a.txt", "b/"}, false))
	require.NoError(t, f.p.GenerateThumb(context.Background(), n))
	require.Equal(t, "ready", f.row(n.ID).State)
	require.Equal(t, 1, reads, "copied once")

	defer func(v int64) { archiveCopyLimit = v }(archiveCopyLimit)
	archiveCopyLimit = 64
	big := f.put("buyuk.zip", zipOf(t, []string{"a.txt", "b.txt", "c.txt"}, false))
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), big), ErrSkipped)
	require.Equal(t, SkipArchiveTooLarge, f.row(big.ID).Error)
	require.Equal(t, 1, reads, "a ZIP past the copy limit is not read at all")
}

func tarGz(t *testing.T, members map[string]int64, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	zeros := make([]byte, 1<<16)
	for _, name := range order {
		size := members[name]
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, Typeflag: tar.TypeReg}))
		for rest := size; rest > 0; {
			n := int64(len(zeros))
			if n > rest {
				n = rest
			}
			_, err := tw.Write(zeros[:n])
			require.NoError(t, err)
			rest -= n
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// A tar.gz is read header by header; a member that inflates past the stream
// budget (a bomb) stops the reading at the budget, and what was read so far
// is drawn, marked as partial.
func TestArchive_TarGzBombStopsAtTheBudget(t *testing.T) {
	defer func(v int64) { archiveStreamBudget = v }(archiveStreamBudget)
	archiveStreamBudget = 1 << 20
	f := newArchiveFixture(t, nil)
	body := tarGz(t, map[string]int64{"ilk.txt": 1, "bomba.bin": 64 << 20, "son.txt": 1}, []string{"ilk.txt", "bomba.bin", "son.txt"})
	require.Less(t, len(body), 1<<20, "the fixture is small on disk")
	n := f.put("yedek.tar.gz", body)
	entries, count, partial, err := f.p.tarEntries(context.Background(), n, f.p.storages[f.st.ID], "tgz")
	require.NoError(t, err)
	require.True(t, partial, "the bomb was read to its end")
	require.Equal(t, []string{"ilk.txt", "bomba.bin"}, entries)
	require.Equal(t, 2, count)
	require.NoError(t, f.p.GenerateThumb(context.Background(), n))
	require.Equal(t, "ready", f.row(n.ID).State)
}

func TestArchive_TarGzListed(t *testing.T) {
	f := newArchiveFixture(t, nil)
	n := f.put("kaynak.tgz", tarGz(t, map[string]int64{"a/b.txt": 3, "c.txt": 1}, []string{"a/b.txt", "c.txt"}))
	entries, count, partial, err := f.p.tarEntries(context.Background(), n, f.p.storages[f.st.ID], "tgz")
	require.NoError(t, err)
	require.False(t, partial)
	require.Equal(t, 2, count)
	require.Equal(t, []string{"a/b.txt", "c.txt"}, entries)
}

func TestArchiveKind(t *testing.T) {
	for name, want := range map[string]string{
		"a.zip": "zip", "A.ZIP": "zip", "a.tar": "tar", "a.tar.gz": "tgz", "a.tgz": "tgz",
		"a.tar.bz2": "tbz2", "a.tbz2": "tbz2", "a.gz": "", "a.7z": "", "a.txt": "",
	} {
		require.Equal(t, want, archiveKind(name), name)
	}
}
