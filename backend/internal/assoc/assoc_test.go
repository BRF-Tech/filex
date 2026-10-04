package assoc

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// memStore is the rules table in memory.
type memStore struct {
	rows map[string]*model.FileAssociation
	puts int
}

func newMem() *memStore { return &memStore{rows: map[string]*model.FileAssociation{}} }

func (m *memStore) ListFileAssociations(context.Context) ([]*model.FileAssociation, error) {
	out := []*model.FileAssociation{}
	keys := make([]string, 0, len(m.rows))
	for k := range m.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cp := *m.rows[k]
		out = append(out, &cp)
	}
	return out, nil
}

func (m *memStore) PutFileAssociation(_ context.Context, a *model.FileAssociation) error {
	m.puts++
	cp := *a
	m.rows[a.Capability+"/"+a.Ext] = &cp
	return nil
}

func (m *memStore) DeleteFileAssociation(_ context.Context, c, e string) (bool, error) {
	_, ok := m.rows[c+"/"+e]
	delete(m.rows, c+"/"+e)
	return ok, nil
}

// src is a fixed set of app handlers.
type src struct{ open, thumb []AppHandler }

func (s src) OpenHandlers() []AppHandler      { return s.open }
func (s src) ThumbnailHandlers() []AppHandler { return s.thumb }

func app(id, version string, ext []string, mime ...string) AppHandler {
	return AppHandler{Handler: Handler{ID: id, App: AppOf(id), Version: version}, Ext: ext, Mime: mime}
}

func ids(hs []Handler) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

func service(t *testing.T) (*Service, *memStore) {
	t.Helper()
	m := newMem()
	s := New(m)
	s.SetSource(src{
		open: []AppHandler{
			app("app:drawio/editor", "1.0", []string{"drawio"}),
			app("app:zeta/viewer", "2.0", []string{"drawio", "svg"}),
		},
		thumb: []AppHandler{
			app("app:pkglist", "0.1.0", []string{"jar", "apk"}),
			app("app:pngplus", "3.1", nil, "image/*"),
		},
	})
	s.SetBuiltinThumb(func(name, mime string) bool {
		e := ExtOf(name)
		return e == "png" || e == "svg" || e == "zip"
	})
	return s, m
}

func TestApply_OffOrderAndTheRest(t *testing.T) {
	avail := []Handler{{ID: "app:a/v"}, {ID: "app:b/v"}, {ID: Builtin}, {ID: "app:c/v"}}
	on, off := Apply(avail, nil)
	assert.Equal(t, []string{"app:a/v", "app:b/v", Builtin, "app:c/v"}, ids(on), "no rule: the default order")
	assert.Empty(t, off)

	on, off = Apply(avail, &Rule{Order: []string{Builtin, "app:c/v", "app:gone/v"}, Off: []string{"app:a/v"}})
	assert.Equal(t, []string{Builtin, "app:c/v", "app:b/v"}, ids(on), "named first, in the rule's order; the rest after, in the default order; an unknown id is ignored")
	assert.Equal(t, []string{"app:a/v"}, ids(off))

	// A rule that names a handler AND switches it off (Put cleans that, but a
	// row is read as it is stored): off wins, and it is never asked.
	on, off = Apply(avail, &Rule{Order: []string{"app:a/v", Builtin}, Off: []string{"app:a/v"}})
	assert.Equal(t, []string{Builtin, "app:b/v", "app:c/v"}, ids(on))
	assert.Equal(t, []string{"app:a/v"}, ids(off))
}

func TestDefaultOrder(t *testing.T) {
	apps := []Handler{{ID: "app:a/v"}}
	assert.Equal(t, []string{"app:a/v", Builtin}, ids(DefaultOrder(CapOpen, true, apps)), "open: apps first, filex's viewer last")
	assert.Equal(t, []string{Builtin, "app:a"}, ids(DefaultOrder(CapThumbnail, true, []Handler{{ID: "app:a"}})), "thumbnail: filex first when it draws the kind")
	assert.Equal(t, []string{"app:a"}, ids(DefaultOrder(CapThumbnail, false, []Handler{{ID: "app:a"}})), "a kind filex does not draw has no built-in handler")
}

