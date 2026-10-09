package wasmplugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── ui.print (task #189 P1b) ──────────────────────────────────────────────
//
// A sandboxed frame may not open the browser's print dialog, so an interface
// hands filex a PDF and filex prints it from its own page (core AppFrame,
// internal/printframe). `ui.print` in the `ui` block asks for it; the derived
// permission `ui:print` is reviewed like `ui:download` - the print dialog can
// save the PDF as well - and nothing has it unless it asked. All red before
// #189 P1b: the field and the permission did not exist.

func TestUIPrint_IsDerivedReviewedAndOffUnlessAsked(t *testing.T) {
	m := mustParse(t, frameBlobManifest(t, map[string]any{"print": true}))
	assert.Contains(t, m.Perms, PermUIPrint)
	assert.False(t, m.NeedsModule(), "it belongs to the interface: an app that is only an interface may hold it")

	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if Permission(r.ID) == PermUIPrint {
				labels[lang] = r.Label
			}
		}
	}
	assert.Equal(t, "Its interface can print documents it hands to filex (a PDF) - each time you allow it; the print dialog can also save the PDF to your computer", labels["en"])
	assert.Equal(t, "Arayüzü, filex'e verdiği belgeleri (bir PDF) yazdırabilir - her seferinde sizin izninizle; yazdırma penceresi PDF'i bilgisayarınıza da kaydedebilir", labels["tr"])

	plain := mustParse(t, uiManifest(t, nil))
	assert.NotContains(t, plain.Perms, PermUIPrint, "off unless the manifest asks")
	off := mustParse(t, frameBlobManifest(t, map[string]any{"print": false}))
	assert.NotContains(t, off.Perms, PermUIPrint, "false is off")
	dl := mustParse(t, frameBlobManifest(t, map[string]any{"download": true}))
	assert.NotContains(t, dl.Perms, PermUIPrint, "ui:download does not bring ui:print")

	_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "ui:print")
	}))
	assert.Error(t, err, "ui:print is derived from the ui block: never written into permissions")
}

// The print grant changes nothing in the interface's policy: filex prints,
// from its own page; the frame gets no modal, no frame, no connection.
func TestUIPrint_LeavesTheInterfacePolicyAlone(t *testing.T) {
	base, baseAllow := UIPolicy(NewGrants([]Permission{PermUI}), frameBlobPkg)
	with, allow := UIPolicy(NewGrants([]Permission{PermUI, PermUIPrint}), frameBlobPkg)
	assert.Equal(t, base, with)
	assert.Equal(t, baseAllow, allow)
}

func TestUIPrint_IsPartOfTheDescribedInterface(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	want := mustParse(t, uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": sum}, "print": true}
	}))
	r := &Registry{}
	got := &wire.Manifest{UI: &wire.UISpec{Bundle: wire.UIBundle{SHA256: sum}}}
	assert.Error(t, r.checkDescribedUI(want, got), "the module does not ask for print")
	got.UI.Print = true
	assert.NoError(t, r.checkDescribedUI(want, got))
}
