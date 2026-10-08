package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// hydrateLinkStates gives every link row of a listing the reason the sync last
// recorded for it (model.Node.LinkState, migration 00098): outside_root,
// broken or unresolved. projectFileNodes sends it as `link_state`, so the
// explorer's badge names the reason - "Outside storage" - where it used to
// say the general "Link" for every listing the catalogue answered, which after
// a storage's first sync is all of them (packages/core lib/symlink).
//
// It sits beside hydrateThumbs at every listing that projects catalogue rows:
// the folder listing, the merged lazy listing, search, the Recent / Starred /
// tag rows and "shared with me". One query for the whole listing, and only
// when it holds a link at all.
//
// Nil-safe. A failed read leaves the rows without a reason, which is the
// general "Link" - what the row said before the catalogue kept one.
func hydrateLinkStates(ctx context.Context, store db.Store, nodes []*model.Node) {
	if store == nil || len(nodes) == 0 {
		return
	}
	var ids []int64
	for _, n := range nodes {
		if n != nil && n.Type == model.NodeTypeSymlink && n.ID > 0 {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	states, err := store.NodeLinkStates(ctx, ids)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n != nil && n.Type == model.NodeTypeSymlink {
			n.LinkState = states[n.ID]
		}
	}
}
