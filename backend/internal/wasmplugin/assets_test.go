package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
)

// assetNet is the network, as far as asset_fetch can see it: URL → body.
// `down` makes every request fail the way an installation with no internet
// does.
type assetNet struct {
	files map[string]string
	down  bool
	hits  []string
}

func (n *assetNet) RoundTrip(r *http.Request) (*http.Response, error) {
	n.hits = append(n.hits, r.URL.String())
	if n.down {
		return nil, errors.New("dial tcp: lookup fonts.example.test: no such host")
	}
	body, ok := n.files[r.URL.String()]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func sumOf(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// runAsset queues the echo app's `asset` probe with these parameters.
func (h *harness) runAsset(t *testing.T, p *Installed, params map[string]any) string {
	t.Helper()
	h.writeFile(t, "x.txt", "x")
	pj, _ := json.Marshal([]string{"x.txt"})
	pp, _ := json.Marshal(params)
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "asset",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pp), Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	require.NoError(t, h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 3, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"x.txt"}, Dest: job.ID}, nil))
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	return got.Message
}

// asset_fetch downloads a pinned file ONCE, and every later call is served
// from the app's cache on the host with no network (2026-09-21: the signing
// app fetches Noto fonts for non-Latin text instead of carrying tens of MB).
func TestAssetFetch_DownloadsOnceThenServesTheCache(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	net := &assetNet{files: map[string]string{"https://example.test/fonts/a.ttf": "the font bytes"}}
	h.reg.SetHTTPTransport(net)
	params := map[string]any{"url": "https://example.test/fonts/a.ttf", "sha256": sumOf("the font bytes"), "max_bytes": 1024}

	assert.Equal(t, "read=14 size=14 cached=false body=the font bytes", h.runAsset(t, p, params), "read with the ordinary file ABI")
	require.Len(t, net.hits, 1)
	cached := filepath.Join(h.reg.assetDir("echo"), sumOf("the font bytes"))
	_, err := os.Stat(cached)
	require.NoError(t, err, "kept in the app's cache, named by its hash")

	// Second use: the cache, and not one more request.
	assert.Equal(t, "read=14 size=14 cached=true body=the font bytes", h.runAsset(t, p, params))
	assert.Len(t, net.hits, 1, "a cached asset never touches the network")

	// Offline, the cache still answers.
	net.down = true
	assert.Contains(t, h.runAsset(t, p, params), "cached=true")

	// ...and what is NOT cached fails the way an offline instance fails.
	other := map[string]any{"url": "https://example.test/fonts/b.ttf", "sha256": sumOf("other"), "max_bytes": 1024}
	assert.Equal(t, "refused=unavailable", h.runAsset(t, p, other))

	// Uninstall takes the downloads with it.
	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	_, err = os.Stat(h.reg.assetDir("echo"))
	assert.True(t, os.IsNotExist(err), "the cache of an uninstalled app is removed")
}

// ⚠⚠ The hash is the whole integrity story: a server — a compromised CDN —
// that answers with other bytes must not get them into the app, and nothing
// it sent may be kept for a later call to find.
func TestAssetFetch_RefusesBytesThatAreNotThePinnedOnes(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	net := &assetNet{files: map[string]string{"https://example.test/fonts/a.ttf": "TAMPERED"}}
	h.reg.SetHTTPTransport(net)
	params := map[string]any{"url": "https://example.test/fonts/a.ttf", "sha256": sumOf("the font bytes"), "max_bytes": 1024}

	assert.Equal(t, "refused=integrity", h.runAsset(t, p, params))
	entries, _ := os.ReadDir(h.reg.assetDir("echo"))
	assert.Empty(t, entries, "nothing of a refused download is kept, not even a partial file")
	lines, _, _ := h.reg.Logs(p.Row.ID, 0)
	var said bool
	for _, l := range lines {
		said = said || strings.Contains(l.Msg, "does not match its pinned sha256")
	}
	assert.True(t, said, "the refusal is said in the app's log")
}

func TestAssetFetch_TheGuards(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	net := &assetNet{files: map[string]string{
		"https://example.test/big.ttf":       strings.Repeat("x", 2048),
		"https://not-granted.test/fonts.ttf": "x",
	}}
	h.reg.SetHTTPTransport(net)
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"a host the app was not granted", map[string]any{"url": "https://not-granted.test/fonts.ttf", "sha256": sumOf("x"), "max_bytes": 10}, "refused=permission_denied"},
		{"plain http", map[string]any{"url": "http://example.test/big.ttf", "sha256": sumOf("x"), "max_bytes": 10}, "refused=invalid"},
		{"no pinned hash", map[string]any{"url": "https://example.test/big.ttf", "sha256": "", "max_bytes": 10}, "refused=invalid"},
		{"over the app's own max_bytes", map[string]any{"url": "https://example.test/big.ttf", "sha256": sumOf(strings.Repeat("x", 2048)), "max_bytes": 100}, "refused=too_large"},
		{"over the host's ceiling", map[string]any{"url": "https://example.test/big.ttf", "sha256": sumOf("x"), "max_bytes": assetMaxBytes + 1}, "refused=invalid"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, h.runAsset(t, p, c.params), c.name)
	}
	for _, hit := range net.hits {
		assert.NotContains(t, hit, "not-granted.test", "a refused host is never contacted")
	}
}

