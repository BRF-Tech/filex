package handlers_test

// The store install through the production router, against what the
// security review of b52c8524 found: another store's link cannot take an app
// (or its license key), one reviewed link installs once, a link names the
// commit it was approved at and the filex it is for, a failed paid upgrade
// leaves the license as it was, and an app reads its license's status, not
// its holder.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// testFilex is the fixture's own address (cfg.PublicURL): what a link made
// for it names as filex_origin.
const testFilex = "http://test.local"

func validLicense(req appstore.LicenseRequest) (map[string]any, string) {
	now := time.Now().UTC()
	return map[string]any{"result": "valid", "app": req.App, "licensee": "Acme Ltd.", "instance_id": req.InstanceID,
		"checked_at": now.Format(time.RFC3339Nano), "next_check_by": now.Add(24 * time.Hour).Format(time.RFC3339),
		"grace_until": now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}, ""
}

// commitOf is the commit a test release is "tagged" at: derived from its
// manifest, so two releases never share one.
func commitOf(m []byte) string { return shaHex(m)[:40] }

// release puts a manifest in the fake repository at a tag and at its commit,
// and answers the commit.
func (f *asFix) release(repo, tag string, m []byte) string {
	c := commitOf(m)
	f.gh.put("/"+repo+"/"+tag+"/filex-app.json", m)
	f.gh.put("/"+repo+"/"+c+"/filex-app.json", m)
	return c
}

// linkAt publishes a signed link at store st for lang-eo from repo at tag.
func (f *asFix) linkAt(st *storetest.Store, keyID, token, version, repo string, m []byte, mut func(p map[string]any)) {
	c := f.release(repo, "v"+version, m)
	p := st.IntentPayload("tid-"+token, "lang-eo", version, repo, "v"+version, time.Now().Add(30*time.Minute))
	p["kind"] = "language_pack"
	p["manifest_sha256"] = shaHex(m)
	p["commit"] = c
	p["filex_origin"] = testFilex
	if mut != nil {
		mut(p)
	}
	st.PutIntent(token, &storetest.IntentEntry{Payload: p, KeyID: keyID})
}

func (f *asFix) reviewAt(t *testing.T, st *storetest.Store, token string) (int, reviewBody, []byte) {
	t.Helper()
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent", map[string]any{"store": st.Origin(), "token": token})
	var rb reviewBody
	_ = json.Unmarshal(body, &rb)
	return code, rb, body
}

// ── Another store's link (review #2) ───────────────────────────────────

