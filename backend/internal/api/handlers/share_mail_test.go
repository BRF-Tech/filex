package handlers_test

// "Send by e-mail" for a public link (share_mail.go): the server writes the
// whole message from the link — its address, expiry, the item's name, kind
// and size, a file request's limits — and nothing the request says about the
// link is mailed. The mails are read off a real SMTP conversation (smtpSink,
// tenant_urls_test.go), as the recipient would get them.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// mailFix is one storage with informe.txt (4 bytes) and a buzon folder, an
// SMTP sink that is verified, and the Grants handler that mails.
type mailFix struct {
	f     *tenantFixture
	sink  *smtpSink
	g     *handlers.Grants
	file  *model.Node
	inbox *model.Node
}

func newMailFix(t *testing.T) *mailFix {
	t.Helper()
	f := newTenantFixture(t)
	sink := newSMTPSink(t)
	g := handlers.NewGrants(f.store, nil)
	g.AttachInvite(share.NewService(f.store), sink.mailer(t, f.store), tenantOperatorURL)
	g.AttachTenants(f.tenants(false))
	return &mailFix{
		f: f, sink: sink, g: g,
		file:  fileNode(t, f.store, f.storage, f.root, "docs/informe.txt", "hola"),
		inbox: mkdirNode(t, f.store, f.storage, f.root, "buzon"),
	}
}

// link makes a link as the fixture's owner.
func (m *mailFix) link(t *testing.T, opts share.CreateOpts) *model.Share {
	t.Helper()
	if opts.CreatedBy == nil {
		owner := m.f.owner.ID
		opts.CreatedBy = &owner
	}
	sh, err := share.NewService(m.f.store).Create(context.Background(), opts)
	require.NoError(t, err)
	return sh
}

func (m *mailFix) send(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/permissions/share-mail", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	m.g.ShareMail(rec, m.f.asOwner(req))
	return rec
}

// count is how many messages the sink has received so far.
func (s *smtpSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

func addresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("kisi%d@example.test", i)
	}
	return out
}

