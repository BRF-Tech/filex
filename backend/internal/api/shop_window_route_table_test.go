package api_test

// The whole route table, classified — the exhaustive half of the shop-window
// gate's demo check.
//
// ⚠⚠ The defect this exists for was MEASURED by walking the real route table:
// on 2026-09-07 a public demo answered all 101 routes under /api/admin with the
// published credentials and not one returned 403. The gate that came out of
// that day probes six of them (`scripts/shop-window-data.mjs` →
// DEMO_GUARDED_WRITES, fired at a live instance by
// `scripts/check-shop-window.mjs --instance`). Six is a smoke test: it proves
// the guard is installed. It cannot see a **fourth guarded prefix** — an
// operator surface mounted somewhere `demoGuardedPrefixes` has never heard of
// — which is exactly how the first hole appeared, because /api/ai/admin was
// the same admin surface behind a different front door.
//
// So this walks all of them, from chi, and classifies each one by ASKING THE
// RUNNING SERVER rather than by reading a list:
//
//	anonymous → 401, ordinary signed-in user → 403   an OPERATOR SURFACE
//	anonymous and ordinary user get the SAME answer  not role-gated at all
//	otherwise (the ordinary user got through)        the product
//
// The middle line is what makes the taxonomy total instead of leaving a bucket
// of "could not tell". A route that answers an anonymous caller and a signed-in
// non-admin identically does not distinguish principals at all, so it cannot be
// admin-only — /dav/* (its own Basic-auth realm) and POST /api/auth/login (a
// wrong password is a wrong password) land there, and neither needs an
// exception written down anywhere.
//
// ⚠ Why not read the middleware chain instead? chi cannot show it. `chi.Walk`
// hands back the middlewares of the mux it walks, and
// `r.Group(func(r chi.Router){ r.Use(auth.RequireAdmin) … })` builds an inline
// router whose chain is baked into the handler before registration. Measured
// while writing this: all 359 walked entries report the same four top-level
// middlewares and not one reports RequireAdmin. Behaviour is the only readable
// signal, so behaviour is what is read.
//
// ⚠ This must never become a list somebody maintains — that is the failure
// mode it exists to prevent. Nothing below names a route. The floors are counts.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The probing principal is deliberately NOT an admin: the whole classification
// turns on what changes between anonymous and "signed in, holding no role".
const (
	probeUserEmail = "route-table-probe@test.local"
	probeUserPw    = "TestUserPass!1"
)

var routeParam = regexp.MustCompile(`\{[^}]*\}`)

// concretePath turns a chi pattern into a URL the router will match. `{id}` → 1
// and a trailing `/*` is dropped: the probe only needs the request to REACH the
// route, and every gate that decides the classification runs before the handler
// looks at the value.
func concretePath(route string) string {
	p := routeParam.ReplaceAllString(route, "1")
	p = strings.TrimSuffix(p, "/*")
	if p == "" {
		p = "/"
	}
	return p
}

// probeBody is invalid on purpose, and that is what makes it safe to fire two
// hundred state-changing requests at a live router: a gate answers before the
// handler ever sees it, and a handler that IS reached answers 400. The one
// thing it must never be is a well-formed request — see demo_exposure_test.go's
// seedVictim for what happens when a probe succeeds and takes the session with
// it.
const probeBody = `{"__shop_window_route_probe":true}`

type routeEntry struct {
	method string
	route  string
}

type verdict struct {
	routeEntry
	anon, user int
	principal  string
}

func (v verdict) operatorSurface() bool {
	return v.user == http.StatusForbidden && v.anon != http.StatusForbidden
}

func (v verdict) indistinct() bool { return v.anon == v.user }

