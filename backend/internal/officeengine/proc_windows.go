package officeengine

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setChildAttr: nothing on Windows (an engine process ends when its stdin
// closes, which happens when filex does).
func setChildAttr(*exec.Cmd) {}

// processMemoryCounters is PROCESS_MEMORY_COUNTERS (psapi.h).
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processRSS is a process's working set (GetProcessMemoryInfo).
func processRSS(pid int) (int64, bool) {
	c, ok := processMemoryInfo(pid)
	return int64(c.workingSetSize), ok
}

// processMemoryInfo is a process's GetProcessMemoryInfo.
func processMemoryInfo(pid int) (processMemoryCounters, bool) {
	var c processMemoryCounters
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return c, false
	}
	defer windows.CloseHandle(h)
	c.cb = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	return c, r != 0
}
