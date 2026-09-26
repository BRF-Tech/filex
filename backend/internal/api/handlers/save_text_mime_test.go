package handlers

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/newdoc"
)

// Every text type New document can create has to be one save-text will then
// save under a name of the person's choosing (#56). The registry writes the
// type's mime into the catalogue; if isTextualMime did not know one of them, a
// `config` made as JSON would open in the editor and refuse its first Ctrl+S.
func TestIsTextualMime_CoversEveryTextTypeNewDocumentCreates(t *testing.T) {
	for _, ty := range newdoc.Types() {
		if ty.Group != newdoc.GroupText {
			continue
		}
		if !isTextualMime(ty.MIME) {
			t.Errorf("%s: %q is written by New document but not accepted as text by save-text", ty.Ext, ty.MIME)
		}
	}
}

func TestIsTextualMime(t *testing.T) {
	for m, want := range map[string]bool{
		"text/plain; charset=utf-8": true,
		"TEXT/X-Python":             true,
		"application/json":          true,
		"application/yaml":          true,
		"application/xml":           true,
		"application/octet-stream":  false,
		"image/png":                 false,
		"application/zip":           false,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": false,
		"": false,
	} {
		if got := isTextualMime(m); got != want {
			t.Errorf("isTextualMime(%q) = %v, want %v", m, got, want)
		}
	}
}
