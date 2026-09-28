package e2edecrypt

// A folder being encrypted in place (feature "conv"): the key file says so,
// some files are still plaintext, and `filex decrypt` copies those with a
// warning that names the conversion rather than calling them strays.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarker_ConvFeature(t *testing.T) {
	base := `"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc="`
	m, err := ParseMarker([]byte(`{"v":3,"req":["conv"],"conv":{"pending":true,"started":"2026-09-27T10:00:00Z"},` + base + `}`))
	require.NoError(t, err)
	require.True(t, m.ConvPending)

	for what, bad := range map[string]string{
		"required, no slot":  `{"v":3,"req":["conv"],` + base + `}`,
		"slot, not required": `{"v":3,"req":[],"conv":{"pending":true},` + base + `}`,
		"slot not pending":   `{"v":3,"req":["conv"],"conv":{"pending":false},` + base + `}`,
		"on a v2 key file":   `{"v":2,"conv":{"pending":true},` + base + `}`,
	} {
		_, err := ParseMarker([]byte(bad))
		require.ErrorIs(t, err, ErrNotMarker, what)
	}
}

func TestDecryptTree_AFolderMidConversion(t *testing.T) {
	// v2-password-changed stands in for a folder half converted: its key file
	// gains `conv`, and one plaintext file is added beside the encrypted ones.
	c := loadFixtures(t)["v2-password-changed"]
	in := copyTree(t, fixtureDir("v2-password-changed"))
	b, err := os.ReadFile(filepath.Join(in, MarkerName))
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	raw["v"] = 3
	raw["req"] = []string{"conv"}
	raw["conv"] = map[string]any{"pending": true}
	nb, _ := json.Marshal(raw)
	require.NoError(t, os.WriteFile(filepath.Join(in, MarkerName), nb, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(in, "not-yet.txt"), []byte("still plaintext\n"), 0o600))

	out := filepath.Join(t.TempDir(), "plain")
	res, err := DecryptTree(in, OpenOptions{Out: out, GOOS: "linux"}, c.Password, false)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(out, "not-yet.txt"))
	require.NoError(t, err)
	require.Equal(t, "still plaintext\n", string(got))
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "not-yet.txt") && strings.Contains(w, "encrypted in place") {
			found = true
		}
	}
	require.True(t, found, "%v", res.Warnings)
}
