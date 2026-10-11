package api_test

// The installed app wears the operator's brand (white label, 0.56).
//
// Reported 2026-10-09: fm.example.com, branded "BRF Teknoloji", installed on a phone
// as plain "filex" with filex's icon. The manifest was the build's byte for
// byte whatever the Branding page said, so three filex instances on one phone
// were three identical "filex" icons. These tests hold the server to the
// branded documents a browser installs from, read WITHOUT a session (Chrome's
// WebAPK server fetches the manifest and its icons with none), and to the one
// thing branding must never move: the manifest `id` and `start_url`, the
// installed app's identity.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// brandIndex is shaped like the real build's index.html: the favicon, the
// apple-touch-icon, the theme-color meta and the title the server dresses.
const brandIndex = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/admin/favicon.svg" />
    <link rel="apple-touch-icon" href="/admin/icons/icon-192.png" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="theme-color" content="#2f6ceb" />
    <title>filex</title>
    <script type="module" crossorigin src="/admin/assets/index-AbC123.js"></script>
  <link rel="manifest" href="/admin/manifest.webmanifest"></head>
  <body><div id="app"></div></body>
</html>
`

// brandManifest is the real build's manifest (web/pwa.config.ts).
const brandManifest = `{"name":"filex - File Manager","short_name":"filex","start_url":"/admin/","display":"standalone","background_color":"#0a0a0a","lang":"en","scope":"/","id":"/admin/","description":"Self-hosted file manager: browse, upload, share and edit your files.","orientation":"any","theme_color":"#2f6ceb","icons":[{"src":"icons/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"any"},{"src":"icons/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"maskable"},{"src":"icons/icon-192.png","sizes":"192x192","type":"image/png","purpose":"any"},{"src":"icons/icon-512.png","sizes":"512x512","type":"image/png","purpose":"any"},{"src":"icons/icon-512.png","sizes":"512x512","type":"image/png","purpose":"maskable"}]}`

// brandSW carries the precache entries the way vite-plugin-pwa writes them.
const brandSW = `define(["./workbox-2fbc6a65"],function(e){"use strict";e.precacheAndRoute([{url:"assets/index-AbC123.js",revision:null},{url:"index.html",revision:"ad978502ba9b55d68d52ac089536548a"},{url:"favicon.svg",revision:"1111"},{url:"manifest.webmanifest",revision:"2379e85ca56b8e8e001fd9e31e4aec3f"}],{})});`

func brandBuild() fstest.MapFS {
	b := fakeBuild()
	b["admin/index.html"] = &fstest.MapFile{Data: []byte(brandIndex)}
	b["admin/manifest.webmanifest"] = &fstest.MapFile{Data: []byte(brandManifest)}
	b["admin/sw.js"] = &fstest.MapFile{Data: []byte(brandSW)}
	return b
}

// brandLogoPNG is a 300x200 logo: a red block on transparency.
func brandLogoPNG(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 50; y < 150; y++ {
		for x := 75; x < 225; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// newBrandServer starts filex with the given branding.* settings stored
// before the first request.
func newBrandServer(t *testing.T, base string, settings map[string]string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.BasePath = base
		c.PublicURL = "http://test.local" + base
	}, func(d *api.Deps) {
		d.Embed = brandBuild()
	})
	for k, v := range settings {
		require.NoError(t, store.UpsertSetting(context.Background(), k, v))
	}
	return srv
}

func decodeManifest(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m), body)
	return m
}

func manifestIcons(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	list, ok := m["icons"].([]any)
	require.True(t, ok, "icons is a list")
	var out []map[string]any
	for _, it := range list {
		out = append(out, it.(map[string]any))
	}
	return out
}

func TestPWABrand_TheManifestWearsTheBrandAndKeepsItsIdentity(t *testing.T) {
	srv := newBrandServer(t, "", map[string]string{
		"branding.name":     "BRF Teknoloji",
		"branding.accent":   "#123456",
		"branding.logo_url": brandLogoPNG(t),
	})
	// No login: a fresh client, as Chrome's WebAPK server is.
	c := bpNoFollow(t)

	resp, body := bpGet(t, c, srv.URL+"/admin/manifest.webmanifest")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/manifest+json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"), "a brand change reaches the next fetch")

	m := decodeManifest(t, body)
	assert.Equal(t, "BRF Teknoloji", m["name"])
	assert.Equal(t, "BRF Teknoloji", m["short_name"])
	assert.Equal(t, "#123456", m["theme_color"])
	// ⚠⚠ The installed app's identity: branding never moves it.
	assert.Equal(t, "/admin/", m["id"])
	assert.Equal(t, "/admin/", m["start_url"])
	assert.Equal(t, "/", m["scope"])
	assert.Equal(t, "#0a0a0a", m["background_color"], "the splash matches the page after it, not the accent")
	assert.Equal(t, "standalone", m["display"])

	icons := manifestIcons(t, m)
	require.Len(t, icons, 4)
	srcRe := regexp.MustCompile(`^brand-icons/[0-9a-f]{16}(-maskable)?-(192|512)\.png$`)
	seen := map[string]bool{}
	for _, ic := range icons {
		src := ic["src"].(string)
		assert.Regexp(t, srcRe, src)
		assert.Equal(t, "image/png", ic["type"])
		seen[ic["purpose"].(string)+" "+ic["sizes"].(string)] = true

		// Fetched without a session, the way the manifest names it.
		iresp, ibody := bpGet(t, c, srv.URL+"/admin/"+src)
		require.Equal(t, http.StatusOK, iresp.StatusCode, src)
		assert.Equal(t, "image/png", iresp.Header.Get("Content-Type"), src)
		assert.Contains(t, iresp.Header.Get("Cache-Control"), "immutable", "the address is the content: %s", src)
		img, err := png.Decode(strings.NewReader(ibody))
		require.NoError(t, err, src)
		want := 192
		if strings.HasSuffix(src, "-512.png") {
			want = 512
		}
		assert.Equal(t, image.Rect(0, 0, want, want), img.Bounds(), src)

		corner := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
		middle := color.NRGBAModel.Convert(img.At(want/2, want/2)).(color.NRGBA)
		assert.Greater(t, middle.R, uint8(150), "the logo is drawn in the middle: %s", src)
		if ic["purpose"] == "maskable" {
			// A transparent logo stands on white, edge to edge: a launcher
			// cuts a maskable icon to its own shape.
			assert.Equal(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, corner, src)
		} else {
			assert.Equal(t, uint8(0), corner.A, "the any icon keeps the logo's transparency: %s", src)
		}
	}
	for _, k := range []string{"any 192x192", "any 512x512", "maskable 192x192", "maskable 512x512"} {
		assert.True(t, seen[k], k)
	}

	// An address that is not the current logo's is not an icon.
	resp, _ = bpGet(t, c, srv.URL+"/admin/brand-icons/0000000000000000-192.png")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPWABrand_TheFirstPaintWearsTheBrand(t *testing.T) {
	srv := newBrandServer(t, "", map[string]string{
		"branding.name":     "BRF <Teknoloji>",
		"branding.accent":   "#123456",
		"branding.logo_url": brandLogoPNG(t),
	})
	c := bpNoFollow(t)

	for _, p := range []string{"/admin/", "/drive/explore", "/admin/files"} {
		resp, body := bpGet(t, c, srv.URL+p)
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		assert.Contains(t, body, "<title>BRF &lt;Teknoloji&gt;</title>", p)
		assert.NotContains(t, body, "<title>filex</title>", p)
		assert.Contains(t, body, `<meta name="theme-color" content="#123456"`, p)
		assert.Regexp(t, `<link rel="apple-touch-icon" href="/admin/brand-icons/[0-9a-f]{16}-maskable-192\.png"`, body, p)
		assert.Regexp(t, `<link rel="icon" type="image/png" href="/admin/brand-icons/[0-9a-f]{16}-192\.png" />`, body, p)
		assert.Contains(t, body, `<link rel="manifest" href="/admin/manifest.webmanifest">`, p)
	}
}

func TestPWABrand_UnderABasePath(t *testing.T) {
	srv := newBrandServer(t, "/filex", map[string]string{
		"branding.name":     "BRF Teknoloji",
		"branding.logo_url": brandLogoPNG(t),
	})
	c := bpNoFollow(t)

	resp, body := bpGet(t, c, srv.URL+"/filex/admin/manifest.webmanifest")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	m := decodeManifest(t, body)
	assert.Equal(t, "BRF Teknoloji", m["name"])
	assert.Equal(t, "/filex/admin/", m["id"], "the base moves the identity; the brand does not")
	assert.Equal(t, "/filex/admin/", m["start_url"])
	assert.Equal(t, "#2f6ceb", m["theme_color"], "no accent set: the product colour stays")

	icons := manifestIcons(t, m)
	require.NotEmpty(t, icons)
	src := icons[0]["src"].(string)
	resp, _ = bpGet(t, c, srv.URL+"/filex/admin/"+src)
	assert.Equal(t, http.StatusOK, resp.StatusCode, src)

	resp, body = bpGet(t, c, srv.URL+"/filex/admin/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<title>BRF Teknoloji</title>")
	assert.Regexp(t, `<link rel="apple-touch-icon" href="/filex/admin/brand-icons/[0-9a-f]{16}-maskable-192\.png"`, body)
	assert.Contains(t, body, `<meta name="theme-color" content="#2f6ceb"`)
}

func TestPWABrand_AnSVGLogoLeadsAndFilexPNGsStayBehindIt(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="red"/></svg>`
	srv := newBrandServer(t, "", map[string]string{
		"branding.name":     "BRF Teknoloji",
		"branding.logo_url": "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)),
	})
	c := bpNoFollow(t)

	_, body := bpGet(t, c, srv.URL+"/admin/manifest.webmanifest")
	icons := manifestIcons(t, decodeManifest(t, body))
	require.Len(t, icons, 4, "the SVG and filex's three PNG entries")
	assert.Regexp(t, `^brand-icons/[0-9a-f]{16}\.svg$`, icons[0]["src"])
	assert.Equal(t, "image/svg+xml", icons[0]["type"])
	assert.Equal(t, "any", icons[0]["purpose"])
	for _, ic := range icons[1:] {
		assert.Equal(t, "image/png", ic["type"])
		assert.True(t, strings.HasPrefix(ic["src"].(string), "icons/icon-"), ic["src"])
	}

	resp, sbody := bpGet(t, c, srv.URL+"/admin/"+icons[0]["src"].(string))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/svg+xml", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox", "the operator's SVG runs nothing when opened on its own")
	assert.Equal(t, svg, sbody)
}

