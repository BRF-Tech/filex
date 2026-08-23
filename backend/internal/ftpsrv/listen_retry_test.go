package ftpsrv

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// ⚠ The incident: on the v0.25.0 rollout of fm.example.com, Docker's embedded DNS
// timed out ONCE, four seconds after the container started. passiveAddress
// failed, the library's Listen failed, and FTPS stayed down for the life of
// the container — with the host port still published (so `ss` showed a
// listener), /healthz green, and the only trace one ERROR line in a log
// nobody reads at boot. A transient lookup failure at start-up must be
// retried, not treated as a verdict.
func TestListenRetriesWhileThePublicHostDoesNotResolve(t *testing.T) {
	var calls atomic.Int32
	restoreLookup := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		if calls.Add(1) < 3 {
			return nil, errors.New("lookup timed out")
		}
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	}
	restoreRetry := listenRetry
	listenRetry = 5 * time.Millisecond
	defer func() { lookupIP = restoreLookup; listenRetry = restoreRetry }()

	d := &driver{srv: &Server{cfg: Config{PublicHost: "fm.example.test"}}}
	host, err := d.passiveHostWithRetry()
	if err != nil {
		t.Fatalf("two transient failures then success must come back as success, got %v", err)
	}
	if host != "203.0.113.7" {
		t.Fatalf("resolved host = %q", host)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("lookup called %d times, want 3 (two failures, one success)", n)
	}
}

// A name that never resolves still fails — loudly, with the name in the
// message — rather than retrying forever: the operator typed it wrong.
func TestListenGivesUpOnAHostThatNeverResolves(t *testing.T) {
	restoreLookup := lookupIP
	lookupIP = func(string) ([]net.IP, error) { return nil, errors.New("no such host") }
	restoreRetry, restoreAttempts := listenRetry, listenAttempts
	listenRetry, listenAttempts = time.Millisecond, 3
	defer func() { lookupIP = restoreLookup; listenRetry, listenAttempts = restoreRetry, restoreAttempts }()

	d := &driver{srv: &Server{cfg: Config{PublicHost: "typo.example.test"}}}
	if _, err := d.passiveHostWithRetry(); err == nil {
		t.Fatal("a host that never resolves must eventually fail")
	}
}
