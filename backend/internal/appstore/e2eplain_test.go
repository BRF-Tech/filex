package appstore_test

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/appstore"
)

// filex 0.56 derives files:e2e-plaintext from the manifest's
// `encrypted_folders` block (wasmplugin/e2eplain.go), so a store link that
// lists it - the store reads it off a 0.56 dry run - pins nothing the
// manifest's own `permissions` must declare, like the interface's ui:*
// permissions. A permission the manifest really declares still counts.
//
// RED PROOF: derivedPermission did not know the id, the link's list held one
// permission the source's did not, and ComparePins answered pin_mismatch for
// the office editor's 0.2.0 link.
func TestComparePins_TheEncryptedFoldersGrantIsDerived(t *testing.T) {
	manifest := []byte(`{"name":"office-editor"}`)
	sum := appstoreSHA(manifest)
	in := &appstore.Intent{App: "office-editor", Version: "0.2.0", Kind: "app", ManifestSHA256: sum,
		UISHA256: strings.Repeat("2", 64), Permissions: []string{"files:read", "files:write", "files:e2e-plaintext", "ui", "ui-viewer:.docx"}}
	src := &appstore.Source{ManifestBytes: manifest, Name: "office-editor", Version: "0.2.0", Kind: "app",
		UISHA256: strings.Repeat("2", 64), DeclaredPermissions: []string{"files:write", "files:read"}}
	if err := appstore.ComparePins(in, src); err != nil {
		t.Fatalf("the derived files:e2e-plaintext in the link: %v", err)
	}
	more := *src
	more.DeclaredPermissions = append([]string{"mail:send"}, src.DeclaredPermissions...)
	if err := appstore.ComparePins(in, &more); code(err) != appstore.CodePinMismatch {
		t.Fatalf("a declared permission the link lacks: want %s, got %v", appstore.CodePinMismatch, err)
	}
}
