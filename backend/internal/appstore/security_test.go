package appstore_test

// The guards the security review of the store install (b52c8524) asked for:
// a license key goes to the store that issued it and to no other, a store's
// signed time is that store's and bounded, a restart does not stretch a
// grace, FILEX_APP_STORE_URLS is an allow list, the store's API follows no
// redirect, a link names its commit, its manifest and the filex it is for,
// and a sealed key opens only in its own row.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

// secondStore is another store, trusted, with a license key.
func (f *fix) secondStore(t *testing.T) *storetest.Store {
	t.Helper()
	b := storetest.New()
	t.Cleanup(b.Close)
	b.AddKey("idx-b", appstore.UseIndex, appstore.KeyActive)
	b.AddKey("lic-b", appstore.UseLicense, appstore.KeyActive)
	if _, err := f.svc.Approve(context.Background(), b.Origin(), b.Fingerprints(), nil, "admin@test"); err != nil {
		t.Fatal(err)
	}
	return b
}

// ── A key goes to the store that issued it (review #2) ─────────────────

// A license moved to another store keeps nothing of the first store's: not
// its key (the first store's secret), not its answers (the first store's
// word).
func TestLicense_AnotherStoreGetsNoneOfTheFirstStoresLicense(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	b := f.secondStore(t)
	b.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, f.clock.Now()), ""
	}
	if err := f.svc.Require(ctx, "sign", b.Origin(), "", nil); err != nil {
		t.Fatal(err)
	}
	v, _ := f.svc.LicenseOf(ctx, "sign")
	if v.KeyPrefix != "" || v.Status != appstore.StatusMissing || !v.Held || v.Licensee != "" {
		t.Fatalf("the license moved to %s kept the first store's key or answers: %+v", b.Origin(), v)
	}
	_, _ = f.svc.Check(ctx, "sign", nil)
	for _, r := range b.Verifies() {
		if r.Key == licKey {
			t.Fatalf("the first store's key was sent to %s", b.Origin())
		}
	}
}

// The trust is asked BEFORE the key leaves: a store that is not trusted -
// never, or no longer - is not sent the key at all.
func TestLicense_AStoreNotTrustedIsNeverSentTheKey(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	c := storetest.New()
	t.Cleanup(c.Close)
	c.AddKey("lic-c", appstore.UseLicense, appstore.KeyActive)
	c.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, f.clock.Now()), ""
	}
	if err := f.svc.Require(ctx, "seal", c.Origin(), "FXL-SEAL-0000-0001", nil); err != nil {
		t.Fatal(err)
	}
	v, _ := f.svc.Check(ctx, "seal", nil)
	if n := len(c.Verifies()); n != 0 {
		t.Fatalf("a store never trusted was sent the key (%d checks)", n)
	}
	if v.LastErrorCode != appstore.CodeTrustRequired || !v.Held {
		t.Fatalf("an untrusted store's license: %+v", v)
	}

	// Trusted once, then no longer.
	if _, err := f.svc.Check(ctx, "sign", nil); err != nil {
		t.Fatal(err)
	}
	sent := len(f.st.Verifies())
	if _, err := f.svc.Remove(ctx, f.st.Origin(), nil); err != nil {
		t.Fatal(err)
	}
	f.clock.pass(25 * time.Hour)
	v, _ = f.svc.Check(ctx, "sign", nil)
	if n := len(f.st.Verifies()); n != sent {
		t.Fatalf("a store no longer trusted was sent the key (%d checks, %d before)", n, sent)
	}
	if v.LastErrorCode != appstore.CodeTrustRequired {
		t.Fatalf("a store no longer trusted: %+v", v)
	}
}

// ── A store's time is that store's, and bounded (review #3) ────────────

