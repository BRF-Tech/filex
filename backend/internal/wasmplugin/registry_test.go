package wasmplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/wasmfixture"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Registry end-to-end: install → job → outputs → state ───────────────

// harness is a registry over an in-memory store and a local storage.
type harness struct {
	reg   *Registry
	store storeT
	drv   *local.Driver
	st    *model.Storage
	root  string
	sink  *memSink
	share *share.Service
}

type storeT = interface {
	GetAppPluginJob(ctx context.Context, id string) (*model.AppPluginJob, error)
	CreateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error
	GetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) (string, bool, error)
	GetAppPluginByName(ctx context.Context, name string) (*model.AppPlugin, error)
	ListAppPlugins(ctx context.Context) ([]*model.AppPlugin, error)
	GetShareByToken(ctx context.Context, token string) (*model.Share, error)
	GetShareByID(ctx context.Context, id int64) (*model.Share, error)
	ListAppPluginShares(ctx context.Context, pluginID int64, activeOnly bool, limit, offset int) ([]*db.ShareWithMeta, int64, error)
	ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error)
	RevokeShare(ctx context.Context, id int64) error
	DeleteShare(ctx context.Context, id int64) error
	UpdateSharePinLock(ctx context.Context, id int64, fails int, until *time.Time) error
	UpdateShareAppState(ctx context.Context, id int64, stateJSON string) error
	CreateNode(ctx context.Context, n *model.Node) (*model.Node, error)
	GetNode(ctx context.Context, id int64) (*model.Node, error)
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	ListAuditRecent(ctx context.Context, limit int) ([]*model.AuditEntry, error)
	UpsertSetting(ctx context.Context, key, value string) error
	CreateStorage(ctx context.Context, s *model.Storage) (*model.Storage, error)
	UpdateStorage(ctx context.Context, s *model.Storage) error
	PutAppPluginScheduleItem(ctx context.Context, it *model.AppPluginScheduleItem) error
	ClaimAppPluginScheduleItem(ctx context.Context, pluginID int64, key, owner string, now time.Time) (bool, error)
	FinishAppPluginScheduleItem(ctx context.Context, pluginID int64, key, status, jobID, errMsg string, rearmAt *time.Time) error
}

// memSink commits outputs through the local driver and CATALOGUES what it
// writes, which is the half of the real sink (handlers.AppPlugins, through
// protocolsync) that anything downstream of a commit depends on: a committed
// output is a file filex knows, with a node, and a share of it points at that
// node. Without the node row here, the registry's own "share the file this
// job is writing" path could be asserted only down to the bytes.
type memSink struct {
	drv      *local.Driver
	store    storeT
	siblings []string
	versions []string
}

func (m *memSink) CommitSibling(ctx context.Context, storageID int64, dir, name string, r io.Reader, size int64, _ *int64) (string, error) {
	rel := strings.Trim(dir+"/"+name, "/")
	rel, err := ops.UniqueDest(ctx, m.drv, rel)
	if err != nil {
		return "", err
	}
	if err := m.drv.Write(ctx, rel, r, size); err != nil {
		return "", err
	}
	m.siblings = append(m.siblings, rel)
	return rel, m.catalogue(ctx, storageID, rel)
}

func (m *memSink) CommitVersion(ctx context.Context, storageID int64, rel string, r io.Reader, size int64, _ *int64) error {
	if err := m.drv.Write(ctx, rel, r, size); err != nil {
		return err
	}
	m.versions = append(m.versions, rel)
	return m.catalogue(ctx, storageID, rel)
}

// catalogue gives the written file a node row, unless it already has one —
// which is precisely the `version` case, where the output overwrites a file
// filex already knows and the node must stay the same one.
func (m *memSink) catalogue(ctx context.Context, storageID int64, rel string) error {
	if m.store == nil {
		return nil
	}
	rel = strings.Trim(rel, "/")
	hash := pathkey.Hash(storageID, "/"+rel)
	if n, err := m.store.GetNodeByPath(ctx, storageID, hash); err == nil && n != nil {
		return nil
	}
	obj, err := m.drv.Stat(ctx, rel)
	if err != nil {
		return err
	}
	_, err = m.store.CreateNode(ctx, &model.Node{
		StorageID: storageID, Name: filepath.Base(rel), Path: "/" + rel, PathHash: hash,
		Type: model.NodeTypeFile, Size: obj.Size, Mime: obj.Mime, SyncState: model.SyncStateSynced,
	})
	return err
}

