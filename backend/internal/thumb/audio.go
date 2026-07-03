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

// generateAudio renders an audio waveform PNG via ffmpeg's `showwavespic`
// filter. Bands the waveform across the canvas so a flat (silent or
// near-silent) clip still produces something visible, and the listener
// can recognise the audio at a glance without opening the file.
//
// Output is a 320×120 JPEG matching the rest of the pipeline (Pipeline
// caches every state as `<id>.jpg` regardless of source). showwavespic
// writes PNG by default — we let ffmpeg re-encode to JPEG via the
// output extension.
func (p *Pipeline) generateAudio(ctx context.Context, node *model.Node, drv storage.Driver) error {
	tmp, err := os.CreateTemp("", "filex-aud-*.bin")
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
	outPath := filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID))
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", tmp.Name(),
		"-filter_complex",
		fmt.Sprintf("aformat=channel_layouts=mono,showwavespic=s=%dx120:colors=0x3b82f6", thumbMaxWidth),
		"-frames:v", "1",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumb: ffmpeg(audio): %w (%s)", err, string(out))
	}
	return nil
}
