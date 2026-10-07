// Package tokenperm is the catalogue of an API key's permissions that come
// in LEVELS: what a key may do with one kind of thing beside its files - read
// it, or read and write it. The first one is `comments` (task #157).
//
// A key's list (model.APIToken.Scopes) holds three kinds of entries:
//
//   - the verbs `read`, `write`, `delete`, `mcp`, `admin`
//     (auth/drivers/apitoken);
//   - at most one `root:<storage>://<path>` confinement (package confine);
//   - a permission at a level, `<key>:<level>` - `comments:rw`.
//
// # Every permission states its default (the maintainer, 2026-10-06)
//
// A key whose list does not name a permission holds that permission's
// DefaultLevel. That is how every key minted before a permission existed gets
// it, and how a new key gets it when whoever minted it chose nothing - with no
// migration and no rewrite of stored rows. The rule for the default:
//
//   - every permission defaults to Read;
//   - a super-administrator kind of permission (administration, tenants,
//     server settings) defaults to None: it is never handed to a key that did
//     not ask for it. Superadmin marks one.
//
// "bundan sonra eklenecek tüm permissionlar default read olarak konulur,
// superadmin tarzı yetkiler sistemde yoksa" - the maintainer's words. Each
// catalogue entry says its default explicitly, and tokenperm_test.go fails for
// an entry that does not, for a default that breaks the rule, and for a
// shipped default that changed (a changed default silently changes every key
// that never named the permission). docs/RBAC.md → "Permissions with a
// level" and docs/CONTRIBUTING.md → "Adding a permission" say it for people.
//
// ⚠ This package imports nothing of filex's own: the model, the auth chain
// and the token issuer all read it, and it must not pull any of them in.
package tokenperm

import (
	"fmt"
	"strings"
)

// Level is how far a key may go with one permission.
type Level string

const (
	// None: not at all. Only a super-administrator kind of permission
	// defaults to it.
	None Level = "none"
	// Read: see it.
	Read Level = "read"
	// ReadWrite: see it, add to it and remove what one may remove. Written
	// `rw` in a key's list; `write` is read as the same level.
	ReadWrite Level = "rw"
)

// rank orders the levels; an unknown level ranks with None.
func (l Level) rank() int {
	switch l {
	case Read:
		return 1
	case ReadWrite:
		return 2
	}
	return 0
}

// Covers reports whether a key at level l may do what need asks.
func (l Level) Covers(need Level) bool { return l.rank() >= need.rank() }

// Def is one permission of the catalogue.
type Def struct {
	// Key names the permission in a key's list (`comments` → `comments:rw`)
	// and in the answers that list a key's levels.
	Key string
	// Levels are the levels a key may be given, lowest first.
	Levels []Level
	// DefaultLevel is the level of every key whose list does not name the
	// permission - the keys that existed before it was added among them.
	// ⚠ Required, and fixed once shipped: Read, or None for a Superadmin
	// permission (the package comment; tokenperm_test.go).
	DefaultLevel Level
	// Superadmin marks a permission that amounts to administering the server
	// or a tenant. It defaults to None.
	Superadmin bool
}

// has reports whether l is one of d's levels.
func (d Def) has(l Level) bool {
	for _, x := range d.Levels {
		if x == l {
			return true
		}
	}
	return false
}

// Comments is reading a file's or a folder's comments (Read) and adding and
// deleting them (ReadWrite) - on /api/files/comments, /api/ai/comments and the
// MCP file_comment_* tools alike (auth.CommentsRead, auth.CommentsWrite).
const Comments = "comments"

// catalogue is every permission, in the order the answers list them.
var catalogue = []Def{
	{Key: Comments, Levels: []Level{Read, ReadWrite}, DefaultLevel: Read},
}

// All returns the catalogue. The slice is a copy.
func All() []Def { return append([]Def(nil), catalogue...) }

// Lookup returns the permission named key.
func Lookup(key string) (Def, bool) {
	for _, d := range catalogue {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// Need is what one action asks of a key: a permission at a level.
type Need struct {
	Key   string
	Level Level
}

// String is how a refusal names the need: `comments:read`, `comments:write`.
func (n Need) String() string {
	switch n.Level {
	case ReadWrite:
		return n.Key + ":write"
	case Read:
		return n.Key + ":read"
	}
	return n.Key
}

// ParseLevel reads a level as a request or a stored list spells it. `write`
// is the same level as `rw`: a refusal says `comments:write`, and a client
// that adds exactly what the refusal named gets what it asked for.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return None, true
	case "read":
		return Read, true
	case "rw", "write":
		return ReadWrite, true
	}
	return "", false
}

