package notify_test

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

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// ONE event, the SAME words on every channel (the maintainers' decision,
// 2026-10-08): the bell (what GET /api/notifications answers - SayRows in
// the reader's language), the push to their phone (laid out as the page's
// pop-up: the instance's name over "<title> - <body>"), the email (the title
// as the subject, the body as the text) and the webhook (the same sentence in
// the instance's language). The desktop app's toast is the bell's row as the
// API answers it (desktop/test/notifications.test.ts).
//
// ⚠ Red on the code before it: the push said "Gelen - Gelen: dosya isteğine
// 1 yükleme" while the bell said "1 dosya geldi", the email went out as the
// emitter's "Yeni dosya yüklemesi" and the webhook carried the emitter's
// title; notify.SayRows does not exist.

// webhookBodies records every webhook delivery's JSON body, by event.
type webhookBodies struct {
	*httptest.Server
	mu  sync.Mutex
	got map[string][]map[string]any
}

func newWebhookBodies(t *testing.T) *webhookBodies {
	t.Helper()
	w := &webhookBodies{got: map[string][]map[string]any{}}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		w.mu.Lock()
		w.got[r.Header.Get("X-Filex-Event")] = append(w.got[r.Header.Get("X-Filex-Event")], m)
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(w.Close)
	return w
}

func (w *webhookBodies) of(event string) []map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]map[string]any(nil), w.got[event]...)
}

// channels is one service with every channel switched on: push (to ps), email
// (into box) and the legacy webhook (to hook).
func channels(t *testing.T, store db.Store, clock *digestClock, ps *pushService, box *mailbox, hook *webhookBodies) (notify.Service, notify.Digests, notify.Pushes) {
	t.Helper()
	svc, dg := digestService(t, store, clock, notify.Config{
		Push:       pushConfig(t, ps, "push-test-secret"),
		Mail:       box.send,
		WebhookURL: hook.URL,
	})
	p, ok := svc.(notify.Pushes)
	require.True(t, ok)
	return svc, dg, p
}

// bellOf is what the person's bell says - exactly what the handler answers:
// their unread rows, said in their language.
func bellOf(t *testing.T, svc notify.Service, uid int64, lang string) []*model.Notification {
	t.Helper()
	rows := unread(t, svc, uid)
	notify.SayRows(lang, rows)
	return rows
}

func TestText_OneEventSaysTheSameOnEveryChannel(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps, box, hook := newPushService(t), &mailbox{}, newWebhookBodies(t)
	svc, _, p := channels(t, store, &digestClock{}, ps, box, hook)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	ctx := context.Background()
	withNotifyPacks(t, srvtext.StaticPacks{"de": {
		"server.notify.drop.received.title":     "{count} Dateien empfangen",
		"server.notify.drop.received.title_one": "{count} Datei empfangen",
	}})

	for _, c := range []struct {
		locale, title, body string
	}{
		{"tr", "1 dosya geldi", "Ayşe → Gelen"},
		{"en", "1 file received", "Ayşe → Gelen"},
		// A language pack's language: its words where it has them.
		{"de", "1 Datei empfangen", "Ayşe → Gelen"},
	} {
		uid := digestUser(t, store, c.locale+"@example.test", c.locale)
		_, err := p.PushSubscribe(ctx, uid, ps.handset(t, c.locale))
		require.NoError(t, err)
		_, err = svc.Send(ctx, notify.Event{
			Event: notify.EventDropReceived, Severity: notify.SeverityInfo,
			// The emitter's own words: a row's fallback, said by nobody now.
			Title: "Yeni dosya yüklemesi", Body: "Ayşe, \"Gelen\" klasörüne dosya bıraktı.",
			Meta:   map[string]any{"folder": "Gelen", "count": 1, "uploader": "Ayşe"},
			Node:   &notify.NodeRef{StorageID: st.ID, Path: "Gelen", Name: "Gelen"},
			Target: notify.DirTarget("Gelen"),
			UserID: &uid,
			Mail:   &notify.Mail{Lang: c.locale, Link: "https://dosya.example.test/admin/"},
		})
		require.NoError(t, err)
		svc.Wait()
		require.Equal(t, 1, flush(t, p))

		// The bell.
		bell := bellOf(t, svc, uid, c.locale)
		require.Len(t, bell, 1)
		require.Equal(t, c.title, bell[0].Title, c.locale)
		require.Equal(t, c.body, bell[0].Body, c.locale)
		// The push: the same two strings, as the page's pop-up lays them out.
		got := ps.received(c.locale)
		require.Len(t, got, 1)
		require.Equal(t, "filex", got[0].Title)
		require.Equal(t, bell[0].Title+" - "+bell[0].Body, got[0].Body, c.locale)
		// The email: the title is the subject, the body the text.
		var mail *sentMail
		for _, m := range box.all() {
			if m.to == c.locale+"@example.test" {
				m := m
				mail = &m
			}
		}
		require.NotNil(t, mail, "no email for %s", c.locale)
		require.Equal(t, bell[0].Title, mail.subject, c.locale)
		require.Equal(t, bell[0].Body+"\n\nhttps://dosya.example.test/admin/", mail.body, c.locale)
	}

	// The webhook: the same sentence, in the instance's language - English
	// here - never the emitter's words.
	hooks := hook.of(string(notify.EventDropReceived))
	require.Len(t, hooks, 3)
	for _, h := range hooks {
		require.Equal(t, "1 file received", h["title"])
		require.Equal(t, "Ayşe → Gelen", h["body"])
	}
}

