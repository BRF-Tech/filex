package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An instance with no tokens answers both token lists with an empty array.
// The store returns a nil slice for no rows, which encoded as
// `"tokens": null`: the Cypress token-kinds spec ("the admin token list"
// must be an array) was red on main for it, and any client mapping over the
// list fails the same way on a fresh install.
func TestTokenLists_NoneIsAnEmptyArray(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	u, err := store.CreateUser(ctx, "ada@example.com", "x", model.RoleAdmin, "tr", "Europe/Istanbul")
	require.NoError(t, err)

	for name, list := range map[string]http.HandlerFunc{
		"GET /api/admin/ai-tokens": handlers.NewAITokens(store, nil).List,
		"GET /api/tokens":          handlers.NewSelfTokens(store, nil, nil).List,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			list(rec, req.WithContext(auth.WithUser(req.Context(), u)))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.JSONEq(t, `{"tokens":[]}`, rec.Body.String())
		})
	}
}
