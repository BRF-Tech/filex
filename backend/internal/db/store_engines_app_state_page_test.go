package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestAppPluginStateFilesPageOnEveryEngine: state_list reads an app's state
// rows a page at a time until the caller's narrowed list is full (0.52.0), so
// the pages must be one order, the same on every engine: no row read twice,
// none skipped, two storages holding the same path kept apart.
func TestAppPluginStateFilesPageOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			mk := func(name string) *model.Storage {
				st, err := store.CreateStorage(ctx, &model.Storage{Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ConfigJSON: json.RawMessage(`{}`)})
				require.NoError(t, err)
				return st
			}
			a, b := mk("a"), mk("b")
			const plugin = 7
			want := []string{}
			for i := 0; i < 7; i++ {
				rel := fmt.Sprintf("docs/f%d.txt", i)
				for _, st := range []*model.Storage{b, a} {
					require.NoError(t, store.SetAppPluginState(ctx, plugin, st.ID, pathkey.Hash(st.ID, "/"+rel), rel, "runs", "1"))
				}
				want = append(want, "a:"+rel, "b:"+rel)
			}
			require.NoError(t, store.SetAppPluginState(ctx, plugin+1, a.ID, pathkey.Hash(a.ID, "/x.txt"), "x.txt", "runs", "1"))

			got := []string{}
			for offset := 0; ; offset += 3 {
				rows, err := store.ListAppPluginStateFiles(ctx, plugin, "runs", 3, offset)
				require.NoError(t, err)
				for _, r := range rows {
					got = append(got, r.StorageName+":"+r.Path)
				}
				if len(rows) < 3 {
					break
				}
			}
			require.Equal(t, want, got, "%s: pages of three read every row once, in one order", e.name)

			rows, err := store.ListAppPluginStateFiles(ctx, plugin, "runs", 3, 100)
			require.NoError(t, err)
			require.Empty(t, rows, "%s: past the end", e.name)
		})
	}
}