// Store A sold lang-eo (repository Owner/lang-eo) with a key. A second
// trusted store B signs a paid link for an app of the same name from
// ANOTHER repository, a newer version, no key - and store A itself signs one
// from another repository. Neither link may take the app: the review says
// where the app came from, and nothing is installed; A's key never reaches B.
func TestStoreInstall_AnotherStoresLinkTakesNeitherTheAppNorItsKey(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.st.License = validLicense
	f.linkAt(f.st, "idx-1", "tokentoken-a1", "1.0.0", "Owner/lang-eo", storePack("1.0.0", "Nuligi"), func(p map[string]any) {
		p["paid"] = true
		p["license_key"] = storeLicKey
	})
	code, rb, body := f.review(t, "tokentoken-a1")
	require.Equal(t, http.StatusOK, code, "%s", body)
	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)

	b := storetest.New()
	t.Cleanup(b.Close)
	b.AddKey("idx-b", appstore.UseIndex, appstore.KeyActive)
	b.AddKey("lic-b", appstore.UseLicense, appstore.KeyActive)
	b.License = validLicense
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores", map[string]any{"store": b.Origin(), "fingerprints": b.Fingerprints()})
	require.Equal(t, http.StatusOK, code, "%s", body)

	f.linkAt(b, "idx-b", "tokentoken-b1", "2.0.0", "Evil/lang-eo", storePack("2.0.0", "Other"), func(p map[string]any) { p["paid"] = true })
	code, _, body = f.reviewAt(t, b, "tokentoken-b1")
	t.Logf("review of B's link: %d %s", code, body)
	assert.Equal(t, http.StatusConflict, code, "another store's link for an installed app: %s", body)
	assert.Equal(t, "store_source_changed", errCode(body))
	assert.Contains(t, string(body), f.st.Origin(), "the refusal names the store the app came from")
	assert.Contains(t, string(body), "Owner/lang-eo", "the refusal names the repository the app came from")

	// The first store, another repository.
	f.linkAt(f.st, "idx-1", "tokentoken-a2", "2.0.0", "Other/lang-eo", storePack("2.0.0", "Alia"), nil)
	code, _, body = f.review(t, "tokentoken-a2")
	assert.Equal(t, http.StatusConflict, code, "the same store, another repository: %s", body)
	assert.Equal(t, "store_source_changed", errCode(body))

	inst, ok := f.reg.ByName("lang-eo")
	require.True(t, ok)
	assert.Equal(t, "1.0.0", inst.Row.Version)
	assert.NotContains(t, inst.Row.SourceURL, "Evil/")
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/"+strconv.FormatInt(inst.Row.ID, 10)+"/license/verify", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	for _, v := range b.Verifies() {
		assert.NotEqual(t, storeLicKey, v.Key, "store A's license key was sent to store B (%s)", b.Origin())
	}

	// The same store and repository upgrades, and the review says where the
	// installed app came from.
	f.linkAt(f.st, "idx-1", "tokentoken-a3", "1.1.0", "Owner/lang-eo", storePack("1.1.0", "Nuligi"), func(p map[string]any) { p["paid"] = true })
	code, _, body = f.review(t, "tokentoken-a3")
	require.Equal(t, http.StatusOK, code, "%s", body)
	var up struct {
		UpgradeOf struct {
			Version string `json:"version"`
			Store   string `json:"store"`
			Repo    string `json:"repo"`
		} `json:"upgrade_of"`
	}
	require.NoError(t, json.Unmarshal(body, &up))
	assert.Equal(t, "1.0.0", up.UpgradeOf.Version)
	assert.Equal(t, f.st.Origin(), up.UpgradeOf.Store, "the review names the store the installed app came from")
	assert.Equal(t, "Owner/lang-eo", up.UpgradeOf.Repo, "the review names the repository the installed app came from")
}

// ── One reviewed link installs once (review #4) ────────────────────────

// Two links for one paid app - two reviews, two keys - installed at once:
// one installs, the other is refused, and the license is the winner's: its
// key, confirmed, the app running. Store installs of one app name run one at
// a time; without that, the loser's Require swapped the key under the
// winner's check, and its undo put back a license nobody had confirmed - or
// removed the row outright.
func TestStoreInstall_TwoLinksOfOneAppAtOnceKeepTheWinnersLicense(t *testing.T) {
	keys := []string{"FXL-ONE0-0000-0001", "FXL-TWO0-0000-0002"}
	for attempt := 0; attempt < 12; attempt++ {
		f := newAsFix(t)
		f.trust(t)
		f.st.License = validLicense
		m := storePack("1.0.0", "Nuligi")
		handles := make([]string, len(keys))
		for i, k := range keys {
			tok, key := "tokentoken-k"+strconv.Itoa(i), k
			f.linkAt(f.st, "idx-1", tok, "1.0.0", "Owner/lang-eo", m, func(p map[string]any) {
				p["paid"] = true
				p["license_key"] = key
			})
			code, rb, body := f.review(t, tok)
			require.Equal(t, http.StatusOK, code, "%s", body)
			handles[i] = rb.Handle
		}

		var arrived sync.WaitGroup
		arrived.Add(2)
		var n int32
		orig := f.gh.srv.Config.Handler
		f.gh.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) <= 2 {
				arrived.Done()
				waitTimeout(&arrived, 2*time.Second)
			}
			orig.ServeHTTP(w, r)
		})
		codes := make([]int, len(keys))
		var wg sync.WaitGroup
		for i := range keys {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = storePost(f.admin, f.srv.URL+"/api/admin/app-plugins/store-intent/install",
					map[string]any{"handle": handles[i], "permissions": []string{}})
			}(i)
		}
		wg.Wait()
		winner := -1
		for i, c := range codes {
			if c == http.StatusCreated {
				require.Equal(t, -1, winner, "attempt %d: both installs succeeded (codes %v)", attempt, codes)
				winner = i
			}
		}
		require.NotEqual(t, -1, winner, "attempt %d: neither install succeeded (codes %v)", attempt, codes)
		lic, _ := f.svc.LicenseOf(context.Background(), "lang-eo")
		p, ok := f.reg.ByName("lang-eo")
		require.True(t, ok)
		st, _ := p.State()
		t.Logf("attempt %d: codes=%v license=%+v state=%s", attempt, codes, lic, st)
		require.NotNil(t, lic, "attempt %d: the winner's license row is gone (codes %v)", attempt, codes)
		assert.Equal(t, appstore.Prefix(keys[winner]), lic.KeyPrefix, "attempt %d: the license is not the winner's key", attempt)
		assert.Equal(t, appstore.StatusValid, lic.Status, "attempt %d: the winner's license is not confirmed", attempt)
		assert.Equal(t, wasmplugin.StateRunning, st, "attempt %d", attempt)
	}
}