func TestChain_ByKindAndRule(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	ch := s.Chain(ctx, CapOpen, "a/b/diagram.DrawIO", "")
	assert.Equal(t, []string{"app:drawio/editor", "app:zeta/viewer", Builtin}, ids(ch.On))
	assert.False(t, ch.Custom)

	_, err := s.Put(ctx, CapOpen, "drawio", Rule{Order: []string{Builtin}, Off: []string{"app:zeta/viewer"}}, nil)
	require.NoError(t, err)
	ch = s.Chain(ctx, CapOpen, "diagram.drawio", "")
	assert.Equal(t, []string{Builtin, "app:drawio/editor"}, ids(ch.On))
	assert.Equal(t, []string{"app:zeta/viewer"}, ids(ch.Off))
	assert.True(t, ch.Custom)
	assert.False(t, s.OpenAllowed(ctx, "x.drawio", "app:zeta/viewer"), "switched off for the kind")
	assert.True(t, s.OpenAllowed(ctx, "x.svg", "app:zeta/viewer"), "the rule is per kind")
	assert.True(t, s.OpenAllowed(ctx, "x.drawio", "app:drawio/editor"))

	th := s.Chain(ctx, CapThumbnail, "photo.png", "image/png")
	assert.Equal(t, []string{Builtin, "app:pngplus"}, ids(th.On), "a media-type family matches; filex draws first")
	th = s.Chain(ctx, CapThumbnail, "lib.jar", "application/zip")
	assert.Equal(t, []string{"app:pkglist"}, ids(th.On), "filex draws no .jar: the app alone")
	assert.Empty(t, s.Chain(ctx, CapThumbnail, "model.stl", "").On, "nobody draws it")
}

func TestChain_AllOffIsNotNobody(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	_, err := s.Put(ctx, CapThumbnail, "jar", Rule{Off: []string{"app:pkglist"}}, nil)
	require.NoError(t, err)
	ch := s.Chain(ctx, CapThumbnail, "lib.jar", "")
	assert.True(t, ch.AllOff(), "handlers exist and every one is off")
	assert.False(t, s.Chain(ctx, CapThumbnail, "model.stl", "").AllOff(), "a kind nobody draws is not 'all off'")
}

func TestPut_RefusesWhatTheKindDoesNotHave(t *testing.T) {
	ctx := context.Background()
	s, m := service(t)
	for name, r := range map[string]Rule{
		"unknown app":        {Order: []string{"app:nope/x"}},
		"wrong shape (open)": {Order: []string{"app:drawio"}},
		"another kind's app": {Order: []string{"app:pkglist/x"}},
		"garbage":            {Off: []string{"drawio"}},
	} {
		_, err := s.Put(ctx, CapOpen, "drawio", r, nil)
		var bad *ErrInvalid
		assert.ErrorAs(t, err, &bad, name)
	}
	_, err := s.Put(ctx, CapThumbnail, "jar", Rule{Order: []string{Builtin}}, nil)
	assert.Error(t, err, "filex draws no .jar, so builtin is not one of its thumbnail handlers")
	_, err = s.Put(ctx, CapOpen, "Draw.IO", Rule{}, nil)
	assert.Error(t, err, "a kind is lower-case, no dot")
	_, err = s.Put(ctx, "print", "drawio", Rule{}, nil)
	assert.Error(t, err)
	assert.Zero(t, m.puts, "nothing refused was stored")
}

func TestPlace_FirstLastOff(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	require.NoError(t, s.Place(ctx, Placement{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceFirst}, nil))
	assert.Equal(t, []string{"app:zeta/viewer", "app:drawio/editor", Builtin}, ids(s.Chain(ctx, CapOpen, "a.drawio", "").On))
	require.NoError(t, s.Place(ctx, Placement{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceLast}, nil))
	assert.Equal(t, []string{"app:drawio/editor", Builtin, "app:zeta/viewer"}, ids(s.Chain(ctx, CapOpen, "a.drawio", "").On))
	require.NoError(t, s.Place(ctx, Placement{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceOff}, nil))
	ch := s.Chain(ctx, CapOpen, "a.drawio", "")
	assert.Equal(t, []string{"app:drawio/editor", Builtin}, ids(ch.On))
	assert.Equal(t, []string{"app:zeta/viewer"}, ids(ch.Off))
	assert.Error(t, s.Place(ctx, Placement{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: "middle"}, nil))
}

