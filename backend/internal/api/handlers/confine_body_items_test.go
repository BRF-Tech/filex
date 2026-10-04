package handlers_test

// GHSA-8gvc-6w52-6c7j, the explorer verbs past their base dir (2026-10-04).
//
// confine_body_shapes_test.go holds the BASE DIR of every manager mutation to
// a `root:` token's folder however the body is shaped. A move, a delete and a
// rename also name the ITEMS they act on (`items[].path`, `item`), a rename
// names a new path, and the listing names a folder by `?path=`. Those are
// confined by confine.Middleware only when it reads the request: a body
// labelled JSON, a `?path=` that is there. Each test sends one of them in a
// shape the middleware does not read and is red while something outside the
// root changes (or is listed); each also shows the same shape still works
// inside the root.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The item of a move, a delete or a rename, outside the root, behind a base
// dir that is inside it - in a body the middleware does not read.
func TestConfineItems_AnItemOutsideTheRootIsNotTouched(t *testing.T) {
	f, tok := confinedFix(t)

	for _, ct := range []string{"text/plain", ""} {
		for _, s := range []struct{ label, action, body string }{
			{"move it in", "move", `{"path":"main://kutu","items":[{"path":"main://disari/gizli.txt"}]}`},
			{"move it in, bare item", "move", `{"path":"main://kutu","items":[{"path":"disari/gizli.txt"}]}`},
			{"delete it", "delete", `{"path":"main://kutu","items":[{"path":"main://disari/gizli.txt"}]}`},
			{"rename it", "rename", `{"path":"main://kutu","item":"main://disari/gizli.txt","name":"ad.txt"}`},
		} {
			code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action="+s.action, ct, []byte(s.body))
			assert.GreaterOrEqual(t, code, 400, "%s (%q): %s", s.label, ct, raw)
			assert.True(t, confExists(f.RootMain, "disari/gizli.txt"), "%s (%q): the file outside the root must stay where it is", s.label, ct)
			assert.False(t, confExists(f.RootMain, "kutu/gizli.txt"), "%s (%q): the file outside the root must not be brought in", s.label, ct)
			assert.False(t, confExists(f.RootMain, "disari/ad.txt"), "%s (%q): the file outside the root must not be renamed", s.label, ct)
		}
	}

	// The same shape inside the root still works.
	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=rename", "text/plain",
		[]byte(`{"path":"main://kutu","item":"main://kutu/ic.txt","name":"ic2.txt"}`))
	assert.Equal(t, http.StatusOK, code, raw)
	assert.True(t, confExists(f.RootMain, "kutu/ic2.txt"), "a rename inside the root must still work")
}

// A rename keeps the item's folder and changes its name - but the item may be
// the root folder itself, and then its new name is a sibling of the root,
// outside it. That is refused in every body shape, JSON included.
func TestConfineItems_TheRootFolderIsNotRenamedOutOfItself(t *testing.T) {
	f, tok := confinedFix(t)

	for _, ct := range []string{"application/json", "text/plain"} {
		code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=rename", ct,
			[]byte(`{"path":"main://kutu","item":"main://kutu","name":"kutu2"}`))
		assert.GreaterOrEqual(t, code, 400, "%q: %s", ct, raw)
		assert.False(t, confExists(f.RootMain, "kutu2"), "%q: nothing may be named outside the root", ct)
		assert.True(t, confExists(f.RootMain, "kutu/ic.txt"), "%q: the root folder stays where it is", ct)
	}
}

// A listing that names no `?path=` is the token's folder, not the top of the
// first storage (where the other folders' names are).
func TestConfineItems_AListingWithNoPathIsTheRoot(t *testing.T) {
	f, tok := confinedFix(t)
	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", "application/json",
		[]byte(`{"path":"main://kutu","name":"altklasor"}`))
	require.Equal(t, http.StatusOK, code, raw)

	code, raw = confRaw(t, f.URL, tok, http.MethodGet, "/api/files/manager?action=index", "", nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotContains(t, raw, "disari", "index: a folder outside the root must not be listed")
	assert.Contains(t, raw, `"dirname":"main://kutu"`, "index: the listing is the root folder")
	assert.Contains(t, raw, "ic.txt", "index: the root folder's own file is listed")

	code, raw = confRaw(t, f.URL, tok, http.MethodGet, "/api/files/manager?action=subfolders", "", nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotContains(t, raw, "disari", "subfolders: a folder outside the root must not be listed")
	assert.Contains(t, raw, "altklasor", "subfolders: the root folder's own folder is listed")
}

// `allowed` changes nothing, but it answers which permissions the caller holds
// at a path; outside the root it holds none, however the question is sent.
func TestConfineItems_AllowedHoldsNothingOutsideTheRoot(t *testing.T) {
	f, tok := confinedFix(t)

	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=allowed", "text/plain",
		[]byte(`{"items":[{"path":"main://disari/gizli.txt"},{"path":"main://kutu/ic.txt"}],"permissions":["files.delete"]}`))
	require.Equal(t, http.StatusOK, code, raw)
	var out struct {
		Allowed [][]string `json:"allowed"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &out), raw)
	require.Len(t, out.Allowed, 2, raw)
	assert.Empty(t, out.Allowed[0], "nothing is held outside the root")
	assert.Equal(t, []string{"files.delete"}, out.Allowed[1], "inside the root the member's permission is held")
}
