package wasmplugin

import "github.com/brf-tech/filex/backend/internal/pluginlog"

// LogLine is one entry of a plugin's log: what the guest logged through
// pdk.Log plus the host's own warnings about it (refused calls, failed loads).
// The admin panel polls it with ?after=<seq>.
//
// The log is internal/pluginlog, the one the storage plugins' page reads too
// (issue #104): a line repeated within the last 50 is counted on the line
// already there (`count`, `last`) instead of written again.
type LogLine = pluginlog.Line

// logRing is an app plugin's log.
type logRing struct{ pluginlog.Log }

func (l *logRing) add(level, msg string) { l.Add(level, msg) }

// after returns the lines with Seq > after and the next cursor.
func (l *logRing) after(after int64) ([]LogLine, int64) { return l.After(after) }
