package onlyoffice

// What a save callback is trusted for (callback_trust.go, filex 0.54): the
// signed payload and nothing else, a callback's shape (never an editor
// configuration), a key filex made for the document `?node=` names, and a
// saved document on the document server's own origin.
//
// ⚠ The refusal tests (down to "the new functions") drive only what the
// callback had before 0.54 (the harness, BuildConfigForNode, HandleCallback),
// so run against that code - those tests and their helpers alone, in the
// package as it was - they fail on their assertions: the file is overwritten,
// the foreign server is asked, the other session's record is gone. The unit
// tests of the new functions are at the bottom.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// dsCallback is a save callback's fields as ONLYOFFICE Docs 9.4 posts them
// (https://api.onlyoffice.com/docs/docs-api/usage-api/callback-handler/):
// everything it sends, not only what filex reads.
func dsCallback(key string, status int, savedURL string, users ...string) map[string]any {
	us := make([]any, len(users))
	acts := make([]any, len(users))
	for i, u := range users {
		us[i] = u
		acts[i] = map[string]any{"type": 0, "userid": u}
	}
	p := map[string]any{"key": key, "status": status, "users": us, "actions": acts}
	if savedURL != "" {
		p["url"] = savedURL
		p["changesurl"] = savedURL + "-changes.zip"
		p["filetype"] = "docx"
		p["lastsave"] = "2026-10-08T09:00:00.000Z"
		p["notmodified"] = false
		p["history"] = map[string]any{
			"serverVersion": "9.4.0",
			"changes":       []any{map[string]any{"created": "2026-10-08 09:00:00", "user": map[string]any{"id": "7", "name": "ada"}}},
		}
	}
	return p
}

// withTimes is claims with the iat and exp Docs signs a callback with
// (services.CoAuthoring.token.outbox.expires: five minutes by default).
func withTimes(claims map[string]any, now time.Time) map[string]any {
	out := make(map[string]any, len(claims)+2)
	for k, v := range claims {
		out[k] = v
	}
	out["iat"] = now.Unix()
	out["exp"] = now.Add(5 * time.Minute).Unix()
	return out
}

// signedBody is the callback body with its token in the body (`inBody`): the
// fields, and `token` signing them.
func signedBody(t *testing.T, p map[string]any) map[string]any {
	t.Helper()
	tok, err := signHS256(withTimes(p, time.Now()), "shh")
	require.NoError(t, err)
	body := make(map[string]any, len(p)+1)
	for k, v := range p {
		body[k] = v
	}
	body["token"] = tok
	return body
}

// headerToken is the callback's Authorization token, Docs' default: the
// fields wrapped in {"payload": …}.
func headerToken(t *testing.T, p map[string]any) string {
	t.Helper()
	tok, err := signHS256(withTimes(map[string]any{"payload": p}, time.Now()), "shh")
	require.NoError(t, err)
	return tok
}

// postCallback posts body (and, when set, the bearer token) to the callback
// for nodeID.
func (h *csvHarness) postCallback(t *testing.T, nodeID int64, body map[string]any, bearer string) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/files/onlyoffice/callback?node=%d", nodeID), bytes.NewReader(raw))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return h.svc.HandleCallback(req, nodeID)
}

// serve is what the stand-in document server hands out as the saved document.
func (h *csvHarness) serve(content string) {
	h.mu.Lock()
	h.saved = []byte(content)
	h.mu.Unlock()
}

// secondDoc is another document on the harness's storage.
func (h *csvHarness) secondDoc(t *testing.T, name, content string) *model.Node {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(h.root, name), []byte(content), 0o644))
	n, err := h.store.CreateNode(context.Background(), &model.Node{
		StorageID: h.node.StorageID, Name: name, Path: "/" + name, PathHash: pathkey.Hash(h.node.StorageID, "/"+name),
		StorageKey: "/" + name, Type: model.NodeTypeFile, Size: int64(len(content)), Mime: docxMime,
		SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

func (h *csvHarness) diskOf(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, name))
	require.NoError(t, err)
	return string(b)
}

// refusedCallback: the callback did nothing for the document server (an error,
// or {"error": 1}).
func refusedCallback(resp map[string]any, err error) bool {
	return err != nil || resp["error"] == 1
}

