// Package appstore is filex's side of an app store (docs/APP-PLUGINS.md →
// Installing from a store, Paid apps; docs/APP-PLUGINS-API.md → The store
// contract): the stores an administrator trusts, the install links a store
// hands out, and the licenses of paid apps, which the store issues, keeps and
// is asked about.
//
// What it never does is install anything by itself. A link opens filex's own
// install review, filled in; the administrator reads it and presses Install,
// exactly as for a repository they typed (the SHA-256 protects the
// administrator, the sandbox protects the user).
package appstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

// ErrNotCanonicalizable is answered for JSON the canonical form cannot carry:
// a duplicated key, invalid UTF-8, trailing data.
var ErrNotCanonicalizable = errors.New("not canonical JSON material")

// numberRe is a JSON number as RFC 8259 spells it.
var numberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Canonical is the one byte form a store signs: the JSON value with every
// object's keys in sorted order (by their UTF-8 bytes; every key of the
// contract is ASCII), no whitespace, UTF-8 text written as itself, and only
// `"`, `\` and the control characters escaped - `\b \f \n \r \t` by name, the
// others as `\u00xx` in lower-case hex. That is what JavaScript's
// JSON.stringify writes for a string, so a store written in any language that
// sorts keys produces the same bytes. A number is kept exactly as it was
// written.
//
// ⚠ Not encoding/json's Marshal: it escapes `<`, `>`, `&`, U+2028 and U+2029,
// which a store that does not would not have signed, and it cannot keep a
// number's spelling. A duplicated key is refused rather than resolved: two
// readers that keep different copies would read two different documents
// under one signature.
func Canonical(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrNotCanonicalizable)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	if err := canonValue(dec, &out); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: data after the value", ErrNotCanonicalizable)
	}
	return out.Bytes(), nil
}

func canonValue(dec *json.Decoder, out *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotCanonicalizable, err)
	}
	return canonToken(dec, tok, out)
}

func canonToken(dec *json.Decoder, tok json.Token, out *bytes.Buffer) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return canonObject(dec, out)
		case '[':
			return canonArray(dec, out)
		}
		return fmt.Errorf("%w: unexpected %q", ErrNotCanonicalizable, v)
	case string:
		writeString(out, v)
	case json.Number:
		s := v.String()
		if !numberRe.MatchString(s) {
			return fmt.Errorf("%w: number %q", ErrNotCanonicalizable, s)
		}
		out.WriteString(s)
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("%w: unexpected token %T", ErrNotCanonicalizable, tok)
	}
	return nil
}

func canonObject(dec *json.Decoder, out *bytes.Buffer) error {
	members := map[string][]byte{}
	keys := []string{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotCanonicalizable, err)
		}
		k, ok := kt.(string)
		if !ok {
			return fmt.Errorf("%w: object key is not a string", ErrNotCanonicalizable)
		}
		if _, dup := members[k]; dup {
			return fmt.Errorf("%w: duplicated key %q", ErrNotCanonicalizable, k)
		}
		var val bytes.Buffer
		if err := canonValue(dec, &val); err != nil {
			return err
		}
		members[k] = val.Bytes()
		keys = append(keys, k)
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return fmt.Errorf("%w: %v", ErrNotCanonicalizable, err)
	}
	sort.Strings(keys)
	out.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		writeString(out, k)
		out.WriteByte(':')
		out.Write(members[k])
	}
	out.WriteByte('}')
	return nil
}

func canonArray(dec *json.Decoder, out *bytes.Buffer) error {
	out.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			out.WriteByte(',')
		}
		first = false
		if err := canonValue(dec, out); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing bracket
		return fmt.Errorf("%w: %v", ErrNotCanonicalizable, err)
	}
	out.WriteByte(']')
	return nil
}

const hexDigits = "0123456789abcdef"

// writeString writes s as JSON.stringify does.
func writeString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hexDigits[c>>4])
				out.WriteByte(hexDigits[c&0xf])
				continue
			}
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
}
