package loginguard_test

// FILEX_TRUSTED_PROXIES seeds the trusted-proxy list (task #258, 0.57). What
// is pinned: an upgraded install whose list was never saved gets the
// variable's list EXACTLY as typed - `auto` and the deployed shape
// "auto, <gateway>" included - so nothing it trusts changes; a saved list is
// never written over by a start; `reseed` writes it on purpose and never drops
// a typed list to `auto` for want of a variable.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func storedProxies(t *testing.T, st interface {
	GetSetting(context.Context, string) (string, error)
}) string {
	t.Helper()
	v, _ := st.GetSetting(context.Background(), loginguard.KeyTrustedProxy)
	return v
}

func TestSeedTrustedProxies_AnUpgradeKeepsTheVariablesListAsTyped(t *testing.T) {
	for _, list := range []string{"auto", "auto, 172.18.0.1", "auto,203.0.113.0/24,198.51.100.7", "none"} {
		t.Run(list, func(t *testing.T) {
			_, st := testutil.NewTestDB(t)
			ctx := context.Background()

			differs, err := loginguard.SeedTrustedProxies(ctx, st, " "+list+" ")
			require.NoError(t, err)
			assert.False(t, differs)
			assert.Equal(t, list, storedProxies(t, st), "written as typed (trimmed), not rewritten")

			// And the running guard trusts exactly what the variable said.
			g := loginguard.New(st)
			require.NoError(t, g.Load(ctx))
			got := g.TrustedProxySource("")()
			want, err := clientip.ParseList(list)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, want.Effective(), got.Effective())
			assert.Equal(t, want.Classes(), got.Classes(), "`auto` stays a word, resolved where filex runs")
		})
	}
}

func TestSeedTrustedProxies_ASavedListIsNeverWrittenOver(t *testing.T) {
	_, st := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, st.UpsertSetting(ctx, loginguard.KeyTrustedProxy, "auto, 192.0.2.10"))

	differs, err := loginguard.SeedTrustedProxies(ctx, st, "auto, 172.18.0.1")
	require.NoError(t, err)
	assert.True(t, differs, "the start says the variable no longer applies")
	assert.Equal(t, "auto, 192.0.2.10", storedProxies(t, st))

	differs, err = loginguard.SeedTrustedProxies(ctx, st, "192.0.2.10,AUTO")
	require.NoError(t, err)
	assert.False(t, differs, "the same list in another spelling is no drift")
}

func TestSeedTrustedProxies_NoVariableWritesNothing(t *testing.T) {
	_, st := testutil.NewTestDB(t)
	differs, err := loginguard.SeedTrustedProxies(context.Background(), st, "  ")
	require.NoError(t, err)
	assert.False(t, differs)
	assert.Equal(t, "", storedProxies(t, st), "no row: `auto` stays the built-in default, not pinned")
}

func TestSeedTrustedProxies_AnEmptiedListIsSeededAgainAtTheNextStart(t *testing.T) {
	// "Remove the saved list" stores nothing; the configuration's list is in
	// force at once (the fallback) and written back by the next start.
	_, st := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, st.UpsertSetting(ctx, loginguard.KeyTrustedProxy, ""))

	g := loginguard.New(st)
	require.NoError(t, g.Load(ctx))
	fallback := g.TrustedProxySource("auto, 172.18.0.1")()
	require.NotNil(t, fallback)
	assert.Contains(t, fallback.Effective(), "172.18.0.1")

	_, err := loginguard.SeedTrustedProxies(ctx, st, "auto, 172.18.0.1")
	require.NoError(t, err)
	assert.Equal(t, "auto, 172.18.0.1", storedProxies(t, st))
}

func TestReseedTrustedProxies(t *testing.T) {
	_, st := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, st.UpsertSetting(ctx, loginguard.KeyTrustedProxy, "auto, 192.0.2.10"))

	wrote, err := loginguard.ReseedTrustedProxies(ctx, st, "auto, 172.18.0.1")
	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Equal(t, "auto, 172.18.0.1", storedProxies(t, st))

	wrote, err = loginguard.ReseedTrustedProxies(ctx, st, "")
	require.NoError(t, err)
	assert.False(t, wrote, "no variable: the saved list stays, never dropped to auto")
	assert.Equal(t, "auto, 172.18.0.1", storedProxies(t, st))

	_, err = loginguard.ReseedTrustedProxies(ctx, st, "auto, not-an-address")
	require.Error(t, err, "a list the page would refuse is refused here too")
	assert.Equal(t, "auto, 172.18.0.1", storedProxies(t, st))
}

func TestSameProxyList(t *testing.T) {
	assert.True(t, loginguard.SameProxyList("auto, 192.0.2.10", "192.0.2.10,AUTO"))
	assert.True(t, loginguard.SameProxyList("auto;192.0.2.10\n", " auto  192.0.2.10"))
	assert.False(t, loginguard.SameProxyList("auto", "auto, 192.0.2.10"))
	assert.False(t, loginguard.SameProxyList("auto, 192.0.2.10", "auto, 192.0.2.11"))
}

func TestProxyConfigSource(t *testing.T) {
	t.Setenv(loginguard.ProxyEnvVar, "auto")
	assert.Equal(t, "FILEX_TRUSTED_PROXIES", loginguard.ProxyConfigSource())
	t.Setenv(loginguard.ProxyEnvVar, "")
	assert.Equal(t, "config.yaml trusted_proxies", loginguard.ProxyConfigSource())
}
