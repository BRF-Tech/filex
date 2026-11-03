package captcha

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// memStore is a settings table.
type memStore map[string]string

func (m memStore) GetSetting(_ context.Context, k string) (string, error) {
	if _, broken := m["__broken__"]; broken {
		return "", errors.New("database is locked")
	}
	v, ok := m[k]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (m memStore) UpsertSetting(_ context.Context, k, v string) error { m[k] = v; return nil }

// fakeProvider is a siteverify that answers what the test says - in the
// process, through the client's transport: no socket is opened (a test
// binary listening on loopback is refused by some desktop firewalls).
type fakeProvider struct {
	calls  atomic.Int64
	answer func(w http.ResponseWriter, r *http.Request)
}

const fakeURL = "https://siteverify.test/verify"

func (p *fakeProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "siteverify.test" {
		return nil, errors.New("dial tcp " + r.URL.Host + ": connection refused")
	}
	p.calls.Add(1)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.answer(rec, r)
	}()
	select {
	case <-done:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return rec.Result(), nil
}

func newFakeProvider(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *fakeProvider {
	t.Helper()
	return &fakeProvider{answer: answer}
}

func jsonAnswer(status int, body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

type harness struct {
	v        *Verifier
	store    memStore
	provider *fakeProvider
	alarms   struct {
		fallback      atomic.Int64
		misconfigured atomic.Int64
		counted       atomic.Int64
	}
}

// newHarness: Turnstile on for every form, keys set, siteverify the fake.
func newHarness(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *harness {
	t.Helper()
	h := &harness{store: memStore{}}
	h.provider = newFakeProvider(t, answer)
	env := map[string]string{EnvProvider: "turnstile", EnvSiteKey: "1x00000000000000000000AA", EnvSecret: "1x0000000000000000000000000000000AA"}
	h.v = &Verifier{
		Settings:   &Settings{Store: h.store, Env: func(k string) string { return env[k] }, TTL: -1},
		Marks:      NewMemoryMarks(),
		Local:      &Local{Key: []byte("test-key"), MaxNumber: 2000},
		Siteverify: map[string]string{ProviderTurnstile: fakeURL, ProviderRecaptcha: fakeURL},
		Client:     &http.Client{Transport: h.provider},
	}
	h.v.Alarms = Alarms{
		Counted:       func(string) { h.alarms.counted.Add(1) },
		Fallback:      func(int, time.Duration, string, string) { h.alarms.fallback.Add(1) },
		Misconfigured: func(string, string) { h.alarms.misconfigured.Add(1) },
	}
	return h
}

var ctx = context.Background()

const goodToken = "0.abcDEF123_-.xyz"

func TestCheck_OffAndUnprotectedFormsPass(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, `{"success":true}`))
	h.v.Settings.Env = func(string) string { return "" }
	if r := h.v.Check(ctx, FormLogin, Fields{}, ""); r.Status != StatusSkipped {
		t.Fatalf("off: %+v", r)
	}
	h.store[KeyProvider] = "turnstile"
	h.store[KeySiteKey] = "k"
	h.store[KeyForms] = `["file_request"]`
	if r := h.v.Check(ctx, FormLogin, Fields{}, ""); r.Status != StatusSkipped {
		t.Fatalf("login not in the list: %+v", r)
	}
	if h.provider.calls.Load() != 0 {
		t.Fatal("the provider was asked")
	}
}

func TestCheck_TokenRules(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, `{"success":true}`))
	if r := h.v.Check(ctx, FormLogin, Fields{}, ""); r.Status != StatusInvalid || r.Reason != ReasonMissingToken || r.HTTPStatus() != 422 {
		t.Fatalf("no token: %+v", r)
	}
	for _, bad := range []string{strings.Repeat("a", TurnstileTokenMax+1), "has space", "a+b/c=", "ü"} {
		if r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: bad}, ""); r.Reason != ReasonBadToken {
			t.Fatalf("token %q: %+v", bad[:min(len(bad), 12)], r)
		}
	}
	if h.provider.calls.Load() != 0 {
		t.Fatal("a token of the wrong shape reached the provider")
	}
	if r := h.v.Check(ctx, FormLogin, Fields{CfTurnstileResponse: goodToken}, "203.0.113.9"); r.Status != StatusPassed {
		t.Fatalf("good token: %+v", r)
	}
	// Once: the second use is refused without asking again.
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, ""); r.Reason != ReasonReplayed {
		t.Fatalf("second use: %+v", r)
	}
	if n := h.provider.calls.Load(); n != 1 {
		t.Fatalf("%d calls, want 1", n)
	}
}

