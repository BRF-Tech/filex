package filesync

import (
	"fmt"
	"time"
)

// humanBytes renders a byte count the way the progress line shows it.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
		v /= 1024
		if v < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
	}
	return fmt.Sprintf("%d B", n) // unreachable
}

// etaMinElapsed is how long transfers run before a remaining-time estimate is
// shown: the first seconds are dominated by connection setup and small files.
const etaMinElapsed = 5 * time.Second

// etaText estimates the time left from the average rate so far, or returns ""
// when there is nothing sensible to say yet.
func etaText(done, total int64, elapsed time.Duration) string {
	if done <= 0 || done >= total || elapsed < etaMinElapsed {
		return ""
	}
	left := time.Duration(float64(elapsed) * float64(total-done) / float64(done)).Round(time.Second)
	switch {
	case left >= time.Hour:
		left = left.Round(time.Minute)
		return fmt.Sprintf("about %dh %dm left", int(left/time.Hour), int((left%time.Hour)/time.Minute))
	case left >= time.Minute:
		return fmt.Sprintf("about %dm left", int(left.Round(time.Minute)/time.Minute))
	default:
		return fmt.Sprintf("about %ds left", int(left/time.Second))
	}
}

// transferBytes is how many bytes an action moves, for the progress line: a
// download or an upload moves its file, a conflict fetches the server's copy
// and (when it differs) sends the local one.
func transferBytes(a Action, local Snapshot) int64 {
	switch a.Kind {
	case ActionDownload:
		return a.RemoteSize
	case ActionUpload:
		return local[a.Rel].Size
	case ActionConflict:
		if a.Mixed {
			return 0
		}
		return a.RemoteSize + local[a.Rel].Size
	}
	return 0
}

// transferLine is the `transfer:` progress line. The byte part — and within
// it the estimate — only appears once there is something to say; the leading
// "done/total" never changes shape, because the desktop app reads it.
func transferLine(done, planned int, bytesDone, bytesTotal int64, elapsed time.Duration) string {
	line := fmt.Sprintf("transfer: %d/%d", done, planned)
	if bytesTotal <= 0 {
		return line
	}
	line += fmt.Sprintf(" (%s of %s", humanBytes(bytesDone), humanBytes(bytesTotal))
	if eta := etaText(bytesDone, bytesTotal, elapsed); eta != "" {
		line += ", " + eta
	}
	return line + ")"
}
