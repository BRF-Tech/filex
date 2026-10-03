package authsetup_test

// The Windows sign-in provider on the Identity providers page: its schema, its
// place in the running set, what happens when the machine can no longer run it
// (left out, the operator told once, everything else keeps working), and the
// file protocols asking more than one directory.

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authwindows "github.com/brf-tech/filex/backend/internal/auth/drivers/windows"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const sidOfAPerson = "S-1-5-21-1000-2000-3000-1105"

func fakeWindows(accounts map[string]string) func(user, domain, password string) (*authwindows.Logon, error) {
	return func(user, domain, password string) (*authwindows.Logon, error) {
		if pw, ok := accounts[user+"|"+domain]; ok && pw == password {
			return &authwindows.Logon{SID: sidOfAPerson, Groups: []string{"Users"}}, nil
		}
		return nil, &authwindows.LogonError{Code: 1326}
	}
}

func enable(t *testing.T, store db.Store, name string, values map[string]string) {
	t.Helper()
	require.NoError(t, authsetup.Save(context.Background(), store, &authsetup.Stored{
		Name: name, Enabled: true, Exists: true, Values: values,
	}))
}

func TestWindows_TheSchemaAndWhatTheDriverIsHanded(t *testing.T) {
	assert.True(t, authsetup.IsManaged("windows"))
	assert.True(t, authsetup.IsOS("windows"))
	assert.False(t, authsetup.IsOS("ldap"))
	for _, k := range []string{"auto_create", "allowed_groups", "domain", "protocol_login"} {
		_, ok := authsetup.FieldOf("windows", k)
		assert.True(t, ok, k)
	}
	// ⚠ The test account is a request field, never a setting.
	for _, k := range []string{"test_account", "test_username", "test_password", "password", "username"} {
		_, ok := authsetup.FieldOf("windows", k)
		assert.False(t, ok, "%s must not be storable", k)
	}
	pc, _ := authsetup.FieldOf("windows", "protocol_login")
	assert.Equal(t, "true", pc.Default)

	b := box(t, testKey)
	cfg, dir, err := authsetup.DriverConfig(&authsetup.Stored{Name: "windows", Values: map[string]string{}}, b,
		authsetup.Options{MultiTenant: true, LoginEmailToken: "evim"})
	require.NoError(t, err)
	assert.True(t, dir, "protocol_login defaults to on")
	assert.Equal(t, false, cfg["auto_create"])
	assert.Equal(t, true, cfg["multi_tenant"])
	assert.Equal(t, "evim", cfg["email_token"], "the installation's token reaches the driver from Options")

	_, dir, err = authsetup.DriverConfig(&authsetup.Stored{Name: "windows", Values: map[string]string{"protocol_login": "false"}}, b, authsetup.Options{})
	require.NoError(t, err)
	assert.False(t, dir)
}

func TestWindows_RunsInTheSetAndCountsAsAWayIn(t *testing.T) {
	defer authwindows.SetLogonForTest(fakeWindows(map[string]string{"ayse|.": "pw"}))()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	l := live(t, store, nil)
	enable(t, store, "windows", map[string]string{"auto_create": "true"})
	require.NoError(t, l.Reload(ctx))

	e, ok := l.Current().Entry("windows")
	require.True(t, ok)
	assert.True(t, e.Live(), e.Err)
	assert.Contains(t, l.Current().Names(), "windows")
	assert.True(t, l.Current().HasDirectory(), "protocol_login is on by default")
	assert.Contains(t, l.AdminPaths(ctx, "oidc"), "windows")
	assert.NotContains(t, l.AdminPaths(ctx, "windows"), "windows", "the one about to be switched off is left out")

	u, tok, err := l.Login().Login(ctx, "ayse", "pw")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "ayse@local", u.Email)
	u2, err := l.Dir().VerifyPassword(ctx, "ayse@local", "pw")
	require.NoError(t, err)
	assert.Equal(t, u.ID, u2.ID, "the file protocols reach the same account")

	// protocol_login off: the browser still signs in, the protocols do not ask.
	enable(t, store, "windows", map[string]string{"auto_create": "true", "protocol_login": "false"})
	require.NoError(t, l.Reload(ctx))
	assert.False(t, l.Current().HasDirectory())
	_, _, err = l.Login().Login(ctx, "ayse", "pw")
	require.NoError(t, err)
}