// A logo given as an address IS fetched now (#219, the unit tests beside
// spa_brand.go draw one), through the download guard: an address on the
// server's own network is the SSRF shape and is never dialled, and a
// site-relative one names this server and is not fetched. Both keep filex's
// icons under the brand's name.
func TestPWABrand_ALogoAddressOnTheServersOwnNetworkIsNotFetched(t *testing.T) {
	var hits atomic.Int32
	logo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("not reached"))
	}))
	defer logo.Close()

	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(brandManifest), &want))
	for _, ref := range []string{logo.URL + "/logo.png", "/branding/logo.png"} {
		srv := newBrandServer(t, "", map[string]string{
			"branding.name":     "BRF Teknoloji",
			"branding.logo_url": ref,
		})
		c := bpNoFollow(t)

		_, body := bpGet(t, c, srv.URL+"/admin/manifest.webmanifest")
		m := decodeManifest(t, body)
		assert.Equal(t, "BRF Teknoloji", m["name"], ref)
		assert.Equal(t, want["icons"], m["icons"], "%s: filex's icons stay", ref)
	}
	assert.Equal(t, int32(0), hits.Load(), "the logo server on this machine never heard from filex")
}

func TestPWABrand_TheWorkerRefetchesTheBrandedDocuments(t *testing.T) {
	c := bpNoFollow(t)
	sw := func(settings map[string]string) string {
		srv := newBrandServer(t, "", settings)
		resp, body := bpGet(t, c, srv.URL+"/admin/sw.js")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
		return body
	}

	plain := sw(nil)
	assert.Equal(t, brandSW, plain, "nothing branded: the worker is served as built")

	a := sw(map[string]string{"branding.name": "BRF Teknoloji"})
	b := sw(map[string]string{"branding.name": "Another Brand"})
	revRe := regexp.MustCompile(`url:"(index\.html|manifest\.webmanifest)",revision:"([^"]+)"`)
	for _, doc := range []string{a, b} {
		got := revRe.FindAllStringSubmatch(doc, -1)
		require.Len(t, got, 2)
		assert.Regexp(t, `^ad978502ba9b55d68d52ac089536548a-[0-9a-f]{12}$`, got[0][2])
		assert.Regexp(t, `^2379e85ca56b8e8e001fd9e31e4aec3f-[0-9a-f]{12}$`, got[1][2])
		assert.Contains(t, doc, `{url:"favicon.svg",revision:"1111"}`, "only the branded documents move")
	}
	assert.NotEqual(t, a, b, "a brand change is a new worker, which refetches index.html and the manifest")
}

