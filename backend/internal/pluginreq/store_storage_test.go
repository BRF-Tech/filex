package pluginreq

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// A person's request for a STORAGE plugin of a store's catalog (#215): a
// storage request, frozen at the build this server would install, an upgrade
// when a plugin of that name is here, and refused where storage plugins are
// off.

func storageEntry() StoreEntry {
	plat := runtime.GOOS + "/" + runtime.GOARCH
	return StoreEntry{Store: "https://apps.filex.sh", App: "myfs", Kind: KindStorageEntry, Version: "1.3.0",
		Label: map[string]string{"en": "My FS"}, Publisher: "Acme", Repo: "acme/filex-myfs",
		ManifestSHA256: strings.Repeat("c", 64), Builds: map[string]string{plat: strings.Repeat("1", 64)}}
}

func TestCreateStore_AStoragePluginIsAStorageRequestAtThisServersBuild(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	m, err := plugin.New(plugin.Options{Store: store, Dir: filepath.Join(t.TempDir(), "plugins"), SecretKey: "test-secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	s := New(Options{Store: store, Plugins: m})
	// The requester is a real account: plugin_requests.requested_by
	// references users(id), and an id nobody has fails the insert.
	uid := dbtest.SeedUserWithRole(t, store, "ayse@test.local", "AysePass!1", model.RoleUser)
	who := Actor{UserID: &uid, Name: "ayse"}

	r, created, err := s.CreateStore(ctx, storageEntry(), "For the archive", who)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if r.Kind != model.PluginRequestKindStorage || r.Op != model.PluginRequestOpInstall || r.SourceKind != SourceStore || r.Name != "myfs" {
		t.Fatalf("request %+v", r)
	}
	if r.SHA256 != strings.Repeat("1", 64) {
		t.Fatalf("frozen at %q, want this platform's build", r.SHA256)
	}
	// Asked again: the request already waiting.
	if again, created, err := s.CreateStore(ctx, storageEntry(), "again", who); err != nil || created || again.ID != r.ID {
		t.Fatalf("again: %v %v %+v", created, err, again)
	}

	// A plugin of that name here, at another version: an upgrade.
	if _, err := store.CreatePlugin(ctx, &model.Plugin{Name: "otherfs", Kind: model.PluginKindBinary, Binary: "otherfs",
		SHA256: strings.Repeat("9", 64), Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	e := storageEntry()
	e.App = "otherfs"
	up, _, err := s.CreateStore(ctx, e, "Newer, please", who)
	if err != nil || up.Op != model.PluginRequestOpUpgrade || up.FromVersion != "1.0.0" || up.PluginID == nil {
		t.Fatalf("upgrade: %+v %v", up, err)
	}
	// The same version: installed already.
	e.Version = "1.0.0"
	if _, _, err := s.CreateStore(ctx, e, "x", who); err == nil {
		t.Fatal("the version installed was asked for again")
	} else {
		var pe *Error
		if !errors.As(err, &pe) || pe.Code != "already_installed" {
			t.Fatalf("want already_installed, got %v", err)
		}
	}
}

func TestCreateStore_StoragePluginsOffRefusesAStorageRequest(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	s := New(Options{Store: store})
	uid := int64(7)
	_, _, err := s.CreateStore(context.Background(), storageEntry(), "For the archive", Actor{UserID: &uid})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "plugins_disabled" {
		t.Fatalf("want plugins_disabled, got %v", err)
	}
}
