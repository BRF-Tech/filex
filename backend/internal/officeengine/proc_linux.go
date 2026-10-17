package officeengine

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setChildAttr: an engine process dies with filex (it also ends on its own
// when its stdin closes, which is the rule everywhere else).
func setChildAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// processRSS is a process's resident memory, from /proc/<pid>/statm.
func processRSS(pid int) (int64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * int64(os.Getpagesize()), true
}