// Store A cannot be reached and its app runs on its grace. Store B signs a
// checked_at a year ahead (not taken: outside a day of this server's clock),
// then one a day ahead (taken - for B's apps only): A's app keeps its grace.
func TestLicense_AnotherStoresTimeHoldsNothingHere(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup A: %+v", v)
	}
	b := f.secondStore(t)
	if err := f.svc.Require(ctx, "zz-other", b.Origin(), "FXL-OTHERKEY-0001", nil); err != nil {
		t.Fatal(err)
	}
	f.st.SetDown(true)
	f.clock.pass(6*24*time.Hour + 2*time.Hour) // A's 7-day grace has 22 hours left

	b.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, f.clock.Now().Add(400*24*time.Hour)), ""
	}
	if v, _ := f.svc.Check(ctx, "zz-other", nil); v.Status == appstore.StatusValid || v.LastErrorCode != appstore.CodeBadAnswer {
		t.Fatalf("a checked_at a year ahead of this server's clock was taken: %+v", v)
	}
	b.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, f.clock.Now().Add(23*time.Hour)), ""
	}
	if v, _ := f.svc.Check(ctx, "zz-other", nil); v.Status != appstore.StatusValid {
		t.Fatalf("a checked_at within a day: %+v", v)
	}
	f.svc.Tick(ctx)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	if v.Held || f.hold.of("sign") != "" {
		t.Fatalf("store B's time held store A's app: status=%s held=%v", v.Status, v.Held)
	}
}

// A future-dated answer is not taken, so it cannot make every later true
// answer "older than the last one taken": a revocation still lands.
func TestLicense_AFutureAnswerDoesNotFreezeTheLicense(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	f.clock.pass(time.Hour)
	*at = t0.Add(50 * 365 * 24 * time.Hour)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.LastErrorCode != appstore.CodeBadAnswer {
		t.Fatalf("an answer fifty years ahead was taken: %+v", v)
	}
	f.clock.pass(time.Hour)
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultRevoked, req.App, req.InstanceID, t0.Add(2*time.Hour)), ""
	}
	v, _ := f.svc.Check(ctx, "sign", nil)
	if v.Status != appstore.ResultRevoked || !v.Held {
		t.Fatalf("a revocation after a future-dated answer was not taken: status=%s held=%v last_error=%q", v.Status, v.Held, v.LastError)
	}
}

// A store's grace_until and next_check_by are held to at most 30 days and 2
// days after its checked_at (clipped, not refused: the answer still says
// valid).
func TestLicense_GraceAndNextCheckAreBounded(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		a := answer(appstore.ResultValid, req.App, req.InstanceID, t0)
		a["grace_until"] = t0.Add(365 * 24 * time.Hour).Format(time.RFC3339)
		a["next_check_by"] = t0.Add(30 * 24 * time.Hour).Format(time.RFC3339)
		return a, ""
	}
	v, _ := f.svc.Check(ctx, "sign", nil)
	if v.Status != appstore.StatusValid || v.GraceUntil == nil || v.NextCheckBy == nil {
		t.Fatalf("setup: %+v", v)
	}
	if v.GraceUntil.After(t0.Add(30*24*time.Hour)) || v.NextCheckBy.After(t0.Add(2*24*time.Hour)) {
		t.Fatalf("unbounded: grace_until %s, next_check_by %s (checked_at %s)", v.GraceUntil, v.NextCheckBy, t0)
	}
	f.clock.pass(3 * 24 * time.Hour)
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGrace || v.Held {
		t.Fatalf("three days on, past the bounded next_check_by: want grace, got %+v", v)
	}
	f.st.SetDown(true)
	f.clock.pass(28 * 24 * time.Hour)
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held {
		t.Fatalf("31 days without an answer, a year of grace signed: want held, got %+v", v)
	}
}

// An install link's expiry is the wall clock's: a store's signed time moves
// no link of it, or of another store, into the past.
func TestIntent_ALinksExpiryIsTheWallClocks(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	*at = f.clock.Now().Add(20 * time.Hour)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	f.intent("tokentoken1", nil) // expires in 30 minutes
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); err != nil {
		t.Fatalf("a link with 30 minutes left, after a store answer 20 hours ahead: %v", err)
	}
}

// ── One review installs once; a failed install forgets its own (review #4)

