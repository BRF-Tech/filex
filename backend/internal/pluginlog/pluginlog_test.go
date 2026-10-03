package pluginlog

// The one rule that keeps a plugin's page log, and the server log behind it,
// from drowning in one repeated line (issue #104): a storage plugin that
// cannot answer for a folder is asked about it on every sync pass, and every
// answer is the same.

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// clockAt is a log whose clock the test turns.
func clockAt(l *Log, t0 time.Time) *time.Time {
	now := t0
	l.now = func() time.Time { return now }
	return &now
}

func TestAddCountsARepeatInsteadOfWritingItAgain(t *testing.T) {
	l := New()
	now := clockAt(l, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	l.Add("warn", "storage s3: Proje: no answer")
	first, _ := l.After(0)
	if len(first) != 1 || first[0].Count != 1 {
		t.Fatalf("one line, count 1; got %+v", first)
	}
	*now = now.Add(10 * time.Second)
	l.Add("warn", "storage s3: Proje: no answer")
	*now = now.Add(10 * time.Second)
	l.Add("warn", "storage s3: Proje: no answer")

	all, next := l.After(0)
	if len(all) != 1 {
		t.Fatalf("a repeated line was written %d times", len(all))
	}
	ln := all[0]
	if ln.Count != 3 {
		t.Errorf("count %d, want 3", ln.Count)
	}
	if ln.ID != first[0].ID {
		t.Errorf("the repeat changed the line's id (%d -> %d): a poller cannot replace it", first[0].ID, ln.ID)
	}
	if ln.Seq <= first[0].Seq || ln.Seq != next {
		t.Errorf("the repeat did not move the cursor (seq %d, first %d, next %d): a poller never sees the new count", ln.Seq, first[0].Seq, next)
	}
	if ln.Last == nil || !ln.Last.Equal(*now) || !ln.TS.Equal(first[0].TS) {
		t.Errorf("first written %v, last %v; want the first time kept and the last repeat's time beside it", ln.TS, ln.Last)
	}

	// A poller that already had the line gets it once more, and only it.
	got, _ := l.After(first[0].Seq)
	if len(got) != 1 || got[0].ID != first[0].ID || got[0].Count != 3 {
		t.Errorf("after the first cursor: %+v", got)
	}
}

// Not only the line before: a pass that cannot answer for three folders writes
// three lines in turn, every pass.
func TestARepeatAmongTheRecentLinesIsCountedToo(t *testing.T) {
	l := New()
	for pass := 0; pass < 4; pass++ {
		for _, p := range []string{"A", "B", "C"} {
			l.Add("warn", "no answer for "+p)
		}
	}
	all, _ := l.After(0)
	if len(all) != 3 {
		t.Fatalf("%d lines for three folders over four passes, want 3", len(all))
	}
	for _, ln := range all {
		if ln.Count != 4 {
			t.Errorf("%q counted %d, want 4", ln.Msg, ln.Count)
		}
	}
	for i := 1; i < len(all); i++ {
		if all[i].Seq <= all[i-1].Seq {
			t.Errorf("the lines are not in cursor order: %+v", all)
		}
	}
}

// A different level is a different line.
func TestLevelIsPartOfTheLine(t *testing.T) {
	l := New()
	l.Add("info", "x")
	l.Add("warn", "x")
	if all, _ := l.After(0); len(all) != 2 {
		t.Errorf("%d lines, want 2", len(all))
	}
}

func TestTheLogKeepsTheNewestLines(t *testing.T) {
	l := New()
	for i := 0; i < Size+25; i++ {
		l.Add("info", fmt.Sprintf("line %d", i))
	}
	all, _ := l.After(0)
	if len(all) != Size {
		t.Fatalf("kept %d lines, want %d", len(all), Size)
	}
	if all[0].Msg != "line 25" || all[len(all)-1].Msg != fmt.Sprintf("line %d", Size+24) {
		t.Errorf("kept %q .. %q, want the newest", all[0].Msg, all[len(all)-1].Msg)
	}
}

func TestALongLineIsClipped(t *testing.T) {
	l := New()
	l.Add("error", strings.Repeat("ş", maxMsg))
	all, _ := l.After(0)
	if len(all[0].Msg) > maxMsg+40 {
		t.Errorf("a %d-byte line was kept whole", len(all[0].Msg))
	}
	if !strings.Contains(all[0].Msg, "more bytes") {
		t.Errorf("a clipped line does not say so: %q", all[0].Msg[len(all[0].Msg)-30:])
	}
}

// The server log gets the same line at most once every five minutes, with
// how many times it repeated in between.
func TestTheServerLogHearsARepeatAtMostEveryFiveMinutes(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := New().Mirror(logger, slog.String("plugin", "acme"))
	now := clockAt(l, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))

	for i := 0; i < 30; i++ { // every 10 s for five minutes
		l.Add("warn", "no answer for Proje")
		*now = now.Add(10 * time.Second)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("the server log got %d copies in five minutes, want 1:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "plugin=acme") || strings.Contains(lines[0], "repeated=") {
		t.Errorf("first copy: %s", lines[0])
	}

	l.Add("warn", "no answer for Proje") // 5 minutes after the first
	lines = strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("after five minutes the line was not written again:\n%s", out.String())
	}
	if !strings.Contains(lines[1], "repeated=29") {
		t.Errorf("the second copy does not say how often it repeated in between: %s", lines[1])
	}
}

// Only warnings and errors reach the server log; the page keeps everything.
func TestInfoLinesStayOnThePage(t *testing.T) {
	var out bytes.Buffer
	l := New().Mirror(slog.New(slog.NewTextHandler(&out, nil)))
	l.Add("info", "plugin up")
	if out.Len() != 0 {
		t.Errorf("an info line reached the server log: %s", out.String())
	}
	if all, _ := l.After(0); len(all) != 1 {
		t.Errorf("the page lost the info line")
	}
}

// A nil log takes lines and drops them: a storage with no plugin page.
func TestANilLogIsSafe(t *testing.T) {
	var l *Log
	l.Add("warn", "x")
	if got, next := l.After(7); len(got) != 0 || next != 7 {
		t.Errorf("nil log: %v %d", got, next)
	}
}

func TestJoinSaysWhereAndWhatTheSameWay(t *testing.T) {
	if got := Join("storage s3", "", " Proje ", "timeout"); got != "storage s3: Proje: timeout" {
		t.Errorf("Join = %q", got)
	}
}
