package handlers_test

// The explorer's doors with every hostile name (task #238): what the browser
// uploads, lists, downloads, renames and makes a folder with is the name the
// person gave, byte for byte; the names no door may create are refused with
// a 400 that says so (NAME_NOT_ALLOWED), never half-written and never a 500;
// a name longer than the storage holds is a 400 NAME_TOO_LONG that carries no
// server path. The list is internal/testutil/hostilenames.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// hnxDo is one request through the fixture's signed-in client.
func (f *stagedFixture) hnxDo(t *testing.T, method, rawURL string, body io.Reader, contentType string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+rawURL, body)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw, resp.Header
}

// hnxMutate is POST /api/files/manager?action=<action> with a JSON body.
func (f *stagedFixture) hnxMutate(t *testing.T, action string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	code, raw, _ := f.hnxDo(t, http.MethodPost, "/api/files/manager?action="+action, bytes.NewReader(b), "application/json")
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return code, out
}

// hnxIndex is the names the explorer's listing of dir shows.
func (f *stagedFixture) hnxIndex(t *testing.T, dir string) []string {
	t.Helper()
	code, raw, _ := f.hnxDo(t, http.MethodGet, "/api/files/manager?action=index&path="+url.QueryEscape("main://"+dir), nil, "")
	require.Equal(t, http.StatusOK, code, string(raw))
	var out struct {
		Files []struct {
			Basename string `json:"basename"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	names := make([]string, 0, len(out.Files))
	for _, e := range out.Files {
		names = append(names, e.Basename)
	}
	return names
}

// hnxStaged uploads content as dir/name over the staged protocol the explorer
// uses (begin, one part, commit, the transfer), and answers begin's status
// and body when begin refuses.
func (f *stagedFixture) hnxStaged(t *testing.T, dir, name string, content []byte) (int, map[string]any) {
	t.Helper()
	size := int64(len(content))
	code, begun := f.begin(t, map[string]any{"path": "main://" + dir, "name": name, "size": size})
	if code != http.StatusOK {
		return code, begun
	}
	id, _ := begun["id"].(string)
	require.NotEmpty(t, id, "%v", begun)
	code, put := f.putChunk(t, id, 0, size, size, content)
	require.Equal(t, http.StatusOK, code, "%v", put)
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))
	return http.StatusOK, nil
}

// hnxPart is one file of a multipart upload: the part's filename, and the
// name sent beside it in `name[]` ("" sends none).
type hnxPart struct{ filename, name, content string }

// hnxMultipart is POST /api/files/manager?action=upload into dir.
func (f *stagedFixture) hnxMultipart(t *testing.T, dir string, parts []hnxPart) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", "main://"+dir))
	for _, p := range parts {
		w, err := mw.CreateFormFile("file[]", p.filename)
		require.NoError(t, err)
		_, _ = io.WriteString(w, p.content)
		if p.name != "" {
			require.NoError(t, mw.WriteField("name[]", p.name))
		}
	}
	require.NoError(t, mw.Close())
	code, raw, _ := f.hnxDo(t, http.MethodPost, "/api/files/manager?action=upload", &buf, mw.FormDataContentType())
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return code, out
}

func TestHostileNames_ExplorerRoundTrip(t *testing.T) {
	f := newStagedFixture(t)
	ctx := context.Background()
	for _, dir := range []string{"hx", "hxd"} {
		code, out := f.hnxMutate(t, "newfolder", map[string]any{"path": "main://", "name": dir})
		require.Equal(t, http.StatusOK, code, "%v", out)
	}

	for _, n := range hostilenames.ForLocalDisk() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			body := []byte("body of " + n.ID)
			code, out := f.hnxStaged(t, "hx", n.Name, body)
			require.Equal(t, http.StatusOK, code, "staged begin of %q: %v", n.Name, out)

			// On disk and in the catalogue under its own bytes.
			got, err := os.ReadFile(filepath.Join(f.rootDir, "hx", n.Name))
			require.NoError(t, err, "not on disk as %q", n.Name)
			assert.Equal(t, body, got)
			row, err := f.store.GetNodeByPath(ctx, f.storage.ID, pathkey.Hash(f.storage.ID, "/hx/"+n.Name))
			require.NoError(t, err)
			require.NotNil(t, row)
			assert.Equal(t, n.Name, row.Name)

			assert.Contains(t, f.hnxIndex(t, "hx"), n.Name, "the listing does not show %q", n.Name)

			// Downloaded: its own bytes (never a sibling's - a trimmed path
			// served `trailing.txt` for `trailing.txt `), and the name the
			// one header builder writes for it.
			code, raw, hdr := f.hnxDo(t, http.MethodGet,
				"/api/files/manager?action=download&path="+url.QueryEscape("main://hx/"+n.Name), nil, "")
			require.Equal(t, http.StatusOK, code, string(raw))
			assert.Equal(t, body, raw)
			assert.Equal(t, httpx.ContentDisposition("attachment", n.Name), hdr.Get("Content-Disposition"))

			// Renamed away and back. The rename box trims the name it is
			// given (product rule): a name with a space at either end comes
			// back without it.
			tmp := "r-" + n.ID + ".tmp"
			code, out = f.hnxMutate(t, "rename", map[string]any{"path": "main://hx", "item": "main://hx/" + n.Name, "name": tmp})
			require.Equal(t, http.StatusOK, code, "%v", out)
			code, out = f.hnxMutate(t, "rename", map[string]any{"path": "main://hx", "item": "main://hx/" + tmp, "name": n.Name})
			require.Equal(t, http.StatusOK, code, "%v", out)
			back, err := os.ReadFile(filepath.Join(f.rootDir, "hx", strings.TrimSpace(n.Name)))
			require.NoError(t, err, "renamed back to %q", n.Name)
			assert.Equal(t, body, back)

			// A folder with the name (trimmed the same way).
			code, out = f.hnxMutate(t, "newfolder", map[string]any{"path": "main://hxd", "name": n.Name})
			require.Equal(t, http.StatusOK, code, "%v", out)
			st, err := os.Stat(filepath.Join(f.rootDir, "hxd", strings.TrimSpace(n.Name)))
			require.NoError(t, err)
			assert.True(t, st.IsDir())
		})
	}
}

// The deep path: 64 folders made one by one through the explorer, a file at
// the bottom, listed and downloaded by its whole path.
func TestHostileNames_ExplorerDeepPath(t *testing.T) {
	f := newStagedFixture(t)
	d := hostilenames.Deep()
	parent := ""
	for _, dir := range d.Dirs() {
		name := dir[strings.LastIndex(dir, "/")+1:]
		code, out := f.hnxMutate(t, "newfolder", map[string]any{"path": "main://" + parent, "name": name})
		require.Equal(t, http.StatusOK, code, "%s: %v", dir, out)
		parent = dir
	}
	code, out := f.hnxStaged(t, parent, d.Leaf, []byte("deep"))
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Contains(t, f.hnxIndex(t, parent), d.Leaf)
	code, raw, _ := f.hnxDo(t, http.MethodGet, "/api/files/manager?action=download&path="+url.QueryEscape("main://"+d.Path), nil, "")
	require.Equal(t, http.StatusOK, code, string(raw))
	assert.Equal(t, "deep", string(raw))
}

// No door makes a name the rule refuses (leafname): every one answers 400,
// NAME_NOT_ALLOWED where the door speaks codes, and nothing reaches the disk.
// Before 0.57 the new-folder box let `..`, `...` and NUL through (200 and a
// catalogue row named `..`, or a 500), and `...` uploaded fine and then
// answered "bad path" to every other request.
func TestHostileNames_RefusedByEveryExplorerDoor(t *testing.T) {
	f := newStagedFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "victim.txt"), []byte("v"), 0o644))
	code, out := f.hnxMutate(t, "newfolder", map[string]any{"path": "main://", "name": "rx"})
	require.Equal(t, http.StatusOK, code, "%v", out)
	before, err := os.ReadDir(filepath.Join(f.rootDir, "rx"))
	require.NoError(t, err)

	for _, n := range hostilenames.Refused() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			code, out := f.begin(t, map[string]any{"path": "main://rx", "name": n.Name, "size": 1})
			assert.Equal(t, http.StatusBadRequest, code, "staged begin: %v", out)
			assert.Equal(t, "NAME_NOT_ALLOWED", out["code"], "staged begin: %v", out)

			code, out = f.hnxMultipart(t, "rx", []hnxPart{{filename: "x.bin", name: n.Name, content: "x"}})
			assert.Equal(t, http.StatusBadRequest, code, "upload: %v", out)
			assert.Equal(t, "NAME_NOT_ALLOWED", out["code"], "upload: %v", out)

			code, out = f.hnxMutate(t, "newfolder", map[string]any{"path": "main://rx", "name": n.Name})
			assert.Equal(t, http.StatusBadRequest, code, "newfolder: %v", out)
			assert.Equal(t, "NAME_NOT_ALLOWED", out["code"], "newfolder: %v", out)

			code, out = f.hnxMutate(t, "rename", map[string]any{"path": "main://", "item": "main://victim.txt", "name": n.Name})
			assert.Equal(t, http.StatusBadRequest, code, "rename: %v", out)

			code, out = f.hnxMutate(t, "newfile", map[string]any{"path": "main://rx", "name": n.Name, "type": "txt", "exact_name": true})
			assert.Equal(t, http.StatusBadRequest, code, "newfile: %v", out)

			b, _ := json.Marshal(map[string]any{"path": "main://rx/" + n.Name, "content": "x"})
			code, raw, _ := f.hnxDo(t, http.MethodPost, "/api/files/save-text", bytes.NewReader(b), "application/json")
			assert.Equal(t, http.StatusBadRequest, code, "save-text: %s", raw)
		})
	}

	after, err := os.ReadDir(filepath.Join(f.rootDir, "rx"))
	require.NoError(t, err)
	assert.Equal(t, len(before), len(after), "a refused name reached the disk")
	v, err := os.ReadFile(filepath.Join(f.rootDir, "victim.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v", string(v))
}

// A batch with one name the rule refuses is refused WHOLE, before a byte is
// written. The check used to sit inside the write loop: `a.txt` landed and
// the batch then answered 400.
func TestHostileNames_UploadBatchIsRefusedBeforeAnyWrite(t *testing.T) {
	f := newStagedFixture(t)
	code, out := f.hnxMultipart(t, "", []hnxPart{
		{filename: "first.txt", content: "first"},
		{filename: hostilenames.Get("dot-dot").Name, content: "never"},
	})
	require.Equal(t, http.StatusBadRequest, code, "%v", out)
	_, err := os.Stat(filepath.Join(f.rootDir, "first.txt"))
	assert.True(t, os.IsNotExist(err), "the batch was half-written: first.txt is on disk")
}

// `name[]` carries the name the browser had: a multipart filename arrives
// with `"` as %22 (WHATWG form-data), and `say "hi".txt` was stored as
// `say %22hi%22.txt`. A count that does not match the files is ignored and the
// filenames are read as before.
func TestHostileNames_UploadNameFieldIsTheName(t *testing.T) {
	f := newStagedFixture(t)
	quoted := hostilenames.Get("double-quote").Name
	code, out := f.hnxMultipart(t, "", []hnxPart{{filename: `say %22hi%22.txt`, name: quoted, content: "q"}})
	require.Equal(t, http.StatusOK, code, "%v", out)
	got, err := os.ReadFile(filepath.Join(f.rootDir, quoted))
	require.NoError(t, err, "not stored as %q", quoted)
	assert.Equal(t, "q", string(got))
	_, err = os.Stat(filepath.Join(f.rootDir, `say %22hi%22.txt`))
	assert.True(t, os.IsNotExist(err), "stored under the escaped filename")

	// Without name[] the filename is the name, as it always was.
	code, out = f.hnxMultipart(t, "", []hnxPart{{filename: "plain name.txt", content: "p"}})
	require.Equal(t, http.StatusOK, code, "%v", out)
	_, err = os.Stat(filepath.Join(f.rootDir, "plain name.txt"))
	assert.NoError(t, err)
}

// A name longer than the local disk holds (over 255 bytes) is the client's to
// shorten: 400 NAME_TOO_LONG at every door, never a 500, never the server's
// disk path in the answer. Staged begin refuses it BEFORE the bytes travel.
func TestHostileNames_TooLongIsNameTooLong(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this file system holds every name of the list")
	}
	f := newStagedFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "short.txt"), []byte("s"), 0o644))
	for _, n := range long {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			check := func(door string, code int, out map[string]any) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, code, "%s: %v", door, out)
				assert.Equal(t, "NAME_TOO_LONG", out["code"], "%s: %v", door, out)
				said, _ := json.Marshal(out)
				assert.NotContains(t, string(said), f.rootDir, "%s: the answer names the server's disk path", door)
			}
			code, out := f.begin(t, map[string]any{"path": "main://", "name": n.Name, "size": 1})
			check("staged begin", code, out)
			code, out = f.hnxMultipart(t, "", []hnxPart{{filename: "x.bin", name: n.Name, content: "x"}})
			check("upload", code, out)
			code, out = f.hnxMutate(t, "newfolder", map[string]any{"path": "main://", "name": n.Name})
			check("newfolder", code, out)
			code, out = f.hnxMutate(t, "rename", map[string]any{"path": "main://", "item": "main://short.txt", "name": n.Name})
			check("rename", code, out)
		})
	}
	s, err := os.ReadFile(filepath.Join(f.rootDir, "short.txt"))
	require.NoError(t, err)
	assert.Equal(t, "s", string(s))
}

