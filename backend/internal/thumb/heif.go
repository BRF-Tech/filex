package thumb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// heifTimeout bounds one ImageMagick run. A 48 MP phone photo decodes in
// well under a second; this is a ceiling for a hostile or damaged file.
const heifTimeout = 60 * time.Second

// generateHEIF draws a HEIC, HEIF or AVIF picture, which Go cannot decode,
// through ImageMagick (libheif underneath: it composes a phone's tiled
// photos and honours their rotation). ImageMagick scales it to the thumbnail
// box and hands back a PNG, and the PNG goes through the same writeJPEG as
// every picture, so an AVIF with transparency gets the same backdrop.
//
// The pipeline only calls it when the install has ImageMagick
// (Capabilities.HEIF); without it the file is skipped as "no_tool:heif". A
// HEIC (HEVC inside) is also skipped, as "no_tool:heic_codec", when that
// ImageMagick cannot decode the HEIC sample enginebin.HEIC draws through the
// very same arguments (libheif without its HEVC decoder plugin).
func (p *Pipeline) generateHEIF(ctx context.Context, node *model.Node, drv storage.Driver) error {
	bin := enginebin.Probe().Path(enginebin.ImageMagick)
	if bin == "" {
		return errors.New("thumb: heif: ImageMagick is not installed")
	}
	dir, err := os.MkdirTemp("", "filex-heif-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "input"+extOf(node.Name))
	if err := p.copySource(ctx, drv, node, src); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, heifTimeout)
	defer cancel()
	// The primary image, upright, into the thumbnail box: the arguments the
	// HEIC probe runs as well (enginebin.MagickThumbArgs).
	cmd := exec.CommandContext(ctx, bin, enginebin.MagickThumbArgs(src, thumbMaxWidth, thumbMaxHeight)...)
	cmd.WaitDelay = time.Second
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("thumb: heif: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	img, _, err := image.Decode(&out)
	if err != nil {
		return fmt.Errorf("thumb: heif: decode ImageMagick's output: %w", err)
	}
	return p.writeJPEG(node.ID, img, thumbQuality)
}

// copySource copies node's bytes to path, through openSource (so an
// end-to-end encrypted file is refused before a tool sees it).
func (p *Pipeline) copySource(ctx context.Context, drv storage.Driver, node *model.Node, path string) error {
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
