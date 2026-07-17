// Package extract turns file bytes into plain text for the search index's
// `content` field ("Bul" wave, v0.2).
//
// Extractors register themselves from init() blocks (same pattern as the
// storage/db driver registries). The queue's content_index job asks
// For(mime, ext) whether a file is extractable and, if so, streams the
// source bytes through the matching Extractor.
//
// Contract notes (shared with the OCR extractor added by S2):
//   - For() normalizes its inputs before calling Supports: `ext` arrives
//     lower-cased without a leading dot ("pdf", not ".PDF"), `mime` arrives
//     lower-cased with any parameters stripped ("text/plain", not
//     "Text/Plain; charset=utf-8"). Supports implementations can rely on
//     that.
//   - Extract must honor `limit` as the cap on the RETURNED text in bytes
//     (<=0 means DefaultLimit). Source-size capping is the caller's job.
//   - "Could not extract" (scanned PDF, corrupt archive, no text layer) is
//     NOT an error: return ("", nil). Reserve errors for transport-level
//     failures (the reader itself failing) so the queue can retry those.
package extract

import (
	"context"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// DefaultLimit is the cap on extracted text, in bytes (200 KiB — frozen in
// the v0.2 "Bul" contract). Callers pass it (or their own smaller cap) to
// Extract; <=0 falls back to this value inside every bundled extractor.
const DefaultLimit int64 = 200 << 10

// Extractor converts one file format family into plain text.
type Extractor interface {
	// Supports reports whether this extractor handles the (mime, ext)
	// pair. Inputs are pre-normalized by For — see the package comment.
	Supports(mime, ext string) bool
	// Extract reads the source bytes from r and returns plain text capped
	// at limit bytes. ("", nil) means "nothing extractable" — not an error.
	Extract(ctx context.Context, r io.Reader, limit int64) (string, error)
}

var (
	regMu      sync.RWMutex
	extractors []Extractor
)

// Register appends e to the extractor chain. Bundled extractors register
// from init(); external ones (OCR) register during server bootstrap.
func Register(e Extractor) {
	if e == nil {
		return
	}
	regMu.Lock()
	extractors = append(extractors, e)
	regMu.Unlock()
}

// For returns the first registered Extractor that claims (mime, ext), or
// nil when the file type is not extractable. Both inputs are optional —
// extension-less files can still match on mime and vice versa.
func For(mime, ext string) Extractor {
	mime = NormalizeMime(mime)
	ext = NormalizeExt(ext)
	regMu.RLock()
	defer regMu.RUnlock()
	for _, e := range extractors {
		if e.Supports(mime, ext) {
			return e
		}
	}
	return nil
}

// Supported reports whether any registered extractor handles (mime, ext).
func Supported(mime, ext string) bool { return For(mime, ext) != nil }

// NormalizeExt lower-cases ext and strips a leading dot ("PDF"/".pdf" → "pdf").
func NormalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

// NormalizeMime lower-cases mime and drops parameters
// ("Text/Plain; charset=utf-8" → "text/plain").
func NormalizeMime(mime string) string {
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

// clamp cuts s at limit bytes without splitting a multi-byte rune, then
// trims surrounding whitespace. Shared by every bundled extractor.
func clamp(s string, limit int64) string {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if int64(len(s)) > limit {
		cut := int(limit)
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return strings.TrimSpace(s)
}

// sanitize drops invalid UTF-8 sequences and NUL bytes — Bleve stores the
// content verbatim and snippets end up in JSON responses, so the text must
// be clean UTF-8 with no control garbage.
func sanitize(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.ReplaceAll(s, "\x00", "")
}
