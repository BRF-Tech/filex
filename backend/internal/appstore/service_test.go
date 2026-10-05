package appstore_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// memStore is app_store_state and the audit log in memory.
type memStore struct {
	mu    sync.Mutex
	rows  map[string]string
	audit []*model.AuditEntry
}

func newMem() *memStore { return &memStore{rows: map[string]string{}} }

func (m *memStore) GetAppStoreState(_ context.Context, k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.rows[k]
	return v, ok, nil
}
func (m *memStore) PutAppStoreState(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[k] = v
	return nil
}
func (m *memStore) DeleteAppStoreState(_ context.Context, k string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[k]
	delete(m.rows, k)
	return ok, nil
}
func (m *memStore) ListAppStoreState(_ context.Context, p string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.rows {
		if strings.HasPrefix(k, p) {
			out[k] = v
		}
	}
	return out, nil
}
func (m *memStore) InsertAuditEntry(_ context.Context, e *model.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, e)
	return nil
}
func (m *memStore) actions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for _, e := range m.audit {
		out = append(out, e.Action)
	}
	return out
}

// holder records the holds the service decides.
type holder struct {
	mu    sync.Mutex
	holds map[string]string
}

func (h *holder) SetLicenseHold(app, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds == nil {
		h.holds = map[string]string{}
	}
	h.holds[app] = reason
}
func (h *holder) of(app string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.holds[app]
}

// clk is the wall clock and the monotonic clock, each moved by hand.
type clk struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Duration
}

func (c *clk) Now() time.Time       { c.mu.Lock(); defer c.mu.Unlock(); return c.wall }
func (c *clk) Mono() time.Duration  { c.mu.Lock(); defer c.mu.Unlock(); return c.mono }
func (c *clk) set(t time.Time)      { c.mu.Lock(); c.wall = t; c.mu.Unlock() }
func (c *clk) pass(d time.Duration) { c.mu.Lock(); c.wall = c.wall.Add(d); c.mono += d; c.mu.Unlock() }

type fix struct {
	st    *storetest.Store
	mem   *memStore
	hold  *holder
	clock *clk
	svc   *appstore.Service
	opts  appstore.Options
}

