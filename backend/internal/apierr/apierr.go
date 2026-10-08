// Package apierr is the one shape of a failure filex tells somebody about:
// a stable CODE a program branches on, the values its sentence takes, and the
// sentence itself, written by the SERVER in the reader's language.
//
// ⚠⚠ Why it exists (0.54 audit, A1/A2). The server already wrote most of its
// sentences in the reader's language (srvtext), but a refusal carried them in
// three shapes - an English sentence in `error`, a code in `error` with the
// sentence in `message`, an English sentence in `error` with a code beside it
// in `code` - and a failed queue job kept Go's own error text. The explorer
// threw the server's sentence away and rebuilt one from regular expressions
// matched against the English ("quota exceeded", "storage is read-only",
// "something with that name already exists here"): a Go error text that
// changed broke the translation silently, and the CLI and an MCP agent read
// "HTTP 403: permission_denied".
//
// The envelope (docs/API-ERRORS.md):
//
//		{"error": "<code>", "message": "<sentence, reader's language>", "params": {…}}
//
//	  - `error` is the code: lower-case ASCII, `[a-z0-9_]`, stable across
//	    releases. A refusal that has always answered a code keeps it; older
//	    clients that match on it keep working.
//	  - `message` is `server.error.<code>` from the server catalogue (srvtext),
//	    filled with `params`, in the reader's language (a language pack's too).
//	    Every surface prints it as it is: the explorer, the admin panel, the
//	    desktop app, the CLI, an MCP agent.
//	  - `params` are the values the sentence was filled with, for a program
//	    that wants them (a name, a limit). Optional.
//
// A failure that is not a refusal (a queue job that failed) carries the same
// three things: Error is what the worker stores (Encode) instead of Go's
// text, and the sentence is made when the row is READ, in the language of
// the person reading it.
package apierr

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Params are the values a code's sentence takes (`{name}`, `{max}`). A
// "count" that is a whole number picks the sentence's plural form.
type Params = srvtext.Vars

// codeRe is what a code may look like.
var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidCode reports whether code has the shape of an error code.
func ValidCode(code string) bool { return codeRe.MatchString(code) }

// Key is the catalogue key of code's sentence.
func Key(code string) string { return srvtext.Prefix + "error." + code }

// Known reports whether the catalogue has a sentence for code.
func Known(code string) bool { return code != "" && srvtext.Has(Key(code)) }

// Text is code's sentence in lang, filled with params; "" for a code the
// catalogue does not know (the caller then says nothing rather than print a
// key). lang is resolved like every server text (srvtext.Pick): a pack's
// language, its base language, the instance default, English.
func Text(lang, code string, params Params) string {
	if !Known(code) {
		return ""
	}
	key := Key(code)
	if c, ok := params["count"]; ok {
		if n, err := strconv.Atoi(c); err == nil {
			return srvtext.Plural(srvtext.Pick(lang), key, n, params)
		}
	}
	return srvtext.Text(srvtext.Pick(lang), key, params)
}

// Body is the envelope of a refusal, ready to marshal.
type Body struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Params  Params `json:"params,omitempty"`
}

// Envelope is code's refusal said in lang.
func Envelope(lang, code string, params Params) Body {
	b := Body{Error: code, Message: Text(lang, code, params)}
	if len(params) > 0 {
		b.Params = params
	}
	return b
}

// Map is Envelope as a map, for a refusal that carries fields of its own
// beside the three (a lock's `plugin`, a draft's `limit`). The three win
// over an extra field of the same name.
func Map(lang, code string, params Params, extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra)+3)
	for k, v := range extra {
		out[k] = v
	}
	out["error"] = code
	out["message"] = Text(lang, code, params)
	if len(params) > 0 {
		out["params"] = params
	} else {
		delete(out, "params")
	}
	return out
}

// ── a failure that travels as a Go error ─────────────────────────────────

// Error is a failure with a code: what a service returns when its caller
// (a handler, the queue worker) must be able to say it in the reader's
// language later. Error() is the English detail, for logs and for an
// administrator's second line - never the sentence a person reads.
type Error struct {
	Code   string
	Params Params
	// Err is the underlying failure, if any.
	Err error
}

// New is a coded failure. detail may be nil.
func New(code string, params Params, detail error) *Error {
	return &Error{Code: code, Params: params, Err: detail}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if s := Text("en", e.Code, e.Params); s != "" {
		return s
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// CodeOf is the code and params of the first *Error in err's chain, or "".
func CodeOf(err error) (string, Params) {
	var ae *Error
	if errors.As(err, &ae) && ae != nil {
		return ae.Code, ae.Params
	}
	return "", nil
}

// ── a failure kept on a row (the queue's `error` column) ─────────────────

// stored is the shape Encode writes: it starts with `{"error_code":`, which
// no Go error text the column held before does, so a row written by an older
// server reads back as its plain text.
type stored struct {
	Code   string `json:"error_code"`
	Params Params `json:"params,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const storedPrefix = `{"error_code":`

// Encode is what a row keeps for a failure: the code, its params and the
// English detail when the failure has a code, else the plain text as before.
func Encode(code string, params Params, detail string) string {
	if code == "" {
		return detail
	}
	b, err := json.Marshal(stored{Code: code, Params: params, Detail: detail})
	if err != nil {
		return detail
	}
	return string(b)
}

// EncodeErr is Encode for an error: its code when it carries one (CodeOf),
// its text as the detail.
func EncodeErr(err error) string {
	if err == nil {
		return ""
	}
	code, params := CodeOf(err)
	return Encode(code, params, err.Error())
}

// Decode reads back what Encode wrote: the code ("" for a plain text), the
// params and the detail (the plain text itself when there is no code).
func Decode(s string) (code string, params Params, detail string) {
	if !strings.HasPrefix(s, storedPrefix) {
		return "", nil, s
	}
	var st stored
	if err := json.Unmarshal([]byte(s), &st); err != nil || !ValidCode(st.Code) {
		return "", nil, s
	}
	return st.Code, st.Params, st.Detail
}

// Codes is every code the catalogue has a sentence for, sorted - for the
// documentation test that lists them.
func Codes() []string {
	prefix := srvtext.Prefix + "error."
	seen := map[string]bool{}
	var out []string
	for _, k := range srvtext.Keys() {
		c, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		c = strings.TrimSuffix(c, "_one")
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
