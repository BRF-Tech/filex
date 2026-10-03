package thumb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Thumbnails drawn by apps: the chain (docs/thumbnails.md) ─────────────

// rulesMem is the Default apps table in memory.
type rulesMem struct {
	mu   sync.Mutex
	rows map[string]*model.FileAssociation
}

func (m *rulesMem) ListFileAssociations(context.Context) ([]*model.FileAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*model.FileAssociation{}
	for _, r := range m.rows {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (m *rulesMem) PutFileAssociation(_ context.Context, a *model.FileAssociation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *a
	m.rows[a.Capability+"/"+a.Ext] = &cp
	return nil
}

func (m *rulesMem) DeleteFileAssociation(_ context.Context, c, e string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[c+"/"+e]
	delete(m.rows, c+"/"+e)
	return ok, nil
}

// fakeApp is an app that draws: what it answers, and what it was handed.
type fakeApp struct {
	name, version string
	ext           []string
	mime          []string
	draw          func(req assoc.DrawRequest, body []byte) ([]byte, error)
	maxBytes      int64
	timeout       time.Duration

	mu    sync.Mutex
	calls []assoc.DrawRequest
	seen  [][]byte
}

// fakeApps is the pipeline's AppThumbs over a real assoc.Service.
type fakeApps struct {
	svc  *assoc.Service
	apps map[string]*fakeApp
}

func (f *fakeApps) OpenHandlers() []assoc.AppHandler { return nil }
func (f *fakeApps) ThumbnailHandlers() []assoc.AppHandler {
	var out []assoc.AppHandler
	for _, a := range f.apps {
		out = append(out, assoc.AppHandler{Handler: assoc.Handler{ID: assoc.ThumbID(a.name), App: a.name, Version: a.version}, Ext: a.ext, Mime: a.mime})
	}
	return out
}

func (f *fakeApps) ThumbChain(ctx context.Context, name, mime string) assoc.Chain {
	return f.svc.Chain(ctx, assoc.CapThumbnail, name, mime)
}

func (f *fakeApps) DrawThumbnail(_ context.Context, h assoc.Handler, req assoc.DrawRequest) ([]byte, error) {
	a := f.apps[h.App]
	body, _ := io.ReadAll(req.Body)
	a.mu.Lock()
	a.calls = append(a.calls, req)
	a.seen = append(a.seen, body)
	a.mu.Unlock()
	if a.maxBytes > 0 && req.Size > a.maxBytes {
		return nil, &assoc.DrawError{App: a.name, Code: assoc.DrawTooLarge, Limit: a.maxBytes}
	}
	return a.draw(req, body)
}

func (f *fakeApps) AppLimits(app string) (int64, time.Duration, bool) {
	a, ok := f.apps[app]
	if !ok {
		return 0, 0, false
	}
	return a.maxBytes, a.timeout, true
}

func (a *fakeApp) called() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func pngOf(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// pngClaiming is a PNG signature and an IHDR that claims w x h pixels.
func pngClaiming(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		c := crc32.NewIEEE()
		c.Write([]byte(typ))
		c.Write(data)
		_ = binary.Write(&b, binary.BigEndian, c.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

type appsFixture struct {
	*archiveFixture
	apps  *fakeApps
	rules *rulesMem
}

// newAppsFixture: a pipeline whose chain is decided by a real assoc.Service,
// with the given apps; filex's own drawer is in it for the kinds it draws.
func newAppsFixture(t *testing.T, apps ...*fakeApp) *appsFixture {
	t.Helper()
	f := newArchiveFixture(t, nil)
	rules := &rulesMem{rows: map[string]*model.FileAssociation{}}
	svc := assoc.New(rules)
	fa := &fakeApps{svc: svc, apps: map[string]*fakeApp{}}
	for _, a := range apps {
		fa.apps[a.name] = a
	}
	svc.SetSource(fa)
	svc.SetBuiltinThumb(BuiltinDraws)
	f.p.AttachApps(fa)
	return &appsFixture{archiveFixture: f, apps: fa, rules: rules}
}

func (f *appsFixture) rule(t *testing.T, ext string, r assoc.Rule) {
	t.Helper()
	_, err := f.apps.svc.Put(context.Background(), assoc.CapThumbnail, ext, r, nil)
	require.NoError(t, err)
}

func drawsPNG(t *testing.T) func(assoc.DrawRequest, []byte) ([]byte, error) {
	return func(assoc.DrawRequest, []byte) ([]byte, error) {
		return pngOf(t, 8, 8, color.RGBA{0x10, 0x80, 0x10, 0xFF}), nil
	}
}

func fails(assoc.DrawRequest, []byte) ([]byte, error) { return nil, errors.New("cannot read it") }

func greenPixel(t *testing.T, f *appsFixture, id int64) bool {
	t.Helper()
	b, err := os.ReadFile(f.p.CachePath(id))
	require.NoError(t, err)
	img, err := jpeg.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	r, g, bl, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	return g>>8 > 0x60 && r>>8 < 0x40 && bl>>8 < 0x40
}

func TestAppChain_TheFirstThatDrawsWins(t *testing.T) {
	ctx := context.Background()
	pk := &fakeApp{name: "pkglist", version: "0.1.0", ext: []string{"jar"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, pk)

	n := f.put("lib.jar", []byte("PK\x03\x04 not really"))
	require.NoError(t, f.p.GenerateThumb(ctx, n))
	row := f.row(n.ID)
	assert.Equal(t, "ready", row.State)
	assert.Equal(t, "app:pkglist@0.1.0", row.Generator)
	assert.Equal(t, []Attempt{{H: "app:pkglist@0.1.0", R: "ok"}}, ParseAttempts(row.Attempts))
	assert.True(t, greenPixel(t, f, n.ID), "filex wrote its own JPEG of the app's picture")
	require.Equal(t, 1, pk.called())
	assert.Equal(t, "lib.jar", pk.calls[0].Name)
	assert.Equal(t, []byte("PK\x03\x04 not really"), pk.seen[0], "the app is handed the file's bytes")
}

func TestAppChain_BuiltinFirstByDefault_AppOnlyAsFallback(t *testing.T) {
	ctx := context.Background()
	alt := &fakeApp{name: "pngplus", version: "1", mime: []string{"image/*"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, alt)

	good := f.put("ok.png", pngOf(t, 4, 4, color.RGBA{0xFF, 0, 0, 0xFF}))
	good.Mime = "image/png"
	require.NoError(t, f.p.GenerateThumb(ctx, good))
	row := f.row(good.ID)
	assert.Equal(t, "builtin", row.Generator)
	assert.Zero(t, alt.called(), "filex drew it: the app was not asked")

	broken := f.put("broken.png", []byte("not a png at all"))
	broken.Mime = "image/png"
	require.NoError(t, f.p.GenerateThumb(ctx, broken), "the fallback drew it")
	row = f.row(broken.ID)
	assert.Equal(t, "ready", row.State)
	assert.Equal(t, "app:pngplus@1", row.Generator)
	tr := ParseAttempts(row.Attempts)
	require.Len(t, tr, 2)
	assert.Equal(t, "builtin", tr[0].H)
	assert.Contains(t, tr[0].R, "decode")
	assert.Equal(t, Attempt{H: "app:pngplus@1", R: "ok"}, tr[1])
}

func TestAppChain_AdministratorsOrderAndOff(t *testing.T) {
	ctx := context.Background()
	alt := &fakeApp{name: "pngplus", version: "1", mime: []string{"image/*"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, alt)
	f.rule(t, "png", assoc.Rule{Order: []string{"app:pngplus", "builtin"}})

	n := f.put("first.png", pngOf(t, 4, 4, color.RGBA{0xFF, 0, 0, 0xFF}))
	n.Mime = "image/png"
	require.NoError(t, f.p.GenerateThumb(ctx, n))
	assert.Equal(t, "app:pngplus@1", f.row(n.ID).Generator, "the app first, by the rule")

	f.rule(t, "png", assoc.Rule{Off: []string{"app:pngplus"}})
	m := f.put("second.png", []byte("broken"))
	m.Mime = "image/png"
	err := f.p.GenerateThumb(ctx, m)
	require.Error(t, err, "filex failed, and the app is switched off: nobody else is asked")
	assert.Equal(t, 1, alt.called())
	assert.Equal(t, "failed", f.row(m.ID).State)

	f.rule(t, "png", assoc.Rule{Off: []string{"app:pngplus", "builtin"}})
	k := f.put("third.png", pngOf(t, 4, 4, color.RGBA{0xFF, 0, 0, 0xFF}))
	k.Mime = "image/png"
	require.ErrorIs(t, f.p.GenerateThumb(ctx, k), ErrSkipped)
	row := f.row(k.ID)
	assert.Equal(t, "skipped", row.State)
	assert.Equal(t, SkipNoHandler, row.Error)
	assert.Equal(t, "[]", row.Attempts)
}

func TestAppChain_NobodyDrewIt_TheFirstHandlersReasonIsTheRows(t *testing.T) {
	ctx := context.Background()
	a := &fakeApp{name: "aaa", version: "1", ext: []string{"jar"}, draw: fails}
	b := &fakeApp{name: "bbb", version: "2", ext: []string{"jar"}, draw: fails, maxBytes: 4}
	f := newAppsFixture(t, a, b)
	n := f.put("lib.jar", []byte("0123456789"))
	require.ErrorIs(t, f.p.GenerateThumb(ctx, n), ErrSkipped)
	row := f.row(n.ID)
	assert.Equal(t, "skipped", row.State)
	assert.Equal(t, "app_failed:aaa", row.Error)
	assert.Equal(t, []Attempt{{H: "app:aaa@1", R: "app_failed:aaa"}, {H: "app:bbb@2", R: "app_too_large:bbb:4"}}, ParseAttempts(row.Attempts))
	assert.Empty(t, row.Generator)
}

func TestAppChain_NobodyDrawsTheKind_ThePlaceholderAsBefore(t *testing.T) {
	ctx := context.Background()
	f := newAppsFixture(t, &fakeApp{name: "pkglist", version: "1", ext: []string{"jar"}, draw: drawsPNG(t)})
	n := f.put("model.stl", []byte("solid x"))
	require.NoError(t, f.p.GenerateThumb(ctx, n))
	row := f.row(n.ID)
	assert.Equal(t, "ready", row.State)
	assert.Empty(t, row.Attempts, "the placeholder card is nobody's")
}

// ⚠ Security: ciphertext never reaches an app - not by folder, not by name,
// not by content.
func TestAppChain_EncryptedFilesNeverReachAnApp(t *testing.T) {
	ctx := context.Background()
	pk := &fakeApp{name: "pkglist", version: "1", ext: []string{"jar", "fxe"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, pk)

	sniffed := f.put("secret.jar", append([]byte(e2e.MagicPrefix), []byte("ciphertext")...))
	require.ErrorIs(t, f.p.GenerateThumb(ctx, sniffed), ErrSkipped)
	assert.Equal(t, "e2e-encrypted content", f.row(sniffed.ID).Error)
	assert.Zero(t, pk.called(), "the content sniff caught it before the app")
}

// The answer is checked before a pixel of it is decoded.
func TestAppChain_AnAnswerFilexRefuses(t *testing.T) {
	ctx := context.Background()
	for name, answer := range map[string][]byte{
		"huge":  pngClaiming(5000, 5000),
		"wide":  pngOf(t, wire.ThumbnailMaxPixels+1, 1, color.RGBA{1, 2, 3, 255}),
		"gif":   gifOf(t),
		"empty": {},
		"heavy": append(pngOf(t, 2, 2, color.RGBA{1, 2, 3, 255}), make([]byte, wire.ThumbnailMaxOutputBytes)...),
		"junk":  []byte("<svg/>"),
	} {
		t.Run(name, func(t *testing.T) {
			answer := answer
			a := &fakeApp{name: "odd", version: "1", ext: []string{"jar"}, draw: func(assoc.DrawRequest, []byte) ([]byte, error) { return answer, nil }}
			f := newAppsFixture(t, a)
			n := f.put("x.jar", []byte("x"))
			require.ErrorIs(t, f.p.GenerateThumb(ctx, n), ErrSkipped)
			assert.Equal(t, "app_failed:odd", f.row(n.ID).Error)
			_, err := os.Stat(f.p.CachePath(n.ID))
			assert.True(t, os.IsNotExist(err), "nothing was written")
		})
	}
}

func gifOf(t *testing.T) []byte {
	var buf bytes.Buffer
	require.NoError(t, gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.Black, color.White}), nil))
	return buf.Bytes()
}

// ── freshness follows the chain ──────────────────────────────────────────

func TestFreshness_TheChainUpToWhoDrewIt(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	alt := &fakeApp{name: "pngplus", version: "1", mime: []string{"image/*"}, draw: drawsPNG(t)}
	other := &fakeApp{name: "zzz", version: "5", mime: []string{"image/*"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, alt, other)
	n := &model.Node{ID: 7, Name: "a.png", Mime: "image/png", Type: model.NodeTypeFile, Size: 10}
	ready := func(attempts ...Attempt) *model.Thumbnail {
		return &model.Thumbnail{State: "ready", SourceSig: SourceSig(n), AttemptedAt: &old, Attempts: encodeAttempts(attempts)}
	}

	// builtin drew it; the apps behind it change nothing.
	assert.Equal(t, Leave, f.p.Assess(n, ready(Attempt{"builtin", "ok"}), now))
	// the administrator put an app in front of it.
	f.rule(t, "png", assoc.Rule{Order: []string{"app:pngplus"}})
	assert.Equal(t, Render, f.p.Assess(n, ready(Attempt{"builtin", "ok"}), now))
	// drawn by the app (now first): fresh; upgraded: stale.
	assert.Equal(t, Leave, f.p.Assess(n, ready(Attempt{"app:pngplus@1", "ok"}), now))
	alt.version = "2"
	assert.Equal(t, Render, f.p.Assess(n, ready(Attempt{"app:pngplus@1", "ok"}), now))
	alt.version = "1"
	// drawn after the app failed: the same chain, fresh.
	assert.Equal(t, Leave, f.p.Assess(n, ready(Attempt{"app:pngplus@1", "app_failed:pngplus"}, Attempt{"builtin", "ok"}), now))

	// skipped by everybody: a NEW handler after them makes it stale.
	sk := &model.Thumbnail{State: "skipped", Error: "app_failed:pngplus", SourceSig: SourceSig(n), AttemptedAt: &old,
		Attempts: encodeAttempts([]Attempt{{"app:pngplus@1", "app_failed:pngplus"}, {"builtin", "thumb: decode"}})}
	assert.Equal(t, Render, f.p.Assess(n, sk, now), "zzz is in the chain now, after both")
	f.rule(t, "png", assoc.Rule{Order: []string{"app:pngplus", "builtin"}, Off: []string{"app:zzz"}})
	assert.Equal(t, Leave, f.p.Assess(n, sk, now), "the same chain as recorded")

	// a row from before 0.50: as if builtin drew it.
	legacy := &model.Thumbnail{State: "ready", SourceSig: SourceSig(n), AttemptedAt: &old}
	assert.Equal(t, Render, f.p.Assess(n, legacy, now), "builtin is no longer first")
	f.rule(t, "png", assoc.Rule{})
	assert.Equal(t, Leave, f.p.Assess(n, legacy, now))

	// an encrypted file's skip is nobody's to redraw, whatever the chain.
	enc := &model.Thumbnail{State: "skipped", Error: "e2e-encrypted content", SourceSig: SourceSig(n), AttemptedAt: &old}
	f.rule(t, "png", assoc.Rule{Order: []string{"app:pngplus"}})
	assert.Equal(t, Leave, f.p.Assess(n, enc, now))
}

func TestFreshness_AKindOnlyAnAppDrawsIsKeptFresh(t *testing.T) {
	now := time.Now()
	pk := &fakeApp{name: "pkglist", version: "1", ext: []string{"jar"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, pk)
	n := &model.Node{ID: 8, Name: "lib.jar", Mime: "application/zip", Type: model.NodeTypeFile, Size: 10}
	assert.Equal(t, Render, f.p.Assess(n, nil, now), "a listing draws a kind an app draws")
	plain := New(nil, t.TempDir(), Capabilities{Image: true})
	assert.Equal(t, Leave, plain.Assess(n, nil, now), "without apps a .jar is only the placeholder card")
}

func TestFreshness_ARaisedAppLimitDrawsItAgain(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	pk := &fakeApp{name: "pkglist", version: "1", ext: []string{"jar"}, draw: drawsPNG(t), maxBytes: 100, timeout: 10 * time.Second}
	f := newAppsFixture(t, pk)
	n := &model.Node{ID: 9, Name: "big.jar", Type: model.NodeTypeFile, Size: 150}
	row := &model.Thumbnail{State: "skipped", Error: "app_too_large:pkglist:100", SourceSig: SourceSig(n), AttemptedAt: &old,
		Attempts: encodeAttempts([]Attempt{{"app:pkglist@1", "app_too_large:pkglist:100"}})}
	assert.Equal(t, Leave, f.p.Assess(n, row, now))
	pk.maxBytes = 200
	assert.Equal(t, Render, f.p.Assess(n, row, now), "the limit now lets it through")
	timed := &model.Thumbnail{State: "skipped", Error: "app_timeout:pkglist:10000", SourceSig: SourceSig(n), AttemptedAt: &old,
		Attempts: encodeAttempts([]Attempt{{"app:pkglist@1", "app_timeout:pkglist:10000"}})}
	assert.Equal(t, Leave, f.p.Assess(n, timed, now))
	pk.timeout = 20 * time.Second
	assert.Equal(t, Render, f.p.Assess(n, timed, now))
	assert.True(t, f.p.Wanted(n, timed, Fix), "and Fix redraws it")
}

func TestParseAppReason(t *testing.T) {
	code, app, limit, ok := ParseAppReason("app_timeout:pkglist:10000")
	assert.True(t, ok)
	assert.Equal(t, []any{SkipAppTimeout, "pkglist", int64(10000)}, []any{code, app, limit})
	_, _, _, ok = ParseAppReason("no_tool:video")
	assert.False(t, ok)
	_, app, _, ok = ParseAppReason("app_failed:pkglist")
	assert.True(t, ok)
	assert.Equal(t, "pkglist", app)
}

// A panic in an app's draw is the app failing that file, not a crash of the
// goroutine that draws thumbnails: the next handler is asked.
func TestAppChain_APanicIsTheAppFailing(t *testing.T) {
	ctx := context.Background()
	bad := &fakeApp{name: "aaa", version: "1", ext: []string{"jar"}, draw: func(assoc.DrawRequest, []byte) ([]byte, error) { panic("boom") }}
	good := &fakeApp{name: "bbb", version: "1", ext: []string{"jar"}, draw: drawsPNG(t)}
	f := newAppsFixture(t, bad, good)
	n := f.put("lib.jar", []byte("x"))
	require.NotPanics(t, func() { require.NoError(t, f.p.GenerateThumb(ctx, n)) })
	row := f.row(n.ID)
	assert.Equal(t, "app:bbb@1", row.Generator)
	assert.Equal(t, Attempt{H: "app:aaa@1", R: "app_failed:aaa"}, ParseAttempts(row.Attempts)[0])
}
