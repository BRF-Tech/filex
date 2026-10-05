package appstore_test

// The second round of the store review (Y2, Y6, and the per-store proof
// inside the one-day window), kept as regression tests. Lesson #1070.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
)

// #5 reopened? The administrator holds the wall clock at t0, kills filex
// (not a clean shutdown) every 30 minutes, and answers the license check with
// the store's LAST genuine signed answer (checked_at t0) - a replay, e.g.
// through a TLS proxy whose CA the administrator put in filex's trust
// (SSL_CERT_FILE). The fake store returning the same answer every time is
// exactly that replay at the protocol level. The restart allowance (debt) is
// taken back by every answer that is not newer than the proof.
func TestLicense_AReplayedAnswerTakesNoRestartAllowanceBack(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	const cycles = 2 * 24 * 20
	for i := 0; i < cycles; i++ {
		f.restart()
		f.clock.mu.Lock()
		f.clock.mono += time.Minute
		f.clock.mu.Unlock()
		f.svc.Tick(ctx)
		f.clock.mu.Lock()
		f.clock.mono += 29 * time.Minute
		f.clock.mu.Unlock()
		f.clock.set(t0)
	}
	f.svc.ApplyHolds(ctx)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	t.Logf("after 20 days of running (30-min kills, the last answer replayed): clock for the store %s, status=%s held=%v grace_until=%v",
		f.svc.Now().Format(time.RFC3339), v.Status, v.Held, v.GraceUntil)
	if !v.Held {
		t.Fatalf("20 running days on one 7-day grace (replayed answer + unclean restarts): status=%s held=%v", v.Status, v.Held)
	}
}

// The same 20 days with CLEAN shutdowns (Run sees its context end) and the
// store unreachable: a clean keeping must lose nothing.
func TestLicense_CleanRestartsDoNotStretchTheGrace(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	f.st.SetDown(true)
	const cycles = 2 * 24 * 20
	for i := 0; i < cycles; i++ {
		f.restart()
		f.clock.mu.Lock()
		f.clock.mono += time.Minute
		f.clock.mu.Unlock()
		f.svc.Tick(ctx)
		f.clock.mu.Lock()
		f.clock.mono += 29 * time.Minute
		f.clock.mu.Unlock()
		f.clock.set(t0)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		f.svc.Run(cctx)
	}
	f.svc.ApplyHolds(ctx)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	t.Logf("clean restarts: clock %s status=%s held=%v", f.svc.Now().Format(time.RFC3339), v.Status, v.Held)
	if !v.Held {
		t.Fatalf("clean restarts stretched the grace: status=%s held=%v", v.Status, v.Held)
	}
}

// #3 per store, inside the one-day window: A's store is down, A's app is in
// its grace (ends t0+7d). At t0+6.5d store B signs a checked_at 23 hours
// ahead (inside the window, so it is taken). A shared clock would judge A at
// t0+7.46d and hold it; a per-store clock does not.
func TestLicense_AnotherStoresProofInsideTheWindowHoldsNothingHere(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup A: %+v", v)
	}
	f.st.SetDown(true)
	f.clock.pass(6*24*time.Hour + 12*time.Hour)
	b := storetest.New()
	t.Cleanup(b.Close)
	b.AddKey("lic-b", appstore.UseLicense, appstore.KeyActive)
	if _, err := f.svc.Approve(ctx, b.Origin(), b.Fingerprints(), nil, "admin@test"); err != nil {
		t.Fatal(err)
	}
	b.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, f.clock.Now().Add(23*time.Hour)), ""
	}
	if err := f.svc.Require(ctx, "zz-other", b.Origin(), "FXL-OTHERKEY-0001", nil); err != nil {
		t.Fatal(err)
	}
	vb, _ := f.svc.Check(ctx, "zz-other", nil)
	t.Logf("B's +23h answer: status=%s code=%s; panel clock now %s (wall %s)", vb.Status, vb.LastErrorCode,
		f.svc.Now().Format(time.RFC3339), f.clock.Now().Format(time.RFC3339))
	f.svc.Tick(ctx)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	if v.Held || f.hold.of("sign") != "" {
		t.Fatalf("B's in-window proof held A's app inside A's grace: status=%s held=%v", v.Status, v.Held)
	}
}

// #6 edges: a list whose only entry does not parse admits nothing; a list of
// blanks is no list; case, default port and a trailing slash are one origin.
func TestTrust_AllowListEdges(t *testing.T) {
	ctx := context.Background()
	bad := newFix(t, func(o *appstore.Options) { o.ConfigStores = []string{"::not a url::"} })
	bad.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	_, err := bad.svc.Approve(ctx, bad.st.Origin(), bad.st.Fingerprints(), nil, "admin@test")
	t.Logf("only a bad entry: Approve -> %s", code(err))
	if code(err) != appstore.CodeStoreRefused {
		t.Errorf("a list with only an unparsable entry admitted a store by TOFU: %v", err)
	}

	// FILEX_APP_STORE_URLS=" , ": given, so in force - and it names no store
	// (store review, second round, Y6).
	blank := newFix(t, func(o *appstore.Options) { o.ConfigStores = nil; o.ConfigStoresSet = true })
	blank.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	_, err = blank.svc.Approve(ctx, blank.st.Origin(), blank.st.Fingerprints(), nil, "admin@test")
	if code(err) != appstore.CodeStoreRefused {
		t.Errorf("a list given as blanks still trusted a store on first use: %v", err)
	}

	f := newFix(t, nil)
	o := f.opts
	o.ConfigStores = []string{strings.ToUpper(f.st.Origin()) + "/"}
	svc := appstore.New(o)
	t.Logf("configured %q, store %s: Configured=%v", o.ConfigStores[0], f.st.Origin(), svc.Configured(f.st.Origin()))
	if !svc.Configured(f.st.Origin()) {
		t.Errorf("an upper-case spelling with a trailing slash is not the same origin")
	}
}