func storePost(client *http.Client, url string, body any) int {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// Two POST /store-intent/install with ONE reviewed handle at once (a double
// click, or on purpose): one installs, the other finds the review taken - and
// its failure path does not forget the license of the app the first one just
// installed.
func TestStoreInstall_TwoInstallsOfOneReviewKeepTheLicense(t *testing.T) {
	for attempt := 0; attempt < 12; attempt++ {
		f := newAsFix(t)
		f.trust(t)
		f.linkAt(f.st, "idx-1", "tokentoken-1", "1.0.0", "Owner/lang-eo", storePack("1.0.0", "Nuligi"), func(p map[string]any) { p["paid"] = true })
		code, rb, body := f.review(t, "tokentoken-1")
		require.Equal(t, http.StatusOK, code, "%s", body)

		var arrived sync.WaitGroup
		arrived.Add(2)
		var n int32
		orig := f.gh.srv.Config.Handler
		f.gh.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) <= 2 {
				arrived.Done()
				waitTimeout(&arrived, 2*time.Second)
			}
			orig.ServeHTTP(w, r)
		})
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = storePost(f.admin, f.srv.URL+"/api/admin/app-plugins/store-intent/install",
					map[string]any{"handle": rb.Handle, "permissions": []string{}})
			}(i)
		}
		wg.Wait()
		p, ok := f.reg.ByName("lang-eo")
		lic, _ := f.svc.LicenseOf(context.Background(), "lang-eo")
		st := ""
		if ok {
			st, _ = p.State()
		}
		t.Logf("attempt %d: codes=%v installed=%v state=%s license_row=%v", attempt, codes, ok, st, lic != nil)
		require.True(t, ok, "attempt %d: nothing installed (codes %v)", attempt, codes)
		if lic == nil || st == wasmplugin.StateRunning {
			t.Fatalf("attempt %d: a PAID app installed without a key: state=%s license_row=%v (codes %v)", attempt, st, lic != nil, codes)
		}
		won, taken := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusCreated:
				won++
			case http.StatusNotFound:
				taken++
			}
		}
		assert.Equal(t, 1, won, "attempt %d: exactly one of the two installs (codes %v)", attempt, codes)
		assert.Equal(t, 1, taken, "attempt %d: the other finds the review taken, 404 intent_session_unknown (codes %v)", attempt, codes)
	}
}

// waitTimeout waits for wg, at most d: a request that never comes (the second
// one, refused before it reads the repository) must not hang the first.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// ── A link names its commit and its filex (review #8) ──────────────────

