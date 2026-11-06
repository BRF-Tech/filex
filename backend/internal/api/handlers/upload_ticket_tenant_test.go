package handlers_test

// An upload ticket is bound to the storage it was minted on - by the id that
// never moves, not by the name people read - and to the minter's tenant, and
// the anonymous redeem asks again, as the minter, before it writes a byte.
//
// The hole these tests close: the redeem used to resolve the storage NAME
// saved at the mint, with no tenant scope and a copy of the minting account
// taken at the mint. A platform operator re-linking that storage to another
// tenant, or deleting it and creating a storage of the same name for another
// tenant, while the ticket lived, turned the ticket into one write into the
// other tenant's file. A ticket whose destination is no longer the minter's
// is no good: the answer an unknown or used ticket gets, and nothing written.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
)

const ticketMarker = "CROSS_TENANT_MARKER"

// mintRedeemURL mints a ticket for dest with tok and returns the redeem URL on
// the fixture's own server (the minted one carries the configured public
// base, not the test server's address).
func mintRedeemURL(t *testing.T, f *mtFix, tok, dest string) string {
	t.Helper()
	info, code := mintTicket(t, f.Srv.Client(), f.URL, tok, map[string]any{"path": dest})
	require.Equal(t, http.StatusOK, code, "the minter may write %s, so it may mint for it", dest)
	u, _ := info["url"].(string)
	require.Contains(t, u, "/u/")
	return f.URL + u[strings.Index(u, "/u/"):]
}

// redeemTicket PUTs body to url with no credential and returns status + JSON body.
func redeemTicket(t *testing.T, f *mtFix, url, body string) (int, map[string]any) {
	t.Helper()
	resp := putNoAuth(t, f.Srv.Client(), url, body)
	defer resp.Body.Close()
	return resp.StatusCode, readBody(t, resp)
}

// assertTicketNoGood is the refusal a ticket whose destination moved gets: the
// one an unknown or already used ticket gets, so the redeemer learns nothing
// about where the storage went.
func assertTicketNoGood(t *testing.T, status int, body map[string]any, why string) {
	t.Helper()
	assert.Equal(t, http.StatusNotFound, status, why)
	assert.Equal(t, "ticket_not_found", body["error"], why)
}

func fileContent(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func aiFetch(t *testing.T, f *mtFix, tok, p string) (int, string) {
	t.Helper()
	resp := aiReq(t, f.Srv.Client(), http.MethodGet, f.URL+"/api/ai/download?path="+p, tok, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Control: in a multi-tenant instance, nothing moved, the ticket writes - as
// the minter, at the minted destination.
func TestUploadTicketTenant_ATicketNothingMovedUnderStillWrites(t *testing.T) {
	f := newMTFix(t, true)
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)

	url := mintRedeemURL(t, f, tokA, "alpha://private/report.txt")
	status, body := redeemTicket(t, f, url, "alpha's own bytes")
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "alpha's own bytes", fileContent(t, f.RootA, "private/report.txt"))

	status, got := aiFetch(t, f, tokA, "alpha://private/report.txt")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "alpha's own bytes", got)

	status, body = redeemTicket(t, f, url, "again")
	assertTicketNoGood(t, status, body, "single use, as before")
}

