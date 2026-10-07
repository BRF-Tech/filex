package model

import "time"

// RecentSearch is one thing a person searched for in a search box that keeps
// its history on the server (migration 00090, task #168): the admin panel's
// search today. The list is per person and per surface, newest first, and
// the same words searched again are one row that moved to the top.
type RecentSearch struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// Surface is which search box kept it (RecentSearchSurfaceAdmin).
	Surface string `json:"-"`
	// Query is the words as typed, a prefix such as `file:` included.
	Query      string    `json:"query"`
	SearchedAt time.Time `json:"searched_at"`
}

// RecentSearchSurfaceAdmin is the admin panel's search (docs/ADMIN-PANEL.md
// → Search).
const RecentSearchSurfaceAdmin = "admin"

// RecentSearchKeep is how many searches a person's list keeps per surface;
// writing one more drops the oldest.
const RecentSearchKeep = 20

// RecentSearchMaxLen is the longest query kept, in characters. A longer one
// is refused rather than cut: a cut query is not what was searched for.
const RecentSearchMaxLen = 200
