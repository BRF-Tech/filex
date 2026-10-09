package handlers_test

// Installing a STORAGE plugin from a store's link (#215,
// handlers/app_store_storage.go), through the production router: the same
// trust, the same routes and the same session gate as an app's link; the
// review reads the release's own feed - the bytes the store pinned - and holds
// this platform's build in it to the link; every sentence of the review is
// the server's, in the reader's language; the install downloads that build,
// holds it to its pin and installs it as an ordinary binary plugin, which
// then starts and is probed like any other.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// storageRelease is a plugin's GitHub release over TLS: its feed and its
// builds, changeable mid-test.
type storageRelease struct {
	mu    sync.Mutex
	files map[string][]byte
	srv   *httptest.Server
}

const relBase = "/acme/filex-myfs/releases/download/v1.0.0/"

func newStorageRelease(t *testing.T) *storageRelease {
	r := &storageRelease{files: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *storageRelease) put(name string, b []byte) string {
	r.mu.Lock()
	r.files[relBase+name] = b
	r.mu.Unlock()
	return r.srv.URL + relBase + name
}

// buildMemfs builds the SDK's example plugin (backend/examples/plugin-memfs).
func buildMemfs(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out := filepath.Join(t.TempDir(), "memfs")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "../../../examples/plugin-memfs")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", b)
	bin, err := os.ReadFile(out)
	require.NoError(t, err)
	return bin
}

type storageFix struct {
	*asFix
	mgr *plugin.Manager
	rel *storageRelease
	bin []byte
}

// newStorageFix is the store fixture with a storage plugin manager whose
// downloads trust the release's TLS server (off: storage plugins disabled).
func newStorageFix(t *testing.T, plugins bool) *storageFix {
	t.Helper()
	rel := newStorageRelease(t)
	sf := &storageFix{rel: rel}
	sf.asFix = newAsFixDeps(t, nil, func(d *api.Deps, store db.Store) {
		if !plugins {
			return
		}
		m, err := plugin.New(plugin.Options{Store: store, Dir: filepath.Join(t.TempDir(), "plugins"),
			SecretKey: "0123456789abcdef0123456789abcdef", HTTP: rel.srv.Client()})
		require.NoError(t, err)
		t.Cleanup(m.Shutdown)
		require.NoError(t, m.Load(context.Background()))
		sf.mgr = m
		d.Plugins = m
	})
	return sf
}

