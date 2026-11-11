// Package htmlx is what happens to a rendered template's HTML before anybody
// sees it: Sanitize (no script can come out of a template), InlineCSS (a
// mail's stylesheet written into its elements, the way mail programs read
// it) and Text (a mail's plain-text version, made from its HTML).
package htmlx

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// dropped are the elements a template may not produce, with everything inside
// them: what runs a script or loads another document into the page.
var dropped = map[string]bool{
	"script": true, "iframe": true, "frameset": true, "object": true,
	"applet": true, "portal": true, "noembed": true,
}

// droppedTag are the elements taken out without their content (they have
// none, or it is the page's): <base> (it would point every relative link of
// the page somewhere else), <embed>, <frame>, and SVG animation, which can set
// an attribute - an href - to a javascript: address after the page has
// loaded (<set attributeName="href" to="javascript:…">).
var droppedTag = map[string]bool{
	"base": true, "embed": true, "frame": true,
	"set": true, "animate": true, "animatemotion": true, "animatetransform": true,
}

// urlAttrs are the attributes that hold an address.
var urlAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true, "poster": true,
	"background": true, "cite": true, "data": true, "xlink:href": true,
	"longdesc": true, "manifest": true, "ping": true,
}

// Sanitize is the HTML a template rendered, with everything a template is not
// allowed to say taken out:
//
//   - the elements of `dropped`, with their content;
//   - every event handler attribute (on…), srcdoc and formaction;
//   - an address that is not http(s), mailto:, tel:, relative, or (for an
//     image) a data:image/…: javascript:, vbscript:, data:text/html…;
//   - a <meta http-equiv> (a refresh to elsewhere, a Content-Security-Policy
//     of the template's own);
//   - a <form> or <link> pointing to another site (form-action and the
//     page's stylesheets stay filex's).
//
// ⚠ It is the second wall, not the first: public pages also carry a
// Content-Security-Policy whose script-src only filex's own nonce'd scripts
// satisfy (docs/TEMPLATES.md "Security"), and a mail program runs no script.
// It is here so that a mail - which has no CSP - and an old browser get no
// script either, and so the editor's preview shows what was taken out.
//
// The template's own text and markup are kept byte for byte where they are
// allowed (z.Raw): a <style> block's selectors are not re-escaped.
func Sanitize(src string) string {
	out, _ := SanitizeReport(src)
	return out
}

// SanitizeReport is Sanitize, and what it took out ("<script>", "onclick",
// "href=javascript:…"), once each - what the editor tells the person who
// wrote the template.
func SanitizeReport(src string) (string, []string) {
	var removed []string
	seen := map[string]bool{}
	note := func(what string) {
		if !seen[what] {
			seen[what] = true
			removed = append(removed, what)
		}
	}
	z := html.NewTokenizer(strings.NewReader(src))
	var out bytes.Buffer
	out.Grow(len(src))
	skip := "" // the dropped element we are inside, until its end tag
	depth := 0
	rawText := false // the text token is a <style>'s (or a <title>'s…): as written
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			rawText = false
			if skip != "" {
				if tag == skip && tt == html.StartTagToken {
					depth++
				}
				continue
			}
			if droppedTag[tag] || !validName(tag) {
				note("<" + tag + ">")
				continue
			}
			if dropped[tag] {
				note("<" + tag + ">")
				if tt == html.StartTagToken {
					skip, depth = tag, 1
				}
				continue
			}
			var attrs []html.Attribute
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs = append(attrs, html.Attribute{Key: string(k), Val: string(v)})
			}
			kept, ok := allowedAttrs(tag, attrs, note)
			if !ok {
				continue
			}
			writeTag(&out, tag, kept, tt == html.SelfClosingTagToken)
			rawText = tt == html.StartTagToken && rawTextElems[tag]
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			rawText = false
			if skip != "" {
				if tag == skip {
					depth--
					if depth == 0 {
						skip = ""
					}
				}
				continue
			}
			if dropped[tag] || droppedTag[tag] || !validName(tag) {
				continue
			}
			out.WriteString("</" + tag + ">")
		case html.TextToken, html.CommentToken, html.DoctypeToken:
			if skip != "" {
				continue
			}
			if tt == html.CommentToken && conditionalComment(raw) {
				// <!--[if mso]> … <![endif]--> is markup a mail program
				// reads; it is checked like the rest of the page.
				out.WriteString(sanitizeConditional(string(raw)))
				continue
			}
			if tt == html.TextToken && !rawText {
				// ⚠ Escaped, never as written: text on both sides of a
				// dropped element would otherwise join into new markup -
				// `<<script>x</script>img src=x onerror=…>` left "<" and
				// "img src=x onerror=…>", which is an <img> with a handler.
				out.WriteString(html.EscapeString(string(z.Text())))
				continue
			}
			out.Write(raw)
		}
	}
	return out.String(), removed
}