func newFix(t *testing.T, mut func(o *appstore.Options)) *fix {
	t.Helper()
	st := storetest.New()
	t.Cleanup(st.Close)
	box, err := secretbox.New("test-secret-key-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	f := &fix{st: st, mem: newMem(), hold: &holder{}, clock: &clk{wall: time.Now().UTC().Truncate(time.Second)}}
	f.opts = appstore.Options{
		Store: f.mem, Client: appstore.NewClient(netguard.Policy{Loopback: true}, "filex-test"), Box: box,
		Loopback: true, Holder: f.hold, FilexVersion: "0.52.0", Now: f.clock.Now, Mono: f.clock.Mono,
	}
	if mut != nil {
		mut(&f.opts)
	}
	f.svc = appstore.New(f.opts)
	return f
}

// testInstance is the filex the tests' links are made for.
const testInstance = storetest.DefaultFilexOrigin

// restart builds a new service over the same rows: a filex that restarted.
func (f *fix) restart() {
	f.clock.mu.Lock()
	f.clock.mono = 0
	f.clock.mu.Unlock()
	f.svc = appstore.New(f.opts)
	f.svc.Start(context.Background())
}

func code(err error) string {
	if e, ok := appstore.AsError(err); ok {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "other: " + err.Error()
}

func (f *fix) trust(t *testing.T) {
	t.Helper()
	if _, err := f.svc.Approve(context.Background(), f.st.Origin(), f.st.Fingerprints(), nil, "admin@test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func (f *fix) intent(token string, mut func(p map[string]any)) {
	p := f.st.IntentPayload("tid-"+token, "lang-eo", "1.0.0", "Owner/lang-eo", "v1.0.0", f.clock.Now().Add(30*time.Minute))
	p["kind"] = "language_pack"
	p["manifest_sha256"] = strings.Repeat("ab", 32)
	if mut != nil {
		mut(p)
	}
	f.st.PutIntent(token, &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
}

// ── Trust ──────────────────────────────────────────────────────────────

func TestTrust_AnUntrustedStoresLinkIsNeverFetched(t *testing.T) {
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.intent("tokentoken1", nil)

	_, err := f.svc.ReadIntent(context.Background(), f.st.Origin(), "tokentoken1", testInstance)
	e, ok := appstore.AsError(err)
	if !ok || e.Code != appstore.CodeTrustRequired {
		t.Fatalf("want %s, got %v", appstore.CodeTrustRequired, err)
	}
	if f.st.IntentReads() != 0 {
		t.Fatalf("the link of an untrusted store was fetched %d times", f.st.IntentReads())
	}
	fp, _ := e.Detail["fingerprints"].([]string)
	if len(fp) != 2 {
		t.Fatalf("the question shows the store's two keys, got %v", e.Detail["fingerprints"])
	}
	if f.svc.TrustStatus(context.Background(), f.st.Origin()) != "" {
		t.Fatal("asking changed the trust")
	}
}

func TestTrust_ApprovalNamesTheKeysShown(t *testing.T) {
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	shown := f.st.Fingerprints()
	// The store's keys change while the administrator reads.
	f.st.AddKey("idx-2", appstore.UseIndex, appstore.KeyActive)
	_, err := f.svc.Approve(context.Background(), f.st.Origin(), shown, nil, "a")
	if code(err) != appstore.CodeKeyChanged {
		t.Fatalf("approving keys nobody saw: want %s, got %v", appstore.CodeKeyChanged, err)
	}
	if f.svc.TrustStatus(context.Background(), f.st.Origin()) != "" {
		t.Fatal("a refused approval stored a trust")
	}
	f.trust(t)
	if f.svc.TrustStatus(context.Background(), f.st.Origin()) != appstore.SourceAdmin {
		t.Fatal("approved, but not trusted")
	}
	if got := f.mem.actions(); len(got) != 1 || got[0] != "app_store.trust" {
		t.Fatalf("audit: %v", got)
	}
}

func TestTrust_ANewKeyAsksAgainARetiredOneGoesByItself(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("idx-old", appstore.UseIndex, appstore.KeyActive)
	f.trust(t)
	f.intent("tokentoken1", nil)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); err != nil {
		t.Fatalf("trusted store: %v", err)
	}

	// Narrowing needs nobody: a retired key leaves the trust, and a link it
	// signs is refused.
	f.st.SetKeyStatus("idx-old", appstore.KeyRetired)
	f.intent("tokentoken2", nil)
	f.st.Intent("tokentoken2").KeyID = "idx-old"
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken2", testInstance); code(err) != appstore.CodeSignature {
		t.Fatalf("a link signed by a retired key: want %s, got %v", appstore.CodeSignature, err)
	}

	// Widening is the administrator's: new material under a known id.
	f.st.ReplaceKey("idx-1")
	f.intent("tokentoken3", nil)
	_, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken3", testInstance)
	e, _ := appstore.AsError(err)
	if e == nil || e.Code != appstore.CodeKeyChanged || e.Detail["previous_keys"] == nil {
		t.Fatalf("a swapped key: want %s with the previous keys, got %v", appstore.CodeKeyChanged, err)
	}
	f.trust(t)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken3", testInstance); err != nil {
		t.Fatalf("re-approved: %v", err)
	}
}

func TestTrust_NextBecomingActiveIsTakenSilently(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("idx-2", appstore.UseIndex, appstore.KeyNext)
	f.trust(t)
	f.intent("tokentoken1", nil)
	f.st.Intent("tokentoken1").KeyID = "idx-2"
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); code(err) != appstore.CodeSignature {
		t.Fatalf("a `next` key signs nothing yet: got %v", err)
	}
	f.st.SetKeyStatus("idx-2", appstore.KeyActive)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); err != nil {
		t.Fatalf("the approved next key, now active: %v", err)
	}
}