// publishStorage lays out the release (the feed naming this platform's
// build) and a signed link for it at the store; mut edits the link.
func (sf *storageFix) publishStorage(t *testing.T, token string, paid bool, mut func(p map[string]any)) []byte {
	t.Helper()
	if sf.bin == nil {
		sf.bin = buildMemfs(t)
	}
	plat := runtime.GOOS + "/" + runtime.GOARCH
	buildURL := sf.rel.put("myfs-"+strings.ReplaceAll(plat, "/", "-"), sf.bin)
	feed, err := json.Marshal(map[string]any{
		"name": "myfs", "version": "1.0.0", "notes": "First release.",
		"binaries": map[string]any{plat: map[string]any{"url": buildURL, "sha256": shaHex(sf.bin)}},
	})
	require.NoError(t, err)
	feedURL := sf.rel.put(plugin.FeedFileName, feed)
	p := sf.st.IntentPayload("tid-"+token, "myfs", "1.0.0", "acme/filex-myfs", "v1.0.0", time.Now().Add(30*time.Minute))
	p["kind"] = appstore.KindStorage
	p["manifest_sha256"] = shaHex(feed)
	p["feed_url"] = feedURL
	p["paid"] = paid
	p["binaries"] = map[string]any{plat: map[string]any{"url": buildURL, "sha256": shaHex(sf.bin), "size": len(sf.bin), "sig": "00"}}
	p["conformance"] = map[string]any{"platform": "linux/amd64", "filex": "0.55.0", "verified": true, "passed": 9, "failed": 0,
		"skipped": 7, "driver": "memfs", "capabilities": []string{"delete", "set_mtime", "write"}}
	if mut != nil {
		mut(p)
	}
	sf.st.PutIntent(token, &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
	return feed
}

type storageReviewBody struct {
	Handle        string `json:"handle"`
	StorageReview struct {
		Name       string `json:"name"`
		Platform   string `json:"platform"`
		SHA256     string `json:"sha256"`
		CanInstall bool   `json:"can_install"`
		Notices    []struct {
			Level string `json:"level"`
			Text  string `json:"text"`
		} `json:"notices"`
		Capabilities []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"capabilities"`
		Signature struct {
			StoreSigned bool `json:"store_signed"`
			Required    bool `json:"required"`
		} `json:"signature"`
	} `json:"storage_review"`
}

func (sf *storageFix) storageReview(t *testing.T, token string, hdr ...string) (int, storageReviewBody, []byte) {
	t.Helper()
	code, body := sf.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent", map[string]any{"store": sf.st.Origin(), "token": token}, hdr...)
	var rb storageReviewBody
	_ = json.Unmarshal(body, &rb)
	return code, rb, body
}

func noticesText(rb storageReviewBody) string {
	var parts []string
	for _, n := range rb.StorageReview.Notices {
		parts = append(parts, n.Level+": "+n.Text)
	}
	return strings.Join(parts, "\n")
}

func TestStoreInstall_AStoragePluginsReviewIsTheServersWords(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.publishStorage(t, "tokenstor01", false, nil)

	code, rb, body := sf.storageReview(t, "tokenstor01", "Accept-Language", "en")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Equal(t, "myfs", rb.StorageReview.Name)
	assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, rb.StorageReview.Platform)
	assert.Equal(t, shaHex(sf.bin), rb.StorageReview.SHA256)
	assert.True(t, rb.StorageReview.CanInstall)
	assert.True(t, rb.StorageReview.Signature.StoreSigned)
	assert.False(t, rb.StorageReview.Signature.Required)
	text := noticesText(rb)
	assert.Contains(t, text, "warning: myfs is a program that runs on this server with filex's own rights, outside any sandbox")
	assert.Contains(t, text, "9 passed, 7 not declared")
	assert.Contains(t, text, "probes every claim again")
	require.Len(t, rb.StorageReview.Capabilities, 3)
	assert.Equal(t, "writing", rb.StorageReview.Capabilities[0].Label)
	assert.Empty(t, sf.st.Completions(), "a review tells the store nothing")

	// The same link read by an administrator whose account speaks Turkish:
	// the server's sentences, in Turkish. ⚠ The account's language is the
	// reader's (requestLang: account, then Accept-Language), so the header
	// alone cannot turn testutil's "en" administrator Turkish.
	sf.publishStorage(t, "tokenstor02", false, nil)
	require.NoError(t, sf.store.UpdateUserLocale(t.Context(), sf.adminID, "tr", "UTC"))
	code, rb, body = sf.storageReview(t, "tokenstor02")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, noticesText(rb), "herhangi bir yalıtım (sandbox) olmadan")
	assert.Equal(t, "yazma", rb.StorageReview.Capabilities[0].Label)
}

func TestStoreInstall_AStoragePluginInstallsHeldToItsPinAndRuns(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.publishStorage(t, "tokenstor03", false, nil)
	code, rb, body := sf.storageReview(t, "tokenstor03")
	require.Equal(t, http.StatusOK, code, "%s", body)

	code, body = sf.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/install", map[string]any{"handle": rb.Handle})
	require.Equal(t, http.StatusCreated, code, "%s", body)
	var out struct {
		Kind   string `json:"kind"`
		Plugin struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
			Source string `json:"source"`
		} `json:"plugin"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, appstore.KindStorage, out.Kind)
	assert.Equal(t, "myfs", out.Plugin.Name)
	assert.Equal(t, shaHex(sf.bin), out.Plugin.SHA256, "the bytes installed are the pinned build")
	assert.Equal(t, "acme/filex-myfs", out.Plugin.Source, "the daily check follows the repository")

	// It starts and proves its claims like any binary plugin.
	deadline := time.Now().Add(30 * time.Second)
	var st *plugin.Status
	for time.Now().Before(deadline) {
		st, _ = sf.mgr.Get(context.Background(), out.Plugin.ID)
		if st != nil && st.State != plugin.StateStarting {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotNil(t, st)
	assert.Equal(t, plugin.StateRunning, st.State, "%s", st.StateError)

	// The store was told; where it came from is kept; the link is spent.
	comps := sf.st.Completions()
	require.Len(t, comps, 1)
	assert.Equal(t, "installed", comps[0].Result)
	from, _ := sf.svc.StorageInstalledFrom(context.Background(), "myfs")
	assert.Equal(t, sf.st.Origin(), from)
	code, _, _ = sf.storageReview(t, "tokenstor03")
	assert.NotEqual(t, http.StatusOK, code, "a used link opens no second review")

	// The plugin list says where it came from.
	code, body = sf.call(t, "", http.MethodGet, "/api/admin/plugins", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, string(body), `"from_store":"`+sf.st.Origin()+`"`)

	// The same version again is not an upgrade.
	sf.publishStorage(t, "tokenstor04", false, nil)
	code, _, body = sf.storageReview(t, "tokenstor04")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeVersionRollback, errCode(body))
}

func TestStoreInstall_AStorageFeedOtherThanThePinnedOneIsRefused(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.publishStorage(t, "tokenstor05", false, func(p map[string]any) { p["manifest_sha256"] = strings.Repeat("0", 64) })
	code, _, body := sf.storageReview(t, "tokenstor05")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodePinMismatch, errCode(body))

	// A link whose build is not the one the feed names: refused too.
	sf.publishStorage(t, "tokenstor06", false, func(p map[string]any) {
		for _, b := range p["binaries"].(map[string]any) {
			b.(map[string]any)["sha256"] = strings.Repeat("1", 64)
		}
	})
	code, _, body = sf.storageReview(t, "tokenstor06")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodePinMismatch, errCode(body))
}

