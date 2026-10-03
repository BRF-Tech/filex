package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func auditSince(t *testing.T, store db.Store, before int) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows[before:]
}

// One request, one row - however many AuditMiddlewares it passes through.
//
// ⚠ /api/ai/admin/* sat under two (the /api/ai group's and the /admin route's)
// and every admin write through an API key was written twice, the second row
// empty (measured 2026-10-01).
func TestAuditMiddlewareNestedWritesOneRow(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	r := chi.NewRouter()
	r.Use(auth.AuditMiddleware(store))
	r.Route("/api", func(r chi.Router) {
		r.With(auth.AuditMiddleware(store)).Patch("/admin/settings", func(w http.ResponseWriter, r *http.Request) {
			auth.AddAuditDetail(r.Context(), "changed", "site_name")
			w.WriteHeader(http.StatusOK)
		})
		// A handler renames its row (a sign-in setting through the generic
		// settings API) - through the door rule of the route it came by.
		rename := func(w http.ResponseWriter, r *http.Request) {
			auth.SetAuditAction(r.Context(), "login_security.update", "login_security")
			w.WriteHeader(http.StatusOK)
		}
		r.With(auth.AuditMiddleware(store)).Put("/admin/settings/{key}", rename)
		r.With(auth.AuditMiddleware(store)).Put("/ai/admin/settings/{key}", rename)
		r.With(auth.AuditMiddleware(store)).Put("/ai/admin/other/{key}", func(w http.ResponseWriter, r *http.Request) {
			auth.SetAuditAction(r.Context(), "settings.update", "setting")
			w.WriteHeader(http.StatusOK)
		})
	})
	send := func(method, path string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	send(http.MethodPatch, "/api/admin/settings")
	rows := auditSince(t, store, 0)
	require.Len(t, rows, 1, "the nested middleware steps aside")
	require.Equal(t, "settings.update", rows[0].Action)
	require.Equal(t, "site_name", rows[0].Metadata["changed"], "the handler's detail reaches the row that is written")

	for _, c := range []struct{ path, want, wantType string }{
		{"/api/admin/settings/login.lock_base_seconds", "login_security.update", "login_security"},
		{"/api/ai/admin/settings/login.lock_base_seconds", "login_security.update", "login_security"},
		{"/api/ai/admin/other/x", "ai.settings.update", "setting"},
	} {
		before := len(auditSince(t, store, 0))
		send(http.MethodPut, c.path)
		rows = auditSince(t, store, before)
		require.Len(t, rows, 1, c.path)
		require.Equal(t, c.want, rows[0].Action, c.path)
		require.Equal(t, c.wantType, rows[0].TargetType, c.path)
	}
}
