package wasmplugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An app's interface draws a document in the frame filex makes for it, and
// that frame's policy decides what the document can fetch (0.57: a preview
// loads no picture from another server by itself). The Office editor app asks
// for no `ui-net:` address, so a document it shows - a .docx with a linked
// picture, a sheet with an image from a URL - cannot reach another server:
// pictures, styles, fonts and media come only from the app's own package,
// data: and blob:, and nothing is connected to but the package and blob:.
//
// The permissions are the Office editor's own (filex-office-editor 0.4.0,
// filex-app.json: permission_reasons ui*), written here: a Go test reads no
// file outside the backend module.
func TestUIPolicy_OfficeEditorReachesNoOtherServer(t *testing.T) {
	pkg := "https://files.example.com/_appui/office-editor/0123456789abcdef/"
	grants := NewGrants([]Permission{
		PermUI, PermUIPackageFetch, PermUIFramePackage, PermUIConnectBlob,
		PermUIEval, PermUIWasmEval, PermUIDownload, PermUIPrint,
		"ui-viewer:.docx", "ui-viewer:.xlsx", "ui-new:.docx",
	})
	csp, allow := UIPolicy(grants, pkg)
	directives := map[string][]string{}
	for _, d := range strings.Split(csp, ";") {
		f := strings.Fields(strings.TrimSpace(d))
		if len(f) > 0 {
			directives[f[0]] = f[1:]
		}
	}
	for _, name := range []string{"img-src", "style-src", "font-src", "media-src", "connect-src", "frame-src", "child-src", "default-src", "object-src"} {
		srcs, ok := directives[name]
		require.True(t, ok, "%s is set", name)
		for _, s := range srcs {
			switch s {
			case pkg, "data:", "blob:", "'unsafe-inline'", "'none'":
			default:
				t.Errorf("%s allows %q: a document in the Office editor could fetch from it", name, s)
			}
		}
	}
	require.Equal(t, []string{"'none'"}, directives["default-src"])
	require.Contains(t, csp, "sandbox allow-scripts")
	require.Equal(t, `("`+pkg+`*")`, allow, "Chrome's Connection-Allowlist names the package alone")

	// The check sees a live address when an app is given one.
	withCDN := NewGrants([]Permission{PermUI, UINetPermission("img", "https://cdn.jsdelivr.net/npm/pictures/")})
	cdnCSP, _ := UIPolicy(withCDN, pkg)
	require.Contains(t, cdnCSP, "img-src "+pkg+" data: blob: https://cdn.jsdelivr.net/npm/pictures/")
}
