package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// ── `filex sync discard`: does the server still have this file? ──────────
//
// serverCopies answers filesync.CopyFinder (task #259): a held file is
// deleted from this computer only when the server holds an identical copy
// (sha256) that the person can reach. Two places, with the endpoints the
// server already has plus the narrowest new one:
//
//  1. The person's trash. GET /api/files/manager/trash, read once per
//     discard, gives the entries of the file's size (its own name first); a
//     trashed file has no download route, so POST
//     /api/files/manager/trash/same compares the file's sha256 with each
//     entry's bytes on the server and answers "same" or "not" - for entries
//     the person's own trash listing shows, and nothing more.
//  2. The pair's storage. GET /api/files/search by the file's name, its exact
//     size, under the pair's storage; each hit is downloaded and hashed here.
//     A server with search off cannot look there: a file the trash does not
//     have then stays, "the server could not be asked".
//
// Anything that cannot be said for sure keeps the file: an error from either
// place, a trash too large to read whole, a hit too large for the budget.
type serverCopies struct {
	api     copyAPI
	storage string // the pair's adapter ("docs")

	loaded   bool
	bySize   map[int64][]cliclient.TrashEntry
	trashErr error
	// downloaded is how much the search hits have cost so far (copyBudget).
	downloaded int64
}

// copyAPI is the slice of the API client serverCopies needs.
type copyAPI interface {
	Trash(ctx context.Context, storageID int64, limit, offset int) (*cliclient.TrashPage, error)
	TrashSame(ctx context.Context, items []cliclient.TrashSameItem) ([]cliclient.TrashSameAnswer, error)
	SearchFiltered(ctx context.Context, query, scope string, storageID int64, limit int, filters url.Values) (*cliclient.SearchResult, error)
	DownloadSized(ctx context.Context, remote string, w io.Writer, want int64) (int64, error)
}

const (
	// copyTrashPages is how many pages of the trash (500 entries each) a
	// discard reads. A larger trash is not searched whole, and a file it
	// might hold stays.
	copyTrashPages = 40
	// copyTrashAsk is how many trash entries of a file's size are asked about.
	copyTrashAsk = 8
	// copyHits is how many search hits of a file's name and size are
	// downloaded and compared.
	copyHits = 4
	// copyBudget is how much one discard downloads to compare, in bytes.
	copyBudget = int64(8 << 30)
)

var errTrashTooLarge = errors.New("the trash holds more entries than one discard reads")

func newServerCopies(api copyAPI, pairRemote string) *serverCopies {
	storage := pairRemote
	if i := strings.Index(storage, "://"); i >= 0 {
		storage = storage[:i]
	}
	return &serverCopies{api: api, storage: storage}
}

// FindCopy implements filesync.CopyFinder.
func (s *serverCopies) FindCopy(ctx context.Context, c filesync.CopyCheck) (string, error) {
	where, trashErr := s.inTrash(ctx, c)
	if where != "" {
		return where, nil
	}
	where, searchErr := s.inStorage(ctx, c)
	if where != "" {
		return where, nil
	}
	// Found nowhere: "no copy" only when both places could be searched.
	if trashErr != nil {
		return "", fmt.Errorf("the server's trash could not be searched: %w", trashErr)
	}
	if searchErr != nil {
		return "", fmt.Errorf("the server could not search %s:// for it: %w", s.storage, searchErr)
	}
	return "", nil
}

// loadTrash reads the person's trash once, keyed by size.
func (s *serverCopies) loadTrash(ctx context.Context) {
	if s.loaded {
		return
	}
	s.loaded = true
	s.bySize = map[int64][]cliclient.TrashEntry{}
	const page = 500
	for i := 0; i < copyTrashPages; i++ {
		p, err := s.api.Trash(ctx, 0, page, i*page)
		if err != nil {
			s.trashErr = err
			return
		}
		for _, e := range p.Entries {
			s.bySize[e.Size] = append(s.bySize[e.Size], e)
		}
		if len(p.Entries) < page || (i+1)*page >= p.Total {
			return
		}
	}
	s.trashErr = errTrashTooLarge
}

// inTrash is the trash entry holding the same bytes, as the person reads it.
func (s *serverCopies) inTrash(ctx context.Context, c filesync.CopyCheck) (string, error) {
	s.loadTrash(ctx)
	cands := append([]cliclient.TrashEntry(nil), s.bySize[c.Size]...)
	// Its own name first: a stale copy of a file the server cleaned up is
	// in the trash under the name it had.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Name == c.Name && cands[j].Name != c.Name })
	if len(cands) > copyTrashAsk {
		cands = cands[:copyTrashAsk]
	}
	if len(cands) == 0 {
		return "", s.trashErr
	}
	items := make([]cliclient.TrashSameItem, len(cands))
	for i, e := range cands {
		items[i] = cliclient.TrashSameItem{ID: e.ID, SHA256: c.SHA256}
	}
	ans, err := s.api.TrashSame(ctx, items)
	if err != nil {
		return "", err
	}
	unchecked := false
	for _, a := range ans {
		if !a.Checked {
			unchecked = true
			continue
		}
		if !a.Same {
			continue
		}
		for _, e := range cands {
			if e.ID == a.ID {
				return "trash: " + e.Location(), nil
			}
		}
	}
	if unchecked {
		return "", errors.New("the server did not compare every trash entry of its size")
	}
	return "", s.trashErr
}

// inStorage is a live file of the pair's storage holding the same bytes.
func (s *serverCopies) inStorage(ctx context.Context, c filesync.CopyCheck) (string, error) {
	if s.storage == "" {
		return "", errors.New("the pair names no storage")
	}
	size := strconv.FormatInt(c.Size, 10)
	res, err := s.api.SearchFiltered(ctx, c.Name, "name", 0, 50, url.Values{
		"type": {"file"}, "min_size": {size}, "max_size": {size}, "under": {s.storage + "://"},
	})
	if err != nil {
		return "", err
	}
	tried := 0
	for _, hit := range res.Results {
		if hit.Type != "file" || hit.Name != c.Name || hit.Size != c.Size {
			continue
		}
		if tried == copyHits {
			break
		}
		tried++
		if s.downloaded+c.Size > copyBudget {
			return "", errors.New("too large to compare in one discard")
		}
		remote := cliclient.RemotePath{Adapter: s.storage, Rel: strings.Trim(hit.Path, "/")}.String()
		h := sha256.New()
		n, err := s.api.DownloadSized(ctx, remote, h, c.Size)
		s.downloaded += n
		if err != nil {
			return "", err
		}
		if hex.EncodeToString(h.Sum(nil)) == c.SHA256 {
			return remote, nil
		}
	}
	return "", nil
}
