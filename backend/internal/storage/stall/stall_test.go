package stall

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// The same failure reads differently on Windows. The admin form's "Test
// connection" on a Windows server said "the connection could not be made" for
// a refused port until both wordings were read. (Moved here from the S3
// driver with the classifier, issue #73.)
func TestReason_ReadsBothPlatformsWording(t *testing.T) {
	p := Settings{}.Policy(Defaults{AttemptTimeoutS: 10, MaxAttempts: 6, TotalTimeoutS: 15})
	cases := map[string]string{
		"dial tcp 127.0.0.1:1: connect: connection refused":                                                            "the connection was refused",
		"dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.": "the connection was refused",
		"read tcp 10.0.0.2:5->10.0.0.1:9000: read: connection reset by peer":                                           "the connection was reset",
		"wsarecv: An existing connection was forcibly closed by the remote host.":                                      "the connection was reset",
		"dial tcp 10.9.9.9:9000: connect: no route to host":                                                            "there is no route to the host",
		"dial tcp 10.9.9.9:9000: connectex: A socket operation was attempted to an unreachable host.":                  "there is no route to the host",
		"dial tcp 10.9.9.9:9000: connectex: A socket operation was attempted to an unreachable network.":               "there is no route to the host",
	}
	for msg, want := range cases {
		got, down := p.Reason(errors.New(msg))
		if !down || got != want {
			t.Errorf("%q: got (%q, %v), want %q", msg, got, down, want)
		}
	}
}

// The budget arithmetic, without sockets: after a silent attempt the next
// one is assumed to cost a whole attempt timeout, a refused one almost
// nothing, and a long moving transfer that broke gets a fresh budget.
func TestClock_Room(t *testing.T) {
	p := Policy{MaxAttempts: 6, AttemptTimeout: time.Second, TotalTimeout: 3 * time.Second}
	near := func(got, want time.Duration) bool {
		return got > want-150*time.Millisecond && got < want+150*time.Millisecond
	}

	silent := &Clock{P: p, start: time.Now().Add(-2500 * time.Millisecond), lastTook: time.Second}
	if r := silent.Room(); !near(r, -500*time.Millisecond) {
		t.Errorf("after a silent attempt at 2.5s: room %v, want about -0.5s (no attempt that cannot finish)", r)
	}
	refused := &Clock{P: p, start: time.Now().Add(-2500 * time.Millisecond), lastTook: time.Millisecond}
	if r := refused.Room(); !near(r, 500*time.Millisecond) {
		t.Errorf("after a refused attempt at 2.5s: room %v, want about 0.5s", r)
	}

	moving := &Clock{P: p, start: time.Now().Add(-10 * time.Second)}
	moving.Failed(time.Now().Add(-8*time.Second), errors.New("connection reset by peer"))
	if r := moving.Room(); !near(r, 2*time.Second) {
		t.Errorf("after an 8s transfer that broke: room %v, want about 2s (a fresh budget)", r)
	}
	silentLong := &Clock{P: p, start: time.Now().Add(-10 * time.Second)}
	silentLong.Failed(time.Now().Add(-8*time.Second), &Error{Kind: Answer, Limit: 8 * time.Second})
	if r := silentLong.Room(); r >= 0 {
		t.Errorf("after an 8s wait for an answer that never came: room %v, want none — silence earns no retry", r)
	}
	// A send the store stopped taking is cut after a minute — longer than the
	// budget, but silence all the same: no fresh budget, no second minute.
	stalledSend := &Clock{P: p, start: time.Now().Add(-65 * time.Second)}
	stalledSend.Failed(time.Now().Add(-65*time.Second),
		&net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded})
	if r := stalledSend.Room(); r >= 0 {
		t.Errorf("after a send that stalled for a minute: room %v, want none", r)
	}
}