func newHarness(t *testing.T, opts func(*Options)) *harness {
	t.Helper()
	wasmfixture.Require(t, fixtureWasm)
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/main", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)
	shareSvc := share.NewService(store)
	shareSvc.AttachSecret("0123456789abcdef0123456789abcdef")
	o := Options{
		Store: store, Share: shareSvc, Dir: filepath.Join(t.TempDir(), "app-plugins"), SecretKey: "0123456789abcdef0123456789abcdef",
		StorageResolver: func(id int64) (storage.Driver, error) { return drv, nil },
	}
	if opts != nil {
		opts(&o)
	}
	reg, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	sink := &memSink{drv: drv, store: store}
	reg.SetOutputSink(sink)
	return &harness{reg: reg, store: store, drv: drv, st: st, root: root, sink: sink, share: shareSvc}
}

func echoInput(t *testing.T, mutate func(m map[string]any)) *InstallInput {
	t.Helper()
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	if mutate != nil {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		mutate(m)
		raw, _ = json.Marshal(m)
	}
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	var m Manifest
	_ = json.Unmarshal(raw, &m.Manifest)
	return &InstallInput{Manifest: raw, Wasm: bytes.NewReader(wasm), Source: "upload", Granted: m.Permissions, Lang: "en"}
}

func (h *harness) install(t *testing.T) *Installed {
	t.Helper()
	st, _, err := h.reg.Install(context.Background(), echoInput(t, nil))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	return p
}

func (h *harness) writeFile(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(h.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func (h *harness) runJob(t *testing.T, p *Installed, action string, paths []string, locale string) (*model.AppPluginJob, error) {
	t.Helper()
	pj, _ := json.Marshal(paths)
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: action,
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: "{}", Locale: locale, Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	err := h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: paths, Dest: job.ID}, nil)
	got, gerr := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, gerr)
	return got, err
}

func TestInstall_DryRunListsPermissions_ThenInstallRuns(t *testing.T) {
	h := newHarness(t, nil)
	in := echoInput(t, nil)
	in.DryRun = true
	_, dry, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, dry)
	assert.Equal(t, "echo", dry.Manifest.Name)
	assert.Len(t, dry.Permissions, len(echoInput(t, nil).Granted))
	assert.Len(t, dry.WasmSHA256, 64)
	rows, _ := h.store.ListAppPlugins(context.Background())
	assert.Empty(t, rows, "a dry run installs nothing")

	p := h.install(t)
	state, serr := p.State()
	assert.Equal(t, StateRunning, state, serr)
	assert.FileExists(t, filepath.Join(h.reg.Dir(), "echo", "plugin.wasm"))
	assert.FileExists(t, filepath.Join(h.reg.Dir(), "echo", "filex-app.json"))

	ans, err := h.reg.ActionsFor(context.Background(), false)
	require.NoError(t, err)
	keys := []string{}
	for _, a := range ans.Actions {
		keys = append(keys, a.Key)
	}
	assert.Contains(t, keys, "plugin:echo/upper")
}

