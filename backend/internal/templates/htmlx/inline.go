package htmlx

import (
	"bytes"
	"sort"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// InlineCSS writes a mail's stylesheet into its elements' style attributes,
// the way mail programs read a message: several of them (Gmail's apps,
// Outlook) drop <style> or most of it.
//
// What it does, and what it leaves:
//
//   - every rule of every <style> in the document whose selector cascadia
//     can match (type, class, id, attribute, descendant/child, :first-child…)
//     is applied to the elements it matches, by specificity and then source
//     order, as a browser would; !important wins over everything but an
//     !important of the element's own style attribute; the element's own
//     style attribute wins over the sheet;
//   - rules it cannot apply to an element - @media, @font-face, @supports,
//     and selectors with a state (:hover, :focus, ::before) - stay in a
//     <style> in the head, for the programs that read one (dark mode, a
//     narrow screen);
//   - a <style data-inline="keep"> is left exactly as written.
//
// The input is a template filex rendered and Sanitize already read; an
// unreadable sheet is not an error, its rules are kept for the programs that
// read <style>.
func InlineCSS(src string) (string, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", err
	}
	var sheets []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "style" && attrOf(n, "data-inline") != "keep" {
			sheets = append(sheets, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(sheets) == 0 {
		return src, nil
	}

	type applied struct {
		spec  [3]int
		order int
		imp   bool
		prop  string
		value string
	}
	byNode := map[*html.Node][]applied{}
	order := 0
	var kept []string
	for _, st := range sheets {
		css := textOf(st)
		rules, rest := splitRules(css)
		kept = append(kept, rest...)
		for _, r := range rules {
			for _, selText := range splitSelectors(r.selector) {
				if stateful(selText) {
					kept = append(kept, selText+"{"+r.body+"}")
					continue
				}
				sel, err := cascadia.Parse(selText)
				if err != nil {
					kept = append(kept, selText+"{"+r.body+"}")
					continue
				}
				spec := sel.Specificity()
				decls := parseDecls(r.body)
				for _, n := range cascadia.QueryAll(doc, sel) {
					for _, d := range decls {
						order++
						byNode[n] = append(byNode[n], applied{spec: [3]int(spec), order: order, imp: d.important, prop: d.prop, value: d.value})
					}
				}
			}
		}
	}
	for n, list := range byNode {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.imp != b.imp {
				return !a.imp
			}
			if a.spec != b.spec {
				return lessSpec(a.spec, b.spec)
			}
			return a.order < b.order
		})
		final := map[string]string{}
		var props []string
		for _, a := range list {
			if _, seen := final[a.prop]; !seen {
				props = append(props, a.prop)
			}
			final[a.prop] = a.value
			if a.imp {
				final[a.prop] = a.value + " !important"
			}
		}
		own := parseDecls(attrOf(n, "style"))
		for _, d := range own {
			if prev, ok := final[d.prop]; ok && strings.HasSuffix(prev, "!important") && !d.important {
				continue
			}
			if _, seen := final[d.prop]; !seen {
				props = append(props, d.prop)
			}
			final[d.prop] = d.value
			if d.important {
				final[d.prop] += " !important"
			}
		}
		var b strings.Builder
		for _, p := range props {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			b.WriteString(p)
			b.WriteString(": ")
			b.WriteString(final[p])
		}
		setAttr(n, "style", b.String())
	}
	// The sheets are replaced by what could not be inlined, in the head.
	for i, st := range sheets {
		if i == 0 && len(kept) > 0 {
			for c := st.FirstChild; c != nil; {
				next := c.NextSibling
				st.RemoveChild(c)
				c = next
			}
			st.AppendChild(&html.Node{Type: html.TextNode, Data: strings.Join(kept, "\n")})
			continue
		}
		st.Parent.RemoveChild(st)
	}
	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return "", err
	}
	return out.String(), nil
}

func lessSpec(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

type cssRule struct{ selector, body string }

// splitRules reads a stylesheet into its plain rules and the text of the
// at-rules (kept as written).
func splitRules(css string) ([]cssRule, []string) {
	css = stripComments(css)
	var rules []cssRule
	var rest []string
	i := 0
	for i < len(css) {
		open := strings.IndexByte(css[i:], '{')
		if open < 0 {
			break
		}
		head := strings.TrimSpace(css[i : i+open])
		// The block's end, counting nested braces (an @media holds rules).
		depth, j := 0, i+open
		for ; j < len(css); j++ {
			if css[j] == '{' {
				depth++
			} else if css[j] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if j >= len(css) {
			break
		}
		body := css[i+open+1 : j]
		if strings.HasPrefix(head, "@") {
			rest = append(rest, head+"{"+body+"}")
		} else if head != "" {
			rules = append(rules, cssRule{selector: head, body: body})
		}
		i = j + 1
	}
	return rules, rest
}

func stripComments(css string) string {
	for {
		a := strings.Index(css, "/*")
		if a < 0 {
			return css
		}
		b := strings.Index(css[a+2:], "*/")
		if b < 0 {
			return css[:a]
		}
		css = css[:a] + css[a+2+b+2:]
	}
}

func splitSelectors(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stateful: a selector that depends on what the reader does or on generated
// content - a style attribute cannot say it.
func stateful(sel string) bool {
	for _, s := range []string{":hover", ":focus", ":active", ":visited", "::", ":before", ":after", ":target", ":checked", ":link"} {
		if strings.Contains(sel, s) {
			return true
		}
	}
	return false
}

type decl struct {
	prop, value string
	important   bool
}

func parseDecls(body string) []decl {
	var out []decl
	for _, part := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		imp := false
		if l := strings.ToLower(v); strings.HasSuffix(l, "!important") {
			imp = true
			v = strings.TrimSpace(v[:len(v)-len("!important")])
		}
		out = append(out, decl{prop: k, value: v, important: imp})
	}
	return out
}

func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func textOf(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}
