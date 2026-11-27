package handlers

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/clientip"
)

// oauthLimiter bounds what a caller may start without signing in - a client
// registration, an authorization request - per network: a bucket of
// oauthBurst that fills by one every oauthRefill. A client that connects
// registers once and authorizes once; a script that registers in a loop gets
// oauthBurst and then one a minute, which, against an unused registration's
// hour of life, holds about eighty of them live from one network.
//
// The network is the caller's address (clientip, which trusts a forwarded
// one only from a trusted proxy), and for IPv6 its /64: one machine is
// handed a whole /64 and would otherwise have a bucket per address.
//
// In memory and per process, like the sign-in guard: the durable ceiling is
// the store's (db.MCPOAuthMaxAnonymous), which holds across replicas.
type oauthLimiter struct {
	mu      sync.Mutex
	buckets map[string]oauthBucket
}

type oauthBucket struct {
	tokens float64
	at     time.Time
}

const (
	oauthBurst      = 20
	oauthRefill     = time.Minute
	oauthMaxBuckets = 4096
)

func oauthNetwork(r *http.Request) string {
	raw := clientip.FromRequest(r)
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if ip.To4() != nil {
		return ip.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func (l *oauthLimiter) allow(r *http.Request) bool {
	now := time.Now()
	key := oauthNetwork(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]oauthBucket{}
	}
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= oauthMaxBuckets {
			// Forget the networks whose bucket has filled again: they are
			// where an absent one would start.
			for k, v := range l.buckets {
				if v.tokens+now.Sub(v.at).Minutes()*float64(time.Minute/oauthRefill) >= oauthBurst {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= oauthMaxBuckets {
				return false
			}
		}
		b = oauthBucket{tokens: oauthBurst, at: now}
	}
	b.tokens += now.Sub(b.at).Minutes() * float64(time.Minute/oauthRefill)
	if b.tokens > oauthBurst {
		b.tokens = oauthBurst
	}
	b.at = now
	if b.tokens < 1 {
		l.buckets[key] = b
		return false
	}
	b.tokens--
	l.buckets[key] = b
	return true
}
