package appdata

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stores answers every engine this run can reach: SQLite always, PostgreSQL
// when FILEX_TEST_PG_DSN names one (the engines job, docs/DATABASES.md).
func stores(t *testing.T, maxBytes int64) map[string]Store {
	t.Helper()
	out := map[string]Store{}
	s, err := OpenSQLite(t.TempDir(), maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	out["sqlite"] = s
	if dsn := os.Getenv("FILEX_TEST_PG_DSN"); dsn != "" {
		p, err := OpenPostgres(context.Background(), dsn, "", maxBytes)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close() })
		out["postgres"] = p
	}
	return out
}

// app is a fresh app name per test, so a shared PostgreSQL starts empty.
func app(t *testing.T) string {
	t.Helper()
	n := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	if len(n) > 28 {
		n = n[len(n)-28:]
	}
	return "t" + strings.Trim(n, "-")
}

func TestStore_ReadWriteDeleteAndConditions(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t, 0) {
		t.Run(name, func(t *testing.T) {
			a := app(t)
			_ = s.Drop(ctx, a)
			sc := TenantScope(7)
			if _, ok, err := s.Get(ctx, a, sc, "c/x"); ok || err != nil {
				t.Fatalf("empty: %v %v", ok, err)
			}
			rev, err := s.Put(ctx, a, sc, "c/x", "one", Cond{IfAbsent: true})
			if err != nil || rev != 1 {
				t.Fatalf("claim: %d %v", rev, err)
			}
			if _, err := s.Put(ctx, a, sc, "c/x", "two", Cond{IfAbsent: true}); !errors.Is(err, ErrConflict) {
				t.Fatalf("second claim: %v", err)
			}
			if _, err := s.Put(ctx, a, sc, "c/x", "two", Cond{IfRev: 9}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale rev: %v", err)
			}
			if rev, err = s.Put(ctx, a, sc, "c/x", "two", Cond{IfRev: 1}); err != nil || rev != 2 {
				t.Fatalf("cas: %d %v", rev, err)
			}
			e, ok, _ := s.Get(ctx, a, sc, "c/x")
			if !ok || e.Value != "two" || e.Rev != 2 {
				t.Fatalf("got %+v", e)
			}
			// Another tenant's scope and the app scope are other keys.
			if _, ok, _ := s.Get(ctx, a, TenantScope(8), "c/x"); ok {
				t.Fatal("tenant 8 reads tenant 7")
			}
			if _, ok, _ := s.Get(ctx, a, AppScope, "c/x"); ok {
				t.Fatal("the app scope reads a tenant's")
			}
			// Another app is another store.
			if _, ok, _ := s.Get(ctx, a+"z", sc, "c/x"); ok {
				t.Fatal("another app reads it")
			}
			if del, err := s.Delete(ctx, a, sc, "c/x", 1); !errors.Is(err, ErrConflict) || del {
				t.Fatalf("delete stale: %v %v", del, err)
			}
			if del, err := s.Delete(ctx, a, sc, "c/x", 0); err != nil || !del {
				t.Fatalf("delete: %v %v", del, err)
			}
			if del, _ := s.Delete(ctx, a, sc, "c/x", 0); del {
				t.Fatal("deleted twice")
			}
			if keys, bytes, _ := s.Usage(ctx, a); keys != 0 || bytes != 0 {
				t.Fatalf("usage after delete: %d keys %d bytes", keys, bytes)
			}
		})
	}
}

// Many callers racing for one code: exactly one gets it.
func TestStore_OneClaimWins(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t, 0) {
		t.Run(name, func(t *testing.T) {
			a := app(t)
			_ = s.Drop(ctx, a)
			var won, lost atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := s.Put(ctx, a, AppScope, "c/race", "x", Cond{IfAbsent: true})
					switch {
					case err == nil:
						won.Add(1)
					case errors.Is(err, ErrConflict):
						lost.Add(1)
					default:
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if won.Load() != 1 || lost.Load() != 15 {
				t.Fatalf("won %d lost %d", won.Load(), lost.Load())
			}
		})
	}
}

func TestStore_Counters(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t, 0) {
		t.Run(name, func(t *testing.T) {
			a := app(t)
			_ = s.Drop(ctx, a)
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := s.Incr(ctx, a, AppScope, "n/x", 2); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if n, _ := s.Incr(ctx, a, AppScope, "n/x", 0); n != 40 {
				t.Fatalf("counter %d, want 40", n)
			}
			if _, err := s.Put(ctx, a, AppScope, "n/x", "1", Cond{}); !errors.Is(err, ErrCounter) {
				t.Fatalf("value over a counter: %v", err)
			}
			_, _ = s.Put(ctx, a, AppScope, "v", "text", Cond{})
			if _, err := s.Incr(ctx, a, AppScope, "v", 1); !errors.Is(err, ErrCounter) {
				t.Fatalf("counting a value: %v", err)
			}
		})
	}
}

