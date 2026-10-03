package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// officeDS is a document server for the office engine's tests: it downloads
// the source from the offered address (through the service, as filex's door
// would serve it), then answers with `answer`.
type officeDS struct {
	t      *testing.T
	svc    *onlyoffice.Service
	srv    *httptest.Server
	answer func(payload map[string]any, src []byte) (code int, result []byte)

	mu       sync.Mutex
	payloads []map[string]any
	offered  []url.Values
	result   []byte
}

func newOfficeDS(t *testing.T) *officeDS {
	d := &officeDS{t: t}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ConvertService.ashx":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			d.mu.Lock()
			d.payloads = append(d.payloads, payload)
			d.mu.Unlock()
			u, _ := url.Parse(payload["url"].(string))
			q := u.Query()
			d.mu.Lock()
			d.offered = append(d.offered, q)
			d.mu.Unlock()
			exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
			f, _, err := d.svc.OpenOffered(r.Context(), q.Get("o"), exp, q.Get("p"), q.Get("sig"))
			if err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": -4})
				return
			}
			src, _ := io.ReadAll(f)
			f.Close()
			code, res := d.answer(payload, src)
			if code != 0 {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": code})
				return
			}
			d.mu.Lock()
			d.result = res
			d.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"endConvert": true, "percent": 100, "fileUrl": d.srv.URL + "/cache/result", "fileType": payload["outputtype"]})
		case "/cache/result":
			d.mu.Lock()
			res := d.result
			d.mu.Unlock()
			_, _ = w.Write(res)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.srv.Close)
	d.svc = onlyoffice.New(nil, nil, d.srv.URL, "engine-secret", "https://filex.test", 0)
	d.answer = func(p map[string]any, src []byte) (int, []byte) {
		return 0, append([]byte("made:"+p["outputtype"].(string)+":"), src...)
	}
	return d
}

func engineRequest(t *testing.T, dir, name, content, to string) wasmplugin.OfficeRequest {
	t.Helper()
	src := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(src, []byte(content), 0o600))
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	return wasmplugin.OfficeRequest{Src: src, Name: name, From: ext, To: to,
		Dst: filepath.Join(dir, strings.TrimSuffix(name, "."+ext)+".out"), MaxBytes: 1 << 20}
}

// The office engine on the shared conversion client: the input offered for
// the one conversion and withdrawn after it, a fresh key each run, the CSV
// knobs passed on, the result written where the engine collects it.
func TestOfficeEngine_ConvertsThroughTheDocumentServer(t *testing.T) {
	ds := newOfficeDS(t)
	eng := officeEngine{svc: ds.svc}
	ctx := context.Background()
	require.True(t, eng.Ready(ctx))

	dir := t.TempDir()
	req := engineRequest(t, dir, "in.xlsx", "XLSX", "csv")
	req.Delimiter, req.CodePage = 4, 65001
	require.NoError(t, eng.Convert(ctx, req))
	got, err := os.ReadFile(req.Dst)
	require.NoError(t, err)
	assert.Equal(t, "made:csv:XLSX", string(got))

	require.Len(t, ds.payloads, 1)
	p := ds.payloads[0]
	assert.Equal(t, "xlsx", p["filetype"])
	assert.Equal(t, "csv", p["outputtype"])
	assert.Equal(t, "in.xlsx", p["title"])
	assert.EqualValues(t, 4, p["delimiter"])
	assert.EqualValues(t, 65001, p["codePage"])
	assert.NotEmpty(t, p["token"], "signed with the secret in force")

	// Withdrawn once the conversion ended: the address is dead.
	q := ds.offered[0]
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	_, _, err = ds.svc.OpenOffered(ctx, q.Get("o"), exp, q.Get("p"), q.Get("sig"))
	assert.ErrorIs(t, err, onlyoffice.ErrOfferGone)

	// The same file again is a new key: a cached failure is never answered.
	req2 := engineRequest(t, t.TempDir(), "in.xlsx", "XLSX", "csv")
	require.NoError(t, eng.Convert(ctx, req2))
	require.Len(t, ds.payloads, 2)
	assert.NotEqual(t, ds.payloads[0]["key"], ds.payloads[1]["key"])
}

// What the engine is told: a refusal with the document server's code, no
// document server, a result over the limit - and nothing left behind.
func TestOfficeEngine_Failures(t *testing.T) {
	ds := newOfficeDS(t)
	eng := officeEngine{svc: ds.svc}
	ctx := context.Background()

	ds.answer = func(map[string]any, []byte) (int, []byte) { return -7, nil }
	req := engineRequest(t, t.TempDir(), "in.xlsx", "XLSX", "html")
	err := eng.Convert(ctx, req)
	var oe *wasmplugin.OfficeError
	require.True(t, errors.As(err, &oe), "%v", err)
	assert.Equal(t, -7, oe.Code)
	_, statErr := os.Stat(req.Dst)
	assert.True(t, os.IsNotExist(statErr), "nothing written")

	ds.answer = func(map[string]any, []byte) (int, []byte) { return 0, []byte(strings.Repeat("x", 4096)) }
	req = engineRequest(t, t.TempDir(), "in.docx", "DOCX", "pdf")
	req.MaxBytes = 1024
	assert.ErrorIs(t, eng.Convert(ctx, req), wasmplugin.ErrOfficeTooLarge)
	_, statErr = os.Stat(req.Dst)
	assert.True(t, os.IsNotExist(statErr), "nothing written")

	none := officeEngine{svc: onlyoffice.New(nil, nil, "", "", "https://filex.test", 0)}
	assert.False(t, none.Ready(ctx))
	assert.ErrorIs(t, none.Convert(ctx, engineRequest(t, t.TempDir(), "in.docx", "DOCX", "pdf")), wasmplugin.ErrOfficeUnconfigured)
	assert.False(t, officeEngine{}.Ready(ctx))
}