func TestTrust_AConfiguredStoreUsesOnlyTheConfiguredKeys(t *testing.T) {
	ctx := context.Background()
	st := storetest.New()
	defer st.Close()
	pub := st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	st.AddKey("idx-2", appstore.UseIndex, appstore.KeyActive)
	f := newFix(t, func(o *appstore.Options) {
		o.ConfigStores = []string{st.Origin()}
		o.ConfigKeys = []string{"index:" + strings.ToLower(hexOf(pub))}
	})
	f.st = st
	f.intent("tokentoken1", nil)
	if _, err := f.svc.ReadIntent(ctx, st.Origin(), "tokentoken1", testInstance); err != nil {
		t.Fatalf("a configured store needs no approval: %v", err)
	}
	f.intent("tokentoken2", nil)
	st.Intent("tokentoken2").KeyID = "idx-2"
	if _, err := f.svc.ReadIntent(ctx, st.Origin(), "tokentoken2", testInstance); code(err) != appstore.CodeKeyNotConfigured {
		t.Fatalf("a published key FILEX_APP_STORE_KEYS lacks: want %s, got %v", appstore.CodeKeyNotConfigured, err)
	}
	if _, err := f.svc.Approve(ctx, st.Origin(), st.Fingerprints(), nil, "a"); err == nil {
		t.Fatal("a configured store is not the administrator's to trust")
	}
}

// ── Install links ──────────────────────────────────────────────────────

func TestIntent_EveryWayALinkIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.trust(t)

	cases := []struct {
		name  string
		setup func(tok string)
		want  string
	}{
		{"tampered", func(tok string) { f.intent(tok, nil); f.st.Intent(tok).Tamper = true }, appstore.CodeSignature},
		{"signed by the license key", func(tok string) { f.intent(tok, nil); f.st.Intent(tok).KeyID = "lic-1" }, appstore.CodeSignature},
		{"expired", func(tok string) {
			f.intent(tok, func(p map[string]any) { p["expires_at"] = f.clock.Now().Add(-time.Minute).Format(time.RFC3339) })
		}, appstore.CodeIntentExpired},
		{"gone at the store", func(tok string) { f.intent(tok, nil); f.st.Intent(tok).Status = http.StatusGone }, appstore.CodeIntentGone},
		{"unknown at the store", func(tok string) {}, appstore.CodeIntentUnknown},
		{"for another store", func(tok string) {
			f.intent(tok, func(p map[string]any) { p["store"] = "https://other.example" })
		}, appstore.CodeIntentInvalid},
		{"pins nothing", func(tok string) {
			f.intent(tok, func(p map[string]any) { delete(p, "manifest_sha256") })
		}, appstore.CodeIntentInvalid},
		{"lives too long", func(tok string) {
			f.intent(tok, func(p map[string]any) { p["expires_at"] = f.clock.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339) })
		}, appstore.CodeIntentInvalid},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := "tokentoken-" + string(rune('a'+i))
			c.setup(tok)
			if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), tok, testInstance); code(err) != c.want {
				t.Fatalf("want %s, got %v", c.want, err)
			}
		})
	}
	hits := f.st.IntentReads()
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "../keys.json", testInstance); code(err) != appstore.CodeIntentInvalid {
		t.Fatalf("a token with a path in it: %v", err)
	}
	if f.st.IntentReads() != hits {
		t.Fatal("a token that is not one was sent to the store")
	}
}

