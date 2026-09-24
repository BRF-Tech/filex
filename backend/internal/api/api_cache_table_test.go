package api_test

// The whole route table against the cache policy: `public` on exactly the four
// answers that say who this instance is, and on nothing else, for anybody.
//
// ⚠⚠ Two designs meet here and both are right about different things.
// PR #41 (Berk Başarır, 2026-09-24) made every /api answer `no-store` after a
// CDN "cache everything" rule served one administrator's GET /api/auth/me to
// everyone who asked. v0.43.0 had just made the four identity answers —
// /api/branding, /api/appearance, /api/public/branding and
// /api/public/ui-locales/{code} — `public, no-cache` with a strong ETag, so a
// public page load costs a 304 (handlers/public_cache.go). The four are the
// same for every visitor of a host; everything else may be somebody's.
//
// So the exception is asserted as a CLOSED set, measured by asking the running
// server rather than by reading the code: a fifth `public` answer — a handler
// that borrowed writePublicJSON, a `max-age` somebody thought harmless — goes
// red here on the day it is written, whoever was signed in.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// instanceIdentityRoutes are the only routes allowed to answer `public`.
// ⚠ Adding a line here is a decision that the answer is the same for every
// person who can reach the host — never a way to make this test pass.
var instanceIdentityRoutes = map[string]bool{
	"/api/branding":                 true,
	"/api/appearance":               true,
	"/api/public/branding":          true,
	"/api/public/ui-locales/{code}": true,
}

func TestAPICache_PublicOnlyOnTheInstanceIdentity(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	adminEmail, adminPw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, adminEmail, adminPw)
	admin, err := store.GetUserByEmail(context.Background(), adminEmail)
	require.NoError(t, err)

	testutil.SeedRegularUser(t, store, probeUserEmail, probeUserPw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	userClient := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, userClient, probeUserEmail, probeUserPw)

	// /api/ai/* authenticates by token only; a cookie there is anonymous.
	adminToken := issueProbeToken(t, store, admin.ID, "read,write,delete,mcp,admin")
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	adminClient.CheckRedirect = anon.CheckRedirect
	userClient.CheckRedirect = anon.CheckRedirect

	routes, ok := srv.Config.Handler.(chi.Routes)
	require.True(t, ok, "the router is no longer a chi.Routes, and this test walks it")
	var gets []string
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && strings.HasPrefix(route, "/api/") {
			gets = append(gets, route)
		}
		return nil
	}))
	sort.Strings(gets)
	// Anti-vacuity: every assertion below is a loop over this slice.
	require.Greater(t, len(gets), 120, "chi.Walk found %d GET /api routes (145 on the day this was written); the walk broke", len(gets))

	type principal struct {
		name   string
		client *http.Client
		token  string
	}
	principals := []principal{
		{"anonymous", anon, ""},
		{"member (cookie)", userClient, ""},
		{"admin (cookie)", adminClient, ""},
		{"admin (token)", anon, adminToken},
	}

	var leaks, bare []string
	publicSeen := map[string]bool{}
	answered := 0
	for _, route := range gets {
		for _, p := range principals {
			req, rerr := http.NewRequest(http.MethodGet, srv.URL+concretePath(route), nil)
			require.NoError(t, rerr)
			if p.token != "" {
				req.Header.Set("X-Filex-Token", p.token)
			}
			resp, rerr := p.client.Do(req)
			require.NoError(t, rerr, "%s as %s", route, p.name)
			_ = resp.Body.Close()
			answered++

			cc := strings.ToLower(strings.Join(resp.Header.Values("Cache-Control"), ", "))
			line := fmt.Sprintf("%-46s as %-16s → %d %q", route, p.name, resp.StatusCode, cc)
			switch {
			case resp.StatusCode == http.StatusSwitchingProtocols:
				// A WebSocket upgrade has no cacheable body.
			case cc == "":
				bare = append(bare, line)
			case strings.Contains(cc, "public"):
				if !instanceIdentityRoutes[route] {
					leaks = append(leaks, line)
					break
				}
				publicSeen[route] = true
				require.Equal(t, "public, no-cache", cc, "%s", line)
				require.NotEmpty(t, resp.Header.Get("ETag"), "%s revalidates against an ETag", line)
			case !strings.Contains(cc, "no-store") && !strings.Contains(cc, "private"):
				// Neither shared-cacheable by name nor forbidden from being
				// stored: a shared cache may still keep it (heuristics).
				leaks = append(leaks, line)
			}
		}
		// A GET that ended a session (none should) would turn every later
		// probe into an anonymous one and the walk into a measurement of 401s.
		for _, c := range []struct {
			cl        *http.Client
			email, pw string
		}{{userClient, probeUserEmail, probeUserPw}, {adminClient, adminEmail, adminPw}} {
			me, merr := c.cl.Get(srv.URL + "/api/auth/me")
			require.NoError(t, merr)
			_ = me.Body.Close()
			if me.StatusCode != http.StatusOK {
				testutil.LoginAs(t, srv, c.cl, c.email, c.pw)
			}
		}
	}

	t.Logf("cache policy: %d GET /api routes × %d principals = %d answers; public on %v",
		len(gets), len(principals), answered, publicSeen)

	require.Empty(t, leaks,
		"%d /api answers may be kept by a shared cache although they are not one of the four\n"+
			"    instance-identity answers:\n      %s\n"+
			"    A CDN rule that caches everything served one administrator's /api/auth/me to every\n"+
			"    visitor (PR #41). Let api.APINoStore's `no-store` stand, or say `private, …`.",
		len(leaks), strings.Join(leaks, "\n      "))
	require.Empty(t, bare,
		"%d /api answers carry no Cache-Control at all — a cache-everything rule takes that as permission:\n      %s",
		len(bare), strings.Join(bare, "\n      "))
	// The exception was really exercised, not merely allowed: the three that
	// need no installed app answer 200 on a bare instance.
	for _, r := range []string{"/api/branding", "/api/appearance", "/api/public/branding"} {
		require.True(t, publicSeen[r], "%s never answered `public, no-cache` — the walk did not reach it", r)
	}
}

// The four are the same answer for everybody — which is the only reason a
// shared cache may hold them. Same bytes (same ETag) whoever asks.
func TestAPICache_TheIdentityAnswersAreTheSameForEverybody(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	adminEmail, adminPw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, adminEmail, adminPw)
	testutil.SeedRegularUser(t, store, probeUserEmail, probeUserPw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	userClient := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, userClient, probeUserEmail, probeUserPw)

	for _, path := range []string{"/api/branding", "/api/appearance", "/api/public/branding"} {
		tags := map[string]string{}
		for name, c := range map[string]*http.Client{"anonymous": http.DefaultClient, "member": userClient, "admin": adminClient} {
			req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			require.NoError(t, err)
			req.Header.Set("Accept-Language", "en")
			resp, err := c.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode, "%s as %s", path, name)
			require.Equal(t, "public, no-cache", resp.Header.Get("Cache-Control"), "%s as %s", path, name)
			tags[name] = resp.Header.Get("ETag")
		}
		require.Equal(t, tags["anonymous"], tags["member"], "%s: a member got a different answer than a stranger", path)
		require.Equal(t, tags["anonymous"], tags["admin"], "%s: an admin got a different answer than a stranger", path)
	}
}
