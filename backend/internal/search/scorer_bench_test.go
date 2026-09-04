package search

// What the re-rank costs.
//
// The scorer runs once per candidate per query piece, on every search,
// so its cost is multiplied by the over-fetch (searchOverFetch x limit,
// capped at searchMaxFetch). If the DP matrix allocated per call that
// would dominate the whole query, which is why scratch exists and why
// the allocation count is asserted rather than described.
//
// Run with:
//
//	go test ./internal/search/ -run xxx -bench Scorer -benchmem

import (
	"fmt"
	"testing"
)

// benchCandidates is a realistic candidate set: names of the length and
// shape a source tree produces, in nested folders.
func benchCandidates(n int) [][2]string {
	words := []string{"main", "handler", "service", "internal", "search", "scorer", "index", "client", "server", "config"}
	exts := []string{"go", "ts", "md", "json", "yaml"}
	out := make([][2]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s_%s-%d.%s", words[i%len(words)], words[(i/7)%len(words)], i, exts[i%len(exts)])
		path := fmt.Sprintf("/%s/%s/%s", words[(i/3)%len(words)], words[(i/11)%len(words)], name)
		out = append(out, [2]string{name, path})
	}
	return out
}

// BenchmarkScorerPerCandidate is the per-candidate cost the re-rank
// pays. Report ns/op as microseconds per candidate.
func BenchmarkScorerPerCandidate(b *testing.B) {
	cands := benchCandidates(512)
	for _, q := range []string{"main", "main go", "Code main", "internal/search/scorer.go"} {
		b.Run(q, func(b *testing.B) {
			pq := PrepareQuery(q)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = pq.ScoreName(cands[i%len(cands)][0], cands[i%len(cands)][1])
			}
		})
	}
}

// BenchmarkScorerResultSet is the number that matters operationally: the
// whole re-rank for one search, at the default limit of 50 and its
// four-times over-fetch (200 candidates), and at the searchMaxFetch cap.
func BenchmarkScorerResultSet(b *testing.B) {
	for _, n := range []int{200, searchMaxFetch} {
		b.Run(fmt.Sprintf("%d-candidates", n), func(b *testing.B) {
			cands := benchCandidates(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pq := PrepareQuery("Code main")
				for _, c := range cands {
					_ = pq.ScoreName(c[0], c[1])
				}
			}
		})
	}
}

// TestScorerDoesNotAllocatePerCall is the assertion behind the comment
// on scratch. Everything ScoreName needs — the DP matrices and the rune
// buffers for both targets — is reused, so the steady state is zero.
//
// The first version of this scorer scored 9 allocations per call: two
// rune slices per target, two lower-case slices, and a throwaway
// strings.ToLower for the path-identity check. At searchMaxFetch
// candidates that is 4500 allocations for one keystroke's worth of
// search, which is why this is a test and not a comment.
// TestScorerReusesItsBuffers is the assertion behind the comment on
// scratch: once warm, scoring allocates nothing at all.
//
// It measures a scratch directly rather than going through ScoreName,
// and that is deliberate. ScoreName takes its scratch from a sync.Pool,
// a GC drains the pool, and a test that measured the pooled path
// therefore measured whether a GC happened to land inside the loop —
// which it does, intermittently, when the whole suite runs. That test
// failed roughly one run in three while asserting something true. The
// invariant worth pinning is that the DP matrices and the rune buffers
// are reused, and this measures exactly that.
//
// The end-to-end figure lives in BenchmarkScorerPerCandidate, which
// amortises the pool over thousands of iterations and reports a flat
// 0 allocs/op.
func TestScorerReusesItsBuffers(t *testing.T) {
	sc := &scratch{}
	pieces := PrepareQuery("Code main").pieces
	label := sc.label.set("main.go")
	full := sc.full.set("Code/main.go")

	// Warm up: the first call grows every buffer to its final size.
	for _, p := range pieces {
		_ = sc.scoreFuzzy(label, p)
		_ = sc.scoreFuzzy(full, p)
	}

	got := testing.AllocsPerRun(1000, func() {
		for _, p := range pieces {
			_ = sc.scoreFuzzy(label, p)
			_ = sc.scoreFuzzy(full, p)
		}
	})
	if got != 0 {
		t.Errorf("scoring allocates %.0f times per candidate, want 0 — are the scratch buffers still being reused?", got)
	}
}
