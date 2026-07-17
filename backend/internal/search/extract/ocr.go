// ocr.go — optional OCR extractor backed by the external `tesseract`
// binary (frozen v0.2 "Bul" contract #4).
//
// Resolution: FILEX_TESSERACT_BIN when set (used exclusively — a bad
// value disables OCR instead of silently falling back), otherwise a
// $PATH lookup for "tesseract". No binary → Supports returns false, so
// the pipeline silently skips images; availability is surfaced to the
// UI via the capabilities `ocr` flag (internal/capability).
//
// Only raster images are attempted: png / jpg / jpeg / webp / tiff.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// ocrTimeout caps a single tesseract run (both the -l attempt and the
// plain fallback share one window).
const ocrTimeout = 30 * time.Second

// ocrExts / ocrMimes gate Supports to raster images only.
var ocrExts = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "webp": true, "tiff": true, "tif": true,
}

var ocrMimes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/tiff": true,
}

func init() { Register(&OCR{}) }

// OCR extracts text from images via the external tesseract binary.
type OCR struct{}

// TesseractBin resolves the tesseract binary path. FILEX_TESSERACT_BIN,
// when set, is authoritative (invalid value → ""); otherwise $PATH is
// searched. Empty string means OCR is unavailable. Shared with
// internal/capability so the probe and the extractor can never disagree.
func TesseractBin() string {
	if bin := os.Getenv("FILEX_TESSERACT_BIN"); bin != "" {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("tesseract"); err == nil {
		return p
	}
	return ""
}

// Supports reports whether this input is an OCR-able image AND the
// tesseract binary is present. Missing binary → false (silent skip).
func (o *OCR) Supports(mime, ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	mime = strings.ToLower(mime)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if !ocrExts[ext] && !ocrMimes[mime] {
		return false
	}
	return TesseractBin() != ""
}

// Extract runs tesseract over the image bytes and returns the recognized
// plain text, truncated to limit. Input is staged in a temp file
// (tesseract's stdin handling is unreliable across builds) and removed
// when done. Tries `-l tur+eng` first; any failure (typically a missing
// language pack) falls back to a plain invocation.
func (o *OCR) Extract(ctx context.Context, r io.Reader, limit int64) (string, error) {
	bin := TesseractBin()
	if bin == "" {
		return "", errors.New("extract: tesseract not available")
	}

	tmp, err := os.CreateTemp("", "filex-ocr-*.img")
	if err != nil {
		return "", fmt.Errorf("extract: ocr temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", fmt.Errorf("extract: ocr stage input: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("extract: ocr stage input: %w", closeErr)
	}

	ctx, cancel := context.WithTimeout(ctx, ocrTimeout)
	defer cancel()

	out, err := runTesseract(ctx, bin, tmpName, true)
	if err != nil {
		// Most likely the tur/eng traineddata is missing — retry with
		// tesseract's default language before giving up.
		out, err = runTesseract(ctx, bin, tmpName, false)
		if err != nil {
			return "", err
		}
	}

	text := strings.TrimSpace(out)
	if limit > 0 && int64(len(text)) > limit {
		text = truncateUTF8(text, limit)
	}
	return text, nil
}

// runTesseract executes `tesseract <file> stdout [-l tur+eng]` and
// returns stdout.
func runTesseract(ctx context.Context, bin, file string, withLang bool) (string, error) {
	args := []string{file, "stdout"}
	if withLang {
		args = append(args, "-l", "tur+eng")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("extract: tesseract: %w: %s", err, msg)
	}
	return stdout.String(), nil
}

// truncateUTF8 cuts s to at most limit bytes without splitting a rune.
func truncateUTF8(s string, limit int64) string {
	if int64(len(s)) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
