package thumb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
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
	rc, err := p.openSource(ctx, drv, node)
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
		"--norestore",
		"--nologo",
		"--nofirststartwizard",
		"-env:UserInstallation=file://"+tmpDir+"/lo-profile",
		"--convert-to", "pdf",
		"--outdir", tmpDir,
		srcPath,
	)
	// Confine LibreOffice's per-user state to the per-call tmp dir so
	// concurrent backfill workers don't collide on the shared
	// /root/.config/libreoffice/4/user lock and so the "Warning:
	// failed to read path from javaldx" stderr line stops hitting
	// process logs on first invocation.
	cmd.Env = append(append([]string(nil), os.Environ()...),
		"HOME="+tmpDir,
		"XDG_CACHE_HOME="+tmpDir+"/cache",
		"XDG_CONFIG_HOME="+tmpDir+"/config",
		"XDG_DATA_HOME="+tmpDir+"/data",
	)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("thumb: libreoffice: %w (%s)", err, strings.TrimSpace(string(combined)))
	}

	pdfName := strings.TrimSuffix(srcName, filepath.Ext(srcName)) + ".pdf"
	pdfPath := filepath.Join(tmpDir, pdfName)
	if _, statErr := os.Stat(pdfPath); statErr != nil {
		// soffice exited 0 but there is no PDF — this usually happens
		// when the source file does not match the schema soffice
		// expects (truncated pptx, etc.) or when the output name does
		// not match our convention. Attach soffice's own stdout/stderr
		// so the root cause is visible.
		entries, _ := os.ReadDir(tmpDir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slog.Warn("thumb: libreoffice produced no PDF",
			slog.String("src", srcName),
			slog.String("expected", pdfPath),
			slog.String("tmp_dir_listing", strings.Join(names, ",")),
			slog.String("soffice_output", strings.TrimSpace(string(combined))),
		)
		// Fallback: scan tmpDir for ANY .pdf — soffice occasionally
		// names the file after the embedded document title rather
		// than the source filename.
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
				pdfPath = filepath.Join(tmpDir, e.Name())
				goto pdfFound
			}
		}
		return fmt.Errorf("thumb: libreoffice produced no PDF: %s (cmd output: %s)", pdfPath, strings.TrimSpace(string(combined)))
	}
pdfFound:

	// ⚠ ONE renderer, shared with generatePDF. This function used to carry
	// its own transcription of the gs/pdftoppm block, which is exactly how
	// the office path kept every bug the PDF path had already been fixed
	// for: the missing downscale (a 358 KB full-page JPEG behind a 184×108
	// card), the zero-exit-but-no-file case, and pdftoppm's zero-padded
	// output name. A document that reaches a card through LibreOffice and
	// one that arrives as a PDF are the same picture of the same page —
	// they must not be produced by two pieces of code.
	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID))
	if err := renderPDFPage1(ctx, pdfPath, out); err != nil {
		return fmt.Errorf("thumb: after libreoffice: %w", err)
	}
	return nil
}