func TestJob_UpperCasesInputs_WritesSiblings_KeepsState(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/a.txt", "hello")
	h.writeFile(t, "docs/b.txt", "world")

	job, err := h.runJob(t, p, "upper", []string{"docs/a.txt", "docs/b.txt"}, "tr")
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, job.Status)
	assert.Equal(t, "bitti", job.Message, "the plugin's message in the actor's locale")
	assert.Equal(t, []string{"docs/a-upper.txt", "docs/b-upper.txt"}, h.sink.siblings)
	var outs []JobOutput
	require.NoError(t, json.Unmarshal([]byte(job.OutputsJSON), &outs))
	assert.Equal(t, []JobOutput{{Path: "docs/a-upper.txt"}, {Path: "docs/b-upper.txt"}}, outs)
	got, _ := os.ReadFile(filepath.Join(h.root, "docs", "a-upper.txt"))
	assert.Equal(t, "HELLO", string(got))

	// Per-file state survived the call and is keyed by the storage path.
	v, found, err := h.store.GetAppPluginState(context.Background(), p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/docs/a.txt"), "runs")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "1", v)

	// Run again: the sibling gets a unique name, the state accumulates.
	job, err = h.runJob(t, p, "upper", []string{"docs/a.txt"}, "en")
	require.NoError(t, err)
	assert.Equal(t, "done", job.Message)
	v, _, _ = h.store.GetAppPluginState(context.Background(), p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/docs/a.txt"), "runs")
	assert.Equal(t, "1+1", v)
	assert.Len(t, h.sink.siblings, 3)
	assert.NotEqual(t, "docs/a-upper.txt", h.sink.siblings[2], "a taken name is not overwritten")

	// The spool is empty once the calls are over.
	entries, _ := os.ReadDir(filepath.Join(h.reg.Dir(), "spool"))
	assert.Empty(t, entries)
}

func TestJob_RefusesInputOverTheLimit(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxInputBytes = 4 })
	p := h.install(t)
	h.writeFile(t, "big.txt", "more than four bytes")
	job, err := h.runJob(t, p, "upper", []string{"big.txt"}, "en")
	require.Error(t, err)
	assert.Equal(t, model.AppPluginJobFailed, job.Status)
	assert.Contains(t, job.Error, "input limit")
}

func TestJob_MissingInputFails(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	job, err := h.runJob(t, p, "upper", []string{"nope.txt"}, "en")
	require.Error(t, err)
	assert.Equal(t, model.AppPluginJobFailed, job.Status)
}

func TestEngine_PathArgumentIsRefusedEvenWithoutTheEngine(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "x.txt", "x")
	job, err := h.runJob(t, p, "escape", []string{"x.txt"}, "en")
	require.NoError(t, err, "the guest asserts the host refused; a nil error means it did")
	assert.Equal(t, model.AppPluginJobOK, job.Status)
	assert.Contains(t, job.Message, "path separators")
}

func TestEngine_FfmpegRunsInsideTheScope(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "x.txt", "x")
	job, err := h.runJob(t, p, "engine", []string{"x.txt"}, "en")
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, job.Status)
	assert.Contains(t, job.Message, "ffmpeg version")
}

func TestInstall_PermissionsMustBeGrantedExactly(t *testing.T) {
	h := newHarness(t, nil)
	in := echoInput(t, nil)
	in.Granted = []string{"files:read"}
	_, _, err := h.reg.Install(context.Background(), in)
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodePermissionsIncomplete, ie.Code)
	assert.Contains(t, ie.Missing, "files:write")
	assert.Contains(t, ie.Missing, "engines:ffmpeg")
	assert.NotContains(t, ie.Missing, "files:read")
	rows, _ := h.store.ListAppPlugins(context.Background())
	assert.Empty(t, rows)
}

func TestInstall_DescribeMismatchLeavesNothingBehind(t *testing.T) {
	h := newHarness(t, nil)
	in := echoInput(t, func(m map[string]any) { m["version"] = "9.9.9" })
	_, _, err := h.reg.Install(context.Background(), in)
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeDescribeMismatch, ie.Code)
	rows, _ := h.store.ListAppPlugins(context.Background())
	assert.Empty(t, rows)
	_, statErr := os.Stat(filepath.Join(h.reg.Dir(), "echo"))
	assert.True(t, os.IsNotExist(statErr), "files removed")
	_, ok := h.reg.ByName("echo")
	assert.False(t, ok)
}

