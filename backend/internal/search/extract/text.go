package extract

import (
	"context"
	"io"
	"strings"
)

func init() { Register(textExtractor{}) }

// textExtractor handles plain-text and source-code files: read the bytes,
// keep only valid UTF-8, cut at the limit. It is registered first, so it
// wins for anything both text-like and claimed by a later extractor.
type textExtractor struct{}

// textExts is the extension allowlist (normalized: lower-case, no dot).
// Plain text + data + the common source-code families the "Bul" contract
// lists (txt/md/csv/json/log + kod uzantıları).
var textExts = map[string]struct{}{
	// plain text / docs / data
	"txt": {}, "text": {}, "md": {}, "markdown": {}, "rst": {}, "adoc": {},
	"csv": {}, "tsv": {}, "json": {}, "jsonl": {}, "ndjson": {}, "log": {},
	"xml": {}, "svg": {}, "srt": {}, "vtt": {}, "tex": {},
	// web
	"html": {}, "htm": {}, "css": {}, "scss": {}, "sass": {}, "less": {},
	"js": {}, "mjs": {}, "cjs": {}, "jsx": {}, "ts": {}, "tsx": {},
	"vue": {}, "svelte": {},
	// languages
	"go": {}, "py": {}, "php": {}, "rb": {}, "rs": {}, "java": {}, "kt": {},
	"kts": {}, "swift": {}, "c": {}, "h": {}, "cc": {}, "cpp": {}, "hpp": {},
	"cs": {}, "scala": {}, "lua": {}, "pl": {}, "r": {}, "dart": {},
	"ex": {}, "exs": {}, "erl": {}, "hs": {},
	// shell / config
	"sh": {}, "bash": {}, "zsh": {}, "fish": {}, "ps1": {}, "bat": {}, "cmd": {},
	"sql": {}, "yaml": {}, "yml": {}, "toml": {}, "ini": {}, "cfg": {},
	"conf": {}, "env": {}, "properties": {}, "dockerfile": {}, "makefile": {},
	"gradle": {}, "tf": {}, "proto": {}, "graphql": {},
}

// textMimes are non-"text/*" mime types that are still plain text.
var textMimes = map[string]struct{}{
	"application/json":          {},
	"application/ld+json":       {},
	"application/xml":           {},
	"application/javascript":    {},
	"application/x-javascript":  {},
	"application/typescript":    {},
	"application/x-sh":          {},
	"application/x-shellscript": {},
	"application/sql":           {},
	"application/x-yaml":        {},
	"application/yaml":          {},
	"application/toml":          {},
	"image/svg+xml":             {},
}

func (textExtractor) Supports(mime, ext string) bool {
	if _, ok := textExts[ext]; ok {
		return true
	}
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	_, ok := textMimes[mime]
	return ok
}

func (textExtractor) Extract(_ context.Context, r io.Reader, limit int64) (string, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	// Read at most limit bytes — a cut mid-rune leaves a partial sequence
	// at the tail, which sanitize() drops along with any other junk.
	b, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return "", err
	}
	return clamp(sanitize(string(b)), limit), nil
}
