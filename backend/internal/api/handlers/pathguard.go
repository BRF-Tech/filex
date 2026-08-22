package handlers

import "strings"

// pathHasDotDot reports whether any SEGMENT of a relative path is "..".
// Traversal needs a whole ../ segment; a bare substring test also rejected
// legitimate FILENAMES — scanners love "Report..pdf", and one such invoice
// ("… Gaz..pdf") was undownloadable on a live deployment: every preview and
// download answered 400 "bad path" for a file the same API had happily
// stored. Same rule the sync engine's AddPair applies.
//
// Two extra refusals keep the guard as strict as the old one where it
// matters: segments are split on BOTH separators, because a backslash is a
// separator to a Windows host even when the wire path uses slashes, and a
// segment made of nothing but dots and spaces is refused outright — Windows
// trims trailing dots and spaces from a component, so ".. " could reach the
// filesystem as "..". No real file is named that way.
func pathHasDotDot(rel string) bool {
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
		if strings.Contains(seg, "..") && strings.Trim(seg, ". ") == "" {
			return true
		}
	}
	return false
}
