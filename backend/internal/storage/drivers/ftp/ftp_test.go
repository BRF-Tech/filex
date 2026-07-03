package ftp

import (
	"context"
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// TestRegistered ensures the init() block registered the driver under the
// expected name and returns a fresh, usable instance.
func TestRegistered(t *testing.T) {
	d, err := storage.Get("ftp")
	if err != nil {
		t.Fatalf("storage.Get(ftp): %v", err)
	}
	if d.Name() != "ftp" {
		t.Fatalf("Name() = %q, want ftp", d.Name())
	}
	if _, ok := d.(*Driver); !ok {
		t.Fatalf("registered factory returned %T, want *Driver", d)
	}
}

// TestInitValidation covers the required-field checks.
func TestInitValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]any
		wantErr bool
	}{
		{
			name:    "missing host",
			cfg:     map[string]any{"user": "u", "password": "p"},
			wantErr: true,
		},
		{
			name:    "missing user",
			cfg:     map[string]any{"host": "h", "password": "p"},
			wantErr: true,
		},
		{
			name:    "missing password",
			cfg:     map[string]any{"host": "h", "user": "u"},
			wantErr: true,
		},
		{
			name: "ok minimal",
			cfg:  map[string]any{"host": "h", "user": "u", "password": "p"},
		},
		{
			name: "ok full",
			cfg: map[string]any{
				"host": "h", "user": "u", "password": "p",
				"port": 2121, "root": "/srv", "tls": true, "passive": false,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{}
			err := d.Init(context.Background(), tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestInitDefaults verifies optional fields fall back to their documented
// defaults.
func TestInitDefaults(t *testing.T) {
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{
		"host": "h", "user": "u", "password": "p",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if d.port != 21 {
		t.Errorf("port default = %d, want 21", d.port)
	}
	if d.root != "/" {
		t.Errorf("root default = %q, want /", d.root)
	}
	if d.tls {
		t.Errorf("tls default = true, want false")
	}
	if !d.passive {
		t.Errorf("passive default = false, want true")
	}
}

// TestCapabilities — FTP is feature-poor (no presign, no multipart, no
// watcher) but supports the rest.
func TestCapabilities(t *testing.T) {
	d := &Driver{}
	got := d.Capabilities()
	want := storage.Capabilities{
		Read: true, Write: true, Move: true,
		Copy: true, Delete: true, Mkdir: true,
	}
	if got != want {
		t.Fatalf("Capabilities() = %+v, want %+v", got, want)
	}
}

// TestComputeCapabilities — verifies the ad-hoc capabilities advertised by
// the static method match what the optional sub-interface assertions
// produce. This catches drift if someone removes a method without
// updating Capabilities().
func TestComputeCapabilities(t *testing.T) {
	d := &Driver{}
	c := storage.ComputeCapabilities(d)
	if !c.Read || !c.Write || !c.Move || !c.Copy || !c.Delete || !c.Mkdir {
		t.Fatalf("ComputeCapabilities missing one or more flags: %+v", c)
	}
	if c.Presign || c.Watch {
		t.Fatalf("FTP must not advertise presign/watch: %+v", c)
	}
}

// TestJoin checks the root-prefix and POSIX cleanup rules.
func TestJoin(t *testing.T) {
	d := &Driver{root: "/home/u"}
	cases := map[string]string{
		"":           "/home/u",
		"/":          "/home/u",
		"a/b":        "/home/u/a/b",
		"/a/b":       "/home/u/a/b",
		"./a":        "/home/u/a",
		"a/../b":     "/home/u/b",
		"/a/../../x": "/home/u/x",
	}
	for in, want := range cases {
		if got := d.join(in); got != want {
			t.Errorf("join(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTranslateErrPassthrough ensures non-FTP errors propagate untouched
// while a 550 response is mapped to ErrNotFound.
func TestTranslateErrPassthrough(t *testing.T) {
	if got := translateErr(nil); got != nil {
		t.Fatalf("translateErr(nil) = %v", got)
	}
	if got := translateErr(errors.New("550 file unavailable")); !errors.Is(got, storage.ErrNotFound) {
		t.Fatalf("translateErr(550) = %v, want ErrNotFound", got)
	}
	custom := errors.New("connection refused")
	if got := translateErr(custom); got != custom {
		t.Fatalf("translateErr passthrough lost original error: %v", got)
	}
}
