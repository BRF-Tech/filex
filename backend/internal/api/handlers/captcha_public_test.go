package handlers_test

// The bot check on the public link surfaces (internal/captcha, task #294),
// through the real handlers and a real database (the shared marks live in
// captcha_marks): the PIN of a link, the upload into a file request with its
// pass, and the no-JavaScript pages that cannot carry the check. No socket is
// opened - the provider answers through the client's transport and the
// requests go through a recorder.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/captcha"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// fakeSiteverify answers siteverify in the process.
type fakeSiteverify struct {
	calls  atomic.Int64
	status int
	body   string
}

func (f *fakeSiteverify) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "siteverify.test" {
		return nil, errors.New("dial tcp: connection refused")
	}
	f.calls.Add(1)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(f.status)
	_, _ = io.WriteString(rec, f.body)
	return rec.Result(), nil
}

// newTestCaptcha is the check with Turnstile on, keys set, forms as given
// (nil = every form), the marks in store and siteverify the fake.
func newTestCaptcha(store db.Store, provider *fakeSiteverify, forms []string) *captcha.Verifier {
	env := map[string]string{
		captcha.EnvProvider: "turnstile",
		captcha.EnvSiteKey:  "1x00000000000000000000AA",
		captcha.EnvSecret:   "1x0000000000000000000000000000000AA",
	}
	if forms != nil {
		env[captcha.EnvForms] = strings.Join(forms, ",")
		if len(forms) == 0 {
			env[captcha.EnvForms] = "none-of-them"
		}
	}
	return &captcha.Verifier{
		Settings:   &captcha.Settings{Store: store, Env: func(k string) string { return env[k] }, TTL: -1},
		Marks:      store,
		Local:      &captcha.Local{Key: []byte("captcha-test-key"), MaxNumber: 3000},
		Client:     &http.Client{Transport: provider},
		Siteverify: map[string]string{captcha.ProviderTurnstile: "https://siteverify.test/v", captcha.ProviderRecaptcha: "https://siteverify.test/r"},
	}
}

type captchaPublicEnv struct {
	r        *chi.Mux
	svc      *share.Service
	store    db.Store
	st       *model.Storage
	root     string
	provider *fakeSiteverify
}

func newCaptchaPublicEnv(t *testing.T, forms []string) *captchaPublicEnv {
	t.Helper()
	mh, store, drv, st, root := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }
	svc := share.NewService(store)
	e := &captchaPublicEnv{svc: svc, store: store, st: st, root: root, provider: &fakeSiteverify{status: 200, body: `{"success":true}`}}
	ch := handlers.NewCaptcha(newTestCaptcha(store, e.provider, forms))
	shareH := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	shareH.AttachCaptcha(ch)
	dh := handlers.NewDrop(store, mh, svc, nil, nil, "")
	dh.AttachCaptcha(ch)
	pub := handlers.NewPublicAPI(store, svc)
	pub.AttachDrop(dh)
	pub.AttachCaptcha(ch)
	r := chi.NewRouter()
	r.Get("/s/{token}", shareH.HandleDownload)
	r.Post("/s/{token}", shareH.HandleDownload)
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)
	r.Post("/api/public/s/{token}/pin", pub.PIN)
	r.Post("/api/public/d/{token}/pin", pub.PIN)
	r.Post("/api/public/d/{token}/captcha", ch.DropPass)
	r.Post("/api/public/d/{token}/upload", pub.DropUpload)
	r.Get("/api/captcha/config", ch.Config)
	r.Post("/api/captcha/local/challenge", ch.LocalChallenge)
	e.r = r
	return e
}

func (e *captchaPublicEnv) do(t *testing.T, method, url string, body any, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept-Language", "en")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func (e *captchaPublicEnv) upload(t *testing.T, token, pass string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	part, err := mw.CreateFormFile("file", "hello.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(part, "hello")
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", "/api/public/d/"+token+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if pass != "" {
		req.Header.Set(captcha.PassHeader, pass)
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func captchaBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

var captchaDropN atomic.Int64

func (e *captchaPublicEnv) drop(t *testing.T, pin string) *model.Share {
	t.Helper()
	folder := mkdirNode(t, e.store, e.st, e.root, "inbox-"+strconv.FormatInt(captchaDropN.Add(1), 10))
	sh, err := e.svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, PIN: pin})
	require.NoError(t, err)
	return sh
}

func TestCaptchaDrop_TheUploadNeedsAPassForThisLinkSpentOnce(t *testing.T) {
	e := newCaptchaPublicEnv(t, nil)
	sh := e.drop(t, "")
	other := e.drop(t, "")

	rec := e.upload(t, sh.Token, "", nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	out := captchaBody(t, rec)
	assert.Equal(t, "captcha_required", out["error"])
	assert.Equal(t, "missing_pass", out["captcha"])
	assert.NotEmpty(t, out["message"], "the server says it in words")

	// The check before the upload, on a small JSON request.
	rec = e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "missing_token", captchaBody(t, rec)["captcha"])
	rec = e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{"captcha_token": "0.good_token-1"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	pass, _ := captchaBody(t, rec)["pass"].(string)
	require.NotEmpty(t, pass)

	// Another link does not take it.
	rec = e.upload(t, other.Token, pass, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "bad_pass", captchaBody(t, rec)["captcha"])

	rec = e.upload(t, sh.Token, pass, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, findUnder(e.root, "", "hello.txt"))

	// Spent.
	rec = e.upload(t, sh.Token, pass, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "bad_pass", captchaBody(t, rec)["captcha"])
}

func TestCaptchaDrop_AnOutageGivesTheLocalChallengeAndItsSolutionAPass(t *testing.T) {
	e := newCaptchaPublicEnv(t, nil)
	e.provider.status, e.provider.body = 503, "down"
	sh := e.drop(t, "")

	rec := e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{"captcha_token": "0.good_token-2"}, nil)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	out := captchaBody(t, rec)
	assert.Equal(t, "captcha_fallback", out["error"])
	assert.Equal(t, "fallback", out["captcha"])
	var c captcha.Challenge
	raw, _ := json.Marshal(out["challenge"])
	require.NoError(t, json.Unmarshal(raw, &c))
	require.NotEmpty(t, c.Signature)

	sol := captcha.EncodeSolution(c, captcha.Solve(c))
	rec = e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{"captcha_local": sol}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	pass, _ := captchaBody(t, rec)["pass"].(string)
	require.Equal(t, http.StatusOK, e.upload(t, sh.Token, pass, nil).Code)

	// The same solution twice is refused (the mark is in the database).
	rec = e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{"captcha_local": sol}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "replayed", captchaBody(t, rec)["captcha"])
}

