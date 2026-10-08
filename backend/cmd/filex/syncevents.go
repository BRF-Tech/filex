package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/version"
)

// ── `filex sync run --json`: the engine's event stream ───────────────────
//
// The desktop app supervises `filex sync run --watch`. Until 0.54 it read the
// English lines below (sync.go, synclive.go, synclock.go) with regular
// expressions (desktop/src/syncstatus.ts): a sentence reworded here silently
// broke the status under a folder, and the engine's error lines reached a
// Turkish window in English. With --json the engine writes one JSON object per
// line on stdout instead — ONE ordered stream, so an error can no longer
// arrive before or after the summary it belongs to depending on which pipe was
// read first:
//
//	{"event":"pass","pair":"pair-1","code":"pass.done","params":{…},"message":"…"}
//
//   - event   what kind of report it is (hello, progress, hold, pass, error,
//     note, lock, local, live, window, fatal);
//   - pair    the pair it is about, when it is about one;
//   - code    the exact report, stable across releases (syncEventCodes);
//   - params  its figures and facts, typed (numbers are numbers);
//   - message the sentence a person reads, said HERE in the --lang language
//     from the server catalogue (internal/srvtext `server.sync.*`) — a
//     language pack's when the language is one a pack on the server adds.
//
// A program shows `message` and acts on `event`, `code` and `params`; it
// never parses `message`. The plain lines for people in a terminal are
// unchanged and stay English.
//
// The language (syncLanguage): --lang, else $FILEX_LANG, else the account's
// own language on the server, else English. A language filex does not ship
// is fetched from the server's language packs (GET /api/public/ui-locales/…),
// once, when the run starts.

// syncEventProtocol is the version of the stream's shape, in the hello event.
// It changes only when a reader would misread the new stream.
const syncEventProtocol = 1

// syncEventCodes is every code the stream uses. Each has its sentence under
// `server.sync.<code>` in the server catalogue (syncevents_test.go holds the
// two together).
var syncEventCodes = []string{
	"hello",
	"progress.inventory", "progress.listing", "progress.listing_here", "progress.plan",
	"progress.transfer", "progress.transfer_bytes", "progress.transfer_eta", "progress.settling",
	"hold",
	"pass.in_step", "pass.done", "pass.done_failed", "pass.failed", "pass.action_failed",
	"pass.raced", "pass.skipped", "pass.trashed",
	"lock.busy", "lock.acquired", "lock.failed",
	"local.watched", "local.too_large", "local.unavailable",
	"live.connected", "live.polling", "live.offline", "live.off",
	"window.waiting", "window.closed", "window.outside",
	"window.any", "window.ok", "window.bad", "window.empty",
	"signed_out", "pairs_busy", "no_pairs", "pairs_unreadable", "fatal",
}

// syncEvent is one line of the stream.
type syncEvent struct {
	Event   string         `json:"event"`
	Pair    string         `json:"pair,omitempty"`
	Code    string         `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`

	// count, when set, is the number the sentence is about (its plural
	// form); vars are the words it takes. Neither is on the wire: params is.
	count *int
	vars  srvtext.Vars
}

// syncText is the sentence of one code in lang; a count is written with the
// language's digit grouping, like every other figure of the stream.
func syncText(lang, code string, count *int, vars srvtext.Vars) string {
	key := srvtext.Prefix + "sync." + code
	if count != nil {
		v := srvtext.Vars{"count": srvtext.Number(lang, int64(*count))}
		for k, x := range vars {
			v[k] = x
		}
		return srvtext.Plural(lang, key, *count, v)
	}
	return srvtext.Text(lang, key, vars)
}

// syncReporter is where a sync command's reports go: the plain lines a
// person reads (stdout and stderr, as before), or, with --json, the event
// stream (stdout only). Safe for concurrent use: the file-system watcher and
// the change stream report from their own goroutines.
type syncReporter struct {
	mu     sync.Mutex
	asJSON bool
	lang   string
	out    io.Writer
	errOut io.Writer
}

// plainReporter writes the plain lines to out and errOut.
func plainReporter(out, errOut io.Writer) *syncReporter {
	if errOut == nil {
		errOut = out
	}
	return &syncReporter{out: out, errOut: errOut, lang: "en"}
}

