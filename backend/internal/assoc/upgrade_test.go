package assoc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandledKinds_ExtensionsAndMediaTypes(t *testing.T) {
	got := HandledKinds(
		[]AppHandler{app("app:x/v", "1", []string{"drawio", "A B"}, "image/svg+xml")},
		[]AppHandler{app("app:x", "1", []string{"jar"})},
	)
	assert.True(t, got[KindKey(CapOpen, "drawio")])
	assert.True(t, got[KindKey(CapOpen, "svg")], "a media type's known extension is a kind")
	assert.True(t, got[KindKey(CapThumbnail, "jar")])
	assert.False(t, got[KindKey(CapOpen, "jar")], "per capability")
	assert.False(t, got[KindKey(CapOpen, "a b")], "what is not a kind is not counted")
}

func TestNewKinds_AndTheRowsAnUpgradeShows(t *testing.T) {
	before := HandledKinds([]AppHandler{app("app:x/v", "1", []string{"drawio"})}, nil)
	after := HandledKinds([]AppHandler{app("app:x/v", "2", []string{"drawio", "vsdx"})}, []AppHandler{app("app:x", "2", []string{"drawio"})})
	assert.Equal(t, map[string]bool{KindKey(CapOpen, "vsdx"): true, KindKey(CapThumbnail, "drawio"): true}, NewKinds(before, after))

	rows := []InstallKind{
		{Capability: CapOpen, Ext: "drawio"},
		{Capability: CapOpen, Ext: "vsdx"},
		{Capability: CapThumbnail, Ext: "drawio"},
	}
	kept := OnlyNewInstallKinds(rows, before)
	require.Len(t, kept, 2)
	assert.Equal(t, "vsdx", kept[0].Ext)
	assert.Equal(t, CapThumbnail, kept[1].Capability, "the same kind for a capability the app did not have is new")
}

// PlaceForApp writes only the app's own handlers, and with only set (an
// upgrade) only for the kinds it adds; the rest is said, and moves nothing.
func TestPlaceForApp_ItsOwnHandlersAndOnlyTheNewKinds(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	failed := s.PlaceForApp(ctx, "zeta", []Placement{
		{Capability: CapOpen, Ext: "svg", Handler: "app:zeta/viewer", Place: PlaceOff},
		{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceFirst},
		{Capability: CapOpen, Ext: "drawio", Handler: "app:drawio/editor", Place: PlaceOff},
	}, map[string]bool{KindKey(CapOpen, "svg"): true}, nil)
	require.Len(t, failed, 2, "%v", failed)
	assert.Contains(t, failed[0], "not a kind this version adds")
	assert.Contains(t, failed[1], "is not this app's")

	assert.Equal(t, []string{"app:zeta/viewer"}, ids(s.Chain(ctx, CapOpen, "a.svg", "").Off), "the new kind took the choice")
	ch := s.Chain(ctx, CapOpen, "a.drawio", "")
	assert.Equal(t, []string{"app:drawio/editor", "app:zeta/viewer", Builtin}, ids(ch.On), "the old kind did not move")
	assert.False(t, ch.Custom)

	// An install (only nil): every kind of its own.
	assert.Empty(t, s.PlaceForApp(ctx, "zeta", []Placement{{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceFirst}}, nil, nil))
	assert.Equal(t, []string{"app:zeta/viewer", "app:drawio/editor", Builtin}, ids(s.Chain(ctx, CapOpen, "a.drawio", "").On))
}
