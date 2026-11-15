package archivecli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// listedName is a member's name the way 7-Zip lists it. 7-Zip 25.01 and
// newer - every version filex accepts - print a line feed in a name as `_`
// in `l -slt` (and in `l`); a tab, a carriage return, an escape and the other
// control characters come through as they are (measured on 25.01 from Debian
// 13, 26.00 from Ubuntu 26.04 and 26.01 from Alpine 3.24, the one the Docker
// image carries; task #350). The archive keeps the line feed: extracting
// writes the real name.
func listedName(name string) string { return strings.ReplaceAll(name, "\n", "_") }

// extractLineBreakMembers extracts the selected members a listing named with
// a `_` where the archive has a line feed. Asked for by the listed name, 7-Zip
// finds no such member and extracts nothing ("No files to process", exit 0),
// so a selection of `line_break.txt` used to end with nothing written and no
// error (task #350; #238 had promised those names).
//
// A member the first run did not write is asked for again with a `?` in
// place of each `_` - 7-Zip's one-character wildcard matches a line feed -
// into a folder of its own inside destDir, so the same budget watches it.
// The wildcard also matches look-alikes (`line_break.txt`, `lineXbreak.txt`):
// only a path that holds a line feed and lists as the selected name moves
// to destDir, under its real name; the rest is removed with the folder. A
// selected name 7-Zip found as it is was the member asked for, and stays the
// only one: an archive holding both `a_b.txt` and `a` LF `b.txt` lists them
// alike, and the request names the one whose name it is.
func (s *Service) extractLineBreakMembers(ctx context.Context, bin, kind, archivePath, destDir, password string, members []string) error {
	// Windows refuses a line feed in a file name: nothing to find there.
	if runtime.GOOS == "windows" {
		return nil
	}
	want := map[string]bool{}
	var patterns []string
	for _, member := range members {
		name := strings.TrimSuffix(member, "/")
		if !strings.Contains(name, "_") || want[name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(destDir, filepath.FromSlash(name))); err == nil {
			continue
		}
		want[name] = true
		patterns = append(patterns, strings.ReplaceAll(name, "_", "?"))
	}
	if len(patterns) == 0 {
		return nil
	}
	scratch, err := os.MkdirTemp(destDir, ".filex-linebreak-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err := s.runSevenZipExtract(ctx, bin, kind, archivePath, scratch, destDir, password, false, patterns); err != nil {
		return err
	}
	return filepath.WalkDir(scratch, func(local string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if local == scratch {
			return nil
		}
		rel, err := filepath.Rel(scratch, local)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(rel, "\n") || !want[listedName(rel)] {
			return nil
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.Rename(local, target); err != nil {
			return err
		}
		if d.IsDir() {
			// Moved whole, with what it holds.
			return filepath.SkipDir
		}
		return nil
	})
}