// The repository is read at the commit the store signed; the tag must still
// serve the same manifest there.
func TestStoreInstall_TheRepositoryIsReadAtTheSignedCommit(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)

	// The tag serves the pack, the commit the store signed serves nothing.
	m := storePack("1.0.0", "Nuligi")
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", m)
	p := f.st.IntentPayload("tid-c1", "lang-eo", "1.0.0", "Owner/lang-eo", "v1.0.0", time.Now().Add(30*time.Minute))
	p["kind"], p["manifest_sha256"], p["commit"], p["filex_origin"] = "language_pack", shaHex(m), commitOf(m), testFilex
	f.st.PutIntent("tokentoken-c1", &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
	code, _, body := f.review(t, "tokentoken-c1")
	assert.NotEqual(t, http.StatusOK, code, "the signed commit serves nothing, yet the review opened: %s", body)

	// The commit serves the approved pack, the tag has moved to other bytes.
	m2 := storePack("1.0.0", "Changed")
	f.gh.put("/Owner/lang-eo/"+commitOf(m)+"/filex-app.json", m)
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", m2)
	p = f.st.IntentPayload("tid-c2", "lang-eo", "1.0.0", "Owner/lang-eo", "v1.0.0", time.Now().Add(30*time.Minute))
	p["kind"], p["manifest_sha256"], p["commit"], p["filex_origin"] = "language_pack", shaHex(m), commitOf(m), testFilex
	f.st.PutIntent("tokentoken-c2", &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
	code, _, body = f.review(t, "tokentoken-c2")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodePinMismatch, errCode(body))
	var e struct {
		Detail struct {
			Mismatches []appstore.Mismatch `json:"mismatches"`
		} `json:"detail"`
	}
	_ = json.Unmarshal(body, &e)
	require.Len(t, e.Detail.Mismatches, 1, "%s", body)
	assert.Equal(t, "commit", e.Detail.Mismatches[0].Field, "the tag no longer serves the commit's manifest: %s", body)

	// Both serve the same bytes: the review opens.
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", m)
	p = f.st.IntentPayload("tid-c3", "lang-eo", "1.0.0", "Owner/lang-eo", "v1.0.0", time.Now().Add(30*time.Minute))
	p["kind"], p["manifest_sha256"], p["commit"], p["filex_origin"] = "language_pack", shaHex(m), commitOf(m), testFilex
	f.st.PutIntent("tokentoken-c3", &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
	code, _, body = f.review(t, "tokentoken-c3")
	assert.Equal(t, http.StatusOK, code, "%s", body)
}

// A link names the filex it was made for (filex_origin, signed): another
// filex refuses it (a link opened on the wrong filex, or sent to another
// filex's administrator, installs nothing there).
func TestStoreInstall_ALinkForAnotherFilexIsRefused(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	m := storePack("1.0.0", "Nuligi")
	cases := []struct {
		token, origin string
		drop          bool
		status        int
		code          string
	}{
		{"tokentoken-o1", "https://other.example", false, http.StatusBadRequest, "intent_wrong_instance"},
		{"tokentoken-o2", "http://test.local.evil.example", false, http.StatusBadRequest, "intent_wrong_instance"},
		{"tokentoken-o3", "", true, http.StatusBadRequest, appstore.CodeIntentInvalid},
		{"tokentoken-o4", "http://test.local/some/path", false, http.StatusBadRequest, appstore.CodeIntentInvalid},
		{"tokentoken-o5", "HTTP://Test.Local:80", false, http.StatusOK, ""},
	}
	for _, c := range cases {
		f.linkAt(f.st, "idx-1", c.token, "1.0.0", "Owner/lang-eo", m, func(p map[string]any) {
			if c.drop {
				delete(p, "filex_origin")
			} else {
				p["filex_origin"] = c.origin
			}
		})
		code, _, body := f.review(t, c.token)
		assert.Equal(t, c.status, code, "filex_origin %q: %s", c.origin, body)
		if c.code != "" {
			assert.Equal(t, c.code, errCode(body), "filex_origin %q: %s", c.origin, body)
		}
	}
}

// ── A failed paid upgrade (review #9) ──────────────────────────────────

// A paid upgrade whose link carries a new key, failing at the install (here:
// a permission that does not parse): the license is what it was - the old
// key, the old answers - and the app keeps running.
func TestStoreInstall_AFailedPaidUpgradeLeavesTheLicenseAsItWas(t *testing.T) {
	ctx := context.Background()
	f := newAsFix(t)
	f.trust(t)
	f.st.License = validLicense
	f.linkAt(f.st, "idx-1", "tokentoken-1", "1.0.0", "Owner/lang-eo", storePack("1.0.0", "Nuligi"), func(p map[string]any) {
		p["paid"] = true
		p["license_key"] = storeLicKey
	})
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	before, _ := f.svc.LicenseOf(ctx, "lang-eo")
	require.NotNil(t, before)
	require.Equal(t, appstore.StatusValid, before.Status)

	f.linkAt(f.st, "idx-1", "tokentoken-2", "1.1.0", "Owner/lang-eo", storePack("1.1.0", "Nuligi"), func(p map[string]any) {
		p["paid"] = true
		p["license_key"] = "FXL-NEWK-EY00-0002"
	})
	code, rb, body = f.review(t, "tokentoken-2")
	require.Equal(t, http.StatusOK, code, "%s", body)
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/install",
		map[string]any{"handle": rb.Handle, "permissions": []string{"not a permission"}})
	require.NotEqual(t, http.StatusCreated, code, "the upgrade was expected to fail: %s", body)

	after, _ := f.svc.LicenseOf(ctx, "lang-eo")
	require.NotNil(t, after)
	assert.Equal(t, before.KeyPrefix, after.KeyPrefix, "a failed upgrade changed the license key")
	assert.Equal(t, appstore.StatusValid, after.Status, "a failed upgrade dropped the license's answers")
	p, _ := f.reg.ByName("lang-eo")
	st, _ := p.State()
	assert.Equal(t, wasmplugin.StateRunning, st, "a failed upgrade held the app")
	assert.Equal(t, "1.0.0", p.Row.Version)
}

