package staging_test

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/staging"
)

func newArea(t *testing.T) *staging.Area {
	t.Helper()
	return staging.New(t.TempDir())
}

// The offset is the contiguous run from part 1, not the total staged bytes.
// A client resuming from "everything I have" instead would skip the hole and
// upload a file with a gap in the middle.
func TestManifest_OffsetIsTheContiguousRunOnly(t *testing.T) {
	a := newArea(t)
	id := "11111111-2222-3333-4444-555555555555"
	_, err := a.Create(id, 300, 100, "")
	require.NoError(t, err)

	// Part 3 lands first.
	m, err := a.WritePart(id, 3, bytes.NewReader(bytes.Repeat([]byte{'c'}, 100)), 100)
	require.NoError(t, err)
	assert.EqualValues(t, 0, m.Offset(), "a part past a hole must not move the resume point")
	assert.EqualValues(t, 100, m.Received(), "…but it IS staged and must be reported as such")
	assert.False(t, m.Complete())

	m, err = a.WritePart(id, 1, bytes.NewReader(bytes.Repeat([]byte{'a'}, 100)), 100)
	require.NoError(t, err)
	assert.EqualValues(t, 100, m.Offset())

	m, err = a.WritePart(id, 2, bytes.NewReader(bytes.Repeat([]byte{'b'}, 100)), 100)
	require.NoError(t, err)
	assert.EqualValues(t, 300, m.Offset(), "closing the hole advances past everything already staged")
	assert.True(t, m.Complete())

	rd, err := a.Open(id)
	require.NoError(t, err)
	defer rd.Close()
	got, err := io.ReadAll(rd)
	require.NoError(t, err)
	assert.Equal(t,
		strings.Repeat("a", 100)+strings.Repeat("b", 100)+strings.Repeat("c", 100),
		string(got),
		"assembly is by part number, never by arrival order")
}

// A truncated body must leave no trace: the part is not recorded, the offset
// does not move, and no half-written file is left behind.
func TestWritePart_ShortBodyIsDiscarded(t *testing.T) {
	a := newArea(t)
	id := "aaaaaaaa-2222-3333-4444-555555555555"
	_, err := a.Create(id, 200, 100, "")
	require.NoError(t, err)

	_, err = a.WritePart(id, 1, bytes.NewReader([]byte("short")), 100)
	require.ErrorIs(t, err, staging.ErrShortPart)

	m, err := a.Manifest(id)
	require.NoError(t, err)
	assert.EqualValues(t, 0, m.Offset())
	assert.Empty(t, m.Parts)

	ents, err := os.ReadDir(a.Dir(id))
	require.NoError(t, err)
	for _, e := range ents {
		assert.Equal(t, staging.ManifestName, e.Name(),
			"a failed chunk must not leave a temp or partial file behind")
	}

	// A body LONGER than declared is refused too — otherwise the assembled
	// object would be longer than the size the caller verified.
	_, err = a.WritePart(id, 1, bytes.NewReader(bytes.Repeat([]byte{'x'}, 150)), 100)
	require.ErrorIs(t, err, staging.ErrShortPart)
}

// Re-sending a chunk is how a client that is not sure whether its last write
// landed recovers. It must be idempotent, not a duplicate part.
func TestWritePart_RewriteIsIdempotent(t *testing.T) {
	a := newArea(t)
	id := "bbbbbbbb-2222-3333-4444-555555555555"
	_, err := a.Create(id, 100, 100, "")
	require.NoError(t, err)

	_, err = a.WritePart(id, 1, bytes.NewReader(bytes.Repeat([]byte{'a'}, 100)), 100)
	require.NoError(t, err)
	m, err := a.WritePart(id, 1, bytes.NewReader(bytes.Repeat([]byte{'b'}, 100)), 100)
	require.NoError(t, err)
	require.Len(t, m.Parts, 1)
	assert.EqualValues(t, 100, m.Offset())

	rd, err := a.Open(id)
	require.NoError(t, err)
	defer rd.Close()
	got, _ := io.ReadAll(rd)
	assert.Equal(t, strings.Repeat("b", 100), string(got))
}

