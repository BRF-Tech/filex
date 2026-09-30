package api_test

// Every state-changing route of the signed-in product says how per-user
// permissions (internal/perm) apply to it — the safety net under phase 2.
//
// ⚠ This IS a list, which shop_window_route_table_test.go warns against, and
// the warning is why it is built the way it is. A list rots when it can go
// stale silently. This one cannot:
//
//   - COMPLETE: chi.Walk finds every POST/PUT/PATCH/DELETE (and the rest of
//     the non-GET verbs) under the prefixes below; one this table does not
//     classify fails the test, so a new endpoint has to be given a
//     permission — or a written reason it needs none — to merge.
//   - NOT STALE: an entry whose route no longer exists fails too.
//   - PROVEN where it can be: a route gated by handlers.RequirePermission is
//     fired by a user with exactly that permission denied, and must answer
//     the structured permission_denied naming it. The gate runs before the
//     body is read, so the probe needs no valid body.
//
// Handler-checked routes decide the permission from the request (create vs
// modify, a download vs a drop link, the op's kind), so a body-less probe
// cannot reach the check; handlers/perm_enforce_test.go drives each of them
// with a real request instead.
//
// The admin area is not listed: /api/admin is RequireAdmin or, per route,
// RequireAdminPermission, and TestPermRouteTable_AdminAreaClosedToPlainUsers
// proves that behaviourally over all of it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// permRoutePrefixes are the signed-in product surfaces this table covers.
// Public link pages (/p, /s, /d, /u), the protocol servers (/dav, /s3) and
// /metrics are outside: the first have no account behind the request, the
// protocols are phase 3, and /metrics is RequireAdmin.
var permRoutePrefixes = []string{"/api/files/", "/api/ai/", "/api/auth/", "/api/tokens", "/api/me/", "/api/notifications/", "/api/sharex/"}

type permRoute struct {
	// route is a permission checked by handlers.RequirePermission before the
	// handler runs — proven below.
	route perm.Perm
	// handler names what the handler checks itself (documentation; proven
	// in handlers/perm_enforce_test.go).
	handler string
	// exempt is why no permission applies.
	exempt string
}

func gated(p perm.Perm) permRoute      { return permRoute{route: p} }
func inHandler(s string) permRoute     { return permRoute{handler: s} }
func exemptBecause(s string) permRoute { return permRoute{exempt: s} }

const (
	whyOwnState     = "the caller's own bookkeeping (preferences, stars, recents, notification state) — no file and no one else is touched"
	whyWithdraw     = "withdrawing or deleting one's own credential, link, comment or upload only narrows access, and must stay possible after the permission that created it is gone"
	whyAuthorised   = "continues an upload or operation that was authorised when it began"
	whyRead         = "a read sent as POST for its body; the folder grant (≥viewer) governs it"
	whySecurity     = "authentication and two-factor are never withheld by a permission"
	whyDocServer    = "the document server's save callback, authorised by its signed token for an editing session that files.modify opened (onlyoffice/config)"
	whyE2EHandshake = "the E2E key-escrow handshake — proves key possession, writes no file"
)

