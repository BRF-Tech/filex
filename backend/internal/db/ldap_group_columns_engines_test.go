package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// What directory sync writes on a group fits every engine's columns: the id
// of a group with no permanent id ("<slug>:dn:" and the DN's SHA-256, a slug
// up to 63 characters) and a directory name cut to 255 characters - MySQL's
// VARCHAR(255), counted in characters, Turkish letters included.
func TestLDAPSyncedGroupColumnsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			id := "ldap-" + strings.Repeat("x", 58) + ":dn:" + strings.Repeat("a1", 32)
			name := strings.Repeat("ğüşiöç", 42) + "çağ"
			require.Len(t, []rune(name), 255)
			g, err := store.CreateGroup(ctx, &model.Group{Name: "deep", DirectoryID: id, DirectoryName: name,
				Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: "cn=" + strings.Repeat("çok-uzun,ou=bölüm-", 30) + "dc=example"}}})
			require.NoError(t, err, "%s: create", e.name)
			require.NoError(t, store.SetGroupDirectory(ctx, g.ID, id, name, model.GroupDirectoryRemoved), "%s: set", e.name)
			got, err := store.GetGroup(ctx, g.ID)
			require.NoError(t, err)
			require.Equal(t, id, got.DirectoryID, e.name)
			require.Equal(t, name, got.DirectoryName, e.name)
		})
	}
}
