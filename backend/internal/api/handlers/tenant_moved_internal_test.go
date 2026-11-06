package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/coedit"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// movedFixture is one local storage of tenant alpha, an account of alpha and
// a second tenant to hand the storage to.
type movedFixture struct {
	store        db.Store
	st           *model.Storage
	root         string
	drv          *local.Driver
	alpha, bravo int64
	uid          int64
}

func newMovedFixture(t *testing.T) *movedFixture {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	f := &movedFixture{store: store, root: t.TempDir(), drv: &local.Driver{}}
	require.NoError(t, f.drv.Init(ctx, map[string]any{"root": f.root}))
	var err error
	f.st, err = store.CreateStorage(ctx, &model.Storage{Name: "alpha", Driver: "local", MountPath: "/alpha", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	for _, slug := range []string{"alpha", "bravo"} {
		p, err := store.CreateProvider(ctx, &model.Provider{Slug: slug, Name: slug, AuthType: "local", Enabled: true})
		require.NoError(t, err)
		if slug == "alpha" {
			f.alpha = p.ID
		} else {
			f.bravo = p.ID
		}
	}
	require.NoError(t, store.LinkProviderStorage(ctx, f.alpha, f.st.ID))
	u, err := store.CreateUser(ctx, "member@alpha.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserEnabled(ctx, u.ID, true))
	require.NoError(t, store.SetUserProvider(ctx, u.ID, f.alpha, ""))
	f.uid = u.ID
	return f
}

func (f *movedFixture) move(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.store.UnlinkProviderStorage(ctx, f.alpha, f.st.ID))
	require.NoError(t, f.store.LinkProviderStorage(ctx, f.bravo, f.st.ID))
}

// An app job's result is written for the person the job runs for only while
// that person's tenant still reaches the storage: the job waited in the
// queue, and the platform operator moved the storage meanwhile.
func TestAppJobResult_AStorageMovedToAnotherTenantTakesNoResult(t *testing.T) {
	f := newMovedFixture(t)
	ctx := context.Background()
	h := &AppPlugins{Store: f.store, ACL: acl.New(f.store), StorageResolver: func(int64) (storage.Driver, error) { return f.drv, nil }}
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "doc.txt"), []byte("v1"), 0o644))

	_, err := h.CommitSibling(ctx, f.st.ID, "", "before.txt", strings.NewReader("ok"), 2, &f.uid)
	require.NoError(t, err, "control: alpha's storage, alpha's member")
	require.NoError(t, h.CommitVersion(ctx, f.st.ID, "doc.txt", strings.NewReader("v2"), 2, &f.uid))

	f.move(t)
	_, err = h.CommitSibling(ctx, f.st.ID, "", "after.txt", strings.NewReader("no"), 2, &f.uid)
	assert.ErrorIs(t, err, errJobTenant)
	_, statErr := os.Stat(filepath.Join(f.root, "after.txt"))
	assert.True(t, os.IsNotExist(statErr), "no new file in bravo's storage")
	assert.ErrorIs(t, h.CommitVersion(ctx, f.st.ID, "doc.txt", strings.NewReader("v3"), 2, &f.uid), errJobTenant)
	b, _ := os.ReadFile(filepath.Join(f.root, "doc.txt"))
	assert.Equal(t, "v2", string(b), "nothing written over bravo's file")

	// A job with no person behind it (an app's own scheduled run) is the
	// app's, and is not asked.
	_, err = h.CommitSibling(ctx, f.st.ID, "", "scheduled.txt", strings.NewReader("ok"), 2, nil)
	assert.NoError(t, err)
}

// A member of an editing-together session stays only while their tenant
// still reaches the file's storage (stillAllowed, asked every touch).
func TestCoEditMember_AStorageMovedToAnotherTenantEndsTheMembership(t *testing.T) {
	f := newMovedFixture(t)
	ctx := context.Background()
	box, err := secretbox.New("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	relay := coedit.New(coedit.Options{Store: f.store, BlobDir: t.TempDir(), Keys: box})
	hello, err := relay.Open(ctx, coedit.OpenRequest{
		Doc: coedit.DocKey(f.st.ID, "doc.txt"), StorageID: f.st.ID, Path: "doc.txt", App: "editor", Build: "1",
		FileSig: "2:0", KeyMode: coedit.KeyServer,
	}, coedit.Identity{User: f.uid, Person: "member", Name: "member", CanEdit: true, TokenHash: coedit.TokenHash("member-token")})
	require.NoError(t, err)
	h := &CoEdit{Relay: relay, Apps: &AppPlugins{Store: f.store, ACL: acl.New(f.store)}, WS: &WS{MultiTenant: true}}
	tk := coeditTicket{SID: hello.SID, Client: hello.Me.Client, UserID: f.uid, CanEdit: true}
	// The socket's context carries the member, as the member's socket does.
	u, err := f.store.GetUser(ctx, f.uid)
	require.NoError(t, err)
	ctx = auth.WithUser(ctx, u)

	require.True(t, h.stillAllowed(ctx, tk), "control")
	f.move(t)
	assert.False(t, h.stillAllowed(ctx, tk), "a member of alpha in what is bravo's storage now")
}

// An open session's member whose account is switched off, or whose tenant is
// suspended, leaves it at the next touch: the rule the tickets and the links
// follow (ticketMinter).
func TestCoEditMember_AnAccountThatMayNoLongerSignInEndsTheMembership(t *testing.T) {
	for _, c := range []struct {
		name string
		off  func(t *testing.T, f *movedFixture)
	}{
		{"account switched off", func(t *testing.T, f *movedFixture) {
			require.NoError(t, f.store.SetUserEnabled(context.Background(), f.uid, false))
		}},
		{"tenant suspended", func(t *testing.T, f *movedFixture) {
			ctx := context.Background()
			p, err := f.store.GetProvider(ctx, f.alpha)
			require.NoError(t, err)
			p.Enabled = false
			require.NoError(t, f.store.UpdateProvider(ctx, p))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newMovedFixture(t)
			ctx := context.Background()
			box, err := secretbox.New("0123456789abcdef0123456789abcdef")
			require.NoError(t, err)
			relay := coedit.New(coedit.Options{Store: f.store, BlobDir: t.TempDir(), Keys: box})
			hello, err := relay.Open(ctx, coedit.OpenRequest{
				Doc: coedit.DocKey(f.st.ID, "doc.txt"), StorageID: f.st.ID, Path: "doc.txt", App: "editor", Build: "1",
				FileSig: "2:0", KeyMode: coedit.KeyServer,
			}, coedit.Identity{User: f.uid, Person: "member", Name: "member", CanEdit: true, TokenHash: coedit.TokenHash("member-token")})
			require.NoError(t, err)
			h := &CoEdit{Relay: relay, Apps: &AppPlugins{Store: f.store, ACL: acl.New(f.store)}, WS: &WS{MultiTenant: true}}
			tk := coeditTicket{SID: hello.SID, Client: hello.Me.Client, UserID: f.uid, CanEdit: true}
			u, err := f.store.GetUser(ctx, f.uid)
			require.NoError(t, err)
			ctx = auth.WithUser(ctx, u)

			require.True(t, h.stillAllowed(ctx, tk), "control")
			c.off(t, f)
			assert.False(t, h.stillAllowed(ctx, tk), c.name)
		})
	}
}
