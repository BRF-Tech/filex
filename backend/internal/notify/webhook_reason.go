package notify

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Why a row's webhook delivery was skipped. The row keeps the CODE in its
// webhook_error column (status "skipped"); the words are the server
// catalogue's (`server.webhooks.skipped.<code>`), said in the reader's
// language when the row is read (WebhookReason).
//
// ⚠ Before 0.54 the row kept one of four English sentences and the admin
// page printed "no webhook is set up" for every one of them - wrong for two:
// a row whose event the webhooks had already received with another row, and
// a row the service stopped before sending.
const (
	// SkipNoDestination: no webhook URL and no enabled target that takes
	// the event.
	SkipNoDestination = "no_destination"
	// SkipDigestUnnamed: a digest goes only to a target that names
	// notification.digest, and none does.
	SkipDigestUnnamed = "digest_unnamed"
	// SkipSibling: the webhooks received this event with another row of it
	// (one row per recipient, one delivery per event).
	SkipSibling = "sibling"
	// SkipStopped: the service was shutting down before the delivery began.
	SkipStopped = "stopped"
)

// legacySkips maps the English a row written before 0.54 kept to its code,
// so an old row reads in the reader's language too.
var legacySkips = map[string]string{
	"no webhook URL configured":                               SkipNoDestination,
	"sent to the webhooks with another row of the same event": SkipSibling,
	"service stopped before delivery":                         SkipStopped,
}

// skipCode is the code a skipped row's webhook_error holds, "" when it holds
// none the catalogue knows.
func skipCode(stored string) string {
	stored = strings.TrimSpace(stored)
	switch stored {
	case SkipNoDestination, SkipDigestUnnamed, SkipSibling, SkipStopped:
		return stored
	}
	if c, ok := legacySkips[stored]; ok {
		return c
	}
	if strings.HasPrefix(stored, "no webhook target names ") {
		return SkipDigestUnnamed
	}
	return ""
}

// WebhookReason is the second half of a row's webhook cell, in lang: why a
// delivery was skipped, in the server's words; for a failed delivery, the
// receiver's own error as it came back (those are not ours to translate);
// "" otherwise.
func WebhookReason(lang string, n *model.Notification) string {
	if n == nil {
		return ""
	}
	switch WebhookStatus(n.WebhookStatus) {
	case WebhookStatusSkipped:
		code := skipCode(n.WebhookError)
		if code == "" {
			code = SkipNoDestination
		}
		return srvtext.Text(lang, "server.webhooks.skipped."+code, nil)
	case WebhookStatusFailed:
		return n.WebhookError
	}
	return ""
}
