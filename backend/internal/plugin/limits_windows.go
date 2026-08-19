//go:build windows

package plugin

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Keeping a plugin from outliving filex, on Windows.
//
// # The measurement
//
// Stop filex without letting it clean up — a hard kill, a crash, a service
// restart — and every plugin it launched keeps running. Observed 2026-08-19 on
// this workstation: two `memfs.exe` processes still alive, one of them an hour
// after the run that started it had gone. Orphans are not merely untidy here:
// a running plugin holds its own .exe open, so the next install or upgrade of
// that plugin fails with a sharing violation, and the socket it still owns
// makes the next start look mysteriously broken.
//
// # Why a job object and not PDEATHSIG-style tricks
//
// A job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is the platform's own
// answer: the kernel kills every process in the job when the last handle to it
// closes, and filex's death closes its handles whether or not it got to run a
// line of cleanup. Nothing depends on the plugin cooperating.
//
// The Unix-side temptation is `Pdeathsig`, and it is deliberately NOT used
// there: in Go it fires when the OS THREAD that forked exits, and the runtime
// retires idle threads, so a perfectly healthy plugin can be killed for no
// reason. An orphan is a nuisance; a plugin that dies at random is a bug
// report nobody can reproduce.
var (
	jobOnce sync.Once
	jobH    windows.Handle
	jobErr  error
)

func pluginJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = err
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(h)
			jobErr = err
			return
		}
		jobH = h
	})
	return jobH, jobErr
}

// applyLimits is a no-op before Start on Windows: a process can only join a
// job once it exists, so the work happens in adoptChild.
func applyLimits(cmd *exec.Cmd) {}

// adoptChild puts a freshly started plugin into filex's job.
//
// A failure here is logged by the caller and nothing more: the plugin runs,
// and the cost is the orphan this file exists to prevent — which is strictly
// better than refusing to start it.
func adoptChild(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	job, err := pluginJob()
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// killGroup: the job takes care of descendants when filex exits, but an
// ordinary stop should not wait for that. Terminating the process is enough
// for the plugin itself; anything it spawned dies with the job.
func killGroup(pid int) {}
