package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A signed thumbnail URL carries the tenant its storage was in when the
// listing signed it, and stops when the storage goes to another tenant.
func TestTenantMoved_ASignedThumbnailStops(t *testing.T) {
	srv, client, store, cacheDir := newThumbServer(t, true)
	ctx := context.Background()
	mine, _, _ := seedTenant(t, store, "globex", "admin@globex.test", false)
	theirs, _, _ := seedTenant(t, store, "initech", "admin@initech.test", false)
	st, _, _, jpeg := seedThumbNode(t, store, cacheDir, "globex")
	require.NoError(t, store.LinkProviderStorage(ctx, mine, st.ID))
	seedUserIn(t, store, mine, "user@globex.test")
	testutil.LoginAs(t, srv, client, "user@globex.test", xtUserPass)

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/manager?action=index&path="+st.Name+"://", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	var thumbURL string
	files, _ := body["files"].([]any)
	for _, raw := range files {
		row, _ := raw.(map[string]any)
		if u, _ := row["thumb_url"].(string); u != "" {
			thumbURL = u
		}
	}
	require.Contains(t, thumbURL, "ten=", "the stamp names the storage's tenant")

	code, got, _ := getRaw(t, srv.URL+thumbURL, nil)
	require.Equal(t, http.StatusOK, code, "control")
	require.Equal(t, jpeg, got)

	require.NoError(t, store.UnlinkProviderStorage(ctx, mine, st.ID))
	require.NoError(t, store.LinkProviderStorage(ctx, theirs, st.ID))
	code, got, _ = getRaw(t, srv.URL+thumbURL, nil)
	assert.Equal(t, http.StatusNotFound, code, "a preview of what is initech's storage now")
	assert.NotEqual(t, jpeg, got)

	// A stamp cannot be stripped of its tenant: the signature covers it.
	stripped := strings.Replace(thumbURL, "&ten=", "&x=", 1)
	code, _, _ = getRaw(t, srv.URL+stripped, nil)
	assert.NotEqual(t, http.StatusOK, code)
}
