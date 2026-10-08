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

// ProgressEvent is one progress report in figures (Engine.OnProgress) — the
// same report Engine.Progress gets as an English line.
//
// Phase is inventory | plan | transfer | settling | hold:
//
//   - inventory: Here is what was found on this computer; once the server
//     listing reports, Listing is true and Listed / ListedFolders are its
//     items and folders so far (Here still set).
//   - plan: Total is the changes to make.
//   - transfer: Done / Total actions; BytesDone / BytesTotal once there are
//     bytes to move; ETA the estimate of the time left, HasETA when there is
//     one (the first seconds have none).
//   - settling: Done of Total changes recorded.
//   - hold: Held is the items held for a decision.
type ProgressEvent struct {
	Phase         string
	Here          int
	Listing       bool
	Listed        int
	ListedFolders int
	Done, Total   int
	BytesDone     int64
	BytesTotal    int64
	ETA           time.Duration
	HasETA        bool
	Held          int
}

// transferEvent is the `transfer` report in figures (transferLine in words).
func transferEvent(done, planned int, bytesDone, bytesTotal int64, elapsed time.Duration) ProgressEvent {
	ev := ProgressEvent{Phase: "transfer", Done: done, Total: planned}
	if bytesTotal > 0 {
		ev.BytesDone, ev.BytesTotal = bytesDone, bytesTotal
		ev.ETA, ev.HasETA = etaLeft(bytesDone, bytesTotal, elapsed)
	}
	return ev
}

// etaLeft estimates the time left from the average rate so far; false when
// there is nothing sensible to say yet.
func etaLeft(done, total int64, elapsed time.Duration) (time.Duration, bool) {
	if done <= 0 || done >= total || elapsed < etaMinElapsed {
		return 0, false
	}
	return time.Duration(float64(elapsed) * float64(total-done) / float64(done)).Round(time.Second), true
}

// etaText estimates the time left from the average rate so far, or returns ""
// when there is nothing sensible to say yet.
func etaText(done, total int64, elapsed time.Duration) string {
	left, ok := etaLeft(done, total, elapsed)
	if !ok {
		return ""
	}
	// Rounded to the unit it is said in BEFORE the unit is chosen: 59m30s is
	// "about 1h 0m", never "about 60m".
	switch m := left.Round(time.Minute); {
	case m >= time.Hour:
		return fmt.Sprintf("about %dh %dm left", int(m/time.Hour), int((m%time.Hour)/time.Minute))
	case left >= time.Minute:
		return fmt.Sprintf("about %dm left", int(m/time.Minute))
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