func TestInstall_NameTaken_And_Remove(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	_, _, err := h.reg.Install(context.Background(), echoInput(t, nil))
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeNameTaken, ie.Code)

	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	_, ok := h.reg.ByName("echo")
	assert.False(t, ok)
	rows, _ := h.store.ListAppPlugins(context.Background())
	assert.Empty(t, rows)
	_, statErr := os.Stat(filepath.Join(h.reg.Dir(), "echo"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestInstall_SHA256MismatchIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	in := echoInput(t, nil)
	in.SHA256 = strings.Repeat("0", 64)
	_, _, err := h.reg.Install(context.Background(), in)
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeSHA256Mismatch, ie.Code)
}

func TestInstall_DemoRefuses(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Demo = true })
	_, _, err := h.reg.Install(context.Background(), echoInput(t, nil))
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeDemo, ie.Code)
}

func TestSettings_SecretsAreSealedAndMasked(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["settings"] = []map[string]any{
			{"key": "tsa_url", "type": "string", "label": "TSA"},
			{"key": "api_key", "type": "password", "label": "Key", "secret": true},
		}
	}))
	require.NoError(t, err)
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, map[string]string{"tsa_url": "https://tsa", "api_key": "s3cret"}))
	vals, fields, err := h.reg.Settings(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Len(t, fields, 2)
	assert.Equal(t, "https://tsa", vals["tsa_url"])
	assert.Equal(t, secretMask, vals["api_key"])

	// Stored sealed, opened only for the host function.
	p, _ := h.reg.ByID(st.ID)
	raw, _ := h.reg.opts.Store.GetAppPluginSettings(context.Background(), st.ID)
	assert.True(t, strings.HasPrefix(raw["api_key"], "enc:"), raw["api_key"])
	open, err := h.reg.openSettings(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", open["api_key"])
	assert.NotContains(t, h.reg.publicSettings(context.Background(), p), "api_key")

	// Sending the mask back keeps the secret; sending a value replaces it.
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, map[string]string{"tsa_url": "https://tsa2", "api_key": secretMask}))
	open, _ = h.reg.openSettings(context.Background(), p)
	assert.Equal(t, "s3cret", open["api_key"])
	assert.Equal(t, "https://tsa2", open["tsa_url"])
}

func TestOverrides_DisableAndAdminOnlyAndApplies(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	rows, err := h.reg.Overrides(context.Background(), p.Row.ID)
	require.NoError(t, err)
	visible := 0
	for _, a := range p.Manifest.Actions {
		if !a.Hidden {
			visible++
		}
	}
	assert.Len(t, rows, visible, "one override row per menu action — hidden ones are not a person's to switch")
	assert.Nil(t, rows[0].Applies, "manifest default")

	require.NoError(t, h.reg.PutOverrides(context.Background(), p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, AdminOnly: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"md"}}},
		{ID: "engine", Enabled: false},
		{ID: "ghost", Enabled: true},
	}))
	ans, _ := h.reg.ActionsFor(context.Background(), false)
	keys := []string{}
	for _, a := range ans.Actions {
		keys = append(keys, a.Key)
	}
	assert.NotContains(t, keys, "plugin:echo/upper", "admin-only hidden from users")
	assert.NotContains(t, keys, "plugin:echo/engine", "disabled")
	assert.Contains(t, keys, "plugin:echo/escape")

	ans, _ = h.reg.ActionsFor(context.Background(), true)
	var upper *ActionRow
	for i := range ans.Actions {
		if ans.Actions[i].ID == "upper" {
			upper = &ans.Actions[i]
		}
	}
	require.NotNil(t, upper, "admins see admin-only actions")
	assert.Equal(t, []string{"md"}, upper.Applies.Ext, "override replaces the manifest rule")

	_, _, _, err = h.reg.ResolveAction(context.Background(), "echo", "upper", false)
	assert.True(t, IsCode(err, CodeRefused))
	_, _, _, err = h.reg.ResolveAction(context.Background(), "echo", "engine", true)
	assert.True(t, IsCode(err, CodeUnsupported))
}

