// Package perm is filex's per-user permission model: 29 named permissions,
// the presets built from them, and the resolver that turns an account's role,
// the install's defaults, the permission rules that match it and its own
// overrides into one answer per permission — with where that answer came
// from, so "why can't they do this?" has a reply.
//
// It is the complement to package acl, not a replacement. acl answers "how
// far may this user reach into this path" (folder grants, the viewer
// ceiling, app-plugin locks); perm answers "which actions may this user take
// at all". A file action needs both (acl.Set.Can, Phase 2).
//
// ⚠ Permissions are persisted BY NAME (overrides, rules, the defaults
// setting), never by bit position. The bit layout of Set is a runtime detail
// and may be reordered freely; a stored name that is no longer in the
// catalogue is ignored on read.
package perm

import (
	"math/bits"
	"path"
	"sort"
	"strings"
)

// Perm is one permission, identified by its stable key.
type Perm string

// File actions. Path-scopable; each maps to an operation every protocol has.
const (
	FilesDownload Perm = "files.download"
	FilesCreate   Perm = "files.create"
	FilesModify   Perm = "files.modify"
	FilesRename   Perm = "files.rename" // a new name in the same folder
	FilesMove     Perm = "files.move"   // to another folder
	FilesDelete   Perm = "files.delete" // to trash
	FilesPurge    Perm = "files.purge"  // permanent delete, empty trash
	// FilesEncrypt is making something end-to-end encrypted: a folder's
	// first .filex-e2e.json (a new encrypted folder, or the first step of
	// encrypting one in place) or a new .fxe. It is carved out of
	// files.create, which the same write needs as well.
	FilesEncrypt Perm = "files.encrypt"
	FilesTag     Perm = "files.tag"
)

// Sharing and features.
const (
	ShareLinks       Perm = "share.links"
	ShareUploadLinks Perm = "share.upload_links"
	ShareUsers       Perm = "share.users"
	CommentsWrite    Perm = "comments.write"
	AIUse            Perm = "ai.use"
	PluginsRun       Perm = "plugins.run"
)

// Access outside the web app.
const (
	AccessWebDAV  Perm = "access.webdav"
	AccessSFTP    Perm = "access.sftp"
	AccessFTP     Perm = "access.ftp"
	AccessS3      Perm = "access.s3"
	AccessNFS     Perm = "access.nfs"
	AccessAPI     Perm = "access.api"
	AccessDesktop Perm = "access.desktop"
)

// Own account.
const (
	AccountEdit Perm = "account.edit"
)

// Admin area.
const (
	AdminUsers   Perm = "admin.users"
	AdminGrants  Perm = "admin.grants"
	AdminShares  Perm = "admin.shares"
	AdminAudit   Perm = "admin.audit"
	AdminMonitor Perm = "admin.monitor"
	// AdminFull is the account role `admin`, surfaced as a permission so the
	// UI can show it. It is never granted by a rule or an override: storages,
	// settings, SSO, plugins and updates each amount to full control of the
	// server, so the only way to hold them is to BE an administrator.
	AdminFull Perm = "admin.full"
)

// Group is a UI grouping of the catalogue.
type Group string

const (
	GroupFiles   Group = "files"
	GroupSharing Group = "sharing"
	GroupAccess  Group = "access"
	GroupAccount Group = "account"
	GroupAdmin   Group = "admin"
)

// Def describes one catalogue entry.
type Def struct {
	Key   Perm
	Group Group
	// RoleOnly entries are held only through the account role; rules and
	// overrides naming them are ignored (and refused by Validate*).
	RoleOnly bool
	// ViewerCapped entries can never be allowed for a role=viewer account,
	// whatever a rule or override says — the same line acl.RoleCeiling draws
	// at LevelViewer for file mutation, extended to sharing (which needs
	// editor level today, see handlers/share.go).
	ViewerCapped bool
}

// catalogue is the ordered list of every permission. Order is UI order and
// bit order; neither is persisted.
var catalogue = []Def{
	{Key: FilesDownload, Group: GroupFiles},
	{Key: FilesCreate, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesModify, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesRename, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesMove, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesDelete, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesPurge, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesEncrypt, Group: GroupFiles, ViewerCapped: true},
	{Key: FilesTag, Group: GroupFiles},

	{Key: ShareLinks, Group: GroupSharing, ViewerCapped: true},
	{Key: ShareUploadLinks, Group: GroupSharing, ViewerCapped: true},
	{Key: ShareUsers, Group: GroupSharing, ViewerCapped: true},
	{Key: CommentsWrite, Group: GroupSharing},
	{Key: AIUse, Group: GroupSharing},
	{Key: PluginsRun, Group: GroupSharing},

	{Key: AccessWebDAV, Group: GroupAccess},
	{Key: AccessSFTP, Group: GroupAccess},
	{Key: AccessFTP, Group: GroupAccess},
	{Key: AccessS3, Group: GroupAccess},
	{Key: AccessNFS, Group: GroupAccess},
	{Key: AccessAPI, Group: GroupAccess},
	{Key: AccessDesktop, Group: GroupAccess},

	{Key: AccountEdit, Group: GroupAccount},

	{Key: AdminUsers, Group: GroupAdmin},
	{Key: AdminGrants, Group: GroupAdmin},
	{Key: AdminShares, Group: GroupAdmin},
	{Key: AdminAudit, Group: GroupAdmin},
	{Key: AdminMonitor, Group: GroupAdmin},
	{Key: AdminFull, Group: GroupAdmin, RoleOnly: true},
}

