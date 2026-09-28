package handlers_test

// POST /api/files/move and /api/files/copy with `name`: one source moved or
// copied under a name of the caller's choosing, as ONE step of the queue.
// Encrypted-names folders need it (a name is sealed for the folder it is in,
// docs/E2E-ENCRYPTION.md → "Folder ids"), and it must be atomic: nothing may
// stop between the move and the rename.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerVerb_MoveAndCopyUnderANewName(t *testing.T) {
	f := newMTFix(t, false)
	f.seedFile(t, f.StA, f.RootA, "a/old-name.txt", "moved")
	f.seedFile(t, f.StA, f.RootA, "a/copied.txt", "copied")
	f.seedFile(t, f.StA, f.RootA, "b/keep.txt", "already there")

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/move", map[string]any{
		"source": []string{"alpha://a/old-name.txt"}, "target": "alpha://b", "name": "new-name.txt",
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	status, body = doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
		"source": []string{"alpha://a/copied.txt"}, "target": "alpha://b", "name": "the-copy.txt",
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	f.drainOps(t)

	got, err := os.ReadFile(filepath.Join(f.RootA, "b", "new-name.txt"))
	require.NoError(t, err)
	assert.Equal(t, "moved", string(got))
	_, err = os.Stat(filepath.Join(f.RootA, "a", "old-name.txt"))
	assert.True(t, os.IsNotExist(err), "the source is gone")
	got, err = os.ReadFile(filepath.Join(f.RootA, "b", "the-copy.txt"))
	require.NoError(t, err)
	assert.Equal(t, "copied", string(got))
	_, err = os.Stat(filepath.Join(f.RootA, "a", "copied.txt"))
	assert.NoError(t, err, "a copy leaves its source")
}

func TestPerVerb_ANameIsOneSegmentForOneSource(t *testing.T) {
	f := newMTFix(t, false)
	f.seedFile(t, f.StA, f.RootA, "a/x.txt", "x")
	f.seedFile(t, f.StA, f.RootA, "a/y.txt", "y")
	for what, req := range map[string]map[string]any{
		"two sources":   {"source": []string{"alpha://a/x.txt", "alpha://a/y.txt"}, "target": "alpha://b", "name": "z.txt"},
		"a path":        {"source": []string{"alpha://a/x.txt"}, "target": "alpha://b", "name": "../escape.txt"},
		"a dot-dot":     {"source": []string{"alpha://a/x.txt"}, "target": "alpha://b", "name": ".."},
		"a backslash":   {"source": []string{"alpha://a/x.txt"}, "target": "alpha://b", "name": `x\y`},
		"with a delete": {"source": []string{"alpha://a/x.txt"}, "name": "z.txt"},
	} {
		url := f.URL + "/api/files/move"
		if what == "with a delete" {
			url = f.URL + "/api/files/delete"
		}
		status, body := doJSON(t, f.A, http.MethodPost, url, req)
		assert.Equal(t, http.StatusBadRequest, status, "%s: %v", what, body)
	}
}