// An asset is the app's own download: reading it needs no `files:read`,
// while a user's file still does. (Asked of the host functions directly: a
// module's describe must match its manifest, so the echo app cannot be
// installed without the permission.)
func TestAssetFetch_ReadWithoutFilesRead(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.reg.SetHTTPTransport(&assetNet{files: map[string]string{"https://example.test/a.ttf": "font"}})
	bare := &Installed{Row: p.Row, Manifest: p.Manifest, Grants: NewGrants([]Permission{"http:example.test"}), logs: p.logs}
	h.writeFile(t, "x.txt", "x")
	s := h.jobScope(t, bare, h.actor(t), "x.txt")

	in, _ := json.Marshal(map[string]any{"url": "https://example.test/a.ttf", "sha256": sumOf("font"), "max_bytes": 100})
	out, err := hfAssetFetch(context.Background(), s, in)
	require.NoError(t, err)
	ref := out.(map[string]any)["ref"].(string)
	open, _ := json.Marshal(map[string]any{"ref": ref})
	got, err := hfFileOpen(context.Background(), s, open)
	require.NoError(t, err, "an asset opens without files:read")
	handle := got.(map[string]any)["handle"].(uint64)
	rd, _ := json.Marshal(map[string]any{"handle": handle, "max": 100})
	b, err := hfFileRead(context.Background(), s, rd)
	require.NoError(t, err)
	assert.Equal(t, "font", string(b[1:]))

	input, _ := json.Marshal(map[string]any{"ref": "in:0"})
	_, err = hfFileOpen(context.Background(), s, input)
	require.Error(t, err, "a user's file still needs files:read")
	assert.Equal(t, "permission_denied", asHostError(err).Code)
	for _, f := range s.Inputs() {
		assert.NotEqual(t, ref, f.Ref, "an asset is never listed as an input")
	}
}

// A few hundred MB at most per app: the least recently used go first.
func TestAssetFetch_TheCacheStaysBounded(t *testing.T) {
	h := newHarness(t, nil)
	dir := h.reg.assetDir("echo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, make([]byte, size), 0o644))
		return p
	}
	old := write(strings.Repeat("a", 64), assetCacheMax/2)
	mid := write(strings.Repeat("b", 64), assetCacheMax/2)
	fresh := write(strings.Repeat("c", 64), assetCacheMax/4)
	past := func(p string, ago int) {
		at := time.Now().Add(-time.Duration(ago) * time.Hour)
		require.NoError(t, os.Chtimes(p, at, at))
	}
	past(old, 3)
	past(mid, 2)
	h.reg.trimAssets(dir, fresh)
	_, err := os.Stat(old)
	assert.True(t, os.IsNotExist(err), "the least recently used goes first")
	_, err = os.Stat(mid)
	assert.NoError(t, err)
	_, err = os.Stat(fresh)
	assert.NoError(t, err, "never the one just stored")
}

// slowNet answers only when released: a CJK face on a slow line.
type slowNet struct {
	release chan struct{}
	body    string
	hits    atomic.Int32
}

func (n *slowNet) RoundTrip(r *http.Request) (*http.Response, error) {
	n.hits.Add(1)
	select {
	case <-n.release:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return &http.Response{StatusCode: 200, ContentLength: int64(len(n.body)), Body: io.NopCloser(strings.NewReader(n.body)), Request: r}, nil
}

// ⚠⚠ A download longer than the screen that asked for it (30 s) must not
// die with that screen: tied to the call, a slow line would restart it from
// zero at every keystroke and never finish. The call answers "still
// downloading" in time for the app to draw its screen; the download goes
// on, shared by every call that wants the same file, and a later call finds
// it on disk.
func TestAssetFetch_ASlowDownloadOutlivesTheCall(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	net := &slowNet{release: make(chan struct{}), body: "a large font"}
	h.reg.SetHTTPTransport(net)
	h.writeFile(t, "x.txt", "x")
	s := h.jobScope(t, p, h.actor(t), "x.txt")
	in, _ := json.Marshal(map[string]any{"url": "https://example.test/cjk.ttf", "sha256": sumOf("a large font"), "max_bytes": 1024})

	short := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), assetAnswerMargin+200*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := hfAssetFetch(ctx, s, in)
		assert.Less(t, time.Since(start), time.Second, "the call leaves its answer margin")
		return err
	}
	err := short()
	require.Error(t, err)
	assert.Equal(t, "timeout", asHostError(err).Code)
	assert.Contains(t, err.Error(), "still downloading")
	// A second screen while the first download runs joins it.
	require.Error(t, short())
	assert.Equal(t, int32(1), net.hits.Load(), "one download, however many calls wait for it")

	close(net.release)
	cached := filepath.Join(h.reg.assetDir("echo"), sumOf("a large font"))
	require.Eventually(t, func() bool { _, err := os.Stat(cached); return err == nil }, 5*time.Second, 20*time.Millisecond,
		"the download finished after the calls that started it had ended")
	out, err := hfAssetFetch(context.Background(), s, in)
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]any)["cached"])
	assert.Equal(t, int32(1), net.hits.Load())
}

