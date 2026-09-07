package thumb

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memSettings is a settings row store. Nothing about the signature depends on
// SQL, so a map is the honest fixture.
type memSettings struct {
	mu      sync.Mutex
	rows    map[string]string
	getErr  error
	putErr  error
	getHits int
}

func newMemSettings() *memSettings { return &memSettings{rows: map[string]string{}} }

func (m *memSettings) GetSetting(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getHits++
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.rows[k], nil
}

func (m *memSettings) UpsertSetting(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	m.rows[k] = v
	return nil
}

func (m *memSettings) get(k string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[k]
}

func parseQuery(t *testing.T, q string) (int64, string) {
	t.Helper()
	var exp, sig string
	for _, p := range strings.Split(q, "&") {
		switch {
		case strings.HasPrefix(p, "exp="):
			exp = strings.TrimPrefix(p, "exp=")
		case strings.HasPrefix(p, "sig="):
			sig = strings.TrimPrefix(p, "sig=")
		}
	}
	require.NotEmpty(t, exp, "no exp in %q", q)
	require.NotEmpty(t, sig, "no sig in %q", q)
	n, err := strconv.ParseInt(exp, 10, 64)
	require.NoError(t, err)
	return n, sig
}

// TestSigner_SeedsItsKeyOnce — the whole scheme was inert in production
// because nothing ever wrote settings.thumb_signing_key and the verifier read
// an empty key as "let everything through".
func TestSigner_SeedsItsKeyOnce(t *testing.T) {
	m := newMemSettings()
	s := NewSigner(m, time.Hour)

	require.Empty(t, m.get(SigningKeySetting))
	q := s.Query(7)
	require.NotEmpty(t, q)

	key := m.get(SigningKeySetting)
	require.Len(t, key, 64, "32 random bytes, hex")

	// Cached: a second mint must not go back to the store.
	before := m.getHits
	_ = s.Query(8)
	assert.Equal(t, before, m.getHits, "the key is read once per process")
	assert.Equal(t, key, m.get(SigningKeySetting), "and never re-rolled")
}

// TestSigner_RoundTrip — the signature it mints is the one it accepts.
func TestSigner_RoundTrip(t *testing.T) {
	s := NewSigner(newMemSettings(), time.Hour)
	exp, sig := parseQuery(t, s.Query(42))
	assert.True(t, s.Verify(42, strconv.FormatInt(exp, 10), sig))
}

// TestSigner_IsBoundToTheNode — a stamp is a capability for ONE preview, not
// for the endpoint.
func TestSigner_IsBoundToTheNode(t *testing.T) {
	s := NewSigner(newMemSettings(), time.Hour)
	exp, sig := parseQuery(t, s.Query(42))
	assert.False(t, s.Verify(43, strconv.FormatInt(exp, 10), sig),
		"a stamp for node 42 must not open node 43")
}

// TestSigner_ExpiryIsCovered — moving the expiry forward must not extend the
// stamp, which means the HMAC has to cover it.
func TestSigner_ExpiryIsCovered(t *testing.T) {
	s := NewSigner(newMemSettings(), time.Hour)
	exp, sig := parseQuery(t, s.Query(42))

	assert.False(t, s.Verify(42, strconv.FormatInt(exp+3600, 10), sig),
		"rewriting exp must invalidate the signature")
	assert.False(t, s.Verify(42, strconv.FormatInt(time.Now().Unix()-1, 10), sig),
		"an expiry in the past is refused before the HMAC is even considered")
}

// TestSigner_RefusesJunk — the pre-fix build accepted `?sig=deadbeef`.
func TestSigner_RefusesJunk(t *testing.T) {
	s := NewSigner(newMemSettings(), time.Hour)
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)

	assert.False(t, s.Verify(42, future, "deadbeef"))
	assert.False(t, s.Verify(42, future, ""))
	assert.False(t, s.Verify(42, "", "deadbeef"))
	assert.False(t, s.Verify(42, "not-a-number", "deadbeef"))
}

// TestSigner_NilIsFailClosed — an install that never wired a signer must
// require an authenticated caller, not accept everything.
func TestSigner_NilIsFailClosed(t *testing.T) {
	var s *Signer
	assert.Empty(t, s.Query(1))
	assert.False(t, s.Verify(1, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10), "anything"))
	assert.Zero(t, s.TTL())
}

// TestSigner_URLIsStableWithinABucket.
//
// ⚠ The URL string is the cache key for both the browser's HTTP cache and
// useThumbs' object-URL cache. An expiry of `now + ttl` would change on every
// listing, so every folder refresh would re-download every thumbnail — a
// silent performance regression that no correctness test would catch.
func TestSigner_URLIsStableWithinABucket(t *testing.T) {
	s := NewSigner(newMemSettings(), 24*time.Hour)
	first := s.Query(42)
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, first, s.Query(42), "two mints inside one bucket must be byte-identical")

	exp, _ := parseQuery(t, first)
	assert.Equal(t, int64(0), exp%3600, "the expiry is quantized to the hour")
	assert.Greater(t, exp, time.Now().Add(24*time.Hour).Unix()-1,
		"quantizing must round UP; rounding down would shorten every stamp")
}

// TestSigner_ReloadsAKeyChangedUnderIt — another replica seeding the row, or
// an operator rotating it, must not make this process reject live signatures.
func TestSigner_ReloadsAKeyChangedUnderIt(t *testing.T) {
	m := newMemSettings()
	minter := NewSigner(m, time.Hour)
	exp, sig := parseQuery(t, minter.Query(42))

	// A second process that cached a DIFFERENT key first.
	verifier := NewSigner(m, time.Hour)
	verifier.key = []byte("a-stale-key-from-before-the-rotation")

	assert.True(t, verifier.Verify(42, strconv.FormatInt(exp, 10), sig),
		"a mismatch must reload the row once before refusing")
}

// TestSigner_UnusableStoreDegradesToAuthOnly — no key, no stamp, and the
// caller falls back to authenticated fetching. It must never mint a signature
// it cannot verify, nor verify one it never minted.
func TestSigner_UnusableStoreDegradesToAuthOnly(t *testing.T) {
	m := newMemSettings()
	m.getErr = errors.New("database is gone")
	m.putErr = errors.New("database is gone")
	s := NewSigner(m, time.Hour)

	// Query still produces a stamp from an in-memory key (the row could not be
	// written), and this process can verify its own — which is the useful
	// degradation. What must NOT happen is accepting an unsigned request.
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	assert.False(t, s.Verify(42, future, "deadbeef"))
	assert.False(t, s.Verify(42, future, ""))
}

// TestSigner_DefaultTTL — 0 means the default, never "no expiry".
func TestSigner_DefaultTTL(t *testing.T) {
	assert.Equal(t, DefaultURLTTL, NewSigner(newMemSettings(), 0).TTL())
	assert.Equal(t, DefaultURLTTL, NewSigner(newMemSettings(), -time.Hour).TTL())
}