// permRouteTable classifies every state-changing route under permRoutePrefixes.
// The key is "METHOD pattern" as chi.Walk reports it; "* pattern" covers every
// method of a route mounted with Handle (the MCP endpoint).
var permRouteTable = map[string]permRoute{
	// ── the agent API: ai.use for the whole of /api/ai (the group gate) ──
	"* /api/ai/mcp":              gated(perm.AIUse),
	"POST /api/ai/delete":        gated(perm.AIUse),
	"POST /api/ai/mkdir":         gated(perm.AIUse),
	"POST /api/ai/move":          gated(perm.AIUse),
	"POST /api/ai/share":         gated(perm.AIUse),
	"POST /api/ai/tags":          gated(perm.AIUse),
	"POST /api/ai/unshare":       gated(perm.AIUse),
	"POST /api/ai/unzip":         gated(perm.AIUse),
	"POST /api/ai/upload":        gated(perm.AIUse),
	"POST /api/ai/upload/ticket": gated(perm.AIUse),
	"POST /api/ai/zip":           gated(perm.AIUse),

	// ── own account and credentials ──
	"POST /api/auth/login":                  exemptBecause(whySecurity),
	"POST /api/auth/logout":                 exemptBecause(whySecurity),
	"POST /api/auth/totp/enroll":            exemptBecause(whySecurity),
	"POST /api/auth/totp/verify":            exemptBecause(whySecurity),
	"POST /api/auth/totp/disable":           exemptBecause(whySecurity),
	"POST /api/auth/desktop/exchange":       exemptBecause("the desktop app's PKCE exchange, before any session exists; access.desktop is checked at /complete"),
	"POST /api/auth/desktop/complete":       gated(perm.AccessDesktop),
	"PATCH /api/auth/profile":               gated(perm.AccountEdit),
	"POST /api/auth/password":               gated(perm.AccountEdit),
	"POST /api/auth/s3-keys":                gated(perm.AccessS3),
	"POST /api/auth/s3-keys/{id}/state":     gated(perm.AccessS3),
	"DELETE /api/auth/s3-keys/{id}":         exemptBecause(whyWithdraw),
	"POST /api/auth/ssh-keys":               gated(perm.AccessSFTP),
	"POST /api/auth/ssh-keys/{id}/state":    gated(perm.AccessSFTP),
	"DELETE /api/auth/ssh-keys/{id}":        exemptBecause(whyWithdraw),
	"POST /api/auth/nfs-exports":            gated(perm.AccessNFS),
	"POST /api/auth/nfs-exports/{id}/state": gated(perm.AccessNFS),
	"DELETE /api/auth/nfs-exports/{id}":     exemptBecause(whyWithdraw),
	"POST /api/tokens":                      gated(perm.AccessAPI),
	"PATCH /api/tokens/{id}":                gated(perm.AccessAPI),
	"DELETE /api/tokens/{id}":               exemptBecause(whyWithdraw),
	"PUT /api/me/prefs/":                    exemptBecause(whyOwnState),
	"PATCH /api/notifications/settings":     exemptBecause(whyOwnState),
	"POST /api/notifications/read-all":      exemptBecause(whyOwnState),
	"POST /api/notifications/{id}/read":     exemptBecause(whyOwnState),
	"POST /api/sharex/upload":               inHandler("files.create or files.modify on the file, share.links on its link (via the agent file operations)"),

	// ── the explorer ──
	"POST /api/files/manager":              inHandler("per action: newfolder/newfile/upload files.create (files.modify to replace), rename files.rename, move files.move, delete files.delete"),
	"POST /api/files/upload/init":          inHandler("files.create, or files.modify when the target exists"),
	"PUT /api/files/upload/{id}":           exemptBecause(whyAuthorised),
	"POST /api/files/upload/{id}/commit":   exemptBecause(whyAuthorised),
	"DELETE /api/files/upload/{id}":        exemptBecause(whyWithdraw),
	"POST /api/files/upload/begin":         inHandler("files.create, or files.modify when the target exists"),
	"POST /api/files/upload/finalize":      exemptBecause(whyAuthorised),
	"POST /api/files/upload/abort":         exemptBecause(whyWithdraw),
	"POST /api/files/save-text":            inHandler("files.modify, or files.create for a new file"),
	"POST /api/files/onlyoffice/config":    inHandler("files.modify for edit mode; without it the document opens read-only"),
	"POST /api/files/onlyoffice/callback":  exemptBecause(whyDocServer),
	"POST /api/files/copy":                 inHandler("files.create at the destination"),
	"POST /api/files/move":                 inHandler("files.move at the source and the destination"),
	"POST /api/files/delete":               inHandler("files.delete"),
	"POST /api/files/ops":                  inHandler("per kind: move files.move, delete files.delete, copy files.create at the destination"),
	"POST /api/files/ops/{id}/cancel":      exemptBecause(whyWithdraw),
	"POST /api/files/manager/restore":      inHandler("files.create where the file came from"),
	"POST /api/files/manager/tags/":        gated(perm.FilesTag),
	"POST /api/files/manager/star/":        exemptBecause(whyOwnState),
	"POST /api/files/manager/recent/":      exemptBecause(whyOwnState),
	"PUT /api/files/manager/view-prefs/":   exemptBecause(whyOwnState),
	"POST /api/files/versions/restore":     inHandler("files.modify"),
	"POST /api/files/versions/snapshot":    inHandler("files.modify"),
	"POST /api/files/archive/list":         exemptBecause(whyRead),
	"POST /api/files/archive/extract":      inHandler("files.create at the destination"),
	"POST /api/files/archive/add":          inHandler("files.modify on an existing archive, files.create on a new one"),
	"POST /api/files/archive/download":     inHandler("files.download on every member"),
	"POST /api/files/archive/create":       inHandler("files.create at the destination, files.download on every selected item"),
	"POST /api/files/drafts/":              inHandler("files.create in the folder the draft is meant for (planNewDoc)"),
	"POST /api/files/drafts/{key}/save":    inHandler("files.create on the name it is saved as"),
	"DELETE /api/files/drafts/{key}":       exemptBecause("discards the caller's own unsaved draft (ownDraft) into their trash — no stored or shared file is touched"),
	"POST /api/files/e2e/cleanup":          inHandler("files.purge when versions or trash entries are removed; dropping caches needs none"),
	"POST /api/files/e2e/password-changed": exemptBecause("records a password change the client already wrote through the file API (files.modify there) and tells the owner — writes no file"),
	"POST /api/files/search":               exemptBecause(whyRead),
	"POST /api/files/ws-ticket":            exemptBecause("a realtime subscription ticket; each folder feed is checked for ≥viewer when joined"),
	"POST /api/files/e2e/escrow/challenge": exemptBecause(whyE2EHandshake),
	"POST /api/files/e2e/escrow/used":      exemptBecause(whyE2EHandshake),

	// ── sharing and collaboration ──
	"POST /api/files/share":                                 inHandler("share.links, or share.upload_links for a drop link"),
	"DELETE /api/files/share/{id}":                          exemptBecause(whyWithdraw),
	"POST /api/files/permissions":                           inHandler("share.users (and owner level on the item)"),
	"PATCH /api/files/permissions/{id}":                     inHandler("share.users"),
	"DELETE /api/files/permissions/{id}":                    inHandler("share.users"),
	"PATCH /api/files/permissions/groups/{id}":              inHandler("share.users"),
	"DELETE /api/files/permissions/groups/{id}":             inHandler("share.users"),
	"POST /api/files/permissions/invite":                    inHandler("share.users"),
	"POST /api/files/permissions/share-mail":                inHandler("share.links"),
	"POST /api/files/comments":                              gated(perm.CommentsWrite),
	"DELETE /api/files/comments/{id}":                       exemptBecause(whyWithdraw),
	"POST /api/files/plugins/actions/{plugin}/{action}/run": gated(perm.PluginsRun),
	"POST /api/files/plugins/views/{plugin}/{view}/event":   gated(perm.PluginsRun),
	"POST /api/files/plugins/ui/{plugin}/{view}/call":       gated(perm.PluginsRun),
	"PUT /api/files/plugins/ui/{plugin}/{view}/save":        gated(perm.PluginsRun),
}