func TestCheck_SiteverifyGetsTheFormAndTheAddress(t *testing.T) {
	var got string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm.Get("secret") + "|" + r.PostForm.Get("response") + "|" + r.PostForm.Get("remoteip")
		jsonAnswer(200, `{"success":true}`)(w, r)
	})
	h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, "203.0.113.9")
	if got != "1x0000000000000000000000000000000AA|"+goodToken+"|203.0.113.9" {
		t.Fatalf("siteverify got %q", got)
	}
}

func TestCheck_ARefusalIs422AndNeverTheFallback(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		reason string
	}{
		"success false":        {200, `{"success":false,"error-codes":["invalid-input-response"]}`, ReasonInvalidToken},
		"expired":              {200, `{"success":false,"error-codes":["timeout-or-duplicate"]}`, ReasonTokenExpired},
		"bad-request is a 422": {400, `{"success":false,"error-codes":["bad-request"]}`, ReasonProviderRejected},
		"429 is a 422":         {429, `{"success":false}`, ReasonProviderRejected},
		"403 no body":          {403, ``, ReasonProviderRejected},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, jsonAnswer(tc.status, tc.body))
			r := h.v.Check(ctx, FormSharePassword, Fields{CaptchaToken: goodToken}, "")
			if r.Status != StatusInvalid || r.Reason != tc.reason || r.HTTPStatus() != 422 {
				t.Fatalf("got %+v, want 422 %s", r, tc.reason)
			}
			if h.v.FallbackActive(ctx) || h.alarms.counted.Load() != 0 {
				t.Fatal("a refusal opened the fallback")
			}
			if n := h.provider.calls.Load(); n != 1 {
				t.Fatalf("%d calls: a 4xx/refusal is not retried", n)
			}
		})
	}
}

func TestCheck_AnOutageIsTheFallback(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}
	for name, tc := range map[string]struct {
		answer func(w http.ResponseWriter, r *http.Request)
		calls  int64
	}{
		"5xx":      {jsonAnswer(502, `bad gateway`), 2},
		"not json": {jsonAnswer(200, `<html>captive portal</html>`), 2},
		"no field": {jsonAnswer(200, `{"ok":true}`), 2},
		"internal": {jsonAnswer(200, `{"success":false,"error-codes":["internal-error"]}`), 2},
		"timeout":  {slow, 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tc.answer)
			h.store[KeyTimeout] = "1000"
			r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, "")
			if r.Status != StatusFallback || r.HTTPStatus() != 409 || r.Challenge == nil {
				t.Fatalf("got %+v, want 409 with a challenge", r)
			}
			if n := h.provider.calls.Load(); n != tc.calls {
				t.Fatalf("%d calls, want %d (one retry)", n, tc.calls)
			}
			if !h.v.FallbackActive(ctx) || h.alarms.counted.Load() != 1 {
				t.Fatal("the outage flag was not raised / not counted")
			}
			// The challenge is solved and the same form goes through.
			n := Solve(*r.Challenge)
			if ok := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: EncodeSolution(*r.Challenge, n)}, ""); ok.Status != StatusPassed || ok.Reason != "local" {
				t.Fatalf("solution: %+v", ok)
			}
			if again := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: EncodeSolution(*r.Challenge, n)}, ""); again.Reason != ReasonReplayed {
				t.Fatalf("the same solution twice: %+v", again)
			}
		})
	}
}

