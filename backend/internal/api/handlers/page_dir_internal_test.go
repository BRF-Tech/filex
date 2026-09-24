package handlers

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// TestServerPages_DirFollowsLang: every page the server renders itself — the
// PIN gate, the unlocked page, the error page, the ZIP wait, the file-request
// uploader, an app's no-JS page, the cache wait, the sign-in bounce and a
// shared folder — carries `dir` beside `lang`, from the one list (wire.IsRTL).
//
// ⚠ Merge seam (feat/043-rtl × feat/043-srvtext): srvtext lets a pack's
// language reach these pages (the server catalogue, extended by packs); rtl
// wrote their stylesheets in logical properties. Without `dir` an Arabic page
// read Arabic words laid out left to right, and those logical properties
// resolved to the wrong edge.
func TestServerPages_DirFollowsLang(t *testing.T) {
	pages := map[string]*template.Template{
		"oidc bounce":   oidcBounceTmpl,
		"cache wait":    cacheWaitTemplate,
		"drop uploader": dropUploaderTemplate,
		"zip wait":      zipWaitTemplate,
		"pin form":      pinFormTemplate,
		"unlocked":      unlockedTemplate,
		"error page":    errorPageTemplate,
		"app page":      appPageTemplate,
	}
	for _, c := range []struct{ lang, dir string }{{"ar", "rtl"}, {"he", "rtl"}, {"fa", "rtl"}, {"en", "ltr"}, {"tr", "ltr"}, {"es", "ltr"}} {
		for name, tmpl := range pages {
			var buf bytes.Buffer
			data := map[string]any{"Lang": c.lang, "Dir": pageDir(c.lang), "T": map[string]string{}}
			if name == "oidc bounce" {
				// This one is executed with map[string]string.
				if err := tmpl.Execute(&buf, map[string]string{"Lang": c.lang, "Dir": pageDir(c.lang)}); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			} else {
				_ = tmpl.Execute(&buf, data) // a page may stop at a field the fixture lacks; the <html> tag is first
			}
			want := `<html lang="` + c.lang + `" dir="` + c.dir + `">`
			if !bytes.Contains(buf.Bytes(), []byte(want)) {
				t.Errorf("%s in %s: want %s, got %.120q", name, c.lang, want, buf.String())
			}
		}
		rec := httptest.NewRecorder()
		_ = share.RenderFolderPage(rec, share.FolderPageData{Lang: c.lang, T: map[string]string{}})
		if want := `<html lang="` + c.lang + `" dir="` + c.dir + `">`; !bytes.Contains(rec.Body.Bytes(), []byte(want)) {
			t.Errorf("shared folder page in %s: want %s, got %.120q", c.lang, want, rec.Body.String())
		}
	}
}

// TestPublicLinkSentence_OneMessageAroundTheLink: a page sentence with a link
// in it is ONE catalogue message with `{link}` in it, not three fragments
// glued around an <a>.
//
// ⚠⚠ The ZIP wait page said `zip_hint_a` + `zip_hint_b` + `zip_hint_c`:
// "The download starts on its own when it is ready. If it does not, " /
// "click here" / ".". A translator saw three strings, one of them a lone full
// stop, and could not move the link, change the punctuation or read the
// sentence whole — and a language that puts the link first could not be
// written at all. (`access.ui.create_then_send` was the same shape and was
// fixed the same way earlier in v0.43.0.)
func TestPublicLinkSentence_OneMessageAroundTheLink(t *testing.T) {
	t.Run("the link lands where the sentence puts it", func(t *testing.T) {
		got := publicLinkSentence(map[string]string{
			"zip_hint": "Ready? If not, {link}.",
			"zip_link": "click here",
		}, "zip_hint", "zip_link", "dl", "?zip=wait")
		want := template.HTML(`Ready? If not, <a id="dl" href="?zip=wait">click here</a>.`)
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a language may put the link first", func(t *testing.T) {
		got := string(publicLinkSentence(map[string]string{
			"zip_hint": "{link} — la descarga empieza sola cuando esté lista.",
			"zip_link": "Descargar ahora",
		}, "zip_hint", "zip_link", "dl", "?zip=wait"))
		if !strings.HasPrefix(got, `<a id="dl"`) {
			t.Errorf("the link is not first: %q", got)
		}
		if !strings.HasSuffix(got, "lista.") {
			t.Errorf("the sentence lost its tail: %q", got)
		}
	})

	t.Run("a pack is text, not markup", func(t *testing.T) {
		got := string(publicLinkSentence(map[string]string{
			"zip_hint": `<script>x</script> {link}`,
			"zip_link": `</a><img onerror=1>`,
		}, "zip_hint", "zip_link", "dl", "?zip=wait"))
		if strings.Contains(got, "<script>") || strings.Contains(got, "<img") {
			t.Errorf("a pack's markup reached the page: %q", got)
		}
	})

	t.Run("the built-in English really is one message with {link} in it", func(t *testing.T) {
		en := srvtext.Builtin("en")
		if _, gone := en["server.public.zip_hint_a"]; gone {
			t.Error("the three-fragment sentence is back")
		}
		if h := en["server.public.zip_hint"]; !strings.Contains(h, "{link}") {
			t.Errorf("server.public.zip_hint has no {link}: %q", h)
		}
		if en["server.public.zip_link"] == "" {
			t.Error("server.public.zip_link is missing")
		}
	})
}
