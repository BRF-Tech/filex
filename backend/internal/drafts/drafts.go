// Package drafts holds what the draft feature (issue #71) keeps outside the
// HTTP layer: the per-person limit, and the key a draft is filed under.
//
// A draft is a new document before its first save. "New document" writes it
// into the chosen storage's drafts area — `.filex-drafts/<user id>/<key>/
// <name>` (internal/syspath.Drafts) — where every editor opens it like any
// other file, and the `drafts` table (migration 00064) remembers where it is
// meant to go. The rules of who may see and write it live with the other
// rules about filex's own paths: syspath (SealedFor, RefusedBy/OwnDraft) and
// acl.Set (a drafts area is its owner's alone). The endpoints are in
// internal/api/handlers/drafts.go; docs/API.md → Drafts describes them.
package drafts

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// Limit bounds, and the default the owner chose (issue #71): 50 drafts per
// person. A limit, not a quota — drafts are ordinary bytes on the storage and
// count against the person's quota like any other file; this caps how many
// unfinished documents one person leaves lying about, because every one of
// them is a file nobody else can see or clean up.
const (
	DefaultLimit = 50
	MinLimit     = 1
	MaxLimit     = 1000
)

// LimitSetting is how many live drafts one person may keep (the Protection
// page, beside the trash's retention). Creating one more is refused with
// DRAFT_LIMIT and the explorer points at the Drafts view — it never falls back
// to creating the file where the draft was meant to go.
//
// ⚠⚠ As with every setting in this family, FILEX_DRAFTS_LIMIT is a SEED, not
// an override: it is read on the first boot that finds no row, and is inert
// after that. The admin page is where the value lives.
var LimitSetting = dbsetting.IntSpec{
	Key:     "drafts.limit",
	EnvVar:  "FILEX_DRAFTS_LIMIT",
	Default: DefaultLimit,
	Min:     MinLimit,
	Max:     MaxLimit,
	Unit:    "drafts",
}

// SeedSettings consumes FILEX_DRAFTS_LIMIT into the settings table, first boot
// only. Call once at boot, before anything resolves the limit.
func SeedSettings(ctx context.Context, st dbsetting.Store) {
	dbsetting.SeedAll(ctx, st, LimitSetting)
}

// Limit is the limit in force right now. Read at the point of use, so a change
// on the Protection page applies to the next draft without a restart.
func Limit(ctx context.Context, g dbsetting.Getter) int {
	return LimitSetting.Resolve(ctx, g)
}

// NewKey mints a draft's key: 16 lowercase hex characters (8 random bytes),
// the `<key>` folder of its path and its address in the API. syspath's draft
// key pattern accepts exactly this shape.
func NewKey() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
