package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/version"
)

// #213 (A9, B17): the desktop app read the engine's English lines with
// regular expressions. `filex sync run --json` gives it events instead —
// {event, pair, code, params, message} — with the message said by the engine
// in the language the app names. These tests hold that contract.

// streamEvents decodes a --json stdout: every line must be one event.
func streamEvents(t *testing.T, out string) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev), "every line of the stream is one JSON event: %q", line)
		for _, k := range []string{"event", "code", "message"} {
			v, ok := ev[k].(string)
			require.True(t, ok && v != "", "event without %s: %q", k, line)
		}
		evs = append(evs, ev)
	}
	return evs
}

func jsonReporter(lang string) (*syncReporter, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &syncReporter{asJSON: true, lang: lang, out: &out, errOut: &errOut}, &out, &errOut
}

func param(ev map[string]any, k string) any {
	p, _ := ev["params"].(map[string]any)
	return p[k]
}

// Every code has its sentence in both shipped languages: a code without one
// would put the raw key on the desktop's screen.
func TestSyncEvents_EveryCodeIsSaidInBothLanguages(t *testing.T) {
	tr := srvtext.Builtin("tr")
	for _, code := range append(append([]string{}, syncEventCodes...), "time.hours", "time.minutes", "time.seconds") {
		key := srvtext.Prefix + "sync." + code
		require.True(t, srvtext.Has(key), "%s has no English sentence", key)
		_, ok := tr[key]
		require.True(t, ok, "%s has no Turkish sentence", key)
	}
}

// A pass with a failed action: the summary first, its error after it, all on
// ONE stream (stdout) — the order the desktop clears and sets errors by.
func TestSyncEvents_APassInTurkish(t *testing.T) {
	rep, out, errOut := jsonReporter("tr")
	rep.result(p1, filesync.Result{
		Planned: 2, Applied: 1, Downloaded: 1,
		Errors:  []string{"download a.txt: connection reset"},
		Skipped: []string{"link"},
	})
	require.Empty(t, errOut.String(), "the stream is stdout only")
	evs := streamEvents(t, out.String())
	require.Len(t, evs, 3, out.String())

	require.Equal(t, "pass", evs[0]["event"])
	require.Equal(t, "pair-1", evs[0]["pair"])
	require.Equal(t, "pass.done_failed", evs[0]["code"])
	require.EqualValues(t, 1, param(evs[0], "failed"))
	require.EqualValues(t, 2, param(evs[0], "planned"))
	require.Equal(t, "2 değişiklikten 1 tanesi yapıldı, 1 tanesi başarısız oldu", evs[0]["message"])

	require.Equal(t, "error", evs[1]["event"])
	require.Equal(t, "pass.action_failed", evs[1]["code"])
	require.Equal(t, "Bir öğe eşitlenemedi: download a.txt: connection reset", evs[1]["message"])

	require.Equal(t, "note", evs[2]["event"])
	require.Equal(t, "pass.skipped", evs[2]["code"])
}

func TestSyncEvents_AClearPassIsInStep(t *testing.T) {
	rep, out, _ := jsonReporter("en")
	rep.result(p1, filesync.Result{})
	evs := streamEvents(t, out.String())
	require.Len(t, evs, 1)
	require.Equal(t, "pass.in_step", evs[0]["code"])
	require.EqualValues(t, 0, param(evs[0], "failed"))
}

// The figures the desktop used to recover from "transfer: 3/10 (1.1 MiB of
// 49.0 GiB, about 8h 10m left)" with a regular expression.
func TestSyncEvents_TransferProgressCarriesItsFigures(t *testing.T) {
	for lang, wait := range map[string]string{"en": "8 h 10 min", "tr": "8 sa 10 dk"} {
		rep, out, _ := jsonReporter(lang)
		rep.progress("pair-1", filesync.ProgressEvent{
			Phase: "transfer", Done: 3, Total: 10, BytesDone: 1_200_000, BytesTotal: 52_600_000_000,
			ETA: 8*time.Hour + 10*time.Minute, HasETA: true,
		})
		evs := streamEvents(t, out.String())
		require.Len(t, evs, 1)
		ev := evs[0]
		require.Equal(t, "progress", ev["event"])
		require.Equal(t, "progress.transfer_eta", ev["code"])
		require.Equal(t, "transfer", param(ev, "phase"))
		require.EqualValues(t, 3, param(ev, "done"))
		require.EqualValues(t, 10, param(ev, "total"))
		require.EqualValues(t, 29400, param(ev, "eta_seconds"))
		require.EqualValues(t, 52_600_000_000, param(ev, "bytes_total"))
		msg := ev["message"].(string)
		require.Contains(t, msg, "3/10", lang)
		require.Contains(t, msg, wait, lang)
	}

	rep, out, _ := jsonReporter("en")
	rep.progress("pair-1", filesync.ProgressEvent{Phase: "inventory", Here: 1204, Listing: true, Listed: 48211, ListedFolders: 312})
	rep.progress("pair-1", filesync.ProgressEvent{Phase: "plan", Total: 1})
	rep.progress("pair-1", filesync.ProgressEvent{Phase: "hold", Held: 3})
	evs := streamEvents(t, out.String())
	require.Len(t, evs, 3)
	require.Equal(t, "progress.listing_here", evs[0]["code"])
	require.EqualValues(t, 1204, param(evs[0], "here"))
	require.EqualValues(t, 48211, param(evs[0], "listed"))
	require.Equal(t, "listing the server - 48,211 items so far (1,204 on this computer)", evs[0]["message"],
		"figures in the language's grouping, as the window wrote them")
	require.Equal(t, "1 change to make", evs[1]["message"], "one is singular")
	require.Equal(t, "hold", evs[2]["event"])
	require.EqualValues(t, 3, param(evs[2], "count"))
}

