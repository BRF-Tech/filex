package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #56: a document created as Plain text may be called LICENSE, Makefile or
// example.custom — and the editor it opens in has to be able to SAVE it.
// save-text allows by extension, and those names carry none it knows, so the
// first Ctrl+S answered 415 "extension not allowed for save-text": a file the
// product had just made and opened in its own text editor, refused by that
// editor. For a name the list does not know, the catalogue's word on the
// bytes decides — the same fact the client uses to open it as text.

func TestSaveText_AnUntypedNameTheCatalogueCallsTextIsSaved(t *testing.T) {
	f := newStagedFixture(t)

	for _, name := range []string{"LICENSE", "example.custom"} {
		require.Equal(t, http.StatusOK, f.mutate(t, "newfile", map[string]any{
			"path": "main://", "name": name, "type": "txt", "exact_name": true,
		}), name)
		require.Equal(t, http.StatusOK, f.saveText(t, "main://"+name, "MIT License\n"), name)
		got, err := os.ReadFile(filepath.Join(f.rootDir, name))
		require.NoError(t, err)
		assert.Equal(t, "MIT License\n", string(got), name)
	}

	// An upload with text in it is text too: the upload path sniffs the bytes.
	code, body := f.uploadMultipartCode(t, "NOTICE", []byte("Copyright 2026\n"))
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, http.StatusOK, f.saveText(t, "main://NOTICE", "Copyright 2026 BRF\n"))
}

func TestSaveText_AnUntypedNameIsStillRefusedWhenItIsNotText(t *testing.T) {
	f := newStagedFixture(t)

	// Binary bytes under a name with no extension: the catalogue says so, and
	// a text write over them is exactly what the list exists to stop.
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")
	code, body := f.uploadMultipartCode(t, "blob", png)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, http.StatusUnsupportedMediaType, f.saveText(t, "main://blob", "overwritten"))
	got, err := os.ReadFile(filepath.Join(f.rootDir, "blob"))
	require.NoError(t, err)
	assert.Equal(t, png, got, "the refused save must not have touched the bytes")

	// No catalogue row at all: nothing vouches for the bytes, so nothing
	// changes from before.
	assert.Equal(t, http.StatusUnsupportedMediaType, f.saveText(t, "main://NEWFILE", "hello"))
	_, err = os.Stat(filepath.Join(f.rootDir, "NEWFILE"))
	assert.True(t, os.IsNotExist(err))
}