func TestWritePart_RejectsOutOfGridParts(t *testing.T) {
	a := newArea(t)
	id := "cccccccc-2222-3333-4444-555555555555"
	_, err := a.Create(id, 250, 100, "") // 3 parts: 100, 100, 50
	require.NoError(t, err)

	_, err = a.WritePart(id, 4, bytes.NewReader([]byte("x")), 1)
	assert.ErrorIs(t, err, staging.ErrBadPart, "there is no part 4 in a 3-part grid")

	_, err = a.WritePart(id, 0, bytes.NewReader([]byte("x")), 1)
	assert.ErrorIs(t, err, staging.ErrBadPart)

	// The last part is 50 bytes, not a full chunk.
	_, err = a.WritePart(id, 3, bytes.NewReader(bytes.Repeat([]byte{'z'}, 100)), 100)
	assert.ErrorIs(t, err, staging.ErrBadPart)
	_, err = a.WritePart(id, 3, bytes.NewReader(bytes.Repeat([]byte{'z'}, 50)), 50)
	assert.NoError(t, err)
}

// Open refuses an incomplete upload — handing a driver an object assembled over
// a hole would write a silently truncated file.
func TestOpen_RefusesIncomplete(t *testing.T) {
	a := newArea(t)
	id := "dddddddd-2222-3333-4444-555555555555"
	_, err := a.Create(id, 200, 100, "")
	require.NoError(t, err)
	_, err = a.WritePart(id, 1, bytes.NewReader(bytes.Repeat([]byte{'a'}, 100)), 100)
	require.NoError(t, err)

	_, err = a.Open(id)
	assert.ErrorIs(t, err, staging.ErrIncomplete)
}

// The assembled reader must seek across part boundaries — chunk 5 serves byte
// ranges out of staging, and the S3 SDK replays a body on retry.
func TestReader_SeeksAcrossParts(t *testing.T) {
	a := newArea(t)
	id := "eeeeeeee-2222-3333-4444-555555555555"
	_, err := a.Create(id, 30, 10, "")
	require.NoError(t, err)
	for i, chunk := range []string{"0123456789", "abcdefghij", "ABCDEFGHIJ"} {
		_, err := a.WritePart(id, i+1, strings.NewReader(chunk), 10)
		require.NoError(t, err)
	}

	rd, err := a.Open(id)
	require.NoError(t, err)
	defer rd.Close()
	assert.EqualValues(t, 30, rd.Size())

	// A read that spans the boundary between two part files.
	_, err = rd.Seek(8, io.SeekStart)
	require.NoError(t, err)
	buf := make([]byte, 6)
	n, err := io.ReadFull(rd, buf)
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, "89abcd", string(buf))

	// Backwards, then to the very end.
	_, err = rd.Seek(0, io.SeekStart)
	require.NoError(t, err)
	all, err := io.ReadAll(rd)
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdefghijABCDEFGHIJ", string(all))

	_, err = rd.Seek(-4, io.SeekEnd)
	require.NoError(t, err)
	tail, err := io.ReadAll(rd)
	require.NoError(t, err)
	assert.Equal(t, "GHIJ", string(tail))
}

// The per-part md5 exists so the S3 composite ETag is computable from the
// manifest alone, without re-reading the object.
func TestManifest_CompositeETag(t *testing.T) {
	a := newArea(t)
	id := "ffffffff-2222-3333-4444-555555555555"
	_, err := a.Create(id, 20, 10, "")
	require.NoError(t, err)
	p1 := bytes.Repeat([]byte{'a'}, 10)
	p2 := bytes.Repeat([]byte{'b'}, 10)
	_, err = a.WritePart(id, 1, bytes.NewReader(p1), 10)
	require.NoError(t, err)
	m, err := a.WritePart(id, 2, bytes.NewReader(p2), 10)
	require.NoError(t, err)

	s1, s2 := md5.Sum(p1), md5.Sum(p2)
	outer := md5.New()
	outer.Write(s1[:])
	outer.Write(s2[:])
	want := hex.EncodeToString(outer.Sum(nil)) + "-2"
	assert.Equal(t, want, m.CompositeETag())
}

