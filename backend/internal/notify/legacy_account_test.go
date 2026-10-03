package notify_test

// The platform operator's notice about an older directory account found in
// another tenant (auth.AdoptAccount): its words come from the server catalogue
// in every built-in language, it names no secret, and only a platform
// administrator's bell carries it — never a tenant administrator's, never a
// member's.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestLegacyAccountElsewhere_SaysItInEveryLanguageAndNamesNoSecret(t *testing.T) {
	ev := notify.LegacyAccountElsewhere("ldap", "alex", "initech", "globex")
	assert.Equal(t, notify.EventLDAPLegacyAccountElsewhere, ev.Event)
	assert.Equal(t, notify.SeverityWarning, ev.Severity)
	assert.Nil(t, ev.UserID, "a broadcast: the bells' rule decides who reads it")

	for _, lang := range srvtext.BuiltinLanguages() {
		title, _ := ev.Meta["title_"+lang].(string)
		body, _ := ev.Meta["body_"+lang].(string)
		require.NotEmpty(t, title, lang)
		require.NotEmpty(t, body, lang)
		for _, s := range []string{title, body} {
			assert.NotContains(t, s, "{", "%s: a placeholder survived: %s", lang, s)
		}
		assert.Contains(t, title, "alex", lang)
		assert.Contains(t, body, "alex", lang)
		assert.Contains(t, body, "initech", lang)
		assert.Contains(t, body, "globex", lang)
	}
	assert.Contains(t, ev.Meta["title_tr"], "kiracıda", "Turkish, with its own letters")
	assert.NotEqual(t, ev.Meta["title_en"], ev.Meta["title_tr"])
	lang := srvtext.Pick()
	assert.Equal(t, ev.Meta["title_"+lang], ev.Title, "the row's own title is the instance's language")
	assert.Equal(t, ev.Meta["body_"+lang], ev.Body)

	for k := range ev.Meta {
		lk := strings.ToLower(k)
		assert.False(t, strings.Contains(lk, "password") || strings.Contains(lk, "token") || strings.Contains(lk, "secret"), k)
	}
	assert.Equal(t, "alex", ev.Meta["account"])
	assert.Equal(t, "initech", ev.Meta["account_tenant"])
	assert.Equal(t, "globex", ev.Meta["login_tenant"])
}

func TestLegacyAccountElsewhere_OnlyThePlatformOperatorsBell(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reader, err := store.CreateUser(ctx, "okur@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	_, err = svc.Send(ctx, notify.LegacyAccountElsewhere("ldap", "alex", "initech", "globex"))
	require.NoError(t, err)

	for _, tc := range []struct {
		bell notify.Bell
		want int
	}{
		{notify.AdminBell, 1},
		{notify.TenantAdminBell, 0},
		{notify.MemberBell, 0},
	} {
		rows, total, err := svc.List(ctx, &reader.ID, tc.bell, false, 50, 0)
		require.NoError(t, err)
		assert.Len(t, rows, tc.want, "bell %d", tc.bell)
		assert.EqualValues(t, tc.want, total, "bell %d", tc.bell)
		n, err := svc.UnreadCount(ctx, &reader.ID, tc.bell)
		require.NoError(t, err)
		assert.EqualValues(t, tc.want, n, "bell %d: the badge agrees", tc.bell)
		assert.Equal(t, tc.want == 1, tc.bell.Admits(string(notify.EventLDAPLegacyAccountElsewhere)))
	}
	all, _, err := svc.List(ctx, nil, notify.AdminBell, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, notify.AudienceAdmins, notify.BroadcastAudience(all[0]))

	// An operator alarm by EVENT, not by address (the v0.43.0 rule): even a
	// row addressed to a member stays out of that member's bell.
	addressed := notify.LegacyAccountElsewhere("ldap", "alex", "initech", "globex")
	addressed.UserID = &reader.ID
	_, err = svc.Send(ctx, addressed)
	require.NoError(t, err)
	rows, _, err := svc.List(ctx, &reader.ID, notify.MemberBell, false, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
