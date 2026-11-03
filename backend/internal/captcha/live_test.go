package captcha

// Against Cloudflare's real siteverify, with its published test keys: the
// answers the classification is built on, measured rather than assumed. Off
// unless FILEX_CAPTCHA_LIVE=1 (it needs the network):
//
//	FILEX_CAPTCHA_LIVE=1 go test -run TestLive ./internal/captcha/

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The test keys Cloudflare publishes (developers.cloudflare.com/turnstile/
// troubleshooting/testing): they work on any host and are no secret.
const (
	liveSecretPasses = "1x0000000000000000000000000000000AA"
	liveSecretFails  = "2x0000000000000000000000000000000AA"
	liveDummyToken   = "XXXX.DUMMY.TOKEN.XXXX"
)

func liveVerifier(t *testing.T, secret string) *Verifier {
	t.Helper()
	if os.Getenv("FILEX_CAPTCHA_LIVE") != "1" {
		t.Skip("set FILEX_CAPTCHA_LIVE=1 to ask Cloudflare")
	}
	env := map[string]string{EnvProvider: "turnstile", EnvSiteKey: "1x00000000000000000000AA", EnvSecret: secret}
	return &Verifier{
		Settings: &Settings{Store: memStore{}, Env: func(k string) string { return env[k] }, TTL: -1},
		Marks:    NewMemoryMarks(),
		Local:    &Local{Key: []byte("live"), MaxNumber: 1000},
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func TestLive_TurnstileTestKeys(t *testing.T) {
	if r := liveVerifier(t, liveSecretPasses).Check(ctx, FormLogin, Fields{CaptchaToken: liveDummyToken}, ""); r.Status != StatusPassed {
		t.Fatalf("the passing secret: %+v", r)
	}
	r := liveVerifier(t, liveSecretFails).Check(ctx, FormLogin, Fields{CaptchaToken: liveDummyToken}, "")
	if r.Status != StatusInvalid || r.HTTPStatus() != 422 {
		t.Fatalf("the failing secret: %+v, want a 422 refusal (not the fallback)", r)
	}
}

func TestLive_AWrongSecretIsAMisconfigurationNotARefusal(t *testing.T) {
	r := liveVerifier(t, "0x0000000000000000000000000000000-not-a-secret").Check(ctx, FormLogin, Fields{CaptchaToken: liveDummyToken}, "")
	if r.Status != StatusFallback || !strings.HasPrefix(r.Reason, "misconfigured:") {
		t.Fatalf("a wrong secret: %+v, want the fallback as a misconfiguration", r)
	}
	p := liveVerifier(t, liveSecretPasses).ProbeWith(ctx, liveVerifier(t, liveSecretPasses).Settings.Load(ctx))
	if !p.Reachable || !p.SecretOK {
		t.Fatalf("the probe with the passing secret: %+v", p)
	}
}
