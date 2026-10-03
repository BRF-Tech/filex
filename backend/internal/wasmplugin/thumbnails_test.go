package wasmplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Thumbnails an app draws (thumbnails.go, docs/thumbnails.md) ──────────

// thumbManifest is the echo fixture's manifest with a `thumbnails` block.
func thumbManifest(t *testing.T, exts ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	if len(exts) == 0 {
		exts = []string{"jar"}
	}
	m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": exts}}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

// installThumbs installs echo as an app that draws thumbnails of exts.
func installThumbs(t *testing.T, h *harness, exts ...string) *Installed {
	t.Helper()
	raw := thumbManifest(t, exts...)
	m, err := ParseManifest(raw)
	require.NoError(t, err)
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	st, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: raw, Wasm: bytes.NewReader(wasm), Source: "upload", Granted: permStrings(m.Perms), Lang: "en",
	})
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	return p
}

func draw(t *testing.T, h *harness, name string, body []byte) ([]byte, error) {
	t.Helper()
	return h.reg.DrawThumbnail(context.Background(), "echo", assoc.DrawRequest{
		NodeID: 42, StorageID: h.st.ID, Path: "/docs/" + name, Name: name, Size: int64(len(body)), Body: bytes.NewReader(body),
	})
}

// readCounter counts the bytes read through it.
type readCounter struct {
	r io.Reader
	n int
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func drawError(t *testing.T, err error) *assoc.DrawError {
	t.Helper()
	var de *assoc.DrawError
	require.True(t, errors.As(err, &de), "a DrawError, got %v", err)
	return de
}

func TestThumbManifest_EachKindIsAPermission(t *testing.T) {
	m, err := ParseManifest(thumbManifest(t, "jar", "apk"))
	require.NoError(t, err)
	g := NewGrants(m.Perms)
	assert.True(t, g.Has("thumbnail:.jar"))
	assert.True(t, g.Has("thumbnail:.apk"))
	assert.True(t, m.NeedsModule())
	assert.False(t, m.IsLanguagePack())
	assert.NotEqual(t, "thumbnail:.jar", Permission("thumbnail:.jar").Label("en"), "the review says it in words")
	assert.Contains(t, Permission("thumbnail:.jar").Label("tr"), "küçük resim")
}

func TestThumbManifest_Refusals(t *testing.T) {
	base := func(mut func(m map[string]any)) []byte {
		raw, err := os.ReadFile("testdata/echo/manifest.json")
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		mut(m)
		out, _ := json.Marshal(m)
		return out
	}
	for name, mut := range map[string]func(m map[string]any){
		"every file": func(m map[string]any) { m["thumbnails"] = map[string]any{"applies": map[string]any{}} },
		"*/*": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"mime": []string{"*/*"}}}
		},
		"folders": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"kind": "dir", "ext": []string{"x"}}}
		},
		"state": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": []string{"x"}, "state": []string{"k"}}}
		},
		"multi": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": []string{"x"}, "multi": true}}
		},
		"bad ext": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": []string{"a b"}}}
		},
		"named by hand": func(m map[string]any) { m["permissions"] = append(m["permissions"].([]any), "thumbnail:.jar") },
		"unknown fields": func(m map[string]any) {
			m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": []string{"x"}}, "max": 3}
		},
	} {
		_, err := ParseManifest(base(mut))
		assert.Error(t, err, name)
	}
}

// An app that says it draws thumbnails and has no export is refused at
// install, not at the first file.
func TestThumbExport_IsCheckedAtLoad(t *testing.T) {
	m, err := ParseManifest(thumbManifest(t))
	require.NoError(t, err)
	err = checkThumbnailExport(context.Background(), m, probeExports{})
	assert.True(t, IsCode(err, CodeRefused), "%v", err)
	assert.NoError(t, checkThumbnailExport(context.Background(), m, probeExports{"thumbnail": true}))
	plain, err := ParseManifest(thumbManifest(t))
	require.NoError(t, err)
	plain.Thumbnails = nil
	assert.NoError(t, checkThumbnailExport(context.Background(), plain, probeExports{}))
}

type probeExports map[string]bool

func (p probeExports) HasExport(_ context.Context, name string) (bool, error) { return p[name], nil }

