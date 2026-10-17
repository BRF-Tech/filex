package handlers

// The document editors' fixed language (#214) on every interface the list
// offers (filex 0.57, markEditorLang): an app's editor opens in it as the
// Document Server's does. On 0.56 the list carried no language and the
// office editor app drew itself in each reader's own.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

func TestMarkEditorLang(t *testing.T) {
	list := func() wasmplugin.ActionsAnswer {
		return wasmplugin.ActionsAnswer{
			Actions: []wasmplugin.ActionRow{{UI: &wasmplugin.UIRef{URL: "/_appui/a/x/index.html"}}, {}},
			Views:   []wasmplugin.ViewRow{{UI: &wasmplugin.UIRef{URL: "/_appui/b/y/index.html"}}, {}},
		}
	}
	fixed := list()
	(&AppPlugins{EditorLang: func(context.Context) string { return "de" }}).markEditorLang(context.Background(), &fixed)
	assert.Equal(t, "de", fixed.Actions[0].UI.EditorLang)
	assert.Equal(t, "de", fixed.Views[0].UI.EditorLang)
	assert.Nil(t, fixed.Views[1].UI, "a view without an interface stays without one")

	for _, h := range []*AppPlugins{{}, {EditorLang: func(context.Context) string { return "" }}} {
		auto := list()
		h.markEditorLang(context.Background(), &auto)
		assert.Empty(t, auto.Actions[0].UI.EditorLang, "each person's own language decides")
		assert.Empty(t, auto.Views[0].UI.EditorLang)
	}
}
