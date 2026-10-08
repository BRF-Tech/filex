package editkind

import (
	"testing"
)

// The two rules never claim one file: a type the built-in editor saves as
// text is not an office document, and every office document is one the
// document server knows (audit B2: `.txt`, `.csv`, `.html`, `.xml` are both
// ONLYOFFICE types and text, and filex opens them as text).
func TestKinds_TextAndOfficeNeverOverlap(t *testing.T) {
	k := Published()
	text := map[string]bool{}
	for _, e := range k.Text {
		text[e] = true
	}
	for _, e := range k.Office {
		if text[e] {
			t.Errorf("%q is published as both text and office", e)
		}
		if DocumentType(e) == "" {
			t.Errorf("%q is office but the document server does not know it", e)
		}
	}
}

// What the clients used to disagree about, answered once (audit B2).
func TestKinds_TheCasesTheClientsGotWrong(t *testing.T) {
	for _, name := range []string{"schema.graphql", "q.gql", "flow.mmd", "flow.mermaid", "app.properties", "x.tsv", "init.lua", "a.pl", "a.r", "a.zsh", "build.gradle", "Makefile", ".editorconfig", ".gitignore", "x.drawio"} {
		if !TextEditable(name) {
			t.Errorf("%q should be text a person edits", name)
		}
		if Office(name) {
			t.Errorf("%q should not be an office document", name)
		}
	}
	for _, name := range []string{"a.docm", "a.xlsm", "a.pptm", "a.ppsx", "a.xlsb", "a.rtf", "A.DOCX", "a.odp"} {
		if !Office(name) {
			t.Errorf("%q should be an office document", name)
		}
		if TextEditable(name) {
			t.Errorf("%q should not be text", name)
		}
	}
	for _, name := range []string{"a.pdf", "a.epub", "a.xps", "a.png", "a.zip", "noext"} {
		if Office(name) || TextEditable(name) {
			t.Errorf("%q is neither", name)
		}
	}
	for _, name := range []string{"a.txt", "a.csv", "a.html", "a.xml"} {
		if Office(name) {
			t.Errorf("%q opens as text, not in the document server", name)
		}
		if !TextEditable(name) {
			t.Errorf("%q is text", name)
		}
	}
}

func TestTextualMime(t *testing.T) {
	for _, m := range []string{"text/plain", "text/x-shellscript; charset=utf8", "application/json", "Application/YAML", "application/toml"} {
		if !TextualMime(m) {
			t.Errorf("TextualMime(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"application/zip", "image/png", ""} {
		if TextualMime(m) {
			t.Errorf("TextualMime(%q) = true, want false", m)
		}
	}
}

// Published is sorted and complete, so a client's lookup and a diff of two
// answers read the same.
func TestPublished_SortedAndFilled(t *testing.T) {
	k := Published()
	for name, list := range map[string][]string{"office": k.Office, "text": k.Text, "text_names": k.TextNames, "text_mimes": k.TextMimes} {
		if len(list) == 0 {
			t.Fatalf("%s is empty", name)
		}
		for i := 1; i < len(list); i++ {
			if list[i-1] >= list[i] {
				t.Fatalf("%s is not sorted at %q, %q", name, list[i-1], list[i])
			}
		}
	}
	if len(k.TextMimePrefixes) != 1 || k.TextMimePrefixes[0] != "text/" {
		t.Fatalf("text_mime_prefixes = %v", k.TextMimePrefixes)
	}
}

// The list Published walks is DocumentType's own table: an extension the
// document server knows is never left out of the office answer by a list
// that fell behind.
func TestDocumentServerExts_IsTheDocumentTypeTable(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range documentServerExts {
		if DocumentType(e) == "" {
			t.Errorf("%q is listed but DocumentType does not know it", e)
		}
		if seen[e] {
			t.Errorf("%q is listed twice", e)
		}
		seen[e] = true
	}
	if len(seen) != 52 {
		t.Errorf("documentServerExts has %d extensions; DocumentType knows 52", len(seen))
	}
}