// A link used once - installed or cancelled - is refused here afterwards,
// whatever the store says (a replayed link), and the store is told.
func TestIntent_ALinkIsUsedOnce(t *testing.T) {
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
	if _, err := f.svc.Pending(p.Handle, 8); code(err) != appstore.CodeIntentNotFound {
		t.Fatalf("another administrator's review: %v", err)
	}
	f.svc.Finish(ctx, p, "cancelled", nil)
	if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), "tokentoken1", testInstance); code(err) != appstore.CodeIntentUsed {
		t.Fatalf("a used link again: want %s, got %v", appstore.CodeIntentUsed, err)
	}
	c := f.st.Completions()
	id, _ := f.svc.InstanceID(ctx)
	if len(c) != 1 || c[0].Result != "cancelled" || c[0].InstanceID != id || c[0].Token != "tokentoken1" {
		t.Fatalf("completions: %+v (instance %s)", c, id)
	}
	if _, err := f.svc.Pending(p.Handle, 7); code(err) != appstore.CodeIntentNotFound {
		t.Fatalf("a finished review is closed: %v", err)
	}
}

func TestComparePins(t *testing.T) {
	manifest := []byte(`{"name":"sign"}`)
	sum := appstoreSHA(manifest)
	in := &appstore.Intent{App: "sign", Version: "1.0.0", Kind: "app", ManifestSHA256: sum, WasmSHA256: strings.Repeat("1", 64),
		UISHA256: strings.Repeat("2", 64), Permissions: []string{"files:read", "http:FreeTSA.org"}}
	good := &appstore.Source{ManifestBytes: manifest, Name: "sign", Version: "1.0.0", Kind: "app", WasmSHA256: strings.Repeat("1", 64),
		UISHA256: strings.Repeat("2", 64), DeclaredPermissions: []string{"http:freetsa.org", "files:read"}}
	if err := appstore.ComparePins(in, good); err != nil {
		t.Fatalf("matching source: %v", err)
	}
	for name, mut := range map[string]func(s *appstore.Source){
		"version (an older release under the tag)": func(s *appstore.Source) { s.Version = "0.9.0" },
		"manifest bytes":    func(s *appstore.Source) { s.ManifestBytes = []byte(`{"name":"sign","x":1}`) },
		"module":            func(s *appstore.Source) { s.WasmSHA256 = strings.Repeat("3", 64) },
		"interface":         func(s *appstore.Source) { s.UISHA256 = strings.Repeat("4", 64) },
		"a permission more": func(s *appstore.Source) { s.DeclaredPermissions = append(s.DeclaredPermissions, "mail:send") },
		"another app":       func(s *appstore.Source) { s.Name = "evil" },
	} {
		t.Run(name, func(t *testing.T) {
			src := *good
			src.DeclaredPermissions = append([]string(nil), good.DeclaredPermissions...)
			mut(&src)
			if err := appstore.ComparePins(in, &src); code(err) != appstore.CodePinMismatch {
				t.Fatalf("want %s, got %v", appstore.CodePinMismatch, err)
			}
		})
	}
	// Derived interface permissions are not a manifest's to declare.
	in2 := *in
	in2.Permissions = append([]string{"ui", "ui-viewer:.drawio"}, in.Permissions...)
	if err := appstore.ComparePins(&in2, good); err != nil {
		t.Fatalf("derived permissions in the link: %v", err)
	}
}

// ── Licenses ───────────────────────────────────────────────────────────

const licKey = "FXL-7Q2M-K9P4-ZZ31"

// answer is a license answer payload at checkedAt.
func answer(result, app, instance string, checkedAt time.Time) map[string]any {
	return map[string]any{
		"result": result, "app": app, "licensee": "Acme Ltd.", "seats": 5, "seats_used": 2,
		"instance_id": instance, "checked_at": checkedAt.UTC().Format(time.RFC3339),
		"next_check_by": checkedAt.Add(24 * time.Hour).UTC().Format(time.RFC3339),
		"grace_until":   checkedAt.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
}

// licFix: a trusted store with an index and a license key, and a paid app
// required with a key.
func licFix(t *testing.T) (*fix, *time.Time) {
	t.Helper()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.trust(t)
	storeNow := f.clock.Now()
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultValid, req.App, req.InstanceID, storeNow), ""
	}
	if err := f.svc.Require(context.Background(), "sign", f.st.Origin(), licKey, nil); err != nil {
		t.Fatal(err)
	}
	return f, &storeNow
}

