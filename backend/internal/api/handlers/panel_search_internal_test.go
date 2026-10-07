package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The panel search's fold is the client's (core lib/fileFilters foldText):
// accents and the Turkish letters to their base letter, the four i's one
// letter, case ignored. A server that folded less would leave out a row the
// client would have shown.
func TestPanelFold_TurkishLettersAndCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Kullanıcılar", "kullanicilar"},
		{"KULLANICILAR", "kullanicilar"},
		{"İSTANBUL", "istanbul"},
		{"Şirket Yönetimi", "sirket yonetimi"},
		{"Çağlayan", "caglayan"},
		{"Ödeme Güvenliği", "odeme guvenligi"},
		{"Convert", "convert"},
		// A decomposed spelling (macOS) folds the same.
		{"S\u0327irket", "sirket"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, panelFold(c.in), c.in)
	}
}

func TestPanelMatch_EveryWordSomewhereBestFirst(t *testing.T) {
	rows := []panelHit{
		{Kind: "user", ID: "1", Label: "Ada Lovelace", Detail: "ada@example.com"},
		{Kind: "user", ID: "2", Label: "Grace Hopper", Detail: "grace@example.com", Terms: []string{"ada-fan"}},
		{Kind: "user", ID: "3", Label: "Bob Convert", Detail: "bob@example.com"},
		{Kind: "app", ID: "convert", Label: "Convert", Labels: map[string]string{"en": "Convert", "tr": "Dönüştür"}},
	}
	got := panelMatch(rows, panelWords("ada"), 10)
	require.Len(t, got, 2)
	assert.Equal(t, "1", got[0].ID, "the label's start comes before a term")
	assert.Equal(t, "2", got[1].ID)

	got = panelMatch(rows, panelWords("ada lovelace"), 10)
	require.Len(t, got, 1, "every word has to be there")
	assert.Equal(t, "1", got[0].ID)

	got = panelMatch(rows, panelWords("grace example"), 10)
	require.Len(t, got, 1, "the detail line counts")
	assert.Equal(t, "2", got[0].ID)

	got = panelMatch(rows, panelWords("donustur"), 10)
	require.Len(t, got, 1, "an app answers to its label in every language it gave")
	assert.Equal(t, "convert", got[0].ID)

	got = panelMatch(rows, panelWords("convert"), 1)
	require.Len(t, got, 1, "the limit")
	assert.Equal(t, "convert", got[0].ID, "the label that starts with the word first")

	assert.Empty(t, panelMatch(rows, panelWords("nobody"), 10))
}

// A word never matches across two fields.
func TestPanelMatch_NotAcrossFields(t *testing.T) {
	rows := []panelHit{{Kind: "group", ID: "1", Label: "Sales", Detail: "team"}}
	assert.Empty(t, panelMatch(rows, panelWords("salesteam"), 10))
	assert.Len(t, panelMatch(rows, panelWords("sales team"), 10), 1)
}

// What a list answers becomes rows without a secret: a share's link token and
// address are not copied.
func TestPanelShares_NoToken(t *testing.T) {
	body := []byte(`{"items":[{"share":{"id":7,"token":"SECRET-TOKEN"},"url":"https://x/s/SECRET-TOKEN",` +
		`"node_path":"/Raporlar/q3.pdf","storage_name":"depo","creator_name":"Ada"}]}`)
	rows, err := panelShares(body, "en")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "q3.pdf", rows[0].Label)
	assert.Equal(t, "depo/Raporlar/q3.pdf", rows[0].Detail)
	assert.Equal(t, "7", rows[0].ID)
	for _, s := range append([]string{rows[0].Label, rows[0].Detail}, rows[0].Terms...) {
		assert.NotContains(t, s, "SECRET-TOKEN")
	}
}