func TestCallback_TheEditorsConfigTokenIsNotACallback(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	vera := h.user(t, "vera@example.com", model.RoleViewer)
	u, err := h.store.GetUser(ctx, vera)
	require.NoError(t, err)
	// Opening a document to look at it is enough to be handed one.
	cfg, err := h.svc.BuildConfigForNode(ctx, h.node, u, "en", "view")
	require.NoError(t, err)
	tok := cfg.Config["token"].(string)
	key := cfg.Config["document"].(map[string]any)["key"].(string)
	h.serve("ATTACKER BYTES")

	resp, err := h.postCallback(t, h.node.ID, map[string]any{
		"key": key, "status": StatusReadyForSaving, "url": h.ds.URL + "/saved", "token": tok,
	}, "")
	assert.True(t, refusedCallback(resp, err), "the editor's config signed a save: %v %v", resp, err)
	assert.Equal(t, "V1", h.disk(t), "a viewer's editor token overwrote the document")

	resp, err = h.postCallback(t, h.node.ID, map[string]any{
		"key": key, "status": StatusReadyForSaving, "url": h.ds.URL + "/saved",
	}, tok)
	assert.True(t, refusedCallback(resp, err), "the editor's config in the Authorization header signed a save")
	assert.Equal(t, "V1", h.disk(t))
}

func TestCallback_TheBodyCannotChangeWhatTheTokenSays(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	h.serve(docxBytes)

	// Signed: closed with no change. Beside it: "ready for saving", a document.
	body := signedBody(t, dsCallback(h.sessionKey, StatusClosedNoChange, "", idStrings(ada)...))
	body["status"] = StatusReadyForSaving
	body["url"] = h.ds.URL + "/saved"
	body["filetype"] = "docx"
	resp, err := h.postCallback(t, h.node.ID, body, "")
	require.NoError(t, err)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "V1", h.disk(t), "the body's status and url were acted on, not the signed ones")
}

func TestCallback_AnotherDocumentsSessionWritesNothingHere(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "MINE")
	theirs := h.secondDoc(t, "theirs.docx", "THEIRS")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada) // Ada's session of rapor.docx
	h.serve(docxBytes)

	// A genuine, signed callback of Ada's session - posted for another
	// document (`?node=` is not signed).
	resp, err := h.postCallback(t, theirs.ID, signedBody(t, dsCallback(h.sessionKey, StatusReadyForSaving, h.ds.URL+"/saved", idStrings(ada)...)), "")
	assert.True(t, refusedCallback(resp, err), "%v %v", resp, err)
	assert.Equal(t, "THEIRS", h.diskOf(t, "theirs.docx"), "a session's save was written over another document")
	assert.Equal(t, "MINE", h.disk(t))

	// Nor does its "closed with no change" end Ada's session there.
	resp, err = h.postCallback(t, theirs.ID, signedBody(t, dsCallback(h.sessionKey, StatusClosedNoChange, "", idStrings(ada)...)), "")
	require.NoError(t, err)
	assert.Equal(t, 0, resp["error"], "a status nothing is done for is still answered")
	row, err := h.store.GetOfficeSession(ctx, h.sessionKey)
	require.NoError(t, err)
	assert.NotNil(t, row, "a callback for another document removed this session's record")
}

func TestCallback_ASavedDocumentElsewhereIsNeverFetched(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	var asked atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte("FOREIGN BYTES"))
	}))
	t.Cleanup(foreign.Close)

	resp, err := h.postCallback(t, h.node.ID, signedBody(t, dsCallback(h.sessionKey, StatusReadyForSaving, foreign.URL+"/saved", idStrings(ada)...)), "")
	assert.True(t, refusedCallback(resp, err), "%v %v", resp, err)
	assert.Zero(t, asked.Load(), "filex fetched an address that is not the document server's")
	assert.Equal(t, "V1", h.disk(t))

	// A redirect from the document server's address to another is not followed.
	via := h.ds.URL + "/redirect?to=" + url.QueryEscape(foreign.URL+"/saved")
	resp, err = h.postCallback(t, h.node.ID, signedBody(t, dsCallback(h.sessionKey, StatusReadyForSaving, via, idStrings(ada)...)), "")
	assert.True(t, refusedCallback(resp, err), "%v %v", resp, err)
	assert.Zero(t, asked.Load(), "a redirect off the document server was followed")
	assert.Equal(t, "V1", h.disk(t))
}

