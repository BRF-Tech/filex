package dav

// A COPY or a MOVE over WebDAV puts the source's bytes at another path. Where
// a role's part refuses files.download on the source, the pre-gate refuses a
// COPY or MOVE to a path where the same caller could download them
// (protoperm.TreeDownloadLifted), and lets one that stays where download is
// refused go through.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// downloadRuleHarness is permHarness plus Locked/secret.txt, and a role for the
// user whose part refuses files.download under Locked on that storage.
func downloadRuleHarness(t *testing.T) (*harness, string) {
	t.Helper()
	ha, root := permHarness(t)
	ctx := context.Background()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Locked", "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Locked", "secret.txt"), []byte("kept"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Locked", "inner", "deep.txt"), []byte("kept too"), 0o644))

	st, err := ha.store.GetStorageByName(ctx, "depo")
	require.NoError(t, err)
	u, err := ha.store.GetUserByEmail(ctx, permUser)
	require.NoError(t, err)
	rule, err := ha.store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Locked stays", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{st.ID}, Paths: []string{"Locked"}},
	})
	require.NoError(t, err)
	require.NoError(t, ha.store.SetUserCustomRole(ctx, u.ID, rule.ID))
	perm.Invalidate()
	return ha, root
}

func TestDAV_CopyKeepsADownloadRuleWhereItWas(t *testing.T) {
	ha, root := downloadRuleHarness(t)

	// The rule holds on the file itself.
	resp := ha.req(t, http.MethodGet, "/dav/depo/Locked/secret.txt", permUser, permPass, "", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, bodyString(t, resp))

	resp = ha.req(t, "COPY", "/dav/depo/Locked/secret.txt", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/out.txt"})
	body := bodyString(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a copy out of Locked: %s", body)
	assert.Contains(t, body, string(perm.FilesDownload), "the refusal names the permission")
	assert.NoFileExists(t, filepath.Join(root, "out.txt"))

	// A folder copy is judged by every file in it.
	resp = ha.req(t, "COPY", "/dav/depo/Locked", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/Open", "Depth": "infinity"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a folder copy out of Locked: %s", bodyString(t, resp))
	assert.NoDirExists(t, filepath.Join(root, "Open"))

	// A copy that stays where download is refused goes through, and so does
	// any copy of a file the rule does not touch.
	resp = ha.req(t, "COPY", "/dav/depo/Locked/secret.txt", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/Locked/inner/again.txt"})
	assert.Less(t, resp.StatusCode, 300, "a copy inside Locked: %s", bodyString(t, resp))
	resp = ha.req(t, "COPY", "/dav/depo/report.txt", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/report-copy.txt"})
	assert.Less(t, resp.StatusCode, 300, "a copy of an ordinary file: %s", bodyString(t, resp))
	assert.FileExists(t, filepath.Join(root, "report-copy.txt"))
}

func TestDAV_MoveKeepsADownloadRuleWhereItWas(t *testing.T) {
	ha, root := downloadRuleHarness(t)

	// A rename that steps out of the rule, and a move out of the folder.
	for _, dest := range []string{"/dav/depo/renamed.txt", "/dav/depo/Locked-not.txt"} {
		resp := ha.req(t, "MOVE", "/dav/depo/Locked/secret.txt", permUser, permPass, "", map[string]string{"Destination": dest})
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "MOVE to %s: %s", dest, bodyString(t, resp))
	}
	resp := ha.req(t, "MOVE", "/dav/depo/Locked/inner", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/inner"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a folder moved out of Locked: %s", bodyString(t, resp))
	assert.FileExists(t, filepath.Join(root, "Locked", "secret.txt"))
	assert.FileExists(t, filepath.Join(root, "Locked", "inner", "deep.txt"))

	// Within Locked the file may still be renamed.
	resp = ha.req(t, "MOVE", "/dav/depo/Locked/secret.txt", permUser, permPass, "", map[string]string{"Destination": "/dav/depo/Locked/secret-v2.txt"})
	assert.Less(t, resp.StatusCode, 300, "a rename inside Locked: %s", bodyString(t, resp))
	assert.FileExists(t, filepath.Join(root, "Locked", "secret-v2.txt"))
}
