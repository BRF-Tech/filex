package handlers_test

// Uploaders (0.60): a person's upload destinations with their own key, the
// upload door (POST /api/uploads) and each upload's deletion link (/x/{token}).
// Driven through the real router (newStagedFixtureWith): the self-service
// routes, the door, the public pages and every door a leaked key must NOT open.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// pngBytes is enough of a PNG for http.DetectContentType to say image/png.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func newUploadersFixture(t *testing.T) *stagedFixture {
	t.Helper()
	return newStagedFixtureWith(t, func(d *api.Deps) { d.ACL = acl.New(d.Store) })
}

// local turns a link the server made on its public URL into one this test's
// server answers.
func (f *stagedFixture) local(u string) string {
	return strings.Replace(u, "http://test.local", f.srv.URL, 1)
}

type madeUploader struct {
	Uploader struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Storage    string `json:"storage"`
		Folder     string `json:"folder"`
		Visibility string `json:"visibility"`
		LinkDays   int    `json:"link_days"`
		Available  bool   `json:"available"`
	} `json:"uploader"`
	Key   string `json:"key"`
	Setup struct {
		Endpoint   string         `json:"endpoint"`
		ShareX     map[string]any `json:"sharex"`
		ShareXFile string         `json:"sharex_file"`
		Curl       string         `json:"curl"`
	} `json:"setup"`
	LinkDaysClamped bool `json:"link_days_clamped"`
}