// Path traversal cannot reach the filesystem: an id is validated before any
// path is built from it.
func TestValidID_RejectsTraversal(t *testing.T) {
	for _, bad := range []string{"", "..", "../../etc", "a/b", "short", `x\y`, "with space", strings.Repeat("a", 65)} {
		assert.False(t, staging.ValidID(bad), "id %q must be rejected", bad)
	}
	assert.True(t, staging.ValidID("11111111-2222-3333-4444-555555555555"))

	a := newArea(t)
	_, err := a.Create("../escape", 10, 10, "")
	assert.ErrorIs(t, err, staging.ErrBadID)
	assert.Equal(t, "", a.Dir("../escape"))
}

// The disk guard refuses when free space is below size × 1.2, and stays quiet
// when it cannot measure — a guard that blocks every upload because it cannot
// read a number is worse than no guard.
func TestEnsureFree_Threshold(t *testing.T) {
	a := newArea(t)
	a.FreeBytes = func(string) (uint64, error) { return 1200, nil }

	assert.NoError(t, a.EnsureFree(1000), "1000 × 1.2 == 1200 free — exactly enough")
	err := a.EnsureFree(1001)
	require.ErrorIs(t, err, staging.ErrNoDiskSpace)
	assert.Contains(t, err.Error(), "free")

	a.FreeBytes = func(string) (uint64, error) { return 0, errors.New("no statfs here") }
	assert.NoError(t, a.EnsureFree(1<<40))
}

func TestCreate_RejectsTooManyParts(t *testing.T) {
	a := newArea(t)
	_, err := a.Create("99999999-2222-3333-4444-555555555555", int64(staging.MaxParts+1)*10, 10, "")
	assert.ErrorIs(t, err, staging.ErrTooManyParts)
}

// Sweep removes idle directories and protects the ones the caller still knows
// about.
func TestSweep_RemovesIdleAndKeepsLive(t *testing.T) {
	a := newArea(t)
	idle := "12121212-2222-3333-4444-555555555555"
	live := "34343434-2222-3333-4444-555555555555"
	protected := "56565656-2222-3333-4444-555555555555"
	for _, id := range []string{idle, live, protected} {
		_, err := a.Create(id, 10, 10, "")
		require.NoError(t, err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, id := range []string{idle, protected} {
		require.NoError(t, os.Chtimes(filepath.Join(a.Dir(id), staging.ManifestName), old, old))
	}

	removed, err := a.Sweep(24*time.Hour, time.Now(), func(id string) bool { return id == protected })
	require.NoError(t, err)
	assert.Equal(t, []string{idle}, removed)
	assert.NoDirExists(t, a.Dir(idle))
	assert.DirExists(t, a.Dir(live), "an upload that is not idle survives")
	assert.DirExists(t, a.Dir(protected), "an upload the caller vouches for survives")
}

// A zero-byte file is a legitimate upload with no parts at all.
func TestZeroByteUpload_IsCompleteImmediately(t *testing.T) {
	a := newArea(t)
	id := "78787878-2222-3333-4444-555555555555"
	m, err := a.Create(id, 0, 4096, "")
	require.NoError(t, err)
	assert.True(t, m.Complete())
	assert.EqualValues(t, 0, m.Offset())

	rd, err := a.Open(id)
	require.NoError(t, err)
	defer rd.Close()
	b, err := io.ReadAll(rd)
	require.NoError(t, err)
	assert.Empty(t, b)
}

// A disabled area (no staging directory configured) must refuse rather than
// write somewhere unexpected.
func TestDisabledArea_Refuses(t *testing.T) {
	a := staging.New("")
	assert.False(t, a.Enabled())
	_, err := a.Create("11111111-2222-3333-4444-555555555555", 10, 10, "")
	assert.Error(t, err)
}
