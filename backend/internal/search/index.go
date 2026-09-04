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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blevesearch/bleve/v2"
	searchpkg "github.com/blevesearch/bleve/v2/search"
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

	// staleSchema is set when the index on disk was written by a build
	// with an older document schema (see indexSchemaVersion). Queries
	// keep working: the legacy sub-queries stay in every query for
	// exactly this reason. But documents written before the upgrade have
	// no name_norm field, so they only answer the legacy half until they
	// are reindexed. Surfaced as Stats().NeedsRebuild.
	staleSchema bool
	// foundSchema is the marker actually read off disk, kept for the log
	// line that tells an operator WHICH schema they are upgrading from.
	foundSchema string

	// pending is the replacement index a rebuild is filling. While it is
	// set, every write lands in both it and the live index — otherwise a
	// file uploaded during a rebuild would disappear from search the
	// moment the replacement was swapped in. See rebuild.go.
	pending bleve.Index

	// rebuilding is the single guard shared by the manual admin endpoint
	// and the automatic schema repair. It lives here, not on the HTTP
	// handler, because a handler-local flag can only see the rebuilds the
	// handler started.
	rebuilding atomic.Bool

	// dirty remembers the documents the write path touched during a
	// rebuild, so the reindex loop does not overwrite them with the older
	// rows it snapshotted. Non-nil only while a rebuild runs.
	dirtyMu sync.Mutex
	dirty   map[string]struct{}

	// FreeBytes probes free disk space on the filesystem holding the
	// index. Swapped in tests to exercise the guard without filling a
	// disk (same shape as staging.Area.FreeBytes).
	FreeBytes func(dir string) (uint64, error)
}

// Open returns an Index ready to use. Pass empty path to disable.
//
// Upgrade behaviour (issue #15 added two indexed fields): an index
// written by an older build is opened AS IS and keeps serving. The
// document schema stamped inside it is compared with the one this build
// writes, and a mismatch is recorded, logged, and reported through the
// admin stats endpoint — but the repair itself is started later, from the
// server bootstrap (Index.AutoRebuildIfStale), because it needs the node
// store and the content queue and neither exists at this point.
//
// Until the replacement is live, search returns everything it returned
// before the upgrade: the pre-#15 sub-queries are still part of every
// query, so the change adds recall and never removes any.
//
// ⚠ v0.29.0 stopped here, and that was the bug. It shipped better
// filename matching that could not reach a single document any existing
// installation already had, said so only in an admin endpoint, and the
// reporter of issue #15 upgraded and correctly reported that nothing had
// changed. What made an automatic rebuild unacceptable then — a rebuild
// started from an empty index and extracted content lives only there —
// is fixed in rebuild.go, not worked around.
func Open(path string) (*Index, error) {
	if path == "" {
		return &Index{}, nil
	}
	// Before anything else: clean up after a container that died in the
	// middle of a rebuild or a swap. See recoverInterrupted.
	recoverInterrupted(path)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		mapping := bleve.NewIndexMapping()
		bx, err := bleve.New(path, mapping)
		if err != nil {
			return nil, err
		}
		stampSchemaVersion(bx)
		return &Index{bleve: bx, path: path}, nil
	}
	bx, err := bleve.Open(path)
	if err != nil {
		return nil, err
	}
	idx := &Index{bleve: bx, path: path}
	if v, err := bx.GetInternal([]byte(indexVersionKey)); err == nil && string(v) != indexSchemaVersion {
		idx.staleSchema = true
		idx.foundSchema = schemaLabel(v)
		// One line, naming both schemas, on every start until it is
		// fixed. The repair itself is started by the server bootstrap:
		// it needs the node store and the content queue, neither of
		// which exists yet here. See Index.AutoRebuildIfStale.
		slog.Warn("search: index document schema is out of date; separator-blind and typo-tolerant name matching cannot reach existing files until it is rebuilt",
			slog.String("found_schema", idx.foundSchema),
			slog.String("want_schema", indexSchemaVersion))
	}
	return idx, nil
}

