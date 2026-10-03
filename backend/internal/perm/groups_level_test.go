package perm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

// LevelUnderGroups is the one rule for the built-in level of an account with
// no custom role of its own: group.SyncLevels writes it, and the previews a
// delegated administrator is judged by (Loader.Preview, the handlers'
// groupsPreview) read it — so a change is judged at the level the account
// will really be on, not at a second copy's idea of it.
func TestLevelUnderGroups(t *testing.T) {
	readOnly := &model.PermissionRule{ID: 1, Name: "Look", Enabled: true, Permissions: ReadOnly.Strings()}
	writer := &model.PermissionRule{ID: 2, Name: "Write", Enabled: true, Permissions: Standard.Strings()}

	assert.Equal(t, model.RoleViewer, LevelUnderGroups(model.RoleUser, readOnly, ""), "a read-only group role makes a Viewer")
	assert.Equal(t, model.RoleUser, LevelUnderGroups(model.RoleViewer, writer, model.RoleViewer), "a writing one a User, whatever was kept")
	assert.Equal(t, model.RoleUser, LevelUnderGroups(model.RoleViewer, nil, model.RoleUser), "no group role: the level kept from before comes back")
	assert.Equal(t, model.RoleViewer, LevelUnderGroups(model.RoleViewer, nil, ""), "nothing kept: the level stays")
	assert.Equal(t, model.RoleViewer, LevelUnderGroups(model.RoleViewer, nil, model.RoleAdmin), "a kept level is never administrator")
	assert.Equal(t, model.RoleViewer, LevelUnderGroups(model.RoleViewer, nil, "nonsense"), "nor anything that is not a role")
}
