package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A link an app opened is listed for what it IS — the owner's decision of
// 2026-09-21: signing links sat in My shares as plain `/teklif.pdf` shares
// offering "Revoke", with no hint that revoking one cancelled a request.
func TestLinkOf_NamesAnAppLinkForWhatItIs(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		page := m["public_pages"].([]any)[0].(map[string]any)
		page["purpose"] = map[string]any{
			"label":   map[string]any{"en": "Signing request", "tr": "İmza isteği"},
			"revoke":  map[string]any{"en": "Revoking this link cancels the signing request.", "tr": "Bu bağlantıyı iptal etmek imza isteğini iptal eder."},
			"section": "requested",
		}
		m["views"] = append(m["views"].([]any), map[string]any{
			"id": "inbox", "placement": "home", "label": map[string]any{"en": "Inbox", "tr": "Gelenler"},
		})
	}))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	_, _, sh := h.invite(t, p, "")

	got := h.reg.LinkOf(sh)
	require.NotNil(t, got)
	assert.Equal(t, "echo", got.Plugin)
	assert.Equal(t, "signer", got.Page)
	assert.Equal(t, "İmza isteği", got.Label["tr"])
	assert.Equal(t, "Revoking this link cancels the signing request.", got.Revoke["en"])
	assert.Equal(t, "inbox", got.View, "the row opens the app's own list")
	assert.Equal(t, "requested", got.Section)

	assert.Nil(t, h.reg.LinkOf(&model.Share{ID: 99, NodeID: 1}), "an ordinary share is a share")
	assert.Nil(t, h.reg.LinkOf(&model.Share{ID: 99, PluginID: 12345, PageID: "signer"}), "an app that is gone names nothing")
}

func TestManifest_PagePurposeIsValidated(t *testing.T) {
	h := newHarness(t, nil)
	_, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["public_pages"].([]any)[0].(map[string]any)["purpose"] = map[string]any{"label": map[string]any{"en": "Only English"}}
	}))
	require.Error(t, err, "a purpose speaks every language the app does")
}

// A page-less link — an app's plain share of a file, like the finished
// document handed to everybody — has no page to declare a purpose, so the app
// gives it one when it opens it. One mechanism for both kinds of link: the
// link's own purpose wins, the page's is the default.
func TestLinkOf_APageLessLinkCarriesThePurposeItWasOpenedWith(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["views"] = append(m["views"].([]any), map[string]any{
			"id": "inbox", "placement": "home", "label": map[string]any{"en": "Inbox", "tr": "Gelenler"},
		})
	}))
	require.NoError(t, err)
	p, _ := h.reg.ByID(st.ID)
	h.writeCatalogued(t, "docs/contract-signed.txt", "signed")
	s := h.jobScope(t, p, h.actor(t), "docs/contract-signed.txt")

	purpose := map[string]any{
		"label":   map[string]any{"en": "Signed copy", "tr": "İmzalı kopya"},
		"revoke":  map[string]any{"en": "Revoking this link takes the signed copy away.", "tr": "Bu bağlantıyı iptal etmek imzalı kopyayı geri alır."},
		"section": "signed",
	}
	out, err := shareCreate(t, s, map[string]any{"subject": "contract-signed.txt", "purpose": purpose})
	require.NoError(t, err)
	sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	require.False(t, sh.IsApp(), "still a plain share of the file")
	got := h.reg.LinkOf(sh)
	require.NotNil(t, got, "a page-less link is named for what it is")
	assert.Equal(t, "İmzalı kopya", got.Label["tr"])
	assert.Equal(t, "signed", got.Section)
	assert.Empty(t, got.Page)

	// Without one it is what it always was: a share.
	out, err = shareCreate(t, s, map[string]any{"subject": "contract-signed.txt"})
	require.NoError(t, err)
	plain, _ := h.store.GetShareByToken(context.Background(), out["token"].(string))
	assert.Nil(t, h.reg.LinkOf(plain))

	// The same rule a manifest page follows: every declared language.
	_, err = shareCreate(t, s, map[string]any{"subject": "x", "purpose": map[string]any{"label": map[string]any{"en": "Signed copy"}}})
	require.Error(t, err, "a purpose missing a declared language is refused")
	_, err = shareCreate(t, s, map[string]any{"subject": "x", "purpose": map[string]any{"label": map[string]any{"en": "A", "tr": "B"}, "section": "Not An Id"}})
	require.Error(t, err, "a section that is not an id is refused")
}
