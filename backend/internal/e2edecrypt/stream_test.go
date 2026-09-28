package e2edecrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// STREAM, the 0x02 folder file and the .fxe container, held to vectors an
// independent implementation wrote (testdata/gen_stream_vectors.py, Python
// `cryptography`). The browser is tested against the same file
// (web/tests/lib/e2estream.test.ts, e2efile.test.ts).

type streamVectors struct {
	KeyHex    string `json:"key_hex"`
	PrefixHex string `json:"prefix_hex"`
	Stream    []struct {
		Log2     int    `json:"log2"`
		Len      int    `json:"len"`
		Seed     int    `json:"seed"`
		CtLen    int    `json:"ct_len"`
		CtSha256 string `json:"ct_sha256"`
		CtHex    string `json:"ct_hex"`
	} `json:"stream"`
	FolderV2 struct {
		FmkHex  string `json:"fmk_hex"`
		Len     int    `json:"len"`
		Seed    int    `json:"seed"`
		FileHex string `json:"file_hex"`
	} `json:"folder_v2"`
	Fxe struct {
		Password    string `json:"password"`
		RecoveryKey string `json:"recovery_key"`
		Name        string `json:"name"`
		Len         int    `json:"len"`
		Seed        int    `json:"seed"`
		FileHex     string `json:"file_hex"`
	} `json:"fxe"`
}

func loadStreamVectors(t *testing.T) streamVectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stream-vectors.json"))
	require.NoError(t, err)
	var v streamVectors
	require.NoError(t, json.Unmarshal(b, &v))
	return v
}

func pattern(n, seed int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*31 + seed) & 0xff)
	}
	return out
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// encryptStream is a test-only writer, to build damaged inputs; the vectors
// below pin it to the independent implementation first.
func encryptStream(t *testing.T, key, prefix, plain []byte, log2 int) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	g, err := cipher.NewGCM(block)
	require.NoError(t, err)
	size := 1 << log2
	var out []byte
	n := (len(plain) + size - 1) / size
	if n == 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		end := min((i+1)*size, len(plain))
		out = g.Seal(out, streamNonce(prefix, uint32(i), i == n-1), plain[i*size:end], nil)
	}
	return out
}

func TestStream_Vectors(t *testing.T) {
	v := loadStreamVectors(t)
	key, prefix := mustHex(t, v.KeyHex), mustHex(t, v.PrefixHex)
	require.NotEmpty(t, v.Stream)
	for _, c := range v.Stream {
		plain := pattern(c.Len, c.Seed)
		ct := encryptStream(t, key, prefix, plain, c.Log2)
		sum := sha256.Sum256(ct)
		require.Equal(t, c.CtSha256, hex.EncodeToString(sum[:]), "len %d @2^%d", c.Len, c.Log2)
		require.EqualValues(t, c.CtLen, StreamCiphertextSize(int64(c.Len), c.Log2))
		var out bytes.Buffer
		n, err := DecryptStream(&out, bytes.NewReader(ct), key, prefix, c.Log2, int64(c.Len))
		require.NoError(t, err, "len %d @2^%d", c.Len, c.Log2)
		require.EqualValues(t, c.Len, n)
		require.True(t, bytes.Equal(plain, out.Bytes()))
	}
}

func TestStream_TamperingIsRefused(t *testing.T) {
	v := loadStreamVectors(t)
	key, prefix := mustHex(t, v.KeyHex), mustHex(t, v.PrefixHex)
	const log2 = 10
	full := 1<<log2 + gcmTagLen
	plain := pattern(3*1024+100, 7) // 4 chunks
	ct := encryptStream(t, key, prefix, plain, log2)
	open := func(b []byte, expect int64) error {
		_, err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(b), key, prefix, log2, expect)
		return err
	}
	require.NoError(t, open(ct, int64(len(plain))))

	for _, cut := range []int{0, 10, full, 2 * full, 3 * full, 3*full + 50, len(ct) - 1} {
		require.ErrorIs(t, open(ct[:cut], -1), ErrContent, "cut at %d", cut)
	}
	swapped := append([]byte{}, ct...)
	copy(swapped[:full], ct[full:2*full])
	copy(swapped[full:2*full], ct[:full])
	require.ErrorIs(t, open(swapped, -1), ErrContent, "reordered")
	for _, extra := range [][]byte{ct[3*full:], ct[:full], make([]byte, gcmTagLen)} {
		longer := append(append([]byte{}, ct...), extra...)
		require.ErrorIs(t, open(longer, -1), ErrContent, "appended %d bytes", len(extra))
	}
	for _, at := range []int{0, full - 1, full, 2*full + 17, len(ct) - 1} {
		bad := append([]byte{}, ct...)
		bad[at] ^= 1
		require.ErrorIs(t, open(bad, -1), ErrContent, "bit at %d", at)
	}
	require.ErrorIs(t, open(ct, int64(len(plain)-1)), ErrContent, "a header that says less")
	require.ErrorIs(t, open(ct, int64(len(plain)+1)), ErrContent, "a header that says more")
	_, err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(ct), key, prefix, 30, -1)
	require.ErrorIs(t, err, ErrContent, "a chunk size no reader accepts")
}

