package handlers_test

// Every public door a link token is opened through answers only links of its
// own kind (GHSA-rcrp-r83c-g8hv). A file request (`/d/<token>`) is a blind
// drop: whoever holds it may put files into its folder and must never see
// what is already there. The download page took the same token and listed
// the folder, zipped it and served it, because it asked "is this an app's
// page?" and never "is this a file request?" - three sibling doors asked
// both. In the other direction a download link must not open a file
// request's upload doors.
//
// The matrix runs through the real router (api.BuildRouter), so a door is
// tested as it is mounted, with every link of the wrong kind and the PIN the
// holder of a protected file request already knows; each door is also opened
// once with a link of its own kind, so a 404 below means the door refused,
// not that the request never reached it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	doorsSecretName = "owner-secret.txt"
	doorsSecretBody = "TOPSECRET-OWNER-DATA"
	doorsPlanted    = "planted-by-the-wrong-link.txt"
	doorsPIN        = "4242"
)

// doorsFixture is the router, one folder holding a file its owner never
// meant anybody to see, and one link of every kind on it.
type doorsFixture struct {
	srv   *httptest.Server
	store db.Store
	root  string
	links map[string]*model.Share
}

func newDoorsFixture(t *testing.T) *doorsFixture {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})
	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	svc := share.NewService(store)
	svc.AttachSecret("0123456789abcdef0123456789abcdef")
	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           svc,
		StorageResolver: resolver,
		LocalAuth:       localDrv,
		ZipCache:        sharezip.New(t.TempDir()),
	}))
	t.Cleanup(srv.Close)
	uid, _ := testutil.SeedAdminUser(t, store)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inbox", doorsSecretName), []byte(doorsSecretBody), 0o644))
	folder, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "inbox", Path: "/inbox",
		PathHash: pathkey.Hash(st.ID, "/inbox"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	file, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: doorsSecretName, Path: "/inbox/" + doorsSecretName,
		PathHash: pathkey.Hash(st.ID, "/inbox/"+doorsSecretName), Type: model.NodeTypeFile,
		Size: int64(len(doorsSecretBody)), Mime: "text/plain",
	})
	require.NoError(t, err)

	mk := func(node int64, kind, pin string) *model.Share {
		sh, err := svc.Create(ctx, share.CreateOpts{NodeID: node, Kind: kind, PIN: pin, CreatedBy: &uid})
		require.NoError(t, err)
		return sh
	}
	return &doorsFixture{srv: srv, store: store, root: root, links: map[string]*model.Share{
		"file request":                mk(folder.ID, model.ShareKindDrop, ""),
		"file request with a PIN":     mk(folder.ID, model.ShareKindDrop, doorsPIN),
		"folder download link":        mk(folder.ID, model.ShareKindDownload, ""),
		"folder download link, PIN":   mk(folder.ID, model.ShareKindDownload, doorsPIN),
		"file download link":          mk(file.ID, model.ShareKindDownload, ""),
		"file download link with PIN": mk(file.ID, model.ShareKindDownload, doorsPIN),
	}}
}

// linkDoor is one public route a token is opened through, as a visitor
// sends it.
type linkDoor struct {
	name   string
	method string
	// path with {t} for the token.
	path string
	// body: nil, a JSON object, url.Values (a form) or []doorsFile (multipart).
	body any
	// pin: the door carries the PIN, so its own-kind control uses the
	// PIN-protected link.
	pin bool
	// own is the kind of link the door is for; "" for a door that answers
	// only an app's page (no own-kind control here).
	own string
}

type doorsFile struct{ field, name, content string }