// emit writes one report: with --json the event (its message said in the
// reporter's language), else the plain line — on stderr when toErr. An
// empty plain line is a report only the stream carries.
func (r *syncReporter) emit(toErr bool, plain string, ev syncEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.asJSON {
		if plain == "" {
			return
		}
		w := r.out
		if toErr {
			w = r.errOut
		}
		fmt.Fprintln(w, plain)
		return
	}
	ev.Message = syncText(r.lang, ev.Code, ev.count, ev.vars)
	enc := json.NewEncoder(r.out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ev)
}

func itoa(n int) string { return strconv.Itoa(n) }

// n is a count in the reporter's language ("48,211", "48.211").
func (r *syncReporter) n(x int) string { return srvtext.Number(r.lang, int64(x)) }

// duration is a time left in words: "8 h 10 min", "12 min", "45 s" (the
// rounding of filesync's etaText: 59 min 30 s is an hour).
func (r *syncReporter) duration(d time.Duration) string {
	m := d.Round(time.Minute)
	switch {
	case m >= time.Hour:
		return srvtext.Text(r.lang, "server.sync.time.hours", srvtext.Vars{
			"hours": itoa(int(m / time.Hour)), "minutes": itoa(int((m % time.Hour) / time.Minute)),
		})
	case d >= time.Minute:
		return srvtext.Text(r.lang, "server.sync.time.minutes", srvtext.Vars{"minutes": itoa(int(m / time.Minute))})
	default:
		return srvtext.Text(r.lang, "server.sync.time.seconds", srvtext.Vars{"seconds": itoa(int(d / time.Second))})
	}
}

// hello opens the stream: the protocol, the engine's release and the
// language its messages are in. Stream only.
func (r *syncReporter) hello() {
	r.emit(false, "", syncEvent{
		Event:  "hello",
		Code:   "hello",
		Params: map[string]any{"protocol": syncEventProtocol, "version": version.Version, "lang": r.lang},
		vars:   srvtext.Vars{"version": version.Version},
	})
}

// progress is one of the engine's progress reports (filesync.ProgressEvent).
// Stream only: the plain line is the engine's own (Engine.Progress).
func (r *syncReporter) progress(pairID string, pe filesync.ProgressEvent) {
	params := map[string]any{"phase": pe.Phase}
	ev := syncEvent{Event: "progress", Pair: pairID}
	switch pe.Phase {
	case "inventory":
		params["here"] = pe.Here
		n := pe.Here
		ev.Code = "progress.inventory"
		if pe.Listing {
			params["listed"] = pe.Listed
			params["folders"] = pe.ListedFolders
			n = pe.Listed
			ev.Code = "progress.listing"
			if pe.Here > 0 {
				ev.Code = "progress.listing_here"
				ev.vars = srvtext.Vars{"here": r.n(pe.Here)}
			}
		}
		ev.count = &n
	case "plan":
		params["total"] = pe.Total
		n := pe.Total
		ev.Code, ev.count = "progress.plan", &n
	case "transfer":
		params["done"], params["total"] = pe.Done, pe.Total
		ev.Code = "progress.transfer"
		ev.vars = srvtext.Vars{"done": r.n(pe.Done), "total": r.n(pe.Total)}
		if pe.BytesTotal > 0 {
			params["bytes_done"], params["bytes_total"] = pe.BytesDone, pe.BytesTotal
			ev.vars["bytes"] = srvtext.Bytes(r.lang, pe.BytesDone)
			ev.vars["size"] = srvtext.Bytes(r.lang, pe.BytesTotal)
			ev.Code = "progress.transfer_bytes"
			if pe.HasETA {
				params["eta_seconds"] = int64(pe.ETA / time.Second)
				ev.vars["wait"] = r.duration(pe.ETA)
				ev.Code = "progress.transfer_eta"
			}
		}
	case "settling":
		params["done"], params["total"] = pe.Done, pe.Total
		ev.Code = "progress.settling"
		ev.vars = srvtext.Vars{"done": r.n(pe.Done), "total": r.n(pe.Total)}
	case "hold":
		n := pe.Held
		params["count"] = n
		ev.Event, ev.Code, ev.count = "hold", "hold", &n
	default:
		return
	}
	ev.Params = params
	r.emit(false, "", ev)
}