func TestFolderFileV2_Vector(t *testing.T) {
	v := loadStreamVectors(t)
	fmk := mustHex(t, v.FolderV2.FmkHex)
	file := mustHex(t, v.FolderV2.FileHex)
	require.EqualValues(t, 2, file[8])
	want := pattern(v.FolderV2.Len, v.FolderV2.Seed)

	whole, err := DecryptContent(fmk, file)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, whole))

	var out bytes.Buffer
	require.NoError(t, DecryptFileStream(&out, bytes.NewReader(file), fmk, nil))
	require.True(t, bytes.Equal(want, out.Bytes()))

	// the previous key of a folder mid re-key is tried second
	out.Reset()
	require.NoError(t, DecryptFileStream(&out, bytes.NewReader(file), make([]byte, 32), fmk))
	require.True(t, bytes.Equal(want, out.Bytes()))

	require.ErrorIs(t, DecryptFileStream(&bytes.Buffer{}, bytes.NewReader(file), make([]byte, 32), nil), ErrContent)
	require.ErrorIs(t, DecryptFileStream(&bytes.Buffer{}, bytes.NewReader(file[:len(file)-100]), fmk, nil), ErrContent)

	newer := append([]byte{}, file...)
	newer[8] = 3
	var ue *UnsupportedError
	require.True(t, errors.As(DecryptFileStream(&bytes.Buffer{}, bytes.NewReader(newer), fmk, nil), &ue))
}

func TestFxe_Vector(t *testing.T) {
	v := loadStreamVectors(t)
	file := mustHex(t, v.Fxe.FileHex)
	want := pattern(v.Fxe.Len, v.Fxe.Seed)

	open := func(b []byte, secret string, recovery bool) ([]byte, string, error) {
		r := bytes.NewReader(b)
		h, err := ReadFxePrefix(r)
		if err != nil {
			return nil, "", err
		}
		k, err := h.Unlock(secret, recovery)
		if err != nil {
			return nil, "", err
		}
		var out bytes.Buffer
		err = DecryptFxe(&out, r, h, k)
		return out.Bytes(), k.Name, err
	}

	got, name, err := open(file, v.Fxe.Password, false)
	require.NoError(t, err)
	require.Equal(t, v.Fxe.Name, name)
	require.True(t, bytes.Equal(want, got))

	got, name, err = open(file, v.Fxe.RecoveryKey, true)
	require.NoError(t, err)
	require.Equal(t, v.Fxe.Name, name)
	require.True(t, bytes.Equal(want, got))

	_, _, err = open(file, v.Fxe.Password+"x", false)
	require.ErrorIs(t, err, ErrWrongPassword)
	_, _, err = open(file, "AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA", true)
	require.ErrorIs(t, err, ErrWrongRecoveryKey)

	_, _, err = open(file[:len(file)-1], v.Fxe.Password, false)
	require.ErrorIs(t, err, ErrContent)
	var ce *CorruptError
	require.ErrorAs(t, err, &ce)

	newer := append([]byte{}, file...)
	newer[8] = 2
	_, _, err = open(newer, v.Fxe.Password, false)
	var ue *UnsupportedError
	require.ErrorAs(t, err, &ue)
}

func TestFxe_UnknownRequiredFeature(t *testing.T) {
	hdr := []byte(`{"salt":"AAAAAAAAAAAAAAAAAAAAAA==","iter":1000,"verify":"x","fmk":"wrapped","fmk_pw":"x",` +
		`"dek":"x","name":"x","chunk":20,"nonce":"AAAAAAAAAA==","size":0,"req":["vault"]}`)
	_, err := ParseFxeHeader(hdr)
	var ue *UnsupportedError
	require.ErrorAs(t, err, &ue)
	require.Equal(t, []string{"vault"}, ue.Features)

	bad := bytes.Replace(hdr, []byte(`"chunk":20`), []byte(`"chunk":30`), 1)
	_, err = ParseFxeHeader(bad)
	var ce *CorruptError
	require.ErrorAs(t, err, &ce)
}
