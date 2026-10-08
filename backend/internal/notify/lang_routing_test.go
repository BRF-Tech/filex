package notify_test

// Translated at the LAST stop (#191, fourth round, the maintainers' rule
// 2026-10-08): an event travels untranslated - its facts and catalogue keys -
// and is said only for its receiver, in the receiver's language. A person
// reads in their ACCOUNT's (every channel: the bell, a push, an email), a
// webhook in the language chosen for it (a target's own, FILEX_WEBHOOK_LANG
// for the legacy one), else the instance's. A webhook body also carries the
// message untranslated (`i18n`), for a receiver that translates for itself.
//
// ⚠ Red on the code before: every webhook was told in the instance's
// language, with no `i18n`, and the drop notice's email went out in the
// language the emitter guessed.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// oneHook records the JSON bodies one webhook receiver gets.
type oneHook struct {
	*httptest.Server
	mu   sync.Mutex
	got  []map[string]any
	raws [][]byte
}

func newOneHook(t *testing.T) *oneHook {
	t.Helper()
	h := &oneHook{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		h.mu.Lock()
		h.got = append(h.got, m)
		h.raws = append(h.raws, b)
		h.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *oneHook) only(t *testing.T) map[string]any {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.got, 1)
	return h.got[0]
}

func dropReceived(uid *int64) notify.Event {
	return notify.Event{
		Event: notify.EventDropReceived, Severity: notify.SeverityInfo,
		Title: "Yeni dosya yüklemesi", Body: "Ayşe, \"Gelen\" klasörüne dosya bıraktı.",
		Meta:   map[string]any{"folder": "Gelen", "count": 1, "uploader": "Ayşe"},
		Target: notify.DirTarget("Gelen"),
		UserID: uid,
	}
}

func TestLang_EachWebhookIsToldInItsOwnLanguage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	// The instance speaks Turkish.
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })

	english, instance, legacy := newOneHook(t), newOneHook(t), newOneHook(t)
	_, err := store.CreateWebhookTarget(ctx, &model.WebhookTarget{Name: "en", URL: english.URL, Enabled: true, Lang: "en"})
	require.NoError(t, err)
	_, err = store.CreateWebhookTarget(ctx, &model.WebhookTarget{Name: "instance", URL: instance.URL, Enabled: true})
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{
		WebhookURL:    legacy.URL,
		WebhookLang:   "en",
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{},
	})
	defer svc.Stop()
	_, err = svc.Send(ctx, dropReceived(nil))
	require.NoError(t, err)
	svc.Wait()

	// The target told in English, the one with none in the instance's
	// Turkish, the legacy webhook in FILEX_WEBHOOK_LANG's English.
	for _, c := range []struct {
		name        string
		hook        *oneHook
		lang, title string
	}{
		{"target en", english, "en", "1 file received"},
		{"target without a language", instance, "tr", "1 dosya geldi"},
		{"legacy FILEX_WEBHOOK_LANG=en", legacy, "en", "1 file received"},
	} {
		body := c.hook.only(t)
		require.Equal(t, c.title, body["title"], c.name)
		require.Equal(t, "Ayşe → Gelen", body["body"], c.name)
		// The facts stay as they are, whatever the language.
		require.Equal(t, string(notify.EventDropReceived), body["event"], c.name)

		// ⭐ The message untranslated: the keys and their values, for a
		// receiver that translates for itself - beside the sentence above.
		i18n, ok := body["i18n"].(map[string]any)
		require.True(t, ok, "%s: the body carries i18n: %v", c.name, body)
		require.Equal(t, c.lang, i18n["lang"], c.name)
		title := i18n["title"].(map[string]any)
		require.Equal(t, "server.notify.drop.received.title", title["key"], c.name)
		require.EqualValues(t, 1, title["count"], c.name)
		require.Equal(t, map[string]any{"count": "1"}, title["vars"], c.name)
		bodyPart := i18n["body"].(map[string]any)
		require.Equal(t, "server.notify.drop.received.body", bodyPart["key"], c.name)
		require.Equal(t, map[string]any{"uploader": "Ayşe", "folder": "Gelen"}, bodyPart["vars"], c.name)
	}
}

// An item inside an encrypted folder is the lock word in `i18n` too: the
// untranslated message carries no more than the sentence does.
func TestLang_TheUntranslatedMessageCarriesNoEncryptedName(t *testing.T) {
	const S = "cnCYVvOrMoH0uQKjxUUeYr9h7KREShFsI3Y"
	_, store := dbtest.NewTestDB(t)
	hook := newOneHook(t)
	svc := notify.New(store, notify.Config{WebhookURL: hook.URL, HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	_, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventFileUploaded, Severity: notify.SeverityInfo, Body: "Kasa/" + S,
		Meta:   map[string]any{"e2e_root": "ekip://Kasa"},
		Node:   &notify.NodeRef{StorageID: st.ID, Path: "Kasa/" + S, Name: S},
		Target: notify.FileTarget("Kasa/" + S),
	})
	require.NoError(t, err)
	svc.Wait()
	hook.mu.Lock()
	defer hook.mu.Unlock()
	require.Len(t, hook.raws, 1)
	require.NotContains(t, string(hook.raws[0]), `"i18n":null`)
	type part struct {
		Vars map[string]string `json:"vars"`
	}
	var body struct {
		Title string `json:"title"`
		I18n  struct {
			Title part `json:"title"`
			Body  part `json:"body"`
		} `json:"i18n"`
	}
	require.NoError(t, json.Unmarshal(hook.raws[0], &body))
	require.Equal(t, "New file: 🔒 Encrypted item", body.Title)
	require.NotEmpty(t, body.I18n.Title.Vars)
	for _, v := range body.I18n.Title.Vars {
		require.NotContains(t, v, S, "the untranslated title named the ciphertext")
	}
	for _, v := range body.I18n.Body.Vars {
		require.NotContains(t, v, S, "the untranslated body named the ciphertext")
	}
}

// A person reads in their ACCOUNT's language on every channel - not the
// instance's, not the language the emitter guessed for the email.
func TestLang_APersonsEmailIsInTheAccountsLanguage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	box := &mailbox{}
	svc, _ := digestService(t, store, &digestClock{}, notify.Config{Mail: box.send})
	defer svc.Stop()
	uid := digestUser(t, store, "okur@example.test", "tr")
	ev := dropReceived(&uid)
	// The emitter guessed English.
	ev.Mail = &notify.Mail{Lang: "en", Link: "https://dosya.example.test/admin/"}
	_, err := svc.Send(context.Background(), ev)
	require.NoError(t, err)
	svc.Wait()

	all := box.all()
	require.Len(t, all, 1)
	require.Equal(t, "tr", all[0].lang)
	require.Equal(t, "1 dosya geldi", all[0].subject)
}

func TestLang_PersonLangIsTheAccountsElseTheInstances(t *testing.T) {
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })
	require.Equal(t, "en", notify.PersonLang(&model.User{Locale: "en"}))
	require.Equal(t, "tr", notify.PersonLang(&model.User{Locale: ""}), "an account that chose none reads the instance's")
	require.Equal(t, "tr", notify.PersonLang(&model.User{Locale: "tlh"}), "a language nobody speaks is the instance's")
	require.Equal(t, "tr", notify.PersonLang(nil))
}