func TestPruneApp_TakesItsHandlersOutOfEveryRule(t *testing.T) {
	ctx := context.Background()
	s, m := service(t)
	_, err := s.Put(ctx, CapOpen, "drawio", Rule{Order: []string{"app:zeta/viewer", Builtin}, Off: []string{"app:drawio/editor"}}, nil)
	require.NoError(t, err)
	_, err = s.Put(ctx, CapThumbnail, "jar", Rule{Off: []string{"app:pkglist"}}, nil)
	require.NoError(t, err)
	require.NoError(t, s.PruneApp(ctx, "drawio"))
	require.NoError(t, s.PruneApp(ctx, "pkglist"))
	r := m.rows["open/drawio"]
	require.NotNil(t, r)
	assert.Equal(t, []string{"app:zeta/viewer", Builtin}, r.Handlers)
	assert.Empty(t, r.Off)
	assert.Nil(t, m.rows["thumbnail/jar"], "a rule left with nothing goes")
}

func TestKinds_ListsAppKindsAndRules(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	_, err := s.Put(ctx, CapThumbnail, "png", Rule{Order: []string{"app:pngplus"}}, nil)
	require.NoError(t, err)
	got := map[string]Kind{}
	for _, k := range s.Kinds(ctx) {
		got[k.Ext] = k
	}
	for _, e := range []string{"drawio", "svg", "jar", "apk", "png", "jpg", "webp"} {
		assert.Contains(t, got, e)
	}
	assert.NotContains(t, got, "pdf", "only filex handles it: nothing to choose")
	assert.True(t, got["png"].Thumbnail.Custom)
	assert.Equal(t, []string{"app:pngplus", Builtin}, ids(got["png"].Thumbnail.On))
	assert.Equal(t, "image/png", got["png"].Mime)
}

func TestInstallKinds_SayWhereTheAppLands(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	open := []AppHandler{app("app:new/view", "1", []string{"drawio"})}
	thumb := []AppHandler{app("app:new", "1", []string{"png", "rar"})}
	rows := s.InstallKinds(ctx, open, thumb)
	byKey := map[string]InstallKind{}
	for _, r := range rows {
		byKey[r.Capability+"/"+r.Ext] = r
	}
	require.Len(t, byKey, 3)
	assert.Equal(t, PlaceLast, byKey["open/drawio"].Default, "apps by name: drawio comes before new")
	assert.Equal(t, []string{"app:drawio/editor", "app:zeta/viewer", Builtin}, ids(byKey["open/drawio"].Current))
	assert.Equal(t, PlaceLast, byKey["thumbnail/png"].Default, "filex draws .png first")
	assert.Equal(t, PlaceFirst, byKey["thumbnail/rar"].Default, "nobody draws .rar")
}

func TestExtOfMatchesAndMimes(t *testing.T) {
	assert.Equal(t, "gz", ExtOf("dir/a.tar.GZ"))
	assert.Equal(t, "", ExtOf(".bashrc"))
	assert.Equal(t, "", ExtOf("noext"))
	assert.Equal(t, "", ExtOf("trailing."))
	assert.True(t, app("app:x", "", nil, "image/*").Matches("", "image/png; charset=x"))
	assert.False(t, app("app:x", "", nil, "image/*").Matches("", "video/mp4"))
	assert.Nil(t, ExtsOfMime("*/*"), "no app claims every kind")
	assert.Contains(t, ExtsOfMime("image/*"), "heic")
	assert.Equal(t, []string{"jar"}, ExtsOfMime("application/java-archive"))
	assert.True(t, ValidID(CapOpen, "app:drawio/editor"))
	assert.False(t, ValidID(CapOpen, "app:drawio"))
	assert.True(t, ValidID(CapThumbnail, "app:drawio"))
	assert.False(t, ValidID(CapThumbnail, "app:drawio/editor"))
	assert.Equal(t, "app:pkglist@0.1.0", Handler{ID: "app:pkglist", Version: "0.1.0"}.Key())
	assert.Equal(t, Builtin, Handler{ID: Builtin, Version: "x"}.Key())
}

