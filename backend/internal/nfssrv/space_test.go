package nfssrv

import (
	"context"
	"encoding/json"
	"testing"

	nfs "github.com/willscott/go-nfs"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/diskfree"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// spaceMount is a mount of export over local storages named names, each in a
// folder of its own on the test's disk. It returns the mount and that disk.
func spaceMount(t *testing.T, export *model.NFSExport, names ...string) (*fs, diskfree.Usage) {
	t.Helper()
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	store := identitystore.New(raw)
	drivers := map[int64]storage.Driver{}
	var disk diskfree.Usage
	for _, name := range names {
		root := t.TempDir()
		cfg, _ := json.Marshal(map[string]any{"path": root})
		st, err := store.CreateStorage(ctx, &model.Storage{Name: name, Driver: "local", Enabled: true, ConfigJSON: cfg})
		if err != nil {
			t.Fatalf("storage: %v", err)
		}
		d := &local.Driver{}
		if err := d.Init(ctx, map[string]any{"path": root}); err != nil {
			t.Fatalf("driver: %v", err)
		}
		drivers[st.ID] = d
		if disk, err = diskfree.Measure(root); err != nil {
			t.Skipf("no disk probe on this platform: %v", err)
		}
	}
	res := protocolauth.New(store, acl.New(store), false)
	srv, err := New(Config{
		Enabled: true, Store: store, Auth: res, ACL: acl.New(store),
		Body: filebody.New(store, nil), Quota: quota.New(store), SpoolDir: t.TempDir(),
		Resolver: func(id int64) (storage.Driver, error) { return drivers[id], nil },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	hash, err := authlocal.HashPassword("NfsPass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(ctx, "df@example.com", hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	principal, err := res.Password(ctx, u.Email, "NfsPass!1")
	if err != nil {
		t.Fatalf("password auth: %v", err)
	}
	return newFS(srv, principal, export), disk
}

// Issue #116, the NFS side: FSSTAT said 32 TiB for every unlimited account.
func TestFSStat_AnExportOfOneStorageIsItsDisk(t *testing.T) {
	f, disk := spaceMount(t, &model.NFSExport{StorageName: "main"}, "main")
	var st nfs.FSStat
	if err := f.fsStat(context.Background(), &st); err != nil {
		t.Fatalf("fsstat: %v", err)
	}
	if st.TotalSize != disk.Total {
		t.Errorf("size = %d, the disk is %d", st.TotalSize, disk.Total)
	}
	if st.AvailableSize == 0 || st.FreeSize < st.AvailableSize {
		t.Errorf("free %d / available %d: clients show a full disk", st.FreeSize, st.AvailableSize)
	}
}

func TestFSStat_AnExportOfTheStorageListCountsADiskOnce(t *testing.T) {
	f, disk := spaceMount(t, &model.NFSExport{}, "main", "docs")
	var st nfs.FSStat
	if err := f.fsStat(context.Background(), &st); err != nil {
		t.Fatalf("fsstat: %v", err)
	}
	if st.TotalSize != disk.Total {
		t.Errorf("size = %d, want the one disk both storages are on (%d)", st.TotalSize, disk.Total)
	}
}
