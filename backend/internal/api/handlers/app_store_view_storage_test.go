package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// sec055 S15: the store screen's Storage tab tells a person who is not the
// server's administrator whether a storage plugin is here - and nothing
// more. Which version is installed and what the server runs on are the
// administrator's: a storage plugin is the whole server's (in a multi-tenant
// filex, every tenant's), and an old, known-vulnerable version or the
// platform is no other person's business.
func TestStoreScreen_AStoragePluginsVersionAndTheServersPlatformAreTheAdministrators(t *testing.T) {
	sf := newStorageFix(t, true)
	sf.trust(t)
	sf.storageIndex(t)
	_, err := sf.store.CreatePlugin(context.Background(), &model.Plugin{Name: "myfs", Kind: model.PluginKindRemote,
		Address: "http://127.0.0.1:9", Version: "0.9.0"})
	require.NoError(t, err)
	p := sf.person(t)
	here := runtime.GOOS + "/" + runtime.GOARCH

	type row struct {
		Name             string         `json:"name"`
		State            string         `json:"state"`
		InstalledVersion *string        `json:"installed_version"`
		Storage          map[string]any `json:"storage"`
	}
	read := func(f *screenFix) map[string]row {
		t.Helper()
		code, body := f.as(t, http.MethodGet, "/api/app-store/catalog?store="+sf.st.Origin(), nil)
		require.Equal(t, http.StatusOK, code, "%s", body)
		var c struct {
			Apps []row `json:"apps"`
		}
		require.NoError(t, json.Unmarshal(body, &c))
		out := map[string]row{}
		for _, a := range c.Apps {
			out[a.Name] = a
		}
		return out
	}

	person := read(p)
	require.Contains(t, person, "myfs")
	assert.Equal(t, "installed", person["myfs"].State, "a person hears that it is here")
	assert.Nil(t, person["myfs"].InstalledVersion, "a person was told which version of a storage plugin is installed")
	require.NotNil(t, person["myfs"].Storage)
	_, hasPlatform := person["myfs"].Storage["platform"]
	assert.False(t, hasPlatform, "a person was told the server's platform")
	require.Contains(t, person, "farfs")
	assert.Equal(t, "No build for this server", person["farfs"].Storage["summary"], "the summary names the server's platform")

	admin := read(&screenFix{asFix: sf.asFix, user: sf.admin})
	require.Contains(t, admin, "myfs")
	require.NotNil(t, admin["myfs"].InstalledVersion)
	assert.Equal(t, "0.9.0", *admin["myfs"].InstalledVersion)
	assert.Equal(t, "update", admin["myfs"].State)
	assert.Equal(t, here, admin["myfs"].Storage["platform"])
	assert.Contains(t, admin["farfs"].Storage["summary"], "("+here+")")
}
