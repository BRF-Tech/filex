package wasmplugin

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── encrypted_folders (filex 0.56.0, task #189) ──────────────────────────
//
// An app's interface may edit a file of an unlocked encrypted folder: the
// browser decrypts it for the frame and encrypts the save (e2eplain.go,
// handlers/app_ui_e2e.go). `encrypted_folders.open` asks for it, and the
// grant `files:e2e-plaintext` is derived from it like the interface's own.
// All red before 0.56: the field and the permission did not exist (0.55
// refused `encrypted_folders` as an unknown field and `files:e2e-plaintext`
// as an unknown permission - measured with the office editor's draft
// manifest, 2026-10-09).

func e2eManifest(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	return uiManifest(t, func(m map[string]any) {
		m["encrypted_folders"] = map[string]any{"open": true}
		if mutate != nil {
			mutate(m)
		}
	})
}

func TestEncryptedFolders_TheGrantIsDerivedReviewedAndOffUnlessAsked(t *testing.T) {
	m := mustParse(t, e2eManifest(t, nil))
	assert.Contains(t, m.Perms, PermFilesE2EPlaintext)
	assert.False(t, m.NeedsModule(), "it belongs to the interface: an app that is only an interface may hold it")

	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if Permission(r.ID) == PermFilesE2EPlaintext {
				labels[lang] = r.Label
			}
		}
	}
	// The review says what it means: the app sees the plaintext, the server
	// does not.
	assert.Contains(t, labels["en"], "in the clear")
	assert.Contains(t, labels["en"], "the app sees every such file opened with it")
	assert.Contains(t, labels["en"], "only encrypted data")
	assert.Contains(t, labels["tr"], "şifresiz verir")
	assert.Contains(t, labels["tr"], "yalnızca şifreli veri")
	assert.NotEqual(t, labels["en"], labels["tr"])
	assert.NotEqual(t, "files:e2e-plaintext", labels["en"], "a sentence, never the id")

	plain := mustParse(t, uiManifest(t, nil))
	assert.NotContains(t, plain.Perms, PermFilesE2EPlaintext, "off unless the manifest asks")
	off := mustParse(t, uiManifest(t, func(m map[string]any) { m["encrypted_folders"] = map[string]any{"open": false} }))
	assert.NotContains(t, off.Perms, PermFilesE2EPlaintext, "false is off")
	empty := mustParse(t, uiManifest(t, func(m map[string]any) { m["encrypted_folders"] = map[string]any{} }))
	assert.NotContains(t, empty.Perms, PermFilesE2EPlaintext, "an empty block asks for nothing")
}

func TestEncryptedFolders_ThePermissionIsNeverWrittenByHand(t *testing.T) {
	_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "files:e2e-plaintext")
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "derived from the encrypted_folders block")

	// But it is a permission this filex knows: a stored grant and an
	// administrator's approval naming it read back.
	p, err := ParsePermission("files:e2e-plaintext")
	require.NoError(t, err)
	assert.Equal(t, PermFilesE2EPlaintext, p)
}

// The plaintext goes to the app's own frame, in a viewer, for a file it
// reads: the block needs all three.
func TestEncryptedFolders_NeedAnInterfaceAViewerAndRead(t *testing.T) {
	for name, mutate := range map[string]func(m map[string]any){
		"no interface": func(m map[string]any) {
			delete(m, "ui")
			m["views"] = []any{map[string]any{"id": "x", "placement": "modal", "label": map[string]any{"en": "X"}}}
		},
		"no viewer": func(m map[string]any) {
			m["views"] = []any{map[string]any{"id": "editor", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Draw"}}}
		},
		"no files:read": func(m map[string]any) { m["permissions"] = []any{"files:write"} },
	} {
		_, err := ParseManifest(e2eManifest(t, mutate))
		assert.Error(t, err, name)
	}
	// An unknown field in the block is refused, like everywhere in a
	// manifest: a typo must not install an app that asked for nothing.
	_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["encrypted_folders"] = map[string]any{"opne": true}
	}))
	assert.Error(t, err)
}

// An update that starts asking is a new grant: the administrator approves it
// again.
func TestEncryptedFolders_AnUpdateThatAsksIsANewGrant(t *testing.T) {
	before := mustParse(t, uiManifest(t, nil))
	after := mustParse(t, e2eManifest(t, nil))
	missing := NewGrants(before.Perms).Missing(after.Perms)
	assert.Contains(t, missing, PermFilesE2EPlaintext)
}

// The module's describe is its proof of what it is (security review UI-2):
// a module app's interface that is handed plaintext must be described as
// such, or the signature never saw the grant.
func TestEncryptedFolders_ArePartOfTheDescribedInterface(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	want := mustParse(t, e2eManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": sum}}
	}))
	r := &Registry{}
	got := &wire.Manifest{UI: &wire.UISpec{Bundle: wire.UIBundle{SHA256: sum}}}
	assert.Error(t, r.checkDescribedUI(want, got), "the module does not ask for encrypted folders")
	got.EncryptedFolders = &wire.EncryptedFolders{Open: true}
	assert.NoError(t, r.checkDescribedUI(want, got))

	plain := mustParse(t, uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": sum}}
	}))
	assert.Error(t, r.checkDescribedUI(plain, got), "a module that asks for more than the manifest")
}

// The administrator's switch: on unless turned off, a typo is not "off".
func TestEncryptedFolders_TheSwitchIsOnUnlessTurnedOff(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "apps.e2e_plaintext", E2EPlaintextSetting.Key)
	assert.Equal(t, "FILEX_APPS_E2E_PLAINTEXT", E2EPlaintextSetting.EnvVar)
	assert.True(t, E2EPlaintextSetting.Resolve(ctx, nil))
	assert.False(t, E2EPlaintextSetting.Resolve(ctx, e2eSettings{"apps.e2e_plaintext": "false"}))
	assert.True(t, E2EPlaintextSetting.Resolve(ctx, e2eSettings{"apps.e2e_plaintext": "true"}))
	assert.True(t, E2EPlaintextSetting.Resolve(ctx, e2eSettings{}))
}

type e2eSettings map[string]string

func (s e2eSettings) GetSetting(_ context.Context, k string) (string, error) { return s[k], nil }