// makeUploader POSTs /api/auth/uploaders as client and decodes the answer.
func makeUploader(t *testing.T, f *stagedFixture, client *http.Client, body map[string]any) (int, madeUploader, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/auth/uploaders", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out madeUploader
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

type uploadAnswer struct {
	URL          string `json:"url"`
	DeletionURL  string `json:"deletion_url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Error        string `json:"error"`
	Code         string `json:"code"`
}

// sendUpload is the request ShareX sends: multipart `file`, the key as a
// bearer. extra adds form fields a client might try.
func sendUpload(t *testing.T, f *stagedFixture, key, name string, content []byte, extra map[string]string) (int, uploadAnswer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range extra {
		require.NoError(t, mw.WriteField(k, v))
	}
	part, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, _ = part.Write(content)
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/uploads", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out uploadAnswer
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

// plainGet fetches u with no credential at all and no redirects followed.
func plainDo(t *testing.T, method, u string, form url.Values) (int, string, http.Header) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	require.NoError(t, err)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// The whole round trip ShareX makes: an uploader made in filex, its one-file
// settings, the upload with the key as a bearer, and a public link that opens
// in the browser.
func TestUploaders_MadeOnceUploadedWithItsKeyLinkedPublicly(t *testing.T) {
	f := newUploadersFixture(t)
	code, made, raw := makeUploader(t, f, f.client, map[string]any{"name": "Screenshots", "storage": "main", "folder": "shots"})
	require.Equal(t, http.StatusCreated, code, raw)
	require.True(t, strings.HasPrefix(made.Key, "fxu_"), "an uploader's key says what it is")
	assert.Equal(t, "public", made.Uploader.Visibility, "public is the default")
	assert.Equal(t, "main", made.Uploader.Storage)
	assert.True(t, made.Uploader.Available)
	assert.DirExists(t, filepath.Join(f.rootDir, "shots"), "the folder is made with the uploader")

	// The settings are built on the server, from the key it just made.
	sx := made.Setup.ShareX
	assert.Equal(t, "ImageUploader, TextUploader, FileUploader", sx["DestinationType"], "ONE file for images, text and files")
	assert.Equal(t, "http://test.local/api/uploads", sx["RequestURL"])
	assert.Equal(t, "POST", sx["RequestMethod"])
	assert.Equal(t, "MultipartFormData", sx["Body"])
	assert.Equal(t, "file", sx["FileFormName"])
	assert.Equal(t, "{json:url}", sx["URL"])
	assert.Equal(t, "{json:thumbnail_url}", sx["ThumbnailURL"])
	assert.Equal(t, "{json:deletion_url}", sx["DeletionURL"])
	assert.Equal(t, "{json:error}", sx["ErrorMessage"])
	headers, _ := sx["Headers"].(map[string]any)
	assert.Equal(t, "Bearer "+made.Key, headers["Authorization"])
	assert.Equal(t, "Screenshots.sxcu", made.Setup.ShareXFile)
	assert.Contains(t, made.Setup.Curl, "Bearer "+made.Key)
	assert.Contains(t, made.Setup.Curl, "http://test.local/api/uploads")

	code, ans, raw := sendUpload(t, f, made.Key, "shot.png", pngBytes, nil)
	require.Equal(t, http.StatusOK, code, raw)
	require.True(t, strings.HasPrefix(ans.URL, "http://test.local/s/"), ans.URL)
	require.True(t, strings.HasSuffix(ans.URL, "?inline=1"), "the link opens in the browser: %s", ans.URL)
	assert.Equal(t, ans.URL, ans.ThumbnailURL, "an image is its own thumbnail")
	require.True(t, strings.HasPrefix(ans.DeletionURL, "http://test.local/x/"), ans.DeletionURL)

	stored := filesIn(t, filepath.Join(f.rootDir, "shots"))
	require.Len(t, stored, 1)
	assert.True(t, strings.HasSuffix(stored[0], "-shot.png"), "random-prefixed: %s", stored[0])

	code, body, hdr := plainDo(t, http.MethodGet, f.local(ans.URL), nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, hdr.Get("Content-Disposition"), "inline")
	assert.Equal(t, string(pngBytes), body)

	// A text capture is a file like any other, and gets no thumbnail.
	code, ans, raw = sendUpload(t, f, made.Key, "note.txt", []byte("a captured note"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.Empty(t, ans.ThumbnailURL)

	// The same name again is a new file with a link of its own: an upload
	// never replaces another, nor repoints the link an earlier one answered.
	code, again, raw := sendUpload(t, f, made.Key, "shot.png", []byte("\x89PNG\r\n\x1a\nsecond"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotEqual(t, ans.URL, again.URL)
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "shots")), 3, "an upload replaced another")
	code, body, _ = plainDo(t, http.MethodGet, f.local(ans.URL), nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "a captured note", body, "the first link still opens the first file")
}

// ⚠⚠ The point of the key: it opens the upload door and nothing else. A
// leaked tool config must not reach a listing, a download, the agent API,
// MCP, WebDAV or the old ShareX endpoint - and the door itself takes no
// folder from the request.
func TestUploaders_TheKeyOpensNoOtherDoor(t *testing.T) {
	f := newUploadersFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "secret.txt"), []byte("not for the key"), 0o600))
	code, made, raw := makeUploader(t, f, f.client, map[string]any{"name": "Tool", "storage": "main", "folder": "drop"})
	require.Equal(t, http.StatusCreated, code, raw)

	doors := []struct{ method, path string }{
		{http.MethodGet, "/api/ai/root"},
		{http.MethodGet, "/api/ai/files?path=main://"},
		{http.MethodGet, "/api/ai/download?path=main://secret.txt"},
		{http.MethodPost, "/api/ai/upload"},
		{http.MethodPost, "/api/ai/mcp"},
		{http.MethodGet, "/api/files/manager?action=index&path=main://"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/auth/uploaders"},
		{http.MethodPost, "/api/sharex/upload"},
		{http.MethodGet, "/api/tokens"},
	}
	for _, d := range doors {
		for _, header := range []string{"Authorization", "X-Filex-Token"} {
			req, err := http.NewRequest(d.method, f.srv.URL+d.path, strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			if header == "Authorization" {
				req.Header.Set(header, "Bearer "+made.Key)
			} else {
				req.Header.Set(header, made.Key)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			assert.Equalf(t, http.StatusUnauthorized, resp.StatusCode, "%s %s with the key as %s must be refused as no credential; body: %.200s", d.method, d.path, header, b)
		}
	}

	// The request names no folder: a client's `folder` / `path` fields are
	// not read, the uploader's folder is where the file goes.
	code, _, raw = sendUpload(t, f, made.Key, "x.txt", []byte("x"), map[string]string{"folder": "elsewhere", "path": "main://secret.txt"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.NoDirExists(t, filepath.Join(f.rootDir, "elsewhere"))
	got, _ := os.ReadFile(filepath.Join(f.rootDir, "secret.txt"))
	assert.Equal(t, "not for the key", string(got), "an upload never overwrites")
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "drop")), 1)

	// No key, a wrong key: refused, nothing written.
	code, ans, _ := sendUpload(t, f, "", "y.txt", []byte("y"), nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "uploader_key_missing", ans.Code)
	assert.NotEmpty(t, ans.Error, "error is a sentence a tool shows")
	code, ans, _ = sendUpload(t, f, "fxu_not-a-real-key", "y.txt", []byte("y"), nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "uploader_key_unknown", ans.Code)
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "drop")), 1)
}

// ⚠⚠ A deletion link is opened by link previews (Slack, Discord) and
// prefetchers: GET answers the confirmation page and NEVER deletes. Only the
// page's POST moves the file to the trash and ends its public link; the
// link is spent then.
func TestUploaders_TheDeletionLinksGETNeverDeletes(t *testing.T) {
	f := newUploadersFixture(t)
	_, made, _ := makeUploader(t, f, f.client, map[string]any{"name": "Shots", "storage": "main", "folder": "shots"})
	code, ans, raw := sendUpload(t, f, made.Key, "keep-me.png", pngBytes, nil)
	require.Equal(t, http.StatusOK, code, raw)
	stored := filesIn(t, filepath.Join(f.rootDir, "shots"))
	require.Len(t, stored, 1)
	del := f.local(ans.DeletionURL)

	for i := 0; i < 3; i++ {
		code, body, hdr := plainDo(t, http.MethodGet, del, nil)
		require.Equal(t, http.StatusOK, code, body)
		assert.Contains(t, hdr.Get("Content-Type"), "text/html")
		assert.Contains(t, body, stored[0], "the page names the file")
		assert.Contains(t, body, `method="post"`)
		assert.Contains(t, body, `name="confirm" value="1"`)
	}
	code, _, _ = plainDo(t, http.MethodHead, del, nil)
	assert.NotEqual(t, http.StatusInternalServerError, code)
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "shots")), 1, "GET deleted the file")
	code, _, _ = plainDo(t, http.MethodGet, f.local(ans.URL), nil)
	assert.Equal(t, http.StatusOK, code, "GET ended the public link")

	// A POST without the page's field is shown the page, nothing more.
	code, body, _ := plainDo(t, http.MethodPost, del, url.Values{})
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `name="confirm"`)
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "shots")), 1)

	// The button.
	code, body, _ = plainDo(t, http.MethodPost, del, url.Values{"confirm": {"1"}})
	require.Equal(t, http.StatusOK, code, body)
	assert.Empty(t, filesIn(t, filepath.Join(f.rootDir, "shots")), "the file left its folder")
	trash := filepath.Join(f.rootDir, ".filex-trash")
	found := false
	_ = filepath.Walk(trash, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, "keep-me.png") {
			found = true
		}
		return nil
	})
	assert.True(t, found, "the file is in the trash, not gone")
	code, _, _ = plainDo(t, http.MethodGet, f.local(ans.URL), nil)
	assert.NotEqual(t, http.StatusOK, code, "the public link still opened after the deletion")

	// Spent: the link now says so, and does nothing.
	code, body, _ = plainDo(t, http.MethodPost, del, url.Values{"confirm": {"1"}})
	assert.Equal(t, http.StatusNotFound, code, body)
	code, _, _ = plainDo(t, http.MethodGet, del, nil)
	assert.Equal(t, http.StatusNotFound, code)
	code, _, _ = plainDo(t, http.MethodGet, f.srv.URL+"/x/not-a-token", nil)
	assert.Equal(t, http.StatusNotFound, code)
}

// A file that is gone already (deleted in filex) gets a page that says so,
// and a deletion link keeps working after its uploader is deleted.
func TestUploaders_DeletionLinkOfAGoneFileAndOfADeletedUploader(t *testing.T) {
	f := newUploadersFixture(t)
	_, made, _ := makeUploader(t, f, f.client, map[string]any{"name": "Shots", "storage": "main", "folder": "shots"})
	_, first, _ := sendUpload(t, f, made.Key, "a.txt", []byte("a"), nil)
	_, second, _ := sendUpload(t, f, made.Key, "b.txt", []byte("b"), nil)

	// The first file is deleted in filex, through the agent API's own delete.
	name := ""
	for _, n := range filesIn(t, filepath.Join(f.rootDir, "shots")) {
		if strings.HasSuffix(n, "-a.txt") {
			name = n
		}
	}
	require.NotEmpty(t, name)
	tok := testutil.NewAPIToken(t, f.store, f.userID, "read,write,delete")
	code, out, raw := ownerTokenCall(t, f.srv.URL, tok, http.MethodPost, "/api/ai/delete", "application/json",
		jsonOf(t, map[string]any{"path": "main://shots/" + name}))
	require.Equal(t, http.StatusOK, code, "%v %s", out, raw)
	code, body, _ := plainDo(t, http.MethodGet, f.local(first.DeletionURL), nil)
	assert.Equal(t, http.StatusGone, code, body)

	// The uploader goes; its key with it; the deletion link it handed out stays.
	req, _ := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/auth/uploaders/"+itoa(made.Uploader.ID), nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	code, ans, _ := sendUpload(t, f, made.Key, "c.txt", []byte("c"), nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "uploader_key_unknown", ans.Code)
	code, body, _ = plainDo(t, http.MethodPost, f.local(second.DeletionURL), url.Values{"confirm": {"1"}})
	assert.Equal(t, http.StatusOK, code, body)
}

// A new key replaces the old one at once.
func TestUploaders_ANewKeyEndsTheOldOne(t *testing.T) {
	f := newUploadersFixture(t)
	_, made, _ := makeUploader(t, f, f.client, map[string]any{"name": "Shots", "storage": "main"})
	assert.Equal(t, "uploads", made.Uploader.Folder, "the default folder is the product's own word")
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/auth/uploaders/"+itoa(made.Uploader.ID)+"/key", nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	var rotated madeUploader
	require.NoError(t, json.Unmarshal(b, &rotated))
	require.NotEqual(t, made.Key, rotated.Key)
	assert.Contains(t, rotated.Setup.Curl, rotated.Key, "the new settings carry the new key")

	code, _, _ := sendUpload(t, f, made.Key, "old.txt", []byte("o"), nil)
	assert.Equal(t, http.StatusUnauthorized, code, "the old key still worked")
	code, _, raw := sendUpload(t, f, rotated.Key, "new.txt", []byte("n"), nil)
	assert.Equal(t, http.StatusOK, code, raw)
}

// A private uploader answers with the file's address in filex (sign-in and
// the reader's permission decide) and makes no public link at all.
func TestUploaders_APrivateUploaderMakesNoPublicLink(t *testing.T) {
	f := newUploadersFixture(t)
	code, made, raw := makeUploader(t, f, f.client, map[string]any{"name": "Private", "storage": "main", "folder": "Team notes", "visibility": "private"})
	require.Equal(t, http.StatusCreated, code, raw)
	before := sharesBy(t, f, f.userID)
	code, ans, raw := sendUpload(t, f, made.Key, "pic.png", pngBytes, nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.Equal(t, before, sharesBy(t, f, f.userID), "a private upload minted a public link")
	assert.Empty(t, ans.ThumbnailURL, "no anonymous thumbnail for a private file")
	u, err := url.Parse(ans.URL)
	require.NoError(t, err)
	assert.Equal(t, "/drive/explore", u.Path, "the end-user front door")
	sel := u.Query().Get("select")
	assert.True(t, strings.HasPrefix(sel, "main://Team notes/") && strings.HasSuffix(sel, "-pic.png"), sel)
	assert.Equal(t, "main/Team%20notes", u.EscapedFragment(), "the explorer's own hash form, escaped per segment")
	assert.NotEmpty(t, ans.DeletionURL, "a private upload can be deleted with its link too")
}

// The link's life is cut to the server's ceiling (share.max_ttl_days) when
// the uploader is saved, and the form is told the ceiling.
func TestUploaders_LinkLifeFollowsTheCeiling(t *testing.T) {
	f := newUploadersFixture(t)
	ctx := context.Background()
	require.NoError(t, f.store.UpsertSetting(ctx, share.SettingKeyMaxTTLDays, "7"))
	code, made, raw := makeUploader(t, f, f.client, map[string]any{"name": "Week", "storage": "main", "link_days": 30})
	require.Equal(t, http.StatusCreated, code, raw)
	assert.Equal(t, 7, made.Uploader.LinkDays)
	assert.True(t, made.LinkDaysClamped)
	listCode, list := doJSON(t, f.client, http.MethodGet, f.srv.URL+"/api/auth/uploaders", nil)
	require.Equal(t, http.StatusOK, listCode)
	assert.EqualValues(t, 7, list["link_max_days"])
	assert.Equal(t, "http://test.local/api/uploads", list["endpoint"])

	_, ans, _ := sendUpload(t, f, made.Key, "a.png", pngBytes, nil)
	tok := strings.TrimSuffix(strings.TrimPrefix(ans.URL, "http://test.local/s/"), "?inline=1")
	sh, err := f.store.GetShareByToken(ctx, tok)
	require.NoError(t, err)
	require.NotNil(t, sh.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(7*24*time.Hour), *sh.ExpiresAt, time.Hour)

	// No ceiling: 0 asks for no expiry at all.
	require.NoError(t, f.store.UpsertSetting(ctx, share.SettingKeyMaxTTLDays, "0"))
	_, forever, _ := makeUploader(t, f, f.client, map[string]any{"name": "Forever", "storage": "main", "link_days": 0})
	_, ans, _ = sendUpload(t, f, forever.Key, "b.png", pngBytes, nil)
	tok = strings.TrimSuffix(strings.TrimPrefix(ans.URL, "http://test.local/s/"), "?inline=1")
	sh, err = f.store.GetShareByToken(ctx, tok)
	require.NoError(t, err)
	assert.Nil(t, sh.ExpiresAt, "no ceiling and 0 days: the link does not expire")
}

// An account without share.links makes private uploaders only; and an
// uploader made while it could, refuses the upload (writing nothing) once it
// cannot. Without access.api there is no uploader at all.
func TestUploaders_PublicNeedsShareLinksAndKeysNeedAccessAPI(t *testing.T) {
	f := newUploadersFixture(t)
	u := seedSharedUser(t, f.store, "shot@test.local", "ShotPass!1")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, client, "shot@test.local", "ShotPass!1")

	code, pub, raw := makeUploader(t, f, client, map[string]any{"name": "Public", "storage": "main", "folder": "u1"})
	require.Equal(t, http.StatusCreated, code, raw)

	deny := func(effects map[string]string) {
		require.NoError(t, f.store.SetUserPermissionOverrides(context.Background(), u.ID, effects, nil))
		perm.Invalidate()
	}
	deny(map[string]string{string(perm.ShareLinks): model.PermDeny})
	code, _, raw = makeUploader(t, f, client, map[string]any{"name": "Public2", "storage": "main", "folder": "u2"})
	assert.Equal(t, http.StatusForbidden, code, raw)
	assert.Contains(t, raw, "uploader_public_denied")
	code, _, raw = makeUploader(t, f, client, map[string]any{"name": "Private", "storage": "main", "folder": "u2", "visibility": "private"})
	assert.Equal(t, http.StatusCreated, code, raw)

	code, ans, raw := sendUpload(t, f, pub.Key, "a.png", pngBytes, nil)
	assert.Equal(t, http.StatusForbidden, code, raw)
	assert.Equal(t, "uploader_public_denied", ans.Code)
	assert.Empty(t, filesIn(t, filepath.Join(f.rootDir, "u1")), "a refused upload left a file behind")

	deny(map[string]string{string(perm.AccessAPI): model.PermDeny})
	code, _, raw = makeUploader(t, f, client, map[string]any{"name": "Nope", "storage": "main", "visibility": "private"})
	assert.Equal(t, http.StatusForbidden, code, raw)
	code, ans, raw = sendUpload(t, f, pub.Key, "b.txt", []byte("b"), nil)
	assert.Equal(t, http.StatusForbidden, code, raw)
	assert.Equal(t, "uploader_owner_no_keys", ans.Code)
}

// A switched-off account's uploader is refused as its tokens are.
func TestUploaders_ADisabledOwnersKeyIsRefused(t *testing.T) {
	f := newUploadersFixture(t)
	u := seedSharedUser(t, f.store, "gone@test.local", "GonePass!1")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, client, "gone@test.local", "GonePass!1")
	_, made, _ := makeUploader(t, f, client, map[string]any{"name": "Shots", "storage": "main", "folder": "g"})
	setEnabled(t, f, u.ID, false)
	code, out, raw := sendUploadRaw(t, f, made.Key)
	assertRefusedAsDisabled(t, "upload door", code, out, raw)
	assert.Empty(t, filesIn(t, filepath.Join(f.rootDir, "g")))
}

func sendUploadRaw(t *testing.T, f *stagedFixture, key string) (int, map[string]any, string) {
	t.Helper()
	body, ct := multipartBody(t, "file", "x.txt", []byte("x"), nil)
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/uploads", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Filex-Uploader-Key", key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

// Somebody else's uploader is answered as one that does not exist.
func TestUploaders_AnotherPersonsUploaderIsNotThere(t *testing.T) {
	f := newUploadersFixture(t)
	_, made, _ := makeUploader(t, f, f.client, map[string]any{"name": "Mine", "storage": "main"})
	seedSharedUser(t, f.store, "other@test.local", "OtherPass!1")
	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, other, "other@test.local", "OtherPass!1")
	for _, m := range []string{http.MethodPatch, http.MethodDelete} {
		code, out := doJSON(t, other, m, f.srv.URL+"/api/auth/uploaders/"+itoa(made.Uploader.ID), map[string]any{"name": "taken"})
		assert.Equal(t, http.StatusNotFound, code, "%s: %v", m, out)
	}
	code, out := doJSON(t, other, http.MethodPost, f.srv.URL+"/api/auth/uploaders/"+itoa(made.Uploader.ID)+"/key", nil)
	assert.Equal(t, http.StatusNotFound, code, "%v", out)
	code, list := doJSON(t, other, http.MethodGet, f.srv.URL+"/api/auth/uploaders", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, list["uploaders"])
}

// The deprecated ShareX endpoint still answers as it did, and says it is
// going.
func TestUploaders_TheOldShareXEndpointSaysItIsDeprecated(t *testing.T) {
	f := newUploadersFixture(t)
	tok := testutil.NewAPIToken(t, f.store, f.userID, "write")
	body, ct := multipartBody(t, "file", "old.png", pngBytes, nil)
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/sharex/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	assert.Equal(t, "true", resp.Header.Get("Deprecation"))
	assert.Contains(t, resp.Header.Get("Link"), "UPLOADERS")
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Contains(t, out["url"], "?inline=1")
	assert.Len(t, out, 1, "the old answer, unchanged")
	assert.Len(t, filesIn(t, filepath.Join(f.rootDir, "sharex")), 1)
}

// A big upload (a screen recording) goes through staging: the answer comes
// as soon as filex holds the bytes, with its link AND its deletion link - both
// name the row the staged commit published - and the link serves the bytes
// once the transfer lands.
func TestUploaders_ALargeUploadGoesThroughStagingWithBothLinks(t *testing.T) {
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.ACL = acl.New(d.Store)
		d.Cfg.Upload.ChunkSize = stagedSurfaceThreshold
	})
	_, made, _ := makeUploader(t, f, f.client, map[string]any{"name": "Recorder", "storage": "main", "folder": "rec"})
	payload := randomBytes(stagedSurfaceThreshold*3 + 7)
	code, ans, raw := sendUpload(t, f, made.Key, "screen.webm", payload, nil)
	require.Equal(t, http.StatusOK, code, raw)
	require.Contains(t, ans.URL, "/s/")
	require.Contains(t, ans.DeletionURL, "/x/", "a staged upload has its deletion link too")

	var name string
	require.Eventually(t, func() bool {
		for _, n := range filesIn(t, filepath.Join(f.rootDir, "rec")) {
			if b, err := os.ReadFile(filepath.Join(f.rootDir, "rec", n)); err == nil && len(b) == len(payload) {
				name = n
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond)
	code, body, _ := plainDo(t, http.MethodGet, f.local(ans.URL), nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, sha256Hex(payload), sha256Hex([]byte(body)))

	code, _, _ = plainDo(t, http.MethodPost, f.local(ans.DeletionURL), url.Values{"confirm": {"1"}})
	require.Equal(t, http.StatusOK, code)
	assert.NoFileExists(t, filepath.Join(f.rootDir, "rec", name))
}

// A personal API key (the desktop app's) may make an uploader, no wider than
// itself: it needs `write`, and the folder must be inside its `root:`.
func TestUploaders_AKeyMakesNoUploaderWiderThanItself(t *testing.T) {
	f := newUploadersFixture(t)
	personal := func(scopes string) string {
		raw := "filex_" + hashForTest("key for " + scopes)[:48]
		_, err := f.store.CreateAPIToken(context.Background(), &model.APIToken{
			UserID: f.userID, Label: "desktop", TokenHash: hashForTest(raw), Scopes: scopes, Kind: model.TokenKindUser,
		})
		require.NoError(t, err)
		return raw
	}
	call := func(tok string, body map[string]any) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/auth/uploaders", bytes.NewReader(jsonOf(t, body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Filex-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	readOnly := personal("read")
	code, raw := call(readOnly, map[string]any{"name": "R", "storage": "main", "folder": "x"})
	assert.Equal(t, http.StatusForbidden, code, "a read-only key made a write credential: %s", raw)

	confined := personal("read,write,root:main://allowed")
	code, raw = call(confined, map[string]any{"name": "Out", "storage": "main", "folder": "elsewhere"})
	assert.Equal(t, http.StatusForbidden, code, "a confined key pointed an uploader outside its root: %s", raw)
	assert.NoDirExists(t, filepath.Join(f.rootDir, "elsewhere"))
	code, raw = call(confined, map[string]any{"name": "In", "storage": "main", "folder": "allowed/shots"})
	assert.Equal(t, http.StatusCreated, code, raw)
	assert.DirExists(t, filepath.Join(f.rootDir, "allowed", "shots"))
}

func hashForTest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
