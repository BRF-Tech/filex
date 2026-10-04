package assoc

// filex 0.51 (GitHub #81): a .csv opens in ONLYOFFICE while it is configured,
// filex's own read-only table second; without ONLYOFFICE, the table alone.
// The administrator's Default apps and the person's "Always open with" may
// name ONLYOFFICE for a .csv, and a rule naming it keeps working (falls to
// the table) when ONLYOFFICE is switched off.
//
// ⚠ Every test here fails on the 0.50 code: ONLYOFFICE was a thumbnail
// handler only (ValidID refused it for open, the open chain never held it,
// Default apps did not list .csv).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// csvService is a service whose document server opens a .csv while
// `configured` says so, with or without an app that opens .csv too.
func csvService(t *testing.T, configured *bool, apps ...AppHandler) *Service {
	t.Helper()
	s := New(newMem())
	s.SetSource(src{open: apps})
	s.SetOnlyOfficeOpen(func(string, string) bool { return *configured })
	return s
}

func TestOnlyOfficeOpens_CSVOnly(t *testing.T) {
	assert.True(t, OnlyOfficeOpens("csv"))
	for _, k := range []string{"tsv", "xlsx", "docx", "txt", ""} {
		assert.False(t, OnlyOfficeOpens(k), k)
	}
	assert.Equal(t, []string{"csv"}, OnlyOfficeOpenKinds())
}

func TestOpenChain_CSVOpensInOnlyOfficeFirst(t *testing.T) {
	ctx := context.Background()
	on := true
	s := csvService(t, &on)
	assert.Equal(t, []string{OnlyOffice, Builtin}, ids(s.Chain(ctx, CapOpen, "liste.csv", "text/csv").On),
		"ONLYOFFICE first, filex's table second")
	assert.Equal(t, []string{Builtin}, ids(s.Chain(ctx, CapOpen, "rapor.docx", "").On),
		"an office document is not opened through the chain")
	assert.Equal(t, []string{Builtin}, ids(s.Chain(ctx, CapOpen, "liste.tsv", "").On), "only .csv")

	on = false
	assert.Equal(t, []string{Builtin}, ids(s.Chain(ctx, CapOpen, "liste.csv", "text/csv").On),
		"without ONLYOFFICE the table alone")
}

func TestOpenChain_AnAppForCSVComesAfterOnlyOffice(t *testing.T) {
	ctx := context.Background()
	on := true
	s := csvService(t, &on, app("app:sheet/view", "1.0", []string{"csv"}))
	assert.Equal(t, []string{OnlyOffice, "app:sheet/view", Builtin}, ids(s.Chain(ctx, CapOpen, "a.csv", "").On))
	assert.Equal(t, []string{OnlyOffice, "app:sheet/view", Builtin}, ids(OpenDefault(true, []Handler{{ID: "app:sheet/view"}})))
	assert.Equal(t, []string{"app:sheet/view", Builtin}, ids(OpenDefault(false, []Handler{{ID: "app:sheet/view"}})))

	// An install that does not choose lands the app after ONLYOFFICE.
	k := s.InstallKinds(ctx, []AppHandler{app("app:other/view", "1.0", []string{"csv"})}, nil)
	require.Len(t, k, 1)
	assert.Equal(t, PlaceLast, k[0].Default)
	assert.Equal(t, []string{OnlyOffice, "app:sheet/view", Builtin}, ids(k[0].Current))
}

func TestDefaultApps_AcceptsOnlyOfficeForCSV(t *testing.T) {
	ctx := context.Background()
	on := true
	s := csvService(t, &on)

	// The administrator puts the table first: ONLYOFFICE second.
	_, err := s.Put(ctx, CapOpen, "csv", Rule{Order: []string{Builtin, OnlyOffice}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{Builtin, OnlyOffice}, ids(s.Chain(ctx, CapOpen, "a.csv", "").On))

	// ...then switches ONLYOFFICE off for .csv.
	_, err = s.Put(ctx, CapOpen, "csv", Rule{Order: []string{Builtin}, Off: []string{OnlyOffice}}, nil)
	require.NoError(t, err)
	ch := s.Chain(ctx, CapOpen, "a.csv", "")
	assert.Equal(t, []string{Builtin}, ids(ch.On))
	assert.Equal(t, []string{OnlyOffice}, ids(ch.Off))

	// ONLYOFFICE is not a .docx open handler, nor one while unconfigured.
	var inv *ErrInvalid
	_, err = s.Put(ctx, CapOpen, "docx", Rule{Order: []string{OnlyOffice}}, nil)
	require.ErrorAs(t, err, &inv)
	on = false
	_, err = s.Put(ctx, CapOpen, "csv", Rule{Order: []string{OnlyOffice}}, nil)
	require.ErrorAs(t, err, &inv)
	assert.Contains(t, inv.Message, "does not open .csv files here")
}

func TestDefaultApps_ARuleNamingOnlyOfficeFallsBackWhenItIsOff(t *testing.T) {
	ctx := context.Background()
	on := true
	s := csvService(t, &on)
	_, err := s.Put(ctx, CapOpen, "csv", Rule{Order: []string{OnlyOffice, Builtin}}, nil)
	require.NoError(t, err)

	// The administrator switches OnlyOffice off under External services: the
	// rule stays, nothing errors, the table opens the file.
	on = false
	ch := s.Chain(ctx, CapOpen, "a.csv", "")
	assert.Equal(t, []string{Builtin}, ids(ch.On))
	assert.Empty(t, ch.Off)
	assert.True(t, ch.Custom)
	assert.NotNil(t, s.Rule(ctx, CapOpen, "csv"), "the rule is kept for when it is back")

	on = true
	assert.Equal(t, []string{OnlyOffice, Builtin}, ids(s.Chain(ctx, CapOpen, "a.csv", "").On))
}

func TestKinds_ListsCSVWhileOnlyOfficeIsConfigured(t *testing.T) {
	ctx := context.Background()
	on := true
	s := csvService(t, &on)
	kinds := s.Kinds(ctx)
	require.Len(t, kinds, 1, "nothing but ONLYOFFICE and filex handle a kind here")
	assert.Equal(t, "csv", kinds[0].Ext)
	assert.Equal(t, "text/csv", kinds[0].Mime)
	assert.Equal(t, []string{OnlyOffice, Builtin}, ids(kinds[0].Open.On))
	assert.False(t, kinds[0].Open.Custom)

	on = false
	assert.Empty(t, s.Kinds(ctx), "no ONLYOFFICE, nothing to choose between")
}
