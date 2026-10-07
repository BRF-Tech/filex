package wasmplugin

// LinkHolds is the question the page door asks before it queues a visitor's
// job (filex #185): is the link's document, where it lies now, inside the root
// the link recorded? It is the screen's own judgement (holdToLink), so the
// door and the screen cannot answer it differently.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestLinkHolds_JudgesTheDocumentWhereItLiesNow(t *testing.T) {
	h := newBareHarness(t, nil)
	ctx := context.Background()
	rooted := &model.Share{AppRoot: "main://docs"}

	assert.True(t, h.reg.LinkHolds(ctx, rooted, h.st.ID, "docs/contract.txt"), "inside the root")
	assert.True(t, h.reg.LinkHolds(ctx, rooted, h.st.ID, "docs/deep/contract.txt"))
	assert.False(t, h.reg.LinkHolds(ctx, rooted, h.st.ID, "secret/contract.txt"), "moved out of the root")
	assert.False(t, h.reg.LinkHolds(ctx, rooted, h.st.ID, "docs-old/contract.txt"), "a sibling with the root's name as a prefix is outside it")
	assert.False(t, h.reg.LinkHolds(ctx, &model.Share{AppRoot: "yan://docs"}, h.st.ID, "docs/contract.txt"), "a root on another storage")
	assert.False(t, h.reg.LinkHolds(ctx, rooted, 0, "docs/contract.txt"), "a storage whose name cannot be read is outside every root")
	assert.False(t, h.reg.LinkHolds(ctx, &model.Share{AppRoot: "no storage named"}, h.st.ID, "docs/contract.txt"),
		"a recorded root that does not read as one holds the link to nothing")
	assert.True(t, h.reg.LinkHolds(ctx, &model.Share{}, h.st.ID, "secret/contract.txt"), "a link opened with no root is held to none")
}
