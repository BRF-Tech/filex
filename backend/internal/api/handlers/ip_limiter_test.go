package handlers

import (
	"strconv"
	"testing"
	"time"
)

// floodAddr is the i-th address of a flood: 10.x.y.z, a fresh one each time.
func floodAddr(i int) string {
	return "10." + strconv.Itoa((i>>16)&255) + "." + strconv.Itoa((i>>8)&255) + "." + strconv.Itoa(i&255)
}

// A flood of fresh addresses costs O(1) an address under the lock: the time
// grows with the flood, not with its square (#325). Before 0.60.1 every new
// client past 4,096 swept the whole map, so 200,000 fresh addresses at a file
// request's door ran for minutes - the limiter itself was the way to take the
// server down, as the short-link limiter's first flood measurement found
// (2026-11-11). The loop stops as soon as it is over its time, rather than
// running out the old square.
func TestIPLimiter_AFloodOfFreshAddressesStaysLinear(t *testing.T) {
	const (
		flood  = 200000
		batch  = 20000
		budget = 20 * time.Second
	)
	l := newIPLimiter(40, time.Hour) // the file request's own limit
	began := time.Now()
	mark := began
	var first time.Duration
	for i := 0; i < flood; i++ {
		if !l.allow(floodAddr(i)) {
			t.Fatalf("address %d was refused its first request", i)
		}
		if (i+1)%1000 == 0 {
			if took := time.Since(began); took > budget {
				t.Fatalf("%d of %d fresh addresses took %v: not O(1) an address", i+1, flood, took)
			}
		}
		switch i + 1 {
		case batch:
			first = time.Since(mark)
		case flood - batch:
			mark = time.Now()
		}
	}
	last := time.Since(mark)
	t.Logf("%d fresh addresses in %v; the first %d took %v, the last %d %v", flood, time.Since(began), batch, first, batch, last)
	// Linear: the last addresses, with the map near its fullest, cost about
	// what the first ones did. A sweep makes the last batch thousands of
	// times dearer; the floor keeps a scheduler hiccup from failing it.
	if last > 10*first && last > time.Second {
		t.Fatalf("the last %d addresses took %v, the first %d %v: the cost of one grows with the crowd", batch, last, batch, first)
	}
}

type limiterClock struct{ t time.Time }

func (c *limiterClock) now() time.Time { return c.t }

// The count is this window and the one before: a boundary of the clock is no
// fresh start, the next window still holds a client that reached its limit,
// and the one after lets it back.
func TestIPLimiter_CountsThisWindowAndTheOneBefore(t *testing.T) {
	clk := &limiterClock{t: time.Date(2026, 11, 12, 10, 59, 59, 0, time.UTC)}
	l := newIPLimiter(3, time.Hour)
	l.now = clk.now
	const ip = "192.0.2.7"
	for i := 0; i < 2; i++ {
		if !l.allow(ip) {
			t.Fatalf("request %d refused", i+1)
		}
	}
	clk.t = clk.t.Add(2 * time.Second) // 11:00:01, the next window
	if !l.allow(ip) {
		t.Fatal("the third request, over the boundary, refused")
	}
	if l.allow(ip) {
		t.Fatal("the fourth request, one second over a boundary, allowed: the boundary reset the count")
	}
	if l.allowN(ip, 1) || !l.allowN("192.0.2.8", 3) {
		t.Fatal("the limit is the client's own")
	}
	clk.t = clk.t.Add(time.Hour) // 12:00:01: the 11 o'clock request still counts
	if !l.allowN(ip, 2) || l.allow(ip) {
		t.Fatal("an hour on, the window before still holds its one request: 2 more fit, a third does not")
	}
	clk.t = clk.t.Add(2 * time.Hour)
	if !l.allowN(ip, 3) {
		t.Fatal("two windows on, the client is back to its whole limit")
	}
}

// A map that is full lets a new client through uncounted - its memory does
// not grow - and still holds the clients it counts to their limit.
func TestIPLimiter_AFullMapLetsANewClientThroughUncounted(t *testing.T) {
	l := newIPLimiter(2, time.Hour)
	l.w.Max = 100
	for i := 0; i < 100; i++ {
		l.allow(floodAddr(i))
	}
	for i := 0; i < 5; i++ {
		if !l.allow("203.0.113.9") {
			t.Fatalf("a client the full map cannot hold was refused at %d", i+1)
		}
	}
	if cur, _ := l.w.Clients(); cur != 100 {
		t.Fatalf("the map holds %d clients, its cap is 100", cur)
	}
	if !l.allow(floodAddr(0)) || l.allow(floodAddr(0)) {
		t.Fatal("a client in the full map is still held to its limit")
	}
}

// After a flood the map holds no more than its cap, and two windows on it
// holds the newcomers alone: the clock ends the flood's counts, not a sweep.
func TestIPLimiter_AFloodEndsWithTheClock(t *testing.T) {
	clk := &limiterClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	l := newIPLimiter(10, time.Minute)
	l.now = clk.now
	l.w.Max = 5000
	for i := 0; i < 8000; i++ {
		l.allow(floodAddr(i))
	}
	if cur, _ := l.w.Clients(); cur != 5000 {
		t.Fatalf("the map holds %d clients, its cap is 5000", cur)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	l.allow("198.51.100.1")
	if cur, prev := l.w.Clients(); cur+prev != 1 {
		t.Fatalf("two windows later the maps hold %d clients", cur+prev)
	}
}

// v6In is the i-th address of a /64: a fresh one each time, all in prefix.
func v6In(prefix string, i int) string {
	return prefix + ":" + strconv.FormatInt(int64(i>>16), 16) + ":" + strconv.FormatInt(int64(i&0xffff), 16) + "::1"
}

// An IPv6 client is its /64, as the short-link limiter counts it
// (shortlink.ClientKey): one subscriber holds a whole /64, so 200,000
// addresses from it are one client held to one limit, not 200,000 fresh
// ones that fill the map and leave every newcomer uncounted (#335). Another
// /64 is another client, and an IPv4 address is still its own.
func TestIPLimiter_AnIPv6ClientIsItsSlash64(t *testing.T) {
	l := newIPLimiter(40, time.Hour)
	allowed := 0
	for i := 0; i < 200000; i++ {
		if l.allow(v6In("2001:db8:1:2", i)) {
			allowed++
		}
	}
	if allowed != 40 {
		t.Fatalf("200,000 addresses of one /64 made %d requests; the /64 is one client with a limit of 40", allowed)
	}
	if cur, _ := l.w.Clients(); cur != 1 {
		t.Fatalf("one /64 holds %d places in the map, want 1", cur)
	}
	if !l.allow("2001:db8:1:3::1") {
		t.Fatal("the next /64 is another client, refused with the first one's count")
	}
	if !l.allow("192.0.2.1") || !l.allow("192.0.2.2") {
		t.Fatal("two IPv4 addresses are two clients")
	}
	if !l.allow("::ffff:192.0.2.3") || l.w.Count(time.Now(), "192.0.2.3") != 1 {
		t.Fatal("an IPv4-mapped address counts as its IPv4 address")
	}
	if cur, _ := l.w.Clients(); cur != 5 {
		t.Fatalf("the map holds %d clients, want 5: one /64, the next /64, three IPv4 addresses", cur)
	}
}