// ── What an app reads about its license (review #11) ───────────────────

// fx.license.get() is open to every user who may run apps, a tenant's
// included, and to tokens: it says the status and the dates, never who holds
// the license.
func TestStoreInstall_AnAppReadsItsStatusNotItsLicensee(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.st.License = validLicense
	f.linkAt(f.st, "idx-1", "tokentoken-1", "1.0.0", "Owner/lang-eo", storePack("1.0.0", "Nuligi"), func(p map[string]any) {
		p["paid"] = true
		p["license_key"] = storeLicKey
	})
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)

	for name, tok := range map[string]string{"session": "", "API key": f.key} {
		code, body = f.call(t, tok, http.MethodGet, "/api/files/plugins/license/lang-eo", nil)
		require.Equal(t, http.StatusOK, code, "%s: %s", name, body)
		assert.Contains(t, string(body), `"status":"valid"`, name)
		assert.NotContains(t, string(body), "Acme", "%s: the license holder's name was handed to the app", name)
		assert.NotContains(t, string(body), "licensee", name)
	}
}

// Without FILEX_PUBLIC_URL, a link's filex_origin is held to the origin the
// request arrived at: the Host header, and https when TLS or
// X-Forwarded-Proto says so. Another filex's link is refused there too.
func TestStoreInstall_WithoutAPublicURLTheRequestsOriginDecides(t *testing.T) {
	f := newAsFixWith(t, func(cfg *config.Config) {
		cfg.PublicURL = ""
		cfg.PublicURLSet = false
	})
	f.trust(t)
	self := strings.ToLower(f.srv.URL)
	m := storePack("1.0.0", "Nuligi")

	f.linkAt(f.st, "idx-1", "tokentoken-u1", "1.0.0", "Owner/lang-eo", m, func(p map[string]any) { p["filex_origin"] = testFilex })
	code, _, body := f.review(t, "tokentoken-u1")
	assert.Equal(t, http.StatusBadRequest, code, "%s", body)
	assert.Equal(t, "intent_wrong_instance", errCode(body))
	assert.Contains(t, string(body), self, "the refusal names the origin this filex was reached at")

	f.linkAt(f.st, "idx-1", "tokentoken-u2", "1.0.0", "Owner/lang-eo", m, func(p map[string]any) { p["filex_origin"] = self })
	code, _, body = f.review(t, "tokentoken-u2")
	assert.Equal(t, http.StatusOK, code, "the request's own origin: %s", body)

	https := "https://" + strings.TrimPrefix(self, "http://")
	f.linkAt(f.st, "idx-1", "tokentoken-u3", "1.0.0", "Owner/lang-eo", m, func(p map[string]any) { p["filex_origin"] = https })
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent",
		map[string]any{"store": f.st.Origin(), "token": "tokentoken-u3"}, "X-Forwarded-Proto", "https")
	assert.Equal(t, http.StatusOK, code, "behind a TLS proxy (X-Forwarded-Proto: https): %s", body)
	f.linkAt(f.st, "idx-1", "tokentoken-u4", "1.0.0", "Owner/lang-eo", m, func(p map[string]any) { p["filex_origin"] = https })
	code, _, body = f.review(t, "tokentoken-u4")
	assert.Equal(t, http.StatusBadRequest, code, "https named, plain http reached: %s", body)
}

