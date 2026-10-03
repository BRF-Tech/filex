package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveEmail(t *testing.T) {
	cases := []struct{ name, domain, token, want string }{
		{"alex", "", "", "alex@local"},
		{"Alex", "", "", "alex@local"},
		{"alex", "", "evim", "alex@evim"},
		{"alex", "corp.example", "evim", "alex@corp.example"},
		{"alex", "@Corp.Example", "", "alex@corp.example"},
		{"alex@example.com", "corp.example", "evim", "alex@example.com"},
		{"  ALEX@CORP.EXAMPLE ", "", "", "alex@corp.example"},
		{"", "corp.example", "", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, DeriveEmail(c.name, c.domain, "", c.token), "%q %q %q", c.name, c.domain, c.token)
	}
}

// LoginNameOf is DeriveEmail backwards, for the no-domain case only: what a
// provider made up it can take apart; a real mailbox it leaves alone.
func TestLoginNameOf(t *testing.T) {
	ok := []struct{ email, token, want string }{
		{"alex@local", "", "alex"},
		{" Alex@LOCAL ", "", "alex"},
		{"alex@local", "local", "alex"},
		{"alex@evim", "evim", "alex"},
		{"alex@evim", " Evim ", "alex"},
		{"ayse.k@local", "", "ayse.k"},
	}
	for _, c := range ok {
		got, found := LoginNameOf(c.email, "", c.token)
		assert.True(t, found, "%q %q", c.email, c.token)
		assert.Equal(t, c.want, got, "%q %q", c.email, c.token)
		// And it is the name DeriveEmail made the address from.
		assert.Equal(t, Normalize(c.email), DeriveEmail(got, "", "", c.token))
	}
	for _, c := range []struct{ email, token string }{
		{"alex", ""},               // a bare name is not an address
		{"alex@corp.example", ""},  // a real mailbox
		{"alex@local", "evim"},     // another installation's token
		{"alex@local.example", ""}, // the token is the whole domain, not a prefix
		{"@local", ""},             // no name
		{"a@b@local", ""},          // not one address
		{"", ""},
	} {
		got, found := LoginNameOf(c.email, "", c.token)
		assert.False(t, found, "%q %q", c.email, c.token)
		assert.Empty(t, got)
	}
}

func TestEmailToken(t *testing.T) {
	got, err := EmailToken("")
	require.NoError(t, err)
	assert.Equal(t, DefaultEmailToken, got)

	got, err = EmailToken(" @Evim ")
	require.NoError(t, err)
	assert.Equal(t, "evim", got)

	for _, bad := range []string{"a b", "evim!", "-x", "x-", ".x", "a..b", "ev@im", "é"} {
		_, err := EmailToken(bad)
		assert.ErrorIs(t, err, ErrBadEmailToken, bad)
	}
}

type fakeChecker struct {
	sys bool
	err error
}

func (f fakeChecker) IsSystemAccount(context.Context, string) (bool, error) { return f.sys, f.err }

func TestCheckOSLogin(t *testing.T) {
	ctx := context.Background()
	ok := []struct{ raw, want string }{{"alex", "alex"}, {"Alex", "alex"}, {" ayse.k ", "ayse.k"}}
	for _, c := range ok {
		n, ref := CheckOSLogin(ctx, c.raw, nil)
		assert.Equal(t, c.want, n)
		assert.Empty(t, ref, c.raw)
	}
	forbidden := []string{"root", "ROOT", "Administrator", "administrator", "admin", "Guest", "daemon", "www-data",
		"systemd-network", "_www", "DefaultAccount", "WDAGUtilityAccount", "nobody", "sshd", "filex"}
	for _, raw := range forbidden {
		n, ref := CheckOSLogin(ctx, raw, nil)
		assert.Empty(t, n, raw)
		assert.Equal(t, RefuseForbidden, ref, raw)
	}
	invalid := []string{"", "ab", "1alex", "a b c", "alex@corp", "ça va", "aléx", "DOMAIN" + string(rune(92)) + "alex"}
	for _, raw := range invalid {
		n, ref := CheckOSLogin(ctx, raw, nil)
		assert.Empty(t, n, raw)
		assert.Equal(t, RefuseInvalidName, ref, raw)
	}
}

func TestCheckOSLogin_TheOSSaysSystemAccount(t *testing.T) {
	ctx := context.Background()
	n, ref := CheckOSLogin(ctx, "backupsvc", fakeChecker{sys: true})
	assert.Empty(t, n)
	assert.Equal(t, RefuseForbidden, ref)

	n, ref = CheckOSLogin(ctx, "alex", fakeChecker{})
	assert.Equal(t, "alex", n)
	assert.Empty(t, ref)

	// A checker that cannot answer refuses: never let an account through because
	// nobody could say it was a service account.
	n, ref = CheckOSLogin(ctx, "alex", fakeChecker{err: errors.New("passwd unreadable")})
	assert.Empty(t, n)
	assert.Equal(t, RefuseForbidden, ref)
}
