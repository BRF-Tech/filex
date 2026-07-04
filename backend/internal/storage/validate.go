package storage

import (
	"errors"
	"strconv"
	"strings"
)

// ConfigInt reads an integer from a driver config value that may arrive as an
// int (YAML / direct), a float64 (JSON numbers unmarshal to float64), or a
// numeric string (form input). This is why e.g. an sftp "port" set via a JSON
// config still resolves correctly.
func ConfigInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}

// ErrRootPathForbidden is returned when a storage is configured with an empty
// or root path/prefix. Operators must always scope a Storage to a sub-folder
// (e.g. "fileman", "data/files") so that filemanager does not shadow or
// inadvertently take ownership of pre-existing files at the bucket / FS root.
var ErrRootPathForbidden = errors.New("ROOT_PATH_FORBIDDEN: storage prefix/path cannot be empty or root '/'; use a sub-folder like 'fileman' or 'data/files'")

// ValidateNonRootPath returns ErrRootPathForbidden if the configured driver
// path/prefix/root is empty or evaluates to the filesystem/bucket root after
// trimming whitespace and slashes.
//
// The function is keyed by driver name to look at the right config field:
//
//	s3              → "prefix"
//	local           → "path" (preferred), then "root"
//	ftp/sftp/webdav → "root" (preferred), then "remote_path"
//
// Unknown drivers fall back to a sweep of the common keys so future drivers
// are protected by default. Callers should invoke this from API handlers
// (Storage create / update) BEFORE persisting the row.
func ValidateNonRootPath(driver string, cfg map[string]any) error {
	var p string
	switch driver {
	case "s3":
		p, _ = cfg["prefix"].(string)
	case "local":
		p, _ = cfg["path"].(string)
		if p == "" {
			p, _ = cfg["root"].(string)
		}
	case "ftp", "sftp", "webdav":
		p, _ = cfg["root"].(string)
		if p == "" {
			p, _ = cfg["remote_path"].(string)
		}
	default:
		// Unknown drivers: try common keys.
		for _, k := range []string{"prefix", "path", "root", "remote_path"} {
			if v, ok := cfg[k].(string); ok && v != "" {
				p = v
				break
			}
		}
	}
	p = strings.Trim(p, "/ \t\r\n")
	if p == "" {
		return ErrRootPathForbidden
	}
	return nil
}
