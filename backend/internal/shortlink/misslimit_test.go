package shortlink

import (
	"strconv"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/shortcode"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func start() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)} }

func TestMissLimiter_ABurstLocksAClientOutAndLetsItBack(t *testing.T) {
	clk := start()
	l := &MissLimiter{Now: clk.now}
	const c = "203.0.113.7"
	for i := 0; i < MissBurst-1; i++ {
		if l.Miss(c) {
			t.Fatalf("locked after %d misses, the burst is %d", i+1, MissBurst)
		}
	}
	if l.Locked(c) {
		t.Fatal("locked before the limit")
	}
	if !l.Miss(c) || !l.Locked(c) {
		t.Fatal("the miss that reaches the burst did not lock")
	}
	if l.Locked("203.0.113.8") {
		t.Fatal("a neighbour is locked")
	}
	clk.t = clk.t.Add(MissBurstLock - time.Second)
	if !l.Locked(c) {
		t.Fatal("lock lifted early")
	}
	clk.t = clk.t.Add(2 * time.Second)
	if l.Locked(c) {
		t.Fatal("lock did not lift")
	}
	for i := 0; i < MissBurst-1; i++ {
		if l.Miss(c) {
			t.Fatalf("after the lockout: locked at miss %d", i+1)
		}
	}
	if !l.Miss(c) {
		t.Fatal("second burst not locked")
	}
}

// A patient guesser who keeps every 10 minutes under the burst (counted
// over this window and the last, so 9 a window) is stopped by the day: 100
// misses, then 24 hours.
func TestMissLimiter_TheDayBoundsAPatientGuesser(t *testing.T) {
	clk := start()
	l := &MissLimiter{Now: clk.now}
	const c = "198.51.100.1"
	misses := 0
	for misses < 200 {
		for i := 0; i < MissBurst/2-1; i++ {
			misses++
			if l.Miss(c) {
				goto locked
			}
		}
		clk.t = clk.t.Add(MissBurstWindow + time.Second)
	}
	t.Fatal("200 misses spread under the burst: never locked")
locked:
	if misses != MissDaily {
		t.Fatalf("locked at miss %d, want %d", misses, MissDaily)
	}
	clk.t = clk.t.Add(MissDailyLock - time.Minute)
	if !l.Locked(c) {
		t.Fatal("day lock lifted early")
	}
	clk.t = clk.t.Add(2 * time.Minute)
	if l.Locked(c) {
		t.Fatal("day lock did not lift")
	}
}

func TestClientKey_IPv6Slash64(t *testing.T) {
	if a, b := ClientKey("2001:db8:1:2::1"), ClientKey("2001:db8:1:2:ffff::9"); a != b {
		t.Fatalf("one /64, two keys: %q %q", a, b)
	}
	if a, b := ClientKey("2001:db8:1:2::1"), ClientKey("2001:db8:1:3::1"); a == b {
		t.Fatal("two /64s, one key")
	}
	if got := ClientKey("::ffff:203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("mapped v4: %q", got)
	}
}

func TestMissLimiter_AlarmOncePerHour(t *testing.T) {
	clk := start()
	alarms := 0
	l := &MissLimiter{Now: clk.now, Alarm: func(int) { alarms++ }}
	for i := 0; i < AlarmPerHour*2; i++ {
		l.Miss("")
	}
	if alarms != 1 {
		t.Fatalf("%d alarms in one hour, want 1", alarms)
	}
	clk.t = clk.t.Add(time.Hour + time.Second)
	for i := 0; i <= AlarmPerHour; i++ {
		l.Miss("")
	}
	if alarms != 2 {
		t.Fatalf("%d alarms after the next hour, want 2", alarms)
	}
}

func TestRateLimiter(t *testing.T) {
	clk := start()
	l := &RateLimiter{Limit: HitLimit, Window: HitWindow, Now: clk.now}
	for i := 0; i < HitLimit; i++ {
		if !l.Allow("a") {
			t.Fatalf("refused at %d", i+1)
		}
	}
	if l.Allow("a") {
		t.Fatal("past the limit allowed")
	}
	if !l.Allow("b") {
		t.Fatal("another client refused")
	}
	// Counted over this minute and the last: the next minute still holds
	// it, the one after lets it in.
	clk.t = clk.t.Add(HitWindow)
	if l.Allow("a") {
		t.Fatal("allowed in the next minute, with the last one's count spent")
	}
	clk.t = clk.t.Add(HitWindow)
	if !l.Allow("a") {
		t.Fatal("not allowed two minutes on")
	}
}

// The kit's guessing arithmetic is built on this limiter's numbers.
func TestMissesPerDay_TheKitAgrees(t *testing.T) {
	if shortcode.MissesPerDay != MissesPerDay {
		t.Fatalf("pkg/pluginkit/shortcode says %d misses a day, the limiter allows %d", shortcode.MissesPerDay, MissesPerDay)
	}
}

// A flood of fresh addresses costs O(1) a miss and bounded memory: the maps
// stop at MaxClients and turn over with the clock.
func TestMissLimiter_AFloodOfAddressesStaysBounded(t *testing.T) {
	clk := start()
	l := &MissLimiter{Now: clk.now}
	d := &Dedupe{Now: clk.now}
	r := &RateLimiter{Limit: HitLimit, Window: HitWindow, Now: clk.now}
	began := time.Now()
	for i := 0; i < MaxClients+50000; i++ {
		c := ClientKey("10." + itoa3(i))
		l.Miss(c)
		d.First(1, c)
		r.Allow(c)
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("%d fresh addresses took %v: not O(1) a miss", MaxClients+50000, took)
	}
	if n, _ := l.daily.Clients(); n > MaxClients {
		t.Fatalf("daily map holds %d clients, the cap is %d", n, MaxClients)
	}
	if n := len(d.cur); n > MaxClients {
		t.Fatalf("dedupe holds %d, the cap is %d", n, MaxClients)
	}
	// Two days on, both spans are new.
	clk.t = clk.t.Add(2 * MissDailyWindow)
	l.Miss("203.0.113.1")
	if cur, prev := l.daily.Clients(); cur+prev != 1 {
		t.Fatalf("two days later: %d clients", cur+prev)
	}
}

func itoa3(i int) string {
	return strconv.Itoa((i>>16)&255) + "." + strconv.Itoa((i>>8)&255) + "." + strconv.Itoa(i&255)
}

// A boundary of the clock does not hand a guesser a fresh count: misses just
// before it count with the ones just after.
func TestMissLimiter_ABoundaryIsNoReset(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 11, 10, 9, 59, 0, time.UTC)}
	l := &MissLimiter{Now: clk.now}
	const c = "192.0.2.9"
	for i := 0; i < MissBurst-1; i++ {
		l.Miss(c)
	}
	clk.t = clk.t.Add(2 * time.Second) // 10:10:01, the next window
	if !l.Miss(c) {
		t.Fatal("the 20th miss, one second over a boundary, did not lock")
	}
}
