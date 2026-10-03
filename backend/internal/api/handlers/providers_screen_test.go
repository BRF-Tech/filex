package handlers_test

// The tenant screen and its MCP tools (docs/TENANT-ADMIN.md): one handler
// (handlers/providers.go) behind three doors - /api/admin/providers (the
// screen), /api/ai/admin/providers and the admin_tenants_* tools - with the
// same guards on each.
//
// RED PROOF (before this change): GET /api/admin/providers/{id} and
// /realm-suggestion answered 404/405, tools/list had no admin_tenants_*, a
// second tenant could be created on another tenant's host (providers.host has
// no unique index; sign-ins and links of one tenant then landed in the other),
// and a tool call on a demo would have written, because the in-process invoke
// never passes api.DemoGuard.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// mcpToolCall calls one tool and answers whether it was an error, the structured
// result ({status, result}) and the text content.
func mcpToolCall(t *testing.T, base, tok, name string, args any) (bool, map[string]any, string) {
	t.Helper()
	a, err := json.Marshal(args)
	require.NoError(t, err)
	code, body := mcpPost(t, &http.Client{}, base+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(a)+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	require.True(t, start >= 0 && end > start, body)
	var rpc struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body[start:end+1]), &rpc), body)
	require.Nil(t, rpc.Error, "%s: a JSON-RPC error, not a tool result: %s", name, body)
	text := ""
	for _, c := range rpc.Result.Content {
		text += c.Text
	}
	return rpc.Result.IsError, rpc.Result.StructuredContent, text
}

// toolResult is the handler's JSON body inside a tool's structured result.
func toolResult(t *testing.T, sc map[string]any) map[string]any {
	t.Helper()
	r, ok := sc["result"].(map[string]any)
	require.True(t, ok, "structured result has no object body: %v", sc)
	return r
}

func TestProviders_RealmSuggestion(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	suggest := func(slug string) map[string]any {
		t.Helper()
		code, out := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/providers/realm-suggestion?slug="+url.QueryEscape(slug), nil)
		require.Equal(t, http.StatusOK, code, out)
		return out
	}
	out := suggest("Müşteri Hizmetleri")
	assert.Equal(t, "musteri-hizmetleri", out["realm"])
	assert.Equal(t, true, out["available"])

	// Taken: the next free variant, and the base is said apart.
	code, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/providers", map[string]any{"slug": "acme-corp"})
	require.Equal(t, http.StatusCreated, code, created)
	out = suggest("Acme Corp")
	assert.Equal(t, "acme-corp-2", out["realm"])
	assert.Equal(t, "acme-corp", out["base"])

	// Reserved: never offered.
	out = suggest("Admin")
	assert.Equal(t, "admin-2", out["realm"])

	// Nothing realm-shaped: no suggestion, said so.
	out = suggest("___")
	assert.Equal(t, "", out["realm"])
	assert.Equal(t, false, out["available"])
}

func TestProviders_GetSaysTheSecretIsSetAndNeverSendsIt(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	const secret = "s3cret-value-never-sent"
	code, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/providers", map[string]any{
		"slug": "acme", "oidc_issuer": "https://id.acme.example", "oidc_client_id": "filex", "oidc_client_secret": secret,
	})
	require.Equal(t, http.StatusCreated, code, created)
	id := itoa(int64(created["id"].(float64)))

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/providers/"+id, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.NotContains(t, string(raw), secret)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, true, got["oidc_client_secret_set"])
	assert.Equal(t, "acme", got["realm"])

	code, out := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/providers/99999", nil)
	assert.Equal(t, http.StatusNotFound, code, out)
}

