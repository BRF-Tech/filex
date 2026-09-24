package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// These lines are the contract with the desktop app's per-pair status
// (desktop/src/syncstatus.ts): an error is shown under the folder it happened
// in and cleared by that folder's next clean pass.

var p1 = filesync.Pair{ID: "pair-1", Local: "/x", Remote: "docs://x"}

func TestAFailedPassSaysSoInItsOwnSummary(t *testing.T) {
	var out, errOut bytes.Buffer
	writeResult(&out, &errOut, p1, filesync.Result{
		Planned: 2, Applied: 1, Downloaded: 1,
		Errors:  []string{"download a.txt: connection reset"},
		Skipped: []string{"link"},
	})
	if !strings.Contains(out.String(), "pair-1: 1/2 done") || !strings.Contains(out.String(), ", 1 failed") {
		t.Fatalf("the summary must carry the verdict: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "pair-1: ! download a.txt: connection reset\n") {
		t.Fatalf("the error must name its pair: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "pair-1: note: skipped 1") {
		t.Fatalf("a skipped item is a note, not an error: %q", errOut.String())
	}
}

// A targeted pass can fail without planning anything (a folder it could not
// list). It must not call itself "already in step" — that is the line that
// clears an error.
func TestAPassThatFailedBeforePlanningIsNotInStep(t *testing.T) {
	var out, errOut bytes.Buffer
	writeResult(&out, &errOut, p1, filesync.Result{Errors: []string{`folder "a": HTTP 502`}})
	if strings.Contains(out.String(), "already in step") {
		t.Fatalf("a failed pass claimed to be in step: %q", out.String())
	}
	if !strings.Contains(out.String(), ", 1 failed") {
		t.Fatalf("summary = %q", out.String())
	}
}

// Quiet no-op passes are the echo of the engine's own uploads — except the
// first clean one after a failure, which is the only thing that clears the
// failure in the desktop app before the next full check.
func TestTheFirstCleanPassAfterAFailureIsAlwaysReported(t *testing.T) {
	var out, errOut bytes.Buffer
	r := newPassReporter(&out, &errOut)

	r.pass(p1, true, filesync.Result{}, nil)
	if out.Len() != 0 {
		t.Fatalf("an echo pass must stay quiet: %q", out.String())
	}
	r.pass(p1, true, filesync.Result{Planned: 1, Errors: []string{"upload a: HTTP 500"}}, nil)
	out.Reset()
	r.pass(p1, true, filesync.Result{}, nil)
	if out.String() != "pair-1: already in step\n" {
		t.Fatalf("the recovery must be reported: %q", out.String())
	}
	out.Reset()
	r.pass(p1, true, filesync.Result{}, nil)
	if out.Len() != 0 {
		t.Fatalf("once recovered, echoes are quiet again: %q", out.String())
	}

	r.pass(p1, true, filesync.Result{}, errors.New("list docs://x: HTTP 502"))
	if errOut.String() == "" || !strings.HasPrefix(strings.Split(errOut.String(), "\n")[1], "pair-1: list docs://x") {
		t.Fatalf("a run error names its pair: %q", errOut.String())
	}
	out.Reset()
	r.pass(p1, true, filesync.Result{}, nil)
	if out.String() != "pair-1: already in step\n" {
		t.Fatalf("recovery after a run error must be reported too: %q", out.String())
	}
}

// localState writes exactly the lines syncstatus.ts parses.
func TestLocalStateLines(t *testing.T) {
	var buf bytes.Buffer
	l := &liveLoop{out: &buf}
	l.localState("pair-1", fmt.Errorf("%w: more than 4000 items (macOS watches every file separately)", errTooLargeToWatch))
	l.localState("pair-1", errors.New("too many open files"))
	l.localState("pair-1", nil)
	want := "pair-1: local: poll-only — too-large — more than 4000 items (macOS watches every file separately)\n" +
		"pair-1: local: poll-only — unavailable — too many open files\n" +
		"pair-1: local: watched\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

type reportRec struct {
	mu   sync.Mutex
	errs []string // "<pair>=<err or ok>"
}

func (r *reportRec) report(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.errs = append(r.errs, id+"=ok")
		return
	}
	r.errs = append(r.errs, id+"="+err.Error())
}

func (r *reportRec) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errs...)
}

// A folder too large to watch is reported for ITS pair, once; when it shrinks
// back under the budget the same pair is told it is watched again.
func TestTheWatcherReportsPerPairAndRecovers(t *testing.T) {
	defer func(b int, a bool) { kqueueBudget, budgetApplies = b, a }(kqueueBudget, budgetApplies)
	kqueueBudget, budgetApplies = 3, true

	big := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f%d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	small := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-yet")
	rec := &reportRec{}
	lw, err := newLocalWatcher("", func(string, string) {}, nil, rec.report)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go lw.Run(ctx)

	pairs := []filesync.Pair{
		{ID: "big", Local: big, Remote: "docs://big"},
		{ID: "small", Local: small, Remote: "docs://small"},
		{ID: "later", Local: missing, Remote: "docs://later"},
	}
	lw.Sync(pairs)
	got := rec.get()
	if len(got) != 1 || !strings.HasPrefix(got[0], "big=") || !strings.Contains(got[0], "too large to watch") {
		t.Fatalf("only the big pair is reported, as too large: %v", got)
	}
	lw.Sync(pairs) // the steady state is not re-reported
	if n := len(rec.get()); n != 1 {
		t.Fatalf("reported again without a change: %v", rec.get())
	}

	for i := 0; i < 4; i++ {
		_ = os.Remove(filepath.Join(big, fmt.Sprintf("f%d.txt", i)))
	}
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	lw.Sync(pairs)
	got = rec.get()
	if len(got) != 2 || got[1] != "big=ok" {
		t.Fatalf("the recovery must be reported for that pair only (a folder that was merely missing never was reported): %v", got)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestWatchBudgetEnv(t *testing.T) {
	defer func(b int, a bool) { kqueueBudget, budgetApplies = b, a }(kqueueBudget, budgetApplies)
	kqueueBudget, budgetApplies = 4000, false
	for _, bad := range []string{"", "0", "-3", "many"} {
		applyWatchBudgetEnv(func(string) string { return bad })
		if budgetApplies || kqueueBudget != 4000 {
			t.Fatalf("%q must be ignored", bad)
		}
	}
	applyWatchBudgetEnv(func(string) string { return " 25 " })
	if !budgetApplies || kqueueBudget != 25 {
		t.Fatalf("budget = %d applies=%v", kqueueBudget, budgetApplies)
	}
}