func TestCaptchaDrop_AFormNotProtectedIsAsBefore(t *testing.T) {
	e := newCaptchaPublicEnv(t, []string{captcha.FormLogin})
	sh := e.drop(t, "")
	require.Equal(t, http.StatusOK, e.upload(t, sh.Token, "", nil).Code)
	rec := e.do(t, "POST", "/api/public/d/"+sh.Token+"/captcha", map[string]string{}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", captchaBody(t, rec)["pass"])
	assert.Equal(t, http.StatusOK, doGet(t, e.r, "/d/"+sh.Token).Code, "the no-JavaScript page is there while nothing it does is protected")
	assert.Equal(t, int64(0), e.provider.calls.Load())
}

func TestCaptchaPIN_TheCheckComesBeforeThePINAndSpendsNoAttempt(t *testing.T) {
	e := newCaptchaPublicEnv(t, nil)
	sh := e.drop(t, "1234")

	rec := e.do(t, "POST", "/api/public/d/"+sh.Token+"/pin", map[string]string{"pin": "0000"}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Equal(t, "missing_token", captchaBody(t, rec)["captcha"])
	fresh, err := e.store.GetShareByToken(context.Background(), sh.Token)
	require.NoError(t, err)
	assert.Equal(t, 0, fresh.PinFails, "a refused check is not a wrong PIN")

	rec = e.do(t, "POST", "/api/public/d/"+sh.Token+"/pin", map[string]string{"pin": "1234", "captcha_token": "0.good_token-3"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Result().Cookies(), "the unlock cookie")
}

func TestCaptchaNoJS_ThePagesThatCannotCarryTheCheckSaySo(t *testing.T) {
	e := newCaptchaPublicEnv(t, nil)
	drop := e.drop(t, "")
	pinDrop := e.drop(t, "1234")
	folder := mkdirNode(t, e.store, e.st, e.root, "shared")
	dl, err := e.svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, PIN: "4321"})
	require.NoError(t, err)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"GET /d/ (upload protected)":     getPage(t, e.r, "/d/"+drop.Token, "en"),
		"POST /d/ PIN form":              postForm(t, e.r, "/d/"+pinDrop.Token, "pin=1234"),
		"GET /s/ PIN form":               getPage(t, e.r, "/s/"+dl.Token, "en"),
		"GET /s/?pin= in the address":    getPage(t, e.r, "/s/"+dl.Token+"?pin=4321", "en"),
		"POST /s/ PIN form":              postForm(t, e.r, "/s/"+dl.Token, "pin=4321"),
		"a PIN in the upload's own form": e.upload(t, pinDrop.Token, "", map[string]string{"pin": "1234"}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, name)
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			assert.Contains(t, rec.Body.String(), "JavaScript", name)
		}
	}
	got, err := e.store.GetShareByToken(context.Background(), dl.Token)
	require.NoError(t, err)
	assert.Equal(t, 0, got.PinFails, "no PIN was compared")
}

func TestCaptchaNoJS_TheFormIsBackWhenThePINIsNotProtected(t *testing.T) {
	e := newCaptchaPublicEnv(t, []string{captcha.FormLogin})
	folder := mkdirNode(t, e.store, e.st, e.root, "shared")
	dl, err := e.svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, PIN: "4321"})
	require.NoError(t, err)
	rec := getPage(t, e.r, "/s/"+dl.Token, "en")
	assert.NotEqual(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "JavaScript needed")
}

func TestCaptchaEndpoints_ConfigAndTheLocalChallenge(t *testing.T) {
	e := newCaptchaPublicEnv(t, nil)
	rec := e.do(t, "GET", "/api/captcha/config", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.NotContains(t, rec.Body.String(), "1x0000000000000000000000000000000AA", "no secret")
	out := captchaBody(t, rec)
	assert.Equal(t, "turnstile", out["provider"])
	assert.Equal(t, "1x00000000000000000000AA", out["site_key"])
	assert.Len(t, out["forms"], 4)

	// The provider answers the server: an unloaded script is not an outage.
	e.provider.body = `{"success":false,"error-codes":["invalid-input-response"]}`
	rec = e.do(t, "POST", "/api/captcha/local/challenge", nil, nil)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "provider", captchaBody(t, rec)["captcha"])

	// It does not answer the server either: the challenge, and the flag.
	e.provider.status = 502
	rec = e.do(t, "POST", "/api/captcha/local/challenge", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, captchaBody(t, rec)["challenge"])
	rec = e.do(t, "GET", "/api/captcha/config", nil, nil)
	assert.Equal(t, true, captchaBody(t, rec)["fallback_active"])
}

func postForm(t *testing.T, r http.Handler, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
