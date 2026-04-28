package sync

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEtagDrift covers the core comparison cases.
func TestEtagDrift(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", false},
		{`"abc"`, "abc", false},
		{"abc-3", "abc-3", false},
		{"abc-3", "abc-2", true},
		{"", "", false},
		{"x", "", true},
	}
	for _, c := range cases {
		got := etagDrift(c.a, c.b)
		assert.Equal(t, c.want, got, "etagDrift(%q,%q)", c.a, c.b)
	}
}

func TestCountParts(t *testing.T) {
	assert.Equal(t, 3, CountParts(`"abc-3"`))
	assert.Equal(t, 1, CountParts("abc"))
	assert.Equal(t, 1, CountParts(""))
	assert.Equal(t, 7, CountParts(`"deadbeef-7"`))
	// Trailing garbage in the count → fallback to 1.
	assert.Equal(t, 1, CountParts(`"abc-not-a-number"`))
}

// TestMultipartETag_Empty produces the well-known md5 of empty input.
func TestMultipartETag_Empty(t *testing.T) {
	tag, err := MultipartETag(strings.NewReader(""), 8*1024*1024)
	require.NoError(t, err)
	assert.Equal(t, `"d41d8cd98f00b204e9800998ecf8427e"`, tag)
}

// TestMultipartETag_SinglePart for an input shorter than the part size,
// no `-N` suffix should be appended (matches AWS behaviour).
func TestMultipartETag_SinglePart(t *testing.T) {
	tag, err := MultipartETag(strings.NewReader("hello"), 8*1024*1024)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(tag, `"`), "wrapped in quotes: %q", tag)
	require.NotContains(t, tag, "-")
}

// TestMultipartETag_MultiPart forces the multi-part path with a tiny part
// size and verifies the `-N` suffix matches the part count.
func TestMultipartETag_MultiPart(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 1024) // 1 KB
	partSize := int64(256)                  // 4 parts expected
	tag, err := MultipartETag(bytes.NewReader(body), partSize)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(tag, `-4"`), "expected -4 suffix, got %s", tag)
	assert.Equal(t, 4, CountParts(tag))
}

// TestMultipartETag_DefaultPartSize when partSize <= 0, the function
// falls back to 8 MiB.
func TestMultipartETag_DefaultPartSize(t *testing.T) {
	tag, err := MultipartETag(strings.NewReader("abc"), 0)
	require.NoError(t, err)
	assert.NotEmpty(t, tag)
}

// TestEtagDrift_QuotedVsUnquoted shouldn't depend on surrounding quotes —
// AWS sometimes returns quoted, our cache stores unquoted.
func TestEtagDrift_QuotedAndUnquoted(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`"abc"`, `abc`, false},
		{`abc`, `"abc"`, false},
		{`"abc-2"`, `abc-2`, false},
	}
	for _, c := range cases {
		got := etagDrift(c.a, c.b)
		assert.Equal(t, c.want, got, "etagDrift(%q,%q)", c.a, c.b)
	}
}
