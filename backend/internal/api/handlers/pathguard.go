package handlers

import "strings"

// pathHasDotDot reports whether any SEGMENT of a slash-separated relative
// path is "..". Traversal needs a whole ../ segment; a bare substring test
// also rejected legitimate FILENAMES — scanners love "Report..pdf", and one
// such invoice ("… Gaz..pdf") was undownloadable on a live deployment: every
// preview and download answered 400 "bad path" for a file the same API had
// happily stored. Same rule the sync engine's AddPair applies.
func pathHasDotDot(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
