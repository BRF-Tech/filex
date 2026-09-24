package handlers_test

// The upload precondition (`expect`) the desktop sync engine sends so that a
// save made in the browser a moment before its own upload is REFUSED rather
// than silently replaced. See upload_expect.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// uploadWithExpect sends one file with an optional expect field and returns
// the recorder.
func uploadWithExpect(t *testing.T, mh *handlers.Manager, name, content, expect string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("path", "main://"))
	if expect != "" {
		require.NoError(t, mw.WriteField("expect", expect))
	}
	part, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, err = io.WriteString(part, content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", "/api/files/manager?action=upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mh.Mutate(rec, req)
	return rec
}

// listedSig is the (size, last_modified) the upload response's listing
// reports for name — exactly what the engine records and later sends back.
func listedSig(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	var resp struct {
		Files []struct {
			Basename     string `json:"basename"`
			Size         int64  `json:"size"`
			LastModified int64  `json:"last_modified"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	for _, f := range resp.Files {
		if f.Basename == name {
			return fmt.Sprintf("%d:%d", f.Size, f.LastModified)
		}
	}
	t.Fatalf("%s not in the upload response listing: %s", name, rec.Body.String())
	return ""
}

func TestUploadExpect_MatchingSignatureWrites(t *testing.T) {
	mh, _, _, _, root := newMutateFixture(t)
	first := uploadWithExpect(t, mh, "doc.txt", "v1", "none")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := uploadWithExpect(t, mh, "doc.txt", "v2 from the desktop", listedSig(t, first, "doc.txt"))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	got, _ := os.ReadFile(filepath.Join(root, "doc.txt"))
	require.Equal(t, "v2 from the desktop", string(got))
}

// The race this exists for: the engine planned from signature S, somebody saved
// in the browser (the file is now S'), and the engine's upload arrives. It must
// be refused, and the browser's bytes must still be the file.
func TestUploadExpect_StaleSignatureIsRefusedAndWritesNothing(t *testing.T) {
	mh, _, _, _, root := newMutateFixture(t)
	first := uploadWithExpect(t, mh, "doc.txt", "v1", "")
	require.Equal(t, http.StatusOK, first.Code)
	planned := listedSig(t, first, "doc.txt")

	web := uploadWithExpect(t, mh, "doc.txt", "the browser's save, longer", "")
	require.Equal(t, http.StatusOK, web.Code)

	late := uploadWithExpect(t, mh, "doc.txt", "desktop", planned)
	require.Equal(t, http.StatusPreconditionFailed, late.Code, late.Body.String())
	require.Contains(t, late.Body.String(), "PRECONDITION_FAILED")
	got, _ := os.ReadFile(filepath.Join(root, "doc.txt"))
	require.Equal(t, "the browser's save, longer", string(got), "a refused upload must write nothing")
}

// `none` = "I am creating this": a file that appeared on the server in the
// meantime must not be replaced by a same-named local file.
func TestUploadExpect_NoneRefusesAnExistingFile(t *testing.T) {
	mh, _, _, _, root := newMutateFixture(t)
	require.Equal(t, http.StatusOK, uploadWithExpect(t, mh, "new.txt", "server first", "").Code)
	rec := uploadWithExpect(t, mh, "new.txt", "local", "none")
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	got, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	require.Equal(t, "server first", string(got))
}

// A malformed precondition fails closed: a client that asked for a check
// must not get an unchecked write because it spelled the check wrong.
func TestUploadExpect_MalformedFailsClosed(t *testing.T) {
	mh, _, _, _, _ := newMutateFixture(t)
	require.Equal(t, http.StatusOK, uploadWithExpect(t, mh, "a.txt", "x", "").Code)
	for _, bad := range []string{"garbage", "1:", ":5", "x:y"} {
		rec := uploadWithExpect(t, mh, "a.txt", "y", bad)
		require.Equal(t, http.StatusPreconditionFailed, rec.Code, "expect=%q", bad)
	}
}

// Without the field, nothing changes — every existing client.
func TestUploadExpect_AbsentIsTheOldBehaviour(t *testing.T) {
	mh, _, _, _, root := newMutateFixture(t)
	require.Equal(t, http.StatusOK, uploadWithExpect(t, mh, "a.txt", "1", "").Code)
	require.Equal(t, http.StatusOK, uploadWithExpect(t, mh, "a.txt", "2", "").Code)
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	require.Equal(t, "2", string(got))
}

// The staged (large-file) path takes the same precondition at COMMIT — the
// moment the file is actually replaced, which for a big upload can be minutes
// after the client last looked. A refused commit costs no bytes: the row stays
// committable.
func TestUploadExpect_StagedCommitHonoursThePrecondition(t *testing.T) {
	f := newStagedFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "big.bin"), []byte("server copy"), 0o644))

	stage := func(content []byte) string {
		code, begun := f.begin(t, map[string]any{
			"path": "main://", "name": "big.bin", "size": len(content), "chunk_size": 4096,
		})
		require.Equal(t, http.StatusOK, code, "%v", begun)
		id, _ := begun["id"].(string)
		code, put := f.putChunk(t, id, 0, int64(len(content)), int64(len(content)), content)
		require.Equal(t, http.StatusOK, code, "%v", put)
		return id
	}
	commitExpect := func(id, expect string) (int, map[string]any) {
		resp, err := f.client.Post(f.srv.URL+"/api/files/upload/"+id+"/commit?expect="+expect, "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// "none" while a catalogued file sits there → refused. (Seed the row the
	// way a listing would have seen it: one upload through the multipart path.)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("path", "main://"))
	part, _ := mw.CreateFormFile("file[]", "big.bin")
	_, _ = io.WriteString(part, "server copy")
	require.NoError(t, mw.Close())
	resp, err := f.client.Post(f.srv.URL+"/api/files/manager?action=upload", mw.FormDataContentType(), &body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	id := stage([]byte("desktop version"))
	code, out := commitExpect(id, "none")
	require.Equal(t, http.StatusPreconditionFailed, code, "%v", out)
	got, _ := os.ReadFile(filepath.Join(f.rootDir, "big.bin"))
	require.Equal(t, "server copy", string(got))

	// …and the very same staged row commits once the precondition is right.
	code, out = commitExpect(id, "")
	require.Equal(t, http.StatusAccepted, code, "a refused commit must stay committable: %v", out)
	require.Equal(t, "ok", f.waitForOp(t, num(out["op_id"])))
}

// The row a write leaves behind must carry the DRIVER's mtime. Anything else
// is rewritten by the next storage scan, which changes last_modified with no
// byte changing — and a sync client then downloads its own upload back (and,
// had the person edited it meanwhile, makes a conflict pair of two identical
// files). Measured before: ...692000 (the row's CreatedAt) → ...692587 (the
// file's mtime) after one scan.
func TestWritesRecordTheDriversMtime(t *testing.T) {
	mh, store, drv, st, root := newMutateFixture(t)
	fileMs := func(name string) int64 {
		info, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
		return info.ModTime().UnixMilli()
	}
	listed := func(rec *httptest.ResponseRecorder, name string) int64 {
		var sz, ms int64
		_, err := fmt.Sscanf(listedSig(t, rec, name), "%d:%d", &sz, &ms)
		require.NoError(t, err)
		return ms
	}

	created := uploadWithExpect(t, mh, "fresh.txt", "one", "")
	require.Equal(t, http.StatusOK, created.Code)
	require.Equal(t, fileMs("fresh.txt"), listed(created, "fresh.txt"), "a NEW file's row must carry the file's own mtime")

	time.Sleep(15 * time.Millisecond)
	replaced := uploadWithExpect(t, mh, "fresh.txt", "two, longer", "")
	require.Equal(t, http.StatusOK, replaced.Code)
	require.Equal(t, fileMs("fresh.txt"), listed(replaced, "fresh.txt"), "a REPLACED file's row must carry the file's own mtime")

	// The text editor's save: create, then save again.
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }
	sth := handlers.NewSaveText(store, resolver)
	for _, content := range []string{"draft", "draft, saved again"} {
		body, _ := json.Marshal(map[string]string{"path": "main://notes.md", "content": content})
		rec := httptest.NewRecorder()
		sth.Save(rec, httptest.NewRequest("POST", "/api/files/save-text", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		n, err := store.GetNodeByPath(context.Background(), st.ID, mutTestPathHash(st.ID, "/notes.md"))
		require.NoError(t, err)
		require.NotNil(t, n.BackendMtime, "save-text must record a backend mtime")
		require.Equal(t, fileMs("notes.md"), n.BackendMtime.UnixMilli(), "after saving %q", content)
	}
}