var index = func() map[Perm]int {
	if len(catalogue) > 64 {
		panic("perm: catalogue outgrew Set's uint64")
	}
	m := make(map[Perm]int, len(catalogue))
	for i, d := range catalogue {
		if _, dup := m[d.Key]; dup {
			panic("perm: duplicate key " + string(d.Key))
		}
		m[d.Key] = i
	}
	return m
}()

// All returns the catalogue in UI order. The slice is a copy.
func All() []Def { return append([]Def(nil), catalogue...) }

// Lookup returns the definition of k and whether it exists.
func Lookup(k Perm) (Def, bool) {
	i, ok := index[k]
	if !ok {
		return Def{}, false
	}
	return catalogue[i], true
}

// Known reports whether k is in the catalogue.
func Known(k Perm) bool { _, ok := index[k]; return ok }

// Set is a set of permissions as a bitset — the in-memory form only.
type Set uint64

// Of builds a Set; unknown keys are ignored.
func Of(ps ...Perm) Set {
	var s Set
	for _, p := range ps {
		s = s.With(p)
	}
	return s
}

// Has reports whether p is in s. Unknown p is never held.
func (s Set) Has(p Perm) bool {
	i, ok := index[p]
	return ok && s&(1<<uint(i)) != 0
}

// With returns s plus p (unchanged for unknown p).
func (s Set) With(p Perm) Set {
	if i, ok := index[p]; ok {
		return s | 1<<uint(i)
	}
	return s
}

// Without returns s minus p.
func (s Set) Without(p Perm) Set {
	if i, ok := index[p]; ok {
		return s &^ (1 << uint(i))
	}
	return s
}

// Len is the number of permissions in s.
func (s Set) Len() int { return bits.OnesCount64(uint64(s)) }

// Keys returns the members of s in catalogue order.
func (s Set) Keys() []Perm {
	out := make([]Perm, 0, s.Len())
	for i, d := range catalogue {
		if s&(1<<uint(i)) != 0 {
			out = append(out, d.Key)
		}
	}
	return out
}

// Strings is Keys as plain strings — what gets persisted.
func (s Set) Strings() []string {
	ks := s.Keys()
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}

// FromStrings parses persisted keys, dropping unknown ones (a permission
// removed from the catalogue must not fail a whole read).
func FromStrings(ss []string) Set {
	var s Set
	for _, k := range ss {
		s = s.With(Perm(k))
	}
	return s
}

// allSet is every catalogue entry.
var allSet = func() Set {
	var s Set
	for _, d := range catalogue {
		s = s.With(d.Key)
	}
	return s
}()

// filter returns the catalogue entries matching keep.
func filter(keep func(Def) bool) Set {
	var s Set
	for _, d := range catalogue {
		if keep(d) {
			s = s.With(d.Key)
		}
	}
	return s
}

// viewerCeiling is every permission a role=viewer account may hold.
var viewerCeiling = filter(func(d Def) bool { return !d.ViewerCapped && !d.RoleOnly })

// Preset is a named, fixed Set offered as a one-click starting point.
type Preset struct {
	Name string
	Set  Set
}

// Preset names.
const (
	PresetFullAdmin  = "full_admin"
	PresetStandard   = "standard"
	PresetReadOnly   = "read_only"
	PresetUploadOnly = "upload_only"
	PresetGuest      = "guest"
)

// Standard is everything outside the admin area: exactly what a role=user
// account could do before permissions existed. It is the fallback for the
// install's defaults, so an upgrade changes nobody's access.
var Standard = filter(func(d Def) bool { return d.Group != GroupAdmin })

// ReadOnly is what a role=viewer account could do before permissions
// existed: read, comment, personal tags, AI, apps, every protocol and token
// (read-only — acl caps the level), and its own profile. No file mutation, no
// sharing.
var ReadOnly = Standard & viewerCeiling

// UploadOnly can put files in — encrypted ones too, as it could before
// files.encrypt existed — and nothing else: a drop-box account.
var UploadOnly = Of(FilesCreate, FilesEncrypt, AccountEdit)

// Guest can look and download, and cannot even change its own profile — a
// shared or demo login.
var Guest = Of(FilesDownload)

var presets = []Preset{
	{Name: PresetFullAdmin, Set: allSet},
	{Name: PresetStandard, Set: Standard},
	{Name: PresetReadOnly, Set: ReadOnly},
	{Name: PresetUploadOnly, Set: UploadOnly},
	{Name: PresetGuest, Set: Guest},
}

// Presets returns the presets in UI order.
func Presets() []Preset { return append([]Preset(nil), presets...) }

// MatchPreset returns the name of the preset equal to s, or "" (custom).
func MatchPreset(s Set) string {
	for _, p := range presets {
		if p.Set == s {
			return p.Name
		}
	}
	return ""
}

// sortedStrings is a small helper for deterministic output in errors/tests.
func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RelocateNeeds is what taking a path from src to dst is: a new name in the
// same folder is files.rename, the same name in another folder (or on another
// storage) files.move, and both at once needs both. Paths are
// storage-relative. A path that does not change still counts as a rename, so
// every caller has something to check.
func RelocateNeeds(srcStorage int64, src string, dstStorage int64, dst string) []Perm {
	sDir, sName := splitRel(src)
	dDir, dName := splitRel(dst)
	var out []Perm
	if srcStorage != dstStorage || sDir != dDir {
		out = append(out, FilesMove)
	}
	if sName != dName || len(out) == 0 {
		out = append(out, FilesRename)
	}
	return out
}

func splitRel(rel string) (dir, name string) {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	return path.Dir("/" + rel), path.Base("/" + rel)
}
