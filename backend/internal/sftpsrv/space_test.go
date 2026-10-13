package sftpsrv_test

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/diskfree"
)

// within reports whether got is want to the block: statvfs counts whole
// blocks, and the disk's free figure moves while a test runs.
func within(got, want, slack uint64) bool {
	if got > want {
		return got-want <= slack
	}
	return want-got <= slack
}

// Issue #116: `sftp df -h` on a local storage said 1.0PB with 0B used,
// whatever the disk was. An account with no quota sees the disk the storage is
// on.
func TestStatVFS_AnUnlimitedAccountSeesTheStoragesDisk(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "df@example.com")
	st := hz.storage(t, "main")
	disk, err := diskfree.Measure(hz.rootOf(t, st))
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	cl := hz.mustDial(t, "df@example.com", testPassword)

	vfs, err := cl.StatVFS("/main")
	if err != nil {
		t.Fatalf("statvfs: %v", err)
	}
	total := vfs.Blocks * vfs.Frsize
	if !within(total, disk.Total, vfs.Frsize) {
		t.Errorf("size = %d bytes, the disk is %d", total, disk.Total)
	}
	if used := (vfs.Blocks - vfs.Bfree) * vfs.Frsize; used == 0 {
		t.Error("used = 0 on a disk the test itself is writing to")
	}
	if vfs.Bavail == 0 {
		t.Error("no space available: WinSCP shows a full disk and refuses to transfer")
	}
}

// Mountain Duck and `sftp` start at the root, the folder that lists the
// storages. Two storages on one disk are that disk once, not twice.
func TestStatVFS_TheRootIsTheStoragesAddedUpADiskOnce(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "df@example.com")
	st := hz.storage(t, "main")
	hz.storage(t, "docs")
	disk, err := diskfree.Measure(hz.rootOf(t, st))
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	cl := hz.mustDial(t, "df@example.com", testPassword)

	vfs, err := cl.StatVFS("/")
	if err != nil {
		t.Fatalf("statvfs: %v", err)
	}
	if total := vfs.Blocks * vfs.Frsize; !within(total, disk.Total, vfs.Frsize) {
		t.Errorf("root size = %d bytes, want the one disk both storages are on (%d)", total, disk.Total)
	}
}

// A storage the caller cannot see has no size to report, the same not-found
// every other verb answers.
func TestStatVFS_AStorageNobodyNamedIsNotFound(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "df@example.com")
	hz.storage(t, "main")
	cl := hz.mustDial(t, "df@example.com", testPassword)
	if _, err := cl.StatVFS("/no-such-storage"); err == nil {
		t.Error("statvfs answered for a storage that does not exist")
	}
}
