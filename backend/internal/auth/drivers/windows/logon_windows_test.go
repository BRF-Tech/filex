//go:build windows

package windows

// The real LogonUserW, on a real Windows machine. It needs an account to sign
// in with, so it runs only when the operator names one:
//
//	FILEX_WIN_SMOKE_USER, FILEX_WIN_SMOKE_PASSWORD   a THROWAWAY local account
//
// (create it with `net user filexsmoke <password> /add`, delete it after).
// ⚠ Never point this at Administrator or at a real person's account.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

func smokeAccount(t *testing.T) (user, pw string) {
	t.Helper()
	user, pw = os.Getenv("FILEX_WIN_SMOKE_USER"), os.Getenv("FILEX_WIN_SMOKE_PASSWORD")
	if user == "" || pw == "" {
		t.Skip("FILEX_WIN_SMOKE_USER / FILEX_WIN_SMOKE_PASSWORD not set")
	}
	return user, pw
}

func TestLogonUserW_RealSignIn(t *testing.T) {
	user, pw := smokeAccount(t)
	require.NotNil(t, defaultLogon, "the operating-system call is wired on Windows")

	res, err := defaultLogon(user, ".", pw)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(res.SID, "S-1-5-21-"), "a local account's SID: %s", res.SID)
	assert.False(t, sidIsSystem(res.SID), "an ordinary account is not a system account: %s", res.SID)
	assert.NotEmpty(t, res.Groups, "the token carries groups (Users, Everyone…)")
	t.Logf("SID %s, %d group names (first: %v)", res.SID, len(res.Groups), res.Groups[:min(3, len(res.Groups))])

	// Wrong password and unknown account: the OS judges, with the code the
	// driver maps to "not authorized" - OR, on a machine that maps failed
	// network logons to the Guest account (the "bad user -> guest" fallback),
	// it signs the person in AS GUEST. That is a success to LogonUserW and the
	// reason the driver checks the SID after it: Guest (RID 501) is a
	// built-in account and is refused (sidIsSystem).
	for name, c := range map[string][2]string{
		"wrong password":  {user, pw + "-nope"},
		"unknown account": {user + "-nobody", pw},
	} {
		res, err := defaultLogon(c[0], ".", c[1])
		if err == nil {
			t.Logf("%s: the OS signed in anyway, as %s (guest fallback)", name, res.SID)
			assert.True(t, sidIsSystem(res.SID), "%s: a fallback sign-in must never look like a person: %s", name, res.SID)
			continue
		}
		var le *LogonError
		require.ErrorAs(t, err, &le, name)
		v, _ := judge(le.Code)
		assert.Equal(t, verdictWrong, v, "%s: Win32 %d", name, le.Code)
	}
}

// The whole driver on a real account, without a database: the name forms all
// reach the same local account, the password is checked by Windows.
func TestLogonUserW_TheNameFormsThatReachTheMachine(t *testing.T) {
	user, pw := smokeAccount(t)
	host, _ := os.Hostname()
	for _, typed := range []string{user, `.\` + user, host + `\` + user, strings.ToUpper(user)} {
		a, ok := parseAccount(typed, "", "", "")
		require.True(t, ok, typed)
		_, err := defaultLogon(a.LogonUser, a.LogonDomain, pw)
		assert.NoError(t, err, "%q -> user %q domain %q", typed, a.LogonUser, a.LogonDomain)
	}
}

// The whole driver on the real operating system: a real account signs in and is
// opened, a wrong password and an unknown account are the ordinary refusal
// (whether Windows says so or falls back to Guest), the built-in accounts are
// refused, and the provider test signs the account in for real.
func TestRealDriver_EndToEndOnThisMachine(t *testing.T) {
	user, pw := smokeAccount(t)
	ctx := context.Background()
	store := newStore(t)
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{"auto_create": true}), "the real call is wired")

	u, tok, err := d.Login(ctx, user, pw)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, strings.ToLower(user)+"@local", u.Email)

	_, _, err = d.Login(ctx, user, pw+"-nope")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, _, err = d.Login(ctx, user+"-nobody", pw)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, gerr := store.GetUserByEmail(ctx, strings.ToLower(user)+"-nobody@local")
	assert.Error(t, gerr, "no account for a name Windows does not know")
	_, _, err = d.Login(ctx, "Administrator", pw)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)

	ta := &auth.TestAccount{Username: user, Password: pw}
	checks := d.Probe(auth.WithTestAccount(ctx, ta), map[string]any{}, nil)
	assert.True(t, auth.ProbeOKAll(checks), "%+v", checks)
	assert.Equal(t, strings.ToLower(user)+"@local", ta.Email)
	bad := &auth.TestAccount{Username: user, Password: pw + "-nope"}
	assert.False(t, auth.ProbeOKAll(d.Probe(auth.WithTestAccount(ctx, bad), map[string]any{}, nil)))
}