// ⚠ Offline, a screen asks for a font at every pause in typing. The host
// tries the network once, answers the calls of the next minute with the
// same failure at once, and says the outage in the app's log ONCE — until
// the file arrives, after which a new outage is news again.
func TestAssetFetch_AFailureIsNotRetriedAtEveryCall(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	net := &assetNet{files: map[string]string{"https://example.test/a.ttf": "font"}, down: true}
	h.reg.SetHTTPTransport(net)
	params := map[string]any{"url": "https://example.test/a.ttf", "sha256": sumOf("font"), "max_bytes": 100}
	said := func() int {
		lines, _, _ := h.reg.Logs(p.Row.ID, 0)
		n := 0
		for _, l := range lines {
			if strings.Contains(l.Msg, "asset could not be fetched") {
				n++
			}
		}
		return n
	}

	for i := 0; i < 3; i++ {
		assert.Equal(t, "refused=unavailable", h.runAsset(t, p, params))
	}
	assert.Len(t, net.hits, 1, "one attempt for three calls inside a minute")
	assert.Equal(t, 1, said(), "the outage is said once")

	// A minute on, it is tried again; still down: tried, not said again.
	h.reg.assetMu.Lock()
	for _, f := range h.reg.assetFails {
		f.at = f.at.Add(-assetRetryAfter)
	}
	h.reg.assetMu.Unlock()
	assert.Equal(t, "refused=unavailable", h.runAsset(t, p, params))
	assert.Len(t, net.hits, 2)
	assert.Equal(t, 1, said())

	// Back: fetched at the next retry, and the slate is clean.
	net.down = false
	h.reg.assetMu.Lock()
	for _, f := range h.reg.assetFails {
		f.at = f.at.Add(-assetRetryAfter)
	}
	h.reg.assetMu.Unlock()
	assert.Contains(t, h.runAsset(t, p, params), "cached=false")
	h.reg.assetMu.Lock()
	assert.Empty(t, h.reg.assetFails, "a fetched asset has no failure on record")
	h.reg.assetMu.Unlock()
}

// The real network, opt-in (FILEX_TEST_NETWORK=1): a pinned file from the
// host the signing app uses comes through the guarded transport and is
// kept; the same URL under a wrong pin is refused and nothing is kept.
func TestAssetFetch_TheRealNetwork(t *testing.T) {
	if os.Getenv("FILEX_TEST_NETWORK") == "" {
		t.Skip("set FILEX_TEST_NETWORK=1 to download from fonts.gstatic.com")
	}
	const (
		u    = "https://fonts.gstatic.com/s/notosansadlam/v27/neIczCCpqp0s5pPusPamd81eMfjPonvqdbYxxpgufnv0TGk.ttf"
		pin  = "b1b63b1f761f8680ead8cf0c91908ac6463fb3199f7969333135b168ffc58f38"
		size = 85028
	)
	h := newHarness(t, nil)
	p := h.install(t)
	bare := &Installed{Row: p.Row, Manifest: p.Manifest, Grants: NewGrants([]Permission{"http:fonts.gstatic.com"}), logs: p.logs}
	h.writeFile(t, "x.txt", "x")
	s := h.jobScope(t, bare, h.actor(t), "x.txt")

	wrong, _ := json.Marshal(map[string]any{"url": u, "sha256": strings.Repeat("0", 64), "max_bytes": size})
	_, err := hfAssetFetch(context.Background(), s, wrong)
	require.Error(t, err)
	assert.Equal(t, "integrity", asHostError(err).Code, "a real file under a wrong pin")
	entries, _ := os.ReadDir(h.reg.assetDir("echo"))
	assert.Empty(t, entries, "nothing of the refused download is kept")

	right, _ := json.Marshal(map[string]any{"url": u, "sha256": pin, "max_bytes": size})
	out, err := hfAssetFetch(context.Background(), s, right)
	require.NoError(t, err)
	assert.Equal(t, int64(size), out.(map[string]any)["size"])
	assert.Equal(t, false, out.(map[string]any)["cached"])
	out, err = hfAssetFetch(context.Background(), s, right)
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]any)["cached"])
}