func TestCheck_TheUnreachableAddress(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, `{"success":true}`))
	h.v.Siteverify[ProviderTurnstile] = "http://127.0.0.1:1/siteverify"
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, ""); r.Status != StatusFallback {
		t.Fatalf("got %+v", r)
	}
}

func TestCheck_ALocalSolutionOnlyDuringAnOutage(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, `{"success":true}`))
	c, _ := h.v.Local.Issue()
	sol := EncodeSolution(c, Solve(c))
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: sol}, ""); r.Reason != ReasonFallbackInactive || r.HTTPStatus() != 422 {
		t.Fatalf("no outage: %+v", r)
	}
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: string(must(json.Marshal(c)))}, ""); r.Reason != ReasonFallbackInactive {
		t.Fatalf("bare JSON: %+v", r)
	}
	_ = h.v.Marks.PutCaptchaMark(ctx, FlagFallback, time.Now().Add(time.Minute).Unix())
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: string(must(json.Marshal(c)))}, ""); r.Reason != ReasonBadPayload {
		t.Fatalf("bare JSON during an outage: %+v", r)
	}
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: sol}, ""); r.Status != StatusPassed {
		t.Fatalf("outage: %+v", r)
	}
	// The flag ends: a solution is refused again.
	h.v.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	c2, _ := h.v.Local.Issue()
	if r := h.v.Check(ctx, FormLogin, Fields{CaptchaLocal: EncodeSolution(c2, Solve(c2))}, ""); r.Reason != ReasonFallbackInactive {
		t.Fatalf("after the flag: %+v", r)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestCheck_AConfigurationThatCannotWorkIsTheFallbackAndAnAlarm(t *testing.T) {
	t.Run("the provider refuses the secret (HTTP 400)", func(t *testing.T) {
		for _, code := range []string{"invalid-input-secret", "missing-input-secret", "sitekey-secret-mismatch", "invalid-keys"} {
			h := newHarness(t, jsonAnswer(400, `{"success":false,"error-codes":["`+code+`"]}`))
			r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, "")
			if r.Status != StatusFallback || r.Challenge == nil || !strings.HasPrefix(r.Reason, "misconfigured:") {
				t.Fatalf("%s: %+v", code, r)
			}
			if h.alarms.misconfigured.Load() != 1 || !h.v.FallbackActive(ctx) {
				t.Fatalf("%s: no alarm / no flag", code)
			}
		}
	})
	t.Run("no site key: no call at all", func(t *testing.T) {
		h := newHarness(t, jsonAnswer(200, `{"success":true}`))
		h.v.Settings.Env = func(k string) string {
			return map[string]string{EnvProvider: "turnstile", EnvSecret: "s"}[k]
		}
		r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, "")
		if r.Status != StatusFallback || r.Reason != "misconfigured:"+MisSiteKeyMissing || h.provider.calls.Load() != 0 {
			t.Fatalf("%+v calls=%d", r, h.provider.calls.Load())
		}
		// Told once an hour, not once a request.
		h.v.Check(ctx, FormLogin, Fields{}, "")
		if h.alarms.misconfigured.Load() != 1 {
			t.Fatalf("told %d times", h.alarms.misconfigured.Load())
		}
	})
	t.Run("the fallback off: 503", func(t *testing.T) {
		h := newHarness(t, jsonAnswer(502, ``))
		h.store[KeyFallback] = "off"
		if r := h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, ""); r.Status != StatusUnavailable || r.HTTPStatus() != 503 {
			t.Fatalf("%+v", r)
		}
	})
}

