package loginguard_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/loginguard"
)

// The realm is part of the account counter (#128): `acme/alex`, `beta/alex`
// and `alex` are three people on a multi-tenant install, and one of them
// guessing wrong must not lock the others.
func TestSubjectCarriesTheRealm(t *testing.T) {
	require.Equal(t, "acme/alex", loginguard.SubjectIn("ACME", " Alex "))
	require.Equal(t, "acme/alex", loginguard.SubjectIn("acme", `CORP\alex`), "the domain prefix goes, the realm stays")
	require.Equal(t, "acme/alex", loginguard.Subject("acme/alex"), "the key an administrator copies from the list")
	require.Equal(t, "acme/alex", loginguard.Subject(`Acme/CORP\Alex`))
	require.Equal(t, "alex", loginguard.SubjectIn("", "alex"))
	require.Equal(t, "alex", loginguard.Subject(`.\alex`), "the DOMAIN\\ fold is unchanged")
	require.Equal(t, "x/y", loginguard.Subject(`CORP\x/y`), "a slash after a backslash is no realm: only the domain fold applies")
	require.Equal(t, "a@b/c", loginguard.Subject("a@b/c"), "nor one after an @")
	require.NotEqual(t, loginguard.SubjectIn("acme", "alex"), loginguard.SubjectIn("beta", "alex"))
	require.NotEqual(t, loginguard.SubjectIn("acme", "alex"), loginguard.Subject("alex"))
}

func TestARealmsLockIsItsOwn(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	inRealm := func(realm, id, ip string) loginguard.Attempt {
		a := web(id, ip)
		a.Realm = realm
		return a
	}
	for i := 0; i < 5; i++ {
		fail(g, inRealm("acme", "alex", "203.0.113.1"))
	}
	require.True(t, g.Check(ctx, inRealm("acme", "alex", "198.51.100.7")).Blocked, "acme's alex is locked")
	require.True(t, g.Check(ctx, web("acme/alex", "198.51.100.7")).Blocked,
		"the same account typed as realm/name over a protocol shares the counter")
	require.False(t, g.Check(ctx, inRealm("beta", "alex", "198.51.100.7")).Blocked, "beta's alex is not")
	require.False(t, g.Check(ctx, web("alex", "198.51.100.7")).Blocked, "the platform's alex is not")
}