// stampSchemaVersion records the schema of the documents this build
// writes. Best effort: a Bleve that cannot hold the marker still serves
// queries, it just cannot tell the operator a rebuild would help.
func stampSchemaVersion(bx bleve.Index) {
	if err := bx.SetInternal([]byte(indexVersionKey), []byte(indexSchemaVersion)); err != nil {
		slog.Warn("could not stamp search index schema version", slog.String("err", err.Error()))
	}
}

// schemaLabel renders the marker read off an index; pre-#15 indexes have
// no marker at all, which is the common case rather than an error.
func schemaLabel(v []byte) string {
	if len(v) == 0 {
		return "1 (pre-0.29, unstamped)"
	}
	return string(v)
}

// NeedsRebuild reports whether the on-disk index predates the current
// document schema. See Open.
func (i *Index) NeedsRebuild() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.staleSchema
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
	// NameNorm and PathNorm are Name/Path run through Normalize: the
	// separator-blind form that makes `invoice 2026` find
	// `invoice_2026.pdf` (issue #15). They are indexed with the SAME
	// default analyzer as every other field — deliberately, because a
	// custom field mapping is stored inside the index at creation time,
	// so a fresh install and an upgraded one would end up analysing the
	// same filename two different ways. Pre-normalising in Go keeps one
	// behaviour everywhere.
	NameNorm string `json:"name_norm,omitempty"`
	PathNorm string `json:"path_norm,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Type     string `json:"type"`
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
	id := strconv.FormatInt(n.ID, 10)
	d := docFor(n)

	// The read lock is held across the write, not just across reading the
	// handles: a rebuild's swap takes the write lock and closes both
	// indexes, and a writer that had already grabbed a handle would then
	// be writing into a closed index.
	i.mu.RLock()
	bx, pending, hook := i.bleve, i.pending, i.contentHook
	if bx == nil {
		i.mu.RUnlock()
		return nil
	}
	// Preserve content across metadata reindexes: Bleve replaces the whole
	// document on Index(), so re-supply what the doc already holds.
	d.Content, d.ContentSig = storedContent(bx, id)
	err := bx.Index(id, d)
	if err == nil {
		i.dualWrite(id, pending, func() error { return pending.Index(id, d) })
	}
	i.mu.RUnlock()
	if err != nil {
		return err
	}
	if allowHook && hook != nil && n.Type == model.NodeTypeFile && d.ContentSig != ContentFingerprint(n) {
		hook(ctx, n)
	}
	return nil
}

// docFor renders the indexable document for a node.
func docFor(n *model.Node) doc {
	return doc{
		StorageID: n.StorageID,
		Name:      n.Name,
		Path:      n.Path,
		NameNorm:  Normalize(n.Name),
		PathNorm:  Normalize(n.Path),
		Mime:      n.Mime,
		Type:      string(n.Type),
	}
}

// dualWrite applies a write to the replacement index a rebuild is
// building, and records the document as one the rebuild loop must not
// overwrite with its older snapshot row. A nil pending index (the normal
// case: no rebuild running) makes it a no-op.
//
// A failure here is logged, not returned. The live index already took the
// write, and failing a user's upload because a background rebuild hiccuped
// would be the tail wagging the dog; the rebuild's own verification is
// what decides whether the replacement is fit to swap in.
func (i *Index) dualWrite(id string, pending bleve.Index, apply func() error) {
	if pending == nil {
		return
	}
	i.markDirty(id)
	if err := apply(); err != nil {
		slog.Warn("search: write to the index being rebuilt failed",
			slog.String("doc", id), slog.String("err", err.Error()))
	}
}

// IndexNodeContent updates the node's document with extracted content. The
// metadata fields are re-supplied from n (the authoritative row) so the
// content update never clobbers them — Bleve has no partial update, the
// whole doc is replaced. Metadata indexing stays synchronous elsewhere;
// this lands later, from the content_index queue job.
func (i *Index) IndexNodeContent(_ context.Context, n *model.Node, content string) error {
	d := docFor(n)
	d.Content = content
	d.ContentSig = ContentFingerprint(n)
	id := strconv.FormatInt(n.ID, 10)

	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.bleve == nil {
		return nil
	}
	if err := i.bleve.Index(id, d); err != nil {
		return err
	}
	pending := i.pending
	i.dualWrite(id, pending, func() error { return pending.Index(id, d) })
	return nil
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
	docID := strconv.FormatInt(id, 10)
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.bleve == nil {
		return nil
	}
	if err := i.bleve.Delete(docID); err != nil {
		return err
	}
	// A delete during a rebuild matters more than a write: without this
	// the file would come back from the dead at the swap, because the
	// rebuild works from a node snapshot that still contains it.
	pending := i.pending
	i.dualWrite(docID, pending, func() error { return pending.Delete(docID) })
	return nil
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
	// Tier is the rank bucket this hit was sorted into (issue #15). It is
	// deliberately NOT on the wire — the HTTP response shape is frozen —
	// but it is what makes "exact matches rank first" an assertable
	// contract instead of a hope about merged Bleve scores.
	Tier Tier
}

// Filter narrows a search to a set of node IDs — how the `tag:` filter
// reaches the index without tags having to be indexed.
//
// Tags live in the node_meta table and change without the node itself
// being re-indexed, so a tag copied into the Bleve document would go
// stale the moment somebody re-tagged a file. The caller resolves the
// tag to node IDs against the database (always current) and hands the
// set down here, where it becomes a conjunction — so the filter runs
// INSIDE the engine and `limit` still counts filtered results.
type Filter struct {
	// Restrict limits results to IncludeIDs. Restrict with an empty
	// IncludeIDs means "the filter matched nothing", and the search
	// honours that: a tag that does not exist returns no results rather
	// than being quietly ignored and returning everything.
	Restrict   bool
	IncludeIDs []int64
	ExcludeIDs []int64
}

// searchOverFetch is how many extra hits are pulled from Bleve so the
// Go-side ranking has something to re-order. Without it the ranking
// could only reshuffle a window Bleve's own scores had already chosen,
// and an exact match that Bleve scored 60th would never reach tier 1.
const searchOverFetch = 4

// searchMaxFetch caps that over-fetch. 500 documents is far more than
// any UI shows and keeps a pathological `limit` from turning into a
// whole-index scan.
const searchMaxFetch = 500

// Search returns top-N name/path matches for the query string — the
// legacy, name-scoped entry point (see SearchScoped for content search).
//
// Falls back to nil result + nil error when index is disabled — callers
// should treat that as "no search engine, do a SQL LIKE instead".
//
// See nameQuery for how a query is built and Tier for how its results
// are ordered.
func (i *Index) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	return i.SearchScoped(ctx, query, limit, ScopeName)
}

// SearchScoped runs the query against the requested scope, unfiltered.
func (i *Index) SearchScoped(ctx context.Context, query string, limit int, scope Scope) ([]Hit, error) {
	return i.SearchFiltered(ctx, query, limit, scope, nil)
}

// SearchFiltered is SearchScoped with an optional node-ID Filter (how the
// `tag:` filter is applied — see Filter).
//
// Ordering is the Tier contract, best tier first, Bleve score descending
// within a tier, node ID as the final tiebreak so the same query always
// comes back in the same order. Because TierContent sorts last, the
// pre-v0.2 frozen contract — name hits before content-only hits — is
// preserved by construction rather than by luck: a document that matched
// on both keeps its NAME tier and stays ahead of content-only hits, and
// is still reported once, with Matched="both" and its snippet.
//
// Two passes run against the name fields. The strict pass is the legacy
// disjunction plus the separator-blind additions; the typo-tolerant pass
// runs ONLY when the strict pass came back short of `limit`. That is both
// the cheap and the quiet choice: fuzziness is the expensive part of the
// query, and always-on fuzziness would pad a perfectly good result list
// with near-misses nobody asked for.
func (i *Index) SearchFiltered(_ context.Context, q string, limit int, scope Scope, f *Filter) ([]Hit, error) {
	if limit <= 0 {
		limit = 50
	}
	// The read lock is held for the whole search, not just long enough to
	// read the handle. A rebuild's swap closes the live index under the
	// write lock, and a query that had already taken the handle and let go
	// of the lock would run against a closed index and fail — measured:
	// 1 error in 269 queries hammered across a swap. Holding it costs
	// nothing (searches share it) and turns that window into a wait of a
	// few milliseconds.
	i.mu.RLock()
	defer i.mu.RUnlock()
	bx := i.bleve
	if bx == nil || q == "" {
		return nil, nil
	}
	if f != nil && f.Restrict && len(f.IncludeIDs) == 0 {
		return nil, nil
	}

	fetch := limit * searchOverFetch
	if fetch > searchMaxFetch {
		fetch = searchMaxFetch
	}
	if fetch < limit {
		fetch = limit
	}

	out := make([]ranked, 0, fetch)
	seen := map[string]int{} // doc id → position in out
	pq := PrepareQuery(q)

	if scope != ScopeContent {
		// collect turns one Bleve pass into ranked hits. Bleve decided
		// only that these documents are worth LOOKING at; the scorer
		// decides whether each is a result and where it belongs.
		collect := func(res *bleve.SearchResult, fromTypoPass bool) {
			for _, h := range res.Hits {
				if _, dup := seen[h.ID]; dup {
					continue
				}
				name, path := hitField(h, "name"), hitField(h, "path")
				sc := pq.ScoreName(name, path)
				if !sc.OK {
					// Some query piece is answered by neither the
					// filename nor any folder above it. From the strict
					// pass that is a half-match and it is dropped —
					// dropping `/Code` from `Code main` is the whole
					// reason that query stopped feeling broken.
					if !fromTypoPass {
						continue
					}
					// From the typo pass it is the expected shape:
					// `mian.go` is not a subsequence of `main.go`, so the
					// scorer cannot see it and edit distance is the only
					// thing that found it. Keep it, ranked last.
					sc = NameScore{OK: true, Tier: TierFuzzy}
				}
				id, _ := strconv.ParseInt(h.ID, 10, 64)
				seen[h.ID] = len(out)
				out = append(out, ranked{
					Hit: Hit{
						NodeID:  id,
						Score:   float64(sc.Score),
						Matched: MatchedName,
						Tier:    sc.Tier,
					},
					pathLen: len(path),
				})
			}
		}
		res, err := runNameSearch(bx, applyFilter(nameQuery(q), f), fetch)
		if err != nil {
			return nil, err
		}
		collect(res, false)
		// ⚠ The gate counts SURVIVORS, not candidates, and that is a
		// deliberate change: the reporter of issue #15 pointed out that
		// edit distance "is gated behind a strict-pass shortfall so it
		// almost never fires". He was right — before the scorer, a
		// strict pass full of half-matches counted as a full result set
		// and closed this gate. Now only real matches do.
		if len(out) < limit {
			if fq := fuzzyNameQuery(q); fq != nil {
				res, err := runNameSearch(bx, applyFilter(fq, f), fetch)
				if err != nil {
					return nil, err
				}
				collect(res, true)
			}
		}
	}

	if scope != ScopeName {
		cq := bleve.NewMatchQuery(q)
		cq.SetField("content")
		// Every word, not any word. The name side has narrowed on extra
		// words since v0.29.0 but the content side was still a default-OR
		// match, so on demo.filex.sh `Code main` returned nine results,
		// seven of them files that merely contained the word "code". A
		// query where extra words WIDEN the result set is the opposite of
		// what anybody types them for.
		cq.SetOperator(query.MatchQueryOperatorAnd)
		req := bleve.NewSearchRequest(applyFilter(cq, f))
		req.Size = fetch
		req.Highlight = bleve.NewHighlight()
		req.Highlight.AddField("content")
		res, err := bx.Search(req)
		if err != nil {
			return nil, err
		}
		for _, h := range res.Hits {
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
			out = append(out, ranked{
				Hit:     Hit{NodeID: id, Score: h.Score, Snippet: snippet, Matched: MatchedContent, Tier: TierContent},
				pathLen: len(hitField(h, "path")),
			})
		}
	}

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Tier != out[b].Tier {
			return out[a].Tier < out[b].Tier
		}
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		// Two files really can be called main.go. VS Code breaks that tie
		// by preferring the shorter path (fallbackCompare) and so do we —
		// the previous tiebreak was the database id, which is not a
		// ranking, it is insert order.
		if out[a].pathLen != out[b].pathLen {
			return out[a].pathLen < out[b].pathLen
		}
		return out[a].NodeID < out[b].NodeID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	hits := make([]Hit, len(out))
	for i, r := range out {
		hits[i] = r.Hit
	}
	return hits, nil
}

// ranked is a Hit plus the sort keys that are not part of the wire
// shape. The HTTP response is frozen, so the path length that breaks a
// score tie lives here rather than on Hit.
type ranked struct {
	Hit
	pathLen int
}

// runNameSearch executes one name-side pass, asking Bleve for the stored
// name and path so the ranking can be computed in Go.
func runNameSearch(bx bleve.Index, q query.Query, size int) (*bleve.SearchResult, error) {
	req := bleve.NewSearchRequest(q)
	req.Size = size
	req.Fields = []string{"name", "path"}
	return bx.Search(req)
}

// hitField reads a stored string field off a hit ("" when absent — an
// index written before that field existed, which is exactly the state an
// un-rebuilt upgrade is in).
func hitField(h *searchpkg.DocumentMatch, name string) string {
	if v, ok := h.Fields[name].(string); ok {
		return v
	}
	return ""
}

// nameQuery builds the name/path disjunction.
//
// The first three sub-queries are the pre-#15 trio, kept verbatim. They
// are what makes this change safe to ship without a reindex: a document
// written by an older build has no name_norm field, and would answer
// none of the separator-blind sub-queries, but it still answers these —
// so an operator who upgrades and does nothing gets everything they got
// before, and a rebuild only adds.
//
//   - Match (name): exact-token hits; ranks full filenames well.
//   - Wildcard *term* (name): mid-string substrings, `squ` → square.jpg.
//   - Wildcard *term* (path): folder segments.
//
// The additions:
//
//   - Match (name_norm, operator AND): separator-blind token matching.
//     `invoice 2026` and `invoice_2026.pdf` normalise to the same words,
//     which is the whole bug from issue #15.
//   - Per-word wildcards (name_norm, path_norm): the legacy wildcards
//     above wildcard the WHOLE raw term, so the first space in a query
//     disabled that half of the search outright — no indexed token can
//     contain a space, so `*main go*` matched nothing, ever. Splitting
//     into one `*word*` per word is what un-breaks multi-word queries.
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

	parts := []query.Query{matchQ, wildQ, pathQ}

	if words := NormWords(term); len(words) > 0 {
		normMatch := bleve.NewMatchQuery(Normalize(term))
		normMatch.SetField("name_norm")
		normMatch.SetOperator(query.MatchQueryOperatorAnd)
		normMatch.SetBoost(4)
		parts = append(parts, normMatch)
		// The per-word wildcards are the expensive half of the query: a
		// leading `*` makes Bleve walk the whole term dictionary, and
		// these add two more such walks on top of the two the legacy
		// trio already does. Measured on a 20k-document index, adding
		// them unconditionally doubled a single-word query from 7.8 ms
		// to 15.7 ms — and bought nothing, because for a single word
		// `*word*` on name_norm finds exactly what `*word*` on name
		// already found. So they run only where they can add something:
		// a multi-word query (the legacy wildcard is dead there, no
		// indexed token contains a space) or a word the normaliser
		// changed (`foo!` -> `foo`).
		if len(words) > 1 || words[0] != strings.ToLower(strings.TrimSpace(term)) {
			parts = append(parts, wordWildcards(words, "name_norm", 2), wordWildcards(words, "path_norm", 1))
		}
	}
	return bleve.NewDisjunctionQuery(parts...)
}

// wordWildcards requires a `*word*` wildcard match for EVERY word — a
// conjunction, so extra words narrow the search instead of widening it.
// Normalize has already stripped everything that is not a letter or a
// digit, so no word can smuggle a `*` or `?` into the pattern.
func wordWildcards(words []string, field string, boost float64) query.Query {
	subs := make([]query.Query, 0, len(words))
	for _, w := range words {
		wq := bleve.NewWildcardQuery("*" + w + "*")
		wq.SetField(field)
		subs = append(subs, wq)
	}
	c := bleve.NewConjunctionQuery(subs...)
	c.SetBoost(boost)
	return c
}

// fuzzyNameQuery is the typo-tolerant pass: one edit-distance query per
// word, all required. Returns nil when the query has no alphanumeric
// content.
//
// Prefix length 1 is a cost decision, not a correctness one — it stops
// Bleve walking the whole term dictionary for every query — and it costs
// exactly the typos that fall on a word's first character. `mian` still
// reaches `main` because both start with `m`.
//
// ⚠ A word whose fuzziness works out to 0 becomes a plain TermQuery.
// Bleve builds a Levenshtein automaton only for distance 1 or 2 and
// rejects 0 outright — with the message "fuzziness exceeds the max
// limit", which reads like the opposite of the actual problem and made
// every short word fail the whole conjunction.
func fuzzyNameQuery(term string) query.Query {
	words := NormWords(term)
	if len(words) == 0 {
		return nil
	}
	subs := make([]query.Query, 0, len(words))
	for _, w := range words {
		fuzz := fuzzinessFor(w)
		if fuzz == 0 {
			tq := bleve.NewTermQuery(w)
			tq.SetField("name_norm")
			subs = append(subs, tq)
			continue
		}
		fq := bleve.NewFuzzyQuery(w)
		fq.SetField("name_norm")
		fq.SetFuzziness(fuzz)
		fq.SetPrefix(1)
		subs = append(subs, fq)
	}
	return bleve.NewConjunctionQuery(subs...)
}

// applyFilter wraps a query in the node-ID Filter, when there is one.
func applyFilter(q query.Query, f *Filter) query.Query {
	if f == nil || (!f.Restrict && len(f.ExcludeIDs) == 0) {
		return q
	}
	b := bleve.NewBooleanQuery()
	b.AddMust(q)
	if f.Restrict {
		b.AddMust(bleve.NewDocIDQuery(docIDs(f.IncludeIDs)))
	}
	if len(f.ExcludeIDs) > 0 {
		b.AddMustNot(bleve.NewDocIDQuery(docIDs(f.ExcludeIDs)))
	}
	return b
}

// docIDs renders node IDs as the string document IDs the index is keyed by.
func docIDs(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
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
	return i.SafeSearchFiltered(ctx, query, limit, scope, nil)
}

// SafeSearchFiltered is SearchFiltered with errors logged instead of returned.
func (i *Index) SafeSearchFiltered(ctx context.Context, query string, limit int, scope Scope, f *Filter) []Hit {
	hits, err := i.SearchFiltered(ctx, query, limit, scope, f)
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
	// NeedsRebuild is true when the index was written by a build with an
	// older document schema. Search still works — see Open — but the
	// documents already in it cannot answer the separator-blind or
	// typo-tolerant half of a query until they are reindexed. It stays
	// true for the whole of a rebuild: until the swap, the index
	// answering queries really is the old one.
	NeedsRebuild bool
	// Rebuilding is true while a replacement index is being built, by
	// either the admin endpoint or the automatic schema repair.
	Rebuilding bool
}

// Stats returns DocCount + on-disk size for the index.
func (i *Index) Stats() IndexStats {
	out := IndexStats{}
	out.Rebuilding = i.rebuilding.Load()
	i.mu.RLock()
	bx := i.bleve
	path := i.path
	out.NeedsRebuild = i.staleSchema
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

// RebuildAll reindexes every node row into a replacement index and swaps
// it in. The Store interface is referenced via an opaque interface to
// avoid an import cycle. Text already extracted is carried over from the
// live index; nothing is re-extracted (use RebuildAllWithContent for
// that). See rebuild.go for the swap.
func (i *Index) RebuildAll(ctx context.Context, store NodeLister) error {
	return i.Rebuild(ctx, store, RebuildOptions{Reason: "admin"})
}

// RebuildAllWithContent is RebuildAll plus content re-extraction: every
// eligible file is re-enqueued for extraction once the replacement index
// is live.
func (i *Index) RebuildAllWithContent(ctx context.Context, store NodeLister) error {
	return i.Rebuild(ctx, store, RebuildOptions{ReExtract: true, Reason: "admin"})
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