// The document server is a thumbnail handler of filex's own (0.50): first
// for the office kinds it draws while OnlyOffice is configured, before any
// app; not a handler at all while it is not; never an opener. The
// administrator reorders it and switches it off like any other.
func TestAssoc_OnlyOfficeFirstForOfficeKinds(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	s.SetSource(src{thumb: []AppHandler{app("app:officeplus", "1.0", []string{"docx", "pdf"})}})
	s.SetBuiltinThumb(func(name, _ string) bool { return ExtOf(name) == "pdf" })
	configured := true
	s.SetOnlyOfficeThumb(func(name, _ string) bool { return configured && ExtOf(name) == "docx" })

	assert.Equal(t, []string{OnlyOffice, "app:officeplus"}, ids(s.Chain(ctx, CapThumbnail, "rapor.docx", "").On),
		"the document server first, the app after")
	assert.Equal(t, []string{Builtin, "app:officeplus"}, ids(s.Chain(ctx, CapThumbnail, "rapor.pdf", "").On),
		"a PDF stays filex's own")
	assert.NotContains(t, ids(s.Chain(ctx, CapOpen, "rapor.docx", "").On), OnlyOffice, "it draws, it does not open")

	assert.True(t, ValidID(CapThumbnail, OnlyOffice))
	// 0.51: also an open handler's shape (a .csv, onlyoffice_open_test.go);
	// which kinds it opens is OnlyOfficeOpens, and a docx is not one.
	assert.True(t, ValidID(CapOpen, OnlyOffice))
	assert.True(t, Handler{ID: OnlyOffice}.IsOnlyOffice())
	assert.False(t, Handler{ID: OnlyOffice}.IsApp())
	assert.False(t, Handler{ID: OnlyOffice}.IsBuiltin())
	assert.Equal(t, OnlyOffice, Handler{ID: OnlyOffice}.Key())

	// The administrator puts the app first, then switches the document
	// server off for the kind.
	_, err := s.Put(ctx, CapThumbnail, "docx", Rule{Order: []string{"app:officeplus", OnlyOffice}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"app:officeplus", OnlyOffice}, ids(s.Chain(ctx, CapThumbnail, "rapor.docx", "").On))
	_, err = s.Put(ctx, CapThumbnail, "docx", Rule{Off: []string{OnlyOffice}}, nil)
	require.NoError(t, err)
	ch := s.Chain(ctx, CapThumbnail, "rapor.docx", "")
	assert.Equal(t, []string{"app:officeplus"}, ids(ch.On))
	assert.Equal(t, []string{OnlyOffice}, ids(ch.Off))

	// Not configured: not a handler, and a rule naming it is refused as one
	// the kind does not have here.
	_, err = s.Reset(ctx, CapThumbnail, "docx")
	require.NoError(t, err)
	configured = false
	assert.Equal(t, []string{"app:officeplus"}, ids(s.Chain(ctx, CapThumbnail, "rapor.docx", "").On))
	_, err = s.Put(ctx, CapThumbnail, "docx", Rule{Order: []string{OnlyOffice}}, nil)
	var inv *ErrInvalid
	require.ErrorAs(t, err, &inv)

	// ThumbDefault is the order the pipeline uses without apps, too.
	assert.Equal(t, []string{OnlyOffice, Builtin}, ids(ThumbDefault(true, true, nil)))
	assert.Equal(t, []string{Builtin}, ids(ThumbDefault(false, true, nil)))
	assert.Empty(t, ThumbDefault(false, false, nil))
}
