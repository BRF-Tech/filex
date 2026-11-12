package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sharexTemplate is the ShareX template the docs hand out
// (docs/uploaders/filex.sxcu, for a ShareX set up by hand on any filex
// server) as this server makes it: the file the web app saves for an
// uploader, byte for byte, with placeholders where the server writes its
// address and the key. This test writes and checks it from
// buildUploaderSetup, the one builder of what the server hands out, and
// web/tests/docs/sharexTemplate.test.ts holds the docs copy to it, so the
// template cannot drift from the server.
//
// ⚠ It lives in the module's testdata, and this test reads nothing outside
// backend/: the Go gates run on a copy of the module alone (on a Windows
// workstation, a mirror on WSL's own disk: scripts/lib/go-build.mjs,
// wslMirrorCd). #334's first version read docs/ from here: it skipped in
// every such copy, so the bond went unchecked exactly where the release's
// gates run, and web/tests/deploy/goWslMirror.test.ts ("no Go test reads a
// file outside the backend module") was red on main.
//
// After a change to what the server makes, write it again and copy it over
// docs/uploaders/filex.sxcu:
//
//	FILEX_UPDATE_WIRE_FIXTURES=1 go test ./internal/api/handlers/ -run TestUploaderSetup_TheShareXTemplateIsTheServersFile
const sharexTemplate = "testdata/uploaders/filex.sxcu"

func TestUploaderSetup_TheShareXTemplateIsTheServersFile(t *testing.T) {
	setup := buildUploaderSetup(uploadsEndpoint("https://YOUR-FILEX-HOST"), "filex", "fxu_YOUR_KEY")
	// packages/core UploadersPanel.vue saves it as JSON.stringify(sharex, null, 2) + "\n".
	body, err := json.MarshalIndent(setup.ShareX, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(body) + "\n"
	for _, s := range []string{"YOUR-FILEX-HOST", "fxu_YOUR_KEY"} {
		if !strings.Contains(got, s) {
			t.Fatalf("the template carries no %q placeholder", s)
		}
	}
	path := filepath.FromSlash(sharexTemplate)
	if os.Getenv("FILEX_UPDATE_WIRE_FIXTURES") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run with FILEX_UPDATE_WIRE_FIXTURES=1", err)
	}
	// ⚠ Line endings normalised: a checkout with autocrlf hands back CRLF.
	if string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) != got {
		t.Fatalf("%s is not the file the server makes. Write it again (FILEX_UPDATE_WIRE_FIXTURES=1) and copy it over docs/uploaders/filex.sxcu; it reads:\n%s", sharexTemplate, got)
	}
}
