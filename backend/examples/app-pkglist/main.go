// Command app-pkglist is a complete filex app that draws THUMBNAILS: the
// contents of a package - a Java archive, an Android package, a Python wheel,
// a VS Code extension, a NuGet package, a browser add-on - as a card that
// lists what is inside, the way filex draws its own .zip and .tar files.
//
// filex lists the archives it knows (zip, tar, tgz, tbz2). A .jar or an .apk
// is a zip under another name, so filex gives it the placeholder card - and a
// .7z or a .rar it cannot read at all. That is the gap an app fills: it says
// which kinds it draws (`thumbnails` in filex-app.json), and filex hands it
// the bytes of each such file, one at a time, and draws its answer. An app for
// 7z or rar is this program with a 7z or rar reader in place of archive/zip.
//
// It exists to be read, and to be built:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o pkglist.wasm ./examples/app-pkglist
//	# Admin → Plugins → Apps → Install an app → Upload files:
//	#   pkglist.wasm and examples/app-pkglist/filex-app.json
//
// The install review lists one permission per kind ("Draws the thumbnails of
// .jar files: it is handed the bytes of every such file filex draws…") and a
// File types group where you say whether it goes first or after filex's own
// drawer. It asks for nothing else: no network, no other file.
//
// docs/PLUGIN-KIT.md → Drawing thumbnails walks through it.
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/thumbkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// kinds are what the app draws, and what each is called on its card.
var kinds = map[string]string{
	"jar":   "Java archive",
	"apk":   "Android package",
	"whl":   "Python wheel",
	"vsix":  "VS Code extension",
	"nupkg": "NuGet package",
	"xpi":   "Browser add-on",
}

// manifest must equal filex-app.json next to this file (main_test.go checks).
var manifest = wire.Manifest{
	ManifestVersion: 1,
	Name:            "pkglist",
	Version:         "0.1.0",
	Label:           wire.Text{"en": "Package contents", "tr": "Paket içeriği"},
	Description: wire.Text{
		"en": "Draws the thumbnail of a .jar, .apk, .whl, .vsix, .nupkg or .xpi as the list of what is inside it.",
		"tr": ".jar, .apk, .whl, .vsix, .nupkg ve .xpi dosyalarının küçük resmini içindekilerin listesi olarak çizer.",
	},
	Filex:       ">=0.50.0",
	Languages:   []string{"en", "tr"},
	Permissions: []string{},
	Thumbnails: &wire.ThumbnailSpec{Applies: wire.Applies{
		Kind: "file",
		Ext:  []string{"apk", "jar", "nupkg", "vsix", "whl", "xpi"},
	}},
}

// ⚠ Registration happens in init(): a wasip1 module built with
// -buildmode=c-shared is a reactor, and main() never runs.
func main() {}

func init() {
	pluginkit.Run(&pluginkit.Plugin{Manifest: manifest, Thumbnail: thumbnail})
}

// maxRead is the most of a package this app reads. filex never sends more
// than the administrator's limit for the app (32 MB by default); a central
// directory is at the end of a zip, so the whole file is needed.
const maxRead = 64 << 20

// thumbnail is the export: read the one file filex handed over, list it.
func thumbnail(in *wire.ThumbnailInput) (*wire.ThumbnailOutput, error) {
	src, err := pluginkit.ThumbnailSource(in)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return draw(in.Ext, src)
}

// draw is the whole app, without the host: what a test calls.
func draw(ext string, r io.Reader) (*wire.ThumbnailOutput, error) {
	name, ok := kinds[strings.ToLower(ext)]
	if !ok {
		return nil, fmt.Errorf("pkglist draws %s files, not .%s", strings.Join(sortedKinds(), ", "), ext)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxRead+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRead {
		return nil, errors.New("the package is larger than this app reads")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Not a zip after all: say so, and filex asks the next handler in the
		// administrator's list, or keeps the file's type icon.
		return nil, fmt.Errorf("not a %s: %w", name, err)
	}
	lines, files := listing(zr.File)
	header := fmt.Sprintf("%s - %d files", strings.ToUpper(ext), files)
	return thumbkit.PNG(thumbkit.ListCard(header, lines, 0))
}

// listing is the top level of the package: its folders (with how many files
// each holds) first, then its files, each sorted by name.
func listing(entries []*zip.File) (lines []string, files int) {
	folders := map[string]int{}
	var top []string
	for _, f := range entries {
		n := strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "/")
		if n == "" || strings.HasSuffix(n, "/") {
			continue
		}
		files++
		if i := strings.IndexByte(n, '/'); i >= 0 {
			folders[n[:i]]++
			continue
		}
		top = append(top, n)
	}
	names := make([]string, 0, len(folders))
	for d := range folders {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		lines = append(lines, fmt.Sprintf("%s/  (%d)", d, folders[d]))
	}
	sort.Strings(top)
	return append(lines, top...), files
}

func sortedKinds() []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, "."+k)
	}
	sort.Strings(out)
	return out
}
