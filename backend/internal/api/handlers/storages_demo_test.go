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

	// The remote drivers go too, by decision (2026-08-19). "Attach your own
	// bucket" reads as harmless, but what it asks the SERVER to do is connect
	// to an address a stranger chose — loopback, a private range, a cloud
	// metadata endpoint. A demo does not need to offer that.
	for _, d := range []string{"s3", "sftp", "webdav", "smb"} {
		if err := demo.denyOnDemo(d); err == nil {
			t.Fatalf("%s asks the server to connect where a visitor points it", d)
		}
	}

	// Plugins are the exception, and only because they cannot normally exist
	// here: demo mode disables the subsystem, so a plugin storage means the
	// operator deliberately turned it back on for their own program.
	if err := demo.denyOnDemo("plugin:memfs"); err != nil {
		t.Fatalf("a deliberately installed plugin is the operator's own: %v", err)
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