// result reports one pass: the plain summary of writeResult, or a "pass"
// event followed by one event per failed action, race, skip and trash move.
//
// ⚠ On the stream the summary comes FIRST and its errors after it, in one
// ordered stream: a reader clears a pair's error on a summary with no
// failures and sets it on each "error" event, and the order is now fixed.
func (r *syncReporter) result(p filesync.Pair, res filesync.Result) {
	if !r.asJSON {
		r.mu.Lock()
		writeResult(r.out, r.errOut, p, res)
		r.mu.Unlock()
		return
	}
	failed := len(res.Errors)
	params := map[string]any{
		"planned": res.Planned, "applied": res.Applied,
		"uploaded": res.Uploaded, "downloaded": res.Downloaded,
		"removed_here": res.DeletedLocal, "removed_server": res.DeletedRemot,
		"conflicts": res.Conflicts, "identical": res.Identical, "held": res.Held,
		"failed": failed, "duration_ms": res.Duration.Milliseconds(),
	}
	if res.Planned == 0 && failed == 0 && res.Held == 0 {
		r.emit(false, "", syncEvent{Event: "pass", Pair: p.ID, Code: "pass.in_step", Params: params})
	} else {
		n := res.Planned
		ev := syncEvent{Event: "pass", Pair: p.ID, Code: "pass.done", Params: params, count: &n,
			vars: srvtext.Vars{"done": r.n(res.Applied)}}
		if failed > 0 {
			ev.Code = "pass.done_failed"
			ev.vars["failed"] = r.n(failed)
		}
		r.emit(false, "", ev)
	}
	for _, e := range res.Errors {
		r.emit(true, "", syncEvent{Event: "error", Pair: p.ID, Code: "pass.action_failed",
			Params: map[string]any{"error": e}, vars: srvtext.Vars{"error": e}})
	}
	for _, rc := range res.Raced {
		r.emit(false, "", syncEvent{Event: "note", Pair: p.ID, Code: "pass.raced",
			Params: map[string]any{"detail": rc}, vars: srvtext.Vars{"name": rc}})
	}
	if n := len(res.Skipped); n > 0 {
		r.emit(false, "", syncEvent{Event: "note", Pair: p.ID, Code: "pass.skipped",
			Params: map[string]any{"count": n, "example": res.Skipped[0]}, count: &n,
			vars: srvtext.Vars{"name": res.Skipped[0]}})
	}
	if n := res.DeletedLocal; n > 0 {
		r.emit(false, "", syncEvent{Event: "note", Pair: p.ID, Code: "pass.trashed",
			Params: map[string]any{"count": n}, count: &n})
	}
}

// passFailed is a pass that could not run.
func (r *syncReporter) passFailed(p filesync.Pair, err error) {
	r.emit(true, fmt.Sprintf("%s: %v", p.ID, err), syncEvent{Event: "error", Pair: p.ID, Code: "pass.failed",
		Params: map[string]any{"error": err.Error()}, vars: srvtext.Vars{"error": err.Error()}})
}

// lockBusy is a pair another process on this computer is syncing; toErr for
// a one-shot run (its plain line has always been on stderr).
func (r *syncReporter) lockBusy(toErr bool, pairID string, err error) {
	r.emit(toErr, pairID+": "+lockBusyLine(err), syncEvent{Event: "lock", Pair: pairID, Code: "lock.busy",
		Params: map[string]any{"state": "busy", "detail": lockBusyDetail(err)}})
}

// lockAcquired: this process syncs the pair again.
func (r *syncReporter) lockAcquired(pairID string) {
	r.emit(false, pairID+": lock: acquired", syncEvent{Event: "lock", Pair: pairID, Code: "lock.acquired",
		Params: map[string]any{"state": "acquired"}})
}

// lockFailed: the lock file itself could not be had (not another process).
func (r *syncReporter) lockFailed(pairID, msg string) {
	r.emit(true, pairID+": "+msg, syncEvent{Event: "error", Pair: pairID, Code: "lock.failed",
		Params: map[string]any{"error": msg}, vars: srvtext.Vars{"error": msg}})
}

// local says whether changes made on this computer in one pair are watched
// (err == nil) or wait for the interval's check, and why.
func (r *syncReporter) local(pairID string, err error, interval time.Duration) {
	secs := int(interval / time.Second)
	switch {
	case err == nil:
		r.emit(false, pairID+": local: watched", syncEvent{Event: "local", Pair: pairID, Code: "local.watched",
			Params: map[string]any{"state": "watched"}})
	case errors.Is(err, errTooLargeToWatch):
		detail := strings.TrimPrefix(err.Error(), errTooLargeToWatch.Error()+": ")
		r.emit(false, pairID+": local: poll-only - too-large - "+detail, syncEvent{Event: "local", Pair: pairID,
			Code:   "local.too_large",
			Params: map[string]any{"state": "too-large", "detail": detail, "interval_seconds": secs},
			vars:   srvtext.Vars{"seconds": itoa(secs)}})
	default:
		r.emit(false, fmt.Sprintf("%s: local: poll-only - unavailable - %v", pairID, err), syncEvent{Event: "local",
			Pair: pairID, Code: "local.unavailable",
			Params: map[string]any{"state": "unavailable", "detail": err.Error(), "interval_seconds": secs},
			vars:   srvtext.Vars{"seconds": itoa(secs), "error": err.Error()}})
	}
}

