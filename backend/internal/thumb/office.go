package thumb

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// generateOffice converts a doc/xls/ppt/odt/ods/odp to PDF via libreoffice
// in headless mode, then re-uses the PDF generator to render page 1.
func (p *Pipeline) generateOffice(ctx context.Context, node *model.Node, drv storage.Driver) error {
	bin := ""
	for _, candidate := range []string{"libreoffice", "soffice"} {
		if path, _ := exec.LookPath(candidate); path != "" {
			bin = path
			break
		}
	}
	if bin == "" {
		return fmt.Errorf("thumb: libreoffice not in PATH")
	}

	tmpDir, err := os.MkdirTemp("", "filex-office-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	srcName := filepath.Base(node.Name)
	if srcName == "" {
		srcName = "input"
	}
	srcPath := filepath.Join(tmpDir, srcName)
	src, err := os.Create(srcPath)
	if err != nil {
		return err
	}
	rc, err := drv.Read(ctx, node.Path)
	if err != nil {
		src.Close()
		return err
	}
	if _, err := io.Copy(src, rc); err != nil {
		rc.Close()
		src.Close()
		return err
	}
	rc.Close()
	src.Close()

	cmd := exec.CommandContext(ctx, bin,
		"--headless",
		"--convert-to", "pdf",
		"--outdir", tmpDir,
		srcPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumb: libreoffice: %w (%s)", err, string(out))
	}

	pdfName := strings.TrimSuffix(srcName, filepath.Ext(srcName)) + ".pdf"
	pdfPath := filepath.Join(tmpDir, pdfName)
	if _, err := os.Stat(pdfPath); err != nil {
		return fmt.Errorf("thumb: libreoffice produced no PDF: %s", pdfPath)
	}

	// Now reuse the gs/pdftoppm path.
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
			pdfPath,
		)
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("thumb: gs after libreoffice: %w (%s)", err, string(outBytes))
		}
		return nil
	}
	if path, _ := exec.LookPath("pdftoppm"); path != "" {
		cmd := exec.CommandContext(ctx, path,
			"-jpeg", "-f", "1", "-l", "1", "-r", "96",
			pdfPath, out[:len(out)-4],
		)
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("thumb: pdftoppm after libreoffice: %w (%s)", err, string(outBytes))
		}
		_ = os.Rename(out[:len(out)-4]+"-1.jpg", out)
		return nil
	}
	return fmt.Errorf("thumb: libreoffice OK but no PDF→JPG renderer")
}
