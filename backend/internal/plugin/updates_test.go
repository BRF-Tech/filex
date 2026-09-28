package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// ── Updates (M5): a storage plugin follows the source it names ──────────

// feedServer is a source: filex-storage.json and the builds it names.
type feedServer struct {
	*httptest.Server
	mu    sync.Mutex
	feed  map[string]any
	files map[string][]byte
	hits  map[string]int
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	f := &feedServer{files: map[string][]byte{}, hits: map[string]int{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		if r.URL.Path == "/filex-storage.json" {
			_ = json.NewEncoder(w).Encode(f.feed)
			return
		}
		if b, ok := f.files[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *feedServer) set(feed map[string]any) {
	f.mu.Lock()
	f.feed = feed
	f.mu.Unlock()
}

func (f *feedServer) source() string { return f.URL + "/filex-storage.json" }

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// newUpdateManager is a manager whose downloads reach the feed server (the
// test client lifts the private-address guard, as Options.HTTP documents).
func newUpdateManager(t *testing.T, f *feedServer, plat string) (*plugin.Manager, db.Store, string) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	dir := t.TempDir()
	m, err := plugin.New(plugin.Options{Store: store, Dir: dir, SecretKey: "test-secret-key", HTTP: f.Client(), Platform: plat})
	require.NoError(t, err)
	t.Cleanup(m.Shutdown)
	return m, store, dir
}

func TestFeedURL_ReadsARepositoryOrAnAddressAndNothingElse(t *testing.T) {
	for in, want := range map[string]string{
		"acme/myfs":                                   "https://github.com/acme/myfs/releases/latest/download/filex-storage.json",
		"https://github.com/acme/myfs.git":            "https://github.com/acme/myfs/releases/latest/download/filex-storage.json",
		"https://github.com/acme/myfs/":               "https://github.com/acme/myfs/releases/latest/download/filex-storage.json",
		"https://example.com/myfs/filex-storage.json": "https://example.com/myfs/filex-storage.json",
		"https://github.com/acme/myfs/releases/download/v1.0.0/filex-storage.json": "https://github.com/acme/myfs/releases/download/v1.0.0/filex-storage.json",
		"": "",
	} {
		got, err := plugin.FeedURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"http://example.com/f.json", "ftp://x/y", "acme", "https://user:pw@example.com/f.json", "file:///etc/passwd", "acme/myfs/extra"} {
		_, err := plugin.FeedURL(bad)
		assert.Error(t, err, bad)
	}
}

// ⚠⚠ Owner's rule: nothing updates itself. The check SAYS a newer build for
// this platform is there — once per version to the bell — and installs
// nothing; what the source cannot deliver is said, not guessed.
func TestUpdates_TheCheckSaysAndInstallsNothing(t *testing.T) {
	f := newFeedServer(t)
	m, store, _ := newUpdateManager(t, f, "testos/testarch")
	ctx := context.Background()
	row, err := store.CreatePlugin(ctx, &model.Plugin{
		Name: "myfs", Kind: model.PluginKindBinary, Binary: "myfs", SHA256: strings.Repeat("a", 64),
		Enabled: false, Version: "1.0.0", Driver: "myfs",
	})
	require.NoError(t, err)
	require.NoError(t, m.Load(ctx))
	var told []string
	var judged []string
	m.SetUpdateHooks(plugin.UpdateHooks{
		Range: func(rng string) (bool, string, string, error) {
			judged = append(judged, rng)
			return rng != ">=9.0.0", rng, "0.48.0", nil
		},
		Announce: func(_ context.Context, name, version, _ string) { told = append(told, name+"@"+version) },
	})

	st, err := m.SetSource(ctx, row.ID, f.source())
	require.NoError(t, err)
	assert.Equal(t, f.source(), st.Source)

	build := []byte("new build")
	f.files["/myfs-testos"] = build
	feed := func(version, filex string, bins map[string]any) map[string]any {
		return map[string]any{"name": "myfs", "version": version, "filex": filex, "notes": "Faster listings.", "binaries": bins}
	}
	ok := map[string]any{"testos/testarch": map[string]any{"url": f.URL + "/myfs-testos", "sha256": sumOf(build)}}

	f.set(feed("1.1.0", ">=0.47.0", ok))
	for i := 0; i < 2; i++ {
		rep, err := m.CheckUpdates(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, rep.Checked)
		assert.Equal(t, []string{"myfs"}, rep.Available)
	}
	st, err = m.Get(ctx, row.ID)
	require.NoError(t, err)
	require.NotNil(t, st.Update)
	assert.Equal(t, plugin.UpdateAvailable, st.Update.Status)
	assert.Equal(t, "1.1.0", st.Update.Version)
	assert.Equal(t, "Faster listings.", st.Update.Notes)
	assert.Equal(t, sumOf(build), st.Update.SHA256)
	assert.Equal(t, "testos/testarch", st.Update.Platform)
	assert.Equal(t, []string{"myfs@1.1.0"}, told, "told once, not every day")
	assert.Equal(t, "1.0.0", st.Version, "nothing moved")
	assert.Equal(t, strings.Repeat("a", 64), st.SHA256, "nothing installed")
	assert.Zero(t, f.hits["/myfs-testos"], "the build is not even downloaded by the check")
	assert.Contains(t, judged, ">=0.47.0", "the range is judged by the apps' grammar")

	cases := []struct {
		name   string
		feed   map[string]any
		status string
		msg    string
	}{
		{"a pre-release is never taken", feed("1.2.0-rc.1", "", ok), plugin.UpdateCurrent, ""},
		{"an older version is not newer", feed("0.9.0", "", ok), plugin.UpdateCurrent, ""},
		{"a range that leaves this filex out", feed("2.0.0", ">=9.0.0", ok), plugin.UpdateIncompatible, ""},
		{"no build for this platform", feed("1.1.0", "", map[string]any{"linux/riscv64": ok["testos/testarch"]}), plugin.UpdateCheckFailed, "no build for testos/testarch"},
		{"a build without its hash", feed("1.1.0", "", map[string]any{"testos/testarch": map[string]any{"url": f.URL + "/myfs-testos"}}), plugin.UpdateCheckFailed, "sha256"},
		{"a feed for another plugin", map[string]any{"name": "other", "version": "3.0.0", "binaries": ok}, plugin.UpdateCheckFailed, `describes "other"`},
	}
	for _, c := range cases {
		f.set(c.feed)
		_, err := m.CheckUpdates(ctx)
		require.NoError(t, err, c.name)
		st, err := m.Get(ctx, row.ID)
		require.NoError(t, err)
		require.NotNil(t, st.Update, c.name)
		assert.Equal(t, c.status, st.Update.Status, c.name)
		if c.msg != "" {
			assert.Contains(t, st.Update.Error, c.msg, c.name)
		}
	}
	assert.Equal(t, []string{"myfs@1.1.0"}, told, "nothing else was announced")

	// Clearing the source stops the checks.
	st, err = m.SetSource(ctx, row.ID, "")
	require.NoError(t, err)
	assert.Empty(t, st.Source)
	assert.Nil(t, st.Update)
	rep, err := m.CheckUpdates(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.Checked)

	_, err = m.SetSource(ctx, row.ID, "http://example.com/filex-storage.json")
	assert.Error(t, err, "a source is https")
}

// A remote plugin is upgraded where it runs: it takes no source.
func TestUpdates_ARemotePluginHasNoSource(t *testing.T) {
	fp := newFakePlugin("acme", fullCaps())
	defer fp.Close()
	f := newFeedServer(t)
	m, _, _ := newUpdateManager(t, f, "")
	require.NoError(t, m.Load(context.Background()))
	st, err := m.InstallRemote(context.Background(), "acme", fp.URL(), "test-token")
	require.NoError(t, err)
	_, err = m.SetSource(context.Background(), st.ID, f.source())
	assert.ErrorContains(t, err, "remote")
	_, err = m.UpgradeFromSource(context.Background(), st.ID)
	assert.ErrorContains(t, err, "remote")
}

// ⭐ The whole path with a real binary: installed FROM its source (the build
// for this platform, held to the feed's hash, the source kept), then an
// administrator's "Review update" — refused while the bytes are not the ones
// the feed names (nothing stopped, nothing swapped), refused when there is
// nothing newer, and installed through the ordinary upgrade when they are.
func TestUpdates_InstallFromSourceAndUpgradeHoldTheBytesToTheFeed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildExamplePlugin(t)
	build, err := os.ReadFile(bin)
	require.NoError(t, err)
	f := newFeedServer(t)
	plat := runtime.GOOS + "/" + runtime.GOARCH
	m, _, dir := newUpdateManager(t, f, plat)
	ctx := context.Background()
	require.NoError(t, m.Load(ctx))
	name := filepath.Base(bin)
	f.files["/"+name] = build
	bins := map[string]any{plat: map[string]any{"url": f.URL + "/" + name, "sha256": sumOf(build)}}
	f.set(map[string]any{"name": "memfs", "version": "1.0.0", "binaries": bins})

	st, err := m.InstallFromSource(ctx, "memfs", f.source())
	require.NoError(t, err)
	assert.Equal(t, f.source(), st.Source, "the source is kept for the daily check")
	st = waitState(t, m, st.ID, plugin.StateRunning)
	assert.Equal(t, sumOf(build), st.SHA256)

	_, err = m.UpgradeFromSource(ctx, st.ID)
	assert.ErrorContains(t, err, "up to date")

	// A newer version whose bytes are not the ones the feed names.
	f.set(map[string]any{"name": "memfs", "version": "1.1.0", "binaries": map[string]any{
		plat: map[string]any{"url": f.URL + "/" + name, "sha256": strings.Repeat("0", 64)},
	}})
	got, err := m.UpgradeFromSource(ctx, st.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256 mismatch")
	require.NotNil(t, got)
	assert.Equal(t, plugin.StateRunning, got.State, "nothing was stopped")
	_, statErr := os.Stat(filepath.Join(dir, "memfs", name+".upgrade"))
	assert.True(t, os.IsNotExist(statErr), "nothing staged is left behind")

	// The right bytes: the ordinary upgrade (signature, conformance, swap).
	f.set(map[string]any{"name": "memfs", "version": "1.1.0", "binaries": bins})
	got, err = m.UpgradeFromSource(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, plugin.StateRunning, got.State)
	assert.Equal(t, sumOf(build), got.SHA256)
}