// The lines the folder card shows: in the reader's language, from the
// engine, with the state as a code the app can style by.
func TestSyncEvents_LocalLiveLockAndWindow(t *testing.T) {
	rep, out, _ := jsonReporter("tr")
	rep.local("pair-1", fmt.Errorf("%w: more than 4000 items", errTooLargeToWatch), 30*time.Second)
	rep.live(cliclient.StreamLive, "watching 3 folder(s)", 30*time.Second)
	rep.lockBusy(false, "pair-1", errors.New("another filex on this computer is syncing this pair (process 42, filex)"))
	w, err := parseSyncWindow("22:00-07:00")
	require.NoError(t, err)
	rep.window("window.waiting", w, at(12, 0))
	evs := streamEvents(t, out.String())
	require.Len(t, evs, 4)

	require.Equal(t, "local.too_large", evs[0]["code"])
	require.Equal(t, "too-large", param(evs[0], "state"))
	require.Equal(t, "more than 4000 items", param(evs[0], "detail"))
	require.Equal(t, "Bu bilgisayarda yapılan değişiklikler 30 saniyelik kontrolde bulunur - klasörde izlenemeyecek kadar çok öğe var", evs[0]["message"])

	require.Equal(t, "live", evs[1]["event"])
	require.Equal(t, "connected", param(evs[1], "state"))
	require.Equal(t, "Canlı - değişiklikler anında geliyor", evs[1]["message"])

	require.Equal(t, "lock.busy", evs[2]["code"])
	require.Contains(t, param(evs[2], "detail"), "process 42")
	require.Equal(t, "Bu bilgisayardaki başka bir filex bu klasörü eşitliyor - o kapandığında bu kopya devralır", evs[2]["message"])

	// B17: the window's next opening, so the app needs no clock arithmetic
	// of its own.
	require.Equal(t, "window.waiting", evs[3]["code"])
	require.Equal(t, "22:00-07:00", param(evs[3], "window"))
	require.Equal(t, at(22, 0).Format(time.RFC3339), param(evs[3], "opens_at"))
	require.Equal(t, "eşitleme saatleri bekleniyor, 22:00-07:00", evs[3]["message"])
}

// Without --json nothing changes for a person in a terminal.
func TestSyncEvents_PlainLinesAreTheSame(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := plainReporter(&out, &errOut)
	rep.live(cliclient.StreamOffline, "connect: refused; retrying in 4s", 30*time.Second)
	rep.lockAcquired("pair-1")
	rep.passFailed(p1, errors.New("list docs://x: HTTP 502"))
	w, _ := parseSyncWindow("22:00-07:00")
	rep.window("window.closed", w, at(7, 0))
	rep.progress("pair-1", filesync.ProgressEvent{Phase: "plan", Total: 3}) // stream only
	require.Equal(t, "live: offline - connect: refused; retrying in 4s\n"+
		"pair-1: lock: acquired\n"+
		"sync: the sync window 22:00-07:00 closed; the rest continues when it opens\n", out.String())
	require.Equal(t, "pair-1: list docs://x: HTTP 502\n", errOut.String())
}

// The error a run stops with is one "fatal" event, and main says nothing
// more about it; its exit status is kept.
func TestSyncEvents_FatalIsSaidOnce(t *testing.T) {
	rep, out, _ := jsonReporter("en")
	err := rep.fatal(&exitError{code: exitPairBusy, err: errPairsBusy([]string{"pair-1", "pair-2"})})
	require.True(t, alreadyReported(err))
	require.Equal(t, exitPairBusy, exitCode(err))
	evs := streamEvents(t, out.String())
	require.Len(t, evs, 1)
	require.Equal(t, "fatal", evs[0]["event"])
	require.Equal(t, "pairs_busy", evs[0]["code"])
	require.EqualValues(t, exitPairBusy, param(evs[0], "exit"))
	require.Equal(t, "2 folders were not synced: another filex on this computer is syncing them", evs[0]["message"])

	plain := plainReporter(&bytes.Buffer{}, &bytes.Buffer{})
	e := errors.New("x")
	require.Same(t, e, plain.fatal(e), "without --json the error is main's to print")
}

