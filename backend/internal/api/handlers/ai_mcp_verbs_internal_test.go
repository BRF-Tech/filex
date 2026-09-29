package handlers

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

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
	for _, scopes := range []string{"mcp", "read,mcp", "read,write,mcp", "read,delete,mcp", "read,write,delete,mcp"} {
		tok := &model.APIToken{Scopes: scopes}
		got := offered(scopes)
		for name, verb := range fileToolVerb {
			if want := verb == "" || tok.HasScope(verb); got[name] != want {
				t.Errorf("%s with %q: offered=%v, want %v", name, scopes, got[name], want)
			}
		}
	}
}