// A review is taken by one install: a second install of the same handle at
// once finds it gone; a failed install puts it back.
func TestIntent_AReviewIsTakenByOneInstall(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.trust(t)
	f.intent("tokentoken1", nil)
	in, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance)
	if err != nil {
		t.Fatal(err)
	}
	p := f.svc.Hold(f.st.Origin(), "tokentoken1", in, 7)
	got, err := f.svc.Take(p.Handle, 7)
	if err != nil || got != p {
		t.Fatalf("take: %v", err)
	}
	if _, err := f.svc.Take(p.Handle, 7); code(err) != appstore.CodeIntentNotFound {
		t.Fatalf("a second take of one review: want %s, got %v", appstore.CodeIntentNotFound, err)
	}
	f.svc.PutBack(p)
	if _, err := f.svc.Take(p.Handle, 8); code(err) != appstore.CodeIntentNotFound {
		t.Fatalf("another administrator took the review: %v", err)
	}
	if _, err := f.svc.Take(p.Handle, 7); err != nil {
		t.Fatalf("put back after a failed install: %v", err)
	}
}

// Undo removes the license row ITS Require created, and no other: of two
// installs of one app, the loser does not forget the winner's license.
func TestLicense_UndoForgetsOnlyTheRowItsRequireCreated(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.trust(t)
	first, err := f.svc.RequireUndoable(ctx, "sign", f.st.Origin(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.RequireUndoable(ctx, "sign", f.st.Origin(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Undo(ctx, second)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v == nil || f.hold.of("sign") == "" {
		t.Fatalf("the second install's undo forgot the license the first one created (%+v)", v)
	}
	f.svc.Forget(ctx, "sign")
	if _, err := f.svc.RequireUndoable(ctx, "sign", f.st.Origin(), "", nil); err != nil {
		t.Fatal(err)
	}
	f.svc.Undo(ctx, first)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v == nil {
		t.Fatal("an undo removed a row another Require created after it")
	}
}

// ── A restart does not stretch a grace (review #5) ─────────────────────

// The administrator holds the wall clock back and restarts filex every 30
// minutes, never letting it shut down cleanly. The proven time is kept at
// start and at the loop's rounds (one minute after start, then hourly); what
// ran after the last keeping is lost at each restart - unless a start that
// follows a run that did not stop cleanly counts that run as having lasted to
// its next keeping.
func TestLicense_RestartsDoNotStretchTheGrace(t *testing.T) {
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
	}
	f.svc.ApplyHolds(ctx)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	t.Logf("after 20 days of running (30-min restarts), clock reads %s, grace_until %v", f.svc.Now().Format(time.RFC3339), v.GraceUntil)
	if !v.Held {
		t.Fatalf("20 running days with the store unreachable and a 7-day grace: status=%s held=%v", v.Status, v.Held)
	}
}

// A clean shutdown keeps the time once more and says so: the next start owes
// no allowance. A start after a run that did not stop cleanly does.
func TestLicense_OnlyAnUncleanStopCostsTheRestartAllowance(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { f.svc.Run(runCtx); close(done) }()
	cancel()
	<-done
	f.restart()
	if got := f.svc.Now(); !got.Equal(t0) {
		t.Fatalf("after a clean shutdown the proven time reads %s, want %s", got, t0)
	}
	f.restart() // killed, no clean shutdown
	if got := f.svc.Now(); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("after an unclean stop the proven time reads %s, want %s (one hour of allowance)", got, t0.Add(time.Hour))
	}
}

// The allowance is filex's own pessimism, not a store's word: the store's
// next answer takes it back, so crashes during normal running do not leave
// the proven time ahead for good.
func TestLicense_TheStoresAnswerTakesTheRestartAllowanceBack(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	for i := 0; i < 3; i++ {
		f.restart()
	}
	if got := f.svc.Now(); !got.Equal(t0.Add(3 * time.Hour)) {
		t.Fatalf("three unclean restarts: the proven time reads %s, want %s", got, t0.Add(3*time.Hour))
	}
	f.clock.pass(10 * time.Minute)
	*at = t0.Add(10 * time.Minute)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("the store's answer: %+v", v)
	}
	if got := f.svc.Now(); !got.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("the store answered %s; the proven time still reads %s (the allowance stayed)", t0.Add(10*time.Minute), got)
	}
}

// ── FILEX_APP_STORE_URLS is an allow list (review #6) ──────────────────

