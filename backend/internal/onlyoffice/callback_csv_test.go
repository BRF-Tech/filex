package onlyoffice

// Saving a .csv edited in ONLYOFFICE (filex 0.51, GitHub #81; callback_csv.go).
//
// Measured on ONLYOFFICE Docs 9.4: with its default (`assemblyFormatAsOrigin:
// true`) an edited CSV comes back as CSV, but comma-separated with a UTF-8
// byte order mark whatever the file was; with that setting off it comes back
// as an XLSX (`filetype: "xlsx"`). The 0.50 callback wrote either as it came,
// to the .csv path.
//
// ⚠ Every test here drives HandleCallback through the API the 0.50 code
// already had, and every one fails there: the semicolon file was rewritten
// with commas and a byte order mark, and the XLSX, the PDF and the zip bytes
// were written under the .csv name with nobody told.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// csvHarness is a .csv on a local storage and a stand-in document server: the
// saved document at /saved, its conversion service, and the converted result.
type csvHarness struct {
	svc   *Service
	node  *model.Node
	root  string
	sink  *captureSink
	ds    *httptest.Server
	store db.Store
	// sessionKey is the document key the callbacks name ("k" until open).
	sessionKey string
	// bodyUsers, when set, is what the callback's BODY says `users` is,
	// whatever its signed token says.
	bodyUsers []string

	mu        sync.Mutex
	saved     []byte
	converted []byte
	convErr   int
	convReqs  []map[string]any
}

func newCSVHarness(t *testing.T, original string) *csvHarness {
	t.Helper()
	return newDocHarness(t, "list.csv", "text/csv", original)
}

// newDocHarness is the harness for a document of any name.
func newDocHarness(t *testing.T, name, mime, original string) *csvHarness {
	t.Helper()
	h := &csvHarness{root: t.TempDir()}
	require.NoError(t, os.WriteFile(filepath.Join(h.root, name), []byte(original), 0o644))

	_, store := dbtest.NewTestDB(t)
	h.store = store
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "Local", Driver: "local", MountPath: "/local", Enabled: true,
		ConfigJSON: []byte(fmt.Sprintf(`{"path":%q}`, filepath.ToSlash(h.root))),
	})
	require.NoError(t, err)
	h.node, err = store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: name, Path: "/" + name, PathHash: pathkey.Hash(st.ID, "/"+name),
		StorageKey: "/" + name, Type: model.NodeTypeFile, Size: int64(len(original)), Mime: mime,
		SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": h.root}))

	mux := http.NewServeMux()
	mux.HandleFunc("/saved", func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, _ = w.Write(h.saved)
	})
	mux.HandleFunc("/ConvertService.ashx", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		tok, _ := body["token"].(string)
		claims, err := verifyHS256(tok, "shh")
		if err != nil {
			_, _ = w.Write([]byte(`{"error":-8}`))
			return
		}
		h.mu.Lock()
		h.convReqs = append(h.convReqs, claims)
		code := h.convErr
		h.mu.Unlock()
		if code != 0 {
			_, _ = fmt.Fprintf(w, `{"error":%d}`, code)
			return
		}
		_, _ = fmt.Fprintf(w, `{"endConvert":true,"percent":100,"fileType":"csv","fileUrl":%q}`, "http://"+r.Host+"/conv.csv")
	})
	mux.HandleFunc("/conv.csv", func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, _ = w.Write(h.converted)
	})
	h.ds = httptest.NewServer(mux)
	t.Cleanup(h.ds.Close)

	protocolsync.SetChangeEmitter(&captureFrames{})
	t.Cleanup(func() { protocolsync.SetChangeEmitter(nil) })
	h.sink = newCaptureSink()
	writehook.Configure(func(context.Context, *model.Node) {}, h.sink)
	t.Cleanup(func() { writehook.Configure(nil, nil) })

	h.svc = New(store, func(int64) (storage.Driver, error) { return drv, nil },
		h.ds.URL, "shh", "https://filex.example", time.Hour)
	return h
}

// save is the document server's status-2 callback: the session is over and
// the document it saved, of type filetype, is at /saved.
func (h *csvHarness) save(t *testing.T, saved, filetype string, users ...string) map[string]any {
	t.Helper()
	h.mu.Lock()
	h.saved = []byte(saved)
	h.mu.Unlock()
	key := h.sessionKey
	if key == "" {
		key = "k"
	}
	payload := map[string]any{"key": key, "status": StatusReadyForSaving, "url": h.ds.URL + "/saved"}
	if filetype != "" {
		payload["filetype"] = filetype
	}
	if len(users) > 0 {
		payload["users"] = users
	}
	tok, err := signHS256(payload, "shh")
	require.NoError(t, err)
	body := map[string]any{"token": tok}
	for k, v := range payload {
		body[k] = v
	}
	if h.bodyUsers != nil {
		body["users"] = h.bodyUsers
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/onlyoffice/callback?node=1", strings.NewReader(string(raw)))
	resp, err := h.svc.HandleCallback(req, h.node.ID)
	require.NoError(t, err)
	return resp
}

func (h *csvHarness) disk(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, h.node.Name))
	require.NoError(t, err)
	return string(b)
}

