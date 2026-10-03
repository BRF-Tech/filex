package perm

import (
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A role's "different in some folders" part (model.PermRuleConditions,
// migration 00069): Allow/Deny for some permissions on some storages and
// paths, over the role's own list.
//
// Only permissions that are checked against a path can differ by folder —
// access.sftp or admin.users has no path to be "under /Archive".
// NormalizeRule refuses the rest rather than store a part that would
// silently do nothing. The role's limits (settings) stay account-wide.

// conditionable is every permission a conditioned rule may change.
var conditionable = Of(FilesDownload, FilesCreate, FilesModify, FilesRename, FilesMove, FilesDelete, FilesPurge, FilesEncrypt, ShareLinks, ShareUploadLinks)

// Conditionable reports whether p may be changed by a rule with conditions.
func Conditionable(p Perm) bool { return conditionable.Has(p) }

// conditionsMatch reports whether an action on (storageID, rel) is inside c.
func conditionsMatch(c model.PermRuleConditions, storageID int64, rel string) bool {
	if len(c.StorageIDs) > 0 {
		found := false
		for _, id := range c.StorageIDs {
			if id == storageID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(c.Paths) == 0 {
		return true
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	for _, p := range c.Paths {
		if pathPatternMatch(p, rel) {
			return true
		}
	}
	return false
}

// pathPatternMatch reports whether rel is at or under a path the pattern
// names. Segments are matched with path.Match ("*", "?", "[…]"); a "**"
// segment matches any number of segments, including none. A pattern names a
// folder AND everything in it, like a grant: "Archive" covers
// "Archive/2024/q1.pdf". So "*.psd" is a .psd at the top level (or anything
// inside a folder named like one); "**/*.psd" is a .psd anywhere.
func pathPatternMatch(pattern, rel string) bool {
	pat := splitSegs(pattern)
	segs := splitSegs(rel)
	// Try rel and each of its ancestors: a match on an ancestor covers rel.
	for n := len(segs); n >= 0; n-- {
		if matchSegs(pat, segs[:n]) {
			return true
		}
	}
	return false
}

func splitSegs(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], segs[1:])
}

// normalizeConditions validates and canonicalizes a rule's conditions.
func normalizeConditions(r *model.PermissionRule) error {
	c := &r.Conditions
	var ids []int64
	seen := map[int64]bool{}
	for _, id := range c.StorageIDs {
		if id <= 0 {
			return invalid("storage id %d is not a storage", id)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	c.StorageIDs = ids
	var paths []string
	for _, p := range c.Paths {
		p = strings.Trim(strings.TrimSpace(strings.ReplaceAll(p, `\`, "/")), "/")
		if p == "" {
			continue
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return invalid("path pattern %q has an empty, . or .. segment", p)
			}
			if seg == "**" {
				continue
			}
			if _, err := path.Match(seg, ""); err != nil {
				return invalid("path pattern %q is not a valid pattern", p)
			}
		}
		if !containsString(paths, p) {
			paths = append(paths, p)
		}
	}
	c.Paths = paths
	if c.Empty() {
		return nil
	}
	for k := range r.Effects {
		if !Conditionable(Perm(k)) {
			return invalid("%q cannot differ by folder; only file actions and links can", k)
		}
	}
	return nil
}