// An upgrade that draws one more kind asks for one more permission.
func TestThumbUpgrade_OneMoreKindIsANewGrant(t *testing.T) {
	old, err := ParseManifest(thumbManifest(t, "jar"))
	require.NoError(t, err)
	next, err := ParseManifest(thumbManifest(t, "jar", "apk"))
	require.NoError(t, err)
	u := upgradeOf(&Installed{Row: &model.AppPlugin{Version: "0.0.1"}, Manifest: old, Grants: NewGrants(old.Perms), Perms: old.Perms}, next)
	assert.Equal(t, []string{"thumbnail:.apk"}, u.Added)
}

func TestDrawThumbnail_DrawsFromTheBytesItIsHanded(t *testing.T) {
	h := newHarness(t, nil)
	installThumbs(t, h, "jar")
	out, err := draw(t, h, "lib.jar", []byte{0xC8, 1, 2, 3})
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	r, _, _, _ := img.At(1, 1).RGBA()
	assert.Equal(t, uint32(0xC8), r>>8, "the app read the file it was handed")

	hs := h.reg.ThumbnailHandlers()
	require.Len(t, hs, 1)
	assert.Equal(t, "app:echo", hs[0].ID)
	assert.Equal(t, []string{"jar"}, hs[0].Ext)
}

// ⚠⚠ The sandbox: a thumbnail call reads the one file it was handed and
// nothing else, whatever the app was granted (echo holds files:write, state,
// users:lookup, notify, mail, files:lock, engines:ffmpeg, public_pages).
func TestDrawThumbnail_TheCallReachesNothingElse(t *testing.T) {
	h := newHarness(t, nil)
	installThumbs(t, h, "jar")
	_, err := draw(t, h, "probe.jar", []byte("12345"))
	de := drawError(t, err)
	assert.Equal(t, assoc.DrawFailed, de.Code)
	msg := de.Error()
	for _, want := range []string{
		"read=ok", "bytes=5", "other=not_found",
		"create=permission_denied", "state=permission_denied", "users=permission_denied",
		"notify=permission_denied", "mail=permission_denied", "lock=permission_denied",
		"engine=permission_denied", "share=permission_denied",
	} {
		assert.Contains(t, msg, want)
	}
	assert.True(t, strings.HasSuffix(msg, ",path="), "the app is never told where the file lives: %s", msg)
}

// Without an http: grant for the host nothing leaves; with one, the call
// that used it leaves an audit row.
func TestDrawThumbnail_NetworkOnlyByGrant_AndAudited(t *testing.T) {
	h := newHarness(t, nil)
	installThumbs(t, h, "jar")
	tr := &cannedTransport{}
	h.reg.SetHTTPTransport(tr)

	_, err := draw(t, h, "other.jar", []byte("x"))
	de := drawError(t, err)
	assert.Contains(t, de.Error(), "permission_denied")
	assert.Empty(t, tr.seen, "not granted: no request")
	rows, err := h.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, AuditActionThumbnailSent, r.Action, "nothing was sent")
	}

	out, err := draw(t, h, "net.jar", []byte{7})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.Len(t, tr.seen, 1)
	assert.Equal(t, "example.test", tr.seen[0].URL.Hostname())
	assert.Equal(t, http.MethodPost, tr.seen[0].Method)
	body, _ := io.ReadAll(tr.seen[0].Body)
	assert.Equal(t, "net.jar", string(body))
	rows, err = h.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var sent int
	for _, r := range rows {
		if r.Action == AuditActionThumbnailSent {
			sent++
			assert.Equal(t, "/docs/net.jar", r.TargetID)
			assert.Equal(t, "echo", r.Metadata["plugin"])
			assert.Contains(t, r.Metadata["hosts"], "example.test")
		}
	}
	assert.Equal(t, 1, sent)
}

