package handlers

// POST /api/files/manager/trash/same: whether trash entries the caller can see
// hold exactly the bytes of a file the caller has (task #259).
//
// The desktop's sync deletes a held file from the person's computer only when
// the server still holds an identical copy of it - in its trash, or in another
// folder - so "Delete from this computer" never loses the last copy of
// anything. A trashed file has no download route (it comes back through a
// restore), so the client sends the sha256 of its own file and the ids of the
// trash entries of that size it found in its listing, and the server says, per
// entry, whether the entry's bytes hash the same.
//
// ⚠ What it answers about is the caller's own trash listing and nothing more:
// an entry is judged by trashListKeep, the very rule GET /manager/trash
// filters by, so an entry this answers "same" about is one the person could
// restore and read anyway. An id the listing would not show - another
// account's deletion, a row with nothing in the trash, a folder - answers
// "not the same", exactly like an entry with other bytes: the answer says
// nothing about whether it exists. Nothing of an entry's content comes back,
// not even its hash.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/trash"
)

const (
	// trashSameMaxItems is how many entries one request may ask about.
	trashSameMaxItems = 50
	// trashSameMaxBytes is how much of the trash one request may read to
	// answer. An entry past it answers checked:false (not known), and the
	// client asks again in a request of its own, or keeps its file.
	trashSameMaxBytes = int64(2 << 30)
)

type trashSameItem struct {
	ID     int64  `json:"id"`
	SHA256 string `json:"sha256"`
}

type trashSameReq struct {
	Items []trashSameItem `json:"items"`
}

// trashSameAnswer is one entry's answer. Checked is false when its bytes were
// not read - the request's budget was spent, or its storage could not be read
// - and Same is then not known: a client treats that as "no copy".
type trashSameAnswer struct {
	ID      int64 `json:"id"`
	Same    bool  `json:"same"`
	Checked bool  `json:"checked"`
}

// Same answers POST /api/files/manager/trash/same.
func (h *Trash) Same(w http.ResponseWriter, r *http.Request) {
	var req trashSameReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	maxP := apierr.Params{"max": strconv.Itoa(trashSameMaxItems)}
	if len(req.Items) == 0 || len(req.Items) > trashSameMaxItems {
		writeError(w, r, http.StatusBadRequest, "trash_same_items", maxP, "max", trashSameMaxItems)
		return
	}
	for _, it := range req.Items {
		if !isSHA256Hex(it.SHA256) {
			writeError(w, r, http.StatusBadRequest, "trash_same_items", maxP, "max", trashSameMaxItems)
			return
		}
	}
	keep := h.trashListKeep(r)
	budget := trashSameMaxBytes
	out := make([]trashSameAnswer, 0, len(req.Items))
	for _, it := range req.Items {
		ans := trashSameAnswer{ID: it.ID}
		n := h.sameCandidate(r.Context(), it.ID, keep)
		switch {
		case n == nil:
			// Not one of the caller's trash entries, or not a file: not
			// the same, said like any other "not the same".
			ans.Checked = true
		case n.Size > budget:
			// Not read: the client asks again on its own.
		default:
			sum, read, err := h.trashSum(r.Context(), n, budget)
			budget -= read
			if err == nil {
				ans.Checked = true
				ans.Same = sum == it.SHA256
			}
		}
		out = append(out, ans)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// sameCandidate is the trashed FILE with that id when the caller's trash
// listing shows it, else nil.
func (h *Trash) sameCandidate(ctx context.Context, id int64, keep func(trash.TrashEntry) bool) *model.Node {
	if id <= 0 || h.Store == nil {
		return nil
	}
	n, err := h.Store.GetNode(ctx, id)
	if err != nil || n == nil || n.DeletedAt == nil || n.Type != model.NodeTypeFile {
		return nil
	}
	e := trash.TrashEntry{ID: n.ID, StorageID: n.StorageID, StorageName: h.storageName(ctx, n.StorageID), Name: n.Name, Size: n.Size}
	e.Path, _ = trash.OriginalPath(n)
	e.Vanished = trash.Vanished(n)
	if !keep(e) {
		return nil
	}
	return n
}

// errTrashSameBudget: the entry holds more than the request may still read
// (its catalogue size was out of date).
var errTrashSameBudget = errors.New("trash entry larger than the request's budget")

// trashSum is the sha256 of a trashed file's bytes, where the trash keeps
// them (the row's path is its trash key), and how many bytes it read.
func (h *Trash) trashSum(ctx context.Context, n *model.Node, budget int64) (string, int64, error) {
	if h.Service == nil || h.Service.Resolver == nil {
		return "", 0, errors.New("trash: no storage resolver")
	}
	drv, err := h.Service.Resolver(n.StorageID)
	if err != nil || drv == nil {
		return "", 0, errors.New("trash: storage unavailable")
	}
	rc, err := drv.Read(ctx, n.Path)
	if err != nil {
		return "", 0, err
	}
	defer rc.Close()
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(rc, budget+1))
	if err != nil {
		return "", read, err
	}
	if read > budget {
		return "", read, errTrashSameBudget
	}
	return hex.EncodeToString(hash.Sum(nil)), read, nil
}

// isSHA256Hex reports whether s is a sha256 written as 64 lower-case hex digits.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
