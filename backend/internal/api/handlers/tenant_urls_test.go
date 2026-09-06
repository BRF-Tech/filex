package handlers_test

// Multi-tenant absolute-URL tests.
//
// One filex install serves several customers, each on its own hostname. Every
// absolute URL it hands out — share links, file-request links, the wss://
// realtime endpoint, upload-ticket URLs and every link inside an e-mail — must
// be built on the TENANT's host, not on the operator's FILEX_PUBLIC_URL.
//
// Before internal/tenanturl only handlers.Auth.redirectBase resolved the
// origin per request; every other builder concatenated PublicURL. These tests
// pin each of those sites, in both directions:
//
//   - multi-tenant, host is a known tenant → the tenant's origin;
//   - multi-tenant, host is NOT a known tenant (the host-header injection
//     case) → the configured PublicURL, never the forged host;
//   - single-tenant → PublicURL, unchanged, request never consulted.
//
// The e-mail sites are covered through a real (in-process) SMTP server so the
// assertion is on the bytes a recipient actually receives, not on an
// intermediate string.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

const (
	tenantOperatorURL = "https://operator.test" // FILEX_PUBLIC_URL
	tenantHost        = "files.tenant-a.test"   // a provisioned tenant
	tenantOrigin      = "https://files.tenant-a.test"
	forgedHost        = "evil.example" // never provisioned
)

// assertOrigin asserts got starts with wantPrefix and, when it does not, says
// what it actually got — the whole point of these tests is which hostname
// came out.
func assertOrigin(t *testing.T, got, wantPrefix, msg string) {
	t.Helper()
	assert.Truef(t, strings.HasPrefix(got, wantPrefix),
		"wrong origin: %s\n  got:         %s\n  want prefix: %s", msg, got, wantPrefix)
}

// ───────────────────────── fixture ─────────────────────────

type tenantFixture struct {
	store    db.Store
	storage  *model.Storage
	root     string
	resolver func(int64) (storage.Driver, error)
	provider *model.Provider
	owner    *model.User
}

// newTenantFixture builds a store with ONE storage that belongs to ONE
// provider whose host is tenantHost, plus an admin user to act as the caller.
func newTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	_, store, drv, st, root := newMutateFixture(t)
	st.RBACEnabled = true
	require.NoError(t, store.UpdateStorage(context.Background(), st))

	p := seedProvider(t, store, &model.Provider{
		Slug: "tenant-a", Host: tenantHost, AuthType: model.AuthTypeLocal,
	})
	require.NoError(t, store.LinkProviderStorage(context.Background(), p.ID, st.ID))

	owner, err := store.CreateUser(context.Background(), "owner@tenant-a.test", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	return &tenantFixture{
		store: store, storage: st, root: root, provider: p, owner: owner,
		resolver: func(id int64) (storage.Driver, error) {
			if id != st.ID {
				return nil, fmt.Errorf("unknown id %d", id)
			}
			return drv, nil
		},
	}
}

// tenants is the resolver routes.go wires into every handler.
func (f *tenantFixture) tenants(multi bool) tenanturl.Resolver {
	return tenanturl.New(f.store, tenantOperatorURL, multi)
}

// asOwner puts the fixture's admin on the request context (the handlers under
// test all sit behind auth middleware in production).
func (f *tenantFixture) asOwner(r *http.Request) *http.Request {
	return r.WithContext(auth.WithUser(r.Context(), f.owner))
}

// ───────────────────────── ws.go: the wss:// endpoint ─────────────────────────