// A storage link's refusal is the server's sentence, in the reader's
// language, and says it is a plugin's (detail.kind) so no client rewords it
// with an app's words.
func TestStoreInstall_AStorageRefusalIsTheServersSentence(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	mut := func(p map[string]any) {
		for _, b := range p["binaries"].(map[string]any) {
			b.(map[string]any)["sha256"] = strings.Repeat("1", 64)
		}
	}
	var refusal struct {
		Error   string         `json:"error"`
		Message string         `json:"message"`
		Detail  map[string]any `json:"detail"`
	}
	// The reader is the administrator's account (requestLang reads its
	// language before Accept-Language): Turkish first, then English.
	sf.publishStorage(t, "tokenstor21", false, mut)
	require.NoError(t, sf.store.UpdateUserLocale(t.Context(), sf.adminID, "tr", "UTC"))
	code, _, body := sf.storageReview(t, "tokenstor21")
	require.Equal(t, http.StatusConflict, code, "%s", body)
	require.NoError(t, json.Unmarshal(body, &refusal))
	assert.Equal(t, appstore.CodePinMismatch, refusal.Error)
	assert.Equal(t, appstore.KindStorage, refusal.Detail["kind"])
	assert.Contains(t, refusal.Message, "Hiçbir şey kurulmadı")
	assert.Contains(t, refusal.Message, "sha256")

	sf.publishStorage(t, "tokenstor22", false, mut)
	require.NoError(t, sf.store.UpdateUserLocale(t.Context(), sf.adminID, "en", "UTC"))
	code, _, body = sf.storageReview(t, "tokenstor22")
	require.Equal(t, http.StatusConflict, code, "%s", body)
	require.NoError(t, json.Unmarshal(body, &refusal))
	assert.Contains(t, refusal.Message, "is not what "+sf.st.Origin()+" approved")

	// A license asked of a plugin that is not here: a code and a sentence.
	code, body = sf.call(t, "", http.MethodGet, "/api/admin/app-plugins/storage/nothere/license", nil, "Accept-Language", "en")
	require.Equal(t, http.StatusNotFound, code, "%s", body)
	require.NoError(t, json.Unmarshal(body, &refusal))
	assert.Equal(t, "not_found", refusal.Error)
	assert.Contains(t, refusal.Message, "No storage plugin called nothere")
}

func TestStoreInstall_NoBuildForThisServerIsSaid(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.publishStorage(t, "tokenstor07", false, func(p map[string]any) {
		p["binaries"] = map[string]any{"plan9/amd64": map[string]any{"url": "https://example.com/x", "sha256": strings.Repeat("1", 64), "sig": "00"}}
	})
	code, _, body := sf.storageReview(t, "tokenstor07")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeNoBuild, errCode(body))
	assert.Contains(t, string(body), "plan9/amd64")
}

