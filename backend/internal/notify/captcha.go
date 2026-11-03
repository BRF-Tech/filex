package notify

import (
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The bot check's alarms (internal/captcha): operator events, said from
// their facts in each reader's language. The reason inside the sentence is a
// phrase of its own (`server.notify.captcha.reason.*` / `.cause.*`), so the
// row carries the whole sentence per language (title_<lang>, body_<lang>),
// the arrangement of LegacyAccountElsewhere.

const (
	captchaFallbackTitle      = srvtext.Prefix + "notify.captcha.fallback_title"
	captchaFallbackBody       = srvtext.Prefix + "notify.captcha.fallback_body"
	captchaMisconfiguredTitle = srvtext.Prefix + "notify.captcha.misconfigured_title"
	captchaMisconfiguredBody  = srvtext.Prefix + "notify.captcha.misconfigured_body"
)

// CaptchaFallback is "more than one form used the local fallback in the
// window": count of them in minutes, the last cause (a code: timeout,
// connection, http_502, misconfigured:<reason>...), the form.
func CaptchaFallback(count, minutes int, cause, form string) Event {
	meta := map[string]any{"count": count, "minutes": minutes, "cause": cause, "form": form}
	say := func(lang, key string) string {
		return srvtext.Text(lang, key, srvtext.Vars{
			"count": strconv.Itoa(count), "minutes": strconv.Itoa(minutes), "reason": captchaCauseWords(lang, cause),
		})
	}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = say(lang, captchaFallbackTitle)
		meta["body_"+lang] = say(lang, captchaFallbackBody)
	}
	lang := srvtext.Pick()
	return Event{Event: EventCaptchaFallback, Severity: SeverityWarning,
		Title: say(lang, captchaFallbackTitle), Body: say(lang, captchaFallbackBody), Meta: meta}
}

// CaptchaMisconfigured is "a provider is chosen and the keys cannot work":
// reason is site_key_missing, secret_missing, secret_unreadable or the
// provider's own code (invalid-input-secret...).
func CaptchaMisconfigured(reason, form string) Event {
	meta := map[string]any{"reason": reason, "form": form}
	say := func(lang, key string) string {
		return srvtext.Text(lang, key, srvtext.Vars{"reason": captchaReasonWords(lang, reason)})
	}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = say(lang, captchaMisconfiguredTitle)
		meta["body_"+lang] = say(lang, captchaMisconfiguredBody)
	}
	lang := srvtext.Pick()
	return Event{Event: EventCaptchaMisconfigured, Severity: SeverityError,
		Title: say(lang, captchaMisconfiguredTitle), Body: say(lang, captchaMisconfiguredBody), Meta: meta}
}

// captchaReasonWords says a misconfiguration; an unknown code as it is.
func captchaReasonWords(lang, reason string) string {
	key := srvtext.Prefix + "notify.captcha.reason." + strings.ReplaceAll(reason, "-", "_")
	if srvtext.Has(key) {
		return srvtext.Text(lang, key, nil)
	}
	return reason
}

// captchaCauseWords says why the provider was not reached.
func captchaCauseWords(lang, cause string) string {
	if r, ok := strings.CutPrefix(cause, "misconfigured:"); ok {
		return captchaReasonWords(lang, r)
	}
	name := cause
	switch {
	case strings.HasPrefix(cause, "http_5"), cause == "internal-error":
		name = "server_error"
	case strings.HasPrefix(cause, "bad_response"):
		name = "bad_response"
	}
	key := srvtext.Prefix + "notify.captcha.cause." + name
	if srvtext.Has(key) {
		return srvtext.Text(lang, key, nil)
	}
	return cause
}
