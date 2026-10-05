package ldap

import (
	"context"
	"strings"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A directory group with no permanent id is keyed by its DN, and a DN has no
// length limit; user_groups.directory_id and directory_name are VARCHAR(255)
// on MySQL. A group under a deep OU (~245 characters and up) failed to come
// in there ("Data too long"). Its id is now "dn:" and the DN's SHA-256, and
// its directory name is cut to 255 characters, here and where it is compared.
func TestDirectorySyncGroups_ALongDNFitsTheColumns(t *testing.T) {
	ctx := context.Background()
	deep := "cn=" + strings.Repeat("çok-uzun-bölüm-adı-", 8) + "grubu," + strings.Repeat("ou=alt-birim-ğüşiöç,", 12) + "dc=example,dc=com"
	require.Greater(t, len(deep), 255)
	slug := "ldap-" + strings.Repeat("x", 58)
	prefix := slug + ":"
	noID := func(dn string) *goldap.Entry {
		return goldap.NewEntry(dn, map[string][]string{"cn": {strings.Repeat("Çok uzun grup adı ", 20)}})
	}

	id := groupID(prefix, noID(deep))
	assert.LessOrEqual(t, len([]rune(id)), 255, "fits user_groups.directory_id on MySQL: %d", len([]rune(id)))
	assert.True(t, strings.HasPrefix(id, prefix+"dn:"), id)
	assert.Equal(t, id, groupID(prefix, noID(" CN="+strings.TrimPrefix(deep, "cn="))), "the same DN, written another way, is the same group")
	assert.NotEqual(t, id, groupID(prefix, noID(deep+"x")))

	fc := &fakeConn{
		entries: []*goldap.Entry{person("cn=ada,dc=example,dc=com", "ada@example.com")},
		groups:  []*goldap.Entry{noID(deep)},
	}
	d, store := newLinkedDriver(t, fc, map[string]any{"directory": slug})
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.GroupsCreated)
	gs, err := store.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, gs, 1)
	assert.LessOrEqual(t, len([]rune(gs[0].DirectoryID)), 255)
	assert.LessOrEqual(t, len([]rune(gs[0].DirectoryName)), 255, "fits user_groups.directory_name on MySQL")

	// Nothing changed: no rename, nothing restored or removed.
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsCreated+rep.GroupsRenamed+rep.GroupsRestored+rep.GroupsRemoved, "%+v", rep)
}

// A DN-keyed group brought in before its id was hashed is the same group:
// it takes the new id, and is neither flagged removed nor made again.
func TestDirectorySyncGroups_ADNKeyedGroupFromBeforeKeepsItsGroup(t *testing.T) {
	ctx := context.Background()
	dn := "cn=finance,ou=groups,dc=example,dc=com"
	entry := goldap.NewEntry(dn, map[string][]string{"cn": {"finance"}})
	fc := &fakeConn{entries: []*goldap.Entry{person("cn=ada,dc=example,dc=com", "ada@example.com")}, groups: []*goldap.Entry{entry}}
	d, store := newLinkedDriver(t, fc, nil)
	old := d.idPrefix() + "dn:" + dn
	g := linkedGroup(t, store, "finance", dn)
	require.NoError(t, store.SetGroupDirectory(ctx, g.ID, old, "finance", ""))

	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsCreated+rep.GroupsRemoved, "%+v", rep)
	got, err := store.GetGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, groupID(d.idPrefix(), entry), got.DirectoryID)
	gs, err := store.ListGroups(ctx)
	require.NoError(t, err)
	assert.Len(t, gs, 1)
}
