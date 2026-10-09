//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pvTestStartLingerer is what a plugin that wants to outlive its run does:
// a helper in a session of its own, out of the process group the validator
// kills.
func pvTestStartLingerer(exe string) error {
	cmd := exec.Command(exe, pvTestLingerChildArg)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// pvTestLiveProcsOf is the test's own look at /proc: the processes of uid
// that are not zombies.
func pvTestLiveProcsOf(t *testing.T, uid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "status"))
		if err != nil {
			continue
		}
		var zombie, mine bool
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			switch {
			case len(f) >= 2 && f[0] == "State:":
				zombie = f[1] == "Z" || f[1] == "X"
			case len(f) >= 2 && f[0] == "Uid:":
				mine = f[1] == strconv.Itoa(uid)
			}
		}
		if mine && !zombie {
			pids = append(pids, pid)
		}
	}
	return pids
}

// pvTestKillAllOf leaves nothing of uid behind, whatever the test found.
func pvTestKillAllOf(t *testing.T, uid int) {
	for i := 0; i < 50; i++ {
		pids := pvTestLiveProcsOf(t, uid)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A store's validator on Linux holds the plugin under test apart from
// itself, the store and the next run - or does not start.
func TestPluginValidator_RefusesAConfigurationThatDoesNotHoldThePluginApart(t *testing.T) {
	type tc struct {
		name, want string
		edit       func(t *testing.T, o *pvOptions)
	}
	cases := []tc{
		{"without --plugin-uid", "--plugin-uid is required", func(t *testing.T, o *pvOptions) { o.pluginUID, o.pluginGID = -1, -1 }},
		{"as uid 0", "never started as root", func(t *testing.T, o *pvOptions) { o.pluginUID, o.pluginGID = 0, pvTestUID }},
		{"as gid 0", "never started as root", func(t *testing.T, o *pvOptions) { o.pluginGID = 0 }},
		{"as the store's user", "is the store's user", func(t *testing.T, o *pvOptions) { o.storeUID = pvTestUID }},
		{"with --insecure-dev", "--insecure-dev is for development outside Linux", func(t *testing.T, o *pvOptions) { o.insecureDev = true }},
		{"without --work-dir", "--work-dir is required", func(t *testing.T, o *pvOptions) { o.workRoot = "" }},
		{"in a shared --work-dir", "is a shared directory", func(t *testing.T, o *pvOptions) {
			d := filepath.Join(filepath.Dir(o.workRoot), "shared")
			if err := os.Mkdir(d, 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(d, 0o1777); err != nil {
				t.Fatal(err)
			}
			o.workRoot = d
		}},
	}
	if euid := os.Geteuid(); euid != 0 {
		cases = append(cases, tc{"as this command's own user", "this command's own user", func(t *testing.T, o *pvOptions) { o.pluginUID, o.pluginGID = euid, euid }})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := validatorOptions(t, t.TempDir())
			o.pluginUID, o.pluginGID = pvTestUID, pvTestUID
			c.edit(t, &o)
			err := runPluginValidator(context.Background(), o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal saying %q, got %v", c.want, err)
			}
		})
	}
}

// The results are written where the plugin's user can neither write nor
// plant a link: a spool that user may change is refused.
func TestPluginValidator_RefusesASpoolThePluginsUserCanWrite(t *testing.T) {
	spool := t.TempDir()
	if err := os.Chmod(spool, 0o777); err != nil {
		t.Fatal(err)
	}
	o := validatorOptions(t, spool)
	o.pluginUID, o.pluginGID = pvTestUID, pvTestUID
	err := runPluginValidator(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "writable by the plugin's user") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

// As root (the store's container) the plugin runs as a user of its own, and
// from inside its run it can start its build and use its socket directory -
// nothing else: not change the build or the run's directory, not list the
// other runs.
func TestPluginValidator_ThePluginCannotChangeItsRunOrListTheOthers(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("starting the plugin as another user needs root")
	}
	o := validatorOptions(t, t.TempDir())
	t.Cleanup(func() { pvTestKillAllOf(t, o.pluginUID) })
	line := pvRunProbe(t, o, pvTestProbeName, runPluginValidator)
	pvProbeSays(t, line, "bin=0555", "work=0711", "root=0711", "run=0700",
		"write-work=denied", "chmod-bin=denied", "list-root=denied", "write-run=ok")
}

// ⚠⚠ A helper the plugin started in a session of its own (out of the process
// group a stop kills) does not outlive the run: the next run's socket
// directory and build would be within its reach.
func TestPluginValidator_NothingThePluginStartedOutlivesItsRun(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("starting the plugin as another user needs root")
	}
	o := validatorOptions(t, t.TempDir())
	t.Cleanup(func() { pvTestKillAllOf(t, o.pluginUID) })
	if left := pvTestLiveProcsOf(t, o.pluginUID); len(left) > 0 {
		t.Skipf("uid %d already runs processes on this machine (%v); the test needs it unused", o.pluginUID, left)
	}
	line := pvRunProbe(t, o, pvTestLingerName, runPluginValidator)
	pvProbeSays(t, line, "linger=ok")
	if left := pvTestLiveProcsOf(t, o.pluginUID); len(left) > 0 {
		t.Fatalf("processes of the plugin's user outlived its run: %v", left)
	}
}

func TestPvStatusRunsAs(t *testing.T) {
	status := func(state, threads, uids string) string {
		return "Name:\tplugin\nState:\t" + state + "\nTgid:\t42\nUid:\t" + uids + "\nGid:\t0\t0\t0\t0\nThreads:\t" + threads + "\n"
	}
	for _, c := range []struct {
		name, status string
		want         bool
	}{
		{"a running process of the user", status("S (sleeping)", "4", "65123\t65123\t65123\t65123"), true},
		{"one whose saved uid is the user's", status("R (running)", "1", "1000\t1000\t65123\t1000"), true},
		{"another user's", status("S (sleeping)", "1", "1000\t1000\t1000\t1000"), false},
		{"a zombie", status("Z (zombie)", "1", "65123\t65123\t65123\t65123"), false},
		{"a zombie leader whose threads still run", status("Z (zombie)", "3", "65123\t65123\t65123\t65123"), true},
	} {
		if got := pvStatusRunsAs(c.status, pvTestUID); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