func TestCallback_AnExpiredTokenIsRefused(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	h.serve(docxBytes)

	p := dsCallback(h.sessionKey, StatusReadyForSaving, h.ds.URL+"/saved", idStrings(ada)...)
	claims := withTimes(p, time.Now().Add(-2*time.Hour))
	tok, err := signHS256(claims, "shh")
	require.NoError(t, err)
	body := map[string]any{"token": tok}
	resp, err := h.postCallback(t, h.node.ID, body, "")
	assert.True(t, refusedCallback(resp, err), "a token two hours past its exp was taken")
	assert.Equal(t, "V1", h.disk(t))
}

// The document server's own callbacks still save, whichever way it sends the
// token, with or without the times it signs.
func TestCallback_ADocumentServersCallbackIsWritten(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header bool
		times  bool
	}{
		{"token in the Authorization header (Docs' default)", true, true},
		{"token in the body", false, true},
		{"a token with no times", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDocHarness(t, "rapor.docx", docxMime, "V1")
			ada := h.user(t, "ada@example.com", model.RoleUser)
			h.open(t, ada)
			h.serve(docxBytes)
			p := dsCallback(h.sessionKey, StatusReadyForSaving, h.ds.URL+"/saved", idStrings(ada)...)

			var resp map[string]any
			var err error
			switch {
			case tc.header:
				resp, err = h.postCallback(t, h.node.ID, p, headerToken(t, p))
			case tc.times:
				resp, err = h.postCallback(t, h.node.ID, signedBody(t, p), "")
			default:
				tok, serr := signHS256(p, "shh")
				require.NoError(t, serr)
				body := map[string]any{"token": tok}
				for k, v := range p {
					body[k] = v
				}
				resp, err = h.postCallback(t, h.node.ID, body, "")
			}
			require.NoError(t, err)
			assert.Equal(t, 0, resp["error"], "%v", resp)
			assert.Equal(t, docxBytes, h.disk(t))
			h.sink.wait(t)
		})
	}
}

// ── the new functions ───────────────────────────────────────────────────

func TestCallbackFromToken_Shapes(t *testing.T) {
	now := time.Now()
	sign := func(c map[string]any) string {
		tok, err := signHS256(c, "shh")
		require.NoError(t, err)
		return tok
	}
	ok := dsCallback("abc-def", StatusReadyForSaving, "https://docs.example/cache/files/x/output.docx", "7")

	for _, tc := range []struct {
		name   string
		claims map[string]any
		want   error
	}{
		{"a callback, token in the body", withTimes(ok, now), nil},
		{"a callback, token in the header", withTimes(map[string]any{"payload": ok}, now), nil},
		{"filex's editor configuration", map[string]any{
			"document":     map[string]any{"key": "abc-def", "url": "https://filex.example/f"},
			"documentType": "word",
			"editorConfig": map[string]any{"callbackUrl": "https://filex.example/cb", "mode": "view", "user": map[string]any{"id": "7"}},
		}, errEditorToken},
		{"an editor configuration that also says key and status", map[string]any{
			"key": "abc-def", "status": 2, "url": "https://docs.example/x",
			"document": map[string]any{"key": "abc-def"},
		}, errEditorToken},
		{"the document server's session token", withTimes(map[string]any{
			"document": map[string]any{"key": "abc-def"}, "editorConfig": map[string]any{"user": map[string]any{"id": "7"}},
		}, now), errEditorToken},
		{"wrapped editor configuration", map[string]any{"payload": map[string]any{
			"key": "abc-def", "status": 2, "documentType": "word",
		}}, errEditorToken},
		{"a conversion request (no status)", map[string]any{
			"key": "fx00", "url": "https://filex.example/f", "filetype": "docx", "outputtype": "pdf", "async": true,
		}, errNotACallback},
		{"a status that is not a number", map[string]any{"key": "abc-def", "status": "2"}, errNotACallback},
		{"a status that is not whole", map[string]any{"key": "abc-def", "status": 2.5}, errNotACallback},
		{"no key", map[string]any{"status": 2}, errNotACallback},
		{"a url that is not a string", map[string]any{"key": "abc-def", "status": 2, "url": 7}, errNotACallback},
		{"expired", withTimes(ok, now.Add(-time.Hour)), errTokenExpired},
		{"not valid yet", func() map[string]any {
			c := withTimes(ok, now)
			c["nbf"] = now.Add(time.Hour).Unix()
			return c
		}(), errTokenNotYet},
		{"issued long ago, no exp", func() map[string]any {
			c := map[string]any{"iat": now.Add(-3 * time.Hour).Unix()}
			for k, v := range ok {
				c[k] = v
			}
			return c
		}(), errTokenTooOld},
		{"a time that is not a number", func() map[string]any {
			c := withTimes(ok, now)
			c["exp"] = "tomorrow"
			return c
		}(), errBadTokenTimes},
		{"within the clock leeway", func() map[string]any {
			c := withTimes(ok, now)
			c["exp"] = now.Add(-time.Minute).Unix()
			return c
		}(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := callbackFromToken(sign(tc.claims), "shh", now)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "abc-def", p.Key)
			assert.Equal(t, StatusReadyForSaving, p.Status)
			assert.Equal(t, "https://docs.example/cache/files/x/output.docx", p.URL)
			assert.Equal(t, "docx", p.FileType)
			assert.Equal(t, []string{"7"}, p.Users)
			assert.Equal(t, "https://docs.example/cache/files/x/output.docx-changes.zip", p.ChangesURL)
			assert.NotEmpty(t, p.History)
			assert.Empty(t, p.Token)
		})
	}

	_, err := callbackFromToken(sign(withTimes(ok, now)), "another secret", now)
	assert.Error(t, err, "another secret's signature")
}