// The storage is re-linked from alpha to bravo while alpha's ticket lives.
func TestUploadTicketTenant_AStorageMovedToAnotherTenantTakesNoWrite(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)
	tokB := issueToken(t, f.Store, f.UserB, "read,write", nil)
	f.seedFile(t, f.StA, f.RootA, "private/victim.txt", "alpha's draft")

	url := mintRedeemURL(t, f, tokA, "alpha://private/victim.txt")

	// Before the move bravo cannot reach alpha's storage at all.
	status, _ := aiFetch(t, f, tokB, "alpha://private/victim.txt")
	require.NotEqual(t, http.StatusOK, status, "bravo reaches alpha's storage before the move")

	// The platform operator hands the storage to bravo.
	require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvA, f.StA.ID))
	require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvB, f.StA.ID))

	// The boundary moved with it: alpha's member no longer reads it, bravo's
	// member now owns it and writes its own content there.
	status, _ = aiFetch(t, f, tokA, "alpha://private/victim.txt")
	require.NotEqual(t, http.StatusOK, status, "alpha still reads the storage it gave away")
	resp := aiReq(t, f.Srv.Client(), http.MethodPost, f.URL+"/api/ai/upload", tokB,
		map[string]any{"path": "alpha://private/victim.txt", "content": "bravo's file"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	status, body := redeemTicket(t, f, url, ticketMarker)
	assertTicketNoGood(t, status, body, "alpha's ticket must not write into a storage that is bravo's now")
	assert.Equal(t, "bravo's file", fileContent(t, f.RootA, "private/victim.txt"),
		"bravo's file is untouched")
	_, got := aiFetch(t, f, tokB, "alpha://private/victim.txt")
	assert.Equal(t, "bravo's file", got)

	// Consumed, not merely refused: handing the storage back does not revive it.
	require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvB, f.StA.ID))
	require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvA, f.StA.ID))
	status, body = redeemTicket(t, f, url, ticketMarker)
	assertTicketNoGood(t, status, body, "a refused ticket is spent")
	assert.Equal(t, "bravo's file", fileContent(t, f.RootA, "private/victim.txt"))
}

// The storage is deleted and a new storage of the same name is created for
// bravo while alpha's ticket lives.
func TestUploadTicketTenant_AStorageReplacedUnderTheSameNameTakesNoWrite(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)
	tokB := issueToken(t, f.Store, f.UserB, "read,write", nil)

	url := mintRedeemURL(t, f, tokA, "alpha://private/victim.txt")

	require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvA, f.StA.ID))
	require.NoError(t, f.Store.DeleteStorage(ctx, f.StA.ID))
	stNew, rootNew := f.mountStorage(t, "alpha")
	require.NotEqual(t, f.StA.ID, stNew.ID)
	require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvB, stNew.ID))

	resp := aiReq(t, f.Srv.Client(), http.MethodPost, f.URL+"/api/ai/upload", tokB,
		map[string]any{"path": "alpha://private/victim.txt", "content": "bravo's file"})
	require.Equal(t, http.StatusOK, resp.StatusCode, "bravo owns the new storage named alpha")
	resp.Body.Close()

	status, body := redeemTicket(t, f, url, ticketMarker)
	assertTicketNoGood(t, status, body, "a ticket names the storage it was minted on, not whatever carries its name now")
	assert.Equal(t, "bravo's file", fileContent(t, rootNew, "private/victim.txt"))
	assert.Empty(t, fileContent(t, f.RootA, "private/victim.txt"), "nor anything in the deleted storage's root")
}

// A storage is renamed inside its own tenant: it keeps its id, and the ticket
// follows it. The destination is the storage, not the word it was called by.
func TestUploadTicketTenant_ARenamedStorageKeepsItsTicket(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)

	url := mintRedeemURL(t, f, tokA, "alpha://docs/notes.txt")

	st, err := f.Store.GetStorage(ctx, f.StA.ID)
	require.NoError(t, err)
	st.Name = "alpha-renamed"
	require.NoError(t, f.Store.UpdateStorage(ctx, st))

	status, body := redeemTicket(t, f, url, "notes")
	require.Equal(t, http.StatusOK, status, "%v", body)
	entry, _ := body["entry"].(map[string]any)
	require.NotNil(t, entry)
	assert.Equal(t, "alpha-renamed://docs/notes.txt", entry["path"], "the answer names the storage as it reads now")
	assert.Equal(t, "notes", fileContent(t, f.RootA, "docs/notes.txt"))
}