// LevelError is a permission entry with a level its permission does not have.
type LevelError struct {
	Key   string
	Level string
}

func (e *LevelError) Error() string {
	d, _ := Lookup(e.Key)
	valid := make([]string, 0, len(d.Levels))
	for _, l := range d.Levels {
		valid = append(valid, string(l))
	}
	return fmt.Sprintf("permission %q has no level %q (valid: %s)", e.Key, e.Level, strings.Join(valid, ", "))
}

// ParseEntry reads one entry of a key's list. isPerm is false for an entry
// that is not a permission of this catalogue (a verb, a `root:`, anything
// else - the caller judges those); err is a *LevelError for an entry that
// names a permission with a level it does not have.
func ParseEntry(entry string) (key string, level Level, isPerm bool, err error) {
	entry = strings.TrimSpace(entry)
	i := strings.IndexByte(entry, ':')
	if i <= 0 {
		return "", "", false, nil
	}
	d, ok := Lookup(entry[:i])
	if !ok {
		return "", "", false, nil
	}
	raw := entry[i+1:]
	l, ok := ParseLevel(raw)
	if !ok || !d.has(l) {
		return d.Key, "", true, &LevelError{Key: d.Key, Level: raw}
	}
	return d.Key, l, true, nil
}

// named is the levels scopes names explicitly - the entries this version can
// read. A permission named twice holds the lower level.
func named(scopes string) map[string]Level {
	out := map[string]Level{}
	for _, e := range strings.Split(scopes, ",") {
		key, l, isPerm, err := ParseEntry(e)
		if !isPerm || err != nil {
			continue
		}
		if prev, seen := out[key]; seen && prev.rank() <= l.rank() {
			continue
		}
		out[key] = l
	}
	return out
}

// LevelIn is the level a stored list gives the permission key: the level it
// names, else the permission's default.
//
// ⚠ An EMPTY list grants nothing (model.APIToken.HasScope), so it holds every
// permission at None, and so does a key the catalogue does not know. An entry
// this version cannot read - a level the permission does not have, say one a
// later version added - is passed over and the default holds.
func LevelIn(scopes, key string) Level {
	d, ok := Lookup(key)
	if !ok || strings.TrimSpace(scopes) == "" {
		return None
	}
	if l, ok := named(scopes)[key]; ok {
		return l
	}
	return d.DefaultLevel
}

// LevelsIn is LevelIn for every permission of the catalogue: what the token
// lists answer beside `scopes` (`"permissions": {"comments": "read"}`), so a
// screen draws a key's levels without a copy of the default rule.
func LevelsIn(scopes string) map[string]Level {
	out := make(map[string]Level, len(catalogue))
	for _, d := range catalogue {
		out[d.Key] = LevelIn(scopes, d.Key)
	}
	return out
}

// Canonical is the entries a list stores for levels: one `<key>:<level>` per
// permission whose level is not its default, in catalogue order. A permission
// at its default is not written - it reads the same, and a key minted before
// the permission existed then looks exactly like one minted after it.
func Canonical(levels map[string]Level) []string {
	var out []string
	for _, d := range catalogue {
		l, ok := levels[d.Key]
		if !ok || l == d.DefaultLevel || !d.has(l) {
			continue
		}
		out = append(out, d.Key+":"+string(l))
	}
	return out
}

// Replace returns scopes with the levels of changes set: every other entry -
// the verbs, the `root:` - stays where it was, the permissions changes does
// not name keep their level, and the permission entries are written again in
// their canonical form at the end. An entry this version cannot read is
// dropped with them (Replace writes what it can read back).
func Replace(scopes string, changes map[string]Level) string {
	levels := map[string]Level{}
	for _, d := range catalogue {
		levels[d.Key] = d.DefaultLevel
	}
	for k, l := range named(scopes) {
		levels[k] = l
	}
	for k, l := range changes {
		if _, ok := Lookup(k); ok {
			levels[k] = l
		}
	}
	var keep []string
	for _, e := range strings.Split(scopes, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, _, isPerm, _ := ParseEntry(e); isPerm {
			continue
		}
		keep = append(keep, e)
	}
	return strings.Join(append(keep, Canonical(levels)...), ",")
}
