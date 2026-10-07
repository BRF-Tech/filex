package handlers

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// offeredTools connects an in-memory client to srv and lists its tools.
func offeredTools(t *testing.T, srv *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "verbs-test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// TestFileToolVerb_EveryFileToolHasAVerb — the table and the registration say
// the same thing. A file tool with no row would be offered to every `mcp`
// token whatever its verbs; a row with no tool is a table that drifted.
func TestFileToolVerb_EveryFileToolHasAVerb(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "verbs-test", Version: "0"}, nil)
	registerFilexTools(srv, &aiOps{}, nil)
	names := offeredTools(t, srv)
	require.GreaterOrEqual(t, len(names), 15, "registered %v", names)

	registered := map[string]bool{}
	for _, n := range names {
		registered[n] = true
		if _, ok := fileToolVerb[n]; !ok {
			t.Errorf("%s is registered but has no verb in fileToolVerb", n)
		}
	}
	for n := range fileToolVerb {
		if !registered[n] {
			t.Errorf("fileToolVerb names %s, which registerFilexTools no longer offers", n)
		}
	}
	for n := range fileToolPerm {
		if !registered[n] {
			t.Errorf("fileToolPerm names %s, which registerFilexTools no longer offers", n)
		}
	}
}

// TestWithdrawUngrantedFileTools — what each verb set is offered.
func TestWithdrawUngrantedFileTools(t *testing.T) {
	offered := func(scopes string) map[string]bool {
		srv := mcp.NewServer(&mcp.Implementation{Name: "verbs-test", Version: "0"}, nil)
		registerFilexTools(srv, &aiOps{}, nil)
		withdrawUngrantedFileTools(srv, &model.APIToken{Scopes: scopes})
		out := map[string]bool{}
		for _, n := range offeredTools(t, srv) {
			out[n] = true
		}
		return out
	}
	for _, scopes := range []string{
		"mcp", "read,mcp", "read,write,mcp", "read,delete,mcp", "read,write,delete,mcp",
		"read,mcp,comments:rw", "read,write,delete,mcp,comments:rw", "mcp,comments:rw",
	} {
		tok := &model.APIToken{Scopes: scopes}
		got := offered(scopes)
		for name, verb := range fileToolVerb {
			want := verb == "" || tok.HasScope(verb)
			if need, ok := fileToolPerm[name]; ok && !auth.TokenHolds(tok, need) {
				want = false
			}
			if got[name] != want {
				t.Errorf("%s with %q: offered=%v, want %v", name, scopes, got[name], want)
			}
		}
	}
}

// TestWithdrawUngrantedFileTools_CommentsAskTheirPermission - task #157:
// adding and deleting a comment are offered to a token that holds the
// comments permission at `rw`, with or without `write`, and to no other -
// not to one with every verb that does not name it. Reading the comments
// stays with `read`.
func TestWithdrawUngrantedFileTools_CommentsAskTheirPermission(t *testing.T) {
	offered := func(scopes string) map[string]bool {
		srv := mcp.NewServer(&mcp.Implementation{Name: "verbs-test", Version: "0"}, nil)
		registerFilexTools(srv, &aiOps{}, nil)
		withdrawUngrantedFileTools(srv, &model.APIToken{Scopes: scopes})
		out := map[string]bool{}
		for _, n := range offeredTools(t, srv) {
			out[n] = true
		}
		return out
	}
	everyVerb := offered("read,write,delete,mcp")
	require.True(t, everyVerb["file_comments"], "reading comments needs read")
	require.False(t, everyVerb["file_comment_add"], "write does not stand in for comments:rw")
	require.False(t, everyVerb["file_comment_delete"], "write does not stand in for comments:rw")

	commenter := offered("read,mcp,comments:rw")
	for _, n := range []string{"file_comments", "file_comment_add", "file_comment_delete"} {
		require.True(t, commenter[n], "read,mcp,comments:rw is offered %s", n)
	}
	require.False(t, commenter["file_write"], "comments:rw is not write")
}
