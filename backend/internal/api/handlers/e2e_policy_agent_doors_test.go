package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
)

// The doors 0.50 opened on the agent surface (ai_doors.go) pass the explorer's
// own routes, and what they create is held to who may encrypt like everything
// else: with the policy off, file_copy and archive_create onto a `.fxe` meet
// the rule's 403 e2e_not_allowed — a tool's error result over MCP, the 403
// itself on the REST twin — and an encrypted folder's key file meets the
// keyless surface's RESERVED_NAME before the rule. archive_extract skips a
// `.fxe` member and extracts the rest, and the same archive under `permitted`
// lands the `.fxe` too: the skip is the policy's. Nothing lands that was
// refused.
//
// app_run is not walked: what a job writes is named by the app, not by the
// caller, and no fixture app names an output like an encryption. Its output
// is committed through the one sink every app write passes
// (AppPlugins.CommitSibling, judged for the job's person), which the door
// matrix and e2e_policy_app_test.go hold to the rule.
func TestE2EPolicy_TheAgentDoorsOf050RefuseAnEncryptionName(t *testing.T) {
	f := newDoorFix(t)
	ctx := context.Background()
	f.put(t, f.Tok, "main://docs/rapor.txt", "plain")
	zip := buildZip(t, map[string]string{"alt/gizli.fxe": fxeBody, "oku.txt": "plain"})
	restCall(t, f.URL, f.Tok, "/api/ai/upload", map[string]any{"path": "main://gelen/paket.zip", "content_base64": base64.StdEncoding.EncodeToString(zip)})
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))

	// The rule's refusal, in a door tool's shape and in the REST twin's.
	ruleRefused := func(what string, tl e2eAITool) {
		t.Helper()
		require.True(t, tl.IsError, "%s went through: %s", what, tl.Raw)
		status, res := doorResult(t, tl)
		assert.Equal(t, http.StatusForbidden, status, "%s: %s", what, tl.Raw)
		assert.Equal(t, "e2e_not_allowed", res["error"], "%s: %s", what, tl.Raw)
		assert.Equal(t, string(e2epolicy.ReasonPolicyOff), res["reason"], "%s: %s", what, tl.Raw)
	}
	restRefused := func(what string, code int, out map[string]any) {
		t.Helper()
		assert.Equal(t, http.StatusForbidden, code, "%s: %v", what, out)
		assert.Equal(t, "e2e_not_allowed", out["error"], "%s: %v", what, out)
	}
	reserved := func(what string, tl e2eAITool) {
		t.Helper()
		require.True(t, tl.IsError, "%s went through: %s", what, tl.Raw)
		assert.Contains(t, tl.Text, "RESERVED_NAME", "%s: %s", what, tl.Raw)
	}
	notLanded := func(rels ...string) {
		t.Helper()
		for _, rel := range rels {
			_, ok := f.read(t, f.RootMain, rel)
			assert.False(t, ok, "%s landed", rel)
		}
	}

	// file_copy
	ruleRefused("file_copy onto x.fxe", f.tool(t, f.Tok, "file_copy", map[string]any{"src": "main://docs/rapor.txt", "dst": "main://docs/x.fxe"}))
	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/copy", map[string]any{"src": "main://docs/rapor.txt", "dst": "main://docs/y.fxe"})
	restRefused("POST /api/ai/copy onto y.fxe", code, out)
	reserved("file_copy onto a key file", f.tool(t, f.Tok, "file_copy", map[string]any{"src": "main://docs/rapor.txt", "dst": "main://docs/" + e2eKeyFile}))

	// archive_create
	ruleRefused("archive_create onto paket.fxe", f.tool(t, f.Tok, "archive_create", map[string]any{
		"sources": []string{"main://docs/rapor.txt"}, "dest": "main://docs/paket.fxe", "format": "zip",
	}))
	code, out = f.rest(t, f.Tok, http.MethodPost, "/api/ai/archive/create", map[string]any{
		"sources": []string{"main://docs/rapor.txt"}, "dest": "main://docs/arsiv.fxe", "format": "zip",
	})
	restRefused("POST /api/ai/archive/create onto arsiv.fxe", code, out)
	reserved("archive_create onto a key file", f.tool(t, f.Tok, "archive_create", map[string]any{
		"sources": []string{"main://docs/rapor.txt"}, "dest": "main://docs/" + e2eKeyFile, "format": "zip",
	}))

	// archive_extract: the `.fxe` member is skipped, the rest lands.
	tl := f.tool(t, f.Tok, "archive_extract", map[string]any{"path": "main://gelen/paket.zip", "dest": "main://acilan"})
	require.False(t, tl.IsError, tl.Raw)
	_, res := doorResult(t, tl)
	op := f.waitOp(t, f.Tok, opIDOf(t, res))
	require.Contains(t, []any{ops.StatusOK, ops.StatusPartial}, op["status"], "%v", op)
	got, ok := f.read(t, f.RootMain, "acilan/oku.txt")
	require.True(t, ok, "precondition: the ordinary member was extracted")
	assert.Equal(t, "plain", got)

	notLanded("docs/x.fxe", "docs/y.fxe", "docs/x-copy.fxe", "docs/"+e2eKeyFile, "docs/paket.fxe", "docs/arsiv.fxe", "acilan/alt/gizli.fxe")
	// A refused copy queued nothing: no operation names the copy's source.
	tl = f.tool(t, f.Tok, "ops_list", map[string]any{})
	require.False(t, tl.IsError, tl.Raw)
	_, listed := doorResult(t, tl)
	raw, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "rapor.txt", "a refused copy was queued: %s", raw)

	// The control: the same archive under `permitted` lands the `.fxe` member
	// too. Without it the skip above could be the extraction's doing for some
	// other reason (a nested member that never reaches the disk, say) and the
	// refusal case would pass all the same.
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyPermitted))
	tl = f.tool(t, f.Tok, "archive_extract", map[string]any{"path": "main://gelen/paket.zip", "dest": "main://izinli"})
	require.False(t, tl.IsError, tl.Raw)
	_, res = doorResult(t, tl)
	op = f.waitOp(t, f.Tok, opIDOf(t, res))
	require.Equal(t, ops.StatusOK, op["status"], "%v", op)
	got, ok = f.read(t, f.RootMain, "izinli/alt/gizli.fxe")
	require.True(t, ok, "the .fxe member did not land under permitted")
	assert.Equal(t, fxeBody, got)
	got, ok = f.read(t, f.RootMain, "izinli/oku.txt")
	require.True(t, ok, "the ordinary member did not land under permitted")
	assert.Equal(t, "plain", got)
}
