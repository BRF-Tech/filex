package handlers_test

/* What an account's e-mail and username may be, refused in the reader's
   language — the self-service profile and an administrator adding a user.

   The release-candidate sweep (2026-09-21) found all of these answering
   wrong: "bu-bir-eposta-degil" saved as an address with "Profil kaydedildi"
   (after which logging in by e-mail failed with 401); another account's
   address answered 200 and changed nothing; a username with "ş" came back
   as raw English. */

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

type refusal struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

func readRefusal(t *testing.T, rec *httptest.ResponseRecorder) refusal {
	t.Helper()
	var r refusal
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r), rec.Body.String())
	return r
}

func TestProfile_TheEmailIsCheckedBeforeAnythingIsSaved(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	ayse, err := store.CreateUser(ctx, "ayse@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "gokcil@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	h := handlers.NewAuthSelf(store)

	patch := func(body, lang string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/auth/profile", strings.NewReader(body))
		req.Header.Set("Accept-Language", lang)
		u, err := store.GetUser(ctx, ayse.ID)
		require.NoError(t, err)
		u.Locale = "" // the reader's language comes from the request here
		rec := httptest.NewRecorder()
		h.UpdateProfile(rec, req.WithContext(auth.WithUser(req.Context(), u)))
		return rec
	}
	stored := func() *model.User {
		u, err := store.GetUser(ctx, ayse.ID)
		require.NoError(t, err)
		return u
	}

	// Not an address: refused, in Turkish, and NOTHING saved — not even the
	// display name that travelled with it.
	rec := patch(`{"email":"bu-bir-eposta-degil","display_name":"Ayşe Y."}`, "tr")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	r := readRefusal(t, rec)
	assert.Equal(t, "email_invalid", r.Error)
	assert.Equal(t, "email", r.Field)
	assert.Contains(t, r.Message, "bir e-posta adresi değil")
	assert.Equal(t, "ayse@local", stored().Email)
	assert.NotEqual(t, "Ayşe Y.", stored().DisplayName, "a refused save wrote the display name anyway")

	// Another account's address: refused as taken, not reported as saved.
	rec = patch(`{"email":"GOKCIL@local"}`, "tr")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	r = readRefusal(t, rec)
	assert.Equal(t, "email_taken", r.Error)
	assert.Equal(t, "gokcil@local başka bir hesaba ait.", r.Message)
	assert.Equal(t, "ayse@local", stored().Email)

	// Emptying it is refused too; the English reader gets English.
	rec = patch(`{"email":"  "}`, "en")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "Enter an email address.", readRefusal(t, rec).Message)

	// Its own address again, and a new valid one (a dotless domain like the
	// first administrator's admin@local included): fine.
	rec = patch(`{"email":"ayse@local"}`, "tr")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = patch(`{"email":"Ayse.Yilmaz@Example.com"}`, "tr")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ayse.yilmaz@example.com", stored().Email)
}

func TestProfile_AUsernameIsRefusedInTheReadersWords(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	u, err := store.CreateUser(ctx, "ayse@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	h := handlers.NewAuthSelf(store)

	for _, c := range []struct {
		name, lang, want string
	}{
		{"Ayşe Yılmaz!", "tr", "Kullanıcı adında “ş” kullanılamaz."},
		{"ayse yilmaz", "tr", "Kullanıcı adında boşluk olamaz."},
		{"ayşe", "tr", "Kullanıcı adında “ş” kullanılamaz."},
		{"ayşe", "en", "“ş” cannot be used in a username."},
		{"ay", "tr", "en az 3 karakter"},
		{"ayse@local", "tr", "@ olamaz"},
		{"9ayse", "tr", "rakamla başlayamaz"},
		{"root", "tr", "ayrılmış bir ad"},
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api/auth/profile",
			strings.NewReader(`{"username":`+strconvQuote(c.name)+`,"email":"new@local"}`))
		req.Header.Set("Accept-Language", c.lang)
		cur, err := store.GetUser(ctx, u.ID)
		require.NoError(t, err)
		cur.Locale = ""
		rec := httptest.NewRecorder()
		h.UpdateProfile(rec, req.WithContext(auth.WithUser(req.Context(), cur)))
		require.Equal(t, http.StatusBadRequest, rec.Code, "%q: %s", c.name, rec.Body.String())
		r := readRefusal(t, rec)
		assert.Equal(t, "username_invalid", r.Error, c.name)
		assert.Equal(t, "username", r.Field)
		assert.Contains(t, r.Message, c.want, c.name)
		assert.NotContains(t, r.Message, "invalid username", "the English sentinel text reached the person")
		// …and the e-mail that came with it was not saved on the way.
		after, err := store.GetUser(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "ayse@local", after.Email, "a refused username left the e-mail half-saved")
	}
}

func TestUsers_CreateRefusesTheAddressInWords(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	_, err := store.CreateUser(ctx, "gokcil@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	h := handlers.NewUsers(store)
	create := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
		req.Header.Set("Accept-Language", "tr")
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		return rec
	}
	rec := create(`{"email":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "Bir e-posta adresi yazın.", readRefusal(t, rec).Message)

	rec = create(`{"email":"bu-bir-eposta-degil"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "email_invalid", readRefusal(t, rec).Error)

	rec = create(`{"email":"gokcil@local"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "a taken address is a conflict, not the driver's 500")
	assert.Equal(t, "gokcil@local başka bir hesaba ait.", readRefusal(t, rec).Message)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
