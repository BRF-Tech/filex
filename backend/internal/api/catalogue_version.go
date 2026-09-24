package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/version"
)

// catalogueContextPath is the translator's context file the web build writes
// beside the SPA (scripts/lib/i18n-catalogue.mjs).
const catalogueContextPath = "i18n/filex-catalogue-context.json"

// catalogueContext serves the catalogue's context file with its `filex`
// field set to THIS binary's version.
//
// ⚠ The web build writes the version it knows (FILEX_VERSION, the CI tag, or
// web/package.json — which between releases still names the LAST one), and a
// translator taking the catalogue from a running server must read the version
// that server actually is: their pack's coverage is measured against it. A
// development binary ("0.1.0-dev", a snapshot) has no version worth stating,
// so the file goes out as built.
func catalogueContext(read func(name string) ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := read(catalogueContextPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if v := releaseVersion(); v != "" {
			var doc map[string]json.RawMessage
			if json.Unmarshal(data, &doc) == nil {
				doc["filex"], _ = json.Marshal(v)
				if b, err := json.MarshalIndent(doc, "", "  "); err == nil {
					data = append(b, '\n')
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

// releaseVersion is version.Version without its "v", or "" for a build that
// is not a release.
func releaseVersion() string {
	v := strings.TrimPrefix(strings.TrimSpace(version.Version), "v")
	if v == "" || strings.Contains(v, "dev") || strings.Contains(v, "snapshot") {
		return ""
	}
	return v
}
