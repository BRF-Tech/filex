package api

// A logo given as an address becomes the installed app's icon too (#219).
//
// 0.56 drew the home-screen icons from a data: URI logo only; an instance
// branded with a logo URL installed under filex's icon while its band and its
// login page showed the operator's logo. The address is one a tenant admin
// typed, so these tests hold the fetch to the guard (a public address only,
// a picture type, a size cap, a timeout), to the background refresh and to
// keeping the last good icon through a hiccup.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

func logoPNG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 48, 32))
	for y := 8; y < 24; y++ {
		for x := 12; x < 36; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// loopbackLogoCache fetches like the server does, through netguard's download
// client, with the one exception a test needs: its logo server is on this
// machine.
func loopbackLogoCache() *brandIconCache {
	c := newBrandIconCache()
	c.fetch = fetchBrandLogo(netguard.Policy{Loopback: true}.DownloadClient(5 * time.Second))
	return c
}

func TestBrandLogo_ALogoAddressIsFetchedAndDrawn(t *testing.T) {
	pic := logoPNG(t, color.NRGBA{R: 200, A: 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pic)
	}))
	defer srv.Close()

	icons := loopbackLogoCache()
	b := &shellBrander{
		resolve: func(*http.Request) handlers.BrandingConfig {
			return handlers.BrandingConfig{Name: "BRF Teknoloji", LogoURL: srv.URL + "/logo.png"}
		},
		icons: icons,
	}
	r := httptest.NewRequest("GET", "/admin/manifest.webmanifest", nil)

	want := decodeBrandLogoBytes("image/png", pic)
	require.NotNil(t, want)

	// The first page after the logo was set waits for it (briefly) and wears it.
	var m map[string]any
	require.NoError(t, json.Unmarshal(b.manifest([]byte(`{"name":"filex - File Manager","id":"/admin/","icons":[{"src":"icons/icon.svg","purpose":"any"}]}`), r), &m))
	assert.Equal(t, "BRF Teknoloji", m["name"])
	list := m["icons"].([]any)
	require.Len(t, list, 4)
	first := list[0].(map[string]any)
	assert.Equal(t, "brand-icons/"+want.hash+"-192.png", first["src"], "named after the picture's content, like a data: URI logo")
	assert.Equal(t, "any", first["purpose"])

	rec := httptest.NewRecorder()
	b.serveIcon(rec, r, want.hash+"-maskable-512.png")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	out, err := png.Decode(rec.Body)
	require.NoError(t, err)
	assert.Equal(t, 512, out.Bounds().Dx())

	idx := string(b.index([]byte(`<link rel="icon" type="image/svg+xml" href="/admin/favicon.svg" /><link rel="apple-touch-icon" href="/admin/icons/icon-192.png" />`), r))
	assert.Contains(t, idx, `href="/admin/brand-icons/`+want.hash+`-192.png"`)
	assert.Contains(t, idx, `href="/admin/brand-icons/`+want.hash+`-maskable-192.png"`)
}

func TestBrandLogo_AnAddressThatIsNotALogoIsNotOne(t *testing.T) {
	pic := logoPNG(t, color.NRGBA{G: 200, A: 255})
	big := append(append([]byte{}, pic...), bytes.Repeat([]byte{0}, brandRemoteLogoMaxBytes)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not a logo</html>"))
		case "/missing":
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(pic)
		case "/big":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(big)
		case "/big-unsized":
			w.Header().Set("Content-Type", "image/png")
			w.(http.Flusher).Flush()
			_, _ = w.Write(big)
		case "/slow":
			<-r.Context().Done()
		case "/redirect-inward":
			http.Redirect(w, r, "http://10.0.0.1/logo.png", http.StatusFound)
		}
	}))
	defer srv.Close()

	fetch := fetchBrandLogo(netguard.Policy{Loopback: true}.DownloadClient(5 * time.Second))
	for _, path := range []string{"/page", "/missing", "/big", "/big-unsized", "/redirect-inward"} {
		_, raw, err := fetch(context.Background(), srv.URL+path)
		assert.ErrorIs(t, err, errBrandLogo, path)
		assert.Nil(t, raw, path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := fetch(ctx, srv.URL+"/slow")
	assert.ErrorIs(t, err, errBrandLogo, "a fetch has a deadline")
}

// The server's own client: an address on this machine (or any private one) is
// the SSRF shape, refused at the dial, and the logo server never hears of it.
func TestBrandLogo_TheServerDoesNotFetchFromItsOwnNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(logoPNG(t, color.NRGBA{B: 200, A: 255}))
	}))
	defer srv.Close()

	c := newBrandIconCache()
	assert.Nil(t, c.logo(srv.URL+"/logo.png"))
	c.fetches.Wait()
	assert.Equal(t, int32(0), hits.Load())

	_, _, err := c.fetch(context.Background(), srv.URL+"/logo.png")
	assert.ErrorIs(t, err, netguard.ErrPrivateTarget)
}

