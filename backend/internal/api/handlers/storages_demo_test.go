package handlers

import "testing"

// A demo instance publishes an admin login — that is what a demo is for — so
// every admin-only door on it is a public door. `local` is the driver that
// opens onto the SERVER's own filesystem.
//
// Measured on this project's public demo (2026-08-19), before the guard
// existed: storages rooted at /data, /etc and /proc/1 were all accepted with
// 200. That is the database, the configuration, and the process environment
// of the host — offered to anyone who read the credentials off the landing
// page. The pre-existing check only refused "/" itself, which stops the
// laziest version of it and nothing else.
func TestDenyOnDemoRefusesLocalDriver(t *testing.T) {
	demo := &Storages{DemoMode: true}
	if err := demo.denyOnDemo("local"); err == nil {
		t.Fatal("a demo must not accept a storage on the server's own filesystem")
	}

	// A bucket or a remote host a visitor brings is their own business; the
	// guard is about this machine's disk, so it must not turn the demo into a
	// read-only exhibit.
	for _, d := range []string{"s3", "sftp", "webdav", "smb", "plugin:memfs"} {
		if err := demo.denyOnDemo(d); err != nil {
			t.Fatalf("%s is the visitor's own backend, not this host: %v", d, err)
		}
	}
}

func TestDenyOnDemoIsSilentOnOrdinaryInstances(t *testing.T) {
	// Off a demo, "admin" is the operator, and mounting a local path is the
	// single most ordinary thing they do.
	ordinary := &Storages{}
	for _, d := range []string{"local", "s3", "sftp"} {
		if err := ordinary.denyOnDemo(d); err != nil {
			t.Fatalf("%s must be accepted on a normal instance: %v", d, err)
		}
	}
}
