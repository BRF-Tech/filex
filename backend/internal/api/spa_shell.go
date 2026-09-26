package api

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/basepath"
)

// The web app is built ONCE and runs at the root and under any base path
// (FILEX_BASE_PATH, internal/basepath). Its JavaScript and CSS find their
// neighbours relative to their own URL (web/vite.config.ts, renderBuiltUrl),
// so only the two documents that spell out an address have to learn the base,
// and the server teaches them as it serves them:
//
//   - index.html — its <script>/<link> tags name `/admin/assets/…`, and the
//     app reads its base from a `<meta name="filex-base">` tag this adds
//     (packages/core/src/lib/appBase.ts);
//   - manifest.webmanifest — `id`, `start_url` and `scope`.
//
// ⚠⚠ At the root nothing is rewritten: newShellDocs returns nil and both files
// are served byte for byte as built. That matters most for the manifest `id`,
// the installed app's identity — change it and every existing install becomes
// a second, separate app.

// shellDocs holds the two documents rewritten for one base.
type shellDocs struct {
	index    []byte
	manifest []byte
}

// newShellDocs renders the documents for base, or nil at the root. A file the
// build does not have stays nil and is served (or 404d) as before.
func newShellDocs(root *embedSubFS, base string) *shellDocs {
	if base == "" || root == nil {
		return nil
	}
	d := &shellDocs{}
	if doc, err := root.ReadFile("index.html"); err == nil {
		d.index = shellIndex(doc, base)
	}
	if doc, err := root.ReadFile("manifest.webmanifest"); err == nil {
		if m, err := shellManifest(doc, base); err == nil {
			d.manifest = m
		} else {
			// Cannot happen with a manifest vite-plugin-pwa wrote; said out
			// loud rather than guessed at if it ever does.
			slog.Error("spa: manifest.webmanifest is not JSON; installing the app under the base path will not work",
				slog.String("base_path", base), slog.String("err", err.Error()))
		}
	}
	return d
}

// shellIndex returns index.html for base: every attribute that names
// `/admin/…` names `<base>/admin/…`, and the base is published to the app in
// a meta tag. A meta tag and not an inline script, because a Content-Security-
// Policy an operator adds in front of filex may refuse inline scripts, and a
// tag is data either way.
func shellIndex(doc []byte, base string) []byte {
	if base == "" {
		return doc
	}
	s := strings.ReplaceAll(string(doc), `="/admin/`, `="`+base+`/admin/`)
	meta := `<meta name="filex-base" content="` + html.EscapeString(base) + `" />`
	if i := strings.Index(s, "<head>"); i >= 0 {
		i += len("<head>")
		s = s[:i] + "\n    " + meta + s[i:]
	} else {
		s = meta + s
	}
	return []byte(s)
}

// shellManifest returns the web app manifest for base: `id`, `start_url` and
// `scope` move under it. Icons are relative to the manifest's own URL already.
func shellManifest(doc []byte, base string) ([]byte, error) {
	if base == "" {
		return doc, nil
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return nil, err
	}
	for _, k := range []string{"id", "start_url", "scope"} {
		if v, ok := m[k].(string); ok && strings.HasPrefix(v, "/") {
			m[k] = base + v
		}
	}
	return json.Marshal(m)
}

// redirectUnderBase answers a directory without its slash (`/admin`,
// `/drive`) with a 301 to the same directory under the base the request
// arrived on.
func redirectUnderBase(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, basepath.Path(r.Context(), target), http.StatusMovedPermanently)
	})
}