// A hidden action is the app's machinery, not a menu row: it is not listed
// for overriding, an override sent for it is not stored, and one that is in
// the table anyway (written by hand, or before the rule) is not honoured.
// Owner, v0.43.0 sweep: "a hidden action is not a person's to switch off".
func TestOverrides_HiddenActionsAreNotAPersonsToSwitch(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	var hidden []string
	for _, a := range p.Manifest.Actions {
		if a.Hidden {
			hidden = append(hidden, a.ID)
		}
	}
	require.NotEmpty(t, hidden, "the echo fixture must carry a hidden action, or this measures nothing")

	rows, err := h.reg.Overrides(context.Background(), p.Row.ID)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotContains(t, hidden, r.ID, "a hidden action is offered as a switch")
	}

	// Sent anyway: dropped like an unknown id, the visible one kept.
	put := []OverrideRow{{ID: "upper", Enabled: false}}
	for _, id := range hidden {
		put = append(put, OverrideRow{ID: id, Enabled: false, AdminOnly: true})
	}
	require.NoError(t, h.reg.PutOverrides(context.Background(), p.Row.ID, put))
	stored, err := h.reg.opts.Store.ListAppPluginOverrides(context.Background(), p.Row.ID)
	require.NoError(t, err)
	for _, o := range stored {
		assert.NotContains(t, hidden, o.ActionID, "an override for a hidden action was stored")
	}
	_, _, _, err = h.reg.ResolveAction(context.Background(), "echo", "upper", true)
	assert.True(t, IsCode(err, CodeUnsupported), "the visible switch still works")

	// Written straight into the table: still not honoured.
	var direct []*model.AppPluginOverride
	for _, id := range hidden {
		direct = append(direct, &model.AppPluginOverride{PluginID: p.Row.ID, ActionID: id, Enabled: false, AdminOnly: true})
	}
	require.NoError(t, h.reg.opts.Store.PutAppPluginOverrides(context.Background(), p.Row.ID, direct))
	for _, id := range hidden {
		_, a, _, err := h.reg.ResolveAction(context.Background(), "echo", id, false)
		require.NoError(t, err, "hidden %s was switched off or reserved by an override", id)
		assert.Equal(t, id, a.ID)
	}
}

func TestEnableDisable_LoadsAndUnloads(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	st, err := h.reg.SetEnabled(context.Background(), p.Row.ID, false)
	require.NoError(t, err)
	assert.Equal(t, StateDisabled, st.State)
	ans, _ := h.reg.ActionsFor(context.Background(), true)
	assert.Empty(t, ans.Actions)
	st, err = h.reg.SetEnabled(context.Background(), p.Row.ID, true)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, st.State)

	// A fresh registry over the same store and dir loads it back.
	reg2, err := New(h.reg.opts)
	require.NoError(t, err)
	defer reg2.Close(context.Background())
	require.NoError(t, reg2.Load(context.Background()))
	p2, ok := reg2.ByName("echo")
	require.True(t, ok)
	state, serr := p2.State()
	assert.Equal(t, StateRunning, state, serr)
}

func TestView_HelloRoundTrip_SanitisesUnknownNodes(t *testing.T) {
	h := newHarness(t, nil)
	h.install(t)
	s, err := h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	assert.Equal(t, "Hello", s.Title.Get("en"))
	require.Len(t, s.Nodes, 1)
	assert.Equal(t, "text", s.Nodes[0].Type)
	s, err = h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "submit"})
	require.NoError(t, err)
	assert.True(t, s.Done)
}

