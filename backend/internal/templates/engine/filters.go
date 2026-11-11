package engine

import (
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/osteele/liquid"
	"github.com/osteele/liquid/filters"
	"github.com/osteele/liquid/values"
)

// collected captures the library's standard filters by name, so each can be
// registered again behind unwrapSafe (filters.AddStandardFilters takes any
// dictionary).
type collected map[string]any

func (c collected) AddFilter(name string, fn any) { c[name] = fn }

// keepsSafety are the standard filters whose answer is still the HTML its
// input was when the input was HTML: they only take whitespace away. Every
// other filter's answer is text again, escaped when it is written - a filter
// with arguments (`replace`, `append`) could put anything into it.
var keepsSafety = map[string]bool{"strip": true, "lstrip": true, "rstrip": true, "strip_newlines": true}

func registerFilters(e *liquid.Engine, o Options) {
	std := collected{}
	filters.AddStandardFilters(std)
	for name, fn := range std {
		e.RegisterFilter(name, unwrapSafe(name, fn))
	}

	// `safe` would write its input unescaped: a template could print a file
	// name, a message, anything a stranger typed, as markup. It does nothing.
	e.RegisterFilter("safe", func(in any) any { return plain(in) })

	if o.Mode == HTML {
		e.RegisterFilter("escape", func(in any) any {
			if sv, ok := in.(values.SafeValue); ok {
				return sv
			}
			return values.SafeValue{Value: html.EscapeString(text(in))}
		})
		e.RegisterFilter("escape_once", func(in any) any {
			if sv, ok := in.(values.SafeValue); ok {
				return sv
			}
			return values.SafeValue{Value: html.EscapeString(html.UnescapeString(text(in)))}
		})
		e.RegisterFilter("newline_to_br", func(in any) any {
			s := text(in)
			if sv, ok := in.(values.SafeValue); ok {
				s = fmt.Sprint(sv.Value)
			} else {
				s = html.EscapeString(s)
			}
			s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "<br>\n")
			return values.SafeValue{Value: s}
		})
		e.RegisterFilter("link_to", linkTo)
	}

	e.RegisterFilter("t", func(key any, args ...any) (any, error) {
		return call(o.Host, "t", key, args)
	})
	e.RegisterFilter("t_html", func(key any, args ...any) (any, error) {
		vars, raw := kwargs(args)
		tpl, err := call(o.Host, "tpl", key, args)
		if err != nil {
			return nil, err
		}
		filled := fillHTML(tpl.(string), vars, raw)
		if o.Mode != HTML {
			return html.UnescapeString(filled), nil
		}
		return values.SafeValue{Value: filled}, nil
	})
	e.RegisterFilter("bytes", func(n any) (any, error) {
		if o.Host == nil {
			return nil, ErrNoHost
		}
		return o.Host.Call("bytes", []any{toInt(n)})
	})
}

// unwrapSafe is a standard filter that accepts HTML (a SafeValue - what
// `capture` and `escape` give) as its text: the library's own filters do not
// know the type, and `{{ captured | strip }}` printed the struct.
func unwrapSafe(name string, fn any) any {
	fv := reflect.ValueOf(fn)
	ft := fv.Type()
	// A filter whose parameters are closures (an expression it evaluates per
	// item) keeps its own signature: the library builds the closure from the
	// parameter's declared type.
	for i := 1; i < ft.NumIn(); i++ {
		if ft.In(i).Kind() == reflect.Struct || ft.In(i).Kind() == reflect.Interface && ft.In(i).NumMethod() > 0 {
			return fn
		}
	}
	keep := keepsSafety[name]
	return func(in any, rest ...any) (any, error) {
		sv, wasSafe := in.(values.SafeValue)
		if wasSafe {
			in = fmt.Sprint(sv.Value)
		}
		args := make([]any, 0, len(rest)+1)
		args = append(args, in)
		for _, a := range rest {
			args = append(args, plain(a))
		}
		out, err := values.Call(fv, args)
		if err != nil {
			return nil, err
		}
		if wasSafe && keep {
			if s, ok := out.(string); ok {
				return values.SafeValue{Value: s}, nil
			}
		}
		return out, nil
	}
}

