package cliclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRemotePath covers the accepted and rejected argument shapes.
func TestParseRemotePath(t *testing.T) {
	cases := []struct {
		in      string
		adapter string
		rel     string
		wantErr bool
	}{
		{"docs://reports/2026", "docs", "reports/2026", false},
		{"docs://", "docs", "", false},
		{"docs:///leading/and/trailing/", "docs", "leading/and/trailing", false},
		{"docs://a//b", "docs", "a/b", false},
		{"docs://./x", "docs", "x", false},
		{"s3-test://çılgın dosya.txt", "s3-test", "çılgın dosya.txt", false},
		{"no-scheme/path", "", "", true},
		{"://rel", "", "", true},
		{"docs://../escape", "", "", true},
		{"docs://a/../../b", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range cases {
		got, err := ParseRemotePath(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "input %q", tc.in)
			continue
		}
		require.NoError(t, err, "input %q", tc.in)
		assert.Equal(t, tc.adapter, got.Adapter, "input %q", tc.in)
		assert.Equal(t, tc.rel, got.Rel, "input %q", tc.in)
	}
}

// TestRemotePath_Helpers exercises String/Base/Dir/Join/IsRoot.
func TestRemotePath_Helpers(t *testing.T) {
	p, err := ParseRemotePath("docs://a/b/c.txt")
	require.NoError(t, err)

	assert.Equal(t, "docs://a/b/c.txt", p.String())
	assert.Equal(t, "c.txt", p.Base())
	assert.Equal(t, "docs://a/b", p.Dir().String())
	assert.False(t, p.IsRoot())

	root, err := ParseRemotePath("docs://")
	require.NoError(t, err)
	assert.True(t, root.IsRoot())
	assert.Equal(t, "docs://", root.String())
	assert.Equal(t, "", root.Base())
	assert.Equal(t, "docs://", root.Dir().String(), "root's parent stays the root")
	assert.Equal(t, "docs://x", root.Join("x").String())

	assert.Equal(t, "docs://a/b/c.txt/d", p.Join("d").String())
}
