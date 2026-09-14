package newdoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- helpers

func openZip(t *testing.T, ext string) *zip.Reader {
	t.Helper()
	b, err := Bytes(ext)
	if err != nil {
		t.Fatalf("Bytes(%q): %v", ext, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("%s is not a readable zip: %v", ext, err)
	}
	return zr
}

func zipNames(zr *zip.Reader) map[string]*zip.File {
	m := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		m[f.Name] = f
	}
	return m
}

func readEntry(t *testing.T, f *zip.File) []byte {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open %s: %v", f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", f.Name, err)
	}
	return b
}

var officeExts = []string{"docx", "xlsx", "pptx", "odt", "ods", "odp"}

// ---------------------------------------------------------------- basics

func TestEveryRegisteredTypeMaterialises(t *testing.T) {
	types := Types()
	if len(types) == 0 {
		t.Fatal("registry is empty")
	}
	seen := map[string]bool{}
	for _, ty := range types {
		if seen[ty.Ext] {
			t.Errorf("duplicate extension %q in the registry", ty.Ext)
		}
		seen[ty.Ext] = true
		if ty.MIME == "" {
			t.Errorf("%s: no MIME type — the node row would be written with an empty content type", ty.Ext)
		}
		if _, err := Bytes(ty.Ext); err != nil {
			t.Errorf("Bytes(%q): %v", ty.Ext, err)
		}
	}
}

func TestUnknownTypeIsRejected(t *testing.T) {
	if _, err := Bytes("exe"); err == nil {
		t.Fatal("Bytes(\"exe\") must fail: a type the server cannot create must never be materialised")
	}
	if _, ok := Lookup("exe"); ok {
		t.Fatal("Lookup(\"exe\") must be false")
	}
}

func TestLookupNormalisesTheExtension(t *testing.T) {
	for _, in := range []string{"docx", ".docx", "DOCX", " .DocX "} {
		ty, ok := Lookup(in)
		if !ok || ty.Ext != "docx" {
			t.Errorf("Lookup(%q) = %v, %v; want docx, true", in, ty.Ext, ok)
		}
	}
}

func TestTextTypesAreGenuinelyEmpty(t *testing.T) {
	for _, ty := range Types() {
		if ty.Group != GroupText {
			continue
		}
		b, err := Bytes(ty.Ext)
		if err != nil {
			t.Fatalf("Bytes(%q): %v", ty.Ext, err)
		}
		if len(b) != 0 {
			t.Errorf("%s: got %d bytes, want 0 — an empty text file is the correct content and a BOM or a newline is content the person did not type", ty.Ext, len(b))
		}
		if b == nil {
			t.Errorf("%s: nil slice; callers stream it and expect a usable empty reader", ty.Ext)
		}
	}
}

func TestOfficeTypesAreNotEmpty(t *testing.T) {
	// The whole point of the package. A zero-byte .docx is the bug this
	// exists to prevent, so it gets its own assertion rather than being an
	// implied consequence of the zip tests below.
	for _, ext := range officeExts {
		b, err := Bytes(ext)
		if err != nil {
			t.Fatalf("Bytes(%q): %v", ext, err)
		}
		if len(b) < 512 {
			t.Errorf("%s: %d bytes — too small to be a real office container", ext, len(b))
		}
		if !bytes.HasPrefix(b, []byte("PK\x03\x04")) {
			t.Errorf("%s: does not start with the ZIP local-file signature", ext)
		}
	}
}

func TestBytesAreDeterministic(t *testing.T) {
	// Two calls must be byte-identical. Not cosmetic: archive/zip writes a
	// timestamp into every entry, and a template that changed on every call
	// would make "did this file come from the template?" unanswerable.
	for _, ext := range officeExts {
		a, _ := Bytes(ext)
		// Defeat the cache so this measures the builder, not the map.
		cacheMu.Lock()
		delete(cache, ext)
		cacheMu.Unlock()
		b, _ := Bytes(ext)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two builds differ (%d vs %d bytes)", ext, len(a), len(b))
		}
	}
}

// ---------------------------------------------------------------- OPC shape