// readDoors: everything that shows, lists or hands out what a link names.
func readDoors() []linkDoor {
	pinForm := url.Values{"pin": {doorsPIN}}
	return []linkDoor{
		{name: "download page", method: "GET", path: "/s/{t}", own: "download"},
		{name: "download page, whole folder as a ZIP", method: "GET", path: "/s/{t}?zip=wait", own: "download"},
		{name: "download page, ZIP preparing", method: "GET", path: "/s/{t}?zip=1", own: "download"},
		{name: "download page, PIN in the address", method: "GET", path: "/s/{t}?pin=" + doorsPIN, pin: true, own: "download"},
		{name: "download page, PIN in the address, ZIP", method: "GET", path: "/s/{t}?pin=" + doorsPIN + "&zip=wait", pin: true, own: "download"},
		{name: "download page, PIN form", method: "POST", path: "/s/{t}", body: pinForm, pin: true, own: "download"},
		{name: "download page, PIN form confirmed", method: "POST", path: "/s/{t}?confirmed=1", body: pinForm, pin: true, own: "download"},
		{name: "a file under a shared folder", method: "GET", path: "/s/{t}/f/" + doorsSecretName, own: "download"},
		{name: "a file under a shared folder, PIN", method: "GET", path: "/s/{t}/f/" + doorsSecretName + "?pin=" + doorsPIN, pin: true, own: "download"},
		{name: "embed metadata", method: "GET", path: "/api/files/share/{t}", own: "download"},
		{name: "public JSON state", method: "GET", path: "/api/public/s/{t}", own: "download"},
		{name: "public JSON folder entries", method: "GET", path: "/api/public/s/{t}/entries", own: "download"},
		{name: "public JSON slots", method: "GET", path: "/api/public/s/{t}/slots", own: "download"},
		{name: "public JSON PIN", method: "POST", path: "/api/public/s/{t}/pin", body: map[string]any{"pin": doorsPIN}, pin: true, own: "download"},
		{name: "public JSON app copy", method: "GET", path: "/api/public/s/{t}/file/" + doorsSecretName},
		{name: "public JSON app event", method: "POST", path: "/api/public/s/{t}/event", body: map[string]any{"event": "open"}},
	}
}

// uploadDoors: everything a file request writes through, or that answers
// for its upload box.
func uploadDoors() []linkDoor {
	files := []doorsFile{{field: "file[]", name: doorsPlanted, content: "x"}}
	withPIN := append([]doorsFile{{field: "pin", content: doorsPIN}}, files...)
	return []linkDoor{
		{name: "upload page", method: "GET", path: "/d/{t}", own: "drop"},
		{name: "upload page, the upload", method: "POST", path: "/d/{t}", body: files, own: "drop"},
		{name: "upload page, the upload with the PIN", method: "POST", path: "/d/{t}", body: withPIN, pin: true, own: "drop"},
		{name: "public JSON state", method: "GET", path: "/api/public/d/{t}", own: "drop"},
		{name: "public JSON upload", method: "POST", path: "/api/public/d/{t}/upload", body: files, own: "drop"},
		{name: "public JSON slots", method: "GET", path: "/api/public/d/{t}/slots", own: "drop"},
		{name: "public JSON PIN", method: "POST", path: "/api/public/d/{t}/pin", body: map[string]any{"pin": doorsPIN}, pin: true, own: "drop"},
	}
}

// knock sends one door's request with token, as a fresh visitor (no cookie
// carried over from another door).
func (f *doorsFixture) knock(t *testing.T, d linkDoor, token string) (*http.Response, string) {
	t.Helper()
	var (
		rdr   io.Reader
		ctype string
	)
	switch b := d.body.(type) {
	case nil:
	case url.Values:
		rdr, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	case []doorsFile:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, p := range b {
			if p.name == "" {
				require.NoError(t, mw.WriteField(p.field, p.content))
				continue
			}
			part, err := mw.CreateFormFile(p.field, p.name)
			require.NoError(t, err)
			_, _ = io.WriteString(part, p.content)
		}
		require.NoError(t, mw.Close())
		rdr, ctype = &buf, mw.FormDataContentType()
	default:
		raw, err := json.Marshal(b)
		require.NoError(t, err)
		rdr, ctype = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequest(d.method, f.srv.URL+strings.ReplaceAll(d.path, "{t}", token), rdr)
	require.NoError(t, err)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func (f *doorsFixture) planted(t *testing.T) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(f.root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == doorsPlanted {
			found = append(found, p)
		}
		return nil
	})
	return found
}

// assertRefused: the door answers like a token that does not exist, and
// leaves nothing behind - no byte, name or listing of the folder, no unlock
// cookie, no file written, no PIN attempt and no download counted.
func (f *doorsFixture) assertRefused(t *testing.T, d linkDoor, linkName string) {
	t.Helper()
	token := f.links[linkName].Token
	before, err := f.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	resp, body := f.knock(t, d, token)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "%s opened %s %s (%s): %.300s", linkName, d.method, d.path, d.name, body)
	assert.NotContains(t, body, doorsSecretBody, "%s handed the owner's file over at %s", linkName, d.name)
	assert.NotContains(t, body, doorsSecretName, "%s named the owner's file at %s", linkName, d.name)
	assert.Empty(t, resp.Cookies(), "%s was given a cookie at %s", linkName, d.name)
	assert.Empty(t, f.planted(t), "%s wrote a file at %s", linkName, d.name)
	after, err := f.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, before.PinFails, after.PinFails, "%s spent a PIN attempt at %s", linkName, d.name)
	assert.Equal(t, before.DownloadCount, after.DownloadCount, "%s counted a download at %s", linkName, d.name)
	assert.Equal(t, before.UploadCount, after.UploadCount, "%s counted an upload at %s", linkName, d.name)
}

