package filesync

import (
	"strings"
	"testing"
	"time"
)

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{
		0:                 "0 B",
		900:               "900 B",
		1024:              "1.0 KiB",
		6000:              "5.9 KiB",
		52_600_000_000:    "49.0 GiB",
		5 * (1 << 40) / 2: "2.5 TiB",
	} {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestEtaText(t *testing.T) {
	if got := etaText(0, 100, time.Minute); got != "" {
		t.Errorf("nothing done yet must give no estimate, got %q", got)
	}
	if got := etaText(50, 100, 2*time.Second); got != "" {
		t.Errorf("two seconds is too early to guess, got %q", got)
	}
	if got := etaText(100, 100, time.Minute); got != "" {
		t.Errorf("finished needs no estimate, got %q", got)
	}
	// A quarter done in 10 minutes: about 30 minutes left.
	if got := etaText(25, 100, 10*time.Minute); got != "about 30m left" {
		t.Errorf("got %q", got)
	}
	// 1 GiB of 52 GiB in 10 minutes: hours.
	if got := etaText(1<<30, 52<<30, 10*time.Minute); got != "about 8h 30m left" {
		t.Errorf("got %q", got)
	}
	if got := etaText(90, 100, 90*time.Second); got != "about 10s left" {
		t.Errorf("got %q", got)
	}
	// Just under an hour rounds UP to it: "about 60m left" is not how the
	// line talks — and the desktop turned that into "60 dk".
	if got := etaText(1000, 60000, 1*time.Minute); got != "about 59m left" {
		t.Errorf("got %q", got)
	}
	if got := etaText(1000, 60500, 1*time.Minute); got != "about 1h 0m left" {
		t.Errorf("59m30s must read as an hour, got %q", got)
	}

}

// The desktop app shows the last progress line; a first sync of 52 GiB used
// to say only "transfer: 120/11704", which says nothing about the nine hours.
func TestTransferProgressCarriesBytes(t *testing.T) {
	r := newRig(t)
	r.writeRemote("a.txt", strings.Repeat("a", 1000))
	r.writeRemote("b.txt", strings.Repeat("b", 2000))
	r.writeLocal("c.txt", strings.Repeat("c", 3000))
	var lines []string
	r.engine.Progress = func(s string) { lines = append(lines, s) }

	r.run()

	last := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "transfer:") {
			last = l
		}
	}
	if want := "transfer: 3/3 (5.9 KiB of 5.9 KiB)"; last != want {
		t.Fatalf("last transfer line = %q, want %q (all lines: %q)", last, want, lines)
	}
}

// The watcher compares a fresh local walk with what the last run reported;
// the two must agree when nothing changed, and differ when something did.
func TestLocalFingerprintMatchesTheRunsOwn(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a/b.txt", "x")
	r.writeRemote("c.txt", "y")
	res := r.run()

	fp, err := LocalFingerprint(r.engine.Pair)
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" || fp != res.LocalFingerprint {
		t.Fatalf("fresh %q vs run %q", fp, res.LocalFingerprint)
	}
	r.writeLocal("a/b.txt", "edited")
	if fp2, _ := LocalFingerprint(r.engine.Pair); fp2 == fp {
		t.Fatal("an edit must change the fingerprint")
	}
}
