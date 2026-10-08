package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// One password rule (local.CheckPassword), asked at every door that sets an
// account's password: the person's own change, an administrator adding an
// account and an administrator setting one. Each refuses in the same words,
// in the reader's language, naming the number. Until 0.54 only the first door
// asked, so an administrator could give an account a one-letter password.
func TestPasswordRule_EveryDoorAsksTheSameRule(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	refused := func(what string, status int, out map[string]any) {
		t.Helper()
		assert.Equal(t, http.StatusBadRequest, status, "%s: %v", what, out)
		assert.Equal(t, "password_too_short", out["error"], "%s: %v", what, out)
		assert.Equal(t, "password", out["field"], "%s: %v", what, out)
		assert.Contains(t, out["message"], fmt.Sprint(local.MinPasswordLen), "%s: the sentence names the number: %v", what, out)
	}

	// An administrator adding an account with a short password.
	status, out := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "short@test.local", "password": "kisa", "role": "user",
	})
	refused("create", status, out)
	u, _ := store.GetUserByEmail(ctx, "short@test.local")
	assert.Nil(t, u, "the account was made with a refused password")

	// Characters, not bytes: "şşşş" is four characters in eight bytes.
	status, out = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "bytes@test.local", "password": "şşşş", "role": "user",
	})
	refused("create, eight bytes in four characters", status, out)

	// A password of the length the rule asks for is taken.
	status, out = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "member@test.local", "password": "Uzun-parola-1", "role": "user",
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	member, err := store.GetUserByEmail(ctx, "member@test.local")
	require.NoError(t, err)

	// An administrator setting a short one, on its own or beside another
	// field: refused before anything is written.
	status, out = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d", srv.URL, member.ID), map[string]any{
		"password": "abc", "display_name": "Değişmemeli",
	})
	refused("update", status, out)
	after, err := store.GetUser(ctx, member.ID)
	require.NoError(t, err)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("Uzun-parola-1")), "the password changed")
	assert.NotEqual(t, "Değişmemeli", after.DisplayName, "a field beside the refused password was written")

	// The person's own change.
	mc := freshClient(t)
	testutil.LoginAs(t, srv, mc, "member@test.local", "Uzun-parola-1")
	status, out = doJSON(t, mc, http.MethodPost, srv.URL+"/api/auth/password", map[string]any{
		"current_password": "Uzun-parola-1", "new_password": "1234567",
	})
	refused("own change", status, out)
}
