package permgap_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/permgap"
)

var counter atomic.Int64

func newStore(t *testing.T) db.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:permgap_test_%d?mode=memory&cache=shared", counter.Add(1))
	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(context.Background(), drv, conn))
	return drv.NewStore(conn)
}

// sent records what Announce sends; the rest of notify.Service is not used.
type sent struct {
	notify.Service
	events []notify.Event
}

func (s *sent) Send(_ context.Context, e notify.Event) (int64, error) {
	s.events = append(s.events, e)
	return int64(len(s.events)), nil
}

// The words of the notice name files.encrypt and files.create. A second
// permission carved out of an older one needs words of its own here
// (server.permission_gaps.*), in the Roles page's warning and in the docs:
// this fails until somebody has looked.
func TestTheNoticeKnowsEveryCarvedOutPermission(t *testing.T) {
	require.Equal(t, []perm.Perm{perm.FilesEncrypt}, perm.CarvedOut())
}

func TestAnnounce_OnceForANewGap(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	n := &sent{}

	permgap.Announce(ctx, store, n)
	require.Empty(t, n.events, "nothing to tell")

	// The User role as 0.50 writes it back: files.create, no files.encrypt.
	b, err := json.Marshal(perm.Standard.Without(perm.FilesEncrypt).Strings())
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingPermissionDefaults, string(b)))

	permgap.Announce(ctx, store, n)
	require.Len(t, n.events, 1)
	e := n.events[0]
	require.Equal(t, notify.EventPermissionGaps, e.Event)
	require.Nil(t, e.UserID, "a broadcast: the administrators nobody confines read it")
	require.Equal(t, notify.SeverityWarning, e.Severity)
	require.Equal(t, 1, e.Meta["count"])
	require.Equal(t, "1 role may have lost Encrypt", e.Meta["title_en"])
	require.Equal(t, "1 rolde Şifreleme izni düşmüş olabilir", e.Meta["title_tr"])
	require.Contains(t, e.Meta["body_en"], "Admin → Roles")

	permgap.Announce(ctx, store, n)
	require.Len(t, n.events, 1, "told once, not at every start")

	// A second one opens: told again, with how many there are now.
	_, err = store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Drop box", Enabled: true, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	require.NoError(t, err)
	permgap.Announce(ctx, store, n)
	require.Len(t, n.events, 2)
	require.Equal(t, 2, n.events[1].Meta["count"])
	require.Equal(t, "2 roles may have lost Encrypt", n.events[1].Meta["title_en"])
}

func TestAnnounce_NotificationsOff(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	b, err := json.Marshal(perm.Standard.Without(perm.FilesEncrypt).Strings())
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingPermissionDefaults, string(b)))

	permgap.Announce(ctx, store, nil)
	n := &sent{}
	permgap.Announce(ctx, store, n)
	require.Len(t, n.events, 1, "nothing was recorded as told while nobody could be told")
}
