package netfs

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forget empties the answer cache.
func forget() {
	cacheMu.Lock()
	cache = map[string]*cached{}
	cacheMu.Unlock()
}

// swapProbe replaces the platform probe for one test.
func swapProbe(t *testing.T, f func(string) (Info, error)) {
	t.Helper()
	was := probe
	probe = f
	forget()
	t.Cleanup(func() {
		probe = was
		forget()
	})
}

// The filesystems task #351 is about are network ones; a local disk, tmpfs
// and the overlay a container runs on are not.
//
// Break: drop FUSE from linuxNetwork - Unraid's /mnt/user (shfs) would be
// watched, and a file written through /mnt/diskN would never show up.
func TestClassifyLinux_NetworkAndFUSEAreNotWatched(t *testing.T) {
	cases := map[uint32]Info{
		0x6969:     {Type: "NFS", Network: true},
		0xFF534D42: {Type: "CIFS", Network: true},
		0xFE534D42: {Type: "SMB2", Network: true},
		0x65735546: {Type: "FUSE", Network: true},
		0x01021997: {Type: "9P", Network: true},
		0x00C36400: {Type: "CephFS", Network: true},
		0xEF53:     {}, // ext2/3/4
		0x58465342: {}, // xfs
		0x9123683E: {}, // btrfs
		0x01021994: {}, // tmpfs
		0x794C7630: {}, // overlayfs (a container's root)
		0x2FC12FC1: {}, // zfs
	}
	for magic, want := range cases {
		assert.Equal(t, want, classifyLinux(magic), "f_type %#x", magic)
	}
}

// statfs's f_type is an int32 on 386 and arm: CIFS's magic arrives negative,
// and uint32() must still find it.
func TestClassifyLinux_ASignedMagicIsTheSameFilesystem(t *testing.T) {
	var signed int32 = -11317950 // 0xFF534D42 as an int32
	assert.Equal(t, Info{Type: "CIFS", Network: true}, classifyLinux(uint32(signed)))
}

func TestClassifyDarwin(t *testing.T) {
	assert.Equal(t, Info{}, classifyDarwin("apfs", true))
	assert.Equal(t, Info{Type: "NFS", Network: true}, classifyDarwin("nfs", false))
	assert.Equal(t, Info{Type: "SMB", Network: true}, classifyDarwin("smbfs\x00\x00", false))
	assert.Equal(t, Info{Type: "AFP", Network: true}, classifyDarwin("afpfs", false))
	// macFUSE may call its mount local; it is FUSE all the same.
	assert.Equal(t, Info{Type: "FUSE", Network: true}, classifyDarwin("macfuse", true))
	assert.Equal(t, Info{Type: "FUSE", Network: true}, classifyDarwin("fuse-t", true))
	// An unknown name without MNT_LOCAL is somebody else's disk.
	assert.Equal(t, Info{Type: "lifs", Network: true}, classifyDarwin("lifs", false))
}

func TestClassifyWindows(t *testing.T) {
	assert.True(t, classifyWindows(`\\nas\share\photos`, 0).Network, "UNC path")
	assert.True(t, classifyWindows(`//nas/share/photos`, 0).Network, "UNC path with forward slashes")
	assert.True(t, classifyWindows(`\\?\UNC\nas\share`, 0).Network, "long UNC path")
	assert.True(t, classifyWindows(`Z:\photos`, driveRemote).Network, "mapped network drive")
	assert.False(t, classifyWindows(`C:\data`, 3).Network, "fixed disk")
	assert.False(t, classifyWindows(`\\?\C:\data`, 3).Network, "a long local path is local")
}

// The answer is remembered, so the admin storage list does not statfs every
// storage on every load.
func TestProbeWithin_RemembersTheAnswer(t *testing.T) {
	var calls atomic.Int32
	swapProbe(t, func(string) (Info, error) {
		calls.Add(1)
		return Info{Type: "NFS", Network: true}, nil
	})
	for i := 0; i < 3; i++ {
		info, ok := ProbeWithin("/srv/nas", time.Second)
		require.True(t, ok)
		assert.True(t, info.Network)
	}
	assert.Equal(t, int32(1), calls.Load())
}

// A hung mount (statfs never returns) costs the caller the wait and nothing
// more: the answer is unknown, and a second caller does not start a second
// probe of the same directory.
//
// Break: start a probe on every call - the second call starts another, and
// calls reaches 2.
func TestProbeWithin_AHungMountIsUnknownAndProbedOnce(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	swapProbe(t, func(string) (Info, error) {
		calls.Add(1)
		<-release
		return Info{}, nil
	})
	defer close(release)
	_, ok := ProbeWithin("/mnt/hung", 20*time.Millisecond)
	assert.False(t, ok)
	_, ok = ProbeWithin("/mnt/hung", 20*time.Millisecond)
	assert.False(t, ok)
	assert.Equal(t, int32(1), calls.Load())
}

func TestProbeWithin_AFailedProbeIsUnknown(t *testing.T) {
	swapProbe(t, func(string) (Info, error) { return Info{}, errors.New("no such directory") })
	_, ok := ProbeWithin("/nowhere", time.Second)
	assert.False(t, ok)
}

// The platform probe answers for a directory on the machine running the
// tests, which is a local disk (or tmpfs) on every CI runner.
func TestProbe_TheTestMachinesTempDirIsLocal(t *testing.T) {
	if os.Getenv("FILEX_TEST_TMP_IS_NETWORK") != "" {
		t.Skip("the temp directory is on a network filesystem here")
	}
	info, err := Probe(t.TempDir())
	if err != nil {
		t.Skipf("no probe on this platform: %v", err)
	}
	assert.False(t, info.Network, "%+v", info)
}
