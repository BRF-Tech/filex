// Package antivirus — savewindow.go
//
// The save-scan window: how long filex waits, after a text-editor save to an
// EXISTING file, before it queues the antivirus scan of that file.
//
// Why a window exists at all. `/api/files/save-text` is the browser's Monaco /
// markdown editor. A person editing a file hits Ctrl+S dozens of times in a
// sitting, and scanning on every save would spend a full ClamAV pass per
// keystroke-burst on a file nobody has finished writing yet. So a save
// SCHEDULES one scan, further saves inside the window are ignored rather than
// rescheduled, and when the scan finally runs it reads the file as it stands
// then — the final state, not the bytes of the first save.
//
// The trade, stated plainly because the number looks arbitrary otherwise:
// one scan per file per window of editing, paid for by the file sitting
// unscanned for up to that long after its LAST save. Shorter = more scans,
// smaller exposure. Longer = fewer scans, bigger exposure. 30 minutes is a
// picked value, not an inherited one: it is longer than a normal editing
// burst (so the burst really does collapse to one scan) and short enough that
// an infected file written through the editor is quarantined well within the
// hour.
//
// ⚠ File CREATION is not covered by any of this. A file the editor creates is
// scanned immediately, exactly like an upload — see handlers.SaveText.
package antivirus

import (
	"context"
	"time"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// Window bounds, in minutes, for SaveWindowSetting.
//
//   - Min 2: below this the window stops coalescing anything (a save every
//     couple of minutes is still one editing session) while multiplying scans.
//   - Max 60: the window IS the time an infected file written through the
//     editor stays live. An hour is the most exposure this feature will trade
//     for fewer scans; longer is not a tuning preference, it is a different
//     security posture.
//
// ⚠ 0 has no meaning here and is not accepted. It reads equally as "scan every
// save" and "never scan", which are opposite behaviours — and the second would
// silently re-open the very gap this window lives inside. Turning scanning off
// is FILEX_CLAMAV=0, a separate and explicit switch.
const (
	DefaultSaveWindowMinutes = 30
	MinSaveWindowMinutes     = 2
	MaxSaveWindowMinutes     = 60
)

// SaveWindowSetting declares the window as a database-backed setting.
//
// ⚠⚠ It is configured DIFFERENTLY from its siblings today, and that is worth
// knowing before you copy either style. FILEX_CLAMAV, FILEX_CLAMAV_BIN and
// FILEX_CLAMAV_MAX are env-only, read straight from the process environment in
// antivirus.go, and the admin Protection page renders the antivirus block
// read-only. This one is a settings-table value the Protection page can WRITE,
// so an operator who finds 30 minutes too long changes it without restarting
// the container. Two mechanisms for one feature is a real cost; it was chosen,
// not stumbled into, and it is the direction the rest of the family is meant
// to follow — this spec is the template for that move.
//
// ⚠⚠ FILEX_CLAMAV_SAVE_WINDOW_MINUTES is a SEED, not an override: it is read
// once, on a boot where no row exists yet. After that the stored row wins and
// changing the variable in compose does nothing. See package dbsetting.
var SaveWindowSetting = dbsetting.IntSpec{
	Key:     "antivirus.save_scan_window_minutes",
	EnvVar:  "FILEX_CLAMAV_SAVE_WINDOW_MINUTES",
	Default: DefaultSaveWindowMinutes,
	Min:     MinSaveWindowMinutes,
	Max:     MaxSaveWindowMinutes,
	Unit:    "minutes",
}

// SaveWindow resolves the window in force right now.
//
// ⚠ Read at SCHEDULE time — once per save that actually schedules a scan, not
// cached at boot. That is what makes the setting live-changeable, which is the
// whole reason it is in the database. Scans already scheduled keep the
// not_before they were given: nothing ever rewrites a pending op, so a changed
// window applies to the next scan scheduled and to no existing one.
func SaveWindow(ctx context.Context, g dbsetting.Getter) time.Duration {
	return time.Duration(SaveWindowSetting.Resolve(ctx, g)) * time.Minute
}
