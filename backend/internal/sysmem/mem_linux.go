//go:build linux

package sysmem

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func cgroupLimit() (uint64, string) {
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, ""
	}
	return cgroupLimitFrom("/", string(self))
}

// hostTotal is MemTotal from /proc/meminfo, in bytes.
func hostTotal() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}
