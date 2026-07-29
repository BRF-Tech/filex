package handlers_test

// Denials on the AI surface must answer 403, not 500. A confined token reaching
// outside its root — and a user without the required grant — are PERMANENT
// refusals; a 5xx tells the caller "server glitch, retry" and scripts/agents
// then hammer a request that can never succeed.
//
// Regression: until 2026-07-29 resolveStorage returned a plain fmt.Errorf for
// both cases, so aiStatus fell through to mapDriverErr's 500 default. Observed
// live: POST /api/ai/upload with a path outside the token's root → 500.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestAI_OutsideConfinedRoot_Returns403(t *testing.T) {
	srv, client, store, adminTok := aiFixture(t)

	// Seed one file inside the tenant root and one outside it, using the
	// unconfined token.
	for _, p := range []string{"main://tenant/in.txt", "main://outside/secret.txt"} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", adminTok, map[string]any{"path": p, "content": "x"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	u, err := store.CreateUser(context.Background(), "confined-403@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	conf := issueToken(t, store, u.ID, "read,write,delete,root:main://tenant", nil)

	// Control: in-root work still succeeds (bare relative path resolves under
	// the root), so a 403 below means "outside", not "broken token".
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", conf, map[string]any{"path": "ok.txt", "content": "x"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{"upload", "/api/ai/upload", map[string]any{"path": "main://outside/x.txt", "content": "x"}},
		{"delete", "/api/ai/delete", map[string]any{"path": "main://outside/secret.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := aiReq(t, client, "POST", srv.URL+tc.path, conf, tc.body)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusForbidden, resp.StatusCode,
				"%s outside the confined root must be 403 (permanent refusal), not 5xx", tc.name)
		})
	}

	t.Run("list", func(t *testing.T) {
		resp := aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://outside", conf, nil)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode,
			"listing outside the confined root must be 403, not 5xx")
	})
}
