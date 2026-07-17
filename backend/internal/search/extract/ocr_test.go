package extract

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Binary missing (env points at a nonexistent path — authoritative, no
// PATH fallback) → Supports is false for everything, silently.
func TestOCR_Supports_NoBinary(t *testing.T) {
	t.Setenv("FILEX_TESSERACT_BIN", filepath.Join(t.TempDir(), "definitely-missing-tesseract"))

	o := &OCR{}
	if o.Supports("image/png", "png") {
		t.Fatal("Supports must be false when the tesseract binary is missing")
	}
	if o.Supports("image/jpeg", "jpg") {
		t.Fatal("Supports must be false when the tesseract binary is missing")
	}
	if got := TesseractBin(); got != "" {
		t.Fatalf("TesseractBin() = %q, want empty", got)
	}
}

// Binary present (the test binary itself doubles as a fake executable) →
// Supports is true for the image allowlist and false for anything else.
func TestOCR_Supports_WithBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	t.Setenv("FILEX_TESSERACT_BIN", self)

	o := &OCR{}
	for _, tc := range []struct {
		mime, ext string
		want      bool
	}{
		{"image/png", "png", true},
		{"image/jpeg", "jpg", true},
		{"image/jpeg", "jpeg", true},
		{"image/webp", "webp", true},
		{"image/tiff", "tiff", true},
		{"image/tiff", "tif", true},
		{"", ".PNG", true},                      // ext-only, dotted, case-insensitive
		{"image/png; charset=binary", "", true}, // mime with parameters
		{"text/plain", "txt", false},            // not an image
		{"application/pdf", "pdf", false},       // pdf is S1's extractor, not OCR
		{"image/svg+xml", "svg", false},         // vector — tesseract can't read it
		{"", "", false},
	} {
		if got := o.Supports(tc.mime, tc.ext); got != tc.want {
			t.Errorf("Supports(%q, %q) = %v, want %v", tc.mime, tc.ext, got, tc.want)
		}
	}
}

// Extract via a stub "tesseract" that echoes fixed text — verifies the
// tmp-file plumbing, trimming and the limit cut without needing a real
// tesseract install. POSIX shells only (the CI/dev verification runs
// under WSL); skipped on Windows.
func TestOCR_Extract_StubBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub shell script not runnable on windows")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "tesseract")
	script := "#!/bin/sh\necho \"merhaba dünya OCR\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FILEX_TESSERACT_BIN", stub)

	o := &OCR{}
	got, err := o.Extract(context.Background(), strings.NewReader("fake image bytes"), 4096)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got != "merhaba dünya OCR" {
		t.Fatalf("Extract = %q, want %q", got, "merhaba dünya OCR")
	}

	// Limit cut lands on a rune boundary ("dünya"nın ü'sü bölünmez).
	got, err = o.Extract(context.Background(), strings.NewReader("x"), 9)
	if err != nil {
		t.Fatalf("Extract (limited): %v", err)
	}
	if got != "merhaba d" {
		t.Fatalf("Extract limited = %q, want %q", got, "merhaba d")
	}
	if int64(len(got)) > 9 {
		t.Fatalf("limit exceeded: %d bytes", len(got))
	}
}

func TestTruncateUTF8_RuneBoundary(t *testing.T) {
	s := "aü" // 'ü' = 2 bytes → cutting at 2 must not split it
	if got := truncateUTF8(s, 2); got != "a" {
		t.Fatalf("truncateUTF8(%q, 2) = %q, want %q", s, got, "a")
	}
	if got := truncateUTF8(s, 3); got != s {
		t.Fatalf("truncateUTF8(%q, 3) = %q, want %q", s, got, s)
	}
}
