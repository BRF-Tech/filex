package handlers_test

// An administrator who installs a language pack sees that language on the
// NEXT page load, not a minute later.
//
// ⚠⚠ The defect this file holds shut. The offered languages ride
// `/api/public/branding`, which was served `Cache-Control: public,
// max-age=60`: install a pack, reload, and the picker still did not list it —
// for up to a minute, with nothing on screen to say why. It was found while
// writing an RTL spec (e2e/tests/126-rtl-server-text.spec.ts), which had to
// install its pack before the first page load to work around it, and "I
// installed it and nothing happened" is reported as a broken install.
//
// The fix is a strong ETag over the body plus `no-cache`, so every reader
// REVALIDATES and an unchanged answer costs a 304 with no body
// (handlers/public_cache.go). These tests are what says it stayed that way:
// the tag moves when the app set moves, an unchanged answer is still cheap,
// and the ~300 KB per-language strings table keeps its caching benefit.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getWith is doReq for one GET that carries request headers and hands back
// the response's — what a conditional request needs.
func getWith(t *testing.T, client *http.Client, url string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// TestPublicAnswers_RevalidateInsteadOfGoingStale: the four public answers
// that say who this instance is are revalidated, not held.
func TestPublicAnswers_RevalidateInsteadOfGoingStale(t *testing.T) {
	f := newAppFixture(t, nil)
	anon := freshClient(t)

	for _, path := range []string{"/api/public/branding", "/api/branding", "/api/appearance"} {
		code, hdr, raw := getWith(t, anon, f.srv.URL+path, nil)
		require.Equal(t, http.StatusOK, code, "%s %s", path, raw)

		cc := hdr.Get("Cache-Control")
		assert.Contains(t, cc, "no-cache", "%s must be revalidated", path)
		assert.NotContains(t, cc, "max-age", "%s: a freshness lifetime is what made it go stale", path)
		// ⚠ Not no-store: the body is still kept and still not transferred
		// again — that is the whole point of revalidating instead of holding.
		assert.NotContains(t, cc, "no-store", "%s: the public pages keep their caching benefit", path)
		assert.Contains(t, cc, "public", "%s stays cacheable by a shared cache", path)

		// ⚠ Only the answer that resolves the visitor's language says it
		// varies by it; the other two would only split a shared cache's keys.
		if path == "/api/public/branding" {
			assert.Contains(t, hdr.Values("Vary"), "Accept-Language", "%s: `locale` is per visitor", path)
		} else {
			assert.NotContains(t, hdr.Values("Vary"), "Accept-Language", "%s does not vary by language", path)
		}

		etag := hdr.Get("ETag")
		require.NotEmpty(t, etag, "%s has no ETag to revalidate against", path)
		assert.True(t, strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`), "%s: %q is not a quoted tag", path, etag)

		// Unchanged: 304, and NOTHING on the wire.
		code, _, raw = getWith(t, anon, f.srv.URL+path, map[string]string{"If-None-Match": etag})
		assert.Equal(t, http.StatusNotModified, code, path)
		assert.Empty(t, raw, "%s: a 304 carries no body", path)

		// `*` and a weakened tag are the same question (RFC 9110).
		code, _, _ = getWith(t, anon, f.srv.URL+path, map[string]string{"If-None-Match": "*"})
		assert.Equal(t, http.StatusNotModified, code, path)
		code, _, _ = getWith(t, anon, f.srv.URL+path, map[string]string{"If-None-Match": `W/` + etag})
		assert.Equal(t, http.StatusNotModified, code, path)
		// Somebody else's tag is not ours.
		code, _, _ = getWith(t, anon, f.srv.URL+path, map[string]string{"If-None-Match": `"beef"`})
		assert.Equal(t, http.StatusOK, code, path)
	}
}

// TestPublicBranding_ATagMovesWhenTheAppSetDoes: the measurement the fix
// exists for — install a pack and the SAME conditional request that was
// answered 304 a moment ago is answered with the new language.
func TestPublicBranding_ATagMovesWhenTheAppSetDoes(t *testing.T) {
	f := newAppFixture(t, nil)
	anon := freshClient(t)
	const url = "/api/public/branding"

	code, hdr, raw := getWith(t, anon, f.srv.URL+url, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	before := hdr.Get("ETag")
	require.NotEmpty(t, before)
	assert.NotContains(t, string(raw), `"es"`, "nothing installed yet")

	// The reader is holding a copy and has been told it is current.
	code, _, _ = getWith(t, anon, f.srv.URL+url, map[string]string{"If-None-Match": before})
	require.Equal(t, http.StatusNotModified, code)

	status, out := postLanguagePack(t, f, spanishPack(t, 12), false)
	require.Equal(t, http.StatusCreated, status, string(out))
	var row struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(out, &row))

	// ⚠⚠ THE POINT. The very same conditional request, immediately after the
	// install: 200 with the language, not another 304.
	code, hdr, raw = getWith(t, anon, f.srv.URL+url, map[string]string{"If-None-Match": before})
	require.Equal(t, http.StatusOK, code, "the pack was installed and the cache was not told")
	after := hdr.Get("ETag")
	assert.NotEqual(t, before, after, "the tag has to move when the app set moves")
	var body struct {
		Locales   []string `json:"locales"`
		UILocales []struct {
			Code string `json:"code"`
			RTL  bool   `json:"rtl"`
		} `json:"ui_locales"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Contains(t, body.Locales, "es")
	assert.Contains(t, body.Locales, "ar")
	require.NotEmpty(t, body.UILocales)

	// …and it moves back when the pack leaves.
	status, out = doReq(t, f.admin, http.MethodDelete, fmt.Sprintf("%s/api/admin/app-plugins/%d", f.srv.URL, row.ID), nil)
	require.True(t, status/100 == 2, "%d %s", status, out)
	code, hdr, raw = getWith(t, anon, f.srv.URL+url, map[string]string{"If-None-Match": after})
	require.Equal(t, http.StatusOK, code, "the pack was removed and the cache was not told")
	assert.NotEqual(t, after, hdr.Get("ETag"))
	assert.NotContains(t, string(raw), `"es"`)
}

// TestPublicUILocale_KeepsItsCachingBenefit: the one payload where a held
// copy really pays — a pack's whole string table — is still not transferred
// twice, and still stops being yesterday's the moment the pack changes.
func TestPublicUILocale_KeepsItsCachingBenefit(t *testing.T) {
	f := newAppFixture(t, nil)
	anon := freshClient(t)

	status, out := postLanguagePack(t, f, spanishPack(t, 2500), false)
	require.Equal(t, http.StatusCreated, status, string(out))
	var row struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(out, &row))

	const url = "/api/public/ui-locales/es"
	code, hdr, raw := getWith(t, anon, f.srv.URL+url, nil)
	require.Equal(t, http.StatusOK, code)
	etag := hdr.Get("ETag")
	require.NotEmpty(t, etag)
	assert.Greater(t, len(raw), 20_000, "this is the big one — the table, not a list")

	code, _, raw = getWith(t, anon, f.srv.URL+url, map[string]string{"If-None-Match": etag})
	assert.Equal(t, http.StatusNotModified, code)
	assert.Empty(t, raw, "the table is not sent again")

	// A pack whose words changed: the tag moves with them. ⚠ Removed and put
	// back rather than posted twice — the install route refuses a name it
	// already has, which is the admin panel's Upgrade button's business.
	status, out = doReq(t, f.admin, http.MethodDelete, fmt.Sprintf("%s/api/admin/app-plugins/%d", f.srv.URL, row.ID), nil)
	require.True(t, status/100 == 2, "%d %s", status, out)
	status, out = postLanguagePack(t, f, spanishPack(t, 2501), false)
	require.Equal(t, http.StatusCreated, status, string(out))

	code, hdr, raw = getWith(t, anon, f.srv.URL+url, map[string]string{"If-None-Match": etag})
	assert.Equal(t, http.StatusOK, code, "a changed pack served yesterday's words for a minute")
	assert.NotEqual(t, etag, hdr.Get("ETag"))
	assert.Greater(t, len(raw), 20_000)
}