func TestStoreInstall_AStorageLinkWhereStoragePluginsAreOff(t *testing.T) {
	sf := newStorageFix(t, false)
	sf.trust(t)
	sf.publishStorage(t, "tokenstor08", false, nil)
	code, _, body := sf.storageReview(t, "tokenstor08")
	assert.Equal(t, http.StatusServiceUnavailable, code, "%s", body)
	assert.Equal(t, appstore.CodePluginsOff, errCode(body))
	assert.Contains(t, string(body), "FILEX_PLUGINS_DISABLED")
}

func TestStoreInstall_APaidStoragePluginKeepsItsOwnLicense(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		now := time.Now().UTC()
		return map[string]any{"result": appstore.ResultValid, "app": req.App, "instance_id": req.InstanceID,
			"checked_at": now.Format(time.RFC3339), "next_check_by": now.Add(24 * time.Hour).Format(time.RFC3339),
			"grace_until": now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}, ""
	}
	sf.publishStorage(t, "tokenstor09", true, nil)
	code, rb, body := sf.storageReview(t, "tokenstor09")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, noticesText(rb), "held until the store confirms its license")
	code, body = sf.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/install",
		map[string]any{"handle": rb.Handle, "license_key": "FXL-7Q2M-K9P4-ZZ31"})
	require.Equal(t, http.StatusCreated, code, "%s", body)
	assert.NotContains(t, string(body), "FXL-7Q2M-K9P4-ZZ31", "the key is in no answer")

	// Its license is its own row, asked about by the plugin's own name.
	code, body = sf.call(t, "", http.MethodGet, "/api/admin/app-plugins/storage/myfs/license", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	var v appstore.View
	require.NoError(t, json.Unmarshal(body, &v))
	assert.Equal(t, "storage:myfs", v.App)
	assert.Equal(t, "myfs", v.Name)
	assert.Equal(t, appstore.StatusValid, v.Status)
	var asked []string
	for _, r := range sf.st.Verifies() {
		asked = append(asked, r.App)
	}
	assert.Equal(t, []string{"myfs"}, asked)
	// An app of the same name has no license: the rows never meet.
	lic, _ := sf.svc.LicenseOf(context.Background(), "myfs")
	assert.Nil(t, lic)
}

func TestStoreInstall_NoKeyReachesAStoragePluginsLicense(t *testing.T) {
	sf := newStorageFix(t, true)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/app-plugins/storage/myfs/license"},
		{http.MethodPut, "/api/admin/app-plugins/storage/myfs/license"},
		{http.MethodPost, "/api/admin/app-plugins/storage/myfs/license/verify"},
	} {
		for name, tok := range map[string]string{"admin-scoped key": sf.key, "app token": sf.appKey} {
			code, body := sf.call(t, tok, r.method, r.path, map[string]any{"key": "FXL-7Q2M-K9P4-ZZ31"})
			assert.Equal(t, http.StatusForbidden, code, "%s %s %s: %s", name, r.method, r.path, body)
		}
	}
}

// storageIndex is the store's signed catalog with a storage plugin that has
// a build for this server (myfs), one without (farfs) and an app (lang-eo).
func (sf *storageFix) storageIndex(t *testing.T) {
	t.Helper()
	doc := sf.st.IndexDoc(
		map[string]any{"name": "myfs", "kind": "storage", "version": "1.0.0"},
		map[string]any{"name": "farfs", "kind": "storage", "version": "2.0.0"},
		map[string]any{"name": "lang-eo", "kind": "language_pack", "version": "1.0.0"},
	)
	here := runtime.GOOS + "/" + runtime.GOARCH
	for _, a := range doc["apps"].([]any) {
		app := a.(map[string]any)
		v := app["versions"].([]any)[0].(map[string]any)
		switch app["name"] {
		case "myfs":
			v["binaries"] = map[string]any{here: map[string]any{"url": "https://example.com/myfs", "sha256": strings.Repeat("1", 64), "sig": "00"}}
			v["conformance"] = map[string]any{"platform": here, "filex": "0.55.0", "verified": true, "passed": 9, "skipped": 7, "capabilities": []string{"write"}}
		case "farfs":
			v["binaries"] = map[string]any{"plan9/amd64": map[string]any{"url": "https://example.com/farfs", "sha256": strings.Repeat("2", 64), "sig": "00"}}
		}
	}
	sf.st.SetIndex("idx-1", doc, false)
}

