//go:build linux

package authsetup_test

// The Linux PAM provider shares the operating-system rules with Windows: one
// list of such providers, one alarm path when one of them cannot start, and one
// directory chain on the file protocols.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authwindows "github.com/brf-tech/filex/backend/internal/auth/drivers/windows"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/pam"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestOSProviders_OneListForWindowsAndPAM(t *testing.T) {
	assert.ElementsMatch(t, []string{"windows", "pam"}, authsetup.OSProviders)
	for _, n := range authsetup.OSProviders {
		assert.True(t, authsetup.IsOS(n), n)
		assert.True(t, authsetup.IsManaged(n), n)
	}
	for _, n := range []string{"oidc", "ldap", "proxy-header", "local"} {
		assert.False(t, authsetup.IsOS(n), n)
	}
}

// pamTree lays out the files the provider looks for; the machine itself is the
// fake `machine`.
func pamTree(t *testing.T, machine func(argv []string, stdin string) (int, string, string, error)) map[string]string {
	t.Helper()
	dir := t.TempDir()
	pamd := filepath.Join(dir, "pam.d")
	require.NoError(t, os.MkdirAll(pamd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pamd, "filex"), []byte("x\n"), 0o644))
	for _, n := range []string{"pamtester", "sudo"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755))
	}
	t.Cleanup(pam.SwapForTest(machine, pamd))
	return map[string]string{
		"pamtester_path": filepath.Join(dir, "pamtester"),
		"sudo_path":      filepath.Join(dir, "sudo"),
	}
}

// A pam that cannot start (sudo wants a password) raises the SAME alarm a
// Windows provider does: the operator is told once, named by provider.
func TestPAM_ThatCannotStartRaisesTheSameAlarmAsWindows(t *testing.T) {
	cfg := pamTree(t, func(argv []string, stdin string) (int, string, string, error) {
		if filepath.Base(argv[0]) == "sudo" {
			return 1, "", "sudo: a password is required\n", nil
		}
		return 127, "", "", nil
	})
	_, store := testutil.NewTestDB(t)
	put(t, store, map[string]string{authsetup.SchemaSetting: "2"})
	enable(t, store, "pam", cfg)
	l := live(t, store, nil)
	e := mustEntry(t, l, "pam")
	require.False(t, e.Live())
	require.NotEmpty(t, e.Err)

	type told struct{ name, reason string }
	got := make(chan told, 4)
	l.SetProviderAlarm(func(name, reason string) { got <- told{name, reason} })
	select {
	case n := <-got:
		assert.Equal(t, "pam", n.name)
		assert.Contains(t, n.reason, "sudo")
	case <-time.After(2 * time.Second):
		t.Fatal("the pam boot failure was never raised")
	}

	// Once per reason: a retry that fails the same way says nothing more.
	require.NoError(t, l.Reload(context.Background()))
	select {
	case n := <-got:
		t.Fatalf("told twice: %v", n)
	case <-time.After(300 * time.Millisecond):
	}
}

// LDAP, Windows and PAM all on the file protocols: one that cannot judge does
// not hide the ones after it, and a refusal is not the last word.
func TestDirectoryChain_LDAPWindowsAndPAMAllAnswer(t *testing.T) {
	defer authwindows.SetLogonForTest(fakeWindows(map[string]string{"ayse|.": "pw-win"}))()
	var machine func(argv []string, stdin string) (int, string, string, error)
	machine = func(argv []string, stdin string) (int, string, string, error) {
		switch filepath.Base(argv[0]) {
		case "getent":
			if argv[2] == "alice" {
				return 0, "alice:x:1001:1001::/home/alice:/bin/bash\n", "", nil
			}
			return 2, "", "", nil
		case "id":
			return 0, "alice staff\n", "", nil
		case "sudo":
			if argv[2] == "-l" {
				return 0, argv[3] + "\n", "", nil
			}
			// `sudo -n <command…>` runs the command: its answer is the
			// command's, so a login nobody has is a "no" here as on a real
			// machine (the setup check's unknown_user step asks exactly that).
			return machine(argv[2:], stdin)
		case "pamtester":
			if argv[2] == "alice" && stdin == "pw-pam\n" {
				return 0, "", "", nil
			}
			return 1, "", "Password: pamtester: Authentication failure\n", nil
		}
		return 127, "", "", nil
	}
	cfg := pamTree(t, machine)
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	winU, err := store.CreateUser(ctx, "ayse@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	pamU, err := store.CreateUser(ctx, "alice@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	l := live(t, store, nil)
	enable(t, store, "ldap", map[string]string{"url": "ldap://127.0.0.1:1", "base_dn": "dc=example", "protocol_login": "true"})
	enable(t, store, "windows", map[string]string{})
	pamValues := map[string]string{"protocol_login": "true"}
	for k, v := range cfg {
		pamValues[k] = v
	}
	enable(t, store, "pam", pamValues)
	require.NoError(t, l.Reload(ctx))
	require.True(t, mustEntry(t, l, "ldap").Live())
	require.True(t, mustEntry(t, l, "windows").Live())
	require.True(t, mustEntry(t, l, "pam").Live(), "%v", mustEntry(t, l, "pam").Err)

	got, err := l.Dir().VerifyPassword(ctx, "ayse@local", "pw-win")
	require.NoError(t, err)
	assert.Equal(t, winU.ID, got.ID)
	got, err = l.Dir().VerifyPassword(ctx, "alice", "pw-pam")
	require.NoError(t, err, "the directories before it (unreachable LDAP, Windows saying no) must not hide pam")
	assert.Equal(t, pamU.ID, got.ID)
	_, err = l.Dir().VerifyPassword(ctx, "alice", "wrong")
	assert.Error(t, err)
}
