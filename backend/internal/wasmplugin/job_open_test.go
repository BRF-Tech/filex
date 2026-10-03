package wasmplugin

// filex #78: a job's result may send the person who queued it to one of the
// files it produced, with one of the app's own screens on it
// (ActionRunOutput.Surface.Open). The signing app's "Convert to PDF" queued a
// conversion and its page then said "queued" for good: nothing followed the
// job, and the wizard never went on to the PDF.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// openApp is an installed app with one page view and one action, enough for
// CheckOpen to judge a request.
func openApp() *Installed {
	return &Installed{Manifest: &Manifest{Manifest: wire.Manifest{
		Name:    "signer",
		Actions: []wire.Action{{ID: "request", View: "request"}},
		Views:   []wire.View{{ID: "request", Placement: "page"}},
	}}}
}

func TestOpenAfter_ResolvesToTheJobsOwnOutput(t *testing.T) {
	out := &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{{Ref: "out:1", Name: "a.pdf"}, {Ref: "out:2", Name: "b.pdf"}}}
	committed := func() []JobOutput { return []JobOutput{{Path: "docs/a.pdf"}, {Path: "docs/b.pdf"}} }

	// No path: the first output.
	c := committed()
	out.Surface = &wire.Surface{Open: &wire.OpenRequest{View: "request"}}
	assert.Empty(t, openAfter(openApp(), out, c))
	require.NotNil(t, c[0].Open, "an open with no path lands on the job's first output")
	assert.Equal(t, JobOpen{View: "request"}, *c[0].Open)
	assert.Nil(t, c[1].Open)

	// A ref the job returned: that output, under the name it was committed as.
	c = committed()
	out.Surface = &wire.Surface{Open: &wire.OpenRequest{Path: "out:2", Action: "request"}}
	assert.Empty(t, openAfter(openApp(), out, c))
	assert.Nil(t, c[0].Open)
	require.NotNil(t, c[1].Open)
	assert.Equal(t, JobOpen{Action: "request"}, *c[1].Open)

	// Naming neither screen just opens the file.
	c = committed()
	out.Surface = &wire.Surface{Open: &wire.OpenRequest{}}
	assert.Empty(t, openAfter(openApp(), out, c))
	require.NotNil(t, c[0].Open)
	assert.Equal(t, JobOpen{}, *c[0].Open)
}

func TestOpenAfter_DropsWhatTheJobMayNotAskFor(t *testing.T) {
	out := &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{{Ref: "out:1", Name: "a.pdf"}}}
	cases := map[string]*wire.OpenRequest{
		// A job sends its person to what it made, never to any other file.
		"another file":   {Path: "docs://contracts/secret.pdf", View: "request"},
		"an unknown ref": {Path: "out:9", View: "request"},
		// The screen has to be the app's own, and one of the two.
		"a screen the app does not have":  {View: "elsewhere"},
		"an action the app does not have": {Action: "elsewhere"},
		"both an action and a view":       {Action: "request", View: "request"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			c := []JobOutput{{Path: "docs/a.pdf"}}
			out.Surface = &wire.Surface{Open: req}
			assert.NotEmpty(t, openAfter(openApp(), out, c), "the drop is said, for the app's log")
			assert.Nil(t, c[0].Open, "nothing is marked")
		})
	}

	// No surface, or one without `open`: nothing to do and nothing to say.
	c := []JobOutput{{Path: "docs/a.pdf"}}
	out.Surface = nil
	assert.Empty(t, openAfter(openApp(), out, c))
	out.Surface = &wire.Surface{Toast: wire.Text{"en": "hi"}}
	assert.Empty(t, openAfter(openApp(), out, c))
	assert.Nil(t, c[0].Open)
}

