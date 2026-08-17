package webdav

import (
	"context"
	"testing"
)

// The validator has always demanded a root for webdav, but the driver
// never read one — so a storage that passed validation still mounted the
// base URL. Both sides read the descriptor's root field now.
func TestInit_RootScopesTheMount(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		path string
		want string
	}{
		{
			name: "root joins under the base URL",
			cfg:  map[string]any{"url": "https://dav.example.com/files/", "user": "u", "password": "p", "root": "fileman"},
			path: "a/b.txt",
			want: "https://dav.example.com/files/fileman/a/b.txt",
		},
		{
			name: "remote_path is the legacy spelling",
			cfg:  map[string]any{"url": "https://dav.example.com/files/", "username": "u", "remote_path": "fileman"},
			path: "a/b.txt",
			want: "https://dav.example.com/files/fileman/a/b.txt",
		},
		{
			// Backward compatibility: a row saved before the driver read
			// root has none, and must keep mounting exactly where it did.
			name: "no root mounts the base URL unchanged",
			cfg:  map[string]any{"url": "https://dav.example.com/files/", "user": "u", "password": "p"},
			path: "a/b.txt",
			want: "https://dav.example.com/files/a/b.txt",
		},
		{
			name: "no path component on the base URL",
			cfg:  map[string]any{"url": "https://dav.example.com", "user": "u", "password": "p", "root": "fileman"},
			path: "a.txt",
			want: "https://dav.example.com/fileman/a.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{}
			if err := d.Init(context.Background(), tc.cfg); err != nil {
				t.Fatalf("init: %v", err)
			}
			if got := d.urlFor(tc.path); got != tc.want {
				t.Errorf("urlFor(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestInit_RequiresURLAndUser(t *testing.T) {
	for _, cfg := range []map[string]any{
		{"user": "u"},
		{"url": "https://dav.example.com/"},
	} {
		d := &Driver{}
		if err := d.Init(context.Background(), cfg); err == nil {
			t.Errorf("cfg %v should fail Init", cfg)
		}
	}
}
