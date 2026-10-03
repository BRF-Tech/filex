package enginebin

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HEIC answers a narrower question than Has(ImageMagick): can the ImageMagick
// this server resolved DECODE a HEIC photo?
//
// ⚠⚠ Installed is not able. ImageMagick reads HEIC through libheif, and libheif
// decodes the HEVC picture inside a HEIC through a plugin. On Ubuntu 24.04
// (libheif 1.17.6) that plugin, libheif-plugin-libde265, is only SUGGESTED by
// libheif1, so `apt install imagemagick` never brings it: ImageMagick lists
// HEIC among its formats, and every phone photo fails with "Unsupported
// feature: Unsupported codec". 0.50's capabilities said HEIC could be drawn
// because ImageMagick was there, and every HEIC file ended `failed` in a
// thumbnail repair (found in the 0.50 test phase, thumbnails scene; reproduced
// in ubuntu:24.04 with this file's sample, which decodes once the plugin is
// installed).
//
// So the answer is measured: the sample below is drawn through the command the
// thumbnailer runs (MagickThumbArgs), and it counts only when ImageMagick hands
// back a picture of the sample's size and colour. The probe runs once, the
// first time anybody asks (the server asks at boot), and again only when the
// ImageMagick binary changes (another path, size or modification time). AVIF
// is not part of it: its AV1 decoder is another plugin, and libheif1 depends
// on one (aomdec or dav1d) on Ubuntu, as libheif does on Alpine.

// heicProbeSample is a 64x64 HEIC of one colour, rgb(220,60,40), 406 bytes.
// It was made for this probe, so no third party's picture is in the binary:
//
//	magick -size 64x64 "xc:rgb(220,60,40)" src.png
//	heif-enc -q 50 -o heic_probe.heic src.png
//
// in alpine:3.24 (libheif 1.23.4 with libheif-x265, x265 4.1). A 16x16 or
// 32x32 one came out larger (457 and 445 bytes): the encoder pads to its
// block size and says so in the stream.
//
//go:embed heic_probe.heic
var heicProbeSample []byte

const heicProbeSize = 64

// heicProbeColour is what the sample decodes to. HEVC is lossy and its colour
// passes through YCbCr, so a decoder is allowed heicColourSlack per channel
// (measured: rgb(221,60,40) on libheif 1.17 and 1.23).
var heicProbeColour = [3]int{220, 60, 40}

const heicColourSlack = 24

// HEICStatus is the probe's answer.
type HEICStatus struct {
	// Decodes: ImageMagick drew the sample, at its size and in its colour.
	Decodes bool
	// Detail says why not: ImageMagick's own words, or what came back instead
	// of the picture. Empty when Decodes.
	Detail string
}

// The probe's clocks, variables so a test can shorten them.
var (
	// heicProbeTimeout bounds one ImageMagick run. Decoding 64x64 pixels takes
	// milliseconds; this is for a host that is busy, or a binary that hangs.
	heicProbeTimeout = 15 * time.Second
	// heicRecheck is how often the binary's identity is looked at again (a
	// stat, not a run): the answer is read for every HEIC thumbnail.
	heicRecheck = 30 * time.Second
	// heicRetry: an answer that is no answer (the run timed out, the sample
	// could not be written) is asked again after this, not kept until the
	// binary changes.
	heicRetry = time.Minute
)

type heicCache struct {
	mu sync.Mutex
	// bin and ident are the binary the answer is about (ident: its path,
	// size and modification time). "" = nothing asked yet.
	bin, ident string
	status     HEICStatus
	// looked is when ident was last compared with the binary on disk.
	looked time.Time
	// retryAt is set when the answer was no answer: ask again after it.
	retryAt time.Time
}

var heicAnswer heicCache

// HEIC is the server's one answer to "can a HEIC photo be drawn here", for
// the thumbnail pipeline, the capabilities and the boot log alike.
func HEIC() HEICStatus {
	return heicAnswer.get(Probe().Path(ImageMagick), time.Now(), probeHEIC)
}

