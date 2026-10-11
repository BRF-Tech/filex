package wasmplugin

import (
	"fmt"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// ── An app that edits inside encrypted folders (filex 0.56.0, task #189) ─
//
// A file in an end-to-end encrypted folder is ciphertext on the server, and
// the server has no key to it (docs/E2E-ENCRYPTION.md). filex 0.55 refused
// it at every app door. 0.56 lets an app's INTERFACE edit it the way
// CryptPad's office documents are edited, without the server ever holding
// the plaintext:
//
//   - the person's browser, which holds the folder key while the folder is
//     unlocked, reads the ciphertext through the app's own read door
//     (handlers UIRead `?e2e=1`), decrypts it and hands the plaintext to the
//     app's sandboxed frame only, over the frame's MessagePort;
//   - what the app saves is encrypted in the browser with the same folder
//     key and written as a new version through the app's save door
//     (`?e2e=1`), which refuses a body that is not an encrypted file.
//
// The manifest says it in `encrypted_folders: {"open": true}`, and the grant
// is DERIVED from that block (`files:e2e-plaintext`), like the interface's
// own: one place says what the app does, so a hand-written permission and
// the block can never disagree, and an update that starts asking is a new
// grant. The install review says what it means in its own sentence (srvtext
// `server.perm.files_e2e_plaintext`): the app sees every such file opened
// with it.
//
// Three more locks than the grant, all on the server:
//
//   - the administrator's switch E2EPlaintextSetting (on unless turned
//     off, seeded once from FILEX_APPS_E2E_PLAINTEXT): off, both doors
//     refuse every app;
//   - not in a vault and not for a single encrypted file (`.fxe`) - 0.56
//     offers neither;
//   - every handover and every save is an audit row (app_plugin.e2e_read,
//     app_plugin.e2e_save).

// PermFilesE2EPlaintext: the app's interface is handed the plaintext of
// files in unlocked encrypted folders, decrypted in the person's browser,
// and what it saves is encrypted there again. Derived from
// `encrypted_folders.open`, never listed in `permissions`.
const PermFilesE2EPlaintext Permission = "files:e2e-plaintext"

// E2EPlaintextSetting is the administrator's switch for every app at once:
// off, no app is handed an encrypted file whatever it was granted. On by
// default - the grant itself is the administrator's yes, app by app, at
// install - and seeded once from FILEX_APPS_E2E_PLAINTEXT, then changed in
// the settings (`apps.e2e_plaintext`). Read at the point of use (both app
// doors, the capabilities answer), so a change applies at once.
var E2EPlaintextSetting = dbsetting.BoolSpec{
	Key: "apps.e2e_plaintext", EnvVar: "FILEX_APPS_E2E_PLAINTEXT", Default: true,
}

// isE2EPlainPerm reports whether p is derived from the `encrypted_folders`
// block.
func isE2EPlainPerm(p Permission) bool { return p == PermFilesE2EPlaintext }

// encryptedFoldersOpen is what the block says, nil-safe.
func encryptedFoldersOpen(m *Manifest) bool {
	return m != nil && m.EncryptedFolders != nil && m.EncryptedFolders.Open
}

// checkEncryptedFolders validates the `encrypted_folders` block and adds its
// derived permission to Perms. Called by Validate once Perms holds the
// manifest's own list and the views and the interface are checked.
//
// The plaintext goes to the app's own frame and nowhere else, so the block
// needs an interface and a `viewer` view (the only place the explorer opens
// an encrypted file in an app), and `files:read` (the read it changes).
func (m *Manifest) checkEncryptedFolders() error {
	if !encryptedFoldersOpen(m) {
		return nil
	}
	if m.UI == nil {
		return fmt.Errorf("manifest: encrypted_folders.open hands the plaintext to the app's interface only, and the manifest has no ui block")
	}
	if !m.hasViewerUI() {
		return fmt.Errorf("manifest: encrypted_folders.open: an encrypted file opens only in a viewer, and no view is a viewer with a ui file")
	}
	if !m.asks(PermFilesRead) {
		return fmt.Errorf("manifest: encrypted_folders.open needs the files:read permission")
	}
	m.Perms = append(m.Perms, PermFilesE2EPlaintext)
	return nil
}

// hasViewerUI reports whether a view is a `viewer` with an interface file:
// where the explorer opens a file in an app's own frame (encrypted_folders
// and co_edit both need one).
func (m *Manifest) hasViewerUI() bool {
	for _, v := range m.Views {
		if v.Placement == "viewer" && v.UI != "" {
			return true
		}
	}
	return false
}

// asks reports whether Perms (the manifest's own list, before a block adds
// what it derives) holds p.
func (m *Manifest) asks(p Permission) bool {
	for _, have := range m.Perms {
		if have == p {
			return true
		}
	}
	return false
}
