// Package engine is the Liquid dialect filex's editable templates are written
// in: the mail and public-page templates an administrator can change
// (internal/templates). It is the ONE definition of that dialect - which tags
// and filters exist, how output is escaped, where `include` reads from - and
// it is compiled twice: into the server, which renders the templates filex
// ships, and into the WebAssembly guest (internal/templates/sandbox/guest),
// which renders a template somebody edited. A template therefore means the
// same thing wherever it runs.
//
// ⚠⚠ Nothing here may import server packages. The guest is built for
// GOOS=wasip1 from this package and the Liquid library alone; whatever the
// filters need from the server (a sentence of the catalogue, a size in the
// reader's language) they ask through Host, which the server answers
// in-process and the guest answers through a host import.
//
// The rules, each with the measurement that made it (docs/TEMPLATES.md,
// "Security"):
//
//   - Output is HTML-escaped (Mode HTML): `{{ x }}` cannot write markup,
//     whatever x holds. osteele/liquid's own `safe` filter would lift that,
//     so `safe` is redefined to do nothing; the only values written as they
//     are come from filex itself (MarkSafe) or from a filter of this package
//     that escaped them first (escape, newline_to_br, link_to, t_html).
//   - `escape` and `escape_once` answer escaped text that is not escaped a
//     second time, `capture` keeps what it captured as the HTML it already is,
//     and `newline_to_br` escapes the text before it adds the <br>: without
//     these three, measured, the library's own versions printed "&amp;lt;"
//     where the reader should have seen "<".
//   - `include` and `render` read only the shared pieces handed to New
//     (Options.Partials). The library's default store reads files from the
//     process's working directory - `{% include 'go.mod' %}` printed go.mod.
//   - `cycle` is not part of the dialect: its renderer shares an error
//     variable between renders (osteele/liquid #150, a data race).
//
// What this package does NOT do is bound the work a template can make:
// Liquid has loops and string building, and the library has no budget
// (`{% for i in (1..1000000000) %}{% endfor %}` runs to the end, a string
// doubled thirty times is ten gigabytes). A template only filex wrote is
// rendered here directly; every edited one runs inside the sandbox, whose
// time, memory and output ceilings are hard.
package engine

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/osteele/liquid"
	"github.com/osteele/liquid/render"
	"github.com/osteele/liquid/values"
)

// Mode is how a template's output is written.
type Mode int

const (
	// HTML escapes every `{{ }}` output: an HTML mail body, a page.
	HTML Mode = iota
	// Text writes outputs as they are: a mail's subject line and its
	// plain-text body, which no browser parses.
	Text
)

// Host answers what a filter needs from the server. fn is one of:
//
//	"t"     args [key, vars]        → the sentence, placeholders filled
//	"tpl"   args [key, vars]        → the sentence with its placeholders as written
//	"bytes" args [n]                → a size in the reader's language ("1.2 MB")
//
// key is a catalogue key without the `server.` prefix ("mail.greeting");
// vars is a map[string]string, and its "count" entry picks the plural form.
type Host interface {
	Call(fn string, args []any) (string, error)
}

// Options configures one render.
type Options struct {
	Mode Mode
	Host Host
	// Partials are the shared pieces `{% include 'name' %}` may read, by name.
	// Nothing else is readable: there is no file system behind include.
	Partials map[string]string
	// Strict makes a variable the data does not have an error rather than an
	// empty string - how a template is checked before it is saved, against
	// sample data that has every variable. Real renders are lax: an optional
	// value that is missing must never cost somebody their mail.
	Strict bool
}

// ErrNoHost is what a filter that needs the server answers without one.
var ErrNoHost = errors.New("this template filter needs the server")

