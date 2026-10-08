package notify_test

// Why a webhook delivery was skipped is a CODE on the row and a sentence the
// server says in the reader's language (0.54 audit, A6). The admin page said
// "no webhook is set up" for every skipped row - wrong for a row whose event
// the webhooks received with another row, and for one the service stopped
// before sending.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

func TestWebhookReason_EachSkipIsSaidForWhatItWas(t *testing.T) {
	said := map[string]string{}
	for _, code := range []string{notify.SkipNoDestination, notify.SkipDigestUnnamed, notify.SkipSibling, notify.SkipStopped} {
		row := &model.Notification{WebhookStatus: string(notify.WebhookStatusSkipped), WebhookError: code}
		got := notify.WebhookReason("tr", row)
		assert.Equal(t, srvtext.Text("tr", "server.webhooks.skipped."+code, nil), got, code)
		assert.NotContains(t, got, "server.", "a catalogue key is never the sentence")
		assert.NotContains(t, got, code, "the code is not the sentence")
		said[got] = code
	}
	assert.Len(t, said, 4, "four reasons, four sentences - not one for all")
}

// A row written before 0.54 keeps the English sentence it was given; it reads
// in the reader's language too.
func TestWebhookReason_ARowWrittenBefore054ReadsInTheReadersLanguage(t *testing.T) {
	for stored, code := range map[string]string{
		"no webhook URL configured":                               notify.SkipNoDestination,
		"sent to the webhooks with another row of the same event": notify.SkipSibling,
		"service stopped before delivery":                         notify.SkipStopped,
		"no webhook target names notification.digest":             notify.SkipDigestUnnamed,
	} {
		row := &model.Notification{WebhookStatus: string(notify.WebhookStatusSkipped), WebhookError: stored}
		assert.Equal(t, srvtext.Text("en", "server.webhooks.skipped."+code, nil), notify.WebhookReason("en", row), stored)
	}
}

func TestWebhookReason_AFailureIsTheReceiversWordsAndASentRowHasNone(t *testing.T) {
	failed := &model.Notification{WebhookStatus: string(notify.WebhookStatusFailed), WebhookError: "bad: HTTP 500"}
	assert.Equal(t, "bad: HTTP 500", notify.WebhookReason("tr", failed))
	sent := &model.Notification{WebhookStatus: string(notify.WebhookStatusSent)}
	assert.Empty(t, notify.WebhookReason("tr", sent))
}

// The list a reader reads carries the sentence (SayRows), beside the code.
func TestWebhookReason_SayRowsPutsItOnTheRow(t *testing.T) {
	row := &model.Notification{Event: "file.uploaded", WebhookStatus: string(notify.WebhookStatusSkipped), WebhookError: notify.SkipSibling}
	notify.SayRows("tr", []*model.Notification{row})
	assert.Equal(t, srvtext.Text("tr", "server.webhooks.skipped.sibling", nil), row.WebhookReason)
	assert.Equal(t, notify.SkipSibling, row.WebhookError, "the code stays for a script")
}
