package handlers_test

// A refused upload says WHY, in the visitor's language.
//
// ⚠⚠ The bug (QA, 2026-09-21): a file the drop link does not accept was sent
// anyway by the JavaScript page and came back `415 {"error":"ext_not_allowed"}`
// with no words. The page had nothing to say for a code it did not know and
// printed its fallback — "Uygulama hata döndürdü" (The app returned an error),
// the APP-PLUGIN runtime's sentence, on a page with no app on it. The server
// already has every one of these sentences in `server.public.*` (the
// no-JavaScript page's script uses them); every refusal now carries one as
// `message`, next to the code a script branches on.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

func dropUploadIn(t *testing.T, r http.Handler, url, lang string, files []fpart) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		part, err := mw.CreateFormFile("file", f.name)
		require.NoError(t, err)
		_, _ = io.WriteString(part, f.content)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", lang)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestDropUpload_RefusalsAreSaidInWords(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	ds := `{"max_files":2,"max_file_size_mb":1,"allowed_ext":["pdf"],"ask_name":true}`
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds})
	require.NoError(t, err)
	url := "/api/public/d/" + sh.Token + "/upload"

	cases := []struct {
		name   string
		lang   string
		files  []fpart
		status int
		code   string
		says   string
	}{
		{"a type the link does not take, in Turkish", "tr", []fpart{{"notes.txt", "x"}},
			http.StatusUnsupportedMediaType, "ext_not_allowed", "İzin verilmeyen dosya türü."},
		{"the same, in English", "en", []fpart{{"notes.txt", "x"}},
			http.StatusUnsupportedMediaType, "ext_not_allowed", "That file type is not allowed."},
		{"more files than one submission may carry", "en", []fpart{{"a.pdf", "a"}, {"b.pdf", "b"}, {"c.pdf", "c"}},
			http.StatusUnprocessableEntity, "too_many_files", "You can send at most 2 files."},
		{"a file over the size limit", "tr", []fpart{{"big.pdf", string(make([]byte, (1<<20)+1))}},
			http.StatusRequestEntityTooLarge, "file_too_large", "en fazla 1 MB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := dropUploadIn(t, r, url, tc.lang, tc.files)
			require.Equal(t, tc.status, status, body)
			assert.Equal(t, tc.code, body["error"], "the code stays — a script branches on it")
			msg, _ := body["message"].(string)
			assert.Contains(t, msg, tc.says)
		})
	}

	// A link that has run out says so — not "try again".
	gone := `{"max_files":2}`
	zero := 0
	spent, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &gone, MaxUploads: &zero})
	require.NoError(t, err)
	status, body := dropUploadIn(t, r, "/api/public/d/"+spent.Token+"/upload", "en", []fpart{{"a.pdf", "a"}})
	require.Equal(t, http.StatusGone, status, body)
	assert.Equal(t, "This upload link has expired or reached its limit.", body["message"])
}