// live is how SERVER changes reach the engine: the change stream's state.
func (r *syncReporter) live(st cliclient.StreamState, detail string, interval time.Duration) {
	secs := int(interval / time.Second)
	code := "live.offline"
	switch st {
	case cliclient.StreamLive:
		code = "live.connected"
	case cliclient.StreamPolling:
		code = "live.polling"
	}
	r.emit(false, fmt.Sprintf("live: %s - %s", st, detail), syncEvent{Event: "live", Code: code,
		Params: map[string]any{"state": string(st), "detail": detail, "interval_seconds": secs},
		vars:   srvtext.Vars{"seconds": itoa(secs)}})
}

// liveOff: --live=false, only the interval finds changes.
func (r *syncReporter) liveOff(interval time.Duration) {
	secs := int(interval / time.Second)
	r.emit(false, "live: polling - changes are found by the interval poll only", syncEvent{Event: "live",
		Code: "live.off", Params: map[string]any{"state": "polling", "interval_seconds": secs},
		vars: srvtext.Vars{"seconds": itoa(secs)}})
}

// window is a --window report: window.waiting (outside it, nothing runs),
// window.closed (a pass it cut short) or window.outside (a one-shot run that
// did nothing). opens_at is the window's next opening, so a reader knows
// when the wait is over without reading the window itself (B17).
func (r *syncReporter) window(code string, w *syncWindow, now time.Time) {
	var plain string
	switch code {
	case "window.waiting":
		plain = fmt.Sprintf("sync: waiting for the sync window %s", w)
	case "window.closed":
		plain = fmt.Sprintf("sync: the sync window %s closed; the rest continues when it opens", w)
	default:
		code = "window.outside"
		plain = fmt.Sprintf("sync: outside the sync window %s; nothing was done", w)
	}
	r.emit(false, plain, syncEvent{Event: "window", Code: code,
		Params: map[string]any{
			"window": w.String(), "start": clockText(w.start), "end": clockText(w.end),
			"opens_at": w.opensAfter(now).Format(time.RFC3339),
		},
		vars: srvtext.Vars{"window": w.String()}})
}

// reportedError is an error the command already reported on its event
// stream: main exits with its status and prints nothing more (a second,
// English line on stderr would only race the event that said it).
type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

// alreadyReported reports whether err was said on an event stream.
func alreadyReported(err error) bool {
	var re *reportedError
	return errors.As(err, &re)
}

// fatal reports the error a run stopped with as a "fatal" event and returns
// it marked as said (reportedError) — or, without --json, as it is.
func (r *syncReporter) fatal(err error) error {
	if err == nil || !r.asJSON || alreadyReported(err) {
		return err
	}
	ev := syncEvent{Event: "fatal", Code: "fatal", Params: map[string]any{"error": err.Error(), "exit": exitCode(err)},
		vars: srvtext.Vars{"error": err.Error()}}
	var pb *pairsBusyError
	var we *windowError
	var pr *pairsReadError
	switch {
	case errors.Is(err, errSignedOut):
		ev.Code, ev.vars = "signed_out", nil
	case errors.As(err, &pb):
		n := len(pb.ids)
		ev.Code, ev.count, ev.vars = "pairs_busy", &n, nil
		ev.Params["pairs"] = pb.ids
	case errors.Is(err, errNoPairs):
		ev.Code, ev.vars = "no_pairs", nil
	case errors.As(err, &we):
		ev.Code = "window.bad"
		if we.empty {
			ev.Code = "window.empty"
		}
		ev.Params["input"] = we.input
		ev.vars = srvtext.Vars{"input": we.input}
	case errors.As(err, &pr):
		ev.Code = "pairs_unreadable"
		ev.vars = srvtext.Vars{"error": pr.err.Error()}
	}
	r.emit(false, "", ev)
	return &reportedError{err: err}
}

