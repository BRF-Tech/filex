package clientip

import (
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type logSink struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *logSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *logSink) count(sub string) int { return strings.Count(s.text(), sub) }

// captureLogs sends the default logger into a buffer for the test.
func captureLogs(t *testing.T) *logSink {
	t.Helper()
	s := &logSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(s, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return s
}

// forwarderClock pins the tracker's clock for a test.
func forwarderClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	now := start
	prev := fwdNow
	fwdNow = func() time.Time { return now }
	t.Cleanup(func() { fwdNow = prev })
	ResetForwarders()
	t.Cleanup(ResetForwarders)
	return &now
}

// fixedAuto installs a loopback-only `auto` (a plain install) as the default.
func fixedAuto(t *testing.T) {
	t.Helper()
	t.Cleanup(SetSource(nil))
	t.Cleanup(SetAutoResolver(func() AutoResolution { return AutoResolution{Environment: EnvPlain} }))
}

func observe(remote string, hdr map[string]string) { Observe(req(remote, hdr)) }

// A proxy on the host or the LAN that is not trusted is named: once in the
// log, and in the list the Sign-in security page reads.
func TestAnUntrustedForwarderIsRememberedAndLoggedOnce(t *testing.T) {
	fixedAuto(t)
	now := forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	logs := captureLogs(t)

	for i := 0; i < 3; i++ {
		observe("172.26.0.1:3700"+fmt.Sprint(i), map[string]string{"X-Forwarded-For": "198.51.100.7"})
		*now = now.Add(time.Second)
	}
	observe("192.168.1.107:5000", map[string]string{"X-Real-IP": "198.51.100.8"})

	got := UntrustedForwarders()
	require.Len(t, got, 2)
	require.Equal(t, "172.26.0.1", got[0].Address, "the busiest first")
	require.EqualValues(t, 3, got[0].Count)
	require.Equal(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), got[0].FirstSeen)
	require.Equal(t, time.Date(2026, 10, 1, 9, 0, 2, 0, time.UTC), got[0].LastSeen)
	require.Equal(t, "192.168.1.107", got[1].Address)
	require.EqualValues(t, 1, got[1].Count)

	require.Equal(t, 1, logs.count("peer=172.26.0.1"), "once per peer, not once per request")
	require.Equal(t, 1, logs.count("peer=192.168.1.107 header=X-Real-IP"))
	require.NotContains(t, logs.text(), "198.51.100", "what the peer wrote is never kept or logged")
}

// Nothing to name: a trusted proxy, a request with no forwarded header, a
// header with nothing in it.
func TestOnlyUntrustedPeersWithAHeaderCount(t *testing.T) {
	fixedAuto(t)
	forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	observe("127.0.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	observe("[::1]:1", map[string]string{"X-Real-IP": "198.51.100.7"})
	observe("203.0.113.9:1", nil)
	observe("203.0.113.9:1", map[string]string{"X-Forwarded-For": " , "})
	observe("not an address", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Empty(t, UntrustedForwarders())

	// Any untrusted peer counts - loopback too, when the list trusts nobody.
	none, err := ParseList("none")
	require.NoError(t, err)
	restore := SetSource(func() *Set { return none })
	observe("127.0.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Equal(t, "127.0.0.1", UntrustedForwarders()[0].Address)
	restore()
}

// Trusting it (the page's one-click add) takes it off the list.
func TestATrustedForwarderLeavesTheList(t *testing.T) {
	fixedAuto(t)
	forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	observe("172.26.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Len(t, UntrustedForwarders(), 1)

	set, err := ParseList("auto, 172.26.0.1")
	require.NoError(t, err)
	restore := SetSource(func() *Set { return set })
	defer restore()
	require.Empty(t, UntrustedForwarders())
	observe("172.26.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Empty(t, UntrustedForwarders(), "trusted now: its header is believed and it is not noted")
	require.Equal(t, "198.51.100.7", FromRequest(req("172.26.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})))
}

// A flood of spoofed headers from the internet: the memory holds at most
// MaxForwarders peers (the one seen longest ago goes), and the log names a
// handful a minute, counting the rest into the next line it writes.
func TestTheTrackerIsBoundedAgainstAFlood(t *testing.T) {
	fixedAuto(t)
	now := forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	logs := captureLogs(t)

	// The real proxy first, seen again throughout.
	observe("192.0.2.10:1", map[string]string{"X-Forwarded-For": "198.51.100.1"})
	for i := 0; i < 1000; i++ {
		*now = now.Add(time.Millisecond)
		a := netip.AddrFrom4([4]byte{203, 0, byte(i / 250), byte(i%250 + 1)})
		observe(a.String()+":1", map[string]string{"X-Forwarded-For": "10.0.0.1"})
		if i%100 == 0 {
			observe("192.0.2.10:1", map[string]string{"X-Forwarded-For": "198.51.100.1"})
		}
	}
	got := UntrustedForwarders()
	require.Len(t, got, MaxForwarders)
	require.Equal(t, "192.0.2.10", got[0].Address, "the busy proxy survives the flood (the least recently seen go first)")
	require.EqualValues(t, 11, got[0].Count)
	require.Equal(t, forwarderLogBurst, strings.Count(logs.text(), "a peer that is not a trusted proxy"), "a few lines a minute")

	// The next minute a new peer is named, with how many went unnamed.
	*now = now.Add(time.Minute)
	observe("198.51.100.200:1", map[string]string{"X-Forwarded-For": "10.0.0.1"})
	require.Equal(t, forwarderLogBurst+1, strings.Count(logs.text(), "a peer that is not a trusted proxy"))
	require.Contains(t, logs.text(), fmt.Sprintf("not_named_before=%d", 1001-forwarderLogBurst))
}

// A peer that stopped sending is forgotten after a day.
func TestAQuietForwarderIsForgotten(t *testing.T) {
	fixedAuto(t)
	now := forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	observe("192.0.2.10:1", map[string]string{"X-Forwarded-For": "198.51.100.1"})
	*now = now.Add(23 * time.Hour)
	require.Len(t, UntrustedForwarders(), 1)
	*now = now.Add(2 * time.Hour)
	require.Empty(t, UntrustedForwarders())
}

// FromRequest is asked many times per request and notes nothing; Observe is
// the one call per request (the access-log middleware).
func TestFromRequestDoesNotCount(t *testing.T) {
	fixedAuto(t)
	forwarderClock(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	r := req("203.0.113.9:1", map[string]string{"X-Forwarded-For": "198.51.100.1"})
	for i := 0; i < 3; i++ {
		require.Equal(t, "203.0.113.9", FromRequest(r))
	}
	require.Empty(t, UntrustedForwarders())
	Observe(r)
	require.EqualValues(t, 1, UntrustedForwarders()[0].Count)
}