// allowedAttrs answers the attributes of tag that may stay, and whether the
// element stays at all.
func allowedAttrs(tag string, attrs []html.Attribute, note func(string)) ([]html.Attribute, bool) {
	if tag == "meta" {
		for _, a := range attrs {
			if strings.EqualFold(a.Key, "http-equiv") {
				note("<meta http-equiv>")
				return nil, false
			}
		}
	}
	if tag == "link" {
		for _, a := range attrs {
			if strings.EqualFold(a.Key, "href") && !relativeURL(a.Val) {
				// A stylesheet from another site: allowed (a web font),
				// the page's CSP lets styles through and nothing else.
				rel := attr(attrs, "rel")
				if !strings.EqualFold(strings.TrimSpace(rel), "stylesheet") {
					note("<link rel=" + rel + ">")
					return nil, false
				}
			}
		}
	}
	out := attrs[:0]
	for _, a := range attrs {
		k := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(k, "on"), k == "srcdoc", k == "formaction":
			note(k)
			continue
		case k == "action" && tag == "form":
			if !relativeURL(a.Val) {
				note("action=" + a.Val)
				continue // the form posts to the page it is on
			}
		case k == "srcset":
			if !safeSrcset(a.Val) {
				note("srcset")
				continue
			}
		case urlAttrs[k]:
			if !safeURL(a.Val, tag == "img" && k == "src") {
				note(k + "=" + scheme(html.UnescapeString(a.Val)) + ":")
				continue
			}
		case k == "style":
			if unsafeStyle(a.Val) {
				note("style")
				continue
			}
		}
		out = append(out, a)
	}
	return out, true
}

// rawTextElems are the elements whose text the browser reads as written.
var rawTextElems = map[string]bool{"style": true, "title": true, "textarea": true, "xmp": true, "noframes": true, "noscript": true}

// validName: a tag name as HTML spells one. The tokenizer, like a browser,
// takes "scr<script" for a name; such an element is nothing a page means, and
// writing it back keeps a "<script" in the output for the next reader.
func validName(tag string) bool {
	if tag == "" {
		return false
	}
	for _, r := range tag {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == ':' || r == '_') {
			return false
		}
	}
	return true
}

func attr(attrs []html.Attribute, key string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func writeTag(b *bytes.Buffer, tag string, attrs []html.Attribute, selfClosing bool) {
	b.WriteByte('<')
	b.WriteString(tag)
	for _, a := range attrs {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(a.Val))
		b.WriteByte('"')
	}
	if selfClosing {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
}

// scheme is u's scheme, lowercased, as a browser reads it (tabs and newlines
// inside it dropped); "" for a relative address.
func scheme(u string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x21 {
			return -1
		}
		return r
	}, u)
	colon := strings.IndexByte(clean, ':')
	if colon < 0 {
		return ""
	}
	if cut := strings.IndexAny(clean, "/?#"); cut >= 0 && cut < colon {
		return ""
	}
	return strings.ToLower(clean[:colon])
}

func relativeURL(u string) bool {
	t := strings.TrimSpace(u)
	return scheme(t) == "" && !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, `\\`) && !strings.HasPrefix(t, `/\`)
}

func safeURL(u string, image bool) bool {
	switch s := scheme(html.UnescapeString(u)); s {
	case "", "http", "https", "mailto", "tel", "cid":
		return true
	case "data":
		if !image {
			return false
		}
		rest := strings.ToLower(strings.TrimSpace(html.UnescapeString(u)))
		rest = strings.TrimPrefix(rest, "data:")
		return strings.HasPrefix(rest, "image/") && !strings.HasPrefix(rest, "image/svg")
	}
	return false
}

func safeSrcset(v string) bool {
	for _, part := range strings.Split(v, ",") {
		f := strings.Fields(part)
		if len(f) > 0 && !safeURL(f[0], true) {
			return false
		}
	}
	return true
}

// unsafeStyle: a style attribute that reaches for script the way old engines
// let it (expression(), javascript: in url(), behavior/binding).
func unsafeStyle(v string) bool {
	l := strings.ToLower(html.UnescapeString(v))
	l = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\\' {
			return -1
		}
		return r
	}, l)
	for _, bad := range []string{"expression(", "javascript:", "vbscript:", "behavior:", "-moz-binding"} {
		if strings.Contains(l, bad) {
			return true
		}
	}
	return false
}

func conditionalComment(raw []byte) bool {
	return bytes.HasPrefix(raw, []byte("<!--[if"))
}

// sanitizeConditional checks the markup inside a mail program's conditional
// comment like the page around it.
func sanitizeConditional(c string) string {
	open := strings.Index(c, "]>")
	end := strings.LastIndex(c, "<![endif]")
	if open < 0 || end < open {
		return ""
	}
	inner, _ := SanitizeReport(c[open+2 : end])
	return c[:open+2] + inner + c[end:]
}
