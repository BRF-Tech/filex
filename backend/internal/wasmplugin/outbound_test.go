package wasmplugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

type fakeNotify struct {
	events []notify.Event
}

func (f *fakeNotify) Send(_ context.Context, e notify.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

type fakeMailer struct {
	sent []string
	err  error
}

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, to+"|"+subject+"|"+body)
	return nil
}

// cannedTransport answers every request itself and records what it saw.
type cannedTransport struct {
	seen []*http.Request
}

func (c *cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.seen = append(c.seen, r)
	return &http.Response{
		StatusCode: 200, Header: http.Header{"X-Answer": []string{"yes"}, "Set-Cookie": []string{"secret=1"}},
		Body: io.NopCloser(strings.NewReader("pong " + r.URL.Path)), Request: r,
	}, nil
}

func TestOutbound_UsersNotifyMailHTTP_ThroughTheGuest(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	dbtest.SeedRegularUser(t, h.reg.opts.Store, "ada@test.local", "AdaPass1!")
	dbtest.SeedRegularUser(t, h.reg.opts.Store, "bob@test.local", "BobPass1!")
	nf := &fakeNotify{}
	ml := &fakeMailer{}
	tr := &cannedTransport{}
	h.reg.SetNotify(nf)
	h.reg.SetMailer(ml)
	h.reg.SetHTTPTransport(tr)
	h.writeFile(t, "x.txt", "x")

	job, err := h.runJob(t, p, "outbound", []string{"x.txt"}, "en")
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, job.Status)
	parts := strings.Split(job.Message, "|")
	require.Len(t, parts, 5, job.Message)

	// users_lookup: "a" matches ada (and the seeded admin, if any) — but not bob.
	assert.True(t, strings.HasPrefix(parts[0], "users:"), parts[0])
	assert.NotEqual(t, "users:0", parts[0])

	// notify_send: one plugin.notice with both languages in meta.
	assert.Equal(t, "notify:1", parts[1])
	require.Len(t, nf.events, 1)
	ev := nf.events[0]
	assert.Equal(t, notify.EventPluginNotice, ev.Event)
	assert.Equal(t, "Hello from echo", ev.Title)
	assert.Equal(t, "echo", ev.Meta["plugin"])
	// The app's name as a reader knows it, per language — what the bell
	// prints before the message instead of the install id.
	assert.Equal(t, "Echo Fixture", ev.Meta["plugin_label_en"])
	assert.Equal(t, "Yankı", ev.Meta["plugin_label_tr"])
	assert.Equal(t, "Yankı selam eder", ev.Meta["title_tr"])
	assert.Equal(t, "v", ev.Meta["k"])
	assert.Equal(t, job.ID, ev.Meta["job"])

	// mail_send: the app's name is on the footer, filex is the sender.
	assert.Equal(t, "mail:ok", parts[2])
	require.Len(t, ml.sent, 1)
	assert.True(t, strings.HasPrefix(ml.sent[0], "ada@example.test|Subject|Body"), ml.sent[0])
	assert.Contains(t, ml.sent[0], "Sent by the Echo Fixture app on filex")

	// http_request: allowed host answered, cookie stripped, UA names the app.
	assert.Equal(t, "http:200:pong /hello", parts[3])
	require.Len(t, tr.seen, 1)
	assert.Equal(t, "1", tr.seen[0].Header.Get("X-Probe"))
	assert.Contains(t, tr.seen[0].Header.Get("User-Agent"), "filex-app/echo")

	// A host outside the grant never reaches the transport.
	assert.Contains(t, parts[4], "permission_denied")
	assert.Len(t, tr.seen, 1)
}

func TestOutbound_UnavailableWhenNotWired_AndMailRateLimit(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "x.txt", "x")
	job, err := h.runJob(t, p, "outbound", []string{"x.txt"}, "en")
	require.NoError(t, err)
	parts := strings.Split(job.Message, "|")
	require.Len(t, parts, 5, job.Message)
	assert.Contains(t, parts[1], "unavailable")
	assert.Contains(t, parts[2], "unavailable")

	// Mailer that fails, and the per-plugin hourly window.
	ml := &fakeMailer{err: errors.New("smtp down")}
	h.reg.SetMailer(ml)
	job, _ = h.runJob(t, p, "outbound", []string{"x.txt"}, "en")
	assert.Contains(t, strings.Split(job.Message, "|")[2], "smtp down")
	p.mailRate.count = mailPerHour
	ml.err = nil
	job, _ = h.runJob(t, p, "outbound", []string{"x.txt"}, "en")
	assert.Contains(t, strings.Split(job.Message, "|")[2], "busy")
	assert.Empty(t, ml.sent)
}

