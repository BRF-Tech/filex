package ftpsrv

import (
	"context"
	"encoding/json"
	"testing"

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

// avblSession is a signed-in FTP session over local storages named names, each
// in a folder of its own on the test's disk; it returns the session and that
// disk. AVBL is not a verb the Go FTP client speaks, so the test asks the
// session the way ftpserverlib does.
func avblSession(t *testing.T, names ...string) (*fs, diskfree.Usage) {
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
		Enabled: true, Addr: "127.0.0.1:0", PublicHost: "127.0.0.1",
		CertDir: t.TempDir(), SpoolDir: t.TempDir(),
		Store: store, Auth: res, ACL: acl.New(store),
		Body: filebody.New(store, nil), Quota: quota.New(store),
		Resolver: func(id int64) (storage.Driver, error) { return drivers[id], nil },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	hash, err := authlocal.HashPassword("FtpPass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(ctx, "df@example.com", hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	principal, err := res.Password(ctx, u.Email, "FtpPass!1")
	if err != nil {
		t.Fatalf("password auth: %v", err)
	}
	return newFS(srv, principal), disk
}

// Issue #116, the FTP side: AVBL said 1 TiB for every unlimited account.
func TestAVBL_InAStorageIsItsDisksFreeSpace(t *testing.T) {
	f, disk := avblSession(t, "main")
	got, err := f.GetAvailableSpace("/main")
	if err != nil {
		t.Fatalf("AVBL: %v", err)
	}
	if got <= 0 || uint64(got) > disk.Total {
		t.Errorf("AVBL = %d, the disk is %d bytes with %d free", got, disk.Total, disk.Free)
	}
	if got == 1<<40 {
		t.Error("AVBL is still the made-up 1 TiB")
	}
}

func TestAVBL_AtTheRootCountsADiskOnce(t *testing.T) {
	f, disk := avblSession(t, "main", "docs")
	got, err := f.GetAvailableSpace("/")
	if err != nil {
		t.Fatalf("AVBL: %v", err)
	}
	if got <= 0 || uint64(got) > disk.Total {
		t.Errorf("AVBL = %d, want at most the one disk both storages are on (%d)", got, disk.Total)
	}
}

func TestAVBL_AStorageNobodyNamedIsNotFound(t *testing.T) {
	f, _ := avblSession(t, "main")
	if _, err := f.GetAvailableSpace("/no-such-storage"); err == nil {
		t.Error("AVBL answered for a storage that does not exist")
	}
}
