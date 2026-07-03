// Package search wraps the embedded Bleve index used for fast filename
// (and optionally content) lookup across all storages.
//
// The index lives at {data_dir}/search.bleve. It is opened lazily on
// first IndexNode/Search call. If Bleve cannot open or create the index,
// Search degrades to nil (callers should fall back to SQL LIKE).
package search

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Index is the search facade.
type Index struct {
	mu    sync.RWMutex
	bleve bleve.Index
	path  string
}

// Open returns an Index ready to use. Pass empty path to disable.
func Open(path string) (*Index, error) {
	if path == "" {
		return &Index{}, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		mapping := bleve.NewIndexMapping()
		bx, err := bleve.New(path, mapping)
		if err != nil {
			return nil, err
		}
		return &Index{bleve: bx, path: path}, nil
	}
	bx, err := bleve.Open(path)
	if err != nil {
		return nil, err
	}
	return &Index{bleve: bx, path: path}, nil
}

// Close releases the index.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil
	}
	return i.bleve.Close()
}

// docFromNode extracts indexable fields from a Node.
type doc struct {
	StorageID int64  `json:"storage_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Mime      string `json:"mime,omitempty"`
	Type      string `json:"type"`
}

// IndexNode adds or updates a node entry.
func (i *Index) IndexNode(_ context.Context, n *model.Node) error {
	i.mu.RLock()
	bx := i.bleve
	i.mu.RUnlock()
	if bx == nil {
		return nil
	}
	d := doc{
		StorageID: n.StorageID,
		Name:      n.Name,
		Path:      n.Path,
		Mime:      n.Mime,
		Type:      string(n.Type),
	}
	return bx.Index(strconv.FormatInt(n.ID, 10), d)
}

// DeleteNode removes a node from the index.
func (i *Index) DeleteNode(_ context.Context, id int64) error {
	i.mu.RLock()
	bx := i.bleve
	i.mu.RUnlock()
	if bx == nil {
		return nil
	}
	return bx.Delete(strconv.FormatInt(id, 10))
}

// Hit is a single search result.
type Hit struct {
	NodeID  int64
	Score   float64
	Snippet string
}

// Search returns top-N matches for the query string.
//
// Falls back to nil result + nil error when index is disabled — callers
// should treat that as "no search engine, do a SQL LIKE instead".
//
// Default mapping uses the standard analyzer, which tokenises filenames
// like "square.jpg" as a single token because the dot isn't a word
// boundary. To make partial matches like "squ" or "jpg" find rows we
// run TWO queries together via a disjunction:
//
//   - Match (name): exact-token hits, ranks well for full filenames
//     ("square.jpg") and word-prefix hits when there's a delimiter.
//   - Wildcard (name): catches mid-string substrings like "squ" → finds
//     "square.jpg" because the wildcard is anchored on both sides.
//
// Either query alone produced gaps in browser smoke (Match misses
// substrings; Wildcard misses tokenised matches when the user types the
// full name). Disjunction is fast at the index sizes we care about.
func (i *Index) Search(_ context.Context, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 50
	}
	i.mu.RLock()
	bx := i.bleve
	i.mu.RUnlock()
	if bx == nil || query == "" {
		return nil, nil
	}

	// Lower-case for the wildcard side: Bleve stores tokens lower-cased
	// by default but wildcard queries are NOT analysed, so an upper-case
	// term in the user's input would miss every row.
	wcTerm := "*" + strings.ToLower(query) + "*"

	matchQ := bleve.NewMatchQuery(query)
	matchQ.SetField("name")

	wildQ := bleve.NewWildcardQuery(wcTerm)
	wildQ.SetField("name")

	pathQ := bleve.NewWildcardQuery(wcTerm)
	pathQ.SetField("path")

	disj := bleve.NewDisjunctionQuery(matchQ, wildQ, pathQ)

	req := bleve.NewSearchRequest(disj)
	req.Size = limit
	res, err := bx.Search(req)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		id, _ := strconv.ParseInt(h.ID, 10, 64)
		out = append(out, Hit{NodeID: id, Score: h.Score})
	}
	return out, nil
}

// SafeSearch wraps Search and logs errors instead of bubbling them.
// Useful in handlers that want a "best effort" search.
func (i *Index) SafeSearch(ctx context.Context, query string, limit int) []Hit {
	hits, err := i.Search(ctx, query, limit)
	if err != nil {
		slog.Warn("search failed", slog.String("err", err.Error()))
		return nil
	}
	return hits
}

// IndexStats summarizes the Bleve index size + last update.
type IndexStats struct {
	DocCount    uint64
	SizeBytes   int64
	LastUpdated string
}

// Stats returns DocCount + on-disk size for the index.
func (i *Index) Stats() IndexStats {
	out := IndexStats{}
	i.mu.RLock()
	bx := i.bleve
	path := i.path
	i.mu.RUnlock()
	if bx != nil {
		if dc, err := bx.DocCount(); err == nil {
			out.DocCount = dc
		}
	}
	if path != "" {
		if size, err := dirSize(path); err == nil {
			out.SizeBytes = size
		}
	}
	return out
}

// RebuildAll drops every document and reindexes from the DB. The Store
// interface is referenced via an opaque interface to avoid an import cycle.
func (i *Index) RebuildAll(ctx context.Context, store NodeLister) error {
	i.mu.Lock()
	bx := i.bleve
	path := i.path
	i.mu.Unlock()
	if bx == nil {
		return errors.New("index disabled")
	}
	// Close + delete + reopen is the simplest "drop everything" approach.
	if err := bx.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	mapping := bleve.NewIndexMapping()
	fresh, err := bleve.New(path, mapping)
	if err != nil {
		return err
	}
	i.mu.Lock()
	i.bleve = fresh
	i.mu.Unlock()
	// Reindex.
	nodes, err := store.AllNodesForIndex(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		_ = i.IndexNode(ctx, n)
	}
	return nil
}

// NodeLister is the slim Store contract RebuildAll needs.
type NodeLister interface {
	AllNodesForIndex(ctx context.Context) ([]*model.Node, error)
}

// SQLLike is implemented in query.go to keep this file focused on Bleve.

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