func TestRefusedHost_PrivateAndLocalRanges(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.5", "192.168.1.10", "169.254.169.254", "0.0.0.0", "fe80::1", "fc00::1"} {
		assert.True(t, netguard.Refused(net.ParseIP(ip)), ip)
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		assert.False(t, netguard.Refused(net.ParseIP(ip)), ip)
	}
	assert.True(t, netguard.Refused(nil))
}

func TestLookupUsers_MatchesEmailNameUsername_CapsAndSkipsDisabled(t *testing.T) {
	h := newHarness(t, nil)
	dbtest.SeedRegularUser(t, h.reg.opts.Store, "zeynep@test.local", "ZeyPass1!")
	rows, err := h.reg.LookupUsers(context.Background(), "ZEY")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "zeynep@test.local", rows[0].Email)
	rows, _ = h.reg.LookupUsers(context.Background(), "nobody-like-this")
	assert.Empty(t, rows)
}

// An app's notice keeps EVERY language the app wrote it in, so a German
// reader's bell prints the German the e-Signature app ships, not its
// English (2026-09-22: only `_en`/`_tr` were kept and a German account read
// "admin@local asks you to sign a document" under "e-Signature:").
func TestNoticeMeta_EveryLanguageTheAppWrote(t *testing.T) {
	label := wire.Text{"en": "e-Signature", "tr": "e-İmza", "de": "E-Signatur", "es": "Firma electrónica"}
	title := wire.Text{"en": "Please sign", "tr": "Lütfen imzalayın", "de": "Bitte unterschreiben", "fr": "Veuillez signer", "pt-BR": "Assine, por favor"}
	body := wire.Text{"en": "It waits.", "de": "Es wartet.", "xx yy": "bad tag", "es": "   "}
	m := noticeMeta("sign", label, title, body)

	// English and Turkish as before — the desktop app and stored rows read them.
	assert.Equal(t, "sign", m["plugin"])
	assert.Equal(t, "e-Signature", m["plugin_label_en"])
	assert.Equal(t, "e-İmza", m["plugin_label_tr"])
	assert.Equal(t, "Please sign", m["title_en"])
	assert.Equal(t, "Lütfen imzalayın", m["title_tr"])
	assert.Equal(t, "It waits.", m["body_tr"], "a missing Turkish body still falls back to English, as before")

	// Every further language, exactly as the app wrote it.
	assert.Equal(t, "E-Signatur", m["plugin_label_de"])
	assert.Equal(t, "Firma electrónica", m["plugin_label_es"])
	assert.Equal(t, "Bitte unterschreiben", m["title_de"])
	assert.Equal(t, "Veuillez signer", m["title_fr"])
	assert.Equal(t, "Assine, por favor", m["title_pt-br"], "a region tag is kept, lower-cased")
	assert.Equal(t, "Es wartet.", m["body_de"])

	// Nothing invented: no key for a language the app did not write, an
	// empty text, or a tag that is not one.
	assert.NotContains(t, m, "title_es")
	assert.NotContains(t, m, "body_es")
	assert.NotContains(t, m, "body_xx yy")
}

// A Text with hundreds of keys cannot make one notification row heavy.
func TestNoticeMeta_LanguagesAreBounded(t *testing.T) {
	title := wire.Text{"en": "Hi"}
	for i := 0; i < 40; i++ {
		title[fmt.Sprintf("l%c%c", 'a'+i/26, 'a'+i%26)] = "x"
	}
	m := noticeMeta("p", wire.Text{"en": "P"}, title, nil)
	n := 0
	for k := range m {
		if strings.HasPrefix(k, "title_") && k != "title_en" && k != "title_tr" {
			n++
		}
	}
	assert.Equal(t, maxNoticeLangs, n)
}
