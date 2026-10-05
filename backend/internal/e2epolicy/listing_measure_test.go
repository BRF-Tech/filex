//go:build measure

package e2epolicy_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestMeasureAListingUnderTheApprovalPolicy times the rule's question - "may
// this person encrypt here?" - for a request as large as the endpoint takes
// (POST /api/files/e2e/allowed, up to 1000 paths; the explorer itself sends
// one, when a menu opens on a folder), for somebody who holds files.encrypt and
// is not an administrator, under the approval policy, on a catalogue the size
// of a real install (170,245 rows, 152,562 of them in the storage asked about)
// in one SQLite file opened as the server opens it: one connection, WAL.
//
//	FILEX_MEASURE_ORDER=shuffled   write the rows in an order unrelated to
//	                               their paths (dbtest.SeedCatalogue)
//
//	go test -tags measure -run TestMeasureAListingUnderTheApprovalPolicy -v -timeout 60m ./internal/e2epolicy
//
// It asks only what the package has had since 0.51, so this file and
// internal/testutil/dbtest/catalogue.go, copied to a tree from before
// migration 00083, give the other half of the comparison. No storage driver
// is wired: the figures are the catalogue's share of the answer, which is the
// part 00083 changes.
//
// ⚠ Compare only numbers taken on the same machine, one run after another.
func TestMeasureAListingUnderTheApprovalPolicy(t *testing.T) {
	ctx := context.Background()
	drv := db.MustGet("sqlite")
	conn, err := drv.Open(ctx, filepath.Join(t.TempDir(), "filex.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(ctx, drv, conn))
	store := drv.NewStore(conn)

	f := &fix{store: store, now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	f.st, err = store.CreateStorage(ctx, &model.Storage{
		Name: "Depo", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	other, err := store.CreateStorage(ctx, &model.Storage{
		Name: "Diger", Driver: "local", MountPath: "/data/other", ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	f.tenant, err = store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, f.tenant.ID, f.st.ID))
	f.admin = f.user(t, "admin@alpha.test", model.RoleAdmin)
	f.ada = f.user(t, "ada@alpha.test", model.RoleUser)
	f.svc = f.serviceOn(store)
	t.Cleanup(perm.Invalidate)
	f.policy(t, true, model.E2EPolicyApproval)

	shuffled := os.Getenv("FILEX_MEASURE_ORDER") == "shuffled"
	rows := dbtest.SeedCatalogue(t, store, f.st.ID, "/Arşiv", 60, 25, 100, shuffled)
	rows += dbtest.SeedCatalogue(t, store, other.ID, "/Arşiv", 7, 25, 100, shuffled)
	rows += dbtest.SeedEmptyFolders(t, store, f.st.ID, "/Yeni", 1000)
	t.Logf("%d rows catalogued (shuffled: %v)", rows, shuffled)

	listing := func(n int, rel func(i int) string) []e2epolicy.Ask {
		asks := make([]e2epolicy.Ask, n)
		for i := range asks {
			asks[i] = e2epolicy.Ask{Rel: rel(i)}
		}
		return asks
	}
	for _, l := range []struct {
		name string
		asks []e2epolicy.Ask
	}{
		{"1000 folders of 100 files each", listing(1000, func(i int) string {
			return fmt.Sprintf("Arşiv/Müşteri-%02d/Sözleşmeler-%02d", i/25, i%25)
		})},
		{"1000 empty folders", listing(1000, func(i int) string { return fmt.Sprintf("Yeni/klasör-%04d", i) })},
		{"60 folders of 2,525 rows each", listing(60, func(i int) string { return fmt.Sprintf("Arşiv/Müşteri-%02d", i) })},
	} {
		// The first folder alone warms the engine up; then the whole listing,
		// again and again for two seconds or twenty times, once at least.
		f.svc.VerdictsFor(ctx, f.ada, f.st, l.asks[:1])
		var verdicts []e2epolicy.Verdict
		began, runs := time.Now(), 0
		for runs < 1 || (runs < 20 && time.Since(began) < 2*time.Second) {
			verdicts = f.svc.VerdictsFor(ctx, f.ada, f.st, l.asks)
			runs++
		}
		mean := time.Since(began) / time.Duration(runs)
		tally := map[e2epolicy.Answer]int{}
		for _, v := range verdicts {
			tally[v.Answer]++
		}
		t.Logf("a listing of %-32s %10s  %8.3f ms a folder  (request %d, allowed %d, denied %d; mean of %d)",
			l.name, mean.Round(time.Millisecond/10), float64(mean)/float64(time.Millisecond)/float64(len(l.asks)),
			tally[e2epolicy.AnswerRequest], tally[e2epolicy.AnswerAllowed], tally[e2epolicy.AnswerDenied], runs)
	}
}