func TestKeyBelongsTo(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	other := h.secondDoc(t, "other.docx", "OTHER")

	key := h.svc.keyFor(ctx, h.node)
	assert.True(t, h.svc.KeyBelongsTo(ctx, key, h.node))
	assert.False(t, h.svc.KeyBelongsTo(ctx, key, other), "sealed for another document")
	flip := "0"
	if key[len(key)-1] == '0' {
		flip = "1"
	}
	assert.False(t, h.svc.KeyBelongsTo(ctx, key[:len(key)-1]+flip, h.node), "a seal changed")
	assert.False(t, h.svc.KeyBelongsTo(ctx, "", h.node))

	// Another secret: the seal no longer holds (the record still can).
	changed := New(h.store, h.svc.StorageResolver, h.ds.URL, "a new secret", "https://filex.example", time.Hour)
	assert.False(t, changed.KeyBelongsTo(ctx, key, h.node))

	// A key from before 0.54 (no seal) counts while its session is recorded
	// for this document - and only for this one.
	legacy := md5Hex("opened by 0.53")
	assert.False(t, h.svc.KeyBelongsTo(ctx, legacy, h.node), "no seal, no record")
	require.NoError(t, h.store.PutOfficeSession(ctx, &model.OfficeSession{
		DocKey: legacy, NodeID: h.node.ID, Size: 2, ExpiresUnix: time.Now().Add(time.Hour).Unix(),
	}))
	assert.True(t, h.svc.KeyBelongsTo(ctx, legacy, h.node))
	assert.False(t, h.svc.KeyBelongsTo(ctx, legacy, other))
	assert.True(t, changed.KeyBelongsTo(ctx, legacy, h.node))
}

// The editor configuration carries the sealed key, and the document server's
// callback for it is acted on.
func TestBuildConfig_TheKeyIsSealedForTheDocument(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	cfg, err := h.svc.BuildConfigForNode(ctx, h.node, nil, "en", "edit")
	require.NoError(t, err)
	key := cfg.Config["document"].(map[string]any)["key"].(string)
	assert.Len(t, key, 65)
	assert.Regexp(t, `^[0-9a-f]{32}-[0-9a-f]{32}$`, key, "the document server takes [0-9A-Za-z.=_-], at most 128")
	assert.True(t, h.svc.KeyBelongsTo(ctx, key, h.node))
}
