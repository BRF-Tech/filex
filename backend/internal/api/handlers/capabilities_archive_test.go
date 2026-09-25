package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The explorer's create dialog offers what `capabilities.archive` says. #48
// published the POLICY there — every format the admin allowed — so a server
// without 7-Zip (the slim image, a desktop install) offered 7z, TAR and a
// password, and failed with PROVIDER_UNAVAILABLE once the dialog was filled
// in. It says what this server can MAKE: a plain ZIP always, the rest and
// passwords only with a usable 7-Zip.
func TestCapabilities_ArchiveOffersWhatThisServerCanMake(t *testing.T) {
	_, _, store := testutil.NewTestServer(t)
	archiveOf := func(t *testing.T, engine *archivecli.Service) map[string]any {
		t.Helper()
		h := handlers.NewCapabilities(capability.New(store), store, false)
		h.Archive = engine
		rec := httptest.NewRecorder()
		h.Get(rec, httptest.NewRequest(http.MethodGet, "/api/files/capabilities", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
		archive, ok := out["archive"].(map[string]any)
		require.True(t, ok, "no archive block: %v", out)
		return archive
	}

	t.Run("no 7-Zip: a plain ZIP only, no password", func(t *testing.T) {
		engine := archivecli.New(store, archivecli.Config{SevenZipBin: filepath.Join(t.TempDir(), "no-7zip-here")})
		got := archiveOf(t, engine)
		assert.Equal(t, []any{"zip"}, got["allowed_formats"])
		assert.Equal(t, "zip", got["default_format"], "the policy's default (7z) cannot be made here")
		assert.Equal(t, false, got["encryption"])
	})

	t.Run("a usable 7-Zip: the policy's formats and passwords", func(t *testing.T) {
		bin, err := exec.LookPath("7zz")
		if err != nil {
			t.Skip("7-Zip is not installed")
		}
		engine := archivecli.New(store, archivecli.Config{SevenZipBin: bin})
		if !engine.SevenZipAvailable(context.Background()) {
			t.Skip("the installed 7-Zip is older than filex accepts")
		}
		got := archiveOf(t, engine)
		want := []any{}
		for _, f := range engine.Policy(context.Background()).AllowedFormats {
			want = append(want, f)
		}
		assert.Equal(t, want, got["allowed_formats"])
		assert.Equal(t, true, got["encryption"])
	})
}
