// Package httpx holds the small HTTP details that have to be identical
// everywhere filex writes a response.
package httpx

import (
	"fmt"
	"strings"
)

// ContentDisposition builds a `Content-Disposition` value that is safe for
// EVERY client, for a file whose name may be anything the user typed.
//
// The trap it exists for: an HTTP header is bytes, and a header value is
// ASCII by specification. Writing `filename="Türkçe adlı dosya.txt"` puts raw
// UTF-8 in the header. Browsers guess their way through it, which is why this
// survives for years unnoticed — but a strict client does not:
//
//	TypeError: Cannot convert argument to a ByteString because the character
//	at index 32 has a value of 305 which is greater than 255
//
// That is Electron's `net.fetch` (undici) refusing byte 0x131 — `ı` — while
// parsing the response, thrown from an event handler where the caller's
// try/catch cannot reach it. Measured 2026-08-29: the filex desktop app took an
// uncaught exception in its main process, and a folder being dragged out
// stopped filling in halfway, silently.
//
// RFC 6266 has the answer, and it is to send BOTH forms:
//
//	attachment; filename="Turkce adli dosya.txt"; filename*=UTF-8''T%C3%BCrk%C3%A7e...
//
// `filename` is a plain-ASCII fallback for anything old or strict; `filename*`
// (RFC 5987) carries the real name percent-encoded, and every current browser
// prefers it. The result is pure ASCII either way, so no client has to guess.
func ContentDisposition(disposition, name string) string {
	if disposition == "" {
		disposition = "attachment"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "file"
	}
	// Path separators never belong in a filename parameter — a client that
	// honours them writes outside the folder the user chose.
	name = strings.NewReplacer("/", "_", `\`, "_", "\r", "_", "\n", "_").Replace(name)

	ascii := asciiFallback(name)
	out := disposition + `; filename="` + ascii + `"`
	if ascii != name {
		// Only add the extended form when it says something the fallback
		// cannot: an ASCII name needs no second copy of itself.
		out += "; filename*=UTF-8''" + rfc5987Escape(name)
	}
	return out
}

// asciiFallback keeps what a plain `filename=` may contain: printable ASCII,
// no quotes or backslashes. Everything else becomes `_`, so a name of nothing
// but non-ASCII characters still yields something a client can save.
func asciiFallback(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	replaced := false
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('_')
			replaced = true
		case r < 0x20 || r > 0x7e:
			b.WriteByte('_')
			replaced = true
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	// A fallback with nothing readable left in it ("__.txt", "___") tells the
	// user nothing; name it `file` and keep the extension, which is the part
	// that still decides what opens it.
	ext := extensionOf(name)
	stem := strings.TrimSuffix(out, ext)
	if !hasAlnum(stem) {
		// ⚠ The STEM, not the whole string: `__.txt` has letters in it, but
		// they are the extension's — the part that names the file is gone.
		return "file" + ext
	}
	_ = replaced
	return out
}

// hasAlnum reports whether anything in s would read as a name.
func hasAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

// extensionOf returns the ASCII extension (with the dot) when there is one.
func extensionOf(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	ext := name[i:]
	for _, r := range ext {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	return ext
}

// rfc5987Escape percent-encodes for the `filename*` parameter: attr-char only,
// everything else as %XX of its UTF-8 bytes.
//
// ⚠ `url.QueryEscape` is NOT this: it turns a space into `+`, which a client
// reading RFC 5987 keeps as a literal plus sign.
func rfc5987Escape(s string) string {
	const attrChar = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if isAlnum || strings.IndexByte(attrChar, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		// ⚠ Byte by byte, not rune by rune, and not `url.PathEscape`: the
		// escape has to describe the UTF-8 BYTES. Converting a byte >= 0x80
		// to a string first turns it into a rune and encodes two bytes —
		// which is a different filename.
		b.WriteString(fmt.Sprintf("%%%02X", c))
	}
	return b.String()
}