func TestLicense_HeldUntilTheStoreSaysValid(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if f.hold.of("sign") == "" {
		t.Fatal("a paid app must be held before its license is confirmed")
	}
	v, err := f.svc.Check(ctx, "sign", nil)
	if err != nil || v.Status != appstore.StatusValid || v.Held {
		t.Fatalf("valid answer: %+v %v", v, err)
	}
	if f.hold.of("sign") != "" {
		t.Fatalf("still held: %q", f.hold.of("sign"))
	}
	if v.Licensee != "Acme Ltd." || v.Seats == nil || *v.Seats != 5 {
		t.Fatalf("license facts: %+v", v)
	}
	req := f.st.Verifies()
	id, _ := f.svc.InstanceID(ctx)
	if len(req) != 1 || req[0].Key != licKey || req[0].App != "sign" || req[0].InstanceID != id || req[0].FilexVersion != "0.52.0" {
		t.Fatalf("the check sent: %+v", req)
	}
}

// The key is in no answer, no audit row and no stored row in the clear.
func TestLicense_TheKeyLeaksNowhere(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	v, _ := f.svc.Check(ctx, "sign", nil)
	_, _ = f.svc.SetKey(ctx, "sign", licKey, nil)
	views, _ := json.Marshal([]any{v, f.svc.Licenses(ctx), f.svc.AppLicense(ctx, "sign")})
	if strings.Contains(string(views), licKey) {
		t.Fatalf("an answer carries the key: %s", views)
	}
	if !strings.Contains(string(views), appstore.Prefix(licKey)) {
		t.Fatalf("the prefix is shown: %s", views)
	}
	audit, _ := json.Marshal(f.mem.audit)
	if strings.Contains(string(audit), licKey) {
		t.Fatalf("the audit log carries the key: %s", audit)
	}
	rows, _ := json.Marshal(f.mem.rows)
	if strings.Contains(string(rows), licKey) {
		t.Fatal("a stored row carries the key in the clear")
	}
}

func TestLicense_EveryRefusalHoldsNeverRemoves(t *testing.T) {
	ctx := context.Background()
	for _, result := range []string{appstore.ResultRevoked, appstore.ResultExpired, appstore.ResultInvalid, appstore.ResultSeatsExhausted, appstore.ResultWrongApp} {
		t.Run(result, func(t *testing.T) {
			f, at := licFix(t)
			_, _ = f.svc.Check(ctx, "sign", nil)
			f.clock.pass(time.Hour)
			*at = f.clock.Now()
			r := result
			f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
				return answer(r, req.App, req.InstanceID, *at), ""
			}
			v, err := f.svc.Check(ctx, "sign", nil)
			if err != nil || v.Status != result || !v.Held {
				t.Fatalf("%s: %+v %v", result, v, err)
			}
			if f.hold.of("sign") == "" {
				t.Fatal("not held")
			}
			if lic, _ := f.svc.LicenseOf(ctx, "sign"); lic == nil {
				t.Fatal("the license row is gone: a license holds an app, it removes nothing")
			}
			acts := strings.Join(f.mem.actions(), ",")
			if !strings.Contains(acts, "app_plugin.license_held") {
				t.Fatalf("no audit row for the hold: %s", acts)
			}
		})
	}
}

