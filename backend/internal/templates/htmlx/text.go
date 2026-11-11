package htmlx

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Text is the plain-text version of a mail's HTML: the part a mail program
// shows when it shows no HTML, and what a screen reader of a text-only
// program reads. Paragraphs and headings are separated by a blank line, a
// list item starts with "- ", a table row is one line, a link whose text is
// not its address is followed by the address, an image by its alt text.
// What a reader never sees - <head>, <style>, an element hidden with
// display:none (a preheader) - is left out.
//
// Two attributes let a template say what its HTML means in plain text:
// data-text="skip" leaves an element out (the address under a button,
// written for a program that shows no buttons, would be said twice), and on
// a link data-text="href" writes the address alone (a button's label means
// nothing without it), data-text="label" its label alone (a name that is a
// link only for those who can click it).
func Text(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return ""
	}
	t := &textWriter{}
	t.node(doc)
	return t.result()
}

type textWriter struct {
	b       strings.Builder
	pending int // line breaks owed before the next text
	started bool
}

// breakLines asks for n line breaks (at most) before the next text.
func (t *textWriter) breakLines(n int) {
	if t.started && n > t.pending {
		t.pending = n
	}
}

func (t *textWriter) write(s string) {
	if s == "" {
		return
	}
	if t.pending > 0 {
		t.b.WriteString(strings.Repeat("\n", t.pending))
		t.pending = 0
	}
	t.b.WriteString(s)
	t.started = true
}

var blockElems = map[string]int{
	"p": 2, "div": 1, "h1": 2, "h2": 2, "h3": 2, "h4": 2, "h5": 2, "h6": 2,
	"ul": 2, "ol": 2, "table": 2, "tr": 1, "blockquote": 2, "pre": 2,
	"section": 2, "article": 2, "header": 2, "footer": 2, "main": 2, "hr": 2,
	"center": 1, "dl": 2, "dt": 1, "dd": 1, "address": 2, "figure": 2,
}

func hidden(n *html.Node) bool {
	style := strings.ToLower(strings.ReplaceAll(attrOf(n, "style"), " ", ""))
	if strings.Contains(style, "display:none") || strings.Contains(style, "mso-hide:all") {
		return true
	}
	_, isHidden := attrKey(n, "hidden")
	return isHidden || strings.EqualFold(attrOf(n, "aria-hidden"), "true") && n.Data != "a"
}

func attrKey(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func (t *textWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		t.text(n.Data)
		return
	case html.ElementNode:
		switch n.Data {
		case "head", "style", "script", "title", "template", "noscript":
			return
		case "br":
			t.pending++
			if !t.started {
				t.pending = 0
			}
			return
		case "img":
			if alt := strings.TrimSpace(attrOf(n, "alt")); alt != "" {
				t.write(alt)
			}
			return
		}
		if hidden(n) || attrOf(n, "data-text") == "skip" {
			return
		}
	}
	if n.Type == html.ElementNode {
		if gap, ok := blockElems[n.Data]; ok {
			t.breakLines(gap)
		}
		switch n.Data {
		case "li":
			t.breakLines(1)
			t.write("- ")
		case "td", "th":
			if n.PrevSibling != nil {
				t.write(" ")
			}
		case "a":
			t.anchor(n)
			return
		case "hr":
			t.write("----")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		t.node(c)
	}
	if n.Type == html.ElementNode {
		if gap, ok := blockElems[n.Data]; ok {
			t.breakLines(gap)
		}
		if n.Data == "li" {
			t.breakLines(1)
		}
	}
}

func (t *textWriter) anchor(n *html.Node) {
	inner := &textWriter{}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		inner.node(c)
	}
	label := strings.TrimSpace(inner.result())
	href := strings.TrimSpace(attrOf(n, "href"))
	switch attrOf(n, "data-text") {
	case "href":
		if href != "" && !strings.HasPrefix(href, "#") {
			t.write(href)
			return
		}
	case "label":
		t.write(label)
		return
	}
	switch {
	case href == "" || strings.HasPrefix(href, "#"):
		t.write(label)
	case label == "" || label == href || "mailto:"+label == href:
		t.write(href)
	default:
		t.write(label + ": " + href)
	}
}

// text writes a text node with its white space collapsed the way HTML shows
// it (a run of spaces and line breaks is one space).
func (t *textWriter) text(s string) {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) && r != ' ' {
			space = true
			continue
		}
		if space && (b.Len() > 0 || (t.started && t.pending == 0 && !endsWithSpace(&t.b))) {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if space && b.Len() > 0 {
		b.WriteByte(' ')
	}
	out := b.String()
	if t.pending > 0 || !t.started {
		out = strings.TrimLeft(out, " ")
	}
	t.write(strings.ReplaceAll(out, " ", " "))
}

func endsWithSpace(b *strings.Builder) bool {
	s := b.String()
	return s == "" || strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\n")
}

func (t *textWriter) result() string {
	lines := strings.Split(t.b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