func TestTrust_ConfiguredStoresAreAnAllowList(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, func(o *appstore.Options) {
		o.ConfigStores = []string{"https://fapps.example"}
		o.ConfigKeys = []string{"index:" + strings.Repeat("11", 32)}
	})
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	if _, err := f.svc.Approve(ctx, f.st.Origin(), f.st.Fingerprints(), nil, "admin@test"); code(err) != appstore.CodeStoreRefused {
		t.Fatalf("with FILEX_APP_STORE_URLS=https://fapps.example, trusting %s by TOFU: want %s, got %v", f.st.Origin(), appstore.CodeStoreRefused, err)
	}
	f.intent("tokentoken1", nil)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); code(err) != appstore.CodeStoreRefused {
		t.Fatalf("a link of a store the list does not name: want %s, got %v", appstore.CodeStoreRefused, err)
	}
	if f.st.IntentReads() != 0 || f.st.KeyReads() != 0 {
		t.Fatalf("a store the list does not name was asked (keys %d, links %d)", f.st.KeyReads(), f.st.IntentReads())
	}
}

// A store an administrator trusted before the list was set is refused too,
// and no license key goes to it.
func TestTrust_TheAllowListAlsoRefusesAStoreTrustedBefore(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	f.opts.ConfigStores = []string{"https://fapps.example"}
	f.restart()
	f.intent("tokentoken1", nil)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); code(err) != appstore.CodeStoreRefused {
		t.Fatalf("an administrator's earlier trust, the list set since: want %s, got %v", appstore.CodeStoreRefused, err)
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.LastErrorCode != appstore.CodeStoreRefused || len(f.st.Verifies()) != 0 {
		t.Fatalf("a license of a store the list does not name was checked with it: %+v (%d checks)", v, len(f.st.Verifies()))
	}
}

// ── The store's API follows no redirect (review #7) ────────────────────

// A store that redirects (an open redirect at the store, a hijacked route)
// does not make filex send its license key, its instance id or anything else
// to the place it points at: the redirect is an answer, never followed.
func TestClient_TheStoreAPIFollowsNoRedirect(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var reached []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reached = append(reached, r.Method+" "+r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(elsewhere.Close)
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+r.URL.Path, status)
		}))
		c := appstore.NewClient(netguard.Policy{Loopback: true}, "filex-test")
		_, kerr := c.Keys(ctx, store.URL)
		_, ierr := c.Intent(ctx, store.URL, "tokentoken1")
		cerr := c.Complete(ctx, store.URL, "tokentoken1", "fx-instance", "installed")
		_, verr := c.VerifyLicense(ctx, store.URL, appstore.LicenseRequest{Key: "FXL-SECRET-0001", App: "sign", InstanceID: "fx-instance"})
		store.Close()
		for name, err := range map[string]error{"keys": kerr, "intent": ierr, "complete": cerr, "verify": verr} {
			if err == nil {
				t.Errorf("%d: %s took a redirect as an answer", status, name)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reached) != 0 {
		t.Fatalf("a redirect from the store was followed: %q", reached)
	}
}

// ── A link pins its manifest and names its commit (review #8) ──────────

func TestIntent_ALinkPinsItsManifestAndNamesItsCommit(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.trust(t)
	cases := map[string]func(p map[string]any){
		"a module pin but no manifest pin": func(p map[string]any) {
			delete(p, "manifest_sha256")
			p["wasm_sha256"] = strings.Repeat("cd", 32)
		},
		"no commit":           func(p map[string]any) { delete(p, "commit") },
		"a branch for commit": func(p map[string]any) { p["commit"] = "main" },
		"a short commit":      func(p map[string]any) { p["commit"] = "abc1234" },
	}
	i := 0
	for name, mut := range cases {
		i++
		t.Run(name, func(t *testing.T) {
			tok := "tokentoken-m" + string(rune('a'+i))
			f.intent(tok, mut)
			if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), tok, testInstance); code(err) != appstore.CodeIntentInvalid {
				t.Fatalf("want %s, got %v", appstore.CodeIntentInvalid, err)
			}
		})
	}
}

