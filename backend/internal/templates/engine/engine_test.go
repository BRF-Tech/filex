package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

type fakeHost struct{}

func (fakeHost) Call(fn string, args []any) (string, error) {
	switch fn {
	case "t", "tpl":
		key := args[0].(string)
		vars := args[1].(map[string]string)
		tpl := map[string]string{
			"mail.greeting":   "Hello,",
			"mail.share.name": "{name} was shared with you",
			"public.zip_hint": "If nothing happens, {link}.",
			"public.zip_link": "download it here",
			"mail.valid_days": "Valid for {count} days",
		}[key]
		if tpl == "" {
			return key, nil
		}
		if fn == "tpl" {
			return tpl, nil
		}
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			tpl = strings.ReplaceAll(tpl, "{"+k+"}", vars[k])
		}
		return tpl, nil
	case "bytes":
		return fmt.Sprintf("%d B", args[0]), nil
	}
	return "", fmt.Errorf("no %s", fn)
}

const hostile = `<script>alert(1)</script>"'&`

func renderHTML(t *testing.T, src string, data map[string]any) string {
	t.Helper()
	out, err := Render(Options{Mode: HTML, Host: fakeHost{}}, "test", src, data)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return out
}

func TestOutputIsEscapedAndEscapedOnce(t *testing.T) {
	d := map[string]any{"x": hostile}
	const once = "&lt;script&gt;alert(1)&lt;/script&gt;&#34;&#39;&amp;"
	for _, src := range []string{
		`{{ x }}`,
		`{{ x | escape }}`,
		`{{ x | escape_once }}`,
		`{% capture c %}{{ x }}{% endcapture %}{{ c }}`,
		`{% capture c %}{{ x }}{% endcapture %}{{ c | strip }}`,
		`{% assign y = x %}{{ y }}`,
		`{{ x | safe }}`,
	} {
		got := renderHTML(t, src, d)
		if got != once && got != strings.ReplaceAll(once, "&#34;", "&#34;") {
			// escape uses html.EscapeString, which spells the quote &#34; too.
			if got != "&lt;script&gt;alert(1)&lt;/script&gt;&#34;&#39;&amp;" {
				t.Errorf("%s = %q, want the text escaped exactly once", src, got)
			}
		}
	}
}

func TestNewlineToBrEscapesFirst(t *testing.T) {
	got := renderHTML(t, `{{ x | newline_to_br }}`, map[string]any{"x": "a<b>\nc"})
	if got != "a&lt;b&gt;<br>\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestFiltersReadCapturedHTMLAsText(t *testing.T) {
	got := renderHTML(t, `{% capture c %}  Ada  {% endcapture %}{{ c | strip | size }}|{{ c | upcase | strip }}`, nil)
	if got != "3|ADA" {
		t.Fatalf("got %q", got)
	}
}

func TestSafeDoesNotLiftEscaping(t *testing.T) {
	got := renderHTML(t, `{{ x | safe }}`, map[string]any{"x": "<b>"})
	if got != "&lt;b&gt;" {
		t.Fatalf("got %q", got)
	}
}

func TestMarkSafeWritesFilexMarkupAsIs(t *testing.T) {
	d := map[string]any{"page": map[string]any{"content": "<p>ok</p>"}, "x": "<p>no</p>"}
	MarkSafe(d, []string{"page.content", "missing.path", "x.y"})
	got := renderHTML(t, `{{ page.content }}{{ x }}`, d)
	if got != "<p>ok</p>&lt;p&gt;no&lt;/p&gt;" {
		t.Fatalf("got %q", got)
	}
}

// ⚠ Red-proven against the library's default store: before
// RegisterTemplateStore(partialStore), `{% include 'go.mod' %}` printed the
// go.mod of the working directory (measured 2026-11-11 with osteele/liquid
// v1.9.2's NewEngine).
func TestIncludeReadsOnlyTheSharedPieces(t *testing.T) {
	if err := os.WriteFile("go.mod.probe", []byte("SECRET-FROM-DISK"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove("go.mod.probe") })
	for _, src := range []string{
		`{% include 'go.mod.probe' %}`,
		`{% render 'go.mod.probe' %}`,
		`{% include '../engine/go.mod.probe' %}`,
		`{% include '/etc/passwd' %}`,
	} {
		out, err := Render(Options{Mode: HTML, Host: fakeHost{}}, "test", src, nil)
		if err == nil || strings.Contains(out, "SECRET") {
			t.Errorf("%s = %q, %v; want an error and nothing from the disk", src, out, err)
		}
	}
	out, err := Render(Options{Mode: HTML, Partials: map[string]string{"brand": "<b>{{ name }}</b>"}}, "test",
		`{% include 'brand' %}`, map[string]any{"name": "A&B"})
	if err != nil || out != "<b>A&amp;B</b>" {
		t.Fatalf("a registered piece: %q, %v", out, err)
	}
}

