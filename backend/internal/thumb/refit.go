package thumb

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ─────────────────── re-fitting pages cached at full size ───────────────────
//
// Before 0.41.0 the PDF and office generators wrote the rasterised first page
// at `gs -r96` size straight into the cache: 794×1123 for A4, 100–400 KB of
// JPEG behind a 184×108 card. fitToThumb fixed what NEW renders produce and
// nothing else, so an upgraded install kept serving every older page at full
// size — the explorer fetches every thumbnail of a folder it opens, and a
// folder of a few hundred office documents cost a browser tab ~3.6 MB of
// decoded image per card.
//
// RefitOversized rewrites those files at the size the pipeline emits today.
// The test for "left over" is the generators' own contract: every one of them
// bounds the WIDTH at thumbMaxWidth (image and PDF/office fit a 320×320 box;
// video and SVG are scaled to 320 wide and keep their height), so a cached
// file wider than that can only predate the fix. It is scaled with the same
// scaleDown fitToThumb uses; a portrait video frame (320×569) is left as it is.
//
// It never deletes and never regenerates. A file it cannot decode, one whose
// header claims more than refitMaxPixels, one written within reapGrace, and one
// that changes while it works are left as they were;
// the new bytes go to a temp file beside the old one and are renamed over it,
// so a request never reads half a JPEG.

// refitMaxPixels bounds what a re-fit will decode. Every file it is meant for
// is a page rendered at 96 dpi (A4 is under 1 MP, A0 is 14 MP). The pass runs
// at every boot, and the decoder allocates for every pixel a header claims
// before it learns whether the rest of the file exists — one damaged header in
// the cache must not be able to take the server down on start.
const refitMaxPixels = 50_000_000

// RefitResult is what one re-fit pass did.
type RefitResult struct {
	Scanned  int   // cache files whose dimensions were read and judged
	Refitted int   // files rewritten at thumbnail size
	Freed    int64 // bytes the rewrites saved
	Failed   int   // oversized files that could not be rewritten (left as they were)
	Skipped  int   // not ours, too fresh, not a readable JPEG, or too large to decode safely
}

// RefitOversized scales down every cached thumbnail wider than thumbMaxWidth.
// See the comment above for which files it touches. An error means the pass
// stopped early (the directory could not be read, or ctx ended); files already
// rewritten stay rewritten.
func (p *Pipeline) RefitOversized(ctx context.Context) (RefitResult, error) {
	var res RefitResult
	if p == nil || p.cacheDir == "" {
		return res, nil
	}
	ents, err := os.ReadDir(p.cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res, nil
		}
		return res, err
	}
	cutoff := time.Now().Add(-reapGrace)
	for _, e := range ents {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if e.IsDir() {
			continue
		}
		if !thumbFileRe.MatchString(e.Name()) {
			res.Skipped++
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			res.Skipped++
			continue
		}
		path := filepath.Join(p.cacheDir, e.Name())
		cfg, ok := jpegConfig(path)
		if !ok {
			res.Skipped++
			continue
		}
		if int64(cfg.Width)*int64(cfg.Height) > refitMaxPixels {
			res.Skipped++
			slog.Warn("thumb: cache file claims too many pixels to decode safely; left as it was",
				slog.String("path", path), slog.Int("width", cfg.Width), slog.Int("height", cfg.Height))
			continue
		}
		res.Scanned++
		if cfg.Width <= thumbMaxWidth {
			continue
		}
		freed, written, rerr := refitFile(path, info)
		switch {
		case rerr != nil:
			res.Failed++
			slog.Warn("thumb: could not re-fit an oversized cache file; left as it was",
				slog.String("path", path), slog.String("err", rerr.Error()))
		case !written:
			// Rewritten by somebody else while we worked: the newer file wins.
			res.Skipped++
		default:
			res.Refitted++
			res.Freed += freed
		}
	}
	return res, nil
}

// jpegConfig reads a cache file's header; ok is false for anything that is not
// a JPEG it can parse.
func jpegConfig(path string) (image.Config, bool) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, false
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "jpeg" {
		return image.Config{}, false
	}
	return cfg, true
}

// refitFile rewrites one cache file at thumbnail size. written is false when
// the file changed between the directory read (seen) and the rename.
func refitFile(path string, seen os.FileInfo) (freed int64, written bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return 0, false, fmt.Errorf("decode: %w", err)
	}
	dst := scaleDown(src, thumbMaxWidth, thumbMaxHeight)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".refit-*.tmp")
	if err != nil {
		return 0, false, err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has happened
	if err := jpeg.Encode(tmp, dst, &jpeg.Options{Quality: thumbQuality}); err != nil {
		tmp.Close()
		return 0, false, err
	}
	if err := tmp.Chmod(seen.Mode().Perm()); err != nil {
		tmp.Close()
		return 0, false, err
	}
	if err := tmp.Close(); err != nil {
		return 0, false, err
	}
	out, err := os.Stat(tmp.Name())
	if err != nil {
		return 0, false, err
	}

	now, err := os.Stat(path)
	if err != nil || !now.ModTime().Equal(seen.ModTime()) || now.Size() != seen.Size() {
		return 0, false, nil
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, false, err
	}
	return seen.Size() - out.Size(), true, nil
}

// refitPass runs RefitOversized and says what it did — one line, quiet or not,
// for the same reason the orphan sweep speaks every time.
func (p *Pipeline) refitPass(ctx context.Context) {
	res, err := p.RefitOversized(ctx)
	if err != nil {
		slog.Warn("thumb cache re-fit stopped early; files already rewritten stay rewritten",
			slog.String("dir", p.cacheDir), slog.String("err", err.Error()))
		return
	}
	slog.Info("thumb cache re-fit",
		slog.String("dir", p.cacheDir),
		slog.Int("scanned", res.Scanned),
		slog.Int("refitted", res.Refitted),
		slog.Int64("freed_bytes", res.Freed),
		slog.Int("failed", res.Failed),
		slog.Int("skipped", res.Skipped))
}
