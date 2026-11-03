package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/captcha"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// newCaptchaVerifier builds the bot check (internal/captcha) from the
// installation: its settings in the settings table (the secret sealed with
// FILEX_SECRET_KEY), its shared state in captcha_marks, its alarms on the
// operator's notifications and its count on /metrics.
//
// ⚠ The local fallback's key is derived from FILEX_SECRET_KEY, so every
// instance behind one address checks the challenges the others issue. Without
// it the key is this process's own: correct on one instance, and a challenge
// issued by another is refused (the trade the share unlock cookie makes).
func newCaptchaVerifier(d *Deps) *captcha.Verifier {
	var box *secretbox.Box
	if b, err := secretbox.New(d.Cfg.SecretKey); err == nil && b.Enabled() {
		box = b
	}
	v := &captcha.Verifier{
		Settings: &captcha.Settings{Store: d.Store, Box: box},
		Local:    &captcha.Local{Key: captchaLocalKey(d.Cfg.SecretKey)},
		Client:   &http.Client{},
		// Test-only (an e2e run's "the provider cannot be reached"); empty in
		// production.
		Siteverify: map[string]string{
			captcha.ProviderTurnstile: strings.TrimSpace(os.Getenv(captcha.EnvTurnstileSiteverify)),
			captcha.ProviderRecaptcha: strings.TrimSpace(os.Getenv(captcha.EnvRecaptchaSiteverify)),
		},
	}
	if d.Store != nil {
		v.Marks = d.Store
	}
	v.Alarms = captcha.Alarms{
		Counted: func(kind string) { metrics.CaptchaFallbacks.WithLabelValues(kind).Inc() },
		Fallback: func(count int, window time.Duration, cause, form string) {
			if d.Notify == nil {
				return
			}
			_, _ = d.Notify.Send(context.Background(), notify.CaptchaFallback(count, int(window/time.Minute), cause, form))
		},
		Misconfigured: func(reason, form string) {
			if d.Notify == nil {
				return
			}
			_, _ = d.Notify.Send(context.Background(), notify.CaptchaMisconfigured(reason, form))
		},
	}
	return v
}

// captchaLocalKey is the local fallback's HMAC key: derived from the
// instance secret, else this process's own.
func captchaLocalKey(secret string) []byte {
	if s := strings.TrimSpace(secret); s != "" {
		sum := sha256.Sum256([]byte("captcha-local:" + s))
		return sum[:]
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	slog.Debug("captcha: no FILEX_SECRET_KEY; the local fallback's key is this process's own")
	return key
}

// captchaFrameSource is the provider's frame origin while the check is on,
// for the pages' frame-src (pageFrameSources).
func captchaFrameSource(v *captcha.Verifier, r *http.Request) string {
	if v == nil {
		return ""
	}
	return captcha.FrameOrigin(v.Config(r.Context()))
}
