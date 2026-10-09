package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// sec055 S12: a storage plugin under a store's license is held BEFORE any
// plugin starts. The plugins used to be loaded before the app store existed,
// so a lapsed license ran its plugin (and opened its storages) after every
// restart until the store's ApplyHolds came round - and with the app store
// off it never came.

// With the app store off, a plugin a store's license names starts held; one
// installed without a store starts as ever.
func TestStartStoragePlugins_AStoreLicensedPluginStartsHeldWithTheStoreOff(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	paid, err := store.CreatePlugin(ctx, &model.Plugin{Name: "paidfs", Kind: model.PluginKindRemote, Address: "http://127.0.0.1:9", TokenSealed: "x", Enabled: true})
	require.NoError(t, err)
	free, err := store.CreatePlugin(ctx, &model.Plugin{Name: "freefs", Kind: model.PluginKindRemote, Address: "http://127.0.0.1:9", TokenSealed: "x", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, store.PutAppStoreState(ctx, "license:"+appstore.StoragePrefix+"paidfs",
		`{"app":"storage:paidfs","store_app":"paidfs","store":"https://apps.example","status":"valid"}`))

	m, err := plugin.New(plugin.Options{Store: store, Dir: t.TempDir(), SecretKey: "test-secret-key"})
	require.NoError(t, err)
	t.Cleanup(m.Shutdown)
	startStoragePlugins(ctx, m, store, nil)

	st, err := m.Get(ctx, paid.ID)
	require.NoError(t, err)
	assert.Equal(t, plugin.StateHeld, st.State, "a plugin under a store's license ran with nobody to check the license")
	assert.Equal(t, appstore.HoldReasonStoreOff, st.StateError)

	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err = m.Get(ctx, free.ID)
		require.NoError(t, err)
		if st.State != plugin.StateStarting || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.NotEqual(t, plugin.StateHeld, st.State, "a plugin no store licenses is not held")
}

// The holds come first: server.go starts the storage plugins only after the
// app store has judged every license (appStore.Start → ApplyHolds), never
// before - read from the source, because the wiring is one long function.
func TestServer_StoragePluginsStartAfterTheLicenseHolds(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(".", "server.go"))
	require.NoError(t, err)
	src := string(b)
	assert.NotContains(t, src, "pluginMgr.Load(", "the plugins are loaded by startStoragePlugins, after the holds")
	start := strings.Index(src, "appStore.Start(ctx)")
	plugins := strings.Index(src, "startStoragePlugins(ctx, pluginMgr, store, appStore)")
	require.Positive(t, start, "appStore.Start is where the holds are applied")
	require.Positive(t, plugins, "the storage plugins are started by startStoragePlugins")
	assert.Greater(t, plugins, start, "the storage plugins start before the license holds are in place")
	prewarm := strings.Index(src, "// Pre-warm storages")
	require.Positive(t, prewarm)
	assert.Less(t, plugins, prewarm, "the storage plugins must be up before storages are pre-warmed")
}