// A revoked token over --json: the stream opens with hello (in the language
// asked for), ends with signed_out, exit status 3, and nothing on stderr.
func TestSyncRun_JSONStreamOfARevokedToken(t *testing.T) {
	dir, st := syncEnv(t)
	t.Setenv("FILEX_LANG", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err, out, errOut := runSync(t, ctx, "run", "--url", srv.URL, "--token", "revoked", "--watch", "20ms", "--json", "--lang", "tr")

	require.Error(t, err)
	require.NoError(t, ctx.Err())
	require.Equal(t, exitSignedOut, exitCode(err))
	require.True(t, alreadyReported(err), "main must not print it a second time")
	require.Empty(t, errOut, "with --json every report is on the stream")
	evs := streamEvents(t, out)
	require.NotEmpty(t, evs)
	require.Equal(t, "hello", evs[0]["event"])
	require.EqualValues(t, syncEventProtocol, param(evs[0], "protocol"))
	require.Equal(t, "tr", param(evs[0], "lang"))
	last := evs[len(evs)-1]
	require.Equal(t, "fatal", last["event"])
	require.Equal(t, "signed_out", last["code"])
	require.Equal(t, "Sunucu bu oturumu artık kabul etmiyor; eşitlemenin sürmesi için yeniden oturum açılmalı", last["message"])
}

// No --lang: the account's language on the server — here one only a language
// pack adds, fetched from the server; what the pack does not translate falls
// back to English.
func TestSyncRun_JSONSpeaksTheAccountsLanguagePack(t *testing.T) {
	dir, st := syncEnv(t)
	t.Setenv("FILEX_LANG", "")
	t.Cleanup(func() { srvtext.SetPacks(nil) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"user":{"locale":"eo"}}`))
		case "/api/public/ui-locales/eo":
			_, _ = w.Write([]byte(`{"code":"eo","strings":{"server.sync.hello":"filex-sinkronigilo {version}"}}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		}
	}))
	t.Cleanup(srv.Close)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err, out, _ := runSync(t, ctx, "run", "--url", srv.URL, "--token", "t", "--watch", "20ms", "--json")
	require.Equal(t, exitSignedOut, exitCode(err))
	evs := streamEvents(t, out)
	require.Equal(t, "eo", param(evs[0], "lang"))
	require.Equal(t, "filex-sinkronigilo "+version.Version, evs[0]["message"])
	last := evs[len(evs)-1]
	require.Equal(t, "signed_out", last["code"])
	require.Equal(t, srvtext.Text("en", "server.sync.signed_out", nil), last["message"], "a key the pack lacks is English")
}

// B17: the engine judges a window for the desktop app, which keeps no
// HH:MM parser of its own any more.
func TestSyncWindowCmd_JSON(t *testing.T) {
	syncEnv(t)
	restore := nowFunc
	t.Cleanup(func() { nowFunc = restore })
	nowFunc = func() time.Time { return at(12, 0) }

	ask := func(args ...string) windowAnswer {
		t.Helper()
		err, out, _ := runSync(t, context.Background(), append([]string{"window", "--json", "--lang", "tr", "--"}, args...)...)
		require.NoError(t, err, "a refused window still exits 0 with --json")
		var a windowAnswer
		require.NoError(t, json.Unmarshal([]byte(out), &a), out)
		return a
	}

	// The engine has always accepted a one-digit hour and spaces; the
	// desktop's own parser read this as "no window".
	a := ask(" 7:00 - 9:00 ")
	require.True(t, a.OK)
	require.Equal(t, "07:00-09:00", a.Window)
	require.False(t, a.Open, "12:00 is outside 07:00-09:00")
	require.Equal(t, at(7, 0).AddDate(0, 0, 1).Format(time.RFC3339), a.OpensAt)
	require.Equal(t, "07:00 ile 09:00 arasında eşitlenir", a.Message)

	nowFunc = func() time.Time { return at(23, 0) }
	a = ask("22:00-07:00")
	require.True(t, a.OK && a.Open && a.OverMidnight)
	require.Equal(t, at(7, 0).AddDate(0, 0, 1).Format(time.RFC3339), a.ClosesAt)

	a = ask("25:00-07:00")
	require.False(t, a.OK)
	require.Equal(t, "window.bad", a.Code)
	require.Equal(t, "", a.Window)
	require.True(t, strings.HasPrefix(a.Message, "25:00-07:00 bir eşitleme saati aralığı değil"), a.Message)

	a = ask("07:00-07:00")
	require.False(t, a.OK)
	require.Equal(t, "window.empty", a.Code)

	a = ask("")
	require.True(t, a.OK)
	require.Equal(t, "", a.Window)
	require.Equal(t, "window.any", a.Code)

	err, _, _ := runSync(t, context.Background(), "window", "25:00-07:00")
	require.Error(t, err, "without --json a refused window is an error, as for `sync run --window`")
}
