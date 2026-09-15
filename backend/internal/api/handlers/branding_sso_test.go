package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Issue #28: "Add option to set custom name for OIDC login button". The label
// is a branding setting, so it rides /api/branding (the pre-session appearance
// fetch the sign-in page already makes) and can differ per tenant.
func TestBranding_SSOLabelIsServedAndValidated(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewBrandingSource(store, false)
	bh := handlers.NewBranding(src)
	seth := handlers.NewSettings(store)
	seth.AttachBranding(src)

	get := func() map[string]any {
		rec := httptest.NewRecorder()
		bh.Get(rec, httptest.NewRequest("GET", "/api/branding", nil))
		require.Equal(t, 200, rec.Code)
		var m map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
		return m
	}
	patch := func(body string) int {
		rec := httptest.NewRecorder()
		seth.Update(rec, httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(body)).WithContext(ctx))
		return rec.Code
	}

	label, ok := get()["sso_label"]
	require.True(t, ok, "sso_label is always present")
	require.Equal(t, "", label, "empty means the translated default")

	require.Equal(t, http.StatusOK, patch(`{"branding.sso_label":"Şirket hesabıyla giriş"}`))
	require.Equal(t, "Şirket hesabıyla giriş", get()["sso_label"], "the write is visible at once (settings invalidates the branding cache)")

	require.Equal(t, http.StatusBadRequest, patch(`{"branding.sso_label":"`+strings.Repeat("ç", 61)+`"}`), "a button label is capped at 60 characters")
	require.Equal(t, http.StatusOK, patch(`{"branding.sso_label":"`+strings.Repeat("ç", 60)+`"}`))
}

// Issue #29, public share pages: an accent-filled button gets a label colour
// picked from the accent, and an edge per theme when the fill would disappear
// into that theme's card.
func TestBranding_SharePageAccentButtonReadableInBothThemes(t *testing.T) {
	ctx := context.Background()
	sharH, src, store, token := brandingShareFixture(t)

	render := func(accent string) string {
		require.NoError(t, store.UpsertSetting(ctx, "branding.accent", accent))
		src.Invalidate()
		rec := getSharePage(t, sharH, token)
		require.Equal(t, 200, rec.Code)
		return rec.Body.String()
	}

	white := render("#ffffff")
	require.Contains(t, white, "--px-on-accent:#15171c", "a white fill gets a dark label")
	require.Contains(t, white, "--px-accent-edge:rgba(21,23,28,0.35)}", "on the light card a white fill needs an edge")
	require.Contains(t, white, "@media (prefers-color-scheme: dark){:root{--px-accent-edge:#ffffff}}", "on the dark card it stands out on its own")

	black := render("#000000")
	require.Contains(t, black, "--px-on-accent:#ffffff", "a black fill gets a white label")
	require.Contains(t, black, "--px-accent-edge:#000000}", "on the light card a black fill stands out on its own")
	require.Contains(t, black, "@media (prefers-color-scheme: dark){:root{--px-accent-edge:rgba(255,255,255,0.45)}}", "on the dark card it needs an edge")

	require.Contains(t, black, "color: var(--px-on-accent, #fff); box-shadow: inset 0 0 0 1px var(--px-accent-edge, transparent)", "the button reads both properties")
}
