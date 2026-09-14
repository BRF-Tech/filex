package thumb

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// generatePDF renders page 1 of a PDF into the cache as a thumbnail.
func (p *Pipeline) generatePDF(ctx context.Context, node *model.Node, drv storage.Driver) error {
	tmp, err := os.CreateTemp("", "filex-pdf-*.pdf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		rc.Close()
		tmp.Close()
		return err
	}
	rc.Close()
	tmp.Close()

	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID))
	return renderPDFPage1(ctx, tmp.Name(), out)
}

// renderPDFPage1 rasterises the first page of `pdfPath` into `out` and scales
// it to thumbnail size. Ghostscript first, poppler's pdftoppm as the fallback
// — the two renderers this project's images ship (docker/Dockerfile).
//
// ⚠ ONE copy of this, called by both the PDF generator and the office one.
// office.go used to carry its own transcription of the same gs/pdftoppm
// block, which is how the office path kept a bug after the PDF path was
// fixed: a second copy of a rule drifts from the first the week it is
// written (filex lesson #67).
//
// ⚠ The scaling at the end is not cosmetic. `gs -r96` renders A4 at
// 816×1056, and that is what the cache used to hand out: measured on
// 2026-09-12 a one-page fixture produced a 358 KB JPEG to be drawn inside a
// 184×108 card, for every PDF and every office document in a folder. The
// card cannot show more detail than its own width carries, so the bytes
// bought nothing at all.
func renderPDFPage1(ctx context.Context, pdfPath, out string) error {
	_ = os.Remove(out)

	// ⚠ Why ghostscript's complaint is carried rather than dropped: the
	// previous version assigned its output to `_`, so a PDF that gs refused
	// (encrypted, damaged, a version it will not open) ended up failing with
	// a message about pdftoppm and no trace anywhere of the first — and real
	// — reason. The two renderers fail for different reasons and the caller
	// needs both to know what is wrong with the file.
	gsWhy := "not installed"
	if gs, _ := exec.LookPath("gs"); gs != "" {
		cmd := exec.CommandContext(ctx, gs,
			"-sDEVICE=jpeg",
			"-dFirstPage=1", "-dLastPage=1",
			"-r96",
			"-dJPEGQ=80",
			"-dNOPAUSE", "-dBATCH", "-dSAFER",
			"-o", out,
			pdfPath,
		)
		gsOut, gsErr := cmd.CombinedOutput()
		switch {
		case gsErr == nil && wroteFrame(out):
			return fitToThumb(out)
		case gsErr != nil:
			gsWhy = fmt.Sprintf("%v (%s)", gsErr, tail(string(gsOut)))
		default:
			gsWhy = fmt.Sprintf("exited 0 but wrote no page (%s)", tail(string(gsOut)))
		}
		_ = os.Remove(out)
	}

	pp, _ := exec.LookPath("pdftoppm")
	if pp == "" {
		return fmt.Errorf("thumb: no PDF renderer left (gs: %s; pdftoppm: not installed)", gsWhy)
	}
	prefix := out[:len(out)-len(filepath.Ext(out))]
	cmd := exec.CommandContext(ctx, pp,
		"-jpeg", "-f", "1", "-l", "1",
		"-r", "96",
		pdfPath, prefix,
	)
	if ppOut, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumb: pdftoppm: %w (%s) [gs: %s]", err, tail(string(ppOut)), gsWhy)
	}
	// ⚠ pdftoppm appends the page number zero-padded to the width of the
	// DOCUMENT's page count, not to one digit: page 1 of a 9-page file is
	// "-1.jpg", of a 120-page file "-001.jpg". The old code renamed "-1.jpg"
	// only, so on any PDF with ten or more pages the rename silently missed
	// and the cache was left empty while the command had succeeded.
	matches, _ := filepath.Glob(prefix + "-*.jpg")
	if len(matches) == 0 {
		return fmt.Errorf("thumb: pdftoppm exited 0 but wrote no page (looked for %s-*.jpg)", prefix)
	}
	if err := os.Rename(matches[0], out); err != nil {
		return fmt.Errorf("thumb: pdftoppm output rename: %w", err)
	}
	for _, extra := range matches[1:] {
		_ = os.Remove(extra)
	}
	if !wroteFrame(out) {
		return fmt.Errorf("thumb: pdftoppm produced an empty page image")
	}
	return fitToThumb(out)
}

// fitToThumb scales a rendered page down to the size every other generator
// emits, in place. A page that is already small enough is left alone.
//
// Same scaleDown the image generator uses, deliberately: one rule decides how
// big a thumbnail is, so a PDF card and a photo card are drawn from images of
// the same order and the cache holds one kind of thing.
func fitToThumb(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("thumb: decode rendered page: %w", err)
	}
	b := src.Bounds()
	if b.Dx() <= thumbMaxWidth && b.Dy() <= thumbMaxHeight {
		return nil
	}
	dst := scaleDown(src, thumbMaxWidth, thumbMaxHeight)
	tmp := path + ".tmp"
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(w, dst, &jpeg.Options{Quality: thumbQuality}); err != nil {
		w.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