// TestLinkDoors_AFileRequestOpensNoReadDoor is the advisory's finding and
// every sibling of it: a file request, with or without its PIN, lists,
// zips, streams and describes nothing.
func TestLinkDoors_AFileRequestOpensNoReadDoor(t *testing.T) {
	f := newDoorsFixture(t)
	for _, d := range readDoors() {
		for _, link := range []string{"file request", "file request with a PIN"} {
			t.Run(d.name+"/"+link, func(t *testing.T) { f.assertRefused(t, d, link) })
		}
	}
}

// TestLinkDoors_ADownloadLinkOpensNoUploadDoor: the other direction - a
// download link (of a folder or of a file, with or without a PIN) writes
// nothing and answers nothing at a file request's doors.
func TestLinkDoors_ADownloadLinkOpensNoUploadDoor(t *testing.T) {
	f := newDoorsFixture(t)
	for _, d := range uploadDoors() {
		for _, link := range []string{"folder download link", "folder download link, PIN", "file download link", "file download link with PIN"} {
			t.Run(d.name+"/"+link, func(t *testing.T) { f.assertRefused(t, d, link) })
		}
	}
}

// TestLinkDoors_EachDoorStillOpensForItsOwnKind is the control that gives
// the two tests above their meaning: the same requests with a link of the
// door's own kind are answered (a door that 404s for everybody would pass
// them).
func TestLinkDoors_EachDoorStillOpensForItsOwnKind(t *testing.T) {
	f := newDoorsFixture(t)
	for _, d := range append(readDoors(), uploadDoors()...) {
		if d.own == "" {
			continue
		}
		link := map[[2]any]string{
			{"download", false}: "folder download link",
			{"download", true}:  "folder download link, PIN",
			{"drop", false}:     "file request",
			{"drop", true}:      "file request with a PIN",
		}[[2]any{d.own, d.pin}]
		t.Run(d.name+"/"+link, func(t *testing.T) {
			resp, body := f.knock(t, d, f.links[link].Token)
			assert.NotEqual(t, http.StatusNotFound, resp.StatusCode, "%s refused its own kind of link: %.300s", d.name, body)
			assert.Less(t, resp.StatusCode, 500, "%s: %.300s", d.name, body)
		})
	}
	// And the bytes really are there for the right link: the advisory's
	// two requests, made with a download link of the folder.
	resp, body := f.knock(t, linkDoor{method: "GET", path: "/s/{t}"}, f.links["folder download link"].Token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, doorsSecretName)
	resp, body = f.knock(t, linkDoor{method: "GET", path: "/s/{t}?zip=wait"}, f.links["folder download link"].Token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "zip")
	assert.Contains(t, body, doorsSecretName)
}

// TestLinkDoors_AFileRequestsPINAnswersItsOwnState: a PIN that opens a file
// request answers the file request's state - the shape its screen's first
// call reads - and not the download page's description of its folder. The
// shared PIN route used to answer every link with the share's shape, so the
// upload box lost its limits and its folder's name after the PIN.
func TestLinkDoors_AFileRequestsPINAnswersItsOwnState(t *testing.T) {
	f := newDoorsFixture(t)
	token := f.links["file request with a PIN"].Token
	first, firstBody := f.knock(t, linkDoor{method: "GET", path: "/api/public/d/{t}"}, token)
	require.Equal(t, http.StatusOK, first.StatusCode, firstBody)
	resp, body := f.knock(t, linkDoor{method: "POST", path: "/api/public/d/{t}/pin", body: map[string]any{"pin": doorsPIN}}, token)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, resp.Cookies(), 1, "the PIN still unlocks")

	var before, after map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstBody), &before))
	require.NoError(t, json.Unmarshal([]byte(body), &after))
	assert.Equal(t, "drop", after["kind"])
	assert.Equal(t, "inbox", after["folder"])
	assert.Equal(t, true, after["unlocked"])
	assert.Contains(t, after, "limits")
	assert.NotContains(t, after, "node", "the download page's description of the folder")
	for k := range before {
		assert.Contains(t, after, k, "the first call's %q is missing after the PIN", k)
	}
	assert.NotContains(t, body, doorsSecretName)
}