// ⭐ The C2 case: a request that names its own address, PIN, expiry, kind and
// size gets the LINK's in the mail — its real address, no PIN at all, the
// days it really has left, the file's own name and size.
func TestShareMail_TheMessageIsTheLinksNotTheRequests(t *testing.T) {
	m := newMailFix(t)
	week := time.Now().Add(7 * 24 * time.Hour)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID, PIN: "4321", ExpiresAt: &week})

	rec := m.send(t, map[string]any{
		"share": sh.Token, "email": "amigo@example.test", "locale": "en",
		// What the request used to say, every one of them a lie here.
		"path": m.f.storage.Name + "://docs/informe.txt", "url": "https://phish.example/login",
		"pin": "0000", "expires_days": 365, "is_dir": true, "size": 987654321, "mode": "drop",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	h, body := mailParts(t, m.sink.only(t))
	assert.Equal(t, "informe.txt has been shared with you", h["Subject"])
	for _, want := range []string{
		"Download it here:\n" + tenantOperatorURL + "/s/" + sh.Token,
		"File: informe.txt", "Size: 4 B", "This link is valid for 7 days.",
		"This link is protected with a PIN. The person who sent it will give you the PIN separately.",
	} {
		assert.Contains(t, body, want)
	}
	// (The token is random: it is taken out before looking for digits.)
	rest := strings.ReplaceAll(body, sh.Token, "")
	for _, never := range []string{"phish.example", "0000", "4321", "365", "Folder:", "upload"} {
		assert.NotContains(t, rest, never, "nothing the request said about the link reaches the mail")
	}

	var out struct {
		Emailed     bool     `json:"emailed"`
		Sent        []string `json:"sent"`
		PinWithheld bool     `json:"pin_withheld"`
		Message     string   `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, out.Emailed)
	assert.Equal(t, []string{"amigo@example.test"}, out.Sent)
	assert.True(t, out.PinWithheld)
	assert.Equal(t, "Email sent to 1 person. The PIN is not in the email - give it to them another way.", out.Message,
		"the composer is told, in their language, that the PIN goes another way")
}

// A file request is mailed as an upload invitation with the limits the LINK
// was made with; the request's `mode` is not asked.
func TestShareMail_AFileRequestSpellsOutItsOwnLimits(t *testing.T) {
	m := newMailFix(t)
	ds := `{"max_files":3,"max_file_size_mb":10,"allowed_ext":["pdf"]}`
	sh := m.link(t, share.CreateOpts{NodeID: m.inbox.ID, Kind: model.ShareKindDrop, DropSettings: &ds})

	rec := m.send(t, map[string]any{"share": sh.Token, "emails": []string{"musteri@example.test"}, "locale": "en"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	h, body := mailParts(t, m.sink.only(t))
	assert.Equal(t, "You've been asked to add files to buzon", h["Subject"])
	for _, want := range []string{
		"Upload your files here:\n" + tenantOperatorURL + "/d/" + sh.Token,
		"Limit: up to 3 files, 10 MB per file.", "Allowed types: pdf",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "PIN", "a link without a PIN says nothing about one")
}

// The request shape every client sent until 0.54 — a path and an address of
// its own — is not a way to send anything.
func TestShareMail_TheOldRequestShapeSendsNothing(t *testing.T) {
	m := newMailFix(t)
	rec := m.send(t, map[string]any{
		"path": m.f.storage.Name + "://docs/informe.txt", "email": "amigo@example.test",
		"url": "https://phish.example/login",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Zero(t, m.sink.count())

	rec = m.send(t, map[string]any{"share": "no-such-link", "email": "amigo@example.test"})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Zero(t, m.sink.count())
}

// One send reaches at most shareMailMaxRecipients (20) addresses.
func TestShareMail_ASendHasARecipientCap(t *testing.T) {
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	rec := m.send(t, map[string]any{"share": sh.Token, "emails": addresses(21), "locale": "en"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"too_many_recipients"`)
	assert.Contains(t, rec.Body.String(), "Send to at most 20 addresses at a time.")
	assert.Zero(t, m.sink.count(), "an over-long list sends to nobody, not to the first twenty")

	rec = m.send(t, map[string]any{"share": sh.Token, "emails": addresses(20), "locale": "en"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 20, m.sink.count())
}

// An account mails links to at most shareMailHourlyRecipients (100)
// addresses an hour, every address counted.
func TestShareMail_AnAccountHasAnHourlyBudget(t *testing.T) {
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})

	for i := 0; i < 5; i++ {
		rec := m.send(t, map[string]any{"share": sh.Token, "emails": addresses(20)})
		require.Equal(t, http.StatusOK, rec.Code, "send %d: %s", i, rec.Body.String())
	}
	rec := m.send(t, map[string]any{"share": sh.Token, "email": "bir-tane-daha@example.test", "locale": "en"})
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"rate_limited"`)
	assert.Equal(t, 100, m.sink.count())
}

// A link that no longer works is not mailed.
func TestShareMail_AnEndedLinkIsNotMailed(t *testing.T) {
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID})
	require.NoError(t, m.f.store.RevokeShare(context.Background(), sh.ID))

	rec := m.send(t, map[string]any{"share": sh.Token, "email": "amigo@example.test", "locale": "en"})
	assert.Equal(t, http.StatusGone, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "This link no longer works")
	assert.Zero(t, m.sink.count())
}

// The OS share sheet gets the mail's own words, from the same builder: the
// link's address, and no PIN.
func TestShareMessage_TheShareSheetGetsTheMailsWords(t *testing.T) {
	m := newMailFix(t)
	sh := m.link(t, share.CreateOpts{NodeID: m.file.ID, PIN: "4321"})

	req := httptest.NewRequest(http.MethodGet,
		"/api/files/permissions/share-message?share="+url.QueryEscape(sh.Token)+"&lang=tr", nil)
	rec := httptest.NewRecorder()
	m.g.ShareMessage(rec, m.f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out struct {
		Subject     string `json:"subject"`
		Body        string `json:"body"`
		PinWithheld bool   `json:"pin_withheld"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "informe.txt dosyası sizinle paylaşıldı", out.Subject)
	assert.Contains(t, out.Body, tenantOperatorURL+"/s/"+sh.Token)
	assert.Contains(t, out.Body, "Bu bağlantı bir PIN ile korunuyor.")
	assert.NotContains(t, strings.ReplaceAll(out.Body, sh.Token, ""), "4321")
	assert.True(t, out.PinWithheld)
	assert.Zero(t, m.sink.count(), "the message is answered, never sent")
}