func TestDrawThumbnail_LimitsTheAdministratorSets(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	p := installThumbs(t, h, "jar")

	ans, err := h.reg.ThumbLimits(ctx, p.Row.ID)
	require.NoError(t, err)
	assert.Equal(t, ThumbLimits{MaxInputMB: 32, TimeoutS: 10, MemoryMB: 64, Concurrency: 2}, ans.Values)

	_, err = h.reg.PutThumbLimits(ctx, p.Row.ID, ThumbLimits{TimeoutS: 61})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeOutOfRange, ie.Code)
	assert.Equal(t, "timeout_s", ie.Where)

	_, err = h.reg.PutThumbLimits(ctx, p.Row.ID, ThumbLimits{MaxInputMB: 1, TimeoutS: 1, MemoryMB: 32, Concurrency: 1})
	require.NoError(t, err)

	// Over the size limit: not sent at all - not even read.
	big := &readCounter{r: bytes.NewReader(make([]byte, 2<<20))}
	_, err = h.reg.DrawThumbnail(ctx, "echo", assoc.DrawRequest{Name: "big.jar", Size: 2 << 20, Body: big})
	de := drawError(t, err)
	assert.Equal(t, assoc.DrawTooLarge, de.Code)
	assert.Equal(t, int64(1<<20), de.Limit)
	assert.Zero(t, big.n, "a file the catalogue puts over the limit is refused before a byte of it is read")

	// A catalogue size behind the file: caught while spooling.
	_, err = h.reg.DrawThumbnail(ctx, "echo", assoc.DrawRequest{Name: "liar.jar", Size: 10, Body: bytes.NewReader(make([]byte, 2<<20))})
	assert.Equal(t, assoc.DrawTooLarge, drawError(t, err).Code)

	// Over the time limit.
	start := time.Now()
	_, err = draw(t, h, "slow.jar", []byte("x"))
	de = drawError(t, err)
	assert.Equal(t, assoc.DrawTimeout, de.Code)
	assert.Equal(t, int64(1000), de.Limit)
	assert.Less(t, time.Since(start), 8*time.Second, "the administrator's 1 s, not the call's default 15 s")

	// Its own memory: a module compiled with 32 MB draws as well.
	_, err = draw(t, h, "ok.jar", []byte{1})
	require.NoError(t, err)

	maxBytes, timeout, ok := h.reg.AppLimits("echo")
	assert.True(t, ok)
	assert.Equal(t, int64(1<<20), maxBytes)
	assert.Equal(t, time.Second, timeout)
}

func TestDrawThumbnail_OnlyTheKindsItWasGranted_AndOnlyWhileOn(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	p := installThumbs(t, h, "jar")
	_, err := draw(t, h, "notes.txt", []byte("x"))
	assert.Equal(t, assoc.DrawFailed, drawError(t, err).Code)

	_, err = h.reg.SetEnabled(ctx, p.Row.ID, false)
	require.NoError(t, err)
	_, err = draw(t, h, "lib.jar", []byte("x"))
	assert.Equal(t, assoc.DrawFailed, drawError(t, err).Code)
	assert.Empty(t, h.reg.ThumbnailHandlers(), "an app that is off draws nothing")
}

func TestDrawThumbnail_AnswersThePipelineChecks(t *testing.T) {
	h := newHarness(t, nil)
	installThumbs(t, h, "jar")
	out, err := draw(t, h, "huge.jar", []byte("x"))
	require.NoError(t, err, "the registry hands the header on; the pipeline refuses it before decoding")
	assert.True(t, bytes.HasPrefix(out, []byte{0x89, 'P', 'N', 'G'}))
	_, err = draw(t, h, "empty.jar", []byte("x"))
	assert.Equal(t, assoc.DrawFailed, drawError(t, err).Code)
}

func TestThumbLimits_DemoAndNoThumbnails(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	p := h.install(t)
	_, err := h.reg.ThumbLimits(ctx, p.Row.ID)
	assert.Error(t, err, "an app that draws no thumbnails has no limits to set")

	d := newHarness(t, func(o *Options) { o.Demo = false })
	q := installThumbs(t, d, "jar")
	d.reg.opts.Demo = true
	_, err = d.reg.PutThumbLimits(ctx, q.Row.ID, ThumbLimits{TimeoutS: 5})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeDemo, ie.Code)
}

func TestManifestHandlers(t *testing.T) {
	m, err := ParseManifest(thumbManifest(t, "jar"))
	require.NoError(t, err)
	open, thumb := ManifestHandlers(m)
	assert.Empty(t, open)
	require.Len(t, thumb, 1)
	assert.Equal(t, "app:echo", thumb[0].ID)
	assert.Equal(t, "0.0.1", thumb[0].Version)
	assert.Equal(t, []string{"jar"}, thumb[0].Ext)
}
