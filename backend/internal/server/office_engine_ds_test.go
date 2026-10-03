package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// The office engine end to end, on a REAL ONLYOFFICE Document Server and the
// REAL Convert app (filex 0.50): the app's soffice line read by the host, the
// file offered through filex's fetch door, converted by the document server,
// written beside the original. Run against each Convert build given - an old
// one (0.1.x, `engines:libreoffice`) proves the alias, a new one
// (`engines:office`) the new name.
//
// It needs a document server and a network the document server can reach
// this test on, so it runs only when told where they are:
//
//	FILEX_TEST_OO_URL       the document server, as this test reaches it
//	FILEX_TEST_OO_JWT       its JWT secret
//	FILEX_TEST_OO_LISTEN    where this test serves filex's fetch door (":18080")
//	FILEX_TEST_OO_CALLBACK  that door's address as the document server reaches it
//	FILEX_TEST_OO_FIXTURES  a directory with letter.docx and report.xlsx
//	FILEX_TEST_CONVERT      Convert builds, ':'-separated directories, each
//	                        with filex-app.json and plugin.wasm
func TestOfficeEngine_TheConvertAppOnARealDocumentServer(t *testing.T) {
	dsURL, secret := os.Getenv("FILEX_TEST_OO_URL"), os.Getenv("FILEX_TEST_OO_JWT")
	listen, callback := os.Getenv("FILEX_TEST_OO_LISTEN"), os.Getenv("FILEX_TEST_OO_CALLBACK")
	fixtures, builds := os.Getenv("FILEX_TEST_OO_FIXTURES"), os.Getenv("FILEX_TEST_CONVERT")
	if dsURL == "" || secret == "" || listen == "" || callback == "" || fixtures == "" || builds == "" {
		t.Skip("needs a document server: FILEX_TEST_OO_URL, _JWT, _LISTEN, _CALLBACK, _FIXTURES and FILEX_TEST_CONVERT")
	}
	ctx := context.Background()
	svc := onlyoffice.New(nil, nil, dsURL, secret, strings.TrimRight(callback, "/"), 0)

	// filex's fetch door, where the document server downloads what is offered.
	mux := http.NewServeMux()
	mux.HandleFunc(onlyoffice.FetchPath, (&handlers.OnlyOffice{Service: svc}).Fetch)
	ln, err := net.Listen("tcp", listen)
	require.NoError(t, err)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// A measurement for the documentation, not a promise: does the document
	// server honour the CSV delimiter on a CSV it WRITES?
	{
		dir := t.TempDir()
		b, err := os.ReadFile(filepath.Join(fixtures, "report.xlsx"))
		require.NoError(t, err)
		src := filepath.Join(dir, "report.xlsx")
		require.NoError(t, os.WriteFile(src, b, 0o600))
		dst := filepath.Join(dir, "report.csv")
		err = officeEngine{svc: svc}.Convert(ctx, wasmplugin.OfficeRequest{Src: src, Name: "report.xlsx", From: "xlsx", To: "csv", Delimiter: 2, Dst: dst, MaxBytes: 1 << 20})
		if err != nil {
			t.Logf("MEASURE csv with delimiter 2 (semicolon): %v", err)
		} else {
			out, _ := os.ReadFile(dst)
			line := strings.SplitN(string(out), "\n", 2)[0]
			t.Logf("MEASURE csv with delimiter 2 (semicolon): first line %q", line)
		}
	}

	for _, dir := range strings.Split(builds, ":") {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "filex-app.json"))
			require.NoError(t, err)
			parsed, err := wasmplugin.ParseManifest(raw)
			require.NoError(t, err, "the host accepts this Convert's manifest")
			office := ""
			for _, p := range parsed.Perms {
				if p == "engines:office" || p == "engines:libreoffice" {
					office = string(p)
				}
			}
			require.NotEmpty(t, office, "this Convert asks for the office engine")
			t.Logf("Convert %s asks for %s", parsed.Version, office)

			h := convertWorld(t, ctx, raw, filepath.Join(dir, "plugin.wasm"), parsed, officeEngine{svc: svc})
			for _, f := range []string{"letter.docx", "report.xlsx"} {
				b, err := os.ReadFile(filepath.Join(fixtures, f))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(h.root, f), b, 0o644))
			}

			job := h.run(t, "letter.docx", map[string]any{"target": "pdf"})
			require.Equal(t, model.AppPluginJobOK, job.Status, "docx -> pdf: %s", job.Error)
			pdf := h.read(t, "letter.pdf")
			assert.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")), "a PDF")

			job = h.run(t, "letter.docx", map[string]any{"target": "odt"})
			require.Equal(t, model.AppPluginJobOK, job.Status, "docx -> odt: %s", job.Error)
			odt := h.read(t, "letter.odt")
			assert.Contains(t, string(odt[:min(120, len(odt))]), "application/vnd.oasis.opendocument.text", "an OpenDocument text, which only the office engine makes")

			job = h.run(t, "report.xlsx", map[string]any{"target": "csv"})
			require.Equal(t, model.AppPluginJobOK, job.Status, "xlsx -> csv: %s", job.Error)
			assert.NotEmpty(t, h.read(t, "report.csv"))

			if office == "engines:libreoffice" {
				// The old converter still routes spreadsheet -> HTML through
				// the office engine, which ONLYOFFICE refuses (-7): the engine
				// answers a failed run, and the converter's own retry takes
				// its sandbox route - the person still gets the HTML
				// (measured 2026-10-02 with Convert 0.1.1).
				job = h.run(t, "report.xlsx", map[string]any{"target": "html"})
				assert.Equal(t, model.AppPluginJobOK, job.Status, "%s", job.Error)
				html := h.read(t, "report.html")
				assert.Contains(t, strings.ToLower(string(html[:min(512, len(html))])), "<", "an HTML document")
			}
		})
	}
}