// person signs a regular user in and shows them the store screen.
func (sf *storageFix) person(t *testing.T) *screenFix {
	t.Helper()
	testutil.SeedRegularUser(t, sf.store, screenUser, screenUserPw)
	u, err := sf.store.GetUserByEmail(context.Background(), screenUser)
	require.NoError(t, err)
	c := freshClient(t)
	testutil.LoginAs(t, sf.srv, c, screenUser, screenUserPw)
	f := &screenFix{asFix: sf.asFix, user: c, userID: u.ID}
	f.setView(t, map[string]any{"enabled": true, "stores": []string{sf.st.Origin()}, "audience": "everyone"})
	return f
}

type catalogAnswer struct {
	StorageNote string `json:"storage_note"`
	Apps        []struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		State   string `json:"state"`
		Storage *struct {
			ForHere      bool   `json:"for_here"`
			Summary      string `json:"summary"`
			Capabilities []struct {
				Label string `json:"label"`
			} `json:"capabilities"`
		} `json:"storage"`
	} `json:"apps"`
}

func TestStoreScreen_StoragePluginsAreOnTheScreenInTheServersWords(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.storageIndex(t)
	p := sf.person(t)

	code, body := p.as(t, http.MethodGet, "/api/app-store/catalog?store="+sf.st.Origin(), nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	var c catalogAnswer
	require.NoError(t, json.Unmarshal(body, &c))
	assert.Contains(t, c.StorageNote, "outside any sandbox")
	byName := map[string]int{}
	for i, a := range c.Apps {
		byName[a.Name] = i
		assert.Equal(t, "none", a.State, a.Name)
	}
	require.Contains(t, byName, "myfs")
	fs := c.Apps[byName["myfs"]]
	require.NotNil(t, fs.Storage)
	assert.True(t, fs.Storage.ForHere)
	assert.Contains(t, fs.Storage.Summary, "9 checks passed")
	require.NotEmpty(t, fs.Storage.Capabilities)
	assert.Equal(t, "writing", fs.Storage.Capabilities[0].Label)
	far := c.Apps[byName["farfs"]]
	require.NotNil(t, far.Storage)
	assert.False(t, far.Storage.ForHere)
	assert.Contains(t, far.Storage.Summary, "No build for this server")
	assert.Nil(t, c.Apps[byName["lang-eo"]].Storage, "an app has no storage part")

	// A request for it is a storage request; the screen then says it waits.
	code, body = p.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": sf.st.Origin(), "app": "myfs", "reason": "For the archive"})
	require.Equal(t, http.StatusCreated, code, "%s", body)
	assert.Contains(t, string(body), `"kind":"storage"`)
	code, body = p.as(t, http.MethodGet, "/api/app-store/catalog?store="+sf.st.Origin(), nil)
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &c))
	for _, a := range c.Apps {
		if a.Name == "myfs" {
			assert.Equal(t, "pending", a.State, "the server's state follows the person's request")
		}
	}
	// No build for this server: nothing to ask for.
	code, body = p.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": sf.st.Origin(), "app": "farfs", "reason": "x"})
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeNoBuild, errCode(body))
}

func TestStoreScreen_NoStoragePluginWhereStoragePluginsAreOff(t *testing.T) {
	sf := newStorageFix(t, false)
	sf.trust(t)
	sf.storageIndex(t)
	p := sf.person(t)
	code, body := p.as(t, http.MethodGet, "/api/app-store/catalog?store="+sf.st.Origin(), nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	var c catalogAnswer
	require.NoError(t, json.Unmarshal(body, &c))
	require.Len(t, c.Apps, 1)
	assert.Equal(t, "lang-eo", c.Apps[0].Name)
	assert.Empty(t, c.StorageNote)
	code, _ = p.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": sf.st.Origin(), "app": "myfs", "reason": "x"})
	assert.Equal(t, http.StatusNotFound, code)
}
