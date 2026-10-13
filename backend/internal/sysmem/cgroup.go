package sysmem

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// unlimitedFloor: cgroup v1 writes "no limit" as a page-rounded int64 max
// (9223372036854771712); anything this large is no limit at all.
const unlimitedFloor = uint64(1) << 62

// cgroupLimitFrom reads the memory limit of the cgroup that self (the text of
// /proc/self/cgroup) names, with the cgroup filesystem under root.
//
// The effective limit is the smallest one on the way up: a cgroup inside a
// limited parent is held by the parent's limit whatever its own file says.
// Inside a container with its own cgroup namespace the path is "/" and only
// the root files are read; with the host's namespace it is the full path.
// cgroup v2 is tried before v1, as a hybrid host serves both.
func cgroupLimitFrom(root, self string) (uint64, string) {
	var v2, v1 string
	haveV2, haveV1 := false, false
	for _, line := range strings.Split(self, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			v2, haveV2 = parts[2], true
			continue
		}
		for _, c := range strings.Split(parts[1], ",") {
			if c == "memory" {
				v1, haveV1 = parts[2], true
			}
		}
	}
	if haveV2 {
		if n := smallestUp(filepath.Join(root, "sys", "fs", "cgroup"), v2, "memory.max"); n > 0 {
			return n, SourceCgroupV2
		}
	}
	if haveV1 {
		if n := smallestUp(filepath.Join(root, "sys", "fs", "cgroup", "memory"), v1, "memory.limit_in_bytes"); n > 0 {
			return n, SourceCgroupV1
		}
	}
	return 0, ""
}

// smallestUp reads file in dir/p and every ancestor of p up to dir itself, and
// returns the smallest real limit among them (0: none).
func smallestUp(dir, p, file string) uint64 {
	var best uint64
	p = path.Clean("/" + p)
	for {
		if n := readLimit(filepath.Join(dir, filepath.FromSlash(p), file)); n > 0 && (best == 0 || n < best) {
			best = n
		}
		if p == "/" {
			return best
		}
		p = path.Dir(p)
	}
}

// readLimit reads one limit file: a number of bytes, or "max" / a v1
// no-limit value, both of which mean none (0).
func readLimit(file string) uint64 {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "" || s == "max" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n >= unlimitedFloor {
		return 0
	}
	return n
}
