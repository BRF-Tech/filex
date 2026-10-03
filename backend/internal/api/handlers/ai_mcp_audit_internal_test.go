package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// Every file tool that can change something has a REST twin to be audited as
// (mcpWriteTwin): the MCP transport is not audited, so a write tool without
// one would leave no row at all. The list of tools is fileToolVerb - which
// ai_mcp_verbs_internal_test already keeps equal to what registerFilexTools
// offers - so a new write tool goes red here until it is given a twin.
func TestMCPWriteToolsHaveAnAuditedRESTTwin(t *testing.T) {
	for tool, verb := range fileToolVerb {
		writes := verb == auth.VerbWrite || verb == auth.VerbDelete || tool == "file_tags"
		twin, ok := mcpWriteTwin[tool]
		if !writes {
			require.False(t, ok, "%s only reads; a read leaves no audit row", tool)
			continue
		}
		require.True(t, ok, "%s changes something and has no REST twin in mcpWriteTwin", tool)
		action, _, _ := auth.ActionForPath(http.MethodPost, twin, "", "")
		require.NotEmpty(t, action, "%s's twin %s is not audited", tool, twin)
	}
	for tool := range mcpWriteTwin {
		_, ok := fileToolVerb[tool]
		require.True(t, ok, "mcpWriteTwin names %s, which is not a file tool", tool)
	}
}
