package wasmplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── co_edit / files:co-edit (task #189 P4) ────────────────────────────────
//
// Editing a file together through filex's relay is said in the manifest's
// `co_edit` block and granted as the DERIVED permission `files:co-edit`, the
// way `encrypted_folders` derives `files:e2e-plaintext`: reviewed in the
// reader's language, held by an app that is only an interface, only with a
// viewer interface and files:read, and never written into `permissions`.
// All red before 0.56: the block and the name were unknown.

func coEditManifest(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	return uiManifest(t, func(m map[string]any) {
		m["co_edit"] = map[string]any{"open": true}
		if mutate != nil {
			mutate(m)
		}
	})
}

func TestCoEdit_IsDerivedReviewedAndHeldByAnInterfaceOnlyApp(t *testing.T) {
	m := mustParse(t, coEditManifest(t, nil))
	assert.Contains(t, m.Perms, PermFilesCoEdit)
	assert.False(t, m.NeedsModule(), "the bridge's coedit.* need no module: the office editor is only an interface")

	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if Permission(r.ID) == PermFilesCoEdit {
				labels[lang] = r.Label
			}
		}
	}
	require.NotEmpty(t, labels["en"])
	assert.NotEqual(t, "files:co-edit", labels["en"], "a sentence, never the id")
	assert.Contains(t, labels["en"], "together")
	assert.Contains(t, labels["tr"], "birlikte")

	plain := mustParse(t, uiManifest(t, nil))
	assert.NotContains(t, plain.Perms, PermFilesCoEdit, "off unless the block asks")
	off := mustParse(t, uiManifest(t, func(m map[string]any) { m["co_edit"] = map[string]any{"open": false} }))
	assert.NotContains(t, off.Perms, PermFilesCoEdit, "false is off")

	p, err := ParsePermission("files:co-edit")
	require.NoError(t, err, "a stored grant reads back")
	assert.Equal(t, PermFilesCoEdit, p)
}

func TestCoEdit_NeedsAViewerInterfaceAndFilesReadAndIsNeverListed(t *testing.T) {
	_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "files:co-edit")
	}))
	assert.ErrorContains(t, err, "derived from the co_edit block")

	_, err = ParseManifest(coEditManifest(t, func(m map[string]any) {
		m["permissions"] = []any{"files:write"}
	}))
	assert.ErrorContains(t, err, "files:read")

	_, err = ParseManifest(coEditManifest(t, func(m map[string]any) {
		views := m["views"].([]any)
		views[0].(map[string]any)["placement"] = "modal"
	}))
	assert.ErrorContains(t, err, "viewer")
}

// An interface-only app may hold the 0.56 grants the bridge answers to:
// files:co-edit and files:e2e-plaintext (the office editor asks for both).
func TestCoEdit_AndThePlaintextGrantNeedNoModule(t *testing.T) {
	m := mustParse(t, coEditManifest(t, func(m map[string]any) {
		m["encrypted_folders"] = map[string]any{"open": true}
	}))
	assert.Contains(t, m.Perms, PermFilesCoEdit)
	assert.Contains(t, m.Perms, PermFilesE2EPlaintext)
	assert.False(t, m.NeedsModule())
}
