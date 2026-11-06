package handlers_test

// What a tenant's member left behind on a storage - a public link, a file
// request, a queued operation (tenant_moved_ops_test.go), a signed thumbnail
// (tenant_moved_thumb_test.go), the editor's fetch URL
// (tenant_moved_editor_test.go) - is asked again when it is used, and stops
// when the platform operator moved the storage to another tenant. Nothing is
// deleted: the storage coming back brings it back. And a link pauses while
// its creator's account is switched off.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
)

// moveStorage hands storage id from one tenant to another, as the platform
// operator's unlink + link does.
func (f *mtFix) moveStorage(t *testing.T, id, from, to int64) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.Store.UnlinkProviderStorage(ctx, from, id))
	require.NoError(t, f.Store.LinkProviderStorage(ctx, to, id))
}

// anon GETs url with no credentials and returns status + body.
func anon(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *mtFix) link(t *testing.T, n *model.Node, kind string, by *int64, pin string) *model.Share {
	t.Helper()
	sh, err := share.NewService(f.Store).Create(context.Background(), share.CreateOpts{NodeID: n.ID, Kind: kind, CreatedBy: by, PIN: pin})
	require.NoError(t, err)
	return sh
}

func (f *mtFix) folderNode(t *testing.T, st *model.Storage, root, rel string) *model.Node {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	n, err := f.Store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: filepath.Base(rel), Path: "/" + rel,
		PathHash: pathkey.Hash(st.ID, "/"+rel), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	return n
}

func dropped(t *testing.T, root, folder, name string) bool {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(root, folder, "*", name))
	direct, _ := filepath.Glob(filepath.Join(root, folder, name))
	return len(got)+len(direct) > 0
}

func TestTenantMoved_ALinkStopsWithItsStorageAndComesBackWithIt(t *testing.T) {
	f := newMTFix(t, true)
	doc := f.seedFile(t, f.StA, f.RootA, "docs/plan.txt", "alpha's plan")
	inbox := f.folderNode(t, f.StA, f.RootA, "Gelen")

	download := f.link(t, doc, model.ShareKindDownload, &f.UserA, "")
	request := f.link(t, inbox, model.ShareKindDrop, &f.UserA, "")
	// A link no account stands behind (an app's scheduled run opens such
	// pages): held to the storage's tenant all the same.
	orphan := f.link(t, doc, model.ShareKindDownload, nil, "")

	served := func(sh *model.Share) bool {
		status, body := anon(t, f.URL+"/s/"+sh.Token)
		if status != http.StatusOK {
			return false
		}
		require.Contains(t, body, "alpha's plan")
		return true
	}
	jsonServed := func(sh *model.Share) int {
		status, _ := anon(t, f.URL+"/api/public/s/"+sh.Token)
		return status
	}

	require.True(t, served(download), "control: the link works")
	require.True(t, served(orphan), "control: the creatorless link works")
	require.Equal(t, http.StatusOK, jsonServed(download))
	status, body := dropFile(t, f, request.Token, "first.txt", "before the move")
	require.Less(t, status, 300, body)
	require.True(t, dropped(t, f.RootA, "Gelen", "first.txt"))

	f.moveStorage(t, f.StA.ID, f.ProvA, f.ProvB)

	assert.False(t, served(download), "alpha's link must not serve what is bravo's storage now")
	assert.False(t, served(orphan), "nor a link with no creator")
	assert.Equal(t, http.StatusNotFound, jsonServed(download))
	status, body = dropFile(t, f, request.Token, "second.txt", "after the move")
	assert.Equal(t, http.StatusNotFound, status, "a file request into bravo's storage: %s", body)
	assert.False(t, dropped(t, f.RootA, "Gelen", "second.txt"), "nothing written")

	// Paused, not deleted: the storage comes back, and so do its links.
	f.moveStorage(t, f.StA.ID, f.ProvB, f.ProvA)
	assert.True(t, served(download), "the same address answers again")
	assert.True(t, served(orphan))
	status, body = dropFile(t, f, request.Token, "third.txt", "back")
	assert.Less(t, status, 300, body)
}

