package assoc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The `engine` capability (filex 0.57): which back end runs the office
// engine - the engine an app brings, or the connected Document Server. Not
// about a kind of file: its one kind is the engine's name. RED before 0.57:
// the capability, EngineChain and EngineSource were unknown.

// engineSrc is a Source whose apps bring engines.
type engineSrc struct {
	src
	engines []AppHandler
}

func (s engineSrc) EngineHandlers() []AppHandler { return s.engines }

func engineService(t *testing.T, ds bool) (*Service, *memStore) {
	t.Helper()
	m := newMem()
	s := New(m)
	s.SetSource(engineSrc{engines: []AppHandler{app("app:office-editor", "0.4.0", []string{EngineOffice})}})
	s.SetOnlyOfficeEngine(func() bool { return ds })
	return s, m
}

func TestEngine_TheAppsEngineFirstTheDocumentServerAfter(t *testing.T) {
	ctx := context.Background()
	s, _ := engineService(t, true)
	ch := s.EngineChain(ctx, EngineOffice)
	assert.Equal(t, []string{"app:office-editor", "onlyoffice"}, ids(ch.On))
	assert.False(t, ch.Custom)

	none, _ := engineService(t, false)
	assert.Equal(t, []string{"app:office-editor"}, ids(none.EngineChain(ctx, EngineOffice).On), "no Document Server connected: not offered")

	plain := New(newMem())
	plain.SetSource(src{})
	plain.SetOnlyOfficeEngine(func() bool { return true })
	assert.Equal(t, []string{"onlyoffice"}, ids(plain.EngineChain(ctx, EngineOffice).On), "a Source that brings no engine: the Document Server alone")
}

func TestEngine_TheAdministratorsOrderIsKeptAndChecked(t *testing.T) {
	ctx := context.Background()
	s, m := engineService(t, true)
	_, err := s.Put(ctx, CapEngine, EngineOffice, Rule{Order: []string{"onlyoffice"}}, nil)
	require.NoError(t, err)
	ch := s.EngineChain(ctx, EngineOffice)
	assert.Equal(t, []string{"onlyoffice", "app:office-editor"}, ids(ch.On))
	assert.True(t, ch.Custom)
	require.Contains(t, m.rows, "engine/office", "stored in the rules table, no table of its own")

	_, err = s.Put(ctx, CapEngine, EngineOffice, Rule{Off: []string{"app:office-editor"}}, nil)
	require.NoError(t, err)
	ch = s.EngineChain(ctx, EngineOffice)
	assert.Equal(t, []string{"onlyoffice"}, ids(ch.On))
	assert.Equal(t, []string{"app:office-editor"}, ids(ch.Off))

	for _, bad := range []Rule{
		{Order: []string{"app:another"}},            // no such engine here
		{Order: []string{"builtin"}},                // filex has no office engine of its own
		{Order: []string{"app:office-editor/view"}}, // an engine has no view
	} {
		_, err := s.Put(ctx, CapEngine, EngineOffice, bad, nil)
		assert.Error(t, err, "%v", bad)
	}

	ok, err := s.Reset(ctx, CapEngine, EngineOffice)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []string{"app:office-editor", "onlyoffice"}, ids(s.EngineChain(ctx, EngineOffice).On))

	require.NoError(t, s.PruneApp(ctx, "office-editor"))
}

// The engine never shows up as a kind of file on the Default apps list, and
// a kind's rule is not an engine's.
func TestEngine_IsNotAKindOfFile(t *testing.T) {
	ctx := context.Background()
	s, _ := engineService(t, true)
	_, err := s.Put(ctx, CapEngine, EngineOffice, Rule{Order: []string{"onlyoffice"}}, nil)
	require.NoError(t, err)
	for _, k := range s.Kinds(ctx) {
		assert.NotEqual(t, EngineOffice, k.Ext)
	}
	// An .office file is a file like any other: filex's own viewer, last in
	// every open chain (DefaultOrder), is all it gets - neither the engine's
	// app nor the Document Server the engine's rule put first - and the
	// engine's rule is not the .office kind's.
	open := s.Chain(ctx, CapOpen, "file.office", "")
	assert.Equal(t, []string{Builtin}, ids(open.On), "no app opens an .office file")
	assert.False(t, open.Custom, "the engine's rule is not a rule of the .office kind")
	assert.True(t, ValidID(CapEngine, OnlyOffice))
	assert.True(t, ValidID(CapEngine, EngineID("office-editor")))
	assert.False(t, ValidID(CapEngine, OpenID("office-editor", "editor")))
}