// plain is a value with any HTML marking taken off: the text it holds.
func plain(v any) any {
	if sv, ok := v.(values.SafeValue); ok {
		return fmt.Sprint(sv.Value)
	}
	return v
}

func text(v any) string {
	if v == nil {
		return ""
	}
	if sv, ok := v.(values.SafeValue); ok {
		return fmt.Sprint(sv.Value)
	}
	return fmt.Sprint(v)
}

func toInt(v any) int64 {
	switch n := plain(v).(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	}
	i, _ := strconv.ParseInt(fmt.Sprint(plain(v)), 10, 64)
	return i
}

// kwargs splits a filter's trailing keyword arguments (`| t: name: x`) into
// the text of each value and the ones that are HTML already.
func kwargs(args []any) (map[string]string, map[string]bool) {
	vars := map[string]string{}
	raw := map[string]bool{}
	for _, a := range args {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			if sv, ok := v.(values.SafeValue); ok {
				vars[k] = fmt.Sprint(sv.Value)
				raw[k] = true
				continue
			}
			if v == nil {
				vars[k] = ""
				continue
			}
			vars[k] = fmt.Sprint(v)
		}
	}
	return vars, raw
}

func call(h Host, fn string, key any, args []any) (any, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	k := strings.TrimSpace(text(key))
	if k == "" {
		return nil, fmt.Errorf("%s needs a key", fn)
	}
	vars, _ := kwargs(args)
	return h.Call(fn, []any{k, vars})
}

var placeholderRe = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// fillHTML is a catalogue sentence as HTML: the sentence escaped, then each
// `{name}` replaced by its value - escaped, unless the value is HTML filex or
// this package made (raw). A placeholder with no value stays as written.
func fillHTML(tpl string, vars map[string]string, raw map[string]bool) string {
	esc := html.EscapeString(tpl)
	return placeholderRe.ReplaceAllStringFunc(esc, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name]
		if !ok {
			return m
		}
		if raw[name] {
			return v
		}
		return html.EscapeString(v)
	})
}

// linkTo is `{{ text | link_to: href, id }}`: an anchor around the escaped
// text. An address that is not http(s), mailto:, tel: or relative becomes
// "#" - a template cannot make a javascript: link out of the data.
func linkTo(in any, args ...any) any {
	href, id := "", ""
	if len(args) > 0 {
		href = text(args[0])
	}
	if len(args) > 1 {
		if _, isMap := args[1].(map[string]any); !isMap {
			id = text(args[1])
		}
	}
	label := html.EscapeString(text(in))
	if sv, ok := in.(values.SafeValue); ok {
		label = fmt.Sprint(sv.Value)
	}
	var b strings.Builder
	b.WriteString(`<a href="`)
	b.WriteString(html.EscapeString(SafeURL(href)))
	b.WriteString(`"`)
	if id != "" {
		b.WriteString(` id="`)
		b.WriteString(html.EscapeString(id))
		b.WriteString(`"`)
	}
	b.WriteString(`>`)
	b.WriteString(label)
	b.WriteString(`</a>`)
	return values.SafeValue{Value: b.String()}
}

// SafeURL is u when it is an address a link may carry - http(s), mailto:,
// tel:, or relative (a path, a query, a fragment) - and "#" otherwise.
func SafeURL(u string) string {
	t := strings.TrimSpace(u)
	if t == "" {
		return "#"
	}
	// Browsers drop tabs and newlines inside a scheme ("java\tscript:").
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r < 0x20 {
			return -1
		}
		return r
	}, t)
	colon := strings.IndexByte(clean, ':')
	if colon < 0 {
		return t
	}
	// A colon after the first /, ? or # is part of a path, not a scheme.
	if cut := strings.IndexAny(clean, "/?#"); cut >= 0 && cut < colon {
		return t
	}
	switch strings.ToLower(clean[:colon]) {
	case "http", "https", "mailto", "tel":
		return t
	}
	return "#"
}
