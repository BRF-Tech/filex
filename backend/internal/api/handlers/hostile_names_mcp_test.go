package handlers_test

// The agent surface (MCP) with every hostile name (task #238). A path is a
// JSON string there - no URL decoding - so what is tested is that the name an
// agent writes is the name it lists, reads, moves, finds and makes a folder
// with, byte for byte; that the names no door may create are refused as a
// TOOL error (an answer the agent can read), never a JSON-RPC failure; and
// that a name too long for the storage says so without the server's paths.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// mcpNames is the `name` of every entry a file_list or file_search answered.
func mcpNames(t *testing.T, structured json.RawMessage) []string {
	t.Helper()
	var out struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(structured, &out), string(structured))
	names := make([]string, 0, len(out.Entries))
	for _, e := range out.Entries {
		names = append(names, e.Name)
	}
	return names
}

func TestMCP_HostileNamesRoundTrip(t *testing.T) {
	srv, _, _, tok := aiFixture(t)
	names := hostilenames.ForLocalDisk()
	for _, dir := range []string{"main://h", "main://moved", "main://dirs"} {
		isErr, text, _ := parityTool(t, srv.URL, tok, "file_mkdir", map[string]any{"path": dir})
		require.False(t, isErr, "file_mkdir %s: %s", dir, text)
	}

	for _, n := range names {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "main://h/" + n.Name
			isErr, text, _ := parityTool(t, srv.URL, tok, "file_write", map[string]any{"path": p, "content": "body of " + n.ID})
			require.False(t, isErr, "file_write %q: %s", n.Name, text)

			isErr, text, _ = parityTool(t, srv.URL, tok, "file_read", map[string]any{"path": p})
			require.False(t, isErr, "file_read %q: %s", n.Name, text)
			require.Contains(t, text, "body of "+n.ID)

			// Moved into another folder under the SAME name: a suffix would
			// push the 255-byte name over the limit it is there to test.
			isErr, text, _ = parityTool(t, srv.URL, tok, "file_move", map[string]any{"src": p, "dst": "main://moved/" + n.Name})
			require.False(t, isErr, "file_move %q: %s", n.Name, text)

			isErr, text, _ = parityTool(t, srv.URL, tok, "file_mkdir", map[string]any{"path": "main://dirs/" + n.Name})
			require.False(t, isErr, "file_mkdir %q: %s", n.Name, text)
		})
	}

	// Listed with the exact bytes they were written with, every one of them.
	for _, dir := range []string{"main://moved", "main://dirs"} {
		isErr, text, structured := parityTool(t, srv.URL, tok, "file_list", map[string]any{"path": dir})
		require.False(t, isErr, "file_list %s: %s", dir, text)
		listed := mcpNames(t, structured)
		for _, n := range names {
			require.Contains(t, listed, n.Name, "%s: %s (%q) is not listed under its own name", dir, n.ID, n.Name)
		}
		require.Len(t, listed, len(names), "%s lists something that is not one of the names: %q", dir, listed)
	}

	// Found by its own name (the database fallback: this fixture has no index).
	for _, n := range names {
		content := false
		isErr, text, structured := parityTool(t, srv.URL, tok, "file_search", map[string]any{"path": "main://", "query": n.Name, "content": &content})
		require.False(t, isErr, "file_search %q: %s", n.Name, text)
		require.Contains(t, mcpNames(t, structured), n.Name, "file_search does not find %s (%q)", n.ID, n.Name)
	}
}

func TestMCP_HostileNamesRefusedAsAToolError(t *testing.T) {
	srv, _, _, tok := aiFixture(t)
	for _, n := range hostilenames.Refused() {
		for _, call := range []struct {
			tool string
			args map[string]any
		}{
			{"file_write", map[string]any{"path": "main://r/" + n.Name, "content": "x"}},
			{"file_mkdir", map[string]any{"path": "main://r/" + n.Name}},
		} {
			isErr, text, _ := parityTool(t, srv.URL, tok, call.tool, call.args)
			require.True(t, isErr, "%s %s (%q) was let through: %s", call.tool, n.ID, n.Name, text)
		}
	}
	// Nothing was made under any of those names.
	isErr, text, structured := parityTool(t, srv.URL, tok, "file_list", map[string]any{"path": "main://r"})
	if !isErr {
		require.Empty(t, mcpNames(t, structured), "refused names left entries behind: %s", text)
	}
}

// A name no door may create any more - a tab, a line break (the owner's
// decision for 0.57) - is still a file when something else put it on the
// disk: the agent lists it with its exact bytes, reads it and deletes it.
// Only a NEW name is asked (leafname).
func TestMCP_PlantedControlNamesStayReachable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows holds no control character in a name")
	}
	planted := hostilenames.Filter(func(n hostilenames.Name) bool {
		return n.Refused && n.Has("control") && !strings.ContainsRune(n.Name, 0)
	})
	require.NotEmpty(t, planted, "the list lost its control-character names")
	srv, _, store, tok := aiFixture(t)
	for _, n := range planted {
		seedServerFile(t, store, "main", "p/"+n.Name, "planted "+n.ID)
	}

	isErr, text, structured := parityTool(t, srv.URL, tok, "file_list", map[string]any{"path": "main://p"})
	require.False(t, isErr, "file_list: %s", text)
	listed := mcpNames(t, structured)
	for _, n := range planted {
		require.Contains(t, listed, n.Name, "%s (%q) is not listed under its own name", n.ID, n.Name)

		p := "main://p/" + n.Name
		isErr, text, _ = parityTool(t, srv.URL, tok, "file_read", map[string]any{"path": p})
		require.False(t, isErr, "file_read %s: %s", n.ID, text)
		require.Contains(t, text, "planted "+n.ID)

		isErr, text, _ = parityTool(t, srv.URL, tok, "file_delete", map[string]any{"path": p})
		require.False(t, isErr, "file_delete %s: %s", n.ID, text)
	}

	isErr, text, structured = parityTool(t, srv.URL, tok, "file_list", map[string]any{"path": "main://p"})
	require.False(t, isErr, "file_list after the deletes: %s", text)
	require.Empty(t, mcpNames(t, structured), "a deleted file is still listed")
}

func TestMCP_HostileNamesTooLongSaySo(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this file system holds every name of the list")
	}
	srv, _, _, tok := aiFixture(t)
	tmp := filepath.ToSlash(os.TempDir())
	for _, n := range long {
		isErr, text, _ := parityTool(t, srv.URL, tok, "file_write", map[string]any{"path": "main://l/" + n.Name, "content": "x"})
		require.True(t, isErr, "%s (%d bytes) was written on a disk that holds 255: %s", n.ID, n.Bytes(), text)
		require.Contains(t, strings.ToLower(text), "too long", "%s: the agent is not told why: %s", n.ID, text)
		require.NotContains(t, filepath.ToSlash(text), tmp, "%s: the answer carries the server's own path: %s", n.ID, text)
	}
}
