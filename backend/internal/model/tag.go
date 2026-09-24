package model

import "time"

// Tag kinds (migration 00055). See docs/SEARCH.md → "Tags" for the product
// rules; the short form is that a PERSONAL tag is one person's own label, like
// a star, and a TEAM tag is shared with everyone in the tenant who can see the
// file.
//
// ⚠ Before v0.43.0 there was one kind and it was shared by EVERY user on the
// instance, across tenants, although the code called it per-user (tester,
// 2026-09-22: a non-admin's "müşteri teklifi" showed in another user's and the
// admin's sidebar, and the other user could remove it). The upgrade turns those
// tags into TEAM tags, so nothing anybody could see disappears.
const (
	TagPersonal = "personal"
	TagTeam     = "team"
)

// ValidTagKind reports whether k is one of the two kinds.
func ValidTagKind(k string) bool { return k == TagPersonal || k == TagTeam }

// Tag is one entry of a tag VOCABULARY — a person's (personal) or a tenant's
// (team). The files carrying it are linked through node_tags, so a tag is
// named once and renaming or re-casing it is one row, not one per file.
//
// Exactly one of OwnerID / TenantID is set, and that is what makes it
// personal or team:
//
//   - personal: OwnerID = the person, TenantID = nil.
//   - team:     OwnerID = nil, TenantID = the tenant (provider id), or 0 for
//     "the instance" — a single-tenant install, or a storage linked to no
//     tenant. A tenant-0 team tag follows the file: every tenant that can see
//     the file sees it (see handlers/tags.go, tagVisible).
//
// ⚠ The two nullable columns are how uniqueness is expressed on all three
// engines: UNIQUE(owner_id, name_key) and UNIQUE(tenant_id, name_key), and a
// NULL never collides in a UNIQUE index — so a personal row is only unique
// among that person's tags and a team row only among that tenant's. MySQL has
// no partial index to say it any other way.
type Tag struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	TenantID *int64 `json:"-"`
	OwnerID  *int64 `json:"-"`
	// Name is the display form, case preserved (tagname.Clean).
	Name string `json:"name"`
	// Key is the identity (tagname.Key) — never shown.
	Key       string    `json:"-"`
	CreatedAt time.Time `json:"-"`
}

// TagQuery selects part of the vocabulary. Both halves are optional and are
// ORed: a caller's visible vocabulary is "my personal tags + my tenant's team
// tags".
type TagQuery struct {
	// OwnerID > 0 includes that person's personal tags.
	OwnerID int64
	// Team includes team tags; TeamTenants, when non-nil, restricts them to
	// those tenant ids (0 included explicitly when wanted). nil = every tenant
	// — only ever for an unconfined caller (single-tenant mode, supertenant).
	Team        bool
	TeamTenants []int64
}

// TagPlacement is one (tag, live file) pair with what visibility needs to
// judge it: the storage and the path. The tag listings are built from these so
// that a tag is only named to someone who can see at least one file carrying
// it — a tag NAME is information too.
type TagPlacement struct {
	TagID     int64
	NodeID    int64
	StorageID int64
	Path      string
}