// v3.1 — a home page's section reaches the view as `data.section`, the
// menu comes back on the surface, and a signed-in person's address reaches
// it as `context.actor.ip` (the signing app prints it only when chosen, and
// only after the signer has seen it).
func TestView_SectionAndActorIPReachTheView(t *testing.T) {
	h := newHarness(t, nil)
	h.install(t)
	actor := &model.User{ID: 7, Email: "ada@test.local", DisplayName: "Ada"}
	ctx := WithActorIP(context.Background(), "203.0.113.9")
	s, err := h.reg.ViewEvent(ctx, "echo", "wizard", 0, nil, actor, "en",
		wire.ViewEventInput{Event: "open", Data: map[string]any{"section": "b"}})
	require.NoError(t, err)
	assert.Equal(t, "b", s.Section)
	require.Len(t, s.Sections, 2, "the menu survives the host's sanitising")
	require.Len(t, s.Nodes, 1)
	txt, _ := json.Marshal(s.Nodes[0].Props)
	assert.Contains(t, string(txt), "section=b")
	assert.Contains(t, string(txt), "ip=203.0.113.9")

	// No address on the context: none invented.
	s, err = h.reg.ViewEvent(context.Background(), "echo", "wizard", 0, nil, actor, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	txt, _ = json.Marshal(s.Nodes[0].Props)
	assert.Contains(t, string(txt), "ip= ")
}

// A view is told whether its file's storage takes writes, so an app whose
// flow ENDS in a write (a signing request: output `none` now, a new version
// when the last signer answers) can refuse before it freezes the file and
// notifies anybody. 2026-09-21: such a request was "sent" on a read-only
// storage and could never complete.
func TestView_InputsSayWhetherTheStorageTakesWrites(t *testing.T) {
	h := newHarness(t, nil)
	h.install(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, "a.pdf"), []byte("%PDF-1.4"), 0o644))
	actor := &model.User{ID: 7, Email: "ada@test.local", DisplayName: "Ada"}
	open := func() string {
		s, err := h.reg.ViewEvent(context.Background(), "echo", "wizard", h.st.ID, []string{"a.pdf"}, actor, "en", wire.ViewEventInput{Event: "open"})
		require.NoError(t, err)
		txt, _ := json.Marshal(s.Nodes[0].Props)
		return string(txt)
	}
	assert.Contains(t, open(), "ro=false")
	h.st.ReadOnly = true
	require.NoError(t, h.store.(interface {
		UpdateStorage(context.Context, *model.Storage) error
	}).UpdateStorage(context.Background(), h.st))
	assert.Contains(t, open(), "ro=true")
}

// ...and so is a JOB: the signing app refuses a request on a read-only
// storage inside the request job too, for a job queued some other way than
// its wizard.
func TestJob_InputsSayWhetherTheStorageTakesWrites(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "a.pdf", "%PDF-1.4")
	run := func() string {
		pj, _ := json.Marshal([]string{"a.pdf"})
		job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "facts",
			StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: "{}", Locale: "en", Label: "x", Status: model.AppPluginJobPending}
		require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
		require.NoError(t, h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 9, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"a.pdf"}, Dest: job.ID}, nil))
		got, _ := h.store.GetAppPluginJob(context.Background(), job.ID)
		require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
		return got.Message
	}
	assert.Equal(t, "ro=false", run())
	h.st.ReadOnly = true
	require.NoError(t, h.store.(interface {
		UpdateStorage(context.Context, *model.Storage) error
	}).UpdateStorage(context.Background(), h.st))
	assert.Equal(t, "ro=true", run())
}

func TestLogs_RingAfterCursor(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	lines, next, err := h.reg.Logs(p.Row.ID, 0)
	require.NoError(t, err)
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[len(lines)-1].Msg, "loaded echo")
	again, _, _ := h.reg.Logs(p.Row.ID, next)
	assert.Empty(t, again)
}

// ⚠⚠ An install finishes whatever the client does. Measured 2026-09-21:
// a 20 MB module compiled for 29 s on a busy machine, the admin page gave
// up at 30 s, and the cancelled request context aborted the compile AND
// the cleanup — no app in the list, and every later install refused as
// "name_taken" until a restart.
func TestInstall_SurvivesTheClientLeaving(t *testing.T) {
	h := newHarness(t, nil)
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	st, _, err := h.reg.Install(gone, echoInput(t, nil))
	require.NoError(t, err, "a closed tab does not undo a good install")
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	state, msg := p.State()
	assert.Equal(t, StateRunning, state, msg)

	// And the upgrade: same module again, the client already gone.
	_, _, err = h.reg.Upgrade(gone, st.ID, echoInput(t, nil))
	require.NoError(t, err)
	p, _ = h.reg.ByID(st.ID)
	state, msg = p.State()
	assert.Equal(t, StateRunning, state, msg)
}