// wsURLFor mints a realtime ticket on `host` and returns the ws_url handed to
// the browser, which packages/core/src/lib/realtime.ts opens verbatim.
func wsURLFor(t *testing.T, f *tenantFixture, multi bool, host string) string {
	t.Helper()
	wsh := handlers.NewWS(f.store, nil, realtime.NewHub(), realtime.NewTicketStore(), tenantOperatorURL)
	wsh.AttachTenants(f.tenants(multi))

	req := httptest.NewRequest(http.MethodPost, "/api/files/ws-ticket", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	wsh.Ticket(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp struct {
		WsURL string `json:"ws_url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.WsURL
}

func TestTenantURLs_WSTicket(t *testing.T) {
	f := newTenantFixture(t)

	assert.Equal(t, "wss://files.tenant-a.test/api/ws", wsURLFor(t, f, true, tenantHost),
		"a tenant's browser must be told to open ITS OWN socket, not the operator's")
	assert.Equal(t, "wss://operator.test/api/ws", wsURLFor(t, f, true, forgedHost),
		"forged Host: the endpoint falls back to PublicURL and never names evil.example")
	assert.Equal(t, "wss://operator.test/api/ws", wsURLFor(t, f, false, tenantHost),
		"single-tenant: PublicURL, whatever Host says")
}

// ───────────────────────── share.go: /s/ and /d/ links ─────────────────────────

func shareRouter(t *testing.T, f *tenantFixture, multi bool) *chi.Mux {
	t.Helper()
	sh := handlers.NewShare(share.NewService(f.store), f.store, f.resolver, tenantOperatorURL, sharezip.New(t.TempDir()))
	sh.AttachTenants(f.tenants(multi))
	r := chi.NewRouter()
	r.Post("/api/files/share", sh.HandleCreate)
	r.Get("/api/files/share", sh.HandleList)
	return r
}

// createShareOn posts a share (or drop) create on `host` and returns its url.
func createShareOn(t *testing.T, r http.Handler, f *tenantFixture, host string, nodeID int64, kind string) string {
	t.Helper()
	payload := map[string]any{"node_id": nodeID}
	if kind != "" {
		payload["kind"] = kind
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = host
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out struct {
		URL   string `json:"url"`
		Share struct {
			URL string `json:"url"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	if out.URL != "" {
		return out.URL
	}
	return out.Share.URL
}

func TestTenantURLs_ShareAndDropLinks(t *testing.T) {
	f := newTenantFixture(t)
	dir := mkdirNode(t, f.store, f.storage, f.root, "docs")

	multi := shareRouter(t, f, true)
	assertOrigin(t, createShareOn(t, multi, f, tenantHost, dir.ID, ""), tenantOrigin+"/s/",
		"a link the tenant sends to a customer must be on the tenant's host")
	assertOrigin(t, createShareOn(t, multi, f, tenantHost, dir.ID, model.ShareKindDrop), tenantOrigin+"/d/",
		"file-request links too")

	forged := createShareOn(t, multi, f, forgedHost, dir.ID, "")
	assertOrigin(t, forged, tenantOperatorURL+"/s/", "forged Host falls back to PublicURL")
	assert.NotContains(t, forged, forgedHost, "a forged Host must never reach a minted link")

	single := shareRouter(t, f, false)
	assertOrigin(t, createShareOn(t, single, f, tenantHost, dir.ID, ""), tenantOperatorURL+"/s/",
		"single-tenant installs keep PublicURL")

	// GET /api/files/share (the "existing links" list) resolves the same way.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/files/share?node_id=%d", dir.ID), nil)
	req.Host = tenantHost
	rec := httptest.NewRecorder()
	multi.ServeHTTP(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list struct {
		Shares []map[string]any `json:"shares"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.NotEmpty(t, list.Shares)
	for _, s := range list.Shares {
		assertOrigin(t, s["url"].(string), tenantOrigin+"/",
			"listed links must match the ones handed out at create time")
	}
}

// ───────────────────────── drop.go: the owner e-mail ─────────────────────────

func TestTenantURLs_DropOwnerMail(t *testing.T) {
	run := func(t *testing.T, multi bool, host string) string {
		t.Helper()
		f := newTenantFixture(t)
		sink := newSMTPSink(t)
		ml := sink.mailer(t, f.store)

		folder := mkdirNode(t, f.store, f.storage, f.root, "inbox")
		mh := handlers.NewManager(f.store, f.resolver)
		svc := share.NewService(f.store)
		dh := handlers.NewDrop(f.store, mh, svc, nil, ml, tenantOperatorURL)
		dh.AttachTenants(f.tenants(multi))

		r := chi.NewRouter()
		r.Post("/d/{token}", dh.Upload)

		sh, err := svc.Create(context.Background(), share.CreateOpts{
			NodeID: folder.ID, Kind: model.ShareKindDrop, CreatedBy: &f.owner.ID,
		})
		require.NoError(t, err)

		rec := dropUploadOn(t, r, "/d/"+sh.Token, host)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return sink.only(t)
	}

	// The folder owner gets a link to their admin UI. On the operator's host
	// they have no session and no files.
	assert.Contains(t, run(t, true, tenantHost), tenantOrigin+"/admin/")

	forged := run(t, true, forgedHost)
	assert.Contains(t, forged, tenantOperatorURL+"/admin/")
	assert.NotContains(t, forged, forgedHost, "a forged Host must never reach an e-mail")

	assert.Contains(t, run(t, false, tenantHost), tenantOperatorURL+"/admin/")
}

func dropUploadOn(t *testing.T, r http.Handler, url, host string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file[]", "hello.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(part, "hello world")
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Host = host
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// ───────────────────────── grants.go: the three invite e-mails ────────────────

type inviteOutcome struct {
	resp map[string]any
	mail string
}

func inviteOn(t *testing.T, multi bool, host string, body map[string]any) inviteOutcome {
	t.Helper()
	f := newTenantFixture(t)
	sink := newSMTPSink(t)

	g := handlers.NewGrants(f.store, nil)
	g.AttachInvite(share.NewService(f.store), sink.mailer(t, f.store), tenantOperatorURL)
	g.AttachTenants(f.tenants(multi))

	// The "shared" outcome needs the target indexed.
	mkdirNode(t, f.store, f.storage, f.root, "docs")
	if v, ok := body["_existing_user"]; ok && v.(bool) {
		_, err := f.store.CreateUser(context.Background(), body["email"].(string), "x", model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		delete(body, "_existing_user")
	}
	body["path"] = f.storage.Name + "://docs"

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/files/permissions/invite", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Host = host
	rec := httptest.NewRecorder()
	g.Invite(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, true, out["emailed"], "these assertions are about the MAIL; it has to have gone out")
	return inviteOutcome{resp: out, mail: sink.only(t)}
}

// The grant notice sent to somebody who already has an account.
func TestTenantURLs_GrantMail(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"email": "gr@tenant-a.test", "level": model.GrantViewer, "_existing_user": true}
	}
	assert.Contains(t, inviteOn(t, true, tenantHost, base()).mail, tenantOrigin+"/admin/explore")

	forged := inviteOn(t, true, forgedHost, base()).mail
	assert.Contains(t, forged, tenantOperatorURL+"/admin/explore")
	assert.NotContains(t, forged, forgedHost)

	assert.Contains(t, inviteOn(t, false, tenantHost, base()).mail, tenantOperatorURL+"/admin/explore")
}

// ⚠ The worst of the class: a customer's brand-new user gets an e-mail with a
// temporary password and a link to a login page. On the operator's host that
// login cannot succeed, and the operator's hostname is disclosed to every
// tenant that ever adds a user.
func TestTenantURLs_AccountCreatedMail(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"email": "new@tenant-a.test", "level": model.GrantViewer,
			"create_user": true, "role": model.RoleViewer}
	}
	got := inviteOn(t, true, tenantHost, base()).mail
	assert.Contains(t, got, tenantOrigin+"/admin/")
	assert.NotContains(t, got, tenantOperatorURL,
		"the operator's hostname must not appear anywhere in a tenant user's welcome mail")

	forged := inviteOn(t, true, forgedHost, base()).mail
	assert.Contains(t, forged, tenantOperatorURL+"/admin/")
	assert.NotContains(t, forged, forgedHost)

	assert.Contains(t, inviteOn(t, false, tenantHost, base()).mail, tenantOperatorURL+"/admin/")
}

// The share-link fallback (no account, no create_user): the link goes both in
// the mail and in the response the panel shows.
func TestTenantURLs_InviteShareFallback(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"email": "guest@elsewhere.test", "level": model.GrantViewer}
	}
	got := inviteOn(t, true, tenantHost, base())
	assert.Contains(t, got.mail, tenantOrigin+"/s/")
	assertOrigin(t, got.resp["url"].(string), tenantOrigin+"/s/", "")

	forged := inviteOn(t, true, forgedHost, base())
	assert.Contains(t, forged.mail, tenantOperatorURL+"/s/")
	assert.NotContains(t, forged.mail, forgedHost)

	assert.Contains(t, inviteOn(t, false, tenantHost, base()).mail, tenantOperatorURL+"/s/")
}

// ─────────── ai_ops.go: no *http.Request in scope, tenant from DATA ───────────

// aiShareURL drives POST /api/ai/share. The AI/MCP/ShareX surfaces build this
// URL from a context, not from a request, so the tenant has to come from the
// node's storage.
func aiShareURL(t *testing.T, f *tenantFixture, multi bool, path string) string {
	t.Helper()
	ai := handlers.NewAI(f.store, f.resolver, share.NewService(f.store), tenantOperatorURL, nil)
	ai.AttachTenants(f.tenants(multi))

	body, _ := json.Marshal(map[string]any{"path": path})
	req := httptest.NewRequest(http.MethodPost, "/api/ai/share", bytes.NewReader(body))
	req.Host = forgedHost // deliberately hostile: this surface must ignore it
	rec := httptest.NewRecorder()
	ai.Share(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.URL
}

func TestTenantURLs_AIShare(t *testing.T) {
	f := newTenantFixture(t)
	mkdirNode(t, f.store, f.storage, f.root, "docs")
	p := f.storage.Name + "://docs"

	got := aiShareURL(t, f, true, p)
	assertOrigin(t, got, tenantOrigin+"/s/",
		"the storage belongs to tenant-a, so its share link does too")
	assert.NotContains(t, got, forgedHost)

	assertOrigin(t, aiShareURL(t, f, false, p), tenantOperatorURL+"/s/", "")
}

// Upload tickets are the same shape: minted from a context, handed to an
// integration as a curl line.
func TestTenantURLs_UploadTicket(t *testing.T) {
	f := newTenantFixture(t)
	mkdirNode(t, f.store, f.storage, f.root, "docs")

	ticketURL := func(multi bool) string {
		ai := handlers.NewAI(f.store, f.resolver, nil, tenantOperatorURL, nil)
		ai.AttachTickets(handlers.NewUploadTicketStore())
		ai.AttachTenants(f.tenants(multi))

		body, _ := json.Marshal(map[string]any{"path": f.storage.Name + "://docs/report.pdf"})
		req := httptest.NewRequest(http.MethodPost, "/api/ai/upload/ticket", bytes.NewReader(body))
		req.Host = forgedHost
		rec := httptest.NewRecorder()
		ai.UploadTicket(rec, f.asOwner(req))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			URL  string `json:"url"`
			Curl string `json:"curl"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Contains(t, out.Curl, out.URL, "the copy-paste line must carry the same URL")
		return out.URL
	}

	assertOrigin(t, ticketURL(true), tenantOrigin+"/u/", "")
	assertOrigin(t, ticketURL(false), tenantOperatorURL+"/u/", "")
}

// ───────────────────────── in-process SMTP sink ─────────────────────────

// smtpSink is a minimal SMTP server that keeps every message body it receives,
// so the e-mail assertions above read the bytes a recipient would get rather
// than an intermediate string. It speaks just enough of RFC 5321 for
// net/smtp with TLS mode "none".
type smtpSink struct {
	addr string
	mu   sync.Mutex
	msgs []string
}

func newSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &smtpSink{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpSink) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	say := func(l string) { _, _ = fmt.Fprintf(c, "%s\r\n", l) }
	say("220 sink ESMTP")
	var body strings.Builder
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.msgs = append(s.msgs, body.String())
				s.mu.Unlock()
				body.Reset()
				say("250 2.0.0 Ok")
				continue
			}
			body.WriteString(line + "\n")
			continue
		}
		switch verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); verb {
		case "EHLO", "HELO":
			say("250-sink")
			say("250 8BITMIME")
		case "DATA":
			inData = true
			say("354 End data with <CR><LF>.<CR><LF>")
		case "QUIT":
			say("221 Bye")
			return
		default:
			say("250 2.0.0 Ok")
		}
	}
}

// mailer returns a mailer.Service pointed at this sink, already "verified" so
// Send actually delivers instead of falling back to the on-screen link.
func (s *smtpSink) mailer(t *testing.T, store db.Store) *mailer.Service {
	t.Helper()
	ctx := context.Background()
	host, port, err := net.SplitHostPort(s.addr)
	require.NoError(t, err)
	for k, v := range map[string]string{
		mailer.KeyHost: host,
		mailer.KeyPort: port,
		mailer.KeyFrom: "filex@operator.test",
		mailer.KeyTLS:  "none",
	} {
		require.NoError(t, store.UpsertSetting(ctx, k, v))
	}
	m := mailer.New(store)
	require.NoError(t, m.Verify(ctx))
	require.True(t, m.Verified())
	return m
}

// only returns the single message the sink received, failing if there is not
// exactly one — an assertion that grepped several mails would be a weak one.
func (s *smtpSink) only(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.msgs, 1, "expected exactly one e-mail")
	return s.msgs[0]
}
