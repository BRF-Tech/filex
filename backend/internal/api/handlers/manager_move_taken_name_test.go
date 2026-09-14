package handlers_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The synchronous move (`?action=move`, what an embed without the queued
// endpoint uses) keeps what already holds the name, the same way the queued
// move and every copy do. See ops.TestOpsWorker_Move_OntoATakenName_KeepsBoth
// for the measurement that found it.
func TestManagerMove_OntoATakenName_KeepsBoth(t *testing.T) {
	srv, client, _, _, root := renameFixture(t)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "Arsiv"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Arsiv", "not.txt"), []byte("was-here"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Leon", "not.txt"), []byte("incoming"), 0o644))

	body, _ := json.Marshal(map[string]any{
		"path":  "files:///Arsiv",
		"items": []map[string]string{{"path": "files:///Leon/not.txt"}},
	})
	resp, err := client.Post(srv.URL+"/api/files/manager?action=move", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	kept, err := os.ReadFile(filepath.Join(root, "Arsiv", "not.txt"))
	require.NoError(t, err)
	require.Equal(t, "was-here", string(kept), "the move overwrote the file that already had the name")
	landed, err := os.ReadFile(filepath.Join(root, "Arsiv", "not-copy.txt"))
	require.NoError(t, err, "the moved file must land beside it under a free name")
	require.Equal(t, "incoming", string(landed))
}
