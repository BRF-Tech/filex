package watchproto

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanFolder_CanonicalForms(t *testing.T) {
	for raw, want := range map[string]string{
		"/":              "/",
		"//":             "/",
		"/a":             "/a",
		"/a/":            "/a",
		"/a//b///":       "/a/b",
		"/Fotoğraflar/ş": "/Fotoğraflar/ş",
		"/a b/c.d":       "/a b/c.d",
		"/.git":          "/.git",
		"/a/..b":         "/a/..b",
	} {
		got, err := CleanFolder(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
}

// Anything that could name a place outside the storage, or be read two ways
// on two operating systems, is refused rather than repaired.
func TestCleanFolder_RefusesWhatCouldClimbOrBeReadTwoWays(t *testing.T) {
	for _, raw := range []string{
		"",
		"a/b",
		"../etc",
		"/..",
		"/a/../../etc",
		"/a/./b",
		"/a\\..\\b",
		"/a\x00b",
		"/a\nb",
		"/a\x7fb",
		"/\xff\xfe",
		"/" + strings.Repeat("a", MaxPathBytes),
	} {
		_, err := CleanFolder(raw)
		require.Error(t, err, "%q", raw)
		assert.True(t, errors.Is(err, ErrInvalid), "%q", raw)
	}
}

func TestReportValidate_CleansAndDedupes(t *testing.T) {
	r := Report{Protocol: 1, Seq: 3, Folders: []string{"/a/", "/a", "/b//c"}, Subtrees: []string{"/big/"}}
	require.NoError(t, r.Validate())
	assert.Equal(t, []string{"/a", "/b/c"}, r.Folders)
	assert.Equal(t, []string{"/big"}, r.Subtrees)
}

func TestReportValidate_Refusals(t *testing.T) {
	many := make([]string, MaxFolders+1)
	for i := range many {
		many[i] = "/f"
	}
	for name, r := range map[string]Report{
		"no protocol":        {Seq: 1},
		"a newer protocol":   {Protocol: Protocol + 1},
		"too many folders":   {Protocol: 1, Folders: many},
		"a climbing folder":  {Protocol: 1, Folders: []string{"/a/../.."}},
		"a climbing subtree": {Protocol: 1, Subtrees: []string{"/../x"}},
		"a long agent field": {Protocol: 1, Agent: Agent{Host: strings.Repeat("h", MaxAgentField+1)}},
	} {
		err := r.Validate()
		require.Error(t, err, name)
		assert.True(t, errors.Is(err, ErrInvalid), name)
	}
}

// A heartbeat - nothing changed - is a valid report.
func TestReportValidate_Heartbeat(t *testing.T) {
	r := Report{Protocol: 1, Seq: 9, Agent: Agent{ID: "x", Version: AgentVersion}}
	require.NoError(t, r.Validate())
	assert.Nil(t, r.Folders)
	assert.Nil(t, r.Subtrees)
}

func TestParentAndWithin(t *testing.T) {
	assert.Equal(t, "/", Parent("/"))
	assert.Equal(t, "/", Parent("/a"))
	assert.Equal(t, "/a", Parent("/a/b"))
	assert.True(t, Within("/a/b", "/"))
	assert.True(t, Within("/a/b", "/a"))
	assert.True(t, Within("/a", "/a"))
	assert.False(t, Within("/ab", "/a"), "a sibling with the same prefix is not inside")
	assert.False(t, Within("/a", "/a/b"))
}

func TestEventsPath(t *testing.T) {
	assert.Equal(t, "/api/storages/7/watch/events", EventsPath(7))
}

// The release workflow, the update manifest and the docs' Releases page all
// tell filex-watch's tags from filex's by this prefix.
func TestTagPrefixIsNotAFilexReleaseTag(t *testing.T) {
	tag := TagPrefix + AgentVersion
	assert.False(t, strings.HasPrefix(tag, "v"), "a filex-watch tag must never look like a filex release (v*)")
	assert.Equal(t, "filex-watch-v"+AgentVersion, tag)
}
