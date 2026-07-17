package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/search/extract"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// TypeContentIndex is the op type for async content extraction ("Bul",
// v0.2): read an eligible file from its storage driver, extract plain text
// (internal/search/extract) and land it in the Bleve `content` field.
const TypeContentIndex = "content_index"

// DefaultContentMaxBytes is the source-size ceiling for extraction when
// FILEX_SEARCH_CONTENT_MAX is unset (5 MiB — frozen v0.2 contract).
const DefaultContentMaxBytes int64 = 5 << 20

// NodeGetter is the slim store contract the content job needs.
type NodeGetter interface {
	GetNode(ctx context.Context, id int64) (*model.Node, error)
}

// ContentIndexer owns the content_index job: Enqueue is wired as the search
// index's content hook (fires on every metadata index whose content
// fingerprint drifted), Handle is registered on the worker pool.
//
// Everything here is best-effort by design — the write path never blocks on
// extraction, and extraction failures are logged + skipped rather than
// failing anything user-visible. Only transport-level failures (storage
// read, index write) return an error so the queue's retry budget applies.
type ContentIndexer struct {
	store    NodeGetter
	resolver func(int64) (storage.Driver, error)
	index    *search.Index
	maxBytes int64
}

// NewContentIndexer wires the job. maxBytes <= 0 falls back to
// DefaultContentMaxBytes.
func NewContentIndexer(store NodeGetter, resolver func(int64) (storage.Driver, error), idx *search.Index, maxBytes int64) *ContentIndexer {
	if maxBytes <= 0 {
		maxBytes = DefaultContentMaxBytes
	}
	return &ContentIndexer{store: store, resolver: resolver, index: idx, maxBytes: maxBytes}
}

// Eligible reports whether n qualifies for content extraction: a live file
// within the size cap whose mime/extension has a registered extractor.
func (c *ContentIndexer) Eligible(n *model.Node) bool {
	if n == nil || n.Type != model.NodeTypeFile || n.DeletedAt != nil {
		return false
	}
	if n.Size <= 0 || n.Size > c.maxBytes {
		return false
	}
	/* wiring:e2 — the encrypted-folder marker is metadata, never content. */
	if n.Name == e2e.MarkerName {
		return false
	}
	/* /wiring:e2 */
	return extract.Supported(n.Mime, nodeExt(n.Name))
}

// Enqueue schedules extraction for n when it is eligible. Best-effort:
// enqueue failures are logged, never surfaced — search freshness must not
// cost a write.
func (c *ContentIndexer) Enqueue(ctx context.Context, drv Driver, n *model.Node) {
	if drv == nil || !c.Eligible(n) {
		return
	}
	if _, err := drv.Enqueue(ctx, Op{
		Type:    TypeContentIndex,
		Payload: map[string]any{"node_id": n.ID},
	}); err != nil {
		slog.Warn("content-index: enqueue failed",
			slog.Int64("node", n.ID), slog.String("err", err.Error()))
	}
}

// Handle processes one content_index op. Ineligible/vanished nodes resolve
// as done (nil); extraction failures are logged and the fingerprint is
// still recorded so the node doesn't re-enqueue forever; storage-read and
// index-write failures return an error to use the queue's retry budget.
func (c *ContentIndexer) Handle(ctx context.Context, op Op) error {
	nodeID := payloadInt64(op.Payload, "node_id")
	if nodeID == 0 {
		return nil
	}
	n, err := c.store.GetNode(ctx, nodeID)
	if err != nil || n == nil {
		// Deleted before the worker got to it — nothing to index.
		return nil
	}
	if !c.Eligible(n) {
		return nil
	}
	ex := extract.For(n.Mime, nodeExt(n.Name))
	if ex == nil {
		return nil
	}
	/* wiring:e2 — E2E-encrypted subtree: never extract/index content. The
	   ciphertext is meaningless (CPU waste) and skipping also closes the
	   leak where a plaintext file written into an encrypted folder via a
	   non-web surface (DAV/CLI/AI) would land readable text in the search
	   index. Index EMPTY content so the fingerprint records and the node
	   stops re-enqueueing (same convention as the extract-failure path). */
	if lk, ok := c.store.(e2e.NodeByPathLookup); ok && e2e.UnderEncrypted(ctx, lk, n.StorageID, n.Path) {
		if c.index == nil {
			return nil
		}
		return c.index.IndexNodeContent(ctx, n, "")
	}
	/* /wiring:e2 */
	drv, err := c.resolver(n.StorageID)
	if err != nil {
		return fmt.Errorf("content-index: resolve storage %d: %w", n.StorageID, err)
	}
	rc, err := drv.Read(ctx, n.Path)
	if err != nil {
		return fmt.Errorf("content-index: read %q: %w", n.Path, err)
	}
	defer rc.Close()

	/* wiring:e2 — belt-and-suspenders magic sniff: an encrypted file that
	   escaped the marker walk (moved out of its folder, marker deleted
	   later) still starts with 'filexe2e' — index it with empty content
	   instead of feeding ciphertext to the extractor. */
	head := make([]byte, len(e2e.MagicPrefix))
	nRead, _ := io.ReadFull(rc, head)
	if nRead == len(head) && e2e.HasMagicPrefix(head) {
		if c.index == nil {
			return nil
		}
		return c.index.IndexNodeContent(ctx, n, "")
	}
	body := io.MultiReader(bytes.NewReader(head[:nRead]), rc)
	/* /wiring:e2 */

	text, err := ex.Extract(ctx, io.LimitReader(body, c.maxBytes), extract.DefaultLimit)
	if err != nil {
		// Extraction error — log + index empty content anyway so the
		// fingerprint lands and the node stops re-enqueueing.
		slog.Warn("content-index: extract failed; indexing without content",
			slog.Int64("node", n.ID), slog.String("path", n.Path), slog.String("err", err.Error()))
		text = ""
	}
	if c.index == nil {
		return nil
	}
	if err := c.index.IndexNodeContent(ctx, n, text); err != nil {
		return fmt.Errorf("content-index: index node %d: %w", n.ID, err)
	}
	return nil
}

// nodeExt returns the lower-case extension of name without the dot.
func nodeExt(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

// payloadInt64 reads an integer payload field regardless of how the queue
// driver round-tripped it (JSON decodes numbers as float64; direct enqueue
// keeps int64).
func payloadInt64(p map[string]any, key string) int64 {
	switch v := p[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}
