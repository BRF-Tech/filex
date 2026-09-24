package wasmplugin

// share_create on a document the STORAGE has and the catalogue has not seen.
//
// A share row points at a node. A file that reached the storage outside filex
// has none until the next sync, but the explorer already lists it (driver
// fallback), the action gate already stat-ed it and the job already read it —
// so asking for signatures on it died at share_create with "no such file".
// The fix records the one input in the catalogue; these tests pin both what
// it does and how far it reaches.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// catalogueSink is the harness's memSink plus the Cataloguer half the real
// sink (handlers.AppPlugins, through protocolsync) has, counting every ask so
// a test can say not only "it worked" but "it was not even consulted".
type catalogueSink struct {
	*memSink
	asked []string
}

func (c *catalogueSink) Catalogue(ctx context.Context, storageID int64, rel string) (*model.Node, error) {
	c.asked = append(c.asked, rel)
	if err := c.memSink.catalogue(ctx, storageID, rel); err != nil {
		return nil, err
	}
	return c.store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/"+strings.Trim(rel, "/")))
}

func (h *harness) withCatalogue() *catalogueSink {
	c := &catalogueSink{memSink: h.sink}
	h.reg.SetOutputSink(c)
	return c
}

// ⭐ The case that was failing: the job's own input, on disk, no node.
func TestShareCreate_AnInputTheCatalogueHasNotSeenGetsItsNode(t *testing.T) {
	h := newHarness(t, nil)
	cat := h.withCatalogue()
	p := h.install(t)
	h.writeFile(t, "inbox/contract.txt", "the terms")
	u := h.actor(t)
	s := h.jobScope(t, p, u, "inbox/contract.txt")
	ref := s.Inputs()[0].Ref

	out, err := shareCreate(t, s, map[string]any{
		"page_id": "signer", "subject": "Please sign", "pin": "auto",
		"files": []map[string]any{{"ref": ref, "name": "contract.txt"}},
	})
	require.NoError(t, err, "a document on the storage must be shareable before the next sync")
	assert.Equal(t, []string{"inbox/contract.txt"}, cat.asked, "the one input, and only it, was catalogued")

	node, err := h.store.GetNodeByPath(context.Background(), h.st.ID, pathkey.Hash(h.st.ID, "/inbox/contract.txt"))
	require.NoError(t, err)
	require.NotNil(t, node, "the input now has a catalogue row")
	sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	assert.Equal(t, node.ID, sh.NodeID, "the link points at the row it caused")

	// A second link on the same document finds the row and asks for nothing.
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "again",
		"files": []map[string]any{{"ref": ref, "name": "contract.txt"}}})
	require.NoError(t, err)
	assert.Len(t, cat.asked, 1, "a catalogued input is not catalogued twice")
}

// ⚠ Only the job's INPUTS. A `path` the plugin merely names is a string it
// chose; the person's ACL was never checked against it, so a missing row
// stays "no such file" — and the catalogue is not even consulted.
func TestShareCreate_APathThatIsNotAnInputIsNotCatalogued(t *testing.T) {
	h := newHarness(t, nil)
	cat := h.withCatalogue()
	p := h.install(t)
	h.writeFile(t, "inbox/contract.txt", "the terms")
	h.writeFile(t, "private/salaries.txt", "not yours")
	s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")

	_, err := shareCreate(t, s, map[string]any{"page_id": "signer", "path": "private/salaries.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
	assert.Empty(t, cat.asked, "a path outside the job's inputs must not reach the catalogue")
	node, _ := h.store.GetNodeByPath(context.Background(), h.st.ID, pathkey.Hash(h.st.ID, "/private/salaries.txt"))
	assert.Nil(t, node)
}

// An input that is on neither side — deleted between the gate and the job —
// is still "no such file", and a sink without the Cataloguer half keeps the
// host exactly as strict as it was.
func TestShareCreate_NoFileOrNoCataloguerStaysNoSuchFile(t *testing.T) {
	t.Run("gone from the storage", func(t *testing.T) {
		h := newHarness(t, nil)
		cat := h.withCatalogue()
		p := h.install(t)
		h.writeFile(t, "inbox/contract.txt", "the terms")
		s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")
		require.NoError(t, h.drv.Delete(context.Background(), "inbox/contract.txt"))
		_, err := shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no such file")
		assert.Equal(t, []string{"inbox/contract.txt"}, cat.asked)
	})
	t.Run("a sink that cannot catalogue", func(t *testing.T) {
		h := newHarness(t, nil)
		p := h.install(t)
		h.writeFile(t, "inbox/contract.txt", "the terms")
		s := h.jobScope(t, p, h.actor(t), "inbox/contract.txt")
		_, err := shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no such file")
	})
}