// TestOOXMLRelationshipsResolve is the assertion that actually catches a
// broken package. Word/OnlyOffice do not reject a file because its XML is
// malformed — the XML is usually fine. They reject it because a relationship
// points at a part that is not in the archive, or because a part carries no
// declared content type. Both are silent to "does it unzip?" and both are
// exactly what a hand-written template gets wrong.
func TestOOXMLRelationshipsResolve(t *testing.T) {
	for _, ext := range []string{"docx", "xlsx", "pptx"} {
		t.Run(ext, func(t *testing.T) {
			zr := openZip(t, ext)
			names := zipNames(zr)

			for name, f := range names {
				if !strings.HasSuffix(name, ".rels") {
					continue
				}
				var rels struct {
					Rel []struct {
						ID     string `xml:"Id,attr"`
						Type   string `xml:"Type,attr"`
						Target string `xml:"Target,attr"`
						Mode   string `xml:"TargetMode,attr"`
					} `xml:"Relationship"`
				}
				if err := xml.Unmarshal(readEntry(t, f), &rels); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(rels.Rel) == 0 {
					t.Errorf("%s: no relationships at all", name)
				}
				// "a/_rels/b.xml.rels" describes the part "a/b.xml", so
				// relative targets resolve against "a".
				base := path.Dir(path.Dir(name))
				for _, r := range rels.Rel {
					if r.Mode == "External" {
						continue
					}
					target := r.Target
					if strings.HasPrefix(target, "/") {
						target = strings.TrimPrefix(target, "/")
					} else {
						target = path.Join(base, target)
					}
					if _, ok := names[target]; !ok {
						t.Errorf("%s: relationship %s points at %q, which is not in the archive", name, r.ID, target)
					}
				}
			}
		})
	}
}

// TestOOXMLContentTypesCoverEveryPart — the other half of the same failure
// mode: a part that exists but has no content type declared is a part the
// consumer does not know how to read.
func TestOOXMLContentTypesCoverEveryPart(t *testing.T) {
	for _, ext := range []string{"docx", "xlsx", "pptx"} {
		t.Run(ext, func(t *testing.T) {
			zr := openZip(t, ext)
			names := zipNames(zr)

			ct, ok := names["[Content_Types].xml"]
			if !ok {
				t.Fatal("no [Content_Types].xml — the package has no manifest at all")
			}
			var types struct {
				Default []struct {
					Ext         string `xml:"Extension,attr"`
					ContentType string `xml:"ContentType,attr"`
				} `xml:"Default"`
				Override []struct {
					PartName    string `xml:"PartName,attr"`
					ContentType string `xml:"ContentType,attr"`
				} `xml:"Override"`
			}
			if err := xml.Unmarshal(readEntry(t, ct), &types); err != nil {
				t.Fatalf("[Content_Types].xml: %v", err)
			}

			defaults := map[string]bool{}
			for _, d := range types.Default {
				defaults[strings.ToLower(d.Ext)] = true
			}
			overrides := map[string]bool{}
			for _, o := range types.Override {
				overrides[strings.TrimPrefix(o.PartName, "/")] = true
				if _, ok := names[strings.TrimPrefix(o.PartName, "/")]; !ok {
					t.Errorf("[Content_Types].xml declares %q, which is not in the archive", o.PartName)
				}
			}
			for name := range names {
				if name == "[Content_Types].xml" {
					continue
				}
				if overrides[name] {
					continue
				}
				e := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
				if !defaults[e] {
					t.Errorf("%s has no declared content type (no Override, and no Default for .%s)", name, e)
				}
			}
		})
	}
}

