package handlers_test

// A `root:` token stays in its folder on the archive routes (2026-10-01).
//
// confine.Middleware rewrote `path` on them and nothing else, and the
// handlers did not ask the root: a token confined to main://kutu packed
// main://disari/gizli.txt into an archive inside its folder (`sources` of
// /archive/create, `files[].source` of /archive/add) and extracted into
// main://disari (`dest` of /archive/extract). And a key in another case
// (`{"PATH": …}`) went past the middleware altogether, while encoding/json
// handed it to the handler's `path` field: save-text wrote outside the folder.
// Each test is red on the code before the fix: the request is accepted and the
// bytes cross the root.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// filesCall sends one JSON request to the explorer's /api/files routes with a
// token.
func filesCall(t *testing.T, base, tok, method, p string, body any) (int, string) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(method, base+p, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// confinedFix is the door fixture with a member's file inside main://kutu and
// another outside it, and that member's token confined to main://kutu.
func confinedFix(t *testing.T) (*doorFix, string) {
	t.Helper()
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://kutu/ic.txt", "icerde")
	f.put(t, f.Tok, "main://disari/gizli.txt", "gizli")
	return f, testutil.NewAPIToken(t, f.Store, f.MemberID, "read,write,delete,root:main://kutu")
}

// settle gives a job that was (wrongly) queued time to run, so "nothing
// crossed" is measured after it, not before.
func settle() { time.Sleep(400 * time.Millisecond) }

func TestConfineArchive_CreateCannotPackFromOutsideTheRoot(t *testing.T) {
	f, tok := confinedFix(t)
	for _, body := range []map[string]any{
		{"sources": []string{"main://disari/gizli.txt"}, "dest": "main://kutu/paket.zip"},
		{"sources": []string{"disari/gizli.txt"}, "dest": "main://kutu/paket2.zip"},
		{"Sources": []string{"main://disari/gizli.txt"}, "dest": "main://kutu/paket3.zip"},
	} {
		code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/archive/create", body)
		assert.Equal(t, http.StatusForbidden, code, "%v: %s", body, raw)
	}
	// …nor into an archive outside it.
	code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/archive/create",
		map[string]any{"sources": []string{"main://kutu/ic.txt"}, "dest": "main://disari/disari.zip"})
	assert.Equal(t, http.StatusForbidden, code, raw)
	settle()
	for _, name := range []string{"kutu/paket.zip", "kutu/paket2.zip", "kutu/paket3.zip", "disari/disari.zip"} {
		_, err := os.Stat(filepath.Join(f.RootMain, filepath.FromSlash(name)))
		assert.True(t, os.IsNotExist(err), "%s must not have been written", name)
	}

	// Inside the root it still works.
	code, raw = filesCall(t, f.URL, tok, http.MethodPost, "/api/files/archive/create",
		map[string]any{"sources": []string{"main://kutu/ic.txt"}, "dest": "main://kutu/icerde.zip"})
	assert.Equal(t, http.StatusAccepted, code, raw)
}

func TestConfineArchive_ExtractCannotWriteOutsideTheRoot(t *testing.T) {
	f, tok := confinedFix(t)
	zipBytes := buildZip(t, map[string]string{"cikan.txt": "zip icinden"})
	restCall(t, f.URL, f.Tok, "/api/ai/upload", map[string]any{"path": "main://kutu/a.zip", "content_base64": base64.StdEncoding.EncodeToString(zipBytes)})

	for _, body := range []map[string]any{
		{"path": "main://kutu/a.zip", "dest": "main://disari/acilan"},
		{"path": "main://kutu/a.zip", "Dest": "main://disari/acilan2"},
		{"path": "main://kutu/a.zip", "dest": "disari/acilan3"},
	} {
		code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/archive/extract", body)
		assert.Equal(t, http.StatusForbidden, code, "%v: %s", body, raw)
	}
	settle()
	for _, dir := range []string{"disari/acilan", "disari/acilan2", "disari/acilan3"} {
		_, err := os.Stat(filepath.Join(f.RootMain, filepath.FromSlash(dir), "cikan.txt"))
		assert.True(t, os.IsNotExist(err), "nothing may be extracted into %s", dir)
	}
}

func TestConfineArchive_AddCannotTakeASourceFromOutside(t *testing.T) {
	f, tok := confinedFix(t)
	for _, body := range []map[string]any{
		{"path": "main://kutu/b.zip", "files": []map[string]string{{"name": "g.txt", "source": "disari/gizli.txt"}}},
		{"path": "main://kutu/c.zip", "files": []map[string]string{{"name": "g.txt", "source": "main://disari/gizli.txt"}}},
		{"path": "main://kutu/d.zip", "Files": []map[string]string{{"name": "g.txt", "Source": "disari/gizli.txt"}}},
	} {
		code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/archive/add", body)
		assert.Equal(t, http.StatusForbidden, code, "%v: %s", body, raw)
	}
	for _, name := range []string{"kutu/b.zip", "kutu/c.zip", "kutu/d.zip"} {
		_, err := os.Stat(filepath.Join(f.RootMain, filepath.FromSlash(name)))
		assert.True(t, os.IsNotExist(err), "%s must not hold a file from outside the root", name)
	}
}

func TestConfine_AKeyInAnotherCaseIsConfinedToo(t *testing.T) {
	f, tok := confinedFix(t)
	for _, body := range []map[string]any{
		{"PATH": "main://disari/gizli.txt", "content": "disariya yazildi"},
		{"Path": "main://disari/gizli.txt", "content": "disariya yazildi"},
	} {
		code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/save-text", body)
		assert.Equal(t, http.StatusForbidden, code, "%v: %s", body, raw)
	}
	got, ok := f.read(t, f.RootMain, "disari/gizli.txt")
	require.True(t, ok)
	assert.Equal(t, "gizli", got, "the file outside the root must not have been overwritten")
	// The lower-case key inside the root is unchanged.
	code, raw := filesCall(t, f.URL, tok, http.MethodPost, "/api/files/save-text", map[string]any{"path": "main://kutu/ic.txt", "content": "yeni icerik"})
	assert.Equal(t, http.StatusOK, code, raw)
}