// TestProviders_SlugAndHostAreEachOneTenants: providers.host has no unique
// index and GetProviderByHost answers one row, so two tenants on one address
// would trade sign-ins, cookies and minted links.
func TestProviders_SlugAndHostAreEachOneTenants(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	post := func(body map[string]any) (int, map[string]any) {
		return doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/providers", body)
	}

	code, acme := post(map[string]any{"slug": "acme", "host": "files.acme.example"})
	require.Equal(t, http.StatusCreated, code, acme)
	acmeID := itoa(int64(acme["id"].(float64)))

	code, out := post(map[string]any{"slug": "acme", "realm": "acme-two"})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "slug_taken", out["error"])
	assert.Equal(t, "slug", out["field"])

	code, out = post(map[string]any{"slug": "beta", "host": "Files.ACME.example"})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "host_taken", out["error"])
	assert.Equal(t, "host", out["field"])

	// A suspended tenant keeps its address: resuming it must not clash.
	code, out = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/providers/"+acmeID, map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, code, out)
	code, out = post(map[string]any{"slug": "beta", "host": "files.acme.example"})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "host_taken", out["error"])

	code, beta := post(map[string]any{"slug": "beta", "host": "files.beta.example"})
	require.Equal(t, http.StatusCreated, code, beta)
	betaID := itoa(int64(beta["id"].(float64)))
	code, out = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/providers/"+betaID, map[string]any{"host": "files.acme.example"})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "host_taken", out["error"])

	// Keeping one's own slug and host is not a clash.
	code, out = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/providers/"+acmeID,
		map[string]any{"slug": "acme", "host": "files.acme.example", "name": "Acme"})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "Acme", out["name"])
}

