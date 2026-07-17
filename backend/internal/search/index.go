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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	index "github.com/blevesearch/bleve_index_api"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Index is the search facade.
type Index struct {
	mu    sync.RWMutex
	bleve bleve.Index
	path  string

	// contentHook, when set, is invoked (best-effort) after a metadata
	// index of a file whose content fingerprint drifted from what the doc
	// already holds — the server wires it to enqueue a content_index job.
	contentHook func(ctx context.Context, n *model.Node)
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

// Enabled reports whether a live Bleve index is wired (false = the server
// runs in SQL LIKE fallback mode).
func (i *Index) Enabled() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bleve != nil
}

// SetContentHook wires the callback fired for file nodes whose content
// needs (re)extraction — see Index.contentHook. Call before serving.
func (i *Index) SetContentHook(h func(ctx context.Context, n *model.Node)) {
	i.mu.Lock()
	i.contentHook = h
	i.mu.Unlock()
}

// docFromNode extracts indexable fields from a Node.
type doc struct {
	StorageID int64  `json:"storage_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Mime      string `json:"mime,omitempty"`
	Type      string `json:"type"`
	// Content is the extracted plain text (queue-fed, capped at 200 KiB);
	// ContentSig fingerprints the source bytes it was extracted from so
	// re-index passes can skip re-extraction when nothing changed.
	Content    string `json:"content,omitempty"`
	ContentSig string `json:"content_sig,omitempty"`
}

// ContentFingerprint identifies a node's content version — used to decide
// whether the indexed content is stale. Prefers the backend etag; nodes
// without one fall back to size + mtime.
func ContentFingerprint(n *model.Node) string {
	if n.Etag != "" {
		return n.Etag
	}
	var mt int64
	if n.BackendMtime != nil {
		mt = n.BackendMtime.UnixMilli()
	}
	return fmt.Sprintf("%d:%d", n.Size, mt)
}

// IndexNode adds or updates a node entry. Previously extracted content is
// carried over (a rename/move must not wipe it); when the content
// fingerprint drifted, the content hook fires so extraction is re-queued.
func (i *Index) IndexNode(ctx context.Context, n *model.Node) error {
	return i.indexNode(ctx, n, true)
}

func (i *Index) indexNode(ctx context.Context, n *model.Node, allowHook bool) error {
	i.mu.RLock()
	bx := i.bleve
	hook := i.contentHook
	i.mu.RUnlock()
	if bx == nil {
		return nil
	}
	id := strconv.FormatInt(n.ID, 10)
	d := doc{
		StorageID: n.StorageID,
		Name:      n.Name,
		Path:      n.Path,
		Mime:      n.Mime,
		Type:      string(n.Type),
	}
	// Preserve content across metadata reindexes: Bleve replaces the whole
	// document on Index(), so re-supply what the doc already holds.
	d.Content, d.ContentSig = storedContent(bx, id)
	if err := bx.Index(id, d); err != nil {
		return err
	}
	if allowHook && hook != nil && n.Type == model.NodeTypeFile && d.ContentSig != ContentFingerprint(n) {
		hook(ctx, n)
	}
	return nil
}

// IndexNodeContent updates the node's document with extracted content. The
// metadata fields are re-supplied from n (the authoritative row) so the
// content update never clobbers them — Bleve has no partial update, the
// whole doc is replaced. Metadata indexing stays synchronous elsewhere;
// this lands later, from the content_index queue job.
func (i *Index) IndexNodeContent(_ context.Context, n *model.Node, content string) error {
	i.mu.RLock()
	bx := i.bleve
	i.mu.RUnlock()
	if bx == nil {
		return nil
	}
	d := doc{
		StorageID:  n.StorageID,
		Name:       n.Name,
		Path:       n.Path,
		Mime:       n.Mime,
		Type:       string(n.Type),
		Content:    content,
		ContentSig: ContentFingerprint(n),
	}
	return bx.Index(strconv.FormatInt(n.ID, 10), d)
}

// storedContent reads the content + fingerprint currently stored on a doc
// (empty strings when the doc is missing or holds no content).
func storedContent(bx bleve.Index, id string) (content, sig string) {
	d, err := bx.Document(id)
	if err != nil || d == nil {
		return "", ""
	}
	d.VisitFields(func(f index.Field) {
		switch f.Name() {
		case "content":
			content = string(f.Value())
		case "content_sig":
			sig = string(f.Value())
		}
	})
	return content, sig
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

// Scope selects which fields a search consults.
type Scope string

// Search scopes (the `scope` query param of the search endpoints).
const (
	ScopeAll     Scope = "all"     // names + content, name hits ranked first
	ScopeName    Scope = "name"    // filenames/paths only (legacy behavior)
	ScopeContent Scope = "content" // extracted file content only
)

// ParseScope maps a request string onto a Scope (default: all).
func ParseScope(s string) Scope {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(ScopeName):
		return ScopeName
	case string(ScopeContent):
		return ScopeContent
	default:
		return ScopeAll
	}
}

// Values for Hit.Matched (frozen v0.2 API contract).
const (
	MatchedName    = "name"
	MatchedContent = "content"
	MatchedBoth    = "both"
)

// Hit is a single search result.
type Hit struct {
	NodeID int64
	Score  float64
	// Snippet is a short plain-text fragment around a content match, with
	// the matched terms wrapped in « » (empty for name-only hits, no HTML).
	Snippet string
	// Matched reports which side(s) hit: "name" | "content" | "both".
	Matched string
}

// Search returns top-N name/path matches for the query string — the
// legacy, name-scoped entry point (see SearchScoped for content search).
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
func (i *Index) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	return i.SearchScoped(ctx, query, limit, ScopeName)
}

// SearchScoped runs the query against the requested scope. With ScopeAll
// the name hits come first (frozen contract: geriye uyumlu sıralama), then
// content-only hits; a doc matching on both is reported once with
// Matched="both". Content hits carry a plain-text Snippet.
func (i *Index) SearchScoped(_ context.Context, query string, limit int, scope Scope) ([]Hit, error) {
	if limit <= 0 {
		limit = 50
	}
	i.mu.RLock()
	bx := i.bleve
	i.mu.RUnlock()
	if bx == nil || query == "" {
		return nil, nil
	}

	var nameRes, contentRes *bleve.SearchResult
	if scope != ScopeContent {
		req := bleve.NewSearchRequest(nameQuery(query))
		req.Size = limit
		res, err := bx.Search(req)
		if err != nil {
			return nil, err
		}
		nameRes = res
	}
	if scope != ScopeName {
		cq := bleve.NewMatchQuery(query)
		cq.SetField("content")
		req := bleve.NewSearchRequest(cq)
		req.Size = limit
		req.Highlight = bleve.NewHighlight()
		req.Highlight.AddField("content")
		res, err := bx.Search(req)
		if err != nil {
			return nil, err
		}
		contentRes = res
	}

	out := make([]Hit, 0, limit)
	seen := map[string]int{} // doc id → position in out
	if nameRes != nil {
		for _, h := range nameRes.Hits {
			id, _ := strconv.ParseInt(h.ID, 10, 64)
			seen[h.ID] = len(out)
			out = append(out, Hit{NodeID: id, Score: h.Score, Matched: MatchedName})
		}
	}
	if contentRes != nil {
		for _, h := range contentRes.Hits {
			snippet := ""
			if frags, ok := h.Fragments["content"]; ok && len(frags) > 0 {
				snippet = plainSnippet(frags[0])
			}
			if pos, ok := seen[h.ID]; ok {
				out[pos].Matched = MatchedBoth
				if out[pos].Snippet == "" {
					out[pos].Snippet = snippet
				}
				continue
			}
			id, _ := strconv.ParseInt(h.ID, 10, 64)
			out = append(out, Hit{NodeID: id, Score: h.Score, Snippet: snippet, Matched: MatchedContent})
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// nameQuery builds the historical name+path disjunction (see Search docs).
func nameQuery(term string) query.Query {
	// Lower-case for the wildcard side: Bleve stores tokens lower-cased
	// by default but wildcard queries are NOT analysed, so an upper-case
	// term in the user's input would miss every row.
	wcTerm := "*" + strings.ToLower(term) + "*"

	matchQ := bleve.NewMatchQuery(term)
	matchQ.SetField("name")

	wildQ := bleve.NewWildcardQuery(wcTerm)
	wildQ.SetField("name")

	pathQ := bleve.NewWildcardQuery(wcTerm)
	pathQ.SetField("path")

	return bleve.NewDisjunctionQuery(matchQ, wildQ, pathQ)
}

// plainSnippet converts a Bleve highlight fragment to the wire snippet
// format: matched terms in « », whitespace collapsed, no HTML markup.
func plainSnippet(frag string) string {
	s := strings.ReplaceAll(frag, "<mark>", "«")
	s = strings.ReplaceAll(s, "</mark>", "»")
	return strings.Join(strings.Fields(s), " ")
}

// SafeSearch wraps Search and logs errors instead of bubbling them.
// Useful in handlers that want a "best effort" search.
func (i *Index) SafeSearch(ctx context.Context, query string, limit int) []Hit {
	return i.SafeSearchScoped(ctx, query, limit, ScopeName)
}

// SafeSearchScoped is SearchScoped with errors logged instead of returned.
func (i *Index) SafeSearchScoped(ctx context.Context, query string, limit int, scope Scope) []Hit {
	hits, err := i.SearchScoped(ctx, query, limit, scope)
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

// RebuildAll drops every document and reindexes metadata from the DB. The
// Store interface is referenced via an opaque interface to avoid an import
// cycle. Content is NOT re-extracted (the fresh index starts without it) —
// use RebuildAllWithContent to also re-queue extraction.
func (i *Index) RebuildAll(ctx context.Context, store NodeLister) error {
	return i.rebuildAll(ctx, store, false)
}

// RebuildAllWithContent is RebuildAll plus content re-extraction: after
// each row lands, the content hook fires for eligible files (the rebuilt
// index holds no content, so every extractable file re-enqueues).
func (i *Index) RebuildAllWithContent(ctx context.Context, store NodeLister) error {
	return i.rebuildAll(ctx, store, true)
}

func (i *Index) rebuildAll(ctx context.Context, store NodeLister, withContent bool) error {
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
		_ = i.indexNode(ctx, n, withContent)
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