// walkStateChanging returns every non-GET route under the covered prefixes,
// collapsing a route registered for many methods (Handle) to "* pattern".
func walkStateChanging(t *testing.T, routes chi.Routes) []string {
	t.Helper()
	byPattern := map[string][]string{}
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return nil
		}
		if strings.HasPrefix(route, "/api/ai/admin") {
			return nil
		}
		for _, p := range permRoutePrefixes {
			if strings.HasPrefix(route, p) || route == strings.TrimSuffix(p, "/") {
				byPattern[route] = append(byPattern[route], method)
				return nil
			}
		}
		return nil
	}))
	var out []string
	for route, methods := range byPattern {
		// Handle registers every method chi knows; a route that answers
		// TRACE is a Handle, not a set of hand-picked verbs.
		if len(methods) > 4 {
			out = append(out, "* "+route)
			continue
		}
		for _, m := range methods {
			out = append(out, m+" "+route)
		}
	}
	sort.Strings(out)
	return out
}

func TestPermRouteTable_EveryRouteIsClassified(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)
	routes, ok := srv.Config.Handler.(chi.Routes)
	require.True(t, ok, "the router is no longer a chi.Routes, and this test walks it")
	walked := walkStateChanging(t, routes)
	require.Greater(t, len(walked), 60, "chi.Walk found %d routes (85 the day this was written); the walk broke", len(walked))

	seen := map[string]bool{}
	for _, k := range walked {
		seen[k] = true
		entry, ok := permRouteTable[k]
		if !assert.True(t, ok, "%s is not in permRouteTable: give it the permission it checks (or a written reason it needs none)", k) {
			continue
		}
		set := 0
		if entry.route != "" {
			set++
			assert.True(t, perm.Known(entry.route), "%s: unknown permission %q", k, entry.route)
		}
		if entry.handler != "" {
			set++
		}
		if entry.exempt != "" {
			set++
		}
		assert.Equal(t, 1, set, "%s must be exactly one of gated / inHandler / exemptBecause", k)
	}
	for k := range permRouteTable {
		assert.True(t, seen[k], "permRouteTable lists %s, which the router no longer has — remove the entry", k)
	}
}