// ── the language of the messages ─────────────────────────────────────────

// langWant is the language asked for: --lang, else $FILEX_LANG.
func langWant(flag string) string {
	if s := strings.TrimSpace(flag); s != "" {
		return s
	}
	return strings.TrimSpace(os.Getenv("FILEX_LANG"))
}

// syncLanguage is the language the event stream speaks: want (--lang or
// $FILEX_LANG), else the account's own language on the server; a language
// filex does not ship is fetched from the server's language packs; English
// when none of that serves. api may be nil (no server to ask).
//
// ⚠ It installs the fetched pack as the process's catalogue overlay
// (srvtext.SetPacks): a CLI run is one process with one language, and the
// overlay is what makes every later sentence of the run speak it.
func syncLanguage(ctx context.Context, want string, api *cliclient.Client) string {
	var cands []string
	if want != "" {
		cands = append(cands, want)
	} else if api != nil {
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if l, err := api.AccountLanguage(actx); err == nil && l != "" {
			cands = append(cands, l)
		}
		cancel()
	}
	for _, c := range cands {
		if r := srvtext.Resolve(c); r != "" {
			return r
		}
		if api == nil {
			continue
		}
		for _, code := range packCandidates(c) {
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			strs, err := api.UILocale(pctx, code)
			cancel()
			if err != nil || len(strs) == 0 {
				continue
			}
			srvtext.SetPacks(srvtext.StaticPacks{code: strs})
			if r := srvtext.Resolve(c); r != "" {
				return r
			}
		}
	}
	return srvtext.Pick()
}

// packCandidates are the pack languages that could serve tag: itself, then
// its primary subtag ("es-MX" → "es-mx", "es").
func packCandidates(tag string) []string {
	t := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tag)), "_", "-")
	if t == "" {
		return nil
	}
	out := []string{t}
	if i := strings.IndexByte(t, '-'); i > 0 {
		out = append(out, t[:i])
	}
	return out
}

// ── `filex sync window`: the window, judged by the engine (B17) ─────────

// windowAnswer is `filex sync window --json`: whether the engine accepts a
// window, its canonical form, and when it opens or closes next. The desktop
// app asks this instead of reading HH:MM itself — its own parser was stricter
// than the engine's ("7:00-9:00" and spaces), so a window the engine accepted
// read as "no window" on the screen.
type windowAnswer struct {
	OK bool `json:"ok"`
	// Window is the canonical HH:MM-HH:MM ("" = any time): what to store and
	// to pass to `sync run --window`.
	Window       string         `json:"window"`
	Start        string         `json:"start,omitempty"`
	End          string         `json:"end,omitempty"`
	OverMidnight bool           `json:"over_midnight,omitempty"`
	Open         bool           `json:"open"`
	OpensAt      string         `json:"opens_at,omitempty"`
	ClosesAt     string         `json:"closes_at,omitempty"`
	Code         string         `json:"code"`
	Params       map[string]any `json:"params,omitempty"`
	Message      string         `json:"message"`

	err error
}

// clockText is minutes after midnight as HH:MM.
func clockText(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

// judgeSyncWindow answers spec at now, its message in lang.
func judgeSyncWindow(spec string, now time.Time, lang string) windowAnswer {
	w, err := parseSyncWindow(spec)
	if err != nil {
		input := strings.TrimSpace(spec)
		code := "window.bad"
		var we *windowError
		if errors.As(err, &we) && we.empty {
			code = "window.empty"
		}
		return windowAnswer{Code: code, Params: map[string]any{"input": input},
			Message: syncText(lang, code, nil, srvtext.Vars{"input": input}), err: err}
	}
	if w == nil {
		return windowAnswer{OK: true, Open: true, Code: "window.any", Message: syncText(lang, "window.any", nil, nil)}
	}
	a := windowAnswer{
		OK: true, Window: w.String(), Start: clockText(w.start), End: clockText(w.end),
		OverMidnight: w.end < w.start, Open: w.contains(now), Code: "window.ok",
	}
	if a.Open {
		a.ClosesAt = w.closesAfter(now).Format(time.RFC3339)
	} else {
		a.OpensAt = w.opensAfter(now).Format(time.RFC3339)
	}
	a.Params = map[string]any{"start": a.Start, "end": a.End}
	a.Message = syncText(lang, "window.ok", nil, srvtext.Vars{"start": a.Start, "end": a.End})
	return a
}
