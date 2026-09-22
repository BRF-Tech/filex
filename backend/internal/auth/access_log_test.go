package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// WithUser and WithToken are the one funnel every authenticating door goes
// through, which is why the access log learns the caller there. A nil user or
// token, or a context without a holder, must be a no-op — they are called
// from background jobs and tests too.
func TestWithUserAndToken_NoteTheCallerForTheAccessLog(t *testing.T) {
	ctx, who := httpx.WithRequestLog(context.Background())
	ctx = WithUser(ctx, &model.User{ID: 7})
	ctx = WithToken(ctx, &model.APIToken{ID: 42})
	if who.UserID() != 7 || who.TokenID() != 42 {
		t.Fatalf("holder has user=%d token=%d, want 7 and 42", who.UserID(), who.TokenID())
	}
	if UserFrom(ctx) == nil || TokenFrom(ctx) == nil {
		t.Fatal("the context lost the caller while noting it")
	}

	// A synthesized principal with no id, and nils, leave it alone.
	WithUser(ctx, &model.User{ID: 0})
	WithUser(ctx, nil)
	WithToken(ctx, nil)
	if who.UserID() != 7 || who.TokenID() != 42 {
		t.Fatalf("holder changed to user=%d token=%d", who.UserID(), who.TokenID())
	}

	// No holder at all: nothing to note, nothing to panic about.
	if u := UserFrom(WithUser(context.Background(), &model.User{ID: 9})); u == nil || u.ID != 9 {
		t.Fatal("WithUser without a holder must still attach the user")
	}
}

// The tenant resolver names the tenant for the access log. It does so itself
// because package tenant is kept stdlib-only.
func TestTenantResolver_NotesTheTenantForTheAccessLog(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	prov, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateUser(ctx, "tenant-log@example.com", "x", model.RoleUser, "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProvider(ctx, created.ID, prov.ID, ""); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, multi := range []bool{true, false} {
		reqCtx, who := httpx.WithRequestLog(WithUser(context.Background(), u))
		req := httptest.NewRequest(http.MethodGet, "/api/files/manager", nil).WithContext(reqCtx)
		TenantResolver(store, multi)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
			ServeHTTP(httptest.NewRecorder(), req)
		want := ""
		if multi {
			want = "acme"
		}
		if who.Tenant() != want {
			t.Errorf("multi-tenant=%v: tenant = %q, want %q", multi, who.Tenant(), want)
		}
	}
}
