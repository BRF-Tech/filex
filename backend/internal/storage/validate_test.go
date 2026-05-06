package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateNonRootPath(t *testing.T) {
	cases := []struct {
		name    string
		driver  string
		cfg     map[string]any
		wantErr bool
	}{
		// s3 — prefix
		{"s3 empty prefix", "s3", map[string]any{"bucket": "b"}, true},
		{"s3 blank prefix", "s3", map[string]any{"prefix": ""}, true},
		{"s3 root prefix", "s3", map[string]any{"prefix": "/"}, true},
		{"s3 double-slash prefix", "s3", map[string]any{"prefix": "//"}, true},
		{"s3 whitespace prefix", "s3", map[string]any{"prefix": "  "}, true},
		{"s3 tab+slash prefix", "s3", map[string]any{"prefix": " / "}, true},
		{"s3 valid prefix", "s3", map[string]any{"prefix": "fileman"}, false},
		{"s3 nested prefix", "s3", map[string]any{"prefix": "fileman/sub"}, false},
		{"s3 leading slash valid", "s3", map[string]any{"prefix": "/fileman/"}, false},

		// local — path preferred, root fallback
		{"local empty", "local", map[string]any{}, true},
		{"local root path", "local", map[string]any{"path": "/"}, true},
		{"local valid path", "local", map[string]any{"path": "/srv/fileman"}, false},
		{"local fallback root", "local", map[string]any{"root": "/srv/fileman"}, false},
		{"local empty path then valid root", "local", map[string]any{"path": "", "root": "/srv/fileman"}, false},
		{"local empty path then root /", "local", map[string]any{"path": "", "root": "/"}, true},

		// sftp/ftp/webdav — root preferred, remote_path fallback
		{"sftp empty root", "sftp", map[string]any{}, true},
		{"sftp root /", "sftp", map[string]any{"root": "/"}, true},
		{"sftp valid root", "sftp", map[string]any{"root": "/home/user/fileman"}, false},
		{"sftp fallback remote_path", "sftp", map[string]any{"remote_path": "/data/fileman"}, false},
		{"ftp valid root", "ftp", map[string]any{"root": "fileman"}, false},
		{"webdav empty", "webdav", map[string]any{}, true},
		{"webdav valid root", "webdav", map[string]any{"root": "fileman"}, false},

		// unknown driver — common-keys sweep
		{"unknown empty", "future-driver", map[string]any{}, true},
		{"unknown via prefix", "future-driver", map[string]any{"prefix": "fileman"}, false},
		{"unknown via path", "future-driver", map[string]any{"path": "fileman"}, false},
		{"unknown via root", "future-driver", map[string]any{"root": "fileman"}, false},
		{"unknown via remote_path", "future-driver", map[string]any{"remote_path": "fileman"}, false},
		{"unknown only root /", "future-driver", map[string]any{"root": "/"}, true},

		// non-string config values are ignored (not coerced)
		{"non-string prefix", "s3", map[string]any{"prefix": 42}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNonRootPath(tc.driver, tc.cfg)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("ValidateNonRootPath(%q,%v) err=%v want err=%v", tc.driver, tc.cfg, err, tc.wantErr)
			}
			if tc.wantErr {
				if !errors.Is(err, ErrRootPathForbidden) {
					t.Fatalf("expected ErrRootPathForbidden sentinel, got %v", err)
				}
				if !strings.Contains(err.Error(), "ROOT_PATH_FORBIDDEN") {
					t.Fatalf("expected error message to carry ROOT_PATH_FORBIDDEN tag, got %q", err.Error())
				}
			}
		})
	}
}
