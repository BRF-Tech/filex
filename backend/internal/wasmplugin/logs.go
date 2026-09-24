package wasmplugin

import (
	"sync"
	"time"
)

// LogLine is one entry of a plugin's ring buffer: what the guest logged
// through pdk.Log plus the host's own warnings about it (refused calls,
// failed loads). The admin panel polls it with ?after=<seq>.
type LogLine struct {
	Seq   int64     `json:"seq"`
	TS    time.Time `json:"ts"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
}

const logRingSize = 500

type logRing struct {
	mu    sync.Mutex
	lines []LogLine
	seq   int64
}

func (l *logRing) add(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.lines = append(l.lines, LogLine{Seq: l.seq, TS: time.Now(), Level: level, Msg: clip(msg, 2000)})
	if len(l.lines) > logRingSize {
		l.lines = l.lines[len(l.lines)-logRingSize:]
	}
}

// after returns the lines with Seq > after and the next cursor.
func (l *logRing) after(after int64) ([]LogLine, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []LogLine{}
	for _, ln := range l.lines {
		if ln.Seq > after {
			out = append(out, ln)
		}
	}
	return out, l.seq
}