// convertWorld is a registry with the office engine behind it, one local
// storage, and the Convert build installed with everything it asks for.
type convertHarness struct {
	reg   *wasmplugin.Registry
	store interface {
		CreateAppPluginJob(context.Context, *model.AppPluginJob) error
		GetAppPluginJob(context.Context, string) (*model.AppPluginJob, error)
	}
	root   string
	st     *model.Storage
	plugin *wasmplugin.Installed
}

func convertWorld(t *testing.T, ctx context.Context, manifest []byte, wasmPath string, parsed *wasmplugin.Manifest, office wasmplugin.OfficeConverter) *convertHarness {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "docs", Driver: "local", MountPath: "/docs", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	require.NoError(t, err)
	reg, err := wasmplugin.New(wasmplugin.Options{
		Store: store, Dir: filepath.Join(t.TempDir(), "app-plugins"), SecretKey: "0123456789abcdef0123456789abcdef",
		StorageResolver: func(int64) (storage.Driver, error) { return drv, nil },
		Office:          office,
	})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	reg.SetOutputSink(dirSink{root: root})
	wasm, err := os.ReadFile(wasmPath)
	require.NoError(t, err)
	granted := make([]string, 0, len(parsed.Perms))
	for _, p := range parsed.Perms {
		granted = append(granted, string(p))
	}
	status, _, err := reg.Install(ctx, &wasmplugin.InstallInput{Manifest: manifest, Wasm: bytes.NewReader(wasm), Source: "upload", Granted: granted, Lang: "en"})
	require.NoError(t, err)
	p, ok := reg.ByID(status.ID)
	require.True(t, ok)
	return &convertHarness{reg: reg, store: store, root: root, st: st, plugin: p}
}

func (h *convertHarness) run(t *testing.T, rel string, params map[string]any) *model.AppPluginJob {
	t.Helper()
	ctx := context.Background()
	pj, _ := json.Marshal([]string{rel})
	pr, _ := json.Marshal(params)
	job := &model.AppPluginJob{ID: wasmplugin.NewJobID(), PluginID: h.plugin.Row.ID, PluginName: h.plugin.Row.Name, ActionID: "convert",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pr), Locale: "en", Label: "convert", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(ctx, job))
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_ = h.reg.RunPluginAction(runCtx, &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{rel}, Dest: job.ID}, nil)
	got, err := h.store.GetAppPluginJob(ctx, job.ID)
	require.NoError(t, err)
	return got
}

func (h *convertHarness) read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, rel))
	require.NoError(t, err, "%s beside the original", rel)
	return b
}

// dirSink commits a job's outputs into the storage's directory.
type dirSink struct{ root string }

func (d dirSink) CommitSibling(_ context.Context, _ int64, dir, name string, r io.Reader, _ int64, _ *int64) (string, error) {
	rel := strings.Trim(strings.Trim(dir, "/")+"/"+name, "/")
	f, err := os.Create(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return rel, err
}

func (d dirSink) CommitVersion(_ context.Context, _ int64, rel string, r io.Reader, _ int64, _ *int64) error {
	f, err := os.Create(filepath.Join(d.root, filepath.FromSlash(strings.Trim(rel, "/"))))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