// key is the editing session's key the config gives the document now.
func (h *csvHarness) key(t *testing.T) string {
	t.Helper()
	cfg, err := h.svc.BuildConfigForNode(context.Background(), h.node, nil, "en", "edit")
	require.NoError(t, err)
	return cfg.Config["document"].(map[string]any)["key"].(string)
}

// What ONLYOFFICE Docs 9.4 hands back for an edited CSV: commas, a UTF-8 byte
// order mark, "\n".
const savedByDS = "\xEF\xBB\xBFad,adet,not\nelma,42,a; b\narmut,5,şeker\n"

func TestCSVCallback_KeepsTheFilesDelimiterByteOrderMarkAndLineEnds(t *testing.T) {
	h := newCSVHarness(t, "ad;adet;not\r\nelma;3;\"a; b\"\r\narmut;5;şeker\r\n")
	resp := h.save(t, savedByDS, "csv", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "ad;adet;not\r\nelma;42;\"a; b\"\r\narmut;5;şeker\r\n", h.disk(t),
		"a semicolon CSV with CRLF and no byte order mark stays one")
	assert.Equal(t, notify.EventFileUpdated, h.sink.wait(t).Event)
}

func TestCSVCallback_AFileWithAByteOrderMarkKeepsIt(t *testing.T) {
	h := newCSVHarness(t, "\xEF\xBB\xBFad,adet,not\nelma,3,a; b\narmut,5,şeker\n")
	resp := h.save(t, savedByDS, "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, savedByDS, h.disk(t))
	h.sink.wait(t)
}

func TestCSVCallback_AnXLSXSaveIsConvertedNeverWrittenUnderTheCSVName(t *testing.T) {
	for name, filetype := range map[string]string{
		"the server says xlsx": "xlsx",
		// A server that does not name the type: the zip bytes do.
		"the server says nothing": "",
	} {
		t.Run(name, func(t *testing.T) {
			h := newCSVHarness(t, "name,qty\nelma,3\n")
			h.converted = []byte("\xEF\xBB\xBFname,qty\nelma,42\n")
			resp := h.save(t, "PK\x03\x04 an XLSX the editor saved", filetype, "7")
			assert.Equal(t, 0, resp["error"])
			assert.Equal(t, "name,qty\nelma,42\n", h.disk(t), "the XLSX was turned back into the CSV it was")
			assert.Equal(t, notify.EventFileUpdated, h.sink.wait(t).Event)

			h.mu.Lock()
			defer h.mu.Unlock()
			require.Len(t, h.convReqs, 1, "converted by the document server's conversion service")
			c := h.convReqs[0]
			assert.Equal(t, "xlsx", c["filetype"])
			assert.Equal(t, "csv", c["outputtype"])
			assert.EqualValues(t, 65001, c["codePage"])
			assert.EqualValues(t, 4, c["delimiter"])
			assert.True(t, strings.HasPrefix(fmt.Sprint(c["url"]), "https://filex.example/api/files/onlyoffice/fetch?"),
				"the spreadsheet is offered from filex's own door: %v", c["url"])
		})
	}
}

func TestCSVCallback_ASaveThatIsNotACSVIsNotWritten(t *testing.T) {
	for name, c := range map[string]struct {
		filetype, saved string
		convErr         int
	}{
		"a PDF":                  {"pdf", "%PDF-1.7 not a table", 0},
		"zip bytes called csv":   {"csv", "PK\x03\x04 a package, not text", 0},
		"XLS bytes called csv":   {"csv", "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1 an old workbook", 0},
		"an XLSX not converted":  {"xlsx", "PK\x03\x04 an XLSX", DSConvert},
		"a type it cannot judge": {"docx", "PK\x03\x04 a letter", 0},
	} {
		t.Run(name, func(t *testing.T) {
			const original = "name,qty\nelma,3\n"
			h := newCSVHarness(t, original)
			h.convErr = c.convErr
			before := h.key(t)

			resp := h.save(t, c.saved, c.filetype, "7", "7", "not-an-id")
			assert.Equal(t, 1, resp["error"], "the document server is told the save failed")
			assert.Equal(t, original, h.disk(t), ".csv kept its CSV")

			e := h.sink.wait(t)
			assert.Equal(t, notify.EventFileUploadFailed, e.Event, "the editor is told the save did not land")
			require.NotNil(t, e.UserID)
			assert.Equal(t, int64(7), *e.UserID)
			assert.Equal(t, writehook.OriginOnlyOffice, e.Meta["origin"])
			assert.Equal(t, "/list.csv", e.Node.Path)
			// In the reader's language (0.51: the server catalogue's words in
			// meta, every built-in language).
			assert.Equal(t, "Your edit to list.csv was not saved", e.Meta["title_en"])
			assert.Equal(t, "list.csv dosyasındaki düzenlemeniz kaydedilmedi", e.Meta["title_tr"])
			assert.Contains(t, e.Meta["body_tr"], "list.csv değişmedi.")
			assert.Contains(t, e.Meta["body_en"], "list.csv did not change.")
			select {
			case extra := <-h.sink.ch:
				t.Fatalf("one notice per editor, got another: %v", extra.Event)
			case <-time.After(200 * time.Millisecond):
			}

			assert.NotEqual(t, before, h.key(t),
				"the next opening gets a new key: an editor opened on the refused session's key never loads (Docs 9.4)")
		})
	}
}