// New is a Liquid engine speaking filex's dialect.
func New(o Options) *liquid.Engine {
	e := liquid.NewEngine()
	if o.Mode == HTML {
		e.SetAutoEscapeReplacer(render.HtmlEscaper)
	}
	if o.Strict {
		e.StrictVariables()
	}
	e.RegisterTemplateStore(partialStore(o.Partials))
	e.UnregisterTag("cycle")
	registerFilters(e, o)
	e.RegisterBlock(captureName, captureBlock(o.Mode))
	return e
}

// captureName is the block `capture` is parsed as. The library cannot have a
// block defined twice (it panics) nor taken away, so the template's
// `{% capture %}` / `{% endcapture %}` tags are renamed to this one before
// parsing (renameCapture) and errors say `capture` again (sourceError).
const captureName = "filex_capture"

// tagToken is the library's own tag and object pattern (parser/scanner.go,
// default delimiters): `{{ … }}`, or `{%-? name args -?%}`.
var tagToken = regexp.MustCompile(`\{\{-?\s*(.+?)\s*-?\}\}|\{%-?\s*(\w+)(?:\s+((?:[^%]|%[^}])+?))?\s*-?%\}`)

// renameCapture renames the capture tags of src, the way the library's
// parser reads them: not inside {% raw %} or {% comment %}, where tags are
// text. Lines and columns stay where they were (the names are spelled in
// place), so an error still points at the template's own line.
func renameCapture(src string) string {
	if !strings.Contains(src, "capture") && !strings.Contains(src, ":") {
		return src
	}
	var b strings.Builder
	last := 0
	inside := "" // "raw" or "comment" while the parser treats tags as text
	// put copies src up to from, then repl in place of src[from:to].
	put := func(from, to int, repl string) {
		b.WriteString(src[last:from])
		b.WriteString(repl)
		last = to
	}
	for _, m := range tagToken.FindAllStringSubmatchIndex(src, -1) {
		if m[2] >= 0 { // an object, {{ … }}
			if inside == "" {
				if s := keywordFirst(src[m[2]:m[3]]); s != src[m[2]:m[3]] {
					put(m[2], m[3], s)
				}
			}
			continue
		}
		name := src[m[4]:m[5]]
		if inside != "" {
			if name == "end"+inside {
				inside = ""
			}
			continue
		}
		switch name {
		case "raw", "comment":
			inside = name
		case "capture", "endcapture":
			put(m[4], m[5], strings.Replace(name, "capture", captureName, 1))
		default:
			if m[6] >= 0 {
				if s := keywordFirst(src[m[6]:m[7]]); s != src[m[6]:m[7]] {
					put(m[6], m[7], s)
				}
			}
		}
	}
	if last == 0 {
		return src
	}
	b.WriteString(src[last:])
	return b.String()
}

// keywordStart is a filter whose first argument is a keyword one:
// `| t: name: item.name`.
var keywordStart = regexp.MustCompile(`(\|\s*[A-Za-z_]\w*\s*:)(\s*)([A-Za-z_][\w-]*\s*:)`)

// keywordFirst lets a filter's arguments start with a keyword one, as in
// Shopify (`{{ 'mail.share.subject_file' | t: name: item.name }}`). The
// library's grammar only takes keywords after a positional argument, so a
// `nil` one is put first; the filters of this package read only keywords.
func keywordFirst(expr string) string {
	if !strings.Contains(expr, ":") {
		return expr
	}
	return keywordStart.ReplaceAllString(expr, "${1}${2}nil, ${3}")
}

// sourceError is err with the renamed capture spelled as the template wrote it.
// sourceError is err as the template's author reads it: the renamed capture
// spelled `capture` again, and the line it happened on quoted - the library
// says "undefined variable" without saying which, and the line shows it.
func sourceError(err error, name, src string) error {
	if err == nil {
		return nil
	}
	line := ""
	var se interface{ LineNumber() int }
	if errors.As(err, &se) && se.LineNumber() > 0 {
		if l := lineOf(src, se.LineNumber()); l != "" && strings.Contains(err.Error(), name) {
			line = l
		}
	}
	if line == "" && !strings.Contains(err.Error(), captureName) {
		return err
	}
	return renamedError{err: err, line: line}
}