// ⭐ Through the real router: a link is mailed only by somebody who manages
// it — its maker or an administrator — even when the caller could make links
// on the very same item. Until 0.54 any editor of the item could mail any
// address it liked "about" it.
func TestShareMail_ALinkSomebodyElseMadeIsRefused(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)
	ctx := context.Background()

	root := t.TempDir()
	code, raw := doReq(t, adminClient, http.MethodPost, srv.URL+"/api/admin/storages", model.Storage{
		Name: "sm1", Driver: "local", MountPath: "/data",
		ConfigJSON: json.RawMessage(fmt.Sprintf(`{"root":%q}`, root)),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true, RBACEnabled: true,
	})
	require.Equal(t, http.StatusOK, code, "create storage: %s", raw)
	st, err := store.GetStorageByName(ctx, "sm1")
	require.NoError(t, err)

	maker := createUser(t, srv.URL, adminClient, "maker@test.local", "MakerPass1!", model.RoleUser)
	other := createUser(t, srv.URL, adminClient, "other@test.local", "OtherPass1!", model.RoleUser)
	for _, g := range []struct {
		id    int64
		level string
	}{{maker, "owner"}, {other, "editor"}} {
		code, raw = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
			map[string]any{"path": "sm1://ortak", "user_id": g.id, "level": g.level})
		require.Equal(t, http.StatusOK, code, "grant: %s", raw)
	}
	node := fileNode(t, store, st, root, "ortak/rapor.txt", "rapor")
	sh, err := share.NewService(store).Create(ctx, share.CreateOpts{NodeID: node.ID, CreatedBy: &maker})
	require.NoError(t, err)

	makerClient, otherClient := freshClient(t), freshClient(t)
	testutil.LoginAs(t, srv, makerClient, "maker@test.local", "MakerPass1!")
	testutil.LoginAs(t, srv, otherClient, "other@test.local", "OtherPass1!")
	mail := map[string]any{"share": sh.Token, "email": "disari@example.test"}

	code, raw = doReq(t, otherClient, http.MethodPost, srv.URL+"/api/files/permissions/share-mail", mail)
	assert.Equal(t, http.StatusForbidden, code, "another editor of the item cannot mail its maker's link: %s", raw)
	code, raw = doReq(t, otherClient, http.MethodGet, srv.URL+"/api/files/permissions/share-message?share="+sh.Token, nil)
	assert.Equal(t, http.StatusForbidden, code, "nor read its message: %s", raw)
	// The old shape — the item's path and an address of the caller's choosing
	// — is not a way round it.
	code, raw = doReq(t, otherClient, http.MethodPost, srv.URL+"/api/files/permissions/share-mail", map[string]any{
		"path": "sm1://ortak/rapor.txt", "email": "disari@example.test", "url": "https://phish.example/login",
	})
	assert.Equal(t, http.StatusBadRequest, code, raw)

	// Its maker passes every gate; mail is not set up on this server, which
	// is the answer it gets.
	code, raw = doReq(t, makerClient, http.MethodPost, srv.URL+"/api/files/permissions/share-mail", mail)
	assert.Equal(t, http.StatusServiceUnavailable, code, string(raw))
	assert.True(t, strings.Contains(string(raw), "not_configured"), string(raw))
}
