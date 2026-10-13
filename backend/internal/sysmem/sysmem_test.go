package sysmem

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFile writes body to root/rel, creating its folders.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A container with its own cgroup namespace sees "0::/" and the limit at the
// root of /sys/fs/cgroup: the case of the podman deployment in issue #115.
func TestCgroupV2_TheContainersOwnLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sys/fs/cgroup/memory.max", "4294967296\n")
	n, src := cgroupLimitFrom(root, "0::/\n")
	if n != 4<<30 || src != SourceCgroupV2 {
		t.Errorf("got %d from %q, want 4 GiB from cgroup v2", n, src)
	}
}

// With the host's namespace the path is the full one, and a parent's limit
// holds a child that has none of its own.
func TestCgroupV2_AParentsLimitHoldsTheChild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sys/fs/cgroup/machine.slice/memory.max", "2147483648\n")
	writeFile(t, root, "sys/fs/cgroup/machine.slice/libpod-abc.scope/memory.max", "max\n")
	writeFile(t, root, "sys/fs/cgroup/memory.max", "max\n")
	n, _ := cgroupLimitFrom(root, "0::/machine.slice/libpod-abc.scope\n")
	if n != 2<<30 {
		t.Errorf("got %d, want the parent's 2 GiB", n)
	}
}

func TestCgroupV2_NoLimitIsNone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sys/fs/cgroup/memory.max", "max\n")
	if n, _ := cgroupLimitFrom(root, "0::/\n"); n != 0 {
		t.Errorf("got %d, want none", n)
	}
}

// cgroup v1 writes "no limit" as a huge page-rounded number.
func TestCgroupV1_LimitAndNoLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sys/fs/cgroup/memory/memory.limit_in_bytes", "1073741824\n")
	n, src := cgroupLimitFrom(root, "12:cpu,cpuacct:/\n11:memory:/\n")
	if n != 1<<30 || src != SourceCgroupV1 {
		t.Errorf("got %d from %q, want 1 GiB from cgroup v1", n, src)
	}
	writeFile(t, root, "sys/fs/cgroup/memory/memory.limit_in_bytes", "9223372036854771712\n")
	if n, _ := cgroupLimitFrom(root, "11:memory:/\n"); n != 0 {
		t.Errorf("v1 no-limit read as %d", n)
	}
}

func TestShareClampsThePercent(t *testing.T) {
	b := Budget{Bytes: 100 << 20}
	cases := []struct{ percent, want int }{{25, 25}, {0, 1}, {-3, 1}, {50, 50}, {90, 50}}
	for _, c := range cases {
		if got := b.Share(c.percent, 1, 50); got != uint64(c.want)*(1<<20) {
			t.Errorf("Share(%d) = %d, want %d MiB", c.percent, got, c.want)
		}
	}
	if (Budget{}).Share(25, 1, 50) != 0 {
		t.Error("an unread budget has a share")
	}
}

func TestAllocatedAndInUseAnswer(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if b := Allocated(); b.Bytes == 0 || b.Source == SourceUnknown {
			t.Errorf("Allocated() = %+v on %s", b, runtime.GOOS)
		}
	}
	if InUse() == 0 {
		t.Error("the Go runtime holds no memory")
	}
}