// lineOf is line n of src, trimmed and cut to a readable length.
func lineOf(src string, n int) string {
	lines := strings.Split(src, "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	l := strings.TrimSpace(strings.TrimRight(lines[n-1], "\r"))
	if r := []rune(l); len(r) > 160 {
		l = string(r[:160]) + "…"
	}
	return strings.ReplaceAll(l, captureName, "capture")
}

type renamedError struct {
	err  error
	line string
}

func (e renamedError) Error() string {
	msg := strings.ReplaceAll(e.err.Error(), captureName, "capture")
	if e.line != "" {
		msg += ": " + e.line
	}
	return msg
}
func (e renamedError) Unwrap() error { return e.err }

// Render parses and renders src. name is the template's name in error
// messages ("mail.share"); every error carries the line it happened on.
func Render(o Options, name, src string, data map[string]any) (string, error) {
	var b strings.Builder
	if err := RenderTo(&b, o, name, src, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderTo is Render writing to w.
func RenderTo(w io.Writer, o Options, name, src string, data map[string]any) error {
	e := New(o)
	tpl, perr := e.ParseTemplateLocation([]byte(renameCapture(src)), name, 1)
	if perr != nil {
		return sourceError(perr, name, src)
	}
	if err := tpl.FRender(w, data); err != nil {
		return sourceError(err, name, src)
	}
	return nil
}

// Check parses src and reports its first syntax error, without rendering.
func Check(o Options, name, src string) error {
	_, err := New(o).ParseTemplateLocation([]byte(renameCapture(src)), name, 1)
	if err != nil {
		return sourceError(err, name, src)
	}
	return nil
}

// partialStore is the only thing `include` and `render` can read. A piece is
// a template of its own, so its capture tags are renamed like the page's.
type partialStore map[string]string

func (p partialStore) ReadTemplate(name string) ([]byte, error) {
	if src, ok := p[strings.TrimSpace(name)]; ok {
		return []byte(renameCapture(src)), nil
	}
	return nil, fmt.Errorf("there is no shared piece named %q", name)
}

// captureBlock is `{% capture x %}…{% endcapture %}`. In HTML mode the inner
// output is already escaped HTML, so it is kept as HTML (SafeValue): the
// library's own capture stored it as a plain string, and `{{ x }}` escaped
// it a second time (measured: "<" came out as "&amp;lt;").
func captureBlock(mode Mode) liquid.Renderer {
	return func(ctx render.Context) (string, error) {
		name := strings.TrimSpace(ctx.TagArgs())
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return "", fmt.Errorf("capture needs exactly one variable name, got %q", ctx.TagArgs())
		}
		s, err := ctx.InnerString()
		if err != nil {
			return "", err
		}
		if mode == HTML {
			ctx.Set(name, values.SafeValue{Value: s})
		} else {
			ctx.Set(name, s)
		}
		return "", nil
	}
}

// MarkSafe wraps the values at the given dotted paths as HTML that is written
// as it is - markup filex itself produced (the content a layout frames, the
// page's theme stylesheet). A path that does not exist, or does not end at a
// string, is left alone.
func MarkSafe(data map[string]any, paths []string) {
	for _, p := range paths {
		parts := strings.Split(p, ".")
		m := data
		for i, k := range parts {
			v, ok := m[k]
			if !ok {
				break
			}
			if i == len(parts)-1 {
				if s, ok := v.(string); ok {
					m[k] = values.SafeValue{Value: s}
				}
				break
			}
			next, ok := v.(map[string]any)
			if !ok {
				break
			}
			m = next
		}
	}
}

// Safe is a value written as it is: markup filex produced. Callers that
// render in-process use it in their data instead of MarkSafe.
func Safe(html string) any { return values.SafeValue{Value: html} }
