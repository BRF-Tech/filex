package handlers_test

// Tags through the agent surface (GET/POST /api/ai/tags; the MCP file_tags
// tool runs the same aiOps.Tags). An agent must name the kind of every tag it
// writes, sees only what its user sees, and a viewer's token cannot change a
// team tag.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

type aiTagsAnswer struct {
	Path        string    `json:"path"`
	Tags        []tagWire `json:"tags"`
	CanEditTeam bool      `json:"can_edit_team"`
	Error       string    `json:"error"`
}

func aiTags(t *testing.T, client *http.Client, base, tok, method string, body any) (int, aiTagsAnswer) {
	t.Helper()
	u := base + "/api/ai/tags"
	if method == http.MethodGet {
		u += "?path=main://teklif.txt"
	}
	resp := aiReq(t, client, method, u, tok, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out aiTagsAnswer
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return resp.StatusCode, out
}

func TestAITags_KindsThroughTheAgentSurface(t *testing.T) {
	srv, client, store, adminTok := aiFixture(t)
	aiSeedFiles(t, srv.URL, client, adminTok, "teklif.txt")
	ctx := context.Background()
	mkTok := func(email, role string) string {
		u, err := store.CreateUser(ctx, email, "x", role, "tr", "UTC")
		require.NoError(t, err)
		return issueToken(t, store, u.ID, fullScopes, nil)
	}
	userTok := mkTok("ayse@test.local", model.RoleUser)
	viewerTok := mkTok("izleyici@test.local", model.RoleViewer)

	code, out := aiTags(t, client, srv.URL, adminTok, http.MethodPost, map[string]any{
		"path": "main://teklif.txt",
		"tags": []map[string]string{{"name": "Müşteri Teklifi", "kind": "team"}, {"name": "Taslak", "kind": "personal"}},
	})
	require.Equal(t, http.StatusOK, code, out.Error)
	require.ElementsMatch(t, []tagWire{{"Müşteri Teklifi", "team"}, {"Taslak", "personal"}}, out.Tags)
	require.True(t, out.CanEditTeam)
	require.Equal(t, "main://teklif.txt", out.Path)

	// Another user's agent: the team tag, not the admin's personal one.
	code, out = aiTags(t, client, srv.URL, userTok, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code, out.Error)
	require.Equal(t, []tagWire{{"Müşteri Teklifi", "team"}}, out.Tags)

	// No default kind on this surface.
	code, out = aiTags(t, client, srv.URL, userTok, http.MethodPost, map[string]any{
		"path": "main://teklif.txt", "tags": []map[string]string{{"name": "Kind yok"}},
	})
	require.Equal(t, http.StatusBadRequest, code, out.Error)

	// A viewer's agent reads team tags but cannot remove them…
	code, out = aiTags(t, client, srv.URL, viewerTok, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code, out.Error)
	require.False(t, out.CanEditTeam)
	code, out = aiTags(t, client, srv.URL, viewerTok, http.MethodPost, map[string]any{
		"path": "main://teklif.txt", "tags": []map[string]string{},
	})
	require.Equal(t, http.StatusForbidden, code, out.Error)
	// …and may keep a personal one of its own.
	code, out = aiTags(t, client, srv.URL, viewerTok, http.MethodPost, map[string]any{
		"path": "main://teklif.txt",
		"tags": []map[string]string{{"name": "müşteri teklifi", "kind": "team"}, {"name": "Okunacak", "kind": "personal"}},
	})
	require.Equal(t, http.StatusOK, code, out.Error)
	require.ElementsMatch(t, []tagWire{{"Müşteri Teklifi", "team"}, {"Okunacak", "personal"}}, out.Tags,
		"a differently-cased team name is the SAME tag — no change, so no 403")

	// The admin's view is untouched by either agent.
	code, out = aiTags(t, client, srv.URL, adminTok, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code, out.Error)
	require.ElementsMatch(t, []tagWire{{"Müşteri Teklifi", "team"}, {"Taslak", "personal"}}, out.Tags)
}
