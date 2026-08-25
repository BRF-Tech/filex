package handlers_test

// The drop link's PUBLIC PAGES — the PIN gate, the uploader and the error
// pages a stranger sees before they ever reach a byte of storage.
//
// These exist because on 2026-08-25 a PIN-protected drop link on fm.example.com
// rendered a gate with an empty <title>, an empty heading, an unlabelled input
// and a blank submit button. Nothing errored: the template asks for
// {{.T.pin_heading}}, Drop passed no T at all, html/template renders a missing
// key as the empty string, and the Execute error was assigned to _. The old
// tests all drove the UPLOAD path, so a page that rendered nothing readable
// still passed every one of them.
//
// So: assert on what the visitor can actually read, in both languages, and on
// the one thing that cannot be faked — that no user-visible element comes out
// empty.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// emptyTagRe finds a user-visible element rendered with nothing inside it —
// exactly the shape the blank PIN gate had (<h1></h1>, <title></title>, an
// empty <button>). Attributes are allowed; content is not.
var emptyTagRe = regexp.MustCompile(`(?s)<(title|h1|button|p class="sub")[^>]*>\s*</(title|h1|button|p)>`)

// assertNoBlankChrome fails with the offending markup rather than a bare
// boolean, so a regression names the element it broke.
func assertNoBlankChrome(t *testing.T, body string) {
	t.Helper()
	if m := emptyTagRe.FindString(body); m != "" {
		t.Fatalf("public page rendered an empty user-visible element: %q", m)
	}
	if strings.Contains(body, `<html lang="">`) {
		t.Fatalf(`public page rendered <html lang=""> — the language never reached the template`)
	}
	if strings.Contains(body, "<no value>") {
		t.Fatalf("public page rendered a missing template key: <no value>")
	}
}