// A name with a space at either end is a name of its own: downloading
// ` notes.txt` must not serve `notes.txt`, nor `report.txt ` serve
// `report.txt` (streamBody trimmed the path before 0.57).
func TestHostileNames_SpacedNamesAreNotTheirTwins(t *testing.T) {
	f := newStagedFixture(t)
	for name, body := range map[string]string{
		" notes.txt": "spaced", "notes.txt": "plain",
		"report.txt ": "spaced", "report.txt": "plain",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, name), []byte(body), 0o644))
	}
	for name, want := range map[string]string{
		" notes.txt": "spaced", "notes.txt": "plain",
		"report.txt ": "spaced", "report.txt": "plain",
	} {
		for _, action := range []string{"download", "preview"} {
			code, raw, _ := f.hnxDo(t, http.MethodGet,
				"/api/files/manager?action="+action+"&path="+url.QueryEscape("main://"+name), nil, "")
			require.Equal(t, http.StatusOK, code, "%s %q: %s", action, name, raw)
			assert.Equal(t, want, string(raw), "%s %q served another file", action, name)
		}
	}
}

// A confined agent token addresses a bare path relative to its root: a space
// at either end stays part of the name, never the twin without it.
func TestHostileNames_ConfinedAgentKeepsSpaces(t *testing.T) {
	srv, client, store, _ := aiFixture(t)
	u, err := store.GetUserByEmail(context.Background(), "admin2@test.local")
	require.NoError(t, err)
	tok := issueToken(t, store, u.ID, "read,write,delete,mcp,root:main://tenant", nil)

	files := map[string]string{
		hostilenames.Get("trailing-space").Name:                    "trailing",
		strings.TrimSpace(hostilenames.Get("trailing-space").Name): "plain trailing",
		hostilenames.Get("leading-space").Name:                     "leading",
		strings.TrimSpace(hostilenames.Get("leading-space").Name):  "plain leading",
	}
	for name, body := range files {
		resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{"path": name, "content": body})
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "%q: %s", name, raw)
	}
	for name, want := range files {
		resp := aiReq(t, client, http.MethodGet, srv.URL+"/api/ai/download?path="+url.QueryEscape(name), tok, nil)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "%q: %s", name, raw)
		assert.Equal(t, want, string(raw), "%q read its twin", name)
	}
}