// Keys come back in byte order on every engine (a locale collation would put
// `a-b` after `ab`), a page at a time, held to their prefix.
func TestStore_ListByteOrderAndPages(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t, 0) {
		t.Run(name, func(t *testing.T) {
			a := app(t)
			_ = s.Drop(ctx, a)
			for _, k := range []string{"u/1/ab", "u/1/a-b", "u/1/B", "u/1/a", "u/2/a", "u/10/a", "v"} {
				if _, err := s.Put(ctx, a, AppScope, k, k, Cond{}); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			after := ""
			for {
				page, next, err := s.List(ctx, a, AppScope, "u/1/", after, 2)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range page {
					got = append(got, e.Key)
				}
				if next == "" {
					break
				}
				after = next
			}
			want := []string{"u/1/B", "u/1/a", "u/1/a-b", "u/1/ab"}
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("got %v, want %v", got, want)
			}
			all, _, _ := s.List(ctx, a, AppScope, "", "", 100)
			if len(all) != 7 {
				t.Fatalf("whole scope: %d", len(all))
			}
		})
	}
}

func TestStore_QuotaAndDrop(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t, 1024) {
		t.Run(name, func(t *testing.T) {
			a := app(t)
			_ = s.Drop(ctx, a)
			if _, err := s.Put(ctx, a, AppScope, "a", strings.Repeat("x", 900), Cond{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Put(ctx, a, AppScope, "b", strings.Repeat("x", 200), Cond{}); !errors.Is(err, ErrQuota) {
				t.Fatalf("over the limit: %v", err)
			}
			// Shrinking a value is always allowed.
			if _, err := s.Put(ctx, a, AppScope, "a", "small", Cond{}); err != nil {
				t.Fatalf("shrink: %v", err)
			}
			if keys, bytes, _ := s.Usage(ctx, a); keys != 1 || bytes != int64(len("a")+len("small")) {
				t.Fatalf("usage %d keys %d bytes", keys, bytes)
			}
			if err := s.Drop(ctx, a); err != nil {
				t.Fatal(err)
			}
			if keys, _, _ := s.Usage(ctx, a); keys != 0 {
				t.Fatalf("after drop: %d keys", keys)
			}
			if _, ok, _ := s.Get(ctx, a, AppScope, "a"); ok {
				t.Fatal("a key survived the drop")
			}
		})
	}
}

func TestStore_RefusesWhatCannotBeStored(t *testing.T) {
	ctx := context.Background()
	s := stores(t, 0)["sqlite"]
	for _, c := range []struct {
		app, scope, key string
		err             error
	}{
		{"x", AppScope, "", ErrKey},
		{"x", AppScope, "a b", ErrKey},
		{"x", AppScope, "caf\xc3\xa9", ErrKey},
		{"x", AppScope, strings.Repeat("k", MaxKeyBytes+1), ErrKey},
		{"x", "t:-1", "k", ErrScope},
		{"x", "tenant", "k", ErrScope},
		{"../x", AppScope, "k", ErrApp},
		{"X", AppScope, "k", ErrApp},
	} {
		if _, err := s.Put(ctx, c.app, c.scope, c.key, "v", Cond{}); !errors.Is(err, c.err) {
			t.Fatalf("%q %q %q: %v, want %v", c.app, c.scope, c.key, err, c.err)
		}
	}
	if _, err := s.Put(ctx, "x", AppScope, "k", "\xff", Cond{}); !errors.Is(err, ErrValue) {
		t.Fatalf("not UTF-8: %v", err)
	}
	if _, err := s.Put(ctx, "x", AppScope, "k", strings.Repeat("v", MaxValueBytes+1), Cond{}); !errors.Is(err, ErrValue) {
		t.Fatalf("too long: %v", err)
	}
}

func TestSameDatabase(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"postgres://u:p@db:5432/filex", "postgres://other@DB/filex?sslmode=disable", true},
		{"postgres://u@db/filex_apps", "postgres://u@db/filex", false},
		{"host=db dbname=filex user=x", "postgres://u@db:5432/filex", true},
		{"postgres://u@db:5433/filex", "postgres://u@db/filex", false},
		{"postgres://u@db/filex", "", false},
	} {
		if got := sameDatabase(c.a, c.b); got != c.same {
			t.Fatalf("%q vs %q: %v", c.a, c.b, got)
		}
	}
}
