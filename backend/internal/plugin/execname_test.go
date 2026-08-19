package plugin_test

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// A plugin stored under the wrong name on Windows is a plugin that never
// starts, and the error Go reports for it — "executable file not found in
// %PATH%" — points at the one thing that is not wrong: the file is exactly
// where filex put it. Measured on Windows 11 with the example plugin uploaded
// as `memfs`, 2026-08-19.
func TestExecNameGivesWindowsBinariesAnExtension(t *testing.T) {
	cases := []struct {
		in, goos, want string
	}{
		// Windows: an extension is what makes a file executable at all.
		{"memfs", "windows", "memfs.exe"},
		{"memfs.exe", "windows", "memfs.exe"},
		{"MemFS.EXE", "windows", "MemFS.EXE"}, // already executable, left alone
		{"run.bat", "windows", "run.bat"},     // and the other executable kinds
		{"run.cmd", "windows", "run.cmd"},
		{"myfs-v2.1", "windows", "myfs-v2.1.exe"}, // ⚠ ".1" is not an executable extension
		// Elsewhere the name is the name; the permission bits decide.
		{"memfs", "linux", "memfs"},
		{"memfs.exe", "linux", "memfs.exe"},
		{"myfs-v2.1", "darwin", "myfs-v2.1"},
		// A path is reduced to its base, and nothing addressable is accepted.
		{"/etc/passwd", "linux", "passwd"},
		{"../../evil", "linux", "evil"},
		{"", "linux", "plugin"},
		{"", "windows", "plugin.exe"},
		{"  spaced  ", "linux", "spaced"},
	}
	for _, c := range cases {
		if got := plugin.ExecName(c.in, c.goos); got != c.want {
			t.Errorf("ExecName(%q, %q) = %q, want %q", c.in, c.goos, got, c.want)
		}
	}
}