// Do: fast failures are retried within the budget, up to MaxAttempts; a
// failure the caller calls final is not; a silent attempt as long as the
// budget earns no second one.
func TestDo_HoldsToTheBudget(t *testing.T) {
	p := Policy{MaxAttempts: 20, AttemptTimeout: time.Second, TotalTimeout: 3 * time.Second}
	refused := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	always := func(error) bool { return true }

	start := time.Now()
	c, err := p.Do(context.Background(), always, func(context.Context) error { return refused })
	took := time.Since(start)
	if err != refused || c.Attempts < 2 {
		t.Fatalf("a refused connect: err=%v after %d attempts", err, c.Attempts)
	}
	if took > 3500*time.Millisecond {
		t.Errorf("a refused connect was retried for %.1fs, budget 3s", took.Seconds())
	}

	c, _ = p.Do(context.Background(), func(error) bool { return false }, func(context.Context) error { return refused })
	if c.Attempts != 1 {
		t.Errorf("a final failure was tried %d times", c.Attempts)
	}

	few := Policy{MaxAttempts: 2, AttemptTimeout: time.Second, TotalTimeout: time.Minute}
	c, _ = few.Do(context.Background(), always, func(context.Context) error { return refused })
	if c.Attempts != 2 {
		t.Errorf("MaxAttempts 2: %d attempts", c.Attempts)
	}

	silence := &Error{Kind: Silent, Limit: time.Second}
	c, _ = p.Do(context.Background(), always, func(context.Context) error { time.Sleep(time.Second); return silence })
	if c.Attempts != 2 {
		// 1 s silent, then room = 3 - 1 - 1 = 1 s: one more; after it none.
		t.Errorf("silent attempts of 1s in a 3s budget: %d attempts, want 2", c.Attempts)
	}

	calls := 0
	c, err = p.Do(context.Background(), always, func(context.Context) error {
		calls++
		if calls < 3 {
			return refused
		}
		return nil
	})
	if err != nil || c.Attempts != 3 {
		t.Errorf("a store back on the third try: err=%v attempts=%d", err, c.Attempts)
	}
}

func TestUnavailable_SaysWhoWhatAndMatches(t *testing.T) {
	p := Policy{MaxAttempts: 3, AttemptTimeout: 15 * time.Second, TotalTimeout: 15 * time.Second}
	c := NewClock(p)
	c.Attempts = 3
	err := p.Unavailable("ftp server ftp.example.com:21", c, &Error{Kind: Silent, Limit: 15 * time.Second, Err: os.ErrDeadlineExceeded})
	if !errors.Is(err, storage.ErrUnavailable) {
		t.Fatalf("not ErrUnavailable: %v", err)
	}
	want := "ftp server ftp.example.com:21 is unavailable: the server sent nothing for 15s (3 attempts in "
	if got := err.Error(); len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("got %q", got)
	}
	refusal := errors.New("530 Login incorrect.")
	if got := p.Unavailable("ftp server x", c, refusal); got != refusal {
		t.Errorf("a refusal was rewritten: %v", got)
	}
}

func TestSettings_ReadsTheFormAndTheAPI(t *testing.T) {
	def := Defaults{AttemptTimeoutS: 15, MaxAttempts: 3, TotalTimeoutS: 15}
	got := Settings{AttemptTimeout: "4", MaxAttempts: float64(2), TotalTimeout: 99999}.Policy(def)
	want := Policy{MaxAttempts: 2, AttemptTimeout: 4 * time.Second, TotalTimeout: TotalTimeoutLimitS * time.Second}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
	if got := (Settings{AttemptTimeout: "", MaxAttempts: -1, TotalTimeout: "soon"}).Policy(def); got != (Settings{}).Policy(def) {
		t.Errorf("empty/negative/junk: %+v", got)
	}
	fields := def.Fields(ServerTexts(AttemptText))
	if len(fields) != 3 {
		t.Fatalf("%d fields", len(fields))
	}
	for i, w := range []struct {
		key      string
		def, max int
	}{{"attempt_timeout_s", 15, AttemptTimeoutLimitS}, {"max_attempts", 3, MaxAttemptsLimit}, {"total_timeout_s", 15, TotalTimeoutLimitS}} {
		f := fields[i]
		if f.Key != w.key || f.Default != w.def || *f.Min != 1 || *f.Max != w.max || !f.Advanced || f.Type != storage.FieldInt ||
			f.I18nKey == "" || f.HelpI18nKey == "" || f.Label == "" || f.Help == "" {
			t.Errorf("field %d: %+v", i, f)
		}
	}
}
