package e2edecrypt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// The writing half (encrypt.go), held to the same independent vectors the
// reading half and the browser are held to (testdata/stream-vectors.json,
// Python `cryptography`). A Go writer that agrees byte for byte with them
// writes what the browser reads (web/tests/lib/e2estream.test.ts opens the
// same vectors), so "Go writes, TS reads" is pinned without trusting either
// hand-written implementation.

// fixedRand hands out exactly the given bytes, in order, then fails: a writer
// that drew its randomness in another order or amount than the format's
// reference would produce other bytes, or run dry.
func fixedRand(parts ...[]byte) *bytes.Reader {
	return bytes.NewReader(bytes.Join(parts, nil))
}

func TestEncryptStream_MatchesTheIndependentVectors(t *testing.T) {
	v := loadStreamVectors(t)
	key, prefix := mustHex(t, v.KeyHex), mustHex(t, v.PrefixHex)
	require.NotEmpty(t, v.Stream)
	for _, c := range v.Stream {
		plain := pattern(c.Len, c.Seed)
		var out bytes.Buffer
		n, err := EncryptStream(&out, bytes.NewReader(plain), key, prefix, c.Log2, int64(c.Len))
		require.NoError(t, err, "len %d @2^%d", c.Len, c.Log2)
		require.EqualValues(t, c.CtLen, n)
		sum := sha256.Sum256(out.Bytes())
		require.Equal(t, c.CtSha256, hex.EncodeToString(sum[:]), "len %d @2^%d", c.Len, c.Log2)
		if c.CtHex != "" {
			require.Equal(t, c.CtHex, hex.EncodeToString(out.Bytes()))
		}
	}
}

func TestEncryptStreamFile_IsTheVectorFolderFile(t *testing.T) {
	// gen_stream_vectors.py: FMK = 100..131, DEK = 200..231, wrapIV = 1..12,
	// prefix B0..B6, 2^10 chunks - drawn in that order by the writer, as the
	// browser draws them (rawDek, wrapIV, prefix).
	v := loadStreamVectors(t)
	fmk := mustHex(t, v.FolderV2.FmkHex)
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(200 + i)
	}
	wrapIV := make([]byte, 12)
	for i := range wrapIV {
		wrapIV[i] = byte(1 + i)
	}
	prefix := []byte{0xB0, 0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6}
	rnd := fixedRand(dek, wrapIV, prefix)

	var out bytes.Buffer
	plain := pattern(v.FolderV2.Len, v.FolderV2.Seed)
	require.NoError(t, encryptStreamFile(&out, bytes.NewReader(plain), int64(len(plain)), fmk, 10, rnd))
	require.Zero(t, rnd.Len(), "every random byte the reference draws, and no more")
	require.Equal(t, v.FolderV2.FileHex, hex.EncodeToString(out.Bytes()))
}

func TestFormatRecoveryKey_IsTheVectorKey(t *testing.T) {
	v := loadStreamVectors(t)
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(50 + i) // RK_RAW in gen_stream_vectors.py
	}
	require.Equal(t, v.Fxe.RecoveryKey, FormatRecoveryKey(raw))
	back, ok := ParseRecoveryKey(FormatRecoveryKey(raw))
	require.True(t, ok)
	require.Equal(t, raw, back)
}

// writeThenRead encrypts with the Go writer and reads it back with the Go
// reader: the round trip at every size that changes the shape of a STREAM.
func TestEncryptFile_RoundTripsAtEveryShape(t *testing.T) {
	fmk := pattern(32, 99)
	const log2 = 10
	chunk := 1 << log2
	for _, n := range []int{0, 1, chunk - 1, chunk, chunk + 1, 2 * chunk, 3*chunk + 77} {
		plain := pattern(n, n)
		var file bytes.Buffer
		require.NoError(t, encryptStreamFile(&file, bytes.NewReader(plain), int64(n), fmk, log2, nil))
		require.EqualValues(t, headerLen+StreamCiphertextSize(int64(n), log2), file.Len(), "size %d", n)
		require.EqualValues(t, 2, file.Bytes()[8])
		var out bytes.Buffer
		require.NoError(t, DecryptFileStream(&out, bytes.NewReader(file.Bytes()), fmk, nil), "size %d", n)
		require.True(t, bytes.Equal(plain, out.Bytes()), "size %d", n)
	}
}

// The one-shot format stays what every filex since v0.30 reads: a small file
// is 0x01, the reserved bytes are zero, and the older reader path opens it.
func TestEncryptFile_SmallFilesKeepTheOneShotFormat(t *testing.T) {
	fmk := pattern(32, 5)
	for _, n := range []int{0, 1, 4096} {
		plain := pattern(n, 3)
		var file bytes.Buffer
		require.NoError(t, EncryptFile(&file, bytes.NewReader(plain), int64(n), fmk, nil))
		b := file.Bytes()
		require.EqualValues(t, EncryptedFileSize(int64(n)), len(b))
		require.True(t, HasMagic(b))
		require.EqualValues(t, 1, b[8], "0x01 below the one-shot limit")
		require.Equal(t, make([]byte, 16), b[81:97], "reserved bytes")
		got, err := DecryptContent(fmk, b)
		require.NoError(t, err)
		require.True(t, bytes.Equal(plain, got))
	}
	// Above the limit the size alone picks STREAM, at the writers' chunk size.
	require.EqualValues(t, headerLen+StreamCiphertextSize(MaxOneShot+1, StreamChunkLog2), EncryptedFileSize(MaxOneShot+1))
	require.EqualValues(t, headerLen+MaxOneShot+gcmTagLen, EncryptedFileSize(MaxOneShot))
}

