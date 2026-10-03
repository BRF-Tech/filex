package wasmplugin

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The maintainer, 2026-09-27: an app may add rows to filex's "New" menu
// (`new_documents: [{ext, label, view, template?}]`). A row makes a file of
// that kind — empty, or the app's template from its package — and opens it in
// the named view. Each kind is a derived permission on the review
// (`ui-new:.<ext>`), so an update that adds one asks again.

func newDocManifest(t *testing.T, mutate func(m map[string]any)) []byte {
	return uiManifest(t, func(m map[string]any) {
		m["languages"] = []any{"en", "tr"}
		m["label"] = map[string]any{"en": "Draw", "tr": "Çiz"}
		v := m["views"].([]any)[0].(map[string]any)
		v["label"] = map[string]any{"en": "Draw", "tr": "Çiz"}
		v["applies"] = map[string]any{"ext": []any{"drawio", "dio"}}
		m["new_documents"] = []any{
			map[string]any{"ext": "drawio", "label": map[string]any{"en": "Diagram", "tr": "Diyagram"}, "view": "editor", "template": "new/blank.xml"},
			map[string]any{"ext": "dio", "label": map[string]any{"en": "Diagram (short)", "tr": "Diyagram (kısa)"}, "view": "editor"},
		}
		if mutate != nil {
			mutate(m)
		}
	})
}

func TestUINewDocuments_AreDeclaredAndReviewed(t *testing.T) {
	m, err := ParseManifest(newDocManifest(t, nil))
	require.NoError(t, err)
	assert.Contains(t, m.Perms, Permission("ui-new:.drawio"))
	assert.Contains(t, m.Perms, Permission("ui-new:.dio"))
	rows := map[string]string{}
	for _, r := range PermissionRows(m, "en") {
		rows[r.ID] = r.Label
	}
	assert.Equal(t, "Adds a new .drawio file to the New menu; it opens in this app's interface", rows["ui-new:.drawio"])
	for _, r := range PermissionRows(m, "tr") {
		if r.ID == "ui-new:.drawio" {
			assert.Equal(t, "“Yeni” menüsüne yeni .drawio dosyası ekler; dosya bu uygulamanın arayüzünde açılır", r.Label)
		}
	}
	_, err = ParsePermission("ui-new:.drawio")
	assert.NoError(t, err)
	for _, bad := range []string{"ui-new:", "ui-new:drawio", "ui-new:.a b", "ui-new:.*"} {
		_, err := ParsePermission(bad)
		assert.Error(t, err, bad)
	}
}

func TestUINewDocuments_RefuseWhatCannotBeMadeOrOpened(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"not an extension": func(m map[string]any) { nd(m, 0)["ext"] = "a b" },
		"a dotted ext":     func(m map[string]any) { nd(m, 0)["ext"] = ".drawio" },
		"no English label": func(m map[string]any) { nd(m, 0)["label"] = map[string]any{"tr": "Diyagram"} },
		"a missing language": func(m map[string]any) {
			nd(m, 0)["label"] = map[string]any{"en": "Diagram"}
		},
		"no such view": func(m map[string]any) { nd(m, 0)["view"] = "nope" },
		"a view that is not a viewer": func(m map[string]any) {
			m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "P", "tr": "P"}})
			nd(m, 0)["view"] = "panel"
		},
		"a kind the view does not open": func(m map[string]any) { nd(m, 0)["ext"] = "svg" },
		"a template outside the package": func(m map[string]any) {
			nd(m, 0)["template"] = "../etc/passwd"
		},
		"the same kind twice": func(m map[string]any) { nd(m, 1)["ext"] = "drawio" },
		"no files:write": func(m map[string]any) {
			m["permissions"] = []any{"files:read"}
		},
		"written into permissions": func(m map[string]any) {
			m["permissions"] = append(m["permissions"].([]any), "ui-new:.drawio")
		},
		"too many": func(m map[string]any) {
			var list []any
			exts := []any{}
			for _, e := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"} {
				exts = append(exts, e)
				list = append(list, map[string]any{"ext": e, "label": map[string]any{"en": e, "tr": e}, "view": "editor"})
			}
			m["views"].([]any)[0].(map[string]any)["applies"] = map[string]any{"ext": exts}
			m["new_documents"] = list
		},
	}
	for name, mutate := range cases {
		_, err := ParseManifest(newDocManifest(t, mutate))
		assert.Error(t, err, name)
	}
	// A view that opens the kind by media type is enough.
	_, err := ParseManifest(newDocManifest(t, func(m map[string]any) {
		m["views"].([]any)[0].(map[string]any)["applies"] = map[string]any{"mime": []any{"image/svg+xml"}}
		m["new_documents"] = []any{map[string]any{"ext": "svg", "label": map[string]any{"en": "Drawing", "tr": "Çizim"}, "view": "editor"}}
	}))
	assert.NoError(t, err)
}