func TestEveryXMLPartIsWellFormed(t *testing.T) {
	for _, ext := range officeExts {
		t.Run(ext, func(t *testing.T) {
			zr := openZip(t, ext)
			for _, f := range zr.File {
				if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
					continue
				}
				dec := xml.NewDecoder(bytes.NewReader(readEntry(t, f)))
				for {
					_, err := dec.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Errorf("%s: malformed XML: %v", f.Name, err)
						break
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------- ODF magic

// TestODFMimetypeIsFirstStoredAndAtOffset38 is the ODF equivalent of the OPC
// tests: the archive can be perfectly valid and every part present, and the
// file still not be an OpenDocument file, because the identity of an ODF
// document is the literal position of its media type in the byte stream.
//
// ⚠ A lexical walk of the template directory produces META-INF first and this
// test is the only thing standing between that and a shipped "valid ZIP that
// is not a document".
func TestODFMimetypeIsFirstStoredAndAtOffset38(t *testing.T) {
	want := map[string]string{"odt": mimeOdt, "ods": mimeOds, "odp": mimeOdp}
	for ext, media := range want {
		t.Run(ext, func(t *testing.T) {
			raw, err := Bytes(ext)
			if err != nil {
				t.Fatal(err)
			}
			zr := openZip(t, ext)
			if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
				t.Fatalf("first entry is %q, want \"mimetype\"", zr.File[0].Name)
			}
			if zr.File[0].Method != zip.Store {
				t.Error("mimetype is compressed; the spec requires it stored")
			}
			if got := string(readEntry(t, zr.File[0])); got != media {
				t.Errorf("mimetype content = %q, want %q", got, media)
			}

			// The byte-level claim. 30 = fixed local file header, then the
			// 8-byte name, then the content — which only holds if the extra
			// field is empty.
			if got := string(raw[30:38]); got != "mimetype" {
				t.Fatalf("bytes 30..38 = %q, want \"mimetype\" (local header is not the expected shape)", got)
			}
			if got := string(raw[38 : 38+len(media)]); got != media {
				t.Errorf("bytes at offset 38 = %q, want %q — a sniffer will not recognise this file", got, media)
			}
			if n := zr.File[0].FileHeader.Extra; len(n) != 0 {
				t.Errorf("mimetype entry carries a %d-byte extra field; it must carry none", len(n))
			}
		})
	}
}

func TestODFManifestListsEveryEntry(t *testing.T) {
	for _, ext := range []string{"odt", "ods", "odp"} {
		t.Run(ext, func(t *testing.T) {
			zr := openZip(t, ext)
			names := zipNames(zr)
			mf, ok := names["META-INF/manifest.xml"]
			if !ok {
				t.Fatal("no META-INF/manifest.xml")
			}
			var m struct {
				Entry []struct {
					Path  string `xml:"full-path,attr"`
					Media string `xml:"media-type,attr"`
				} `xml:"file-entry"`
			}
			if err := xml.Unmarshal(readEntry(t, mf), &m); err != nil {
				t.Fatal(err)
			}
			listed := map[string]bool{}
			for _, e := range m.Entry {
				listed[e.Path] = true
				if e.Path == "/" {
					continue
				}
				if _, ok := names[e.Path]; !ok {
					t.Errorf("manifest lists %q, which is not in the archive", e.Path)
				}
			}
			for name := range names {
				if name == "mimetype" || name == "META-INF/manifest.xml" {
					continue
				}
				if !listed[name] {
					t.Errorf("%s is in the archive but not in the manifest", name)
				}
			}
		})
	}
}

// ---------------------------------------------------------------- the tree

// TestNoOrphanTemplateFiles — every file compiled into the binary is
// referenced by the manifest. An orphan is a part somebody wrote and forgot to
// wire, and the document it belongs to is silently incomplete.
func TestNoOrphanTemplateFiles(t *testing.T) {
	used := map[string]bool{}
	for _, tp := range registry {
		if tp.single != "" {
			used[tp.single] = true
		}
		for _, p := range tp.parts {
			if p.src != "" {
				used[p.src] = true
			}
		}
	}
	for _, f := range embeddedFiles() {
		if !used[f] {
			t.Errorf("templates/%s is embedded but never used by any type", f)
		}
	}
	if len(embeddedFiles()) == 0 {
		t.Fatal("nothing was embedded — check the //go:embed pattern (it must use the all: prefix, or _rels/ is skipped)")
	}
}

// TestRelsDirectoriesSurvivedTheEmbed guards the specific `all:` trap named in
// the package doc: without it the build succeeds and produces a ZIP with no
// relationship parts in it.
func TestRelsDirectoriesSurvivedTheEmbed(t *testing.T) {
	var got int
	for _, f := range embeddedFiles() {
		if strings.Contains(f, "_rels/") {
			got++
		}
	}
	if got == 0 {
		t.Fatal("no _rels/ parts were embedded — the //go:embed pattern lost them")
	}
}

// ---------------------------------------------------------------- dump hook

// TestDumpArtifacts is how the bytes get proved against a real program. It
// writes one file per type into the directory named by FILEX_NEWDOC_DUMP, so
// the templates can be handed to LibreOffice:
//
//	FILEX_NEWDOC_DUMP=/tmp/nd go test ./internal/newdoc -run TestDumpArtifacts
//	soffice --headless --convert-to pdf /tmp/nd/*.docx
//
// A file LibreOffice converts is a file an editor can open. Skipped unless the
// variable is set, so it costs nothing in CI.
func TestDumpArtifacts(t *testing.T) {
	dir := os.Getenv("FILEX_NEWDOC_DUMP")
	if dir == "" {
		t.Skip("set FILEX_NEWDOC_DUMP to write the templates out")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ty := range Types() {
		b, err := Bytes(ty.Ext)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "blank."+ty.Ext)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-8s %6d bytes -> %s", ty.Ext, len(b), p)
	}
}
