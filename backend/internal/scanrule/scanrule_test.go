package scanrule_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/scanrule"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func mustRule(t *testing.T, patterns string) *scanrule.Rule {
	t.Helper()
	r, err := scanrule.Parse(patterns)
	require.NoError(t, err)
	return r
}

// The tree and the two patterns from issue #44, verbatim: the scan must never
// descend into `.snapshots` or `downloads/incomplete`, and everything else is
// walked as before.
func TestExcluded_TheIssuesExample(t *testing.T) {
	r := mustRule(t, "**/.*\ndownloads/incomplete/**")
	for _, p := range []string{
		"/.snapshots", "/.snapshots/daily/0001/film.mkv",
		"/downloads/incomplete", "/downloads/incomplete/part.001",
		"/Movies/.DS_Store", "/Music/album/.cache/x",
	} {
		assert.True(t, r.Excluded(p), "%s must be excluded", p)
		assert.True(t, r.Skips(p), "%s must be skipped", p)
	}
	for _, p := range []string{
		"/Movies", "/Movies/film.mkv", "/Music", "/downloads", "/downloads/complete",
		"/downloads/complete/film.mkv", "/downloads/incomplete-notes.txt",
	} {
		assert.False(t, r.Excluded(p), "%s must be walked", p)
	}
}

// A pattern without a "/" names an entry at ANY depth, the way .gitignore
// reads it — `@eaDir`, `.git`, `*.tmp` are written once, not once per level.
// One with a "/" (or a leading one) is anchored at the storage root.
func TestExcluded_FloatingAndAnchored(t *testing.T) {
	r := mustRule(t, ".git\n*.tmp\n/build\ndocs/*.bak\n@eaDir/")
	cases := map[string]bool{
		"/.git":                       true,
		"/src/app/.git/objects/aa/bb": true,
		"/a.tmp":                      true,
		"/deep/down/b.tmp":            true,
		"/deep/down/b.tmp.txt":        false,
		"/build":                      true,
		"/build/out.o":                true,
		"/src/build":                  false, // anchored: only the root's build
		"/docs/x.bak":                 true,
		"/docs/sub/x.bak":             false, // * stays inside one name
		"/other/docs/x.bak":           false,
		"/photos/@eaDir/thumb.jpg":    true, // a trailing / is ignored
		"/.gitignore":                 false,
	}
	for p, want := range cases {
		assert.Equal(t, want, r.Excluded(p), "Excluded(%q)", p)
	}
}

func TestExcluded_DoubleStarInTheMiddle(t *testing.T) {
	r := mustRule(t, "projects/**/node_modules")
	assert.True(t, r.Excluded("/projects/node_modules"), "** matches zero folders")
	assert.True(t, r.Excluded("/projects/a/b/node_modules/x/index.js"))
	assert.False(t, r.Excluded("/other/node_modules"))
	assert.False(t, r.Excluded("/projects/a/node_modules_backup"))
}

// ⚠⚠ `.*` is the pattern people write first. It must not take filex's own
// machinery out of the catalogue: the desktop's "open with" working copies
// (`.filex-open`) and the empty-folder marker are catalogued as before, and an
// encrypted folder's marker keeps its row — the lock screen reads it.
func TestExcluded_NeverFilexsOwnNames(t *testing.T) {
	r := mustRule(t, ".*\n*.json")
	for _, p := range []string{
		"/.filex-open", "/.filex-open/a1b2c3d4e5f6-rapor.docx",
		"/.keepdir", "/docs/.keepdir",
		"/kasa/.filex-e2e.json",
	} {
		assert.False(t, r.Excluded(p), "%s is filex's own and outside the operator's patterns", p)
	}
	// The sealed trees stay skipped whatever the patterns say.
	for _, p := range []string{"/.filex-trash", "/.filex-trash/x", "/.versions/12/1", "/.thumbs/a.jpg"} {
		assert.True(t, r.Skips(p), "%s is never walked", p)
	}
	// …and a marker inside an excluded FOLDER goes with the folder.
	r2 := mustRule(t, "arsiv/**")
	assert.True(t, r2.Excluded("/arsiv/.filex-e2e.json"))
}

func TestNilAndEmptyRuleSkipOnlyFilexsTrees(t *testing.T) {
	for _, r := range []*scanrule.Rule{nil, {}, mustRule(t, "  \n# only a comment\n\n")} {
		assert.True(t, r.Empty())
		assert.False(t, r.Excluded("/.git/config"))
		assert.False(t, r.Skips("/.git/config"))
		assert.True(t, r.Skips("/.versions/1/2"))
		assert.True(t, r.Skips("/.filex-trash"))
	}
}

func TestParse_ShapesItAccepts(t *testing.T) {
	r := mustRule(t, "  .git  \r\n# a comment\r\n\r\n./cache/\n")
	assert.Equal(t, []string{".git", "./cache/"}, r.Patterns())
	assert.True(t, r.Excluded("/cache/x"), "./ and a trailing / are cleaned")
	assert.False(t, r.Excluded("/a/cache/x"), "./cache is anchored")

	r, err := scanrule.Parse([]any{".git", "*.tmp"})
	require.NoError(t, err)
	assert.Equal(t, []string{".git", "*.tmp"}, r.Patterns())

	r, err = scanrule.FromConfig(map[string]any{"root": "/data"})
	require.NoError(t, err)
	assert.True(t, r.Empty())
	r, err = scanrule.FromConfig(map[string]any{storage.ScanExcludeKey: ".git"})
	require.NoError(t, err)
	assert.True(t, r.Excluded("/x/.git"))
}

// A pattern that cannot mean what the operator wants is refused on write —
// above all one that excludes the whole storage.
func TestParse_Refuses(t *testing.T) {
	for _, bad := range []any{
		"!keep.txt",
		"a/../b",
		"[unclosed",
		"*", "**", "/*", "**/*", "*/**", "?*", "[!.]*", "[^.]*", "/", "./",
		strings.Repeat("a", scanrule.MaxPatternLength+1),
		strings.Repeat("p\n", scanrule.MaxPatterns+1),
		[]any{".git", 7},
		42,
	} {
		_, err := scanrule.Parse(bad)
		assert.True(t, errors.Is(err, scanrule.ErrInvalid), "Parse(%v) must be refused, got %v", bad, err)
	}
	// Specific patterns that merely start with a wildcard are fine.
	for _, ok := range []string{"*.tmp", ".*", "*~", "**/.*", "*/cache", "f*"} {
		_, err := scanrule.Parse(ok)
		assert.NoError(t, err, "Parse(%q)", ok)
	}
	// `[!…]` negates, as in a shell: `[!.]*.log` is a log not starting with a dot.
	r := mustRule(t, "[!.]*.log")
	assert.True(t, r.Excluded("/logs/app.log"))
	assert.False(t, r.Excluded("/logs/.hidden.log"))
}
