package wasmplugin

// A public link an app opens hands over only what the person its job runs for
// may download (files.download, as a download link from the Share dialog): a
// link of the document asks the document, a page that hands over copies asks
// every input they are made from. A page that hands over no copy is the app's
// own content and is opened as before.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// roleStoreT is the part of db.Store that gives a person a custom role.
type roleStoreT interface {
	CreatePermissionRule(ctx context.Context, r *model.PermissionRule) (*model.PermissionRule, error)
	SetUserCustomRole(ctx context.Context, userID, roleID int64) error
}

// denyDownloadFor gives u a role that is the standard list everywhere and
// refuses files.download on these paths of the harness's storage.
func (h *harness) denyDownloadFor(t *testing.T, u *model.User, paths ...string) {
	t.Helper()
	rs, ok := h.store.(roleStoreT)
	require.True(t, ok, "the test store is not a full db.Store")
	ctx := context.Background()
	rule, err := rs.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "No download there", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{h.st.ID}, Paths: paths},
	})
	require.NoError(t, err)
	require.NoError(t, rs.SetUserCustomRole(ctx, u.ID, rule.ID))
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
}

func TestShareCreate_HandsOverOnlyWhatThePersonMayDownload(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/gizli.txt", "gizli")
	h.writeCatalogued(t, "docs/acik.txt", "acik")
	u := h.actor(t)
	h.denyDownloadFor(t, u, "docs/gizli.txt")
	copyOf := func(s *Scope, i int) []map[string]any {
		return []map[string]any{{"ref": s.Inputs()[i].Ref, "name": "copy.txt"}}
	}

	// The document itself, as a download link.
	_, err := shareCreate(t, h.jobScope(t, p, u, "docs/gizli.txt"), map[string]any{"subject": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may not download")

	// A page that hands over a copy of it.
	s := h.jobScope(t, p, u, "docs/gizli.txt")
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x", "files": copyOf(s, 0)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may not download")

	// A page with copies is made from every input of the job.
	s = h.jobScope(t, p, u, "docs/acik.txt", "docs/gizli.txt")
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x", "files": copyOf(s, 0)})
	require.Error(t, err)

	// A page that hands over no copy is the app's own content: opened.
	_, err = shareCreate(t, h.jobScope(t, p, u, "docs/gizli.txt"), map[string]any{"page_id": "signer", "subject": "x"})
	require.NoError(t, err)

	// What the person may download is shared as before - also a link of one
	// input of a job that has a refused one beside it.
	_, err = shareCreate(t, h.jobScope(t, p, u, "docs/acik.txt"), map[string]any{"subject": "x"})
	require.NoError(t, err)
	s = h.jobScope(t, p, u, "docs/acik.txt")
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x", "files": copyOf(s, 0)})
	require.NoError(t, err)
	s = h.jobScope(t, p, u, "docs/acik.txt", "docs/gizli.txt")
	_, err = shareCreate(t, s, map[string]any{"subject": "x", "ref": s.Inputs()[0].Ref})
	require.NoError(t, err)
}
