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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// csvHarness is a .csv on a local storage and a stand-in document server: the
// saved document at /saved, its conversion service, and the converted result.
type csvHarness struct {
	svc  *Service
	node *model.Node
	root string
	// drv is the driver the service resolves the storage to. A test may put
	// another in its place.
	drv   storage.Driver
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

	h.drv = drv
	h.svc = New(store, func(int64) (storage.Driver, error) { return h.drv, nil },
		h.ds.URL, "shh", "https://filex.example", time.Hour)
	return h
}

// save is the document server's status-2 callback: the session is over and
// the document it saved, of type filetype, is at /saved.
func (h *csvHarness) save(t *testing.T, saved, filetype string, users ...string) map[string]any {
	t.Helper()
	return h.callback(t, StatusReadyForSaving, saved, filetype, users...)
}

// callback is the document server's callback with a document to write:
// status 2, or 6 for a save while the session is still open.
func (h *csvHarness) callback(t *testing.T, status int, saved, filetype string, users ...string) map[string]any {
	t.Helper()
	h.mu.Lock()
	h.saved = []byte(saved)
	h.mu.Unlock()
	key := h.sessionKey
	if key == "" {
		key = "k"
	}
	payload := map[string]any{"key": key, "status": status, "url": h.ds.URL + "/saved"}
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

// The cells nobody touched keep their text (csv_keep.go). Measured on filex
// 0.51.0 with Docs 9.4.0: one cell edited, and the callback wrote the phone
// numbers and the codes of every row without their leading zeros, a date its
// own way, and an empty cell at the end of each data row.

func TestCSVCallback_TheCellsNobodyTouchedKeepTheirText(t *testing.T) {
	h := newCSVHarness(t, measuredOriginal)
	resp := h.save(t, measuredSaved, "csv", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, measuredWant, h.disk(t),
		"the file as it was, but for the edited cell and the two new rows")
	assert.Equal(t, notify.EventFileUpdated, h.sink.wait(t).Event)
}

// A session that saves more than once (force saves, status 6, then the last
// one, status 2): each save is compared with the file as it is on the storage
// by then, which the save before it wrote.
func TestCSVCallback_EverySaveOfASessionKeepsThem(t *testing.T) {
	h := newCSVHarness(t, crlf("kod;ad;not", "007;elma;a", "042;armut;b"))

	resp := h.callback(t, StatusForceSave, ds("kod,ad,not", "7,elma,a1,", "42,armut,b,"), "csv", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;ad;not", "007;elma;a1", "042;armut;b"), h.disk(t))

	resp = h.callback(t, StatusForceSave, ds("kod,ad,not", "7,elma,a2,", "42,armut,b,"), "csv", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;ad;not", "007;elma;a2", "042;armut;b"), h.disk(t), "the second save still holds 007")

	resp = h.save(t, ds("kod,ad,not", "7,elma,a3,", "42,armut,b,", "9,kiraz,c"), "csv", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;ad;not", "007;elma;a3", "042;armut;b", "9;kiraz;c"), h.disk(t))
}

func TestCSVCallback_AFileTooLargeToCompareIsWrittenAsBefore(t *testing.T) {
	defer func(n int64) { csvKeepMaxBytes = n }(csvKeepMaxBytes)
	csvKeepMaxBytes = 16
	h := newCSVHarness(t, crlf("kod;tel", "007;05320000001"))
	resp := h.save(t, ds("kod,tel", "7,5320000001"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;tel", "7;5320000001"), h.disk(t),
		"the file's delimiter and line ends, ONLYOFFICE's values: a save is never refused over this")
}

// A file in a legacy code page (Windows-1254 here): its text cannot be
// compared with the UTF-8 ONLYOFFICE saved. It is written as 0.51.0 wrote it,
// in UTF-8 with a byte order mark, and is a UTF-8 file from then on.
func TestCSVCallback_AFileThatIsNotUTF8IsWrittenAsBefore(t *testing.T) {
	h := newCSVHarness(t, "kod;ad\r\n007;\xFEeker\r\n")
	resp := h.save(t, ds("kod,ad", "7,şeker"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "\xEF\xBB\xBF"+crlf("kod;ad", "7;şeker"), h.disk(t))

	// The next save is compared: its rows are the file's own bytes, without
	// the empty cell ONLYOFFICE ends them with.
	resp = h.save(t, ds("kod,ad,", "7,şeker,", "9,nar,"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "\xEF\xBB\xBF"+crlf("kod;ad", "7;şeker", "9;nar"), h.disk(t))
}

// The sniff reads the first 64 KiB. A file that is ASCII that far and a legacy
// code page after it is not UTF-8 either.
func TestCSVCallback_AFileThatIsNotUTF8AfterItsHeadIsWrittenAsBefore(t *testing.T) {
	original := strings.Repeat("a;007\r\n", 70<<10/7) + "b;\xFDeker\r\n"
	h := newCSVHarness(t, original)
	resp := h.save(t, ds("a,7"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "a;7\r\n", h.disk(t))
}

func TestCSVCallback_AFileGoneFromTheStorageIsWrittenTheServersWay(t *testing.T) {
	h := newCSVHarness(t, crlf("kod;tel", "007;05320000001"))
	require.NoError(t, os.Remove(filepath.Join(h.root, h.node.Name)))
	resp := h.save(t, ds("kod,tel", "7,5320000001"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, ds("kod,tel", "7,5320000001"), h.disk(t),
		"nothing is guessed about a file nobody could read: comma, a byte order mark")
}

// A document server set to `assemblyFormatAsOrigin: false` saves an XLSX; the
// CSV it is converted to goes through the same comparison.
func TestCSVCallback_AnXLSXSaveKeepsThemToo(t *testing.T) {
	h := newCSVHarness(t, crlf("kod;tel;not", "007;05320000001;eski", "042;05330000002;x"))
	h.converted = []byte(ds("kod,tel,not", "7,5320000001,yeni", "42,5330000002,x"))
	resp := h.save(t, "PK\x03\x04 an XLSX the editor saved", "xlsx", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;tel;not", "007;05320000001;yeni", "042;05330000002;x"), h.disk(t))
}

// captureLog turns the process's log into text a test can read, until the
// test ends.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// logLine is the one line of log that holds every one of has.
func logLine(t *testing.T, log string, has ...string) string {
	t.Helper()
	for _, line := range strings.Split(log, "\n") {
		found := true
		for _, h := range has {
			found = found && strings.Contains(line, h)
		}
		if found {
			return line
		}
	}
	t.Fatalf("no log line with %q in:\n%s", has, log)
	return ""
}

// The file a save is compared with is read through the resolver the fetch
// endpoint serves the document server from (Service.Body), not from the
// driver. While an upload is still transferring the two differ: the editor was
// given the staged copy, and the driver still holds the version before it.
// Read from the driver, the save would be lined up with a file nobody edited.
func TestCSVCallback_ASaveIsComparedWithTheCopyTheEditorWasGiven(t *testing.T) {
	ctx := context.Background()
	// On the driver: the version the upload is replacing, its zeros long gone.
	h := newCSVHarness(t, crlf("kod;ad;not", "7;elma;a", "42;armut;b"))
	uploaded := crlf("kod;ad;not", "007;elma;a", "042;armut;b")

	area := staging.New(t.TempDir())
	const upload = "csv-keep-upload-0001"
	_, err := area.Create(upload, int64(len(uploaded)), 16, "")
	require.NoError(t, err)
	for off, n := 0, 1; off < len(uploaded); off, n = off+16, n+1 {
		part := uploaded[off:min(off+16, len(uploaded))]
		_, err := area.WritePart(upload, n, strings.NewReader(part), int64(len(part)))
		require.NoError(t, err)
	}
	require.NoError(t, h.store.CreateStagedUpload(ctx, &model.StagedUpload{
		ID: upload, StorageID: h.node.StorageID, StorageKey: h.node.Path,
		TotalSize: int64(len(uploaded)), ChunkSize: 16, State: model.StagedUploadCommitting,
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	// What a commit does once the row is published.
	require.NoError(t, h.store.AttachStagedUploadTarget(ctx, upload, h.node.ID, 0))
	require.NoError(t, h.store.SetNodeTransferState(ctx, h.node.ID, model.TransferStateStaged))
	h.svc.AttachBody(filebody.New(h.store, area))

	resp := h.save(t, ds("kod,ad,not", "7,elma,a", "42,armut,c"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;ad;not", "007;elma;a", "042;armut;c"), h.disk(t),
		"the zeros of the uploaded copy; the driver's copy never had them")
}

// cutRead is a storage whose first read breaks off after a number of bytes: a
// backend that goes away in the middle of a file.
type cutRead struct {
	*local.Driver
	after int64
	reads int
}

func (d *cutRead) Read(ctx context.Context, rel string) (io.ReadCloser, error) {
	rc, err := d.Driver.Read(ctx, rel)
	if d.reads++; err != nil || d.reads > 1 {
		return rc, err
	}
	return &cutReader{ReadCloser: rc, left: d.after}, nil
}

type cutReader struct {
	io.ReadCloser
	left int64
}

func (r *cutReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, errors.New("the storage went away")
	}
	n, err := r.ReadCloser.Read(p[:min(int64(len(p)), r.left)])
	r.left -= int64(n)
	return n, err
}

// The head of the file arrived and the rest did not: how the file is written
// is known, what it says is not. It keeps its own delimiter and line ends
// (0.51.0 knew that much too), and ONLYOFFICE's values.
func TestCSVCallback_AFileThatStopsBeingReadKeepsItsDialect(t *testing.T) {
	original := crlf("kod;tel") + strings.Repeat("007;05320000001\r\n", 2*CSVSniffBytes/17)
	h := newCSVHarness(t, original)
	h.drv = &cutRead{Driver: h.drv.(*local.Driver), after: CSVSniffBytes}
	log := captureLog(t)

	resp := h.save(t, ds("kod,tel", "7,5320000001"), "csv")
	assert.Equal(t, 0, resp["error"], "a save is never refused over this")
	assert.Equal(t, crlf("kod;tel", "7;5320000001"), h.disk(t))
	assert.Contains(t, logLine(t, log(), "CSV cells not kept", "why=unreadable"), "level=INFO")
}

// The row says how long the file is, and the row may be behind (a file
// replaced on the storage while it is open). The read stops one byte past
// the bound, so a longer file is known to be one and is not compared cut
// short: its last rows would count as deleted.
func TestCSVCallback_AFileLongerThanItsRowSaysIsNotComparedCutShort(t *testing.T) {
	h := newCSVHarness(t, crlf("kod;tel"))
	// Longer than the head the dialect is read from, and than the bound.
	longer := crlf("kod;tel") + strings.Repeat("007;05320000001\r\n", (80<<10)/17)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, h.node.Name), []byte(longer), 0o644))
	defer func(n int64) { csvKeepMaxBytes = n }(csvKeepMaxBytes)
	csvKeepMaxBytes = 72 << 10
	require.Less(t, h.node.Size, csvKeepMaxBytes, "the row still says the short file")
	require.Greater(t, int64(len(longer)), csvKeepMaxBytes)
	log := captureLog(t)

	resp := h.save(t, ds("kod,tel", "7,5320000001"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, crlf("kod;tel", "7;5320000001"), h.disk(t))
	logLine(t, log(), "CSV cells not kept", "why=too_large")
}

// What the log says when the cells were not kept. ⚠ `check_failed` and `panic`
// are filex's own failures: a warning, the panic with what it said and where.
// Logged like a file that is not UTF-8, a bug in production would leave
// nothing to find it by.
func TestCSVCallback_ASaveFilexFailedToKeepIsAWarning(t *testing.T) {
	// The last record's quote is never closed: written as it was with a record
	// after it, it would swallow that record (csvKeepCheck refuses it).
	h := newCSVHarness(t, "a;b\n\"c;d")
	log := captureLog(t)
	resp := h.save(t, ds("a,b", "c;d", "new,row"), "csv")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "a;b\n\"c;d\"\nnew;row\n", h.disk(t), "the save, in the file's dialect")
	assert.Contains(t, logLine(t, log(), "CSV cells not kept", "why=check_failed"), "level=WARN")

	log = captureLog(t)
	csvNotKept(h.node, CSVKept{Why: "panic", Panic: "index out of range [3] with length 3\ngoroutine 7 [running]:"})
	line := logLine(t, log(), "CSV cells not kept", "why=panic")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "path=/list.csv")
	assert.Contains(t, line, "index out of range [3] with length 3", "what panicked")
	assert.Contains(t, line, "goroutine 7", "and where")

	log = captureLog(t)
	csvNotKept(h.node, CSVKept{Why: "not_utf8"})
	assert.Contains(t, logLine(t, log(), "CSV cells not kept", "why=not_utf8"), "level=INFO")
	log = captureLog(t)
	csvNotKept(h.node, CSVKept{Why: "empty"})
	assert.Empty(t, log(), "an empty file has no cell to keep: nothing to say")
}
