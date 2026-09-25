//go:build measure

package archivecli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestMeasureOvershoot measures how far past the byte limit an extraction
// gets before it is stopped: the peak number of bytes on disk, sampled every
// 2 ms, against the configured limit. It needs real bombs and a real 7-Zip,
// so it is behind a build tag and driven by the environment:
//
//	FILEX_MEASURE_ARCHIVE  the archive to extract
//	FILEX_MEASURE_NAME     the name the user sees (picks the reader); default
//	                       the archive's own name
//	FILEX_MEASURE_KIND     set (gzip, zip, 7z, …) to force the 7-Zip path
//	                       with that -t type and the workspace monitor — how
//	                       #48 extracted every format as submitted
//	FILEX_MEASURE_LIMIT    MaxExpandedBytes
//	FILEX_MEASURE_7Z       the 7-Zip binary
//
//	go test -tags measure -run TestMeasureOvershoot -v ./internal/archivecli
func TestMeasureOvershoot(t *testing.T) {
	archive := os.Getenv("FILEX_MEASURE_ARCHIVE")
	if archive == "" {
		t.Skip("no archive")
	}
	name := os.Getenv("FILEX_MEASURE_NAME")
	if name == "" {
		name = archive
	}
	limit, _ := strconv.ParseInt(os.Getenv("FILEX_MEASURE_LIMIT"), 10, 64)
	dest := t.TempDir()
	svc := New(memorySettings{
		SettingMaxEntries:       "20000",
		SettingMaxExpandedBytes: strconv.FormatInt(limit, 10),
		SettingTimeoutSeconds:   "600",
	}, Config{SevenZipBin: os.Getenv("FILEX_MEASURE_7Z")})
	var peak atomic.Int64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if total := diskBytes(dest); total > peak.Load() {
				peak.Store(total)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	start := time.Now()
	var err error
	if kind := os.Getenv("FILEX_MEASURE_KIND"); kind != "" {
		err = svc.extractSevenZip(context.Background(), archive, kind, dest, "", nil)
	} else {
		err = svc.ExtractAs(context.Background(), archive, name, dest, "", nil)
	}
	elapsed := time.Since(start)
	close(stop)
	<-sampled
	final := diskBytes(dest)
	if final > peak.Load() {
		peak.Store(final)
	}
	t.Logf("archive=%s kind=%q limit=%d MiB err=%v elapsed=%s peak=%.1f MiB (%.2fx limit, %+.1f MiB) left-on-disk=%.1f MiB",
		filepath.Base(name), os.Getenv("FILEX_MEASURE_KIND"), limit>>20, err, elapsed.Round(time.Millisecond),
		float64(peak.Load())/(1<<20), float64(peak.Load())/float64(limit), float64(peak.Load()-limit)/(1<<20),
		float64(final)/(1<<20))
}

func diskBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