// mintDrop creates a drop link (optionally PIN-protected) on a fresh folder and
// returns the token plus the PIN the server generated.
func mintDrop(t *testing.T, r http.Handler, store db.Store, st *model.Storage, root, folder string, withPIN bool) (token, pin string) {
	t.Helper()
	mkdirNode(t, store, st, root, folder)
	payload := map[string]any{"path": "main://" + folder, "kind": "drop"}
	if withPIN {
		payload["password"] = true
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/files/share", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out struct {
		Token string `json:"token"`
		Share struct {
			PIN string `json:"password_pin"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotEmpty(t, out.Token)
	if withPIN {
		require.NotEmpty(t, out.Share.PIN, "share create should return the generated PIN")
	}
	return out.Token, out.Share.PIN
}

func getPage(t *testing.T, r http.Handler, url, acceptLang string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestDropPage_PINGateHasText is the regression test for the blank gate: the
// page a stranger lands on must READ as a PIN prompt, in their language.
func TestDropPage_PINGateHasText(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gizli", true)

	for _, tc := range []struct {
		name, lang string
		want       []string
	}{
		{"turkish", "tr-TR,tr;q=0.9", []string{"PIN girin", "Bu paylaşım PIN korumalı", "Kilidi aç"}},
		{"english", "en-GB,en;q=0.9", []string{"Enter PIN", "This share is PIN-protected", "Unlock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getPage(t, r, "/d/"+tok, tc.lang)
			require.Equal(t, http.StatusOK, rec.Code)
			body := rec.Body.String()
			assertNoBlankChrome(t, body)
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
			// The input must be labelled for anyone not reading the screen.
			assert.NotContains(t, body, `aria-label=""`)
		})
	}
}

// TestDropPage_WrongPINSaysSo: a rejected PIN comes back with a readable
// reason, in the page's own language — not a blank card.
func TestDropPage_WrongPINSaysSo(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gizli", true)

	req := httptest.NewRequest("POST", "/d/"+tok, strings.NewReader("pin=000000"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "tr")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	body := rec.Body.String()
	assertNoBlankChrome(t, body)
	assert.Contains(t, body, "PIN yanlış")
}

// TestDropPage_UploaderHasText: the uploader itself speaks the visitor's
// language, script strings included — it used to be hard-coded Turkish for
// every visitor on Earth.
func TestDropPage_UploaderHasText(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)

	tr := getPage(t, r, "/d/"+tok, "tr")
	require.Equal(t, http.StatusOK, tr.Code)
	assertNoBlankChrome(t, tr.Body.String())
	assert.Contains(t, tr.Body.String(), "Dosya gönder")
	assert.Contains(t, tr.Body.String(), "Dosyaları buraya bırakın")

	en := getPage(t, r, "/d/"+tok, "en")
	require.Equal(t, http.StatusOK, en.Code)
	assertNoBlankChrome(t, en.Body.String())
	assert.Contains(t, en.Body.String(), "Send files")
	assert.Contains(t, en.Body.String(), "Drop your files here")
	// The script's own messages travel with the page, so an English visitor
	// does not get a Turkish error the moment something goes wrong.
	assert.Contains(t, en.Body.String(), "storage is unreachable")
	assert.NotContains(t, en.Body.String(), "Gönderiliyor")
}

// TestDropPage_UnknownTokenHasText: even the 404 has to say something.
func TestDropPage_UnknownTokenHasText(t *testing.T) {
	r, _, _, _, _ := newDropFixture(t)
	rec := getPage(t, r, "/d/yokboylebirtoken", "tr")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assertNoBlankChrome(t, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Bulunamadı")
}

// ─────────────── storage down: what the uploader is told ───────────────

// writeFailDriver is a real driver whose writes fail — the shape of an object
// store answering 503, which is what Hetzner did for eleven hours on
// 2026-08-25 while every drop into an S3-backed folder returned a bare 500.
type writeFailDriver struct {
	storage.Driver
	err error
}

func (d *writeFailDriver) Write(ctx context.Context, path string, r io.Reader, size int64) error {
	return d.err
}

func (d *writeFailDriver) Mkdir(ctx context.Context, path string) error { return d.err }

// newFailingDropFixture is newDropFixture with the storage layer answering
// errors instead of writing.
func newFailingDropFixture(t *testing.T, writeErr error) (*chi.Mux, db.Store, *model.Storage, string) {
	t.Helper()
	_, store, drv, st, root := newMutateFixture(t)
	failing := &writeFailDriver{Driver: drv, err: writeErr}
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return failing, nil
	}
	mh := handlers.NewManager(store, resolver)
	svc := share.NewService(store)
	shareH := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	dh := handlers.NewDrop(store, mh, svc, nil, nil, "")
	r := chi.NewRouter()
	r.Post("/api/files/share", shareH.HandleCreate)
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)
	return r, store, st, root
}

// TestDrop_StorageDownIsNotA500: when the backing storage cannot take the
// bytes, the uploader must be told that the STORAGE is unavailable — with a
// 503, not a 500 that reads like "you broke it, try again" and sends somebody
// retrying into a wall for eleven hours.
func TestDrop_StorageDownIsNotA500(t *testing.T) {
	r, store, st, root := newFailingDropFixture(t, fmt.Errorf("s3: ServiceUnavailable: Service is unable to handle request"))
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)

	rec := doDropUpload(t, r, "/d/"+tok, "", nil, []fpart{{name: "rapor.txt", content: "x"}})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "storage_unavailable", out["error"])
}

// TestDrop_OutOfSpaceIsNotAnOutage: a full disk is a different answer from a
// dead one — the visitor is told to tell the owner, not to come back later.
func TestDrop_OutOfSpaceIsNotAnOutage(t *testing.T) {
	r, store, st, root := newFailingDropFixture(t, quotaExceededErr())
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)

	rec := doDropUpload(t, r, "/d/"+tok, "", nil, []fpart{{name: "rapor.txt", content: "x"}})
	require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "quota_exceeded", out["error"])
}

// quotaExceededErr is the error a driver surfaces when the account behind the
// link is out of room — wrapped, because that is how it travels in practice.
func quotaExceededErr() error {
	return fmt.Errorf("driver refused the write: %w", quota.ErrQuotaExceeded)
}