// An app installed straight from its repository (no store) is not taken
// under a store's license by that store's PAID link for the same repository:
// the store could hold it whenever it liked, and removing the app - its data
// with it - would be the only way out. The link is refused like a link from
// elsewhere (store_source_changed, the installed side naming no store); a
// FREE link for the same repository still upgrades it (store review, second
// round, Y3).
func TestStoreInstall_AStoresPaidLinkDoesNotAdoptADirectGitHubApp(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	m1 := storePack("1.0.0", "Nuligi")
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", m1)
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins", map[string]any{"github_repo": "Owner/lang-eo", "ref": "v1.0.0", "permissions": []string{}})
	require.Equal(t, http.StatusCreated, code, "%s", body)

	b := storetest.New()
	t.Cleanup(b.Close)
	b.AddKey("idx-b", appstore.UseIndex, appstore.KeyActive)
	b.AddKey("lic-b", appstore.UseLicense, appstore.KeyActive)
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores", map[string]any{"store": b.Origin(), "fingerprints": b.Fingerprints()})
	require.Equal(t, http.StatusOK, code, "%s", body)
	f.linkAt(b, "idx-b", "tokentoken-b1", "1.1.0", "Owner/lang-eo", storePack("1.1.0", "Nuligi"), func(p map[string]any) { p["paid"] = true })
	code, _, body = f.reviewAt(t, b, "tokentoken-b1")
	require.Equal(t, http.StatusConflict, code, "a paid link for a storeless app: %s", body)
	assert.Equal(t, appstore.CodeSourceChanged, errCode(body))
	var e struct {
		Detail struct {
			Installed struct {
				Store string `json:"store"`
				Repo  string `json:"repo"`
			} `json:"installed"`
		} `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, "", e.Detail.Installed.Store, "the installed side names no store: %s", body)
	assert.Equal(t, "Owner/lang-eo", e.Detail.Installed.Repo)
	inst, _ := f.reg.ByName("lang-eo")
	assert.Equal(t, "1.0.0", inst.Row.Version, "nothing was upgraded")
	lic, _ := f.svc.LicenseOf(context.Background(), "lang-eo")
	assert.Nil(t, lic, "no license row was made")

	f.linkAt(f.st, "idx-1", "tokentoken-a1", "2.0.0", "Owner/lang-eo", storePack("2.0.0", "Nuligi"), nil)
	code, rb, body := f.review(t, "tokentoken-a1")
	require.Equal(t, http.StatusOK, code, "a free link for the same repository still upgrades it: %s", body)
	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	inst, _ = f.reg.ByName("lang-eo")
	assert.Equal(t, "2.0.0", inst.Row.Version)
}

// A FILEX_PUBLIC_URL that is not an address refuses every link, saying so:
// it never falls back to the request's Host, which the operator who set it
// did not mean (store review, second round, Y4). Nothing is asked of the
// store's link.
func TestStoreInstall_AnUnusablePublicURLRefusesEveryLink(t *testing.T) {
	f := newAsFixWith(t, func(cfg *config.Config) {
		cfg.PublicURL = "files.example.com/filex"
		cfg.PublicURLSet = true
	})
	f.trust(t)
	f.linkAt(f.st, "idx-1", "tokentoken-p1", "1.0.0", "Owner/lang-eo", storePack("1.0.0", "Nuligi"), func(p map[string]any) {
		p["filex_origin"] = strings.ToLower(f.srv.URL)
	})
	hits := f.st.IntentReads()
	code, _, body := f.review(t, "tokentoken-p1")
	assert.Equal(t, http.StatusBadRequest, code, "%s", body)
	assert.Equal(t, "intent_wrong_instance", errCode(body))
	assert.Contains(t, string(body), "public_url_invalid")
	assert.Contains(t, string(body), "FILEX_PUBLIC_URL")
	assert.Equal(t, hits, f.st.IntentReads(), "the link was not read")
}