// TestShopWindow_DemoGuardCoversEveryOperatorSurface walks the real route table
// and asserts the demo guard refuses every state-changing route that a role
// gate protects.
//
// ⚠ Stated as "operator surface ⇒ refused", never as "these paths are refused".
// A new admin surface at /api/operator/… goes red the day it is added, with no
// list to update; and a maintainer cannot make it green by widening the guard
// over the product, because TestShopWindow_DemoStillDemonstratesTheProduct
// below measures the other direction.
func TestShopWindow_DemoGuardCoversEveryOperatorSurface(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		// The guard is exercised through api.DemoGuard directly, so the router
		// under the probes is an ORDINARY install. Booting it in demo mode
		// would answer 403 everywhere and the classification would be a picture
		// of the guard rather than of the roles.
		c.Demo.Mode = false
	}, nil)

	// An admin has to exist: an instance with no administrator is not the shape
	// any of these gates were written against.
	testutil.SeedAdmin(t, store)
	testutil.SeedRegularUser(t, store, probeUserEmail, probeUserPw)
	u, err := store.GetUserByEmail(context.Background(), probeUserEmail)
	require.NoError(t, err)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	userClient := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, userClient, probeUserEmail, probeUserPw)

	// The token half of the same principal. /api/ai/* authenticates by token
	// only, so a cookie there IS anonymous — without this, every route on the
	// token-auth admin surface would look "indistinct" and the /api/ai/admin
	// hole found on 2026-09-07 would be invisible all over again.
	userToken := issueProbeToken(t, store, u.ID, "read,write,delete,mcp")
	anonClient := &http.Client{}

	// ── the table ────────────────────────────────────────────────────────────
	routes, ok := srv.Config.Handler.(chi.Routes)
	require.True(t, ok, "the router is no longer a chi.Routes, and this whole test walks it")

	var table []routeEntry
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		table = append(table, routeEntry{method, route})
		return nil
	}))
	sort.Slice(table, func(i, j int) bool {
		if table[i].route != table[j].route {
			return table[i].route < table[j].route
		}
		return table[i].method < table[j].method
	})

	// Anti-vacuity, first. Every assertion below is a loop over this slice, so
	// an empty or truncated walk would pass all of them in silence — and this
	// project has already shipped two gates that measured nothing.
	require.Greater(t, len(table), 250,
		"chi.Walk returned %d routes. The table has been ~359 entries since the launch audit; "+
			"a number this small means the walk broke, not that the product shrank", len(table))

	send := func(c *http.Client, token string, e routeEntry) int {
		req, rerr := http.NewRequest(e.method, srv.URL+concretePath(e.route), strings.NewReader(probeBody))
		require.NoError(t, rerr)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Filex-Token", token)
		}
		resp, rerr := c.Do(req)
		require.NoError(t, rerr, "%s %s", e.method, e.route)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	var verdicts []verdict
	for _, e := range table {
		switch e.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// Reads are the half a demo deliberately keeps open, and the guard
			// says so in one switch. Classifying them would measure the demo
			// policy rather than the route.
			continue
		}

		v := verdict{routeEntry: e, principal: "cookie"}
		v.user = send(userClient, "", e)
		if v.user == http.StatusUnauthorized {
			// Either a token-authenticated route, or the probe just logged this
			// session out (POST /api/auth/logout is in the table). Both are
			// answered by trying the token principal; the cookie is restored
			// below so the next route is measured, not the wreckage of this one.
			v.principal = "token"
			v.user = send(anonClient, userToken, e)
		}
		v.anon = send(anonClient, "", e)
		reviveSession(t, srv, userClient)
		verdicts = append(verdicts, v)
	}

	// ── the assertion ────────────────────────────────────────────────────────
	var operators, indistinct, product int
	var holes []string
	for _, v := range verdicts {
		switch {
		case v.operatorSurface():
			operators++
			if !demoGuardRefuses(v.method, v.route) {
				holes = append(holes, fmt.Sprintf("%-7s %-46s (anonymous %d → signed-in non-admin %d, %s)",
					v.method, v.route, v.anon, v.user, v.principal))
			}
		case v.indistinct():
			indistinct++
		default:
			product++
		}
	}

	t.Logf("route table: %d entries, %d state-changing — %d operator surfaces, %d not role-gated, %d product",
		len(table), len(verdicts), operators, indistinct, product)

	require.Empty(t, holes,
		"%d state-changing OPERATOR routes are not refused on a demo:\n      %s\n"+
			"    A demo publishes its admin login on purpose, so \"admin-only\" and \"public\" are the\n"+
			"    same set of people there. Every line above is a door a visitor holding the published\n"+
			"    credentials can open. Add the MOUNT POINT to demoGuardedPrefixes in\n"+
			"    backend/internal/api/demo_guard.go — a prefix covers the routes that do not exist\n"+
			"    yet, which is the whole reason the guard is written that way.",
		len(holes), strings.Join(holes, "\n      "))

	// The second anti-vacuity floor, and the more important one: every
	// classification above comes from a live request, so a harness that quietly
	// stopped authenticating would report every route "indistinct" and leave
	// the assertion with no subjects at all.
	require.Greater(t, operators, 80,
		"only %d state-changing operator surfaces were found. There were 115 on the day this was "+
			"written (102 under the two admin prefixes, plus /metrics). A number this small means "+
			"the probe stopped being able to sign in, not that the admin surface shrank — and with "+
			"no subjects the assertion above proves nothing", operators)
}