// The address is read again on its own clock, in the background; a hiccup
// keeps the icon the address gave last, and a new picture is a new icon.
func TestBrandLogo_AnAddressIsReadAgainAndAHiccupKeepsTheIcon(t *testing.T) {
	a := logoPNG(t, color.NRGBA{R: 200, A: 255})
	bpic := logoPNG(t, color.NRGBA{G: 200, A: 255})
	var mu sync.Mutex
	answers := []func() (string, []byte, error){
		func() (string, []byte, error) { return "image/png", a, nil },
		func() (string, []byte, error) { return "", nil, errors.New("the logo host is down") },
		func() (string, []byte, error) { return "image/png", bpic, nil },
	}
	var calls int
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := newBrandIconCache()
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	c.fetch = func(context.Context, string) (string, []byte, error) {
		mu.Lock()
		defer mu.Unlock()
		f := answers[calls]
		calls++
		return f()
	}
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	const ref = "https://logo.example/brf.png"

	first := c.logo(ref)
	require.NotNil(t, first)
	assert.Equal(t, decodeBrandLogoBytes("image/png", a).hash, first.hash)

	advance(brandRemoteLogoTTL - time.Minute)
	assert.Same(t, first, c.logo(ref), "inside the hour: the cached picture, no request")
	c.fetches.Wait()
	assert.Equal(t, 1, calls)

	advance(2 * time.Minute)
	assert.Same(t, first, c.logo(ref), "the refresh runs in the background; the page is not held")
	c.fetches.Wait()
	assert.Equal(t, 2, calls)
	assert.Same(t, first, c.logo(ref), "the host was down: the icon it gave last stays")
	c.fetches.Wait()
	assert.Equal(t, 2, calls, "a failure is retried later, not on every page")

	advance(brandRemoteLogoRetry)
	c.logo(ref)
	c.fetches.Wait()
	assert.Equal(t, 3, calls)
	next := c.logo(ref)
	require.NotNil(t, next)
	assert.NotEqual(t, first.hash, next.hash, "a new picture at the address is a new icon address")
}

// A first fetch slower than the short wait answers the page without the logo;
// the next page has it.
func TestBrandLogo_ASlowFirstFetchDoesNotHoldThePage(t *testing.T) {
	old := brandRemoteLogoColdWait
	brandRemoteLogoColdWait = 50 * time.Millisecond
	defer func() { brandRemoteLogoColdWait = old }()

	release := make(chan struct{})
	pic := logoPNG(t, color.NRGBA{R: 90, G: 90, A: 255})
	c := newBrandIconCache()
	c.fetch = func(context.Context, string) (string, []byte, error) {
		<-release
		return "image/png", pic, nil
	}
	start := time.Now()
	assert.Nil(t, c.logo("https://logo.example/slow.png"))
	assert.Less(t, time.Since(start), 2*time.Second)
	close(release)
	c.fetches.Wait()
	assert.NotNil(t, c.logo("https://logo.example/slow.png"))
}

// A push and the page's own pop-up show the installed app's PNG icon, read
// off the manifest this branding is served (#219): the drawn logo's 192 px
// icon, filex's PNG where the logo is an SVG or there is none.
func TestBrandLogo_ThePushIconIsTheManifestsPNG(t *testing.T) {
	built := []byte(`{"name":"filex - File Manager","id":"/admin/","icons":[` +
		`{"src":"icons/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"any"},` +
		`{"src":"icons/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"maskable"},` +
		`{"src":"icons/icon-192.png","sizes":"192x192","type":"image/png","purpose":"any"},` +
		`{"src":"icons/icon-512.png","sizes":"512x512","type":"image/png","purpose":"any"}]}`)
	pic := logoPNG(t, color.NRGBA{R: 200, A: 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pic)
	}))
	defer srv.Close()
	hash := decodeBrandLogoBytes("image/png", pic).hash

	cases := []struct {
		name string
		cfg  handlers.BrandingConfig
		want string
	}{
		{"nothing branded", handlers.BrandingConfig{}, "icons/icon-192.png"},
		{"a name only", handlers.BrandingConfig{Name: "BRF Teknoloji"}, "icons/icon-192.png"},
		{"a data: URI logo", handlers.BrandingConfig{Name: "BRF Teknoloji", LogoURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pic)}, "brand-icons/" + hash + "-192.png"},
		{"a logo address", handlers.BrandingConfig{Name: "BRF Teknoloji", LogoURL: srv.URL + "/logo.png"}, "brand-icons/" + hash + "-192.png"},
		{"an SVG logo", handlers.BrandingConfig{LogoURL: `data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22/%3E`}, "icons/icon-192.png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &shellBrander{resolve: func(*http.Request) handlers.BrandingConfig { return c.cfg }, icons: loopbackLogoCache()}
			got := b.pushIcon(built, c.cfg)
			assert.Equal(t, c.want, got)
			served := b.manifest(built, httptest.NewRequest("GET", "/admin/manifest.webmanifest", nil))
			assert.Equal(t, firstPNGIcon(served), got, "the same icon the manifest the browser installs from names")
		})
	}
	assert.Empty(t, (&shellBrander{icons: newBrandIconCache()}).pushIcon(nil, handlers.BrandingConfig{}), "no build: no icon, the worker's own")
}