// A file that ALREADY has a name with a control character (made on the disk,
// over another protocol before 0.57) stays usable through the explorer: it
// is listed, downloaded, overwritten under its own name, renamed away and
// deleted. Only a NEW such name is refused (the owner's rule for 0.57).
func TestHostileNames_ControlNamedFileStaysUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses control characters in a file name")
	}
	f := newStagedFixture(t)
	code, out := f.hnxMutate(t, "newfolder", map[string]any{"path": "main://", "name": "cn"})
	require.Equal(t, http.StatusOK, code, "%v", out)
	tab := hostilenames.Get("control-tab").Name
	newline := hostilenames.Get("control-newline").Name
	for _, name := range []string{tab, newline} {
		require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "cn", name), []byte("old"), 0o644))
	}

	listed := f.hnxIndex(t, "cn")
	for _, name := range []string{tab, newline} {
		assert.Contains(t, listed, name)
		code, raw, _ := f.hnxDo(t, http.MethodGet, "/api/files/manager?action=download&path="+url.QueryEscape("main://cn/"+name), nil, "")
		require.Equal(t, http.StatusOK, code, string(raw))
		assert.Equal(t, "old", string(raw))
	}

	// Overwritten under its own name: the browser upload and the staged one.
	code, out = f.hnxMultipart(t, "cn", []hnxPart{{filename: "x.bin", name: tab, content: "new tab"}})
	require.Equal(t, http.StatusOK, code, "%v", out)
	got, err := os.ReadFile(filepath.Join(f.rootDir, "cn", tab))
	require.NoError(t, err)
	assert.Equal(t, "new tab", string(got))
	code, out = f.hnxStaged(t, "cn", newline, []byte("new newline"))
	require.Equal(t, http.StatusOK, code, "%v", out)
	got, err = os.ReadFile(filepath.Join(f.rootDir, "cn", newline))
	require.NoError(t, err)
	assert.Equal(t, "new newline", string(got))

	// A NEW such name is still refused.
	code, out = f.hnxMultipart(t, "cn", []hnxPart{{filename: "x.bin", name: "new" + tab, content: "x"}})
	assert.Equal(t, http.StatusBadRequest, code, "%v", out)

	// Renamed away, then deleted.
	code, out = f.hnxMutate(t, "rename", map[string]any{"path": "main://cn", "item": "main://cn/" + tab, "name": "tab-name.txt"})
	require.Equal(t, http.StatusOK, code, "%v", out)
	_, err = os.Stat(filepath.Join(f.rootDir, "cn", "tab-name.txt"))
	assert.NoError(t, err)
	code, out = f.hnxMutate(t, "delete", map[string]any{"path": "main://cn", "items": []map[string]string{{"path": "main://cn/" + newline}}})
	require.Equal(t, http.StatusOK, code, "%v", out)
	_, err = os.Stat(filepath.Join(f.rootDir, "cn", newline))
	assert.True(t, os.IsNotExist(err), "the control-named file was not deleted")
}