// The store unreachable: the last valid answer holds until ITS grace_until,
// and not a second longer - by the clock's latest reading, which turning the
// wall clock back does not move, inside one run or across a restart.
func TestLicense_GraceIsTheStoresAndTheClockCannotStretchIt(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid {
		t.Fatalf("setup: %+v", v)
	}
	f.st.SetDown(true)
	f.clock.pass(2 * 24 * time.Hour)
	v, _ := f.svc.Check(ctx, "sign", nil)
	if v.Status != appstore.StatusGrace || v.Held || v.LastErrorCode != appstore.CodeUnreachable {
		t.Fatalf("two days unreachable: want grace, running; got %+v", v)
	}
	// Past the store's grace_until.
	f.clock.pass(6 * 24 * time.Hour)
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held || f.hold.of("sign") == "" {
		t.Fatalf("past grace: want grace_expired, held; got %+v", v)
	}
	// The administrator turns the wall clock back to the day after the check.
	f.clock.set(t0.Add(24 * time.Hour))
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held {
		t.Fatalf("clock turned back in the same run: the grace came back (%+v)", v)
	}
	// ...and restarts filex with it still turned back.
	f.restart()
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held || f.hold.of("sign") == "" {
		t.Fatalf("clock turned back across a restart: the grace came back (%+v)", v)
	}
	// The store reachable again, the wall clock still eight days back: its
	// answer is more than a day from this server's clock and is not taken.
	f.st.SetDown(false)
	*at = t0.Add(9 * 24 * time.Hour)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusGraceExpired || !v.Held || v.LastErrorCode != appstore.CodeBadAnswer {
		t.Fatalf("store back, the clock still turned back: want held, the answer refused; got %+v", v)
	}
	// The clock set right: a fresh signed answer releases it.
	f.clock.set(t0.Add(9 * 24 * time.Hour))
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid || v.Held {
		t.Fatalf("store back: %+v", v)
	}
}

// Turned back BEFORE the grace ended, inside one run: the monotonic clock
// keeps counting, so the grace still ends on time.
func TestLicense_MonotonicTimeEndsTheGraceWhateverTheWallClockSays(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	_, _ = f.svc.Check(ctx, "sign", nil)
	f.st.SetDown(true)
	f.clock.set(t0.Add(-30 * 24 * time.Hour)) // back a month
	f.clock.mu.Lock()
	f.clock.mono += 8 * 24 * time.Hour // ...while eight days really pass
	f.clock.mu.Unlock()
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held {
		t.Fatalf("eight days on the monotonic clock, the wall clock a month back: %+v", v)
	}
}