func TestCycleIsNotPartOfTheDialect(t *testing.T) {
	if _, err := Render(Options{Mode: HTML}, "test", `{% cycle 'a', 'b' %}`, nil); err == nil {
		t.Fatal("cycle rendered; it must be refused (osteele/liquid #150)")
	}
}

func TestTranslateAndFill(t *testing.T) {
	d := map[string]any{"name": "<x>.pdf", "n": 3}
	got := renderHTML(t, `{{ 'mail.greeting' | t }} {{ 'mail.share.name' | t: name: name }} {{ 'mail.valid_days' | t: count: n }}`, d)
	if got != "Hello, &lt;x&gt;.pdf was shared with you Valid for 3 days" {
		t.Fatalf("got %q", got)
	}
	got = renderHTML(t, `{% assign a = 'public.zip_link' | t | link_to: '?zip=wait', 'dl' %}{{ 'public.zip_hint' | t_html: link: a }}`, nil)
	if got != `If nothing happens, <a href="?zip=wait" id="dl">download it here</a>.` {
		t.Fatalf("t_html: %q", got)
	}
	got = renderHTML(t, `{{ 'mail.share.name' | t_html: name: name }}`, d)
	if got != "&lt;x&gt;.pdf was shared with you" {
		t.Fatalf("t_html with text: %q", got)
	}
}

func TestLinkToRefusesScriptAddresses(t *testing.T) {
	for _, href := range []string{"javascript:alert(1)", " JavaScript:x", "java\tscript:x", "data:text/html,x", "vbscript:x"} {
		got := renderHTML(t, `{{ 'x' | link_to: h }}`, map[string]any{"h": href})
		if !strings.HasPrefix(got, `<a href="#">`) {
			t.Errorf("%q → %s", href, got)
		}
	}
	for _, href := range []string{"https://a.example/b?c=d&e", "/s/abc", "?zip=wait", "#top", "mailto:a@example.com", "./a:b"} {
		got := renderHTML(t, `{{ 'x' | link_to: h }}`, map[string]any{"h": href})
		if strings.HasPrefix(got, `<a href="#">`) && href != "#top" {
			t.Errorf("%q was refused: %s", href, got)
		}
	}
}

func TestTextModeWritesAsIs(t *testing.T) {
	out, err := Render(Options{Mode: Text, Host: fakeHost{}}, "subject", `{{ 'mail.share.name' | t: name: x }}`, map[string]any{"x": "a & <b>"})
	if err != nil || out != "a & <b> was shared with you" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestStrictNamesTheMissingVariable(t *testing.T) {
	_, err := Render(Options{Mode: HTML, Strict: true}, "mail.share", "line one\n{{ item.nmae }}", map[string]any{"item": map[string]any{"name": "a"}})
	if err == nil || !strings.Contains(err.Error(), "2") {
		t.Fatalf("want an error on line 2, got %v", err)
	}
}

func TestSyntaxErrorCarriesTheLine(t *testing.T) {
	err := Check(Options{Mode: HTML}, "page.pin", "<p>\n{% if x %}\n")
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("got %v", err)
	}
}

func TestCaptureInsideRawStaysText(t *testing.T) {
	got := renderHTML(t, `{% raw %}{% capture x %}{% endraw %}|{%- capture y -%}A{%- endcapture -%}{{ y }}`, nil)
	if got != "{% capture x %}|A" {
		t.Fatalf("got %q", got)
	}
	_, err := Render(Options{Mode: HTML}, "t", `{% capture %}x{% endcapture %}`, nil)
	if err == nil || strings.Contains(err.Error(), captureName) || !strings.Contains(err.Error(), "capture") {
		t.Fatalf("the error must say capture, as written: %v", err)
	}
}

// The library says "undefined variable" without naming it: the error quotes
// the line, so the editor shows which.
func TestErrorsQuoteTheLine(t *testing.T) {
	_, err := Render(Options{Mode: HTML, Strict: true}, "mail.share", "<p>ok</p>\n<p>{{ item.nmae }}</p>", map[string]any{"item": map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "{{ item.nmae }}") {
		t.Fatalf("got %v", err)
	}
}