// A provider the machine can no longer run is left out; every other way in
// keeps working; the operator is told once per reason, and again when it
// breaks anew.
func TestWindows_ThatCannotStartIsLeftOutAndTheOperatorIsToldOnce(t *testing.T) {
	restore := authwindows.SetLogonForTest(fakeWindows(map[string]string{"ayse|.": "Zx9-not-in-a-log"}))
	defer restore()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	var logBuf bytes.Buffer
	l := live(t, store, &logBuf)

	var mu sync.Mutex
	var told []string
	l.SetProviderAlarm(func(name, reason string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, name+": "+reason)
	})
	count := func() int {
		time.Sleep(100 * time.Millisecond) // the alarm runs off the reload's goroutine
		mu.Lock()
		defer mu.Unlock()
		return len(told)
	}

	enable(t, store, "windows", map[string]string{"auto_create": "true"})
	require.NoError(t, l.Reload(ctx))
	require.True(t, mustEntry(t, l, "windows").Live())
	assert.Zero(t, count(), "a healthy provider says nothing")

	// The machine can no longer ask Windows (the picture of a data directory
	// copied to another operating system): a config change rebuilds it.
	authwindows.SetLogonForTest(nil)
	enable(t, store, "windows", map[string]string{"auto_create": "true", "domain": "corp.test"})
	require.NoError(t, l.Reload(ctx))
	e := mustEntry(t, l, "windows")
	assert.False(t, e.Live())
	assert.Contains(t, e.Err, "only on Windows")
	assert.NotContains(t, l.Current().Names(), "windows")
	assert.Equal(t, 1, count())
	mu.Lock()
	assert.Contains(t, told[0], "windows")
	mu.Unlock()

	// The others are untouched: password sign-in (local) is still answered.
	assert.True(t, l.Current().PasswordLogin())
	assert.True(t, mustEntry(t, l, "local").Live())

	// The same reason again (a retry) is not told again.
	require.NoError(t, l.Reload(ctx))
	require.NoError(t, l.Reload(ctx))
	assert.Equal(t, 1, count())

	// It works again → forgotten; breaks again → told again.
	authwindows.SetLogonForTest(fakeWindows(map[string]string{"ayse|.": "Zx9-not-in-a-log"}))
	require.NoError(t, l.Reload(ctx))
	require.True(t, mustEntry(t, l, "windows").Live())
	authwindows.SetLogonForTest(nil)
	enable(t, store, "windows", map[string]string{"auto_create": "true", "domain": "corp2.test"})
	require.NoError(t, l.Reload(ctx))
	assert.Equal(t, 2, count())
	assert.NotContains(t, logBuf.String(), "Zx9-not-in-a-log", "the log names the reason, never a value")
}

// A failure at boot happens before notifications exist: registering the alarm
// raises it.
func TestWindows_ABootFailureIsRaisedWhenTheAlarmIsRegistered(t *testing.T) {
	defer authwindows.SetLogonForTest(nil)()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	put(t, store, map[string]string{authsetup.SchemaSetting: "2"}) // not a pre-0.43 import
	enable(t, store, "windows", map[string]string{})
	l := live(t, store, nil)
	assert.False(t, mustEntry(t, l, "windows").Live())

	got := make(chan string, 2)
	l.SetProviderAlarm(func(name, reason string) { got <- name })
	select {
	case n := <-got:
		assert.Equal(t, "windows", n)
	case <-time.After(2 * time.Second):
		t.Fatal("the boot failure was never raised")
	}
	_ = ctx
}

// Two directories on the file protocols (LDAP and Windows): one that cannot
// judge does not hide the other, and a refusal by one is not the last word.
func TestWindows_TheFileProtocolsAskEveryDirectory(t *testing.T) {
	defer authwindows.SetLogonForTest(fakeWindows(map[string]string{"ayse|.": "pw"}))()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	u, err := store.CreateUser(ctx, "ayse@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	l := live(t, store, nil)
	// LDAP first (unreachable: cannot judge), then Windows.
	enable(t, store, "ldap", map[string]string{"url": "ldap://127.0.0.1:1", "base_dn": "dc=example", "protocol_login": "true"})
	enable(t, store, "windows", map[string]string{})
	require.NoError(t, l.Reload(ctx))
	require.True(t, mustEntry(t, l, "ldap").Live())
	require.True(t, mustEntry(t, l, "windows").Live())

	got, err := l.Dir().VerifyPassword(ctx, "ayse@local", "pw")
	require.NoError(t, err, "the unreachable directory must not hide the one that can answer")
	assert.Equal(t, u.ID, got.ID)

	_, err = l.Dir().VerifyPassword(ctx, "ayse@local", "wrong")
	assert.Error(t, err)
}

func mustEntry(t *testing.T, l *authsetup.Live, name string) authsetup.Entry {
	t.Helper()
	e, ok := l.Current().Entry(name)
	require.True(t, ok, name)
	return e
}