// pushIconSpy is the real notification service with the push icon hook kept,
// so the test can ask it what a person's push would show.
type pushIconSpy struct {
	notify.Service
	fn func(context.Context, *model.User) string
}

func (s *pushIconSpy) SetPushIcon(fn func(context.Context, *model.User) string) { s.fn = fn }

// A Web Push shows the icon the installed app's manifest names (#219): the
// router hands the notification service the PNG it reads off the manifest
// this server serves - the drawn logo where there is one, filex's otherwise.
func TestPWABrand_APushShowsTheIconTheManifestNames(t *testing.T) {
	for _, branded := range []bool{false, true} {
		spy := &pushIconSpy{}
		srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
			d.Embed = brandBuild()
			spy.Service = notify.New(d.Store, notify.Config{})
			d.Notify = spy
		})
		defer spy.Stop()
		ctx := context.Background()
		if branded {
			require.NoError(t, store.UpsertSetting(ctx, "branding.name", "BRF Teknoloji"))
			require.NoError(t, store.UpsertSetting(ctx, "branding.logo_url", brandLogoPNG(t)))
		}
		require.NotNil(t, spy.fn, "the router wires the push icon")

		_, body := bpGet(t, bpNoFollow(t), srv.URL+"/admin/manifest.webmanifest")
		var firstPNG string
		for _, ic := range manifestIcons(t, decodeManifest(t, body)) {
			if ic["type"] == "image/png" && ic["purpose"] == "any" {
				firstPNG = ic["src"].(string)
				break
			}
		}
		got := spy.fn(ctx, &model.User{ID: 1})
		assert.Equal(t, firstPNG, got, "branded=%v: the push's icon is the manifest's", branded)
		if branded {
			assert.Regexp(t, `^brand-icons/[0-9a-f]{16}-192\.png$`, got)
		} else {
			assert.Equal(t, "icons/icon-192.png", got)
		}
	}
}
