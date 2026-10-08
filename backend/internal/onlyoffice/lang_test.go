package onlyoffice

// The editor's language (GitHub Discussion #93, task #214): the server
// chooses it, for every screen that opens the editor. Before, the config said
// whatever the request named, else "en", and no screen named anything, so
// ONLYOFFICE was English for everybody - every test here that reads an editor
// config is red on that code (and lang.go did not exist).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The order: the request's own language, then the screen's (Accept-Language),
// then the account's, then the instance's, then English.
func TestChooseEditorLocale_TheOrder(t *testing.T) {
	const screen = "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7"
	cases := []struct {
		name                            string
		requested, accept, account, def string
		want                            EditorLocale
	}{
		{"the screen's language", "", screen, "de", "fr", EditorLocale{"tr", "tr-TR"}},
		{"the request's own language first", "es", screen, "de", "fr", EditorLocale{"es", "es-ES"}},
		{"the account's when the request says nothing", "", "", "de", "fr", EditorLocale{"de", "de-DE"}},
		{"the instance's when the person has none", "", "", "", "fr", EditorLocale{"fr", "fr-FR"}},
		{"English when nobody says anything", "", "", "", "", EditorLocale{"en", "en-US"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ChooseEditorLocale(EditorLangAuto, c.requested, c.accept, c.account, c.def))
			assert.Equal(t, c.want, ChooseEditorLocale("", c.requested, c.accept, c.account, c.def), "no setting is auto")
		})
	}
}

// The administrator's fixed language is the answer for everybody, whatever
// the person's screen, account or request says.
func TestChooseEditorLocale_AFixedLanguageBeatsThePersons(t *testing.T) {
	got := ChooseEditorLocale("de", "tr", "tr-TR,tr;q=0.9", "tr", "tr")
	assert.Equal(t, EditorLocale{"de", "de-DE"}, got)

	got = ChooseEditorLocale("pt-PT", "en", "en-US", "en", "")
	assert.Equal(t, EditorLocale{"pt-PT", "pt-PT"}, got)

	// A stored value ONLYOFFICE does not offer (a hand-edited row) is "auto":
	// the person's own language is a better guess than English.
	got = ChooseEditorLocale("klingon", "", "tr-TR", "", "")
	assert.Equal(t, EditorLocale{"tr", "tr-TR"}, got)
}

// A language ONLYOFFICE does not offer is passed over for the next one the
// person gave; with none left, English.
func TestChooseEditorLocale_ALanguageTheEditorDoesNotOfferFallsThrough(t *testing.T) {
	// Persian: a filex language pack can speak it, ONLYOFFICE does not.
	assert.Equal(t, EditorLocale{"de", "de-DE"}, ChooseEditorLocale(EditorLangAuto, "fa", "fa-IR,fa;q=0.9,de;q=0.5", "", ""))
	assert.Equal(t, EditorLocale{"es", "es-ES"}, ChooseEditorLocale(EditorLangAuto, "fa", "fa-IR", "es", ""))
	assert.Equal(t, EditorLocale{"en", "en-US"}, ChooseEditorLocale(EditorLangAuto, "fa", "fa-IR,fa;q=0.9", "", "fa"))
	// Not a language at all.
	assert.Equal(t, EditorLocale{"en", "en-US"}, ChooseEditorLocale(EditorLangAuto, "!!", "*", "", ""))
}

// A regional or older tag reaches the language ONLYOFFICE offers for it, and
// a region ONLYOFFICE lists is kept for the spreadsheet's formats.
func TestMatchEditorLang_TheNearestLanguageAndItsRegion(t *testing.T) {
	cases := map[string]EditorLocale{
		"tr":         {"tr", "tr-TR"},
		"TR":         {"tr", "tr-TR"},
		"tr-TR":      {"tr", "tr-TR"},
		"de-AT":      {"de", "de-AT"},
		"de-LU":      {"de", "de-DE"},
		"en-GB":      {"en", "en-GB"},
		"en":         {"en", "en-US"},
		"es-MX":      {"es", "es-MX"},
		"fr-CA":      {"fr", "fr-FR"},
		"pt":         {"pt", "pt-BR"},
		"pt-br":      {"pt", "pt-BR"},
		"pt_BR":      {"pt", "pt-BR"},
		"pt-PT":      {"pt-PT", "pt-PT"},
		"pt-AO":      {"pt-PT", "pt-PT"},
		"zh":         {"zh", "zh-CN"},
		"zh-CN":      {"zh", "zh-CN"},
		"zh-TW":      {"zh-TW", "zh-TW"},
		"zh-HK":      {"zh-TW", "zh-TW"},
		"zh-Hant":    {"zh-TW", "zh-TW"},
		"sr":         {"sr", "sr-Latn-RS"},
		"sr-Latn-RS": {"sr", "sr-Latn-RS"},
		"sr-Cyrl":    {"sr-Cyrl", "sr-Cyrl-RS"},
		"sr-Cyrl-RS": {"sr-Cyrl", "sr-Cyrl-RS"},
		"nb-NO":      {"no", ""},
		"nn":         {"no", ""},
		"ar":         {"ar", ""},
		"ar-SA":      {"ar", "ar-SA"},
		"az-AZ":      {"az", "az-Latn-AZ"},
		"sv-FI":      {"sv", "sv-FI"},
	}
	for tag, want := range cases {
		got, ok := matchEditorLang(tag)
		if assert.True(t, ok, tag) {
			assert.Equal(t, want, got, tag)
		}
	}
	for _, tag := range []string{"", "*", "auto", "fa", "fa-IR", "x-klingon", "123", "-"} {
		_, ok := matchEditorLang(tag)
		assert.False(t, ok, "%q is no language the editor offers", tag)
	}
}