func TestCheck_MoreThanOneFallbackInTheWindowIsTold(t *testing.T) {
	h := newHarness(t, jsonAnswer(503, ``))
	for i := 0; i < 4; i++ {
		h.v.Check(ctx, FormLogin, Fields{CaptchaToken: goodToken}, "")
	}
	if h.alarms.counted.Load() != 4 || h.v.FallbackTotal() != 4 {
		t.Fatalf("counted %d", h.alarms.counted.Load())
	}
	if h.alarms.fallback.Load() != 1 {
		t.Fatalf("told %d times, want once per window", h.alarms.fallback.Load())
	}
}

func TestChallengeAnswer(t *testing.T) {
	t.Run("the server reaches the provider: no fallback", func(t *testing.T) {
		h := newHarness(t, jsonAnswer(200, `{"success":false,"error-codes":["invalid-input-response"]}`))
		if st, body := h.v.ChallengeAnswer(ctx); st != 409 || body["captcha"] != "provider" {
			t.Fatalf("%d %v", st, body)
		}
		if h.v.FallbackActive(ctx) {
			t.Fatal("flag raised")
		}
	})
	t.Run("the server cannot either: a challenge", func(t *testing.T) {
		h := newHarness(t, jsonAnswer(500, ``))
		st, body := h.v.ChallengeAnswer(ctx)
		if st != 200 || body["captcha"] != "fallback" || body["challenge"] == nil || !h.v.FallbackActive(ctx) {
			t.Fatalf("%d %v", st, body)
		}
	})
	t.Run("off", func(t *testing.T) {
		h := newHarness(t, jsonAnswer(200, ``))
		h.v.Settings.Env = func(string) string { return "" }
		if st, _ := h.v.ChallengeAnswer(ctx); st != 404 {
			t.Fatalf("%d", st)
		}
	})
}

func TestPublic_SaysNoSecret(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, ``))
	p := h.v.Public(ctx)
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "1x0000000000000000000000000000000AA") {
		t.Fatal("the secret is in the public answer")
	}
	if p.Provider != ProviderTurnstile || p.SiteKey == nil || *p.SiteKey != "1x00000000000000000000AA" || len(p.Forms) != 4 || p.Script == nil {
		t.Fatalf("%s", b)
	}
	h.v.Settings.Env = func(string) string { return "" }
	off := h.v.Public(ctx)
	if off.Provider != ProviderNone || off.SiteKey != nil || len(off.Forms) != 0 {
		t.Fatalf("%+v", off)
	}
}

func TestPass_BoundToItsLinkAndSpentOnce(t *testing.T) {
	h := newHarness(t, jsonAnswer(200, ``))
	p, exp := h.v.IssuePass("file_request:aaa")
	if time.Until(exp) > PassTTL || time.Until(exp) < PassTTL-time.Minute {
		t.Fatalf("expires %s", exp)
	}
	if h.v.SpendPass(ctx, p, "file_request:bbb") {
		t.Fatal("another link took the pass")
	}
	if !h.v.SpendPass(ctx, p, "file_request:aaa") {
		t.Fatal("its own link refused it")
	}
	if h.v.SpendPass(ctx, p, "file_request:aaa") {
		t.Fatal("spent twice")
	}
	p2, _ := h.v.IssuePass("file_request:aaa")
	h.v.Now = func() time.Time { return time.Now().Add(PassTTL + time.Second) }
	if h.v.SpendPass(ctx, p2, "file_request:aaa") {
		t.Fatal("an expired pass was taken")
	}
	for _, bad := range []string{"", "x", "a.b", p2 + "0"} {
		if h.v.SpendPass(ctx, bad, "file_request:aaa") {
			t.Fatalf("%q taken", bad)
		}
	}
}

