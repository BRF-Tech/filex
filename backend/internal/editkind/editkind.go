// Package editkind is the server's one answer to "how is this file edited":
// as text (the built-in editor, saved through save-text), in the document
// server (ONLYOFFICE), or not at all here.
//
// ⚠⚠ Why one package (filex #211, audit B2). The question was answered in
// seven places: two lists in the server (the ONLYOFFICE document types, and
// save-text's "is this text" rule) and five in the clients (the explorer's
// two office lists, the preview's editable list, the desktop's "open with"
// list, the text-mime list). They had already drifted: the preview offered
// Edit on `.graphql` and `.mmd`, which save-text then refused with a 415,
// while `.properties`, `.lua` or a `Makefile` - which save-text accepts - got
// no Edit at all, and `.docm`/`.xlsm`/`.pptm` were not office documents to
// the explorer. Now the rule lives here, the server publishes it
// (`/api/files/capabilities` → `edit_kinds`), and the clients read that.
//
// The two rules are consistent by construction: a type the built-in editor
// saves as text is never also an office document (`.txt`, `.csv`, `.html`
// and `.xml` are things ONLYOFFICE can open, but filex opens them as text),
// and a fixed-layout format the document server only displays (`.pdf`,
// `.xps`, `.epub`) is not an office document either - filex has its own
// viewers for those.
package editkind

import (
	"path"
	"sort"
	"strings"
)

// DocumentType returns "word", "cell", "slide", or "" for an extension (with
// or without its dot): ONLYOFFICE's own mapping of what it opens. Unknown
// returns "" so callers can 415.
func DocumentType(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "doc", "docm", "docx", "dot", "dotm", "dotx", "epub", "fodt", "htm", "html", "mht", "mhtml", "odt", "ott", "pdf", "rtf", "stw", "sxw", "txt", "wps", "wpt", "xml", "xps":
		return "word"
	case "csv", "et", "ett", "fods", "ods", "ots", "sxc", "xls", "xlsb", "xlsm", "xlsx", "xlt", "xltm", "xltx":
		return "cell"
	case "dps", "dpt", "fodp", "odp", "otp", "pot", "potm", "potx", "pps", "ppsm", "ppsx", "ppt", "pptm", "pptx", "sxi":
		return "slide"
	}
	return ""
}

// documentServerExts is every extension DocumentType answers for, so Office
// can be listed without a second table.
var documentServerExts = []string{
	"doc", "docm", "docx", "dot", "dotm", "dotx", "epub", "fodt", "htm", "html", "mht", "mhtml", "odt", "ott", "pdf", "rtf", "stw", "sxw", "txt", "wps", "wpt", "xml", "xps",
	"csv", "et", "ett", "fods", "ods", "ots", "sxc", "xls", "xlsb", "xlsm", "xlsx", "xlt", "xltm", "xltx",
	"dps", "dpt", "fodp", "odp", "otp", "pot", "potm", "potx", "pps", "ppsm", "ppsx", "ppt", "pptm", "pptx", "sxi",
}

// viewOnly are fixed-layout formats: the document server can display them but
// not edit them, and filex has its own viewer for each.
var viewOnly = map[string]bool{"pdf": true, "xps": true, "epub": true}

// textExts round-trip cleanly as UTF-8 plain text: JSON, YAML, code,
// markdown, config files. A draw.io diagram is XML and the draw.io viewer
// saves it as text; a Mermaid diagram and a GraphQL schema are text their
// viewers save the same way.
var textExts = map[string]bool{}

// textNames are files with no telling extension that are text by name.
var textNames = map[string]bool{
	"dockerfile": true, "makefile": true, ".env": true, ".gitignore": true, ".editorconfig": true,
}

// structuredText are the media types filed under application/ that are text
// a person edits as text (every text/* is too).
var structuredText = []string{
	"application/json", "application/xml", "application/yaml", "application/x-yaml",
	"application/javascript", "application/x-sh", "application/toml",
}

func init() {
	for _, e := range []string{
		"txt", "md", "markdown", "log", "csv", "tsv",
		"conf", "ini", "env", "toml", "cfg", "properties",
		"json", "jsonc", "yaml", "yml", "xml", "svg", "html", "htm",
		"css", "scss", "sass", "less",
		"js", "mjs", "cjs", "ts", "tsx", "jsx", "vue", "svelte",
		"php", "py", "rb", "rs", "go", "java", "kt", "swift",
		"cpp", "c", "h", "hpp", "cs", "dart",
		"sh", "bash", "zsh", "sql", "lua", "pl", "r",
		"dockerfile", "gradle", "gitignore", "editorconfig",
		"graphql", "gql",
		"drawio", "dio",
		"mmd", "mermaid",
	} {
		textExts[e] = true
	}
}

// extOf is the lower-case extension of a name, without its dot.
func extOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

// TextEditable reports whether a file is saved as text by its NAME: its
// extension, or a name that is text on its own (`Makefile`, `.gitignore`).
func TextEditable(name string) bool {
	if textExts[extOf(name)] {
		return true
	}
	return textNames[strings.ToLower(path.Base(name))]
}

// TextualMime reports whether a media type is text a person edits as text:
// any text/*, and the structured-text types filed under application/. It
// answers for a name that says nothing (`LICENSE`, `example.custom`, #56).
func TextualMime(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	if strings.HasPrefix(m, "text/") {
		return true
	}
	for _, s := range structuredText {
		if m == s {
			return true
		}
	}
	return false
}

// OfficeExt reports whether files of this extension (with or without its dot)
// open in the document server as office documents.
func OfficeExt(ext string) bool {
	e := strings.ToLower(strings.TrimPrefix(ext, "."))
	if e == "" || DocumentType(e) == "" || viewOnly[e] || textExts[e] {
		return false
	}
	return true
}

// Office reports whether a file opens in the document server, by its name.
func Office(name string) bool { return OfficeExt(extOf(name)) }

// Kinds is the rule as `/api/files/capabilities` publishes it (`edit_kinds`):
// lists the clients look a file up in, instead of lists of their own.
type Kinds struct {
	// Office: the extensions the document server opens and edits.
	Office []string `json:"office"`
	// Text: the extensions the built-in editor opens and save-text saves.
	Text []string `json:"text"`
	// TextNames: whole file names (lower case) that are text by name.
	TextNames []string `json:"text_names"`
	// TextMimePrefixes and TextMimes: the media types that make a file whose
	// name says nothing text (`text/` and the structured ones).
	TextMimePrefixes []string `json:"text_mime_prefixes"`
	TextMimes        []string `json:"text_mimes"`
}

// Published is the rule, sorted, as clients read it.
func Published() Kinds {
	k := Kinds{TextMimePrefixes: []string{"text/"}}
	for _, e := range documentServerExts {
		if OfficeExt(e) {
			k.Office = append(k.Office, e)
		}
	}
	for e := range textExts {
		k.Text = append(k.Text, e)
	}
	for n := range textNames {
		k.TextNames = append(k.TextNames, n)
	}
	k.TextMimes = append(k.TextMimes, structuredText...)
	sort.Strings(k.Office)
	sort.Strings(k.Text)
	sort.Strings(k.TextNames)
	sort.Strings(k.TextMimes)
	return k
}
