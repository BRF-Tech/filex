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

// generateVideo extracts a frame at 00:00:01 using ffmpeg and writes a JPEG
// to the cache.
//
// Strategy: download (or stream) into a tmp file, then ffmpeg -ss 1 -i
// IN -frames:v 1 -vf scale=320:-1 OUT.jpg. We do NOT use the streaming
// pipe because ffmpeg's seek-after-pipe behavior is unreliable on long
// videos.
func (p *Pipeline) generateVideo(ctx context.Context, node *model.Node, drv storage.Driver) error {
	tmp, err := os.CreateTemp("", "filex-vid-*.bin")
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
		"-ss", "1",
		"-i", tmp.Name(),
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-1", thumbMaxWidth),
		"-q:v", "5",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumb: ffmpeg: %w (%s)", err, string(out))
	}
	return nil
}