func TestNormalizeEditorLang(t *testing.T) {
	for in, want := range map[string]string{
		"": EditorLangAuto, " auto ": EditorLangAuto, "AUTO": EditorLangAuto,
		"de": "de", "DE": "de", "de-DE": "de", "de_DE": "de",
		"pt_pt": "pt-PT", "zh-tw": "zh-TW", "sr-cyrl": "sr-Cyrl", "nb": "no",
	} {
		got, ok := NormalizeEditorLang(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"fa", "klingon", "de;rm -rf", "<b>"} {
		_, ok := NormalizeEditorLang(bad)
		assert.False(t, ok, bad)
	}
}

// Accept-Language as a browser writes it: in order, the wildcard and a
// refused tag (q=0) left out.
func TestAcceptLanguageTags(t *testing.T) {
	assert.Equal(t, []string{"tr-TR", "tr", "en"},
		acceptLanguageTags("tr-TR, tr;q=0.9, *;q=0.5, de;q=0, en;q=0.1"))
	assert.Empty(t, acceptLanguageTags(""))
}

// The administrator's list is ONLYOFFICE's: every entry is one the editor
// takes as itself, named, once, with a regional setting ONLYOFFICE lists.
func TestEditorLanguages_EveryEntryIsOneTheEditorTakes(t *testing.T) {
	list := EditorLanguages()
	require.Len(t, list, 46, "ONLYOFFICE's Docs API reference lists 46 interface languages")
	seen := map[string]bool{}
	for _, l := range list {
		assert.False(t, seen[l.Code], "%s twice", l.Code)
		seen[l.Code] = true
		assert.NotEmpty(t, strings.TrimSpace(l.Name), l.Code)
		got, ok := NormalizeEditorLang(l.Code)
		assert.True(t, ok, l.Code)
		assert.Equal(t, l.Code, got, "a listed code is stored as itself")
	}
	for _, l := range editorLanguages {
		if l.region != "" {
			_, ok := regionByLower[strings.ToLower(l.region)]
			assert.True(t, ok, "%s's region %s is not one ONLYOFFICE lists", l.code, l.region)
		}
	}
	for _, code := range []string{"tr", "de", "fr", "es", "pt-PT", "zh-TW", "sr-Cyrl", "ar"} {
		assert.True(t, seen[code], code)
	}
}

func editorConfigOf(t *testing.T, svc *Service, user *model.User, lang string, opts ...ConfigOption) map[string]any {
	t.Helper()
	cfg, err := svc.BuildConfigForNode(context.Background(),
		&model.Node{ID: 31, Name: "rapor.docx", PathHash: "h", Size: 10}, user, lang, "view", opts...)
	require.NoError(t, err)
	ec, ok := cfg.Config["editorConfig"].(map[string]any)
	require.True(t, ok)
	return ec
}

// The editor config carries the server's choice: the screen's language with
// its region, the administrator's fixed one over it, the account's when the
// request says nothing - and the config is signed over it (the token is made
// after).
func TestBuildConfigForNode_TheEditorSpeaksThePersonsLanguage(t *testing.T) {
	svc := New(nil, nil, "https://docs.example", "shh", "https://filex.example", time.Hour)

	ec := editorConfigOf(t, svc, nil, "", WithAcceptLanguage("tr-TR,tr;q=0.9,en;q=0.8"))
	assert.Equal(t, "tr", ec["lang"], "the screen spoke Turkish")
	assert.Equal(t, "tr-TR", ec["region"])

	ec = editorConfigOf(t, svc, &model.User{ID: 4, Email: "a@example.com", Locale: "fr"}, "")
	assert.Equal(t, "fr", ec["lang"], "the account's language when the request names none")
	assert.Equal(t, "fr-FR", ec["region"])

	svc.DefaultLocale = "de"
	ec = editorConfigOf(t, svc, nil, "")
	assert.Equal(t, "de", ec["lang"], "the instance's language")

	svc.LiveEditorLang = func(context.Context) string { return "es" }
	ec = editorConfigOf(t, svc, &model.User{ID: 4, Locale: "fr"}, "tr", WithAcceptLanguage("tr-TR"))
	assert.Equal(t, "es", ec["lang"], "the administrator's fixed language is everybody's")
	assert.Equal(t, "es-ES", ec["region"])

	svc.LiveEditorLang = func(context.Context) string { return EditorLangAuto }
	ec = editorConfigOf(t, svc, nil, "", WithAcceptLanguage("ar-SA"))
	assert.Equal(t, "ar", ec["lang"])
	assert.Equal(t, "ar-SA", ec["region"])

	ec = editorConfigOf(t, svc, nil, "", WithAcceptLanguage("nb-NO"))
	assert.Equal(t, "no", ec["lang"])
	assert.NotContains(t, ec, "region", "no regional setting of its own: ONLYOFFICE derives one")
}