func nd(m map[string]any, i int) map[string]any {
	return m["new_documents"].([]any)[i].(map[string]any)
}

// The template is a file of the package: one it does not hold refuses the
// install; one it holds is what a new file is made of. A kind the grant does
// not hold is not offered.
func TestUINewDocuments_ComeFromTheRunningAppsGrant(t *testing.T) {
	h := newHarness(t, nil)
	files := uiDoc()
	man := newDocManifest(t, nil)

	m, err := ParseManifest(man)
	require.NoError(t, err)
	_, _, err = h.reg.Install(context.Background(), &InstallInput{
		Manifest: man, UI: strings.NewReader(string(uiZip(t, files))), Source: "upload", Granted: permStrings(m.Perms), Lang: "en",
	})
	require.Error(t, err, "the package holds no new/blank.xml")
	assert.Contains(t, err.Error(), "new/blank.xml")

	files["new/blank.xml"] = "<mxfile/>"
	p, _ := h.installUI(t, man, uiZip(t, files))
	docs := h.reg.NewDocuments()
	require.Len(t, docs, 2)
	assert.Equal(t, "app:drawio:drawio", docs[0].Key)
	assert.Equal(t, "drawio", docs[0].Plugin)
	assert.Equal(t, "editor", docs[0].View)
	assert.Equal(t, "Diagram", docs[0].Label["en"])
	assert.True(t, docs[0].Template)
	assert.False(t, docs[1].Template)

	b, err := h.reg.NewDocumentBytes(context.Background(), "app:drawio:drawio")
	require.NoError(t, err)
	assert.Equal(t, "<mxfile/>", string(b))
	b, err = h.reg.NewDocumentBytes(context.Background(), "app:drawio:dio")
	require.NoError(t, err)
	assert.Empty(t, b, "no template: an empty file")
	for _, k := range []string{"app:drawio:svg", "app:other:drawio", "drawio", "app:drawio"} {
		_, err := h.reg.NewDocumentBytes(context.Background(), k)
		assert.Error(t, err, k)
	}

	// A grant that does not hold the kind (a row written before it existed,
	// or by hand): that kind is not offered and not made.
	full := p.Grants
	var fewer []Permission
	for perm := range full {
		if perm != "ui-new:.drawio" {
			fewer = append(fewer, perm)
		}
	}
	p.Grants = NewGrants(fewer)
	require.Len(t, h.reg.NewDocuments(), 1)
	assert.Equal(t, "dio", h.reg.NewDocuments()[0].Ext)
	_, err = h.reg.NewDocumentBytes(context.Background(), "app:drawio:drawio")
	assert.Error(t, err)
	p.Grants = full

	// Switched off: nothing offered, nothing made.
	_, err = h.reg.SetEnabled(context.Background(), p.Row.ID, false)
	require.NoError(t, err)
	assert.Empty(t, h.reg.NewDocuments())
	_, err = h.reg.NewDocumentBytes(context.Background(), "app:drawio:drawio")
	assert.Error(t, err)
}

// The maintainer, 2026-09-27: `ui.download` — an interface may hand the person a file
// to keep on their own disk, through filex, each time with the person's say.
// The manifest asks (`ui.download: true`); the permission is derived and on
// the review, and part of the module's described interface.
func TestUIDownload_IsAPermissionTheManifestAsksFor(t *testing.T) {
	m, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": strings.Repeat("ab", 32)}, "download": true}
	}))
	require.NoError(t, err)
	assert.Contains(t, m.Perms, PermUIDownload)
	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if r.ID == string(PermUIDownload) {
				labels[lang] = r.Label
			}
		}
	}
	assert.Equal(t, "Its interface can save files to your computer - each time you allow it", labels["en"])
	assert.Equal(t, "Arayüzü bilgisayarınıza dosya kaydedebilir - her seferinde sizin izninizle", labels["tr"])

	plain, err := ParseManifest(uiManifest(t, nil))
	require.NoError(t, err)
	assert.NotContains(t, plain.Perms, PermUIDownload)
	_, err = ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "ui:download")
	}))
	assert.Error(t, err, "derived: never listed")

	r := &Registry{}
	got := &wire.Manifest{UI: &wire.UISpec{Bundle: wire.UIBundle{SHA256: strings.Repeat("ab", 32)}}}
	assert.Error(t, r.checkDescribedUI(m, got), "the module does not ask for it")
	got.UI.Download = true
	assert.NoError(t, r.checkDescribedUI(m, got))
}