// The ops row carries the mark adapter-qualified, and only once the job has
// FINISHED: a row that says "go there" while the job runs, or after it
// failed, would send somebody to nothing.
func TestDecorateOps_OpenOnlyOnAFinishedJob(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reg, err := New(Options{Store: store, Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(ctx) })
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "docs", Driver: "local", MountPath: "/docs", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"/tmp/filex-open-test"}`),
	})
	require.NoError(t, err)

	marked := `[{"path":"reports/nda.pdf","open":{"view":"request"}},{"path":"reports/nda-2.pdf"}]`
	okID, runningID, failedID, plainID := int64(61), int64(62), int64(63), int64(64)
	for _, j := range []*model.AppPluginJob{
		{ID: "job-ok", OpID: &okID, Status: model.AppPluginJobOK, OutputsJSON: marked},
		{ID: "job-running", OpID: &runningID, Status: model.AppPluginJobRunning, OutputsJSON: marked},
		{ID: "job-failed", OpID: &failedID, Status: model.AppPluginJobFailed, OutputsJSON: marked, Error: "boom"},
		{ID: "job-plain", OpID: &plainID, Status: model.AppPluginJobOK, OutputsJSON: `[{"path":"reports/nda.pdf"}]`},
	} {
		j.PluginName, j.ActionID, j.StorageID, j.Locale, j.Label = "signer", "convert", st.ID, "en", "Convert"
		require.NoError(t, store.CreateAppPluginJob(ctx, j))
	}
	rows := []*ops.Op{
		{ID: okID, Kind: ops.OpPluginAction, Status: "ok"},
		{ID: runningID, Kind: ops.OpPluginAction, Status: "running"},
		{ID: failedID, Kind: ops.OpPluginAction, Status: "failed"},
		{ID: plainID, Kind: ops.OpPluginAction, Status: "ok"},
	}
	reg.DecorateOps(ctx, rows)

	require.NotNil(t, rows[0].Open, "a finished job's mark reaches its ops row")
	assert.Equal(t, ops.OpOpen{Path: "docs://reports/nda.pdf", View: "request"}, *rows[0].Open)
	assert.Equal(t, []ops.OpOutput{{Path: "docs://reports/nda.pdf"}, {Path: "docs://reports/nda-2.pdf"}}, rows[0].Outputs,
		"the outputs read as they always did")
	assert.Nil(t, rows[1].Open, "not while the job runs")
	assert.Nil(t, rows[2].Open, "not after it failed")
	assert.Nil(t, rows[3].Open, "not on a job whose app asked for nothing")

	// Through JSON, the shape the client reads (usePendingOps normalizeOp).
	b, err := json.Marshal(rows[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"open":{"path":"docs://reports/nda.pdf","view":"request"}`)
	b, _ = json.Marshal(rows[3])
	assert.NotContains(t, string(b), `"open"`)
}

// openOps numbers the ops rows of runUpperThen: one per job, because
// DecorateOps finds a job BY its ops row.
var openOps int64 = 7800

// The whole round, with a real module: `upper` asked to bring its person back
// to the `wizard` page on what it wrote (the e2e 192 walk, without a browser).
func (h *harness) runUpperThen(t *testing.T, p *Installed, paths []string, params map[string]any) (*model.AppPluginJob, *ops.Op) {
	t.Helper()
	pj, _ := json.Marshal(paths)
	pp, _ := json.Marshal(params)
	openOps++
	opID := openOps
	job := &model.AppPluginJob{ID: NewJobID(), OpID: &opID, PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "upper",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pp), Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	require.NoError(t, h.reg.RunPluginAction(context.Background(),
		&ops.Op{ID: opID, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: paths, Dest: job.ID}, nil))
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	row := &ops.Op{ID: opID, Kind: ops.OpPluginAction, Status: "ok"}
	h.reg.DecorateOps(context.Background(), []*ops.Op{row})
	return got, row
}

func TestJob_OpenSendsThePersonToTheOutput(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/a.txt", "hello")
	h.writeFile(t, "docs/b.txt", "world")

	_, row := h.runUpperThen(t, p, []string{"docs/a.txt"}, map[string]any{"then": "wizard"})
	require.NotNil(t, row.Open, "the job's `surface.open` reached its ops row")
	assert.Equal(t, ops.OpOpen{Path: "main://docs/a-upper.txt", View: "wizard"}, *row.Open)

	// By its ref: the LAST output, under the name the host committed it as -
	// which only the host knew (a taken name gets a free one).
	_, row = h.runUpperThen(t, p, []string{"docs/a.txt", "docs/b.txt"}, map[string]any{"then": "wizard", "then_path": "last"})
	require.NotNil(t, row.Open)
	assert.Equal(t, "main://"+h.sink.siblings[len(h.sink.siblings)-1], row.Open.Path)
	assert.Contains(t, row.Open.Path, "b-upper")

	// A file the job did not make, or a screen the app does not have: the
	// job still finishes, and its row sends nobody anywhere.
	_, row = h.runUpperThen(t, p, []string{"docs/a.txt"}, map[string]any{"then": "wizard", "then_path": "main://docs/b.txt"})
	assert.Nil(t, row.Open)
	assert.NotEmpty(t, row.Outputs)
	_, row = h.runUpperThen(t, p, []string{"docs/a.txt"}, map[string]any{"then": "no-such-view"})
	assert.Nil(t, row.Open)

	// Asked for nothing: nothing, exactly as before.
	_, row = h.runUpperThen(t, p, []string{"docs/a.txt"}, map[string]any{})
	assert.Nil(t, row.Open)
}