// Turned FORWARD: the grace ends early - even right after a valid answer
// (fail-safe: the app is held while this server's clock says the grace has
// passed, and an answer a year behind this server's clock is not taken) - and
// only for as long as the clock is ahead: the proven time never took the
// wrong clock in, so once it is fixed the app runs and the next outage is
// judged by the store's time.
func TestLicense_AClockSetForwardEndsTheGraceOnlyWhileItIsAhead(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	_, _ = f.svc.Check(ctx, "sign", nil)
	f.clock.set(t0.Add(400 * 24 * time.Hour))
	*at = t0.Add(time.Hour)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusGraceExpired || !v.Held || v.LastErrorCode != appstore.CodeBadAnswer {
		t.Fatalf("a valid answer with this server's clock a year ahead: want held (grace_expired), the answer refused; got %+v", v)
	}
	f.clock.set(t0.Add(2 * time.Hour)) // the clock is fixed
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Held {
		t.Fatalf("the clock fixed: want the app running again, got %+v", v)
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusValid || v.Held {
		t.Fatalf("the clock fixed, the store asked again: want valid, got %+v", v)
	}
	f.restart() // and a restart does not bring the mistaken year back
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Held {
		t.Fatalf("after a restart: %+v", v)
	}
	f.clock.pass(3 * time.Hour)
	f.st.SetDown(true)
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status != appstore.StatusGrace || v.Held {
		t.Fatalf("the store down three hours later: want grace, got %+v", v)
	}
}

// A store whose clock is behind the proven time does not pull it back.
func TestLicense_TheProvenTimeOnlyMovesForward(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	_, _ = f.svc.Check(ctx, "sign", nil) // proven: t0
	f.clock.pass(10 * 24 * time.Hour)    // ten days of running time
	f.clock.set(t0)                      // ...and the wall clock turned back to t0
	*at = t0.Add(time.Minute)            // a store answer barely later than the first
	f.st.SetDown(false)
	_, _ = f.svc.Check(ctx, "sign", nil)
	f.st.SetDown(true)
	// The latest answer's grace is t0+1m+7d; the proven time is t0+10d.
	f.svc.ApplyHolds(ctx)
	if v, _ := f.svc.LicenseOf(ctx, "sign"); v.Status != appstore.StatusGraceExpired || !v.Held {
		t.Fatalf("a store answer behind the proven time pulled it back: %+v", v)
	}
}

// A replayed older answer, another installation's answer, an answer signed
// with the index key: none is taken, and none moves the status.
func TestLicense_AnswersThatAreNotTaken(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	t0 := *at
	_, _ = f.svc.Check(ctx, "sign", nil)
	f.clock.pass(time.Hour)

	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultRevoked, req.App, req.InstanceID, t0.Add(-time.Hour)), ""
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status == appstore.ResultRevoked || v.LastErrorCode != appstore.CodeBadAnswer {
		t.Fatalf("an answer older than the last taken: %+v", v)
	}
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultRevoked, req.App, "fx-someone-else", t0.Add(2*time.Hour)), ""
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status == appstore.ResultRevoked {
		t.Fatalf("another installation's answer was taken: %+v", v)
	}
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		return answer(appstore.ResultRevoked, req.App, req.InstanceID, t0.Add(2*time.Hour)), "idx-1"
	}
	if v, _ := f.svc.Check(ctx, "sign", nil); v.Status == appstore.ResultRevoked || v.LastErrorCode != appstore.CodeSignature {
		t.Fatalf("an answer signed by the index key: %+v", v)
	}
	if f.hold.of("sign") != "" {
		t.Fatal("an answer that was not taken held the app")
	}
}

func TestLicense_NoKeyIsMissingAndHeld(t *testing.T) {
	ctx := context.Background()
	f := newFix(t, nil)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.trust(t)
	if err := f.svc.Require(ctx, "sign", f.st.Origin(), "", nil); err != nil {
		t.Fatal(err)
	}
	v, _ := f.svc.LicenseOf(ctx, "sign")
	if v.Status != appstore.StatusMissing || !v.Held || f.hold.of("sign") == "" {
		t.Fatalf("no key: %+v", v)
	}
	if err := f.svc.Require(ctx, "x", f.st.Origin(), "has space", nil); code(err) != appstore.CodeLicenseKey {
		t.Fatalf("a key with a space: %v", err)
	}
	if got := f.svc.AppLicense(ctx, "free-app"); got.Status != appstore.StatusFree {
		t.Fatalf("a free app reads %q", got.Status)
	}
}

func TestLicense_DueAndTick(t *testing.T) {
	ctx := context.Background()
	f, at := licFix(t)
	f.svc.Tick(ctx) // never asked: due
	if n := len(f.st.Verifies()); n != 1 {
		t.Fatalf("first tick asked %d times", n)
	}
	f.clock.pass(time.Hour)
	f.svc.Tick(ctx)
	if n := len(f.st.Verifies()); n != 1 {
		t.Fatalf("an hour later the store was asked again (%d)", n)
	}
	f.clock.pass(24 * time.Hour)
	*at = f.clock.Now()
	f.svc.Tick(ctx)
	if n := len(f.st.Verifies()); n != 2 {
		t.Fatalf("a day later: %d checks, want 2", n)
	}
}

func hexOf(b []byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, d[c>>4], d[c&0xf])
	}
	return string(out)
}

func appstoreSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hexOf(sum[:])
}