func TestAIAdmin_TenantTools(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	ctx := context.Background()
	tok := issueToken(t, store, uid, "mcp,admin", nil)

	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	for _, name := range []string{
		"admin_tenants_list", "admin_tenants_get", "admin_tenants_suggest_realm", "admin_tenants_create",
		"admin_tenants_update", "admin_tenants_delete", "admin_tenants_link_storage", "admin_tenants_unlink_storage",
	} {
		assert.Contains(t, body, `"`+name+`"`)
	}

	isErr, sc, text := mcpToolCall(t, srv.URL, tok, "admin_tenants_suggest_realm", map[string]any{"slug": "Çağrı Merkezi"})
	require.False(t, isErr, text)
	assert.Equal(t, "cagri-merkezi", toolResult(t, sc)["realm"])

	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_create", map[string]any{"slug": "acme", "name": "Acme", "host": "files.acme.example"})
	require.False(t, isErr, text)
	assert.Equal(t, float64(http.StatusCreated), sc["status"])
	created := toolResult(t, sc)
	assert.Equal(t, "acme", created["realm"], "the realm defaults to the slug")
	id := int64(created["id"].(float64))

	// A realm is never changed: the tool has no field for it, and the handler
	// would refuse one anyway (realm_immutable, providers_realm_test.go).
	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_update", map[string]any{"id": id, "name": "Acme Inc", "enabled": false})
	require.False(t, isErr, text)
	updated := toolResult(t, sc)
	assert.Equal(t, "Acme Inc", updated["name"])
	assert.Equal(t, false, updated["enabled"])
	assert.Equal(t, "acme", updated["realm"])

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "acme-files", Driver: "local", MountPath: "/acme", ConfigJSON: json.RawMessage(`{"root":"/tmp/acme"}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_link_storage", map[string]any{"id": id, "storage_id": st.ID})
	require.False(t, isErr, text)
	assert.Equal(t, []any{float64(st.ID)}, toolResult(t, sc)["storage_ids"])
	ids, err := store.ListProviderStorageIDs(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []int64{st.ID}, ids)

	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_get", map[string]any{"id": id})
	require.False(t, isErr, text)
	assert.Equal(t, "Acme Inc", toolResult(t, sc)["name"])

	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_list", map[string]any{})
	require.False(t, isErr, text)
	list := toolResult(t, sc)["providers"].([]any)
	assert.Len(t, list, 2, "the platform's own tenant and acme")

	isErr, _, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_unlink_storage", map[string]any{"id": id, "storage_id": st.ID})
	require.False(t, isErr, text)
	ids, err = store.ListProviderStorageIDs(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, ids)
	_, err = store.GetStorage(ctx, st.ID)
	assert.NoError(t, err, "unlinking never deletes the storage")

	// The platform's own tenant is never deleted.
	plat, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	isErr, _, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_delete", map[string]any{"id": plat.ID})
	assert.True(t, isErr)
	assert.Contains(t, text, "cannot delete the supertenant")

	isErr, sc, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_delete", map[string]any{"id": id})
	require.False(t, isErr, text)
	assert.Equal(t, true, toolResult(t, sc)["ok"])
	gone, err := store.GetProvider(ctx, id)
	assert.True(t, err != nil || gone == nil, "the tenant is deleted")

	// Every write is in the audit log, under the AI marker, with what it named.
	actions := auditActions(t, store)
	for _, want := range []string{"ai.providers.create", "ai.providers.update", "ai.providers.storage_link", "ai.providers.storage_unlink", "ai.providers.delete"} {
		assert.Contains(t, actions, want)
	}
}

func auditActions(t *testing.T, store db.Store) []string {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 200)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		out = append(out, r.Action)
	}
	return out
}

// TestAIAdmin_TenantTools_TenantAdminIsRefused: an admin-scoped token of a
// TENANT's administrator reaches the admin tools, and the tenant lifecycle is
// still the platform operator's - the handler's own gate, since the tool never
// passes a route middleware.
func TestAIAdmin_TenantTools_TenantAdminIsRefused(t *testing.T) {
	srv, _, store := multiTenantServer(t)
	ctx := context.Background()
	_, email, _ := seedTenant(t, store, "globex", "admin@globex.test", false)
	u, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	tok := issueToken(t, store, u.ID, "mcp,admin", nil)
	before, err := store.ListProviders(ctx)
	require.NoError(t, err)

	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"admin_tenants_list", map[string]any{}},
		{"admin_tenants_suggest_realm", map[string]any{"slug": "x"}},
		{"admin_tenants_create", map[string]any{"slug": "takeover"}},
		{"admin_tenants_update", map[string]any{"id": 1, "host": "files.attacker.example"}},
	} {
		isErr, _, text := mcpToolCall(t, srv.URL, tok, call.name, call.args)
		assert.True(t, isErr, "%s answered a tenant's administrator: %s", call.name, text)
		assert.Contains(t, text, "supertenant_only", call.name)
	}
	after, err := store.ListProviders(ctx)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "no tenant was created")
	plat, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	assert.Empty(t, plat.Host, "the platform's tenant was not repointed")
}

// TestAIAdmin_TenantTools_DemoWritesNothing: on a public demo a tool's write
// is refused although the in-process call never passes api.DemoGuard (the
// admin tools answer the guard's DemoRefusal themselves, AIAdmin.invoke, and
// the handler would refuse with demo_read_only behind it), and reading still
// works.
func TestAIAdmin_TenantTools_DemoWritesNothing(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Demo.Mode = true
	})
	ctx := context.Background()
	uid, _ := testutil.SeedAdminUser(t, store)
	tok := issueToken(t, store, uid, "mcp,admin", nil)

	isErr, _, text := mcpToolCall(t, srv.URL, tok, "admin_tenants_create", map[string]any{"slug": "demo-tenant"})
	assert.True(t, isErr, text)
	assert.True(t, strings.Contains(text, `"demo":"read-only"`) || strings.Contains(text, "demo_read_only"),
		"a demo's refusal, not some other error: %s", text)
	p, err := store.GetProviderBySlug(ctx, "demo-tenant")
	assert.True(t, err != nil || p == nil, "nothing was created on the demo")

	isErr, _, text = mcpToolCall(t, srv.URL, tok, "admin_tenants_list", map[string]any{})
	assert.False(t, isErr, text)
}
