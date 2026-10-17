package search

// The search settings (task #261): db -> env -> default, and config.yaml
// seeding when the variable is not set. Each case is a setting an install
// already had before 0.57 and must keep across the upgrade.

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// rowsStore is a settings table in a map.
type rowsStore map[string]string

func (m rowsStore) GetSetting(_ context.Context, key string) (string, error) { return m[key], nil }
func (m rowsStore) UpsertSetting(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

// noSearchEnv clears every search variable for the test.
func noSearchEnv(t *testing.T) {
	t.Helper()
	for _, s := range []string{"FILEX_SEARCH_CONTENT", "FILEX_SEARCH_CONTENT_MAX", "FILEX_SEARCH_MEMORY_PERCENT", "FILEX_SEARCH_AUTO_REBUILD"} {
		t.Setenv(s, "")
	}
}

func TestSeedSettings_FromTheVariables(t *testing.T) {
	noSearchEnv(t)
	t.Setenv("FILEX_SEARCH_CONTENT", "0")
	// 9.54 MB in bytes: rounded UP, the cap can only stay or grow.
	t.Setenv("FILEX_SEARCH_CONTENT_MAX", "10000000")
	t.Setenv("FILEX_SEARCH_MEMORY_PERCENT", " 30% ")
	t.Setenv("FILEX_SEARCH_AUTO_REBUILD", "false")
	st := rowsStore{}
	ctx := context.Background()
	SeedSettings(ctx, st, config.Default().Search)

	if ContentSetting.Resolve(ctx, st) {
		t.Error("FILEX_SEARCH_CONTENT=0 did not switch content search off")
	}
	if got := ContentMaxSetting.Resolve(ctx, st); got != 10 {
		t.Errorf("content_max_mb = %d, want 10 (10000000 bytes rounded up)", got)
	}
	if got := ContentMaxBytes(ctx, st); got != 10<<20 {
		t.Errorf("ContentMaxBytes = %d", got)
	}
	if got := MemoryPercentSetting.Resolve(ctx, st); got != 30 {
		t.Errorf("memory_percent = %d, want 30", got)
	}
	if AutoRebuildSetting.Resolve(ctx, st) {
		t.Error("FILEX_SEARCH_AUTO_REBUILD=false did not switch the repair off")
	}
}

func TestSeedSettings_FromConfigYAMLWhenTheVariableIsUnset(t *testing.T) {
	noSearchEnv(t)
	c := config.Default().Search
	c.Content = false
	c.ContentMaxBytes = 20 << 20
	c.MemoryPercent = 40
	c.AutoRebuild = false
	st := rowsStore{}
	ctx := context.Background()
	SeedSettings(ctx, st, c)
	if ContentSetting.Resolve(ctx, st) || AutoRebuildSetting.Resolve(ctx, st) {
		t.Error("a switch set off in config.yaml was lost")
	}
	if got := ContentMaxSetting.Resolve(ctx, st); got != 20 {
		t.Errorf("content_max_mb = %d, want 20", got)
	}
	if got := MemoryPercentSetting.Resolve(ctx, st); got != 40 {
		t.Errorf("memory_percent = %d, want 40", got)
	}
}

// The defaults are not written: a row holding a default pins it, and a later
// release that changes the default would not reach this install.
func TestSeedSettings_TheDefaultsAreNotPinned(t *testing.T) {
	noSearchEnv(t)
	st := rowsStore{}
	SeedSettings(context.Background(), st, config.Default().Search)
	if len(st) != 0 {
		t.Errorf("an unconfigured install wrote rows: %v", st)
	}
}

func TestSeedSettings_TheVariableBeatsConfigYAML(t *testing.T) {
	noSearchEnv(t)
	t.Setenv("FILEX_SEARCH_MEMORY_PERCENT", "40")
	c := config.Default().Search
	c.MemoryPercent = 10
	st := rowsStore{}
	SeedSettings(context.Background(), st, c)
	if got := MemoryPercentSetting.Resolve(context.Background(), st); got != 40 {
		t.Errorf("memory_percent = %d, want the variable's 40", got)
	}
}

// ⚠⚠ The variables are SEEDS: once a row exists - an admin saved the page -
// neither the variable nor config.yaml changes it.
func TestSeedSettings_TheRowWins(t *testing.T) {
	noSearchEnv(t)
	t.Setenv("FILEX_SEARCH_MEMORY_PERCENT", "40")
	t.Setenv("FILEX_SEARCH_CONTENT", "0")
	c := config.Default().Search
	c.ContentMaxBytes = 50 << 20
	st := rowsStore{
		MemoryPercentSetting.Key: "12",
		ContentSetting.Key:       "true",
		ContentMaxSetting.Key:    "7",
	}
	ctx := context.Background()
	SeedSettings(ctx, st, c)
	if got := MemoryPercentSetting.Resolve(ctx, st); got != 12 {
		t.Errorf("memory_percent = %d, the variable overrode the saved 12", got)
	}
	if !ContentSetting.Resolve(ctx, st) {
		t.Error("FILEX_SEARCH_CONTENT=0 overrode a saved switch")
	}
	if got := ContentMaxSetting.Resolve(ctx, st); got != 7 {
		t.Errorf("content_max_mb = %d, config.yaml overrode the saved 7", got)
	}
}

// The budget is a clamped share of the memory filex was given: an operator who
// asks for more than half gets half, and nothing below one percent. (Moved
// from internal/server with the function.)
func TestApplyMemoryPercent_ClampsTheShare(t *testing.T) {
	given := sysmem.Allocated()
	if given.Bytes == 0 {
		t.Skip("this platform reports no memory")
	}
	for _, c := range []struct{ asked, used int }{{25, 25}, {0, 25}, {90, 50}, {-5, 1}} {
		idx, err := Open(filepath.Join(t.TempDir(), "idx.bleve"))
		if err != nil {
			t.Fatal(err)
		}
		b := idx.ApplyMemoryPercent(c.asked)
		limit := idx.MemoryLimit()
		want := given.Bytes / 100 * uint64(c.used)
		if b.Percent != c.used || b.Bytes != want {
			t.Errorf("asked %d%%: applied %d%% = %d, want %d%% = %d", c.asked, b.Percent, b.Bytes, c.used, want)
		}
		// The limit is the baseline (what the process held) plus the budget.
		if limit < want || limit > want+sysmem.InUse()+(64<<20) {
			t.Errorf("asked %d%%: limit %d MiB, want %d MiB plus the baseline", c.asked, limit>>20, want>>20)
		}
		if got := idx.AppliedMemoryBudget(); got != b {
			t.Errorf("AppliedMemoryBudget = %+v, want %+v", got, b)
		}
		_ = idx.Close()
	}
}

// Saved again while filex runs, the budget is measured from the SAME
// baseline: what the index holds by then is not a new starting point.
func TestApplyMemoryPercent_AgainKeepsTheFirstBaseline(t *testing.T) {
	given := sysmem.Allocated()
	if given.Bytes == 0 {
		t.Skip("this platform reports no memory")
	}
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.install(idx)
	mem.held.Store(100 << 20)
	idx.ApplyMemoryPercent(10)
	first := idx.MemoryLimit()
	if want := uint64(100<<20) + given.Bytes/100*10; first != want {
		t.Fatalf("limit %d, want %d", first, want)
	}
	// The index has grown by the time an admin saves 20%.
	mem.held.Store(900 << 20)
	idx.ApplyMemoryPercent(20)
	if want := uint64(100<<20) + given.Bytes/100*20; idx.MemoryLimit() != want {
		t.Errorf("limit %d MiB, want %d MiB: the re-apply took a new baseline", idx.MemoryLimit()>>20, want>>20)
	}
}

func TestBytesToMB_RoundsUp(t *testing.T) {
	for raw, want := range map[string]int{"1": 1, "1048576": 1, "1048577": 2, "5242880": 5, " 10000000 ": 10} {
		got, ok := bytesToMB(raw)
		if !ok || got != want {
			t.Errorf("bytesToMB(%q) = %d, %v; want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "0", "-5", "5MB", strconv.Itoa(-1)} {
		if _, ok := bytesToMB(raw); ok {
			t.Errorf("bytesToMB(%q) accepted", raw)
		}
	}
}