func TestSettings_PanelThenEnvironmentThenOff(t *testing.T) {
	store := memStore{}
	box, err := secretbox.New("test-secret-key-for-captcha-settings")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{EnvProvider: "recaptcha", EnvSiteKey: "env-site", EnvSecret: "env-secret", EnvForms: "login, nonsense,file_request", EnvTimeout: "99999"}
	s := &Settings{Store: store, Box: box, Env: func(k string) string { return env[k] }, TTL: -1}

	c := s.Load(ctx)
	if c.Provider != ProviderRecaptcha || c.SiteKey != "env-site" || c.Secret != "env-secret" || strings.Join(c.Forms, ",") != "login,file_request" ||
		c.VerifyTimeout != MaxVerifyTimeoutMS*time.Millisecond || c.Sources["provider"] != SourceEnv {
		t.Fatalf("env: %+v", c)
	}

	changed, err := s.Apply(ctx, Patch{Provider: ptr("Turnstile"), SiteKey: ptr("db-site"), Secret: ptr("db-secret"), Forms: &[]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "provider,site_key,forms,secret" {
		t.Fatalf("changed %v", changed)
	}
	if strings.Contains(store[KeySecret], "db-secret") || !secretbox.IsSealed(store[KeySecret]) {
		t.Fatal("the secret is not sealed in the row")
	}
	c = s.Load(ctx)
	if c.Provider != ProviderTurnstile || c.SiteKey != "db-site" || c.Secret != "db-secret" || len(c.Forms) != 0 || c.Sources["secret"] != SourceDB {
		t.Fatalf("db: %+v", c)
	}
	if st := s.Stored(ctx); !st.HasSecret || st.SiteKey != "db-site" {
		t.Fatalf("stored %+v", st)
	}

	// An emptied field is the environment's again.
	if _, err := s.Apply(ctx, Patch{SiteKey: ptr(""), ClearForms: true, ClearSecret: true}); err != nil {
		t.Fatal(err)
	}
	c = s.Load(ctx)
	if c.SiteKey != "env-site" || c.Secret != "env-secret" || len(c.Forms) != 2 {
		t.Fatalf("cleared: %+v", c)
	}

	// A secret that does not open does NOT fall back to the environment.
	store[KeySecret] = "not-sealed"
	c = s.Load(ctx)
	if !c.SecretUnreadable || c.Secret != "" || c.Misconfiguration() != MisSecretUnreadable {
		t.Fatalf("unreadable: %+v", c)
	}
}

func TestSettings_Refusals(t *testing.T) {
	s := &Settings{Store: memStore{}, Env: func(string) string { return "" }}
	for name, p := range map[string]Patch{
		"provider": {Provider: ptr("hcaptcha")},
		"fallback": {Fallback: ptr("open")},
		"forms":    {Forms: &[]string{"register"}},
		"timeout":  {VerifyTimeoutMS: intPtr(50)},
		"site key": {SiteKey: ptr("two words")},
	} {
		var inv *Invalid
		if _, err := s.Apply(ctx, p); !errors.As(err, &inv) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.Apply(ctx, Patch{Secret: ptr("x")}); !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("no box: %v", err)
	}
}

func ptr(s string) *string { return &s }
func intPtr(n int) *int    { return &n }

// A settings table that cannot be read does not turn the protection off: the
// last good read stays in force.
func TestSettings_ADatabaseErrorKeepsTheLastGoodRead(t *testing.T) {
	store := memStore{KeyProvider: "turnstile", KeySiteKey: "k"}
	s := &Settings{Store: store, Env: func(string) string { return "" }, TTL: time.Nanosecond}
	if c := s.Load(ctx); c.Provider != ProviderTurnstile {
		t.Fatalf("first read: %+v", c)
	}
	store["__broken__"] = "1"
	time.Sleep(time.Millisecond)
	if c := s.Load(ctx); c.Provider != ProviderTurnstile || c.SiteKey != "k" {
		t.Fatalf("a failed read switched the check: %+v", c)
	}
	delete(store, "__broken__")
	store[KeyProvider] = "none"
	time.Sleep(time.Millisecond)
	if c := s.Load(ctx); c.Provider != ProviderNone {
		t.Fatalf("a good read is taken again: %+v", c)
	}
}
