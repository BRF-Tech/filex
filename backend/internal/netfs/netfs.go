// Package netfs answers one question about a directory: is the filesystem it
// lives on one that another machine can write to without this kernel seeing
// it happen?
//
// It matters to the lazy catalogue (docs/LAZY-CATALOGUE.md), which trusts a
// folder it watches with fsnotify and lists it from the catalogue alone. A
// watch (inotify, FSEvents, ReadDirectoryChangesW) reports what passes through
// THIS kernel. On NFS, SMB/CIFS, 9P, a cluster filesystem or a FUSE mount, a
// file another machine writes - or another process writes past the FUSE layer
// - never passes through it, so the watch stays quiet and the folder looks
// current while it is not: the file someone copied onto the NAS does not show
// up in filex (task #351). On such a filesystem the lazy catalogue places no
// watch, and every folder somebody opens is read from the disk.
//
// FUSE counts as "network" on purpose. inotify on a FUSE mount only sees the
// changes that go through that mount on this kernel; Unraid's /mnt/user (shfs)
// merges several disks, and a write through /mnt/diskN, /mnt/cache, the mover
// or another container mapped to a disk path bypasses it. A watch there looks
// trustworthy and misses exactly the writes #351 is about.
//
// A probe that cannot answer reports ok=false; callers decide what an unknown
// filesystem means for them (the lazy catalogue treats it as untrusted).
package netfs

import (
	"path/filepath"
	"strings"
	gosync "sync"
	"time"
)

// Info is what the probe found.
type Info struct {
	// Type names the filesystem when it has a protocol name a person would
	// recognise ("NFS", "SMB", "FUSE", "9P"...). Empty when there is none to
	// give (a Windows network drive says only that it is remote).
	Type string `json:"type,omitempty"`
	// Network: another machine (or another path past a FUSE layer) can change
	// the directory without this kernel's change notifications seeing it.
	Network bool `json:"network"`
}

// Linux statfs f_type values of the filesystems a watch cannot be trusted on
// (linux/magic.h and the filesystems' own headers). Defined here rather than
// taken from x/sys/unix, which does not carry all of them on every arch.
var linuxNetwork = map[uint32]string{
	0x00006969: "NFS",    // NFS_SUPER_MAGIC
	0x0000517B: "SMB",    // SMB_SUPER_MAGIC (smbfs, the old client)
	0xFF534D42: "CIFS",   // CIFS_SUPER_MAGIC
	0xFE534D42: "SMB2",   // SMB2_SUPER_MAGIC (the cifs client speaking SMB2/3)
	0x65735546: "FUSE",   // FUSE_SUPER_MAGIC: sshfs, rclone mount, shfs, virtiofs, gluster...
	0x01021997: "9P",     // V9FS_MAGIC: WSL drive mounts, VM shared folders
	0x00C36400: "CephFS", // CEPH_SUPER_MAGIC
	0x5346414F: "AFS",    // AFS_SUPER_MAGIC (OpenAFS)
	0x6B414653: "AFS",    // AFS_FS_MAGIC (kAFS)
	0x73757245: "Coda",   // CODA_SUPER_MAGIC
	0x0000564C: "NCP",    // NCP_SUPER_MAGIC
	0x0BD00BD0: "Lustre", // LUSTRE_SUPER_MAGIC
	0x47504653: "GPFS",   // GPFS_SUPER_MAGIC (IBM Storage Scale)
	0x01161970: "GFS2",   // GFS2_MAGIC
	0x7461636F: "OCFS2",  // OCFS2_SUPER_MAGIC
	0x19830326: "BeeGFS", // BEEGFS (fhgfs)
}

// classifyLinux reads a statfs f_type. The field is int32 on 386 and arm and
// int64 on amd64; the caller converts with uint32(), which keeps 0xFF534D42 as
// it is whichever sign the platform gave it.
func classifyLinux(magic uint32) Info {
	if name, ok := linuxNetwork[magic]; ok {
		return Info{Type: name, Network: true}
	}
	return Info{}
}