// A storage the platform kept in no tenant and then handed to one keeps the
// links made on it: the platform's data given to a customer is the operator's
// own move (db.StampStands). Pinned so the rule is a decision, not an accident.
func TestTenantMoved_APlatformStorageGivenToATenantKeepsItsLinks(t *testing.T) {
	f := newMTFix(t, true)
	st, root := f.mountStorage(t, "ortak")
	doc := f.seedFile(t, st, root, "duyuru.txt", "for everybody")
	sh := f.link(t, doc, model.ShareKindDownload, nil, "")

	require.NoError(t, f.Store.LinkProviderStorage(context.Background(), f.ProvB, st.ID))
	status, body := anon(t, f.URL+"/s/"+sh.Token)
	assert.Equal(t, http.StatusOK, status, body)
}

// A link pauses while its creator's account is off: the address answers like
// one that does not exist, a file request takes nothing, and the account
// coming back brings the same address and PIN back. Another account's links
// are not touched.
func TestTenantMoved_ADisabledAccountsLinksPause(t *testing.T) {
	for _, multi := range []bool{false, true} {
		name := "single-tenant"
		if multi {
			name = "multi-tenant"
		}
		t.Run(name, func(t *testing.T) {
			f := newMTFix(t, multi)
			ctx := context.Background()
			docA := f.seedFile(t, f.StA, f.RootA, "docs/a.txt", "alpha's plan")
			docB := f.seedFile(t, f.StB, f.RootB, "docs/b.txt", "bravo's plan")
			inbox := f.folderNode(t, f.StA, f.RootA, "Gelen")

			plain := f.link(t, docA, model.ShareKindDownload, &f.UserA, "")
			pinned := f.link(t, docA, model.ShareKindDownload, &f.UserA, "246813")
			request := f.link(t, inbox, model.ShareKindDrop, &f.UserA, "")
			others := f.link(t, docB, model.ShareKindDownload, &f.UserB, "")

			pinOK := func() bool {
				resp, err := http.Post(f.URL+"/api/public/s/"+pinned.Token+"/pin", "application/json",
					strings.NewReader(`{"pin":"246813"}`))
				require.NoError(t, err)
				defer resp.Body.Close()
				return resp.StatusCode == http.StatusOK
			}

			status, _ := anon(t, f.URL+"/s/"+plain.Token)
			require.Equal(t, http.StatusOK, status, "control")
			require.True(t, pinOK(), "control: the PIN opens the link")

			require.NoError(t, f.Store.SetUserEnabled(ctx, f.UserA, false))

			status, _ = anon(t, f.URL+"/s/"+plain.Token)
			assert.Equal(t, http.StatusNotFound, status, "a switched-off account's link")
			status, _ = anon(t, f.URL+"/api/public/s/"+plain.Token)
			assert.Equal(t, http.StatusNotFound, status)
			assert.False(t, pinOK(), "nor with its PIN")
			status, body := dropFile(t, f, request.Token, "late.txt", "while off")
			assert.Equal(t, http.StatusNotFound, status, body)
			assert.False(t, dropped(t, f.RootA, "Gelen", "late.txt"), "nothing written")
			status, _ = anon(t, f.URL+"/s/"+others.Token)
			assert.Equal(t, http.StatusOK, status, "another account's link is not touched")

			require.NoError(t, f.Store.SetUserEnabled(ctx, f.UserA, true))
			status, _ = anon(t, f.URL+"/s/"+plain.Token)
			assert.Equal(t, http.StatusOK, status, "the same address comes back")
			assert.True(t, pinOK(), "with the same PIN")
			status, body = dropFile(t, f, request.Token, "back.txt", "on again")
			assert.Less(t, status, 300, body)
		})
	}
}