// The minter's account is asked again at the redeem: a disabled account, a
// suspended tenant, or a minter moved to another tenant spends the ticket and
// writes nothing.
func TestUploadTicketTenant_TheMinterIsAskedAgain(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *mtFix)
	}{
		{"account disabled", func(t *testing.T, f *mtFix) {
			require.NoError(t, f.Store.SetUserEnabled(context.Background(), f.UserA, false))
		}},
		{"tenant suspended", func(t *testing.T, f *mtFix) {
			ctx := context.Background()
			p, err := f.Store.GetProvider(ctx, f.ProvA)
			require.NoError(t, err)
			p.Enabled = false
			require.NoError(t, f.Store.UpdateProvider(ctx, p))
		}},
		// The storage goes too, so the minter could write there from its new
		// tenant: the ticket was minted for alpha's storage, and does not cross
		// the boundary with them.
		{"minter and storage moved to another tenant", func(t *testing.T, f *mtFix) {
			ctx := context.Background()
			require.NoError(t, f.Store.SetUserProvider(ctx, f.UserA, f.ProvB, ""))
			require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvA, f.StA.ID))
			require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvB, f.StA.ID))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMTFix(t, true)
			tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)
			url := mintRedeemURL(t, f, tokA, "alpha://private/late.txt")

			c.change(t, f)

			status, body := redeemTicket(t, f, url, ticketMarker)
			assertTicketNoGood(t, status, body, c.name)
			assert.Empty(t, fileContent(t, f.RootA, "private/late.txt"), "nothing written")
		})
	}
}

// Single-tenant mode has no tenants to cross, and keeps every rule above that
// is not about them: the storage by id, and the account asked again.
func TestUploadTicketTenant_SingleTenantKeepsTheStorageAndTheAccount(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)

	ok := mintRedeemURL(t, f, tokA, "alpha://docs/ok.txt")
	status, body := redeemTicket(t, f, ok, "fine")
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "fine", fileContent(t, f.RootA, "docs/ok.txt"))

	replaced := mintRedeemURL(t, f, tokA, "alpha://docs/replaced.txt")
	require.NoError(t, f.Store.DeleteStorage(ctx, f.StA.ID))
	_, rootNew := f.mountStorage(t, "alpha")
	status, body = redeemTicket(t, f, replaced, ticketMarker)
	assertTicketNoGood(t, status, body, "a replaced storage is another storage in any mode")
	assert.Empty(t, fileContent(t, rootNew, "docs/replaced.txt"))

	disabled := mintRedeemURL(t, f, tokA, "alpha-arsiv://late.txt")
	require.NoError(t, f.Store.SetUserEnabled(ctx, f.UserA, false))
	status, body = redeemTicket(t, f, disabled, ticketMarker)
	assertTicketNoGood(t, status, body, "a disabled account's ticket")
	assert.Empty(t, fileContent(t, f.RootA2, "late.txt"))
}

// A ticket follows the API token it was minted with, as a /z/ link does: a
// token revoked or expired after the mint takes its tickets with it.
func TestUploadTicketTenant_ATicketFollowsItsToken(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()

	t.Run("revoked", func(t *testing.T) {
		tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
		url := mintRedeemURL(t, f, tok, "alpha://private/revoked.txt")
		row, err := f.Store.GetAPITokenByHash(ctx, apitoken.HashToken(tok))
		require.NoError(t, err)
		require.NoError(t, f.Store.DeleteAPIToken(ctx, row.ID))

		status, body := redeemTicket(t, f, url, ticketMarker)
		assertTicketNoGood(t, status, body, "a revoked token's ticket")
		assert.Empty(t, fileContent(t, f.RootA, "private/revoked.txt"))
	})

	t.Run("expired", func(t *testing.T) {
		soon := time.Now().Add(1500 * time.Millisecond)
		tok := issueToken(t, f.Store, f.UserA, "read,write", &soon)
		url := mintRedeemURL(t, f, tok, "alpha://private/expired.txt")
		time.Sleep(time.Until(soon) + 200*time.Millisecond)

		status, body := redeemTicket(t, f, url, ticketMarker)
		assertTicketNoGood(t, status, body, "an expired token's ticket")
		assert.Empty(t, fileContent(t, f.RootA, "private/expired.txt"))
	})

	t.Run("control: the token still stands", func(t *testing.T) {
		tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
		url := mintRedeemURL(t, f, tok, "alpha://private/kept.txt")
		status, body := redeemTicket(t, f, url, "kept")
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, "kept", fileContent(t, f.RootA, "private/kept.txt"))
	})
}
