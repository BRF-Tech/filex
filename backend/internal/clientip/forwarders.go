package clientip

// forwarders.go - peers that send X-Forwarded-For / X-Real-IP without being
// trusted.
//
// Their header is ignored (the socket is the truth), which is right for a
// stranger and wrong for the operator's own proxy: behind an untrusted proxy
// every visitor is counted as the proxy - one per-address counter, one
// address in the audit trail. Since the default became `auto` a proxy on the
// host (through a published port) or on another machine is exactly that, so
// such a peer is remembered and named: once in the log, and on the Sign-in
// security page with a one-click "trust it".
//
// Bounded against the internet: at most MaxForwarders peers are held (the one
// seen longest ago goes first), a peer quiet for forwarderTTL is dropped, only
// the ADDRESS of the peer is kept (never a header's value, which the sender
// writes), and the log says at most forwarderLogBurst lines a minute however
// many new peers turn up (the rest are counted and summed up in the next line
// that is allowed).

import (
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// MaxForwarders is how many untrusted forwarders are remembered.
	MaxForwarders = 256
	// forwarderTTL is how long a peer that stopped sending is remembered.
	forwarderTTL = 24 * time.Hour
	// forwarderLogBurst is how many peers a minute are named in the log.
	forwarderLogBurst = 5
)

// Forwarder is one peer that sent a forwarded address without being trusted.
type Forwarder struct {
	Address   string    `json:"address"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Count is how many requests carried the header from it.
	Count int64 `json:"count"`
}

type fwdEntry struct {
	first, last time.Time
	count       int64
}

var fwd = struct {
	mu        sync.Mutex
	peers     map[netip.Addr]*fwdEntry
	logWindow time.Time // the minute the log budget counts in
	logged    int       // lines written in that minute
	muted     int       // new peers not named since the last line
}{}

// fwdNow is the clock (tests move it).
var fwdNow = time.Now

// logForwarder names a new untrusted forwarder in the log (tests watch it).
var logForwarder = func(peer netip.Addr, header string, muted int) {
	attrs := []any{slog.String("peer", peer.String()), slog.String("header", header)}
	if muted > 0 {
		attrs = append(attrs, slog.Int("not_named_before", muted))
	}
	slog.Warn("clientip: a peer that is not a trusted proxy sent a forwarded client address; it is ignored and every request from it counts as the peer's own - if it is your reverse proxy, trust it on the Sign-in security page or in FILEX_TRUSTED_PROXIES", attrs...)
}

// Observe notes a request whose peer sent X-Forwarded-For or X-Real-IP
// without being a trusted proxy. The access-log middleware calls it once per
// request; FromRequest (asked many times per request) does not.
func Observe(r *http.Request) {
	header := ""
	switch {
	case hasValue(r.Header.Values("X-Forwarded-For")):
		header = "X-Forwarded-For"
	case strings.TrimSpace(r.Header.Get("X-Real-IP")) != "":
		header = "X-Real-IP"
	default:
		return
	}
	peer, ok := parseHostPort(r.RemoteAddr)
	if !ok || trusted().Contains(peer) {
		return
	}
	noteForwarder(peer, header)
}

func hasValue(vs []string) bool {
	for _, v := range vs {
		if strings.Trim(v, " \t,") != "" {
			return true
		}
	}
	return false
}

func noteForwarder(peer netip.Addr, header string) {
	now := fwdNow().UTC()
	fwd.mu.Lock()
	if fwd.peers == nil {
		fwd.peers = map[netip.Addr]*fwdEntry{}
	}
	if e := fwd.peers[peer]; e != nil {
		e.last = now
		e.count++
		fwd.mu.Unlock()
		return
	}
	if len(fwd.peers) >= MaxForwarders {
		dropStaleLocked(now)
	}
	if len(fwd.peers) >= MaxForwarders {
		var oldest netip.Addr
		var at time.Time
		for a, e := range fwd.peers {
			if !oldest.IsValid() || e.last.Before(at) {
				oldest, at = a, e.last
			}
		}
		delete(fwd.peers, oldest)
	}
	fwd.peers[peer] = &fwdEntry{first: now, last: now, count: 1}
	// The log: a few new peers a minute, the rest counted.
	if now.Sub(fwd.logWindow) >= time.Minute {
		fwd.logWindow, fwd.logged = now, 0
	}
	say, muted := fwd.logged < forwarderLogBurst, 0
	if say {
		fwd.logged++
		muted, fwd.muted = fwd.muted, 0
	} else {
		fwd.muted++
	}
	fwd.mu.Unlock()
	if say {
		logForwarder(peer, header, muted)
	}
}

// dropStaleLocked forgets the peers quiet for forwarderTTL. Caller holds fwd.mu.
func dropStaleLocked(now time.Time) {
	for a, e := range fwd.peers {
		if now.Sub(e.last) > forwarderTTL {
			delete(fwd.peers, a)
		}
	}
}

// UntrustedForwarders lists the peers remembered, the busiest first (then the
// most recent). A peer that has since been trusted, or that has been quiet
// for a day, is forgotten here.
func UntrustedForwarders() []Forwarder {
	set := trusted()
	now := fwdNow().UTC()
	fwd.mu.Lock()
	dropStaleLocked(now)
	out := make([]Forwarder, 0, len(fwd.peers))
	for a, e := range fwd.peers {
		if set.Contains(a) {
			delete(fwd.peers, a)
			continue
		}
		out = append(out, Forwarder{Address: a.String(), FirstSeen: e.first, LastSeen: e.last, Count: e.count})
	}
	fwd.mu.Unlock()
	slices.SortFunc(out, func(x, y Forwarder) int {
		switch {
		case x.Count != y.Count:
			if x.Count > y.Count {
				return -1
			}
			return 1
		case !x.LastSeen.Equal(y.LastSeen):
			return y.LastSeen.Compare(x.LastSeen)
		}
		return strings.Compare(x.Address, y.Address)
	})
	return out
}

// ResetForwarders forgets every peer and the log budget (tests).
func ResetForwarders() {
	fwd.mu.Lock()
	defer fwd.mu.Unlock()
	fwd.peers = nil
	fwd.logWindow, fwd.logged, fwd.muted = time.Time{}, 0, 0
}
