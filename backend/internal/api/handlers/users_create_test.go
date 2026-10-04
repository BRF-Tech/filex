package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Add user makes a LOCAL account (people of a directory arrive by sign-in
// and sync): its username, an invitation instead of a typed password, and
// no account for an address a directory owns unless asked.
func TestUsers_CreateLocalAccount(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	admin, err := store.CreateUser(ctx, "root@local", "x", model.RoleAdmin, "en", "")
	require.NoError(t, err)
	h := handlers.NewUsers(store)
	h.DirectoryFor = func(email string) (string, string, bool) {
		if strings.HasSuffix(email, "@partner.example") {
			return "ldap-partner", "Partner LDAP", true
		}
		return "", "", false
	}
	create := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.Create(rec, req.WithContext(auth.WithUser(req.Context(), admin)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// A username of one's choosing.
	status, out := create(`{"email":"jane.doe@corp.example","display_name":"Jane Doe","role":"user","password":"s3cret-pass-1","username":"Jane"}`)
	require.Equal(t, http.StatusOK, status, out)
	assert.Equal(t, "jane", out["username"])
	assert.Nil(t, out["invite"], "not invited: no invite in the answer")
	status, out = create(`{"email":"john@corp.example","role":"user","password":"s3cret-pass-1","username":"jane"}`)
	assert.Equal(t, http.StatusConflict, status, "the username is taken")
	assert.Equal(t, "username_taken", out["error"])

	// Invited: a first password is made; with no mailer it comes back once.
	status, out = create(`{"email":"invited@corp.example","role":"viewer","send_invite":true,"password":"ignored"}`)
	require.Equal(t, http.StatusOK, status, out)
	invite := out["invite"].(map[string]any)
	assert.Equal(t, false, invite["emailed"])
	pw, _ := invite["temp_password"].(string)
	require.Len(t, pw, 16)
	u, err := store.GetUserByEmail(ctx, "invited@corp.example")
	require.NoError(t, err)
	assert.NotEmpty(t, u.PasswordHash, "the invited account has its first password")

	// An address a directory owns: refused, naming the directory…
	status, out = create(`{"email":"pat@partner.example","role":"user","password":"s3cret-pass-1"}`)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "directory_email", out["error"])
	assert.Equal(t, "ldap-partner", out["directory"])
	assert.Equal(t, "Partner LDAP", out["label"])
	assert.Contains(t, out["message"], "Partner LDAP")
	_, err = store.GetUserByEmail(ctx, "pat@partner.example")
	assert.Error(t, err, "nothing was made")
	// …unless the administrator says to make it anyway.
	status, out = create(`{"email":"pat@partner.example","role":"user","password":"s3cret-pass-1","allow_directory_email":true}`)
	require.Equal(t, http.StatusOK, status, out)
	assert.Equal(t, model.AuthSourceLocal, out["auth_source"])
}
