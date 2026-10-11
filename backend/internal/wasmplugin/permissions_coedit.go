package wasmplugin

import (
	"fmt"
	"strings"
)

// ── An app that edits a file together (filex 0.56.0, task #189 P4) ──────
//
// An app's interface edits a file with other people through filex's relay
// (internal/coedit, the bridge's `coedit.*`, docs/APP-PLUGINS-API.md →
// Editing together): what each member's editor changes is sealed by filex's
// page with the session's key, ordered by the relay without being read, and
// opened again by every other member's page. The app never holds a key and
// never opens a connection.
//
// The manifest says it in `co_edit: {"open": true}`, and the grant is DERIVED
// from that block (`files:co-edit`), the way `encrypted_folders` derives
// `files:e2e-plaintext`: one place says what the app does, so a hand-written
// permission and the block can never disagree, and an update that starts
// asking is a new grant. The review says it in its own sentence (srvtext
// `server.perm.files_co_edit`). Who may change the file stays filex's own
// answer: a member writes only where the person may change the file and the
// app holds files:write; a person who may only read it follows along.

// PermFilesCoEdit: the app's interface edits the files it opens together
// with other people, through filex's relay. Derived from `co_edit.open`,
// never listed in `permissions`.
const PermFilesCoEdit Permission = "files:co-edit"

// Parsed here so a stored grant and an approval naming it read back.
func init() { bare[PermFilesCoEdit] = true }

// isCoEditPerm reports whether p is derived from the `co_edit` block.
func isCoEditPerm(p Permission) bool { return p == PermFilesCoEdit }

// coEditOpen is what the block says, nil-safe.
func coEditOpen(m *Manifest) bool {
	return m != nil && m.CoEdit != nil && m.CoEdit.Open
}

// checkCoEdit validates the `co_edit` block and adds its derived permission
// to Perms. Called by Validate once Perms holds the manifest's own list.
//
// Editing together is the interface's (the bridge's `coedit.*`), in the view
// a file is opened in: the block needs an interface, a `viewer` view and
// `files:read` (a member opens the document).
func (m *Manifest) checkCoEdit() error {
	for _, raw := range m.Permissions {
		if Permission(strings.TrimSpace(raw)) == PermFilesCoEdit {
			return fmt.Errorf("manifest: permission %q is derived from the co_edit block - leave it out of permissions", raw)
		}
	}
	if !coEditOpen(m) {
		return nil
	}
	if m.UI == nil {
		return fmt.Errorf("manifest: co_edit.open is for an app's own interface, and the manifest has no ui block")
	}
	if !m.hasViewerUI() {
		return fmt.Errorf("manifest: co_edit.open: a file is edited together where it opens, and no view is a viewer with a ui file")
	}
	if !m.asks(PermFilesRead) {
		return fmt.Errorf("manifest: co_edit.open needs the files:read permission - a member opens the document it edits")
	}
	m.Perms = append(m.Perms, PermFilesCoEdit)
	return nil
}