func (c *heicCache) get(bin string, now time.Time, probe func(bin string) (HEICStatus, bool)) HEICStatus {
	if bin == "" {
		return HEICStatus{Detail: "ImageMagick is not installed"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	due := !c.retryAt.IsZero() && !now.Before(c.retryAt)
	if bin == c.bin && !due && now.Sub(c.looked) < heicRecheck {
		return c.status
	}
	ident := binaryIdentity(bin)
	c.looked = now
	if bin == c.bin && ident == c.ident && !due {
		return c.status
	}
	// ⚠ Run under the lock: the callers that arrive meanwhile (thumbnail
	// workers, a capabilities refresh) wait for this one answer instead of
	// starting an ImageMagick each.
	status, settled := probe(bin)
	c.bin, c.ident, c.status = bin, ident, status
	c.retryAt = time.Time{}
	if !settled {
		c.retryAt = now.Add(heicRetry)
	}
	slog.Info("enginebin: HEIC decode probe",
		slog.String("imagemagick", bin),
		slog.Bool("decodes", status.Decodes),
		slog.String("detail", status.Detail))
	return status
}

// binaryIdentity is what "the binary changed" is measured by.
func binaryIdentity(bin string) string {
	fi, err := os.Stat(bin)
	if err != nil {
		return bin + "|?"
	}
	return fmt.Sprintf("%s|%d|%d", bin, fi.Size(), fi.ModTime().UnixNano())
}

// MagickThumbArgs are the ImageMagick arguments that draw the primary image
// of src (`[0]`: the first of a burst or a sequence), upright (-auto-orient
// before -thumbnail, so the box fits the picture as it is shown), into a w x h
// box it is never enlarged to, as a PNG on stdout. thumb.generateHEIF runs
// them for every HEIC, HEIF and AVIF, and the HEIC probe runs the same ones:
// the probe measures the command the thumbnails use, not a cousin of it.
func MagickThumbArgs(src string, w, h int) []string {
	return []string{src + "[0]", "-auto-orient", "-thumbnail", fmt.Sprintf("%dx%d>", w, h), "png:-"}
}

// probeHEIC draws the sample through bin. settled is false when the run said
// nothing about HEIC (it timed out, the sample could not be written): that
// answer is asked again after heicRetry.
func probeHEIC(bin string) (status HEICStatus, settled bool) {
	dir, err := os.MkdirTemp("", "filex-heic-probe-*")
	if err != nil {
		return HEICStatus{Detail: "the HEIC probe could not make a temporary folder: " + err.Error()}, false
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "probe.heic")
	if err := os.WriteFile(src, heicProbeSample, 0o600); err != nil {
		return HEICStatus{Detail: "the HEIC probe could not write its sample: " + err.Error()}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), heicProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, MagickThumbArgs(src, heicProbeSize, heicProbeSize)...)
	cmd.WaitDelay = time.Second
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return HEICStatus{Detail: fmt.Sprintf("ImageMagick did not decode the HEIC sample within %s", heicProbeTimeout)}, false
	}
	if runErr != nil {
		said := firstLine(stderr.String())
		if said == "" {
			said = runErr.Error()
		}
		return HEICStatus{Detail: said}, true
	}
	return checkHEICPicture(out.Bytes()), true
}

// checkHEICPicture: what ImageMagick wrote is the sample, decoded.
func checkHEICPicture(b []byte) HEICStatus {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return HEICStatus{Detail: "ImageMagick exited 0 but wrote no PNG for the HEIC sample"}
	}
	r := img.Bounds()
	if r.Dx() != heicProbeSize || r.Dy() != heicProbeSize {
		return HEICStatus{Detail: fmt.Sprintf("ImageMagick drew the %dx%d HEIC sample as %dx%d", heicProbeSize, heicProbeSize, r.Dx(), r.Dy())}
	}
	cr, cg, cb, _ := img.At(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2).RGBA()
	got := [3]int{int(cr >> 8), int(cg >> 8), int(cb >> 8)}
	for i := range got {
		if d := got[i] - heicProbeColour[i]; d > heicColourSlack || d < -heicColourSlack {
			return HEICStatus{Detail: fmt.Sprintf("ImageMagick decoded the HEIC sample to rgb(%d,%d,%d), not rgb(%d,%d,%d)",
				got[0], got[1], got[2], heicProbeColour[0], heicProbeColour[1], heicProbeColour[2])}
		}
	}
	return HEICStatus{Decodes: true}
}

// firstLine is the first line of a program's complaint that says something.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
