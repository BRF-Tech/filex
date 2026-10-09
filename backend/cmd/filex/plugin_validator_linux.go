//go:build linux

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pvCredentialsSupported: Linux can start the plugin under test as another
// user (the validator runs as root in its container, with CAP_SETUID,
// CAP_SETGID, CAP_CHOWN, CAP_KILL, CAP_DAC_OVERRIDE and CAP_FOWNER only).
const pvCredentialsSupported = true

// pvRunAs starts a command as uid:gid with no supplementary groups.
func pvRunAs(uid, gid int) func(*exec.Cmd) {
	return func(cmd *exec.Cmd) {
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}
	}
}

// pvChown hands one path to uid (gid -1: unchanged).
func pvChown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

// pvOwner is the uid that owns a file.
func pvOwner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// pvWritableBy reports whether a process running as uid:gid, with no
// supplementary groups, may create or replace entries in the directory fi
// describes - the permission bits the kernel would check for it: the owner's
// when it owns it, else the group's when it is in it, else everyone's. A
// file it cannot read the owner of counts as writable.
func pvWritableBy(fi os.FileInfo, uid, gid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	m := fi.Mode().Perm()
	switch {
	case int(st.Uid) == uid:
		return m&0o200 != 0
	case int(st.Gid) == gid:
		return m&0o020 != 0
	default:
		return m&0o002 != 0
	}
}

// pvSweepTime bounds one sweep: a process killed is gone in milliseconds,
// one in an uninterruptible wait (a disk, a FUSE call) takes longer.
const pvSweepTime = 10 * time.Second

// pvSweep is what runs between two runs: every process of the plugin's user
// killed, then what that user left in the shared temporary directories
// removed. An error is a process left alive; the leftovers are only logged.
func pvSweep(o pvOptions) error {
	if o.pluginUID <= 0 {
		return nil
	}
	if err := pvKillUser(o.pluginUID, pvSweepTime); err != nil {
		return err
	}
	if err := pvClearLeftovers(o.pluginUID); err != nil && o.log != nil {
		o.log.Warn("plugin-validator: what the plugin's user left could not all be removed", slog.Any("err", err))
	}
	return nil
}

// pvKillUser kills every process running as uid until /proc shows none
// alive, or answers how many outlived limit.
//
// ⚠ Not the process group: a plugin can leave it (setsid) and live on after
// its run, beside the next run's socket. A process of that user cannot change
// user (no capability, no-new-privileges), so its uid is what finds it. Each
// round kills what it sees; a child forked meanwhile is seen by the next.
func pvKillUser(uid int, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		pids, err := pvProcsOf(uid)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d process(es) of the plugin's user (uid %d) are still running after %s: %v", len(pids), uid, limit, pids)
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pvProcsOf lists the live processes (thread groups) whose real, effective,
// saved or filesystem uid is uid. A zombie is dead - its parent, or the
// container's init, reaps it - unless it is a group leader whose other
// threads still run.
func pvProcsOf(uid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("the processes cannot be listed: %w", err)
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue // gone meanwhile
		}
		if pvStatusRunsAs(string(b), uid) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// pvStatusRunsAs reads a /proc/<pid>/status: a live process any of whose
// uids is uid.
func pvStatusRunsAs(status string, uid int) bool {
	dead, threads, match := false, 1, false
	for _, line := range strings.Split(status, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		switch k {
		case "State":
			dead = len(f) > 0 && (f[0] == "Z" || f[0] == "X")
		case "Threads":
			if len(f) > 0 {
				if n, err := strconv.Atoi(f[0]); err == nil {
					threads = n
				}
			}
		case "Uid":
			for _, s := range f {
				if n, err := strconv.Atoi(s); err == nil && n == uid {
					match = true
				}
			}
		}
	}
	return match && (!dead || threads > 1)
}

// pvSharedTemp are the directories any user may create in: the plugin's
// scratch space, which the next run's plugin shares.
var pvSharedTemp = []string{"/tmp", "/var/tmp", "/dev/shm"}

// pvClearLeftovers removes what uid created at the top of the shared
// temporary directories. Called only once no process of uid is left, so
// nothing is swapped under the removal (which follows no link).
func pvClearLeftovers(uid int) error {
	var errs []error
	seen := map[string]bool{}
	for _, d := range append([]string{os.TempDir()}, pvSharedTemp...) {
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			fi, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if owner, ok := pvOwner(fi); ok && owner == uid {
				if err := os.RemoveAll(p); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}
