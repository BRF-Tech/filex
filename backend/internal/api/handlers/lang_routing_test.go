package handlers_test

// Translated at the LAST stop (#191, fourth round, the maintainers' rule
// 2026-10-08): a message travels untranslated and is said only for the one
// who receives it, in THEIR language - an account's own for a person, the
// language chosen for a receiver no person stands behind (a webhook's
// setting, the form's language for a bare address), else the instance's.
// Never the sender's screen.
//
// ⚠ Red on the code before: a share mail to several addresses went out in
// ONE language (the composer's), a webhook target had no language of its own
// (always the instance's) and the web panel kept drawing a language the
// person had changed on the desktop app.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// mailsByRecipient is every message the sink received, by its To: address.
func mailsByRecipient(t *testing.T, s *smtpSink) map[string]map[string]string {
	t.Helper()
	s.mu.Lock()
	msgs := append([]string(nil), s.msgs...)
	s.mu.Unlock()
	out := map[string]map[string]string{}
	for _, raw := range msgs {
		h, _ := mailParts(t, raw)
		out[strings.TrimSpace(h["To"])] = h
	}
	return out
}

func TestShareMail_EachRecipientReadsInTheirOwnLanguage(t *testing.T) {
	m := newMailFix(t)
	ctx := context.Background()
	// An address that is somebody's account: its owner reads Turkish.
	_, err := m.f.store.CreateUser(ctx, "okur@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	// The form says English: the bare address reads English, the account
	// its owner's Turkish - one request, two languages.
	rec := m.send(t, map[string]any{
		"share": sh.Token, "emails": []string{"okur@example.test", "yabanci@example.test"}, "locale": "en",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := mailsByRecipient(t, m.sink)
	require.Len(t, got, 2)
	assert.Equal(t, "tr", got["okur@example.test"]["Content-Language"])
	assert.Equal(t, "informe.txt dosyası sizinle paylaşıldı", got["okur@example.test"]["Subject"])
	assert.Equal(t, "en", got["yabanci@example.test"]["Content-Language"])
	assert.Equal(t, "informe.txt has been shared with you", got["yabanci@example.test"]["Subject"])
}

func TestShareMail_ABareAddressWithNoChosenLanguageReadsTheInstances(t *testing.T) {
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	// No language for the recipient: the instance's (FILEX_DEFAULT_LOCALE),
	// not the sender's.
	rec := m.send(t, map[string]any{"share": sh.Token, "email": "yabanci@example.test"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := mailsByRecipient(t, m.sink)
	require.Len(t, got, 1)
	assert.Equal(t, "tr", got["yabanci@example.test"]["Content-Language"])
	assert.Equal(t, "informe.txt dosyası sizinle paylaşıldı", got["yabanci@example.test"]["Subject"])
}

// A webhook target is a receiver no person stands behind: it has the
// language chosen for it (migration 00105), checked against the languages
// the server speaks; "" is the instance's.
func TestAdminWebhooks_ATargetHasALanguageOfItsOwn(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Notify = notify.New(d.Store, notify.Config{HTTPTimeout: time.Second, RetryBackoffs: []time.Duration{}})
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name": "tr-hook", "url": "https://example.com/hook", "lang": "tr",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		ID   int64  `json:"id"`
		Lang string `json:"lang"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, "tr", created.Lang)
	stored, err := store.GetWebhookTarget(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "tr", stored.Lang)

	// A language the server does not speak is refused, on create and on patch.
	resp = whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name": "bad", "url": "https://example.com/hook2", "lang": "tlh-x-klingon",
	})
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(raw), "invalid_lang")

	// Back to the instance's.
	resp = whDoJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/webhooks/"+strconv.FormatInt(created.ID, 10), map[string]any{"lang": ""})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stored, err = store.GetWebhookTarget(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "", stored.Lang)
}

// One language per person, whichever surface chose it: the desktop app sets
// it, and the web panel's document - which holds the old one and outranks
// the account on the web - says the new one too.
func TestUserPrefs_ALanguageChosenOnOneSurfaceIsEverySurfaces(t *testing.T) {
	h, store, ada, _ := newPrefsFixture(t)
	ctx := context.Background()

	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"theme":"dark","locale":"tr"}}`).Code)
	ada.Locale = "tr"
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "desktop", `{"prefs":{"locale":"en"}}`).Code)

	got, err := store.GetUser(ctx, ada.ID)
	require.NoError(t, err)
	assert.Equal(t, "en", got.Locale)
	web := prefsGet(t, h, ada, "web").Body.String()
	assert.Contains(t, web, `"locale":"en"`, "the web document follows the account")
	assert.NotContains(t, web, `"locale":"tr"`)
	assert.Contains(t, web, `"theme":"dark"`, "nothing else in it moves")

	// A document that holds no language is left as it is: it reads the
	// account's anyway.
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"palette":"lilac"}}`).Code)
	ada.Locale = "en"
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "desktop", `{"prefs":{"locale":"tr"}}`).Code)
	assert.NotContains(t, prefsGet(t, h, ada, "web").Body.String(), `"locale"`)
}

// #191 follow-up (the maintainers' rule, 2026-10-08): a mail to somebody with
// no account here is in the language PICKED for them, else the server's - the
// sender's screen language is not a choice for the recipient, and the form
// sends a language only when somebody picked one. The composer's own answer
// stays in the composer's language.
func TestShareMail_NothingPickedIsTheServersLanguageAndTheAnswerTheComposers(t *testing.T) {
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	// The composer reads English (their account), and picked nothing.
	rec := m.send(t, map[string]any{"share": sh.Token, "email": "yabanci@example.test"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := mailsByRecipient(t, m.sink)
	assert.Equal(t, "tr", got["yabanci@example.test"]["Content-Language"], "the server's language, not the composer's")
	var out struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, srvtext.Plural("en", "server.share_mail.sent", 1, nil), out.Message, "the composer's answer is in the composer's language")
}

func TestShareMessage_TheSheetIsInThePickedLanguageElseTheServers(t *testing.T) {
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	subject := func(query string) string {
		req := httptest.NewRequest(http.MethodGet, "/api/files/permissions/share-message?share="+url.QueryEscape(sh.Token)+query, nil)
		req.Header.Set("Accept-Language", "en-US")
		rec := httptest.NewRecorder()
		m.g.ShareMessage(rec, m.f.asOwner(req))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			Subject string `json:"subject"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out.Subject
	}
	// Nothing picked: the server's Turkish, though the sender reads English.
	assert.Equal(t, "informe.txt dosyası sizinle paylaşıldı", subject(""))
	// Picked: that language.
	assert.Equal(t, "informe.txt has been shared with you", subject("&lang=en"))
}

func TestInvite_ANewAccountWithNothingPickedStartsInTheServersLanguage(t *testing.T) {
	srvtext.SetDefault("tr")
	t.Cleanup(func() { srvtext.SetDefault("") })
	got := inviteOn(t, false, tenantHost, map[string]any{
		"email": "yeni@example.test", "level": model.GrantViewer, "create_user": true, "role": model.RoleViewer,
	})
	h, _ := mailParts(t, got.mail)
	assert.Equal(t, "tr", h["Content-Language"], "no language picked: the server's, never the inviter's screen")
}
