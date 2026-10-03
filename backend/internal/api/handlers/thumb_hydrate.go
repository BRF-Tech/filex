package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// ThumbRefresher is what a listing hands a missing or stale thumbnail to
// (*thumb.Refresher). Consider must never block: it runs inside the listing.
type ThumbRefresher interface {
	Consider(n *model.Node, t *model.Thumbnail) thumb.Verdict
}

// hydrateThumbs is the ONE way a listing learns its files' thumbnails: every
// folder listing, the merged lazy listing, search, recent/starred/tags and
// "shared with me" come through here.
//
//   - One batched query for the whole listing. Each path used to ask once per
//     file, the N+1 the comment beside it promised to fix "if profiles ever
//     flag it".
//   - Every file's row is handed to the refresher, which draws in the
//     background what is missing or stale (thumb.Assess: the catalogue row the
//     listing just read against the thumbnail row next to it, nothing else).
//     This is how a file changed outside filex, or one that never had a
//     picture, gets one without anybody running a command (issue #79).
//
// Nil-safe in store and refresher. A failed read leaves n.Thumb nil, which is
// "no picture" in the listing and nothing queued: without the rows there is
// nothing to judge freshness by, and guessing "missing" would queue every
// file of the folder on a database hiccup.
func hydrateThumbs(ctx context.Context, store db.Store, refresh ThumbRefresher, nodes []*model.Node) {
	if store == nil || len(nodes) == 0 {
		return
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.Type == model.NodeTypeFile && n.ID > 0 {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := store.GetThumbnails(ctx, ids)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n == nil || n.Type != model.NodeTypeFile || n.ID <= 0 {
			continue
		}
		n.Thumb = rows[n.ID]
		// filex's own folders (version snapshots, the open-with working
		// area) are never shown, so they are never drawn either: the same
		// syspath rule the listing projector and the repair walk apply.
		if refresh != nil && !syspath.Hidden(n.Path) {
			refresh.Consider(n, n.Thumb)
		}
	}
}

// FolderPreviewSwitch reports whether folders show their pictures
// (*thumb.Pipeline: thumb.FolderPreviewsSetting, cached).
type FolderPreviewSwitch interface{ FolderPreviews() bool }

// folderPreviewsOn: the switch is on, or not wired (tests, an embed
// without settings).
func (h *Manager) folderPreviewsOn() bool {
	return h.FolderPreviews == nil || h.FolderPreviews.FolderPreviews()
}

// What a folder shows of itself: the folderPreviewCount files that came into
// it last, drawn inside the folder (docs/thumbnails.md, Folder previews).
const (
	folderPreviewCount = 3
	// previewCandidates is how many of a folder's newest files are read to
	// find folderPreviewCount the caller may see.
	previewCandidates = folderPreviewCount + 3
)

// folderPreview is one file a folder shows, as the listing sends it: its
// name, and its thumbnail when one is ready (else the views draw the file's
// type icon in its place).
type folderPreview struct {
	Name     string `json:"name"`
	ThumbURL string `json:"thumb_url,omitempty"`
}

// annotateFolderPreviews adds `preview` to the folder rows of a projected
// listing: the files that came into each folder last, newest first.
//
//   - "Last" is the later of when the file entered the catalogue and its own
//     modification time. Only the folder's own files: its subfolders are not
//     looked into, and a folder that holds only folders shows none.
//   - ONE catalogue query for all the folders of the listing
//     (Store.ListPreviewCandidates) and one for their thumbnail rows.
//   - A file with a thumbnail that is ready carries its URL; one without is
//     still shown, as its type icon. Read at listing time from the children's
//     own thumbnails, so it cannot go stale on its own.
//   - A child the caller cannot see (set) or one of filex's own files is
//     never shown, and an encrypted file's thumbnail is never ready.
//   - A shown file with no thumbnail yet is handed to the refresher, so a
//     folder nobody opened gets its pictures without anybody opening it.
func annotateFolderPreviews(ctx context.Context, store db.Store, refresh ThumbRefresher, signer *thumb.Signer,
	storageID int64, nodes []*model.Node, set *acl.Set, files []map[string]any) {
	if store == nil || len(files) == 0 {
		return
	}
	var dirIDs []int64
	for _, n := range nodes {
		if n != nil && n.Type == model.NodeTypeDirectory && n.ID > 0 && n.DeletedAt == nil && !syspath.Hidden(n.Path) {
			dirIDs = append(dirIDs, n.ID)
		}
	}
	if len(dirIDs) == 0 {
		return
	}
	cands, err := store.ListPreviewCandidates(ctx, storageID, dirIDs, previewCandidates)
	if err != nil || len(cands) == 0 {
		return
	}
	var ids []int64
	for _, kids := range cands {
		for _, c := range kids {
			ids = append(ids, c.ID)
		}
	}
	rows, err := store.GetThumbnails(ctx, ids)
	if err != nil {
		return
	}
	previews := map[int64][]folderPreview{}
	for dirID, kids := range cands {
		for _, c := range kids {
			if syspath.Hidden(c.Path) || (set != nil && !set.CanSee(acl.CleanRel(c.Path))) {
				continue
			}
			if len(previews[dirID]) >= folderPreviewCount {
				break
			}
			row := rows[c.ID]
			if refresh != nil {
				refresh.Consider(c, row)
			}
			p := folderPreview{Name: c.Name}
			if thumbServable(row) {
				p.ThumbURL = thumbURL(signer, c.ID, row)
			}
			previews[dirID] = append(previews[dirID], p)
		}
	}
	for _, f := range files {
		if f["type"] != "dir" {
			continue
		}
		id, _ := f["id"].(int64)
		if p := previews[id]; len(p) > 0 {
			f["preview"] = p
		}
	}
}
