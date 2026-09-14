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

const (
	// Average luma (0-255) a frame has to clear to count as "something to
	// look at". Video black is 16, not 0 — a frame sitting on the broadcast
	// floor has nothing in it — and measurement on 2026-09-12 showed a
	// threshold of exactly 16 still letting black frames through, so the
	// number carries a margin over the floor rather than sitting on it.
	minFrameLuma = 24
	// How far into the clip the search for an opening frame runs. Long
	// enough for a title card or a fade-in, short enough that one thumbnail
	// never turns into a full decode of a feature film.
	firstFrameWindowSeconds = "10"
)

// generateVideo writes the video's FIRST frame to the cache as a JPEG.
//
// "First" needs one qualification, and it is the whole design. A great many
// real videos open on black — a fade-in, a slate, a camera's own leader — so
// the literal frame 0 of a holiday clip is a black rectangle, which tells the
// reader exactly as much as the coloured "MP4" tile it was meant to replace.
// So the rule is: the first frame with something in it, looked for over the
// opening seconds; and when the clip is dark the whole way through, the
// literal first frame, because then black really is what the video looks like.
//
// ⚠ This replaces a single `-ss 1` seek, which failed in two measured ways
// (both on the fixtures in this repo's thumbnail harness, 2026-09-12):
//
//   - A clip SHORTER than one second decoded no frame at all. ffmpeg printed
//     "Output file is empty, nothing was encoded" and exited ZERO, so the
//     pipeline wrote state=ready for a thumbnail that had never been created
//     and the card 404ed on /api/files/thumb/{id} — landing on the generic
//     extension tile with nothing anywhere saying why. A 0.4s fixture
//     reproduced it every time.
//   - One second was not a guard against black either: a 1.6s fade-in puts
//     the one-second mark inside the fade, and the fixture produced a
//     581-byte pure-black JPEG that the pipeline happily called ready.
//
// Both are why every ffmpeg run here is followed by wroteFrame(): ffmpeg's
// exit code does not tell you whether a frame came out, so the file itself is
// the only honest answer.
func (p *Pipeline) generateVideo(ctx context.Context, node *model.Node, drv storage.Driver) error {
	tmp, err := os.CreateTemp("", "filex-vid-*.bin")
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
	return extractFirstFrame(ctx, tmp.Name(), filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID)))
}

// extractFirstFrame writes the opening frame of the video at `srcPath` to
// `outPath`. Split out from generateVideo so the decision it makes — which
// frame, and what to do when there is none — can be measured against real
// clips without a store, a driver or a node behind it.
func extractFirstFrame(ctx context.Context, srcPath, outPath string) error {
	_ = os.Remove(outPath)

	// Pass 1 — the first frame that is not black. signalstats publishes each
	// frame's average luma as metadata and the metadata filter drops the
	// frames below the threshold, so `-frames:v 1` stops on the first one
	// that survives.
	selectBright := fmt.Sprintf(
		"signalstats,metadata=mode=select:key=lavfi.signalstats.YAVG:value=%d:function=greater,scale=%d:-1",
		minFrameLuma, thumbMaxWidth,
	)
	first := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-t", firstFrameWindowSeconds,
		"-i", srcPath,
		"-vf", selectBright,
		"-frames:v", "1",
		"-q:v", "5",
		// ⚠ An OUTPUT option, and it has to stay after the input: given to
		// ffmpeg before `-i` it is parsed as an input option, the image2
		// muxer never gets it, and the run writes nothing at all.
		"-update", "1",
		outPath,
	)
	firstOut, firstErr := first.CombinedOutput()
	if firstErr == nil && wroteFrame(outPath) {
		return nil
	}

	// Pass 2 — the literal first frame. Reached when the opening really is
	// dark (an all-black clip, a night shot) or when signalstats is not
	// available in this ffmpeg build.
	_ = os.Remove(outPath)
	second := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", srcPath,
		"-vf", fmt.Sprintf("scale=%d:-1", thumbMaxWidth),
		"-frames:v", "1",
		"-q:v", "5",
		"-update", "1",
		outPath,
	)
	secondOut, secondErr := second.CombinedOutput()
	if secondErr != nil {
		return fmt.Errorf("thumb: ffmpeg: %w (%s)", secondErr, string(secondOut))
	}
	if !wroteFrame(outPath) {
		// Exit code zero, no frame. Say so, and carry what the first pass
		// reported too — when a file is damaged the two runs usually fail
		// for the same reason and only one of them prints it.
		return fmt.Errorf(
			"thumb: ffmpeg exited 0 but wrote no frame (first pass: %v %s) (second pass: %s)",
			firstErr, tail(string(firstOut)), tail(string(secondOut)),
		)
	}
	return nil
}

// wroteFrame answers the only question that matters after an ffmpeg or
// ghostscript run: is there an image on disk? Their exit codes do not say —
// ffmpeg reports success for "Output file is empty, nothing was encoded" —
// so the pipeline must not take a zero exit as proof that a card has
// something to show.
func wroteFrame(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

// tail keeps the end of a subprocess transcript — the part with the error in
// it. ffmpeg leads with a banner and a stream dump nobody needs in a log line.
func tail(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}
