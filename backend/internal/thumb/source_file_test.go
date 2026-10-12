package thumb

// The file a thumbnail program reads (source_file.go, issue #114).
//
// Before 0.56.2 every video, audio and PDF thumbnail started by copying the
// WHOLE source into the system temp directory - 7 GB per film on the
// reporter's CIFS mount - and a killed process left the copy behind. These
// tests pin the two rules that replaced it: a local file is read in place and
// nothing is written anywhere; anything else is copied into the pipeline's
// own work directory, which startup empties.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// isolateTemp points the system temp directory at an empty one, so a test can
// say "nothing was written there" and ClearWork cannot touch the real one.
func isolateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	require.Equal(t, filepath.Clean(dir), filepath.Clean(os.TempDir()))
	return dir
}

// noCopies fails when a copy of a source exists anywhere a generator writes one.
func noCopies(t *testing.T, p *Pipeline, tmp string) {
	t.Helper()
	left, _ := os.ReadDir(p.workDir())
	require.Empty(t, left, "a copy was written to the work directory")
	for _, pat := range legacyTempPatterns {
		m, _ := filepath.Glob(filepath.Join(tmp, pat))
		require.Empty(t, m, "a copy was written to the system temp directory")
	}
}

// A local storage's file is handed over in place: no copy anywhere, and the
// program reads the very bytes on the storage.
//
// ⚠ RED on 0.56.1: generateVideo/Audio/PDF copied the whole file into
// os.TempDir() before the program ran, whatever the storage.
func TestSourceInput_ALocalFileIsReadInPlace(t *testing.T) {
	tmp := isolateTemp(t)
	f := newArchiveFixture(t, nil)
	body := bytes.Repeat([]byte("frame "), 4096)
	n := f.put("film.mp4", body)

	in, err := f.p.sourceInput(context.Background(), f.p.storages[f.st.ID], n, "filex-vid-*.bin")
	require.NoError(t, err)
	defer in.close()
	require.NotNil(t, in.file, "a local file was not handed over in place")
	require.Empty(t, in.tmp, "a local file was copied")
	noCopies(t, f.p, tmp)

	if runtime.GOOS == "windows" {
		require.Equal(t, filepath.Join(f.root, "film.mp4"), in.path)
		return
	}
	require.Equal(t, inheritedPath, in.path)
	// Twice: every pass of a generator opens the source again, and the second
	// must start at byte 0 too (the offset is shared on macOS).
	for range 2 {
		out, err := in.command(context.Background(), "cat", in.path).Output()
		require.NoError(t, err)
		require.Equal(t, body, out, "the program did not read the storage's file")
	}
}

// A storage that is not a local disk still gets a copy, but in the work
// directory and gone once the program is done.
func TestSourceInput_ANonLocalFileIsCopiedIntoTheWorkDirectory(t *testing.T) {
	tmp := isolateTemp(t)
	reads := 0
	f := newArchiveFixture(t, func(d storage.Driver) storage.Driver { return noRange{Driver: d, reads: &reads} })
	body := []byte("not a local file")
	n := f.put("film.mp4", body)

	in, err := f.p.sourceInput(context.Background(), f.p.storages[f.st.ID], n, "filex-vid-*.bin")
	require.NoError(t, err)
	require.Nil(t, in.file)
	require.Equal(t, f.p.workDir(), filepath.Dir(in.tmp), "the copy is not in the work directory")
	got, err := os.ReadFile(in.path)
	require.NoError(t, err)
	require.Equal(t, body, got)
	m, _ := filepath.Glob(filepath.Join(tmp, "filex-*"))
	require.Empty(t, m, "the copy went to the system temp directory")

	in.close()
	noCopies(t, f.p, tmp)
}

// Ciphertext is never handed to a program, in place or copied.
func TestSourceInput_AnEncryptedLocalFileIsRefused(t *testing.T) {
	isolateTemp(t)
	f := newArchiveFixture(t, nil)
	n := f.put("film.mp4", append(append([]byte{}, e2e.MagicPrefix...), "ciphertext"...))

	_, err := f.p.sourceInput(context.Background(), f.p.storages[f.st.ID], n, "filex-vid-*.bin")
	require.ErrorIs(t, err, errEncryptedContent)
}

// Startup empties the work directory, and removes the copies an older filex
// left in the system temp directory - but not a fresh one another process may
// still be writing, and nothing that is not one of ours.
func TestClearWork_RemovesWhatAKilledProcessLeftBehind(t *testing.T) {
	tmp := isolateTemp(t)
	p := New(nil, t.TempDir(), Capabilities{})
	require.NoError(t, os.MkdirAll(p.workDir(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(p.workDir(), "filex-vid-1.bin"), []byte("x"), 0o600))

	old := time.Now().Add(-2 * legacyTempAge)
	write := func(name string, mtime time.Time) string {
		path := filepath.Join(tmp, name)
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
		require.NoError(t, os.Chtimes(path, mtime, mtime))
		return path
	}
	stale := []string{write("filex-vid-11.bin", old), write("filex-aud-12.bin", old), write("filex-pdf-13.pdf", old), write("filex-zip-14", old)}
	fresh := write("filex-vid-15.bin", time.Now())
	other := write("someone-else.bin", old)

	p.ClearWork()

	_, err := os.Stat(p.workDir())
	require.True(t, os.IsNotExist(err), "the work directory survived startup")
	for _, s := range stale {
		_, err := os.Stat(s)
		require.True(t, os.IsNotExist(err), "a leftover copy survived startup: %s", s)
	}
	require.FileExists(t, fresh, "a copy another process may still be writing was removed")
	require.FileExists(t, other, "a file that is not filex's was removed")
}

// The whole path with the real program: a video on a local storage gets its
// frame, and nothing is copied anywhere. An all-black clip on purpose: the
// first pass finds no bright frame, so ffmpeg opens the source a second time.
func TestGenerateVideo_ALocalVideoGetsItsFrameWithoutACopy(t *testing.T) {
	ffmpegOrSkip(t)
	tmp := isolateTemp(t)
	f := newArchiveFixture(t, nil)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"bright.mp4", []string{"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30", "-t", "2"}},
		{"black.mp4", []string{"-f", "lavfi", "-i", "color=c=black:s=320x180:r=30:d=2"}},
	} {
		clip, err := os.ReadFile(makeClip(t, t.TempDir(), c.name, c.args...))
		require.NoError(t, err)
		n := f.put(c.name, clip)

		require.NoError(t, f.p.generateVideo(context.Background(), n, f.p.storages[f.st.ID]), c.name)
		require.True(t, wroteFrame(f.p.CachePath(n.ID)), "%s: no frame", c.name)
		noCopies(t, f.p, tmp)
	}
}

// Ghostscript under -dSAFER reads the inherited descriptor too.
func TestGeneratePDF_ALocalPDFRendersWithoutACopy(t *testing.T) {
	gsOrSkip(t)
	tmp := isolateTemp(t)
	f := newArchiveFixture(t, nil)
	pdf, err := os.ReadFile(makePDF(t, t.TempDir(), 2))
	require.NoError(t, err)
	n := f.put("doc.pdf", pdf)

	require.NoError(t, f.p.generatePDF(context.Background(), n, f.p.storages[f.st.ID]))
	require.True(t, wroteFrame(f.p.CachePath(n.ID)), "no page")
	noCopies(t, f.p, tmp)
}
