package handlers_test

/* The bug this locks: the PIN gate rendered in English and the page behind it
   in Turkish, so entering a PIN changed the language of the share. Every public
   page must answer in ONE language, and it must be the visitor's. */

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// pinShare builds a PIN-protected folder share and returns the handler.
func pinShare(t *testing.T, defaultLocale string) (*handlers.Share, *model.Share, string) {
	t.Helper()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }
	ctx := context.Background()

	require.NoError(t, drv.Mkdir(ctx, "album"))
	require.NoError(t, drv.Write(ctx, "album/a.txt", strings.NewReader("hi"), 2))
	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "album", Path: "album",
		PathHash: mutTestPathHash(st.ID, "album"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	svc := share.NewService(store)
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: node.ID, PIN: "1234"})
	require.NoError(t, err)

	h := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	h.AttachLocale(defaultLocale)
	return h, sh, "1234"
}

// publicGet renders one public page and returns its HTML. Mirrors browseGet in
// share_browse_test.go, plus the Accept-Language header these tests turn on.
func publicGet(h *handlers.Share, token, query, acceptLang string) string {
	req := httptest.NewRequest("GET", "/s/"+token+query, nil)
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)
	return rec.Body.String()
}

// TestPublicPages_OneLanguagePerVisitor: the gate and the page behind it agree.
func TestPublicPages_OneLanguagePerVisitor(t *testing.T) {
	h, sh, pin := pinShare(t, "en")

	// ── Turkish browser ────────────────────────────────────────────
	gate := publicGet(h, sh.Token, "", "tr-TR,tr;q=0.9")
	require.Contains(t, gate, `<html lang="tr"`)
	require.Contains(t, gate, "Bu paylaşım PIN korumalı")
	require.NotContains(t, gate, "This share is PIN-protected")

	behind := publicGet(h, sh.Token, "?pin="+pin, "tr-TR,tr;q=0.9")
	require.Contains(t, behind, `<html lang="tr"`, "the page behind the gate must not switch language")
	require.Contains(t, behind, "Tümünü indir (ZIP)")

	// ── English browser ────────────────────────────────────────────
	gateEN := publicGet(h, sh.Token, "", "en-GB,en;q=0.9")
	require.Contains(t, gateEN, `<html lang="en"`)
	require.Contains(t, gateEN, "This share is PIN-protected")

	behindEN := publicGet(h, sh.Token, "?pin="+pin, "en-GB,en;q=0.9")
	require.Contains(t, behindEN, `<html lang="en"`)
	require.Contains(t, behindEN, "Download all (ZIP)")
	require.NotContains(t, behindEN, "Tümünü indir")
}

// An unsupported browser language falls back to the SERVER's default, and
// ?lang= overrides everything (how you send a link to somebody whose browser
// is set to neither).
func TestPublicPages_FallbackAndOverride(t *testing.T) {
	h, sh, _ := pinShare(t, "tr")

	de := publicGet(h, sh.Token, "", "de-DE,de;q=0.9")
	require.Contains(t, de, `<html lang="tr"`, "unsupported language falls back to the server default")

	forced := publicGet(h, sh.Token, "?lang=en", "tr-TR,tr;q=0.9")
	require.Contains(t, forced, `<html lang="en"`, "?lang= wins over the browser")
	require.Contains(t, forced, "This share is PIN-protected")
}

// A wrong PIN re-renders the gate — the error line must be in the same
// language as the page carrying it.
func TestPublicPages_WrongPinSpeaksTheSameLanguage(t *testing.T) {
	h, sh, _ := pinShare(t, "en")

	body := publicGet(h, sh.Token, "?pin=0000", "tr-TR,tr;q=0.9")
	require.Contains(t, body, `<html lang="tr"`)
	require.Contains(t, body, "PIN yanlış")
	require.NotContains(t, body, "Wrong PIN")
}