// TestPermRouteTable_GatedRoutesRefuse proves every gated(...) entry: a user
// with exactly that permission denied gets permission_denied naming it.
func TestPermRouteTable_GatedRoutesRefuse(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	ctx := context.Background()
	const email, pw = "perm-route-probe@test.local", "TestUserPass!1"
	testutil.SeedRegularUser(t, store, email, pw)
	u, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	session := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, session, email, pw)
	// /api/ai authenticates by token only.
	token := issueProbeToken(t, store, u.ID, "read,write,delete,mcp")

	var keys []string
	for k := range permRouteTable {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	proven := 0
	for _, k := range keys {
		entry := permRouteTable[k]
		if entry.route == "" {
			continue
		}
		method, pattern, _ := strings.Cut(k, " ")
		if method == "*" {
			method = http.MethodPost
		}
		t.Run(k, func(t *testing.T) {
			require.NoError(t, store.SetUserPermissionOverrides(ctx, u.ID, map[string]string{string(entry.route): model.PermDeny}, nil))
			perm.Invalidate()

			req, err := http.NewRequest(method, srv.URL+concretePath(pattern), strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			client := session
			if strings.HasPrefix(pattern, "/api/ai/") {
				req.Header.Set("X-Filex-Token", token)
				client = http.DefaultClient
			}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s with %s denied: %v", k, entry.route, body)
			assert.Equal(t, "permission_denied", body["error"], k)
			assert.Equal(t, string(entry.route), body["permission"], k)
		})
		proven++
	}
	require.NoError(t, store.SetUserPermissionOverrides(ctx, u.ID, nil, nil))
	require.Greater(t, proven, 20, "only %d gated routes were proven", proven)
}

// TestPermRouteTable_AdminAreaClosedToPlainUsers: a plain user with no admin
// permission is refused at every state-changing /api/admin route — whichever
// half of the admin area it lives in.
func TestPermRouteTable_AdminAreaClosedToPlainUsers(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const email, pw = "perm-admin-probe@test.local", "TestUserPass!1"
	testutil.SeedRegularUser(t, store, email, pw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	session := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, session, email, pw)

	routes := srv.Config.Handler.(chi.Routes)
	var probed int
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/admin/") {
			return nil
		}
		req, err := http.NewRequest(method, srv.URL+concretePath(route), strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := session.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s %s answered a plain user", method, route)
		probed++
		return nil
	}))
	require.Greater(t, probed, 100, "only %d /api/admin routes walked (125 the day this was written); the walk broke", probed)
}
