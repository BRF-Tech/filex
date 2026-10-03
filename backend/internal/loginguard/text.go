package loginguard

import (
	"math"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The sentences a caller reads. They come from the server catalogue
// (srvtext, `server.login.*`) in the reader's language — never an
// `if lang == "tr"` pair — and an unknown identifier reads exactly what a real
// one does.

// wait renders a duration the way a person reads it: whole seconds under a
// minute, whole minutes (rounded UP — never promise a door opens sooner than it
// does) from a minute on.
func wait(lang string, d time.Duration) string {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	if secs < 60 {
		return srvtext.Plural(lang, "server.login.wait_seconds", secs, nil)
	}
	mins := (secs + 59) / 60
	return srvtext.Plural(lang, "server.login.wait_minutes", mins, nil)
}

// RetryAfterSeconds is the Retry-After header's value: whole seconds, at least 1.
func RetryAfterSeconds(d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}

// Message is what to tell the caller about a failed attempt: how many tries are
// left and at which failure the lock falls — or, when this failure locked, when
// it opens.
func (o Outcome) Message(lang string) string {
	if o.Unlimited {
		return srvtext.Text(lang, "server.login.failed", nil)
	}
	if o.Locked {
		return lockedText(lang, o.Scope, o.RetryAfter)
	}
	key := "server.login.failed_remaining"
	if o.Scope == model.LoginThrottleIP {
		key = "server.login.failed_remaining_ip"
	}
	return srvtext.Plural(lang, key, o.Remaining, srvtext.Vars{"limit": strconv.Itoa(o.Limit)})
}

// Message is what to tell a caller whose attempt was refused because a lock is
// in force.
func (v Verdict) Message(lang string) string {
	return lockedText(lang, v.Scope, v.RetryAfter)
}

func lockedText(lang, scope string, d time.Duration) string {
	key := "server.login.locked"
	if scope == model.LoginThrottleIP {
		key = "server.login.locked_ip"
	}
	return srvtext.Text(lang, key, srvtext.Vars{"wait": wait(lang, d)})
}
