package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The editor's fetch URL (an hour's life) stops serving the document when its
// storage leaves the tenant the editor was opened in.
func TestTenantMoved_TheEditorsFetchURLStops(t *testing.T) {
	f := newXTFixture(t, false)
	ctx := context.Background()
	status, body := f.do(t, http.MethodPost, "/api/files/onlyoffice/config", map[string]any{
		"node_id": f.mine.node.ID, "mode": "view",
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	cfg, _ := body["config"].(map[string]any)
	document, _ := cfg["document"].(map[string]any)
	fetch, _ := document["url"].(string)
	require.Contains(t, fetch, "ten=", "the fetch URL names the document's tenant")
	query := fetch[strings.Index(fetch, "?")+1:]

	code, got := xtFetchBody(t, f, query)
	require.Equal(t, http.StatusOK, code, "control: %s", got)

	require.NoError(t, f.store.UnlinkProviderStorage(ctx, f.mine.provider, f.mine.storage.ID))
	require.NoError(t, f.store.LinkProviderStorage(ctx, f.theirs.provider, f.mine.storage.ID))
	code, got = xtFetchBody(t, f, query)
	assert.Equal(t, http.StatusNotFound, code, "the document of a storage that is the other tenant's now: %s", got)

	require.NoError(t, f.store.UnlinkProviderStorage(ctx, f.theirs.provider, f.mine.storage.ID))
	require.NoError(t, f.store.LinkProviderStorage(ctx, f.mine.provider, f.mine.storage.ID))
	code, _ = xtFetchBody(t, f, query)
	assert.Equal(t, http.StatusOK, code, "the storage back, the URL answers again")
}
