package handlers_test

/* PATCH /api/auth/profile and the profile picture: what it stores, what it
   refuses, and what it leaves alone. */

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func patchProfile(t *testing.T, h *handlers.AuthSelf, store db.Store, u *model.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/auth/profile", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.UpdateProfile(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	return rec
}

func TestProfileAvatar(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	u, err := store.CreateUser(ctx, "burak@brf.sh", "x", model.RoleAdmin, "tr", "Europe/Istanbul")
	require.NoError(t, err)
	h := handlers.NewAuthSelf(store)

	// Stored, and handed straight back so the SPA can render it.
	rec := patchProfile(t, h, store, u, `{"avatar_url":"`+testAvatarURI+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out model.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, testAvatarURI, out.AvatarURL)

	saved, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, testAvatarURI, saved.AvatarURL)

	// A patch that does not mention the picture must not wipe it — the profile
	// form sends whatever it currently shows, and other callers send neither.
	rec = patchProfile(t, h, store, saved, `{"display_name":"Burak"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	saved, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, testAvatarURI, saved.AvatarURL)
	require.Equal(t, "Burak", saved.DisplayName)

	// Anything that is not an image reference is refused OUT LOUD: the user is
	// looking at an upload they believe worked.
	for _, bad := range []string{
		`{"avatar_url":"javascript:alert(1)"}`,
		`{"avatar_url":"data:text/html;base64,PHNjcmlwdD4="}`,
		`{"avatar_url":"data:image/png;base64,` + strings.Repeat("A", 60*1024) + `"}`,
	} {
		rec = patchProfile(t, h, store, saved, bad)
		require.Equal(t, http.StatusBadRequest, rec.Code, bad)
	}
	saved, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, testAvatarURI, saved.AvatarURL, "a refused patch must not have touched the stored picture")

	// An explicit empty string is how the picture is removed.
	rec = patchProfile(t, h, store, saved, `{"avatar_url":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	saved, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, saved.AvatarURL)
}
