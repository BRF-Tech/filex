package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestDeleteUserTakesItsLinksOnEveryEngine — deleting an account deletes the
// public links it opened (Burak, 2026-09-28: "Kapansın"), in the store, so no
// door that deletes an account can leave one working.
//
// ⚠ Measured before the change: the account row went, `shares.created_by`
// became NULL (ON DELETE SET NULL) and the link kept answering — a link with
// no creator is one handlers.linkCreatorAllows leaves open.
//
// What stays: somebody else's link, an app's own public page (the app opened
// it), and a link with no recorded creator (older than the column; nothing
// says whose it is).
func TestDeleteUserTakesItsLinksOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "local", Driver: "local", MountPath: "/data/files",
				ConfigJSON: json.RawMessage(`{"root":"/data/files"}`),
				SyncMode:   model.SyncModeOnDemand, Enabled: true,
			})
			require.NoError(t, err)
			node := func(name string, typ model.NodeType) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: name, Path: "/" + name,
					PathHash: pathkey.Hash(st.ID, "/"+name), Type: typ,
				})
				require.NoError(t, err)
				return n
			}
			report := node("report.pdf", model.NodeTypeFile)
			inbox := node("Inbox", model.NodeTypeDirectory)

			gone, err := store.CreateUser(ctx, "gone@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)
			stays, err := store.CreateUser(ctx, "stays@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)

			seq := 0
			link := func(n *model.Node, by *int64, mut func(*model.Share)) string {
				seq++
				sh := &model.Share{NodeID: n.ID, Token: "tok-" + e.name + "-" + string(rune('a'+seq)), CreatedBy: by}
				if mut != nil {
					mut(sh)
				}
				_, err := store.CreateShare(ctx, sh)
				require.NoError(t, err)
				return sh.Token
			}
			past := time.Now().UTC().Add(-48 * time.Hour)

			download := link(report, &gone.ID, nil)
			drop := link(inbox, &gone.ID, func(s *model.Share) { s.Kind = model.ShareKindDrop })
			ended := link(report, &gone.ID, func(s *model.Share) { s.ExpiresAt = &past })
			appLink := link(report, &gone.ID, func(s *model.Share) { s.PluginID = 3 })
			appPage := link(report, &gone.ID, func(s *model.Share) { s.PluginID = 3; s.PageID = "sign" })
			theirs := link(report, &stays.ID, nil)
			nobodys := link(report, nil, nil)

			closed, err := store.DeleteUserWithLinks(ctx, gone.ID)
			require.NoError(t, err)
			// download + drop + the link an app opened on the person's behalf;
			// the expired one is deleted but was not open, the app page stays.
			require.EqualValues(t, 3, closed, "links still open when the account went")

			_, err = store.GetUser(ctx, gone.ID)
			require.Error(t, err, "the account is gone")
			for _, tok := range []string{download, drop, ended, appLink} {
				_, err := store.GetShareByToken(ctx, tok)
				require.Error(t, err, "%s: a link the deleted account opened must go with it", tok)
			}
			page, err := store.GetShareByToken(ctx, appPage)
			require.NoError(t, err, "an app's own page stays")
			require.True(t, page.IsApp())
			require.Nil(t, page.CreatedBy, "…with no person behind it any more")
			for _, tok := range []string{theirs, nobodys} {
				_, err := store.GetShareByToken(ctx, tok)
				require.NoError(t, err, "%s: somebody else's link, or one with no creator, is untouched", tok)
			}

			// DeleteUser — what every other door calls — does the same.
			again, err := store.CreateUser(ctx, "again@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)
			tok := link(report, &again.ID, nil)
			require.NoError(t, store.DeleteUser(ctx, again.ID))
			_, err = store.GetShareByToken(ctx, tok)
			require.Error(t, err, "DeleteUser takes the links too")
		})
	}
}
