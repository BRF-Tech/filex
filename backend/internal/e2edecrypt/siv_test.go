package e2edecrypt

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// nameVectors is testdata/name-vectors.json: AES-256-SIV name vectors that
// Python `cryptography`'s AESSIV produced (gen_name_vectors.py) — a third
// implementation, sharing nothing with this package or with the browser's.
type nameVectors struct {
	KeyHex     string `json:"key_hex"`
	RootID     string `json:"root_id"`
	DirIDLabel string `json:"dir_id_label"`
	Long       int    `json:"long"`
	RFC        struct {
		KeyHex       string `json:"key_hex"`
		ADHex        string `json:"ad_hex"`
		PlaintextHex string `json:"plaintext_hex"`
		OutputHex    string `json:"output_hex"`
	} `json:"rfc5297_a1"`
	Vectors []struct {
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
		Dir      bool   `json:"dir"`
		DirID    string `json:"dir_id"`
		Encoded  string `json:"encoded"`
		Stored   string `json:"stored"`
		Sidecar  string `json:"sidecar"`
	} `json:"vectors"`
}

func loadVectors(t *testing.T) nameVectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "name-vectors.json"))
	require.NoError(t, err)
	var v nameVectors
	require.NoError(t, json.Unmarshal(b, &v))
	require.NotEmpty(t, v.Vectors)
	return v
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func TestSiv_RFC5297AppendixA1(t *testing.T) {
	v := loadVectors(t).RFC
	k, err := newSivKey(unhex(t, v.KeyHex))
	require.NoError(t, err)
	out := k.seal(unhex(t, v.PlaintextHex), unhex(t, v.ADHex))
	require.Equal(t, v.OutputHex, hex.EncodeToString(out))
	p, err := k.open(out, unhex(t, v.ADHex))
	require.NoError(t, err)
	require.Equal(t, v.PlaintextHex, hex.EncodeToString(p))
}

func TestSiv_RefusesEveryFlippedBit(t *testing.T) {
	k, err := newSivKey(unhex(t, loadVectors(t).KeyHex))
	require.NoError(t, err)
	sealed := k.seal([]byte("Rapor.docx"))
	for i := range sealed {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte(nil), sealed...)
			bad[i] ^= 1 << bit
			_, err := k.open(bad)
			require.ErrorIs(t, err, errSivAuth, "byte %d bit %d", i, bit)
		}
	}
}

func TestSiv_KeySizes(t *testing.T) {
	for _, n := range []int{0, 16, 48, 65} {
		_, err := newSivKey(make([]byte, n))
		require.Error(t, err, "len %d", n)
	}
}

func TestSiv_MessageLengthsAroundTheBlock(t *testing.T) {
	// S2V takes a different branch below, at and above one block, and CMAC
	// pads an incomplete last block: every length from 0 to 3 blocks
	// round-trips.
	k, err := newSivKey(unhex(t, loadVectors(t).KeyHex))
	require.NoError(t, err)
	for n := 0; n <= 48; n++ {
		p := make([]byte, n)
		for i := range p {
			p[i] = byte(i*7 + n)
		}
		got, err := k.open(k.seal(p))
		require.NoError(t, err, "len %d", n)
		require.Equal(t, p, got, "len %d", n)
	}
}