func TestText_ADigestSaysTheSameOnEveryChannel(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps, box, hook := newPushService(t), &mailbox{}, newWebhookBodies(t)
	clock := &digestClock{}
	svc, dg, p := channels(t, store, clock, ps, box, hook)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	ctx := context.Background()
	uid := digestUser(t, store, "sahip@example.test", "tr")
	holds(t, svc, dg, uid, string(notify.EventDropReceived))
	_, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	for _, folder := range []string{"Gelen", "Gelen", "Arsiv"} {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventDropReceived, Severity: notify.SeverityInfo,
			Meta:   map[string]any{"folder": folder, "count": 1},
			Node:   &notify.NodeRef{StorageID: st.ID, Path: folder, Name: folder},
			Target: notify.DirTarget(folder),
			UserID: &uid,
			Mail:   &notify.Mail{Lang: "tr", Link: "https://dosya.example.test/admin/"},
		})
		require.NoError(t, err)
	}
	svc.Wait()
	require.Zero(t, flush(t, p), "a held row was pushed on its own")
	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	svc.Wait()
	require.Equal(t, 1, flush(t, p))

	bell := bellOf(t, svc, uid, "tr")
	require.Len(t, bell, 1)
	require.Equal(t, "3 bildirim", bell[0].Title)
	require.Equal(t, "Gelen: dosya isteğine 2 yükleme; Arsiv: dosya isteğine 1 yükleme", bell[0].Body)

	got := ps.received("phone")
	require.Len(t, got, 1)
	require.Equal(t, bell[0].Title+" - "+bell[0].Body, got[0].Body)

	mails := box.all()
	require.Len(t, mails, 1)
	require.Equal(t, bell[0].Title, mails[0].subject)
	for _, line := range strings.Split(bell[0].Body, "; ") {
		require.Contains(t, mails[0].body, "\n"+line, "the email says each of the bell's lines on its own line")
	}
}

// An item inside an end-to-end encrypted folder: no channel without the key
// prints its ciphertext name, all of them say the lock word in the same
// sentence, and the bell's row says where it stands for a browser that has
// the key.
func TestText_AnEncryptedNameIsTheLockWordOnEveryChannel(t *testing.T) {
	const S = "cnCYVvOrMoH0uQKjxUUeYr9h7KREShFsI3Y"
	_, store := dbtest.NewTestDB(t)
	ps, box, hook := newPushService(t), &mailbox{}, newWebhookBodies(t)
	svc, _, p := channels(t, store, &digestClock{}, ps, box, hook)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	ctx := context.Background()
	uid := digestUser(t, store, "ayse@example.test", "tr")
	_, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	_, err = svc.Send(ctx, notify.Event{
		Event: notify.EventFileUploaded, Severity: notify.SeverityInfo, Body: "Kasa/" + S,
		Meta:   map[string]any{"e2e_root": "ekip://Kasa"},
		Node:   &notify.NodeRef{StorageID: st.ID, Path: "Kasa/" + S, Name: S},
		Target: notify.FileTarget("Kasa/" + S),
		UserID: &uid,
		Mail:   &notify.Mail{Lang: "tr"},
	})
	require.NoError(t, err)
	svc.Wait()
	require.Equal(t, 1, flush(t, p))

	bell := bellOf(t, svc, uid, "tr")
	require.Len(t, bell, 1)
	require.Equal(t, "Yeni dosya: 🔒 Şifreli öğe", bell[0].Title)
	require.Equal(t, "Kasa/🔒 Şifreli öğe", bell[0].Body)
	require.NotNil(t, bell[0].E2E, "the bell says where the name stands")
	require.Equal(t, "ekip://Kasa/"+S, bell[0].E2E.Names[0].Wire)

	push := ps.received("phone")[0]
	mail := box.all()[0]
	hooks := hook.of(string(notify.EventFileUploaded))
	require.Len(t, hooks, 1)
	require.Equal(t, bell[0].Title+" - "+bell[0].Body, push.Body)
	require.Equal(t, bell[0].Title, mail.subject)
	require.Equal(t, bell[0].Body, mail.body)
	require.Equal(t, "New file: 🔒 Encrypted item", hooks[0]["title"])
	for _, said := range []string{push.Body, mail.subject, mail.body, hooks[0]["title"].(string), hooks[0]["body"].(string)} {
		require.NotContains(t, said, S, "a channel printed the ciphertext name")
	}
}
