package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// App-plugin badges on listings.
//
// Two facts a row needs so the explorer can behave without a round trip per
// file: `locked` (+ `lock` {plugin, reason, until}) when an app holds the
// file read-only, and `app_state` — the `<plugin>:<key>` state keys apps
// keep on it — so the context menu can offer state-aware actions
// (`applies.state` / `applies.no_state`: "Sign" only where a signature is
// pending, "Request signatures" only where none is). One locks query and one
// state query per listing; nothing per row.

type appBadge struct {
	Lock  *model.AppPluginLock
	State []string
}

// appBadgesFor computes the badges for rels on one storage. Both maps may
// be empty; a store error yields no badges rather than no listing.
func appBadgesFor(ctx context.Context, store db.Store, storageID int64, rels []string) map[string]appBadge {
	out := map[string]appBadge{}
	if store == nil || storageID <= 0 || len(rels) == 0 {
		return out
	}
	hashes := make([]string, 0, len(rels))
	byHash := make(map[string]string, len(rels))
	for _, rel := range rels {
		rel = strings.Trim(rel, "/")
		h := pathkey.Hash(storageID, "/"+rel)
		hashes = append(hashes, h)
		byHash[h] = rel
	}
	if locks, err := store.ListAppPluginLocks(ctx, storageID); err == nil {
		now := time.Now()
		for _, l := range locks {
			if !l.Live(now) {
				continue
			}
			if rel, ok := byHash[l.PathHash]; ok {
				b := out[rel]
				b.Lock = l
				out[rel] = b
			}
		}
	}
	if states, err := store.ListAppPluginStateKeys(ctx, storageID, hashes); err == nil {
		// ⚠ A personal key (`todo@7`) is shown to its person as `todo@me`
		// and to nobody else: the menu offers "Sign / Fill" only to the
		// people who have something to sign, and a listing never says who
		// else has.
		var me int64
		if u := auth.UserFrom(ctx); u != nil {
			me = u.ID
		}
		for h, keys := range states {
			if rel, ok := byHash[h]; ok {
				if keys = wasmplugin.PersonalStateKeys(keys, me); len(keys) == 0 {
					continue
				}
				b := out[rel]
				b.State = keys
				out[rel] = b
			}
		}
	}
	return out
}

// annotateAppBadges stamps `locked`/`lock`/`app_state` onto listing entries
// (the map-shaped rows of the folder listing and search). Entries name
// their file by an adapter-qualified `path`.
func annotateAppBadges(ctx context.Context, store db.Store, storageID int64, entries []map[string]any) {
	if len(entries) == 0 {
		return
	}
	rels := make([]string, 0, len(entries))
	for _, e := range entries {
		p, _ := e["path"].(string)
		_, rel := splitAdapterPath(p)
		rels = append(rels, strings.Trim(rel, "/"))
	}
	badges := appBadgesFor(ctx, store, storageID, rels)
	if len(badges) == 0 {
		return
	}
	for i, e := range entries {
		b, ok := badges[rels[i]]
		if !ok {
			continue
		}
		if b.Lock != nil {
			e["locked"] = true
			e["lock"] = lockView(b.Lock)
			// A locked file is read-only for everyone; say so in the same
			// field the UI already reads for storage-level read-only.
			if lv, _ := e["perm"].(string); lv == "editor" || lv == "owner" {
				e["perm"] = "viewer"
			}
		}
		if len(b.State) > 0 {
			e["app_state"] = b.State
		}
	}
}

// lockView is the client-facing shape of a lock.
//
// ⚠ `plugin` is the app's manifest NAME, which is what addresses it, and
// `plugin_label` is what a person should be shown instead (lockAppLabel):
// the banner in the details panel read "sign locked this file" where every
// other screen says "e-Signature" (v0.43.0). The label is omitted when
// nothing can resolve it, and the client then falls back to the name.
func lockView(l *model.AppPluginLock) map[string]any {
	v := map[string]any{"plugin": l.PluginName}
	if label := lockAppLabel(l.PluginName); len(label) > 0 {
		v["plugin_label"] = label
	}
	plain, text := lockReasonOf(l)
	if plain != "" {
		v["reason"] = plain
	}
	if len(text) > 0 {
		v["reason_text"] = text
	}
	if l.Until != nil {
		v["until"] = l.Until
	}
	return v
}