// TestShopWindow_DemoStillDemonstratesTheProduct is the other direction, and
// the reason the guard cannot simply be widened until the test above goes
// quiet. A guard over /api would refuse everything, satisfy "every operator
// surface is refused", and leave a demo that demonstrates nothing.
func TestShopWindow_DemoStillDemonstratesTheProduct(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) { c.Demo.Mode = false }, nil)
	testutil.SeedAdmin(t, store)
	testutil.SeedRegularUser(t, store, probeUserEmail, probeUserPw)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	userClient := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, userClient, probeUserEmail, probeUserPw)

	routes, ok := srv.Config.Handler.(chi.Routes)
	require.True(t, ok)

	var refusedProduct []string
	reachable := 0
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return nil
		}
		req, rerr := http.NewRequest(method, srv.URL+concretePath(route), strings.NewReader(probeBody))
		if rerr != nil {
			return rerr
		}
		req.Header.Set("Content-Type", "application/json")
		resp, rerr := userClient.Do(req)
		if rerr != nil {
			return rerr
		}
		_ = resp.Body.Close()
		reviveSession(t, srv, userClient)
		// Only the routes an ordinary user demonstrably gets THROUGH: a 2xx, or
		// a 4xx that is the handler complaining about the deliberately invalid
		// body. 401 and 403 mean a gate answered, and those are the subject of
		// the test above.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil
		}
		reachable++
		if demoGuardRefuses(method, route) {
			refusedProduct = append(refusedProduct,
				fmt.Sprintf("%-7s %-46s (an ordinary user gets %d here)", method, route, resp.StatusCode))
		}
		return nil
	}))

	require.Greater(t, reachable, 40,
		"only %d routes were reachable by an ordinary signed-in user; the probe is not signing in", reachable)

	// ⚠ The identity routes are the deliberate exception, and the guard's own
	// first criterion: the demo account is SHARED, so a visitor changing its
	// password or enrolling TOTP takes the demo away from everybody else until
	// the nightly restore. They are product routes for one person and a denial
	// of service for every other reader.
	var unexpected []string
	for _, r := range refusedProduct {
		if !strings.Contains(r, "/api/auth/password") &&
			!strings.Contains(r, "/api/auth/profile") &&
			!strings.Contains(r, "/api/auth/totp") {
			unexpected = append(unexpected, r)
		}
	}
	require.Empty(t, unexpected,
		"the demo guard refuses %d routes an ordinary user is entitled to use:\n      %s\n"+
			"    Uploading, renaming, sharing and tagging ARE the demo; the nightly restore is what\n"+
			"    makes them safe. Guarding them turns the demo into a screenshot.",
		len(unexpected), strings.Join(unexpected, "\n      "))
}

// reviveSession signs the probing user back in if a probe just logged them out.
//
// ⚠⚠ Not optional, and not cosmetic. POST /api/auth/logout is in the table and
// the probe fires it like any other route; from that point on every remaining
// route answers the ordinary user 401, every verdict reads "indistinct", and
// the operator-surface assertion runs out of subjects while reporting success.
// That is exactly the shape demo_exposure_test.go's seedVictim comment warns
// about, one layer up.
//
// GET /api/auth/me first, because the alternative — signing in again after
// every probe — is a bcrypt round each time and it doubled the runtime of this
// test for nothing.
func reviveSession(t *testing.T, srv *httptest.Server, c *http.Client) {
	t.Helper()
	resp, err := c.Get(srv.URL + "/api/auth/me")
	require.NoError(t, err)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return
	}
	testutil.LoginAs(t, srv, c, probeUserEmail, probeUserPw)
}

// demoGuardRefuses asks the product's own middleware instead of
// re-implementing its prefix table here. A second copy of the rules is how a
// gate starts agreeing with itself and disagreeing with the server.
func demoGuardRefuses(method, route string) bool {
	rec := httptest.NewRecorder()
	api.DemoGuard(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(rec, httptest.NewRequest(method, concretePath(route), nil))
	return rec.Code == http.StatusForbidden
}

func issueProbeToken(t *testing.T, store db.Store, userID int64, scopes string) string {
	t.Helper()
	b := make([]byte, 16)
	_, err := rand.Read(b)
	require.NoError(t, err)
	plain := "tok_" + hex.EncodeToString(b)
	_, err = store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    userID,
		Label:     "shop-window route probe",
		TokenHash: apitoken.HashToken(plain),
		Scopes:    scopes,
	})
	require.NoError(t, err)
	return plain
}