// What a writer produced is refused by the reader the moment anyone has
// reordered, cut, extended, replayed or flipped it.
func TestEncryptStream_TamperingWhatGoWroteIsRefused(t *testing.T) {
	key, prefix := pattern(32, 1), []byte{1, 2, 3, 4, 5, 6, 7}
	const log2 = 10
	full := 1<<log2 + gcmTagLen
	plain := pattern(4*1024+300, 9) // 5 chunks, the last one short
	var body bytes.Buffer
	_, err := EncryptStream(&body, bytes.NewReader(plain), key, prefix, log2, int64(len(plain)))
	require.NoError(t, err)
	ct := body.Bytes()
	open := func(b []byte) error {
		_, err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(b), key, prefix, log2, -1)
		return err
	}
	require.NoError(t, open(ct))

	chunkAt := func(i int) []byte { return ct[i*full : min((i+1)*full, len(ct))] }
	join := func(idx ...int) []byte {
		var out []byte
		for _, i := range idx {
			out = append(out, chunkAt(i)...)
		}
		return out
	}
	cases := map[string][]byte{
		"two chunks swapped":           join(1, 0, 2, 3, 4),
		"the last two swapped":         join(0, 1, 2, 4, 3),
		"cut after a whole chunk":      join(0, 1, 2),
		"cut before the last":          join(0, 1, 2, 3),
		"cut inside a chunk":           ct[:2*full+100],
		"a chunk replayed":             join(0, 1, 1, 2, 3, 4),
		"the last chunk replayed":      join(0, 1, 2, 3, 4, 4),
		"a chunk dropped":              join(0, 2, 3, 4),
		"an empty last chunk appended": append(append([]byte{}, ct...), make([]byte, gcmTagLen)...),
		"another file's chunk":         append(join(0, 1, 2, 3), otherLastChunk(t, prefix, log2)...),
	}
	for name, b := range cases {
		require.ErrorIs(t, open(b), ErrContent, name)
	}
	for _, at := range []int{0, full - 1, 3 * full, len(ct) - 1} {
		bad := append([]byte{}, ct...)
		bad[at] ^= 0x80
		require.ErrorIs(t, open(bad), ErrContent, "bit at %d", at)
	}
}

// otherLastChunk is a valid final chunk of ANOTHER stream (another key): it
// must not pass for this one's end.
func otherLastChunk(t *testing.T, prefix []byte, log2 int) []byte {
	t.Helper()
	var b bytes.Buffer
	_, err := EncryptStream(&b, bytes.NewReader(pattern(10, 4)), pattern(32, 77), prefix, log2, 10)
	require.NoError(t, err)
	return b.Bytes()
}

// A writer told the wrong size makes nothing a reader would take as the
// file: too few bytes and too many are both refused before a last chunk is
// sealed over the wrong end.
func TestEncryptStream_RefusesASizeTheContentDoesNotHave(t *testing.T) {
	key, prefix := pattern(32, 1), []byte{1, 2, 3, 4, 5, 6, 7}
	plain := pattern(3000, 2)
	for _, said := range []int64{2999, 3001, 0, 1 << 10, 4096} {
		_, err := EncryptStream(io.Discard, bytes.NewReader(plain), key, prefix, 10, said)
		require.ErrorIs(t, err, ErrSizeMismatch, "said %d", said)
	}
	err := EncryptFile(io.Discard, bytes.NewReader(plain), 2999, pattern(32, 3), nil)
	require.ErrorIs(t, err, ErrSizeMismatch)
	err = EncryptFile(io.Discard, bytes.NewReader(plain), 3001, pattern(32, 3), nil)
	require.ErrorIs(t, err, ErrSizeMismatch)
}

func TestEncryptStream_RefusesWhatNoReaderTakes(t *testing.T) {
	key := pattern(32, 1)
	_, err := EncryptStream(io.Discard, bytes.NewReader(nil), key, []byte{1, 2, 3}, 10, 0)
	require.Error(t, err, "a prefix that is not 7 bytes")
	_, err = EncryptStream(io.Discard, bytes.NewReader(nil), key, make([]byte, 7), 9, 0)
	require.Error(t, err, "a chunk size below 2^10")
	_, err = EncryptStream(io.Discard, bytes.NewReader(nil), key, make([]byte, 7), 25, 0)
	require.Error(t, err, "a chunk size above 2^24")
	_, err = EncryptContent(pattern(16, 1), []byte("x"), nil)
	require.Error(t, err, "a folder key that is not 32 bytes")
}

// Two files never share a file key or a nonce prefix: every one is drawn
// fresh.
func TestEncryptFile_FreshKeyAndPrefixPerFile(t *testing.T) {
	fmk := pattern(32, 8)
	var a, b bytes.Buffer
	plain := pattern(2000, 1)
	require.NoError(t, encryptStreamFile(&a, bytes.NewReader(plain), 2000, fmk, 10, nil))
	require.NoError(t, encryptStreamFile(&b, bytes.NewReader(plain), 2000, fmk, 10, nil))
	require.False(t, bytes.Equal(a.Bytes()[9:69], b.Bytes()[9:69]), "wrapped key")
	require.False(t, bytes.Equal(a.Bytes()[prefixOff:prefixOff+7], b.Bytes()[prefixOff:prefixOff+7]), "nonce prefix")
	require.False(t, bytes.Equal(a.Bytes()[headerLen:], b.Bytes()[headerLen:]), "body")
}

func TestEncryptFile_ARandomSourceThatFailsStopsTheWriter(t *testing.T) {
	err := EncryptFile(io.Discard, bytes.NewReader([]byte("abc")), 3, pattern(32, 1), bytes.NewReader([]byte{1, 2, 3}))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrSizeMismatch))
}
