package thumb

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// generatePDF renders page 1 to a JPEG using ghostscript.
//
// Tries `gs` first (Ghostscript), falls back to `pdftoppm` (poppler-utils).
func (p *Pipeline) generatePDF(ctx context.Context, node *model.Node, drv storage.Driver) error {
	tmp, err := os.CreateTemp("", "filex-pdf-*.pdf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	rc, err := drv.Read(ctx, node.Path)
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

	if path, _ := exec.LookPath("gs"); path != "" {
		cmd := exec.CommandContext(ctx, path,
			"-sDEVICE=jpeg",
			"-dFirstPage=1", "-dLastPage=1",
			"-r96",
			"-dJPEGQ=80",
			"-o", out,
			tmp.Name(),
		)
		if outBytes, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			_ = outBytes
		}
	}
	if path, _ := exec.LookPath("pdftoppm"); path != "" {
		cmd := exec.CommandContext(ctx, path,
			"-jpeg", "-f", "1", "-l", "1",
			"-r", "96",
			tmp.Name(), out[:len(out)-4],
		)
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("thumb: pdftoppm: %w (%s)", err, string(outBytes))
		}
		// pdftoppm appends -1.jpg.
		_ = os.Rename(out[:len(out)-4]+"-1.jpg", out)
		return nil
	}
	return fmt.Errorf("thumb: no PDF renderer (need gs or pdftoppm)")
}