// macOS f_fstypename values of network and FUSE filesystems.
var darwinNetwork = map[string]string{
	"nfs":     "NFS",
	"smbfs":   "SMB",
	"cifs":    "CIFS",
	"afpfs":   "AFP",
	"webdav":  "WebDAV",
	"ftp":     "FTP",
	"macfuse": "FUSE",
	"osxfuse": "FUSE",
	"fusefs":  "FUSE",
}

// classifyDarwin reads statfs's f_fstypename and whether the mount carries
// MNT_LOCAL. A name on the list is network whatever the flag says (a FUSE
// mount may call itself local); a mount without MNT_LOCAL is network whatever
// its name.
func classifyDarwin(fstype string, local bool) Info {
	name := strings.ToLower(strings.TrimRight(fstype, "\x00"))
	if t, ok := darwinNetwork[name]; ok {
		return Info{Type: t, Network: true}
	}
	if strings.HasPrefix(name, "fuse") || strings.HasPrefix(name, "macfuse") {
		return Info{Type: "FUSE", Network: true}
	}
	if !local && name != "" {
		return Info{Type: name, Network: true}
	}
	return Info{}
}

// driveRemote is GetDriveType's DRIVE_REMOTE.
const driveRemote = 4

// classifyWindows: a UNC path (\\server\share, or \\?\UNC\server\share) is a
// network path; anything else is remote when its volume is a network drive
// (a mapped letter). `\\?\C:\...` is a local path written the long way.
func classifyWindows(p string, driveType uint32) Info {
	s := strings.ReplaceAll(p, "/", `\`)
	switch {
	case strings.HasPrefix(strings.ToUpper(s), `\\?\UNC\`):
		return Info{Network: true}
	case strings.HasPrefix(s, `\\?\`), strings.HasPrefix(s, `\\.\`):
		// a long local path or a device path: the drive type decides.
	case strings.HasPrefix(s, `\\`):
		return Info{Network: true}
	}
	if driveType == driveRemote {
		return Info{Network: true}
	}
	return Info{}
}

// Probe asks the operating system about dir, now. It can block for as long as
// the filesystem does (a hung NFS mount blocks statfs): callers on a request
// path use ProbeWithin.
func Probe(dir string) (Info, error) { return probe(dir) }

// probe is the platform's probe; a test swaps it.
var probe = probeFS

// CacheFor is how long ProbeWithin keeps an answer: a storage's mount does not
// change under a running process often, and the admin storage list asks on
// every load.
var CacheFor = 5 * time.Minute

type cached struct {
	info Info
	ok   bool
	at   time.Time
	done chan struct{}
}

var (
	cacheMu gosync.Mutex
	cache   = map[string]*cached{}
)

// ProbeWithin is Probe, remembered for CacheFor and waited for at most wait.
// ok is false when the probe failed or did not answer in time; a probe still
// running is not started a second time, so a hung mount costs one goroutine,
// not one per request.
func ProbeWithin(dir string, wait time.Duration) (Info, bool) {
	dir = filepath.Clean(dir)
	cacheMu.Lock()
	c := cache[dir]
	if c != nil {
		select {
		case <-c.done:
			if time.Since(c.at) < CacheFor {
				cacheMu.Unlock()
				return c.info, c.ok
			}
			c = nil
		default:
		}
	}
	if c == nil {
		c = &cached{done: make(chan struct{})}
		cache[dir] = c
		go func(c *cached) {
			info, err := probe(dir)
			cacheMu.Lock()
			c.info, c.ok, c.at = info, err == nil, time.Now()
			cacheMu.Unlock()
			close(c.done)
		}(c)
	}
	cacheMu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-c.done:
		cacheMu.Lock()
		defer cacheMu.Unlock()
		return c.info, c.ok
	case <-t.C:
		return Info{}, false
	}
}