// A link names the filex it was made for (filex_origin, signed): another
// filex refuses it with intent_wrong_instance. The two spellings are
// compared normalised.
func TestIntent_ALinkIsForTheFilexItNames(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.trust(t)
	cases := []struct {
		name, origin string
		drop         bool
		instance     string
		want         string
	}{
		{"another filex", "https://other.example", false, testInstance, appstore.CodeWrongInstance},
		{"a longer host", testInstance + ".evil.example", false, testInstance, appstore.CodeWrongInstance},
		{"another port", testInstance + ":8080", false, testInstance, appstore.CodeWrongInstance},
		{"another scheme", "https://test.local", false, testInstance, appstore.CodeWrongInstance},
		{"none named", "", true, testInstance, appstore.CodeIntentInvalid},
		{"with a path", testInstance + "/filex", false, testInstance, appstore.CodeIntentInvalid},
		{"this filex does not know its address", testInstance, false, "", appstore.CodeWrongInstance},
		{"spelled otherwise", "HTTP://Test.LOCAL:80/", false, testInstance, ""},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := "tokentoken-o" + string(rune('a'+i))
			f.intent(tok, func(p map[string]any) {
				if c.drop {
					delete(p, "filex_origin")
				} else {
					p["filex_origin"] = c.origin
				}
			})
			if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), tok, c.instance); code(err) != c.want {
				t.Fatalf("filex_origin %q on %q: want %q, got %v", c.origin, c.instance, c.want, err)
			}
		})
	}
}

func TestInstanceOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://Files.Example.com":      "https://files.example.com",
		"https://files.example.com:443/": "https://files.example.com",
		"http://files.example.com:80":    "http://files.example.com",
		"https://files.example.com:8443": "https://files.example.com:8443",
		"https://example.com/filex/":     "https://example.com",
		"http://[::1]:5212":              "http://[::1]:5212",
		"HTTPS://EXAMPLE.COM:443/a/b":    "https://example.com",
	} {
		got, err := appstore.InstanceOrigin(in)
		if err != nil || got != want {
			t.Errorf("InstanceOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "files.example.com", "ftp://files.example.com", "https://user@files.example.com", "https://files.example.com/?a=1", "https://files.example.com/#x"} {
		if got, err := appstore.InstanceOrigin(bad); err == nil {
			t.Errorf("InstanceOrigin(%q) = %q, want a refusal", bad, got)
		}
	}
}

// ── A failed paid upgrade (review #9) ──────────────────────────────────

// A paid upgrade brings a new key; the upgrade fails: Undo puts the license
// back as it was - the old key, the old answers, the app running.
func TestLicense_UndoPutsAChangedLicenseBack(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	u, err := f.svc.RequireUndoable(ctx, "sign", f.st.Origin(), "FXL-NEWK-EY00-0003", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.KeyPrefix != appstore.Prefix("FXL-NEWK-EY00-0003") || !v.Held {
		t.Fatalf("the upgrade's new key, before it is confirmed: %+v", v)
	}
	f.svc.Undo(ctx, u)
	v, _ := f.svc.LicenseOf(ctx, "sign")
	if v == nil || v.KeyPrefix != appstore.Prefix(licKey) || v.Status != appstore.StatusValid || v.Held || f.hold.of("sign") != "" {
		t.Fatalf("a failed upgrade's license is not what it was: %+v (hold %q)", v, f.hold.of("sign"))
	}
}

// ── A key sealed for one app opens for no other (review #10) ───────────

// Somebody who can write the database copies one app's sealed key into
// another app's license row: filex does not open it there, and nothing is
// sent to the store.
func TestLicense_AKeySealedForAnotherAppDoesNotOpen(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if err := f.svc.Require(ctx, "seal", f.st.Origin(), "FXL-SEAL-0000-0002", nil); err != nil {
		t.Fatal(err)
	}
	var sign, seal map[string]any
	f.mem.mu.Lock()
	_ = json.Unmarshal([]byte(f.mem.rows["license:sign"]), &sign)
	_ = json.Unmarshal([]byte(f.mem.rows["license:seal"]), &seal)
	seal["key_sealed"] = sign["key_sealed"]
	b, _ := json.Marshal(seal)
	f.mem.rows["license:seal"] = string(b)
	f.mem.mu.Unlock()

	v, _ := f.svc.Check(ctx, "seal", nil)
	for _, r := range f.st.Verifies() {
		if r.App == "seal" && r.Key == licKey {
			t.Fatalf("the key sealed for sign was opened for seal and sent to the store")
		}
	}
	if v.LastErrorCode != appstore.CodeLicenseKey || !v.Held {
		t.Fatalf("a moved sealed key: %+v", v)
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("control: sign's own key still opens: %+v", v)
	}
}
