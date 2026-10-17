package officeengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The runner (runner.go) with fake engine processes (fake_test.go): this
// test binary started again, speaking the engine's protocol without any
// WebAssembly. What is measured is filex's side: a deadline ends the
// process, a crash or a memory overrun is a failure and the next job starts
// another process, "preparing" until the prepare run is done, the error
// classes, the pools.
//
// RED before 0.57: there was no engine runner - the office engine was the
// Document Server alone.

func testPackage() *Package {
	return &Package{App: "office-editor", Version: "0.4.0", Engine: "engine.zip", EngineSHA256: "e1",
		UI: "ui.zip", UISHA256: "u1", EditorDir: "editor", HostVersion: "test"}
}

// testRunner is a runner whose processes are fake engines in mode.
func testRunner(t *testing.T, mode string, o Options) *Runner {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	o.Command = []string{exe}
	o.Env = append(o.Env, fakeEnv+"="+mode)
	if o.CacheDir == "" {
		o.CacheDir = t.TempDir()
	}
	if o.WorkDir == "" {
		o.WorkDir = t.TempDir()
	}
	r := New(o)
	t.Cleanup(r.Close)
	return r
}

// readyRunner is a runner whose package is prepared and ready.
func readyRunner(t *testing.T, o Options) *Runner {
	t.Helper()
	r := testRunner(t, "ok", o)
	r.SetPackage(testPackage())
	waitState(t, r, StateReady, 20*time.Second)
	return r
}

func waitState(t *testing.T, r *Runner, want State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if r.Status().State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the engine never became %s: %+v", want, r.Status())
}

// job converts an input file holding the fake's command; it answers the
// result's bytes.
func job(t *testing.T, ctx context.Context, r *Runner, command string, maxBytes int64) (string, error) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "in.docx")
	require.NoError(t, os.WriteFile(src, []byte(command), 0o600))
	dst := filepath.Join(dir, "in.pdf")
	err := r.Convert(ctx, Conversion{Src: src, From: "docx", To: "pdf", Dst: dst, MaxBytes: maxBytes, Delimiter: 2, CodePage: 65001})
	if err != nil {
		_, statErr := os.Stat(dst)
		assert.True(t, os.IsNotExist(statErr), "a failed job leaves nothing at Dst")
		return "", err
	}
	b, rerr := os.ReadFile(dst)
	require.NoError(t, rerr)
	return string(b), nil
}

func TestRunner_PreparesInTheBackgroundThenRuns(t *testing.T) {
	cache := t.TempDir()
	r := testRunner(t, "prepare-wait", Options{CacheDir: cache})
	r.SetPackage(testPackage())

	st := r.Status()
	assert.Equal(t, StatePreparing, st.State)
	assert.Equal(t, "office-editor", st.App)
	assert.False(t, r.Ready(), "not ready while it is being prepared")
	_, err := job(t, context.Background(), r, "pid", 0)
	assert.ErrorIs(t, err, ErrPreparing, "a job meanwhile says why it cannot run")

	require.NoError(t, os.WriteFile(filepath.Join(cache, "gate"), nil, 0o600))
	waitState(t, r, StateReady, 20*time.Second)
	assert.True(t, r.Ready())
	out, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, out)

	// Prepared once per machine: another runner on the same cache - a
	// restart - is ready at once (its prepare run would fail if it ran).
	again := testRunner(t, "prepare-fail", Options{CacheDir: cache})
	again.SetPackage(testPackage())
	assert.Equal(t, StateReady, again.Status().State, "the prepared marker is kept")

	// A new filex (and with it a new wazero) prepares again.
	next := testPackage()
	next.HostVersion = "next"
	again.SetPackage(next)
	waitState(t, again, StateFailed, 20*time.Second)
}

func TestRunner_AFailedPrepareSaysWhyAndRunsNothing(t *testing.T) {
	r := testRunner(t, "prepare-fail", Options{RetryPrepare: time.Hour})
	r.SetPackage(testPackage())
	waitState(t, r, StateFailed, 20*time.Second)
	assert.Contains(t, r.Status().Error, "does not compile", "the administrator reads why")
	assert.False(t, r.Ready())
	_, err := job(t, context.Background(), r, "pid", 0)
	assert.ErrorIs(t, err, ErrFailed)
}

func TestRunner_NoPackageIsNoEngine(t *testing.T) {
	r := testRunner(t, "ok", Options{})
	assert.Equal(t, StateAbsent, r.Status().State)
	_, err := job(t, context.Background(), r, "pid", 0)
	assert.ErrorIs(t, err, ErrNoEngine)
}

// The deadline is filex's: a module compiled without wazero's context watch
// cannot be stopped from inside, so the process is ended, and the next job
// starts another one.
func TestRunner_TheDeadlineEndsTheProcessAndTheNextJobStartsAnother(t *testing.T) {
	r := readyRunner(t, Options{})
	first, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = job(t, ctx, r, "sleep 1h", 0)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 10*time.Second, "the process was ended, not waited for")

	second, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "a new process took the next job")
}

func TestRunner_ACrashIsAFailureAndTheNextJobStartsAgain(t *testing.T) {
	r := readyRunner(t, Options{})
	_, err := job(t, context.Background(), r, "crash", 0)
	e, ok := AsError(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, CodeUnknown, e.Code, "a crash may pass: asked again later")
	assert.Equal(t, "crash", e.What)

	_, err = job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
}

func TestRunner_AProcessOverItsMemoryLimitIsEnded(t *testing.T) {
	if _, ok := processRSS(os.Getpid()); !ok {
		t.Skip("this system's process memory is not read here (the per-instance cap still holds)")
	}
	r := readyRunner(t, Options{ProcessLimit: func() int64 { return 128 << 20 }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := job(t, ctx, r, "grow 512", 0)
	e, ok := AsError(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, CodeTooLarge, e.Code, "too large, as the Document Server's own limit says it")
	assert.Equal(t, "memory", e.What)

	_, err = job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err, "the next job runs in a new process")
}

// x2t's return codes come back as the Document Server's conversion errors,
// so a caller says both back ends' failures with one table.
func TestRunner_FailuresComeBackAsTheDocumentServersCodes(t *testing.T) {
	r := readyRunner(t, Options{})
	for exit, want := range map[int]int{
		90: CodePassword, 91: CodePassword, 85: CodePassword,
		80: CodeCorrupt, 84: CodeCorrupt, 86: CodeCorrupt, 92: CodeCorrupt,
		82: CodeParams, 88: CodeParams,
		89: CodeDetect, 95: CodeDetect,
		93: CodeTooLarge, 94: CodeTooLarge, 96: CodeTooLarge,
		83: CodeTimeout,
	} {
		_, err := job(t, context.Background(), r, "x2t "+strconv.Itoa(exit), 0)
		e, ok := AsError(err)
		require.True(t, ok, "x2t %d: %v", exit, err)
		assert.Equal(t, want, e.Code, "x2t %d", exit)
		assert.Equal(t, "x2t-"+strconv.Itoa(exit), e.What)
	}
}

func TestRunner_AResultOverTheCeilingIsNotWritten(t *testing.T) {
	r := readyRunner(t, Options{})
	_, err := job(t, context.Background(), r, "big 4096", 1024)
	e, ok := AsError(err)
	require.True(t, ok, "%v", err)
	assert.True(t, e.Over, "the caller tells the result's ceiling from the input's")
}

// What a job asks reaches the engine as it was asked: the conversion API's
// output type and CSV knobs, the result's ceiling, the memory cap.
func TestRunner_TheRequestReachesTheEngine(t *testing.T) {
	r := readyRunner(t, Options{MaxMB: func() int { return 100 }})
	out, err := job(t, context.Background(), r, "request", 1<<20)
	require.NoError(t, err)
	var got struct {
		Request Request     `json:"request"`
		Config  ChildConfig `json:"config"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, OpConvert, got.Request.Op)
	assert.Equal(t, "docx", got.Request.From)
	assert.Equal(t, "pdf", got.Request.To)
	assert.Equal(t, 2, got.Request.Delimiter)
	assert.Equal(t, 65001, got.Request.CodePage)
	assert.Equal(t, int64(1<<20), got.Request.MaxBytes)
	assert.Equal(t, MaxMBMin, got.Config.MaxMB, "the cap is clamped to what x2t needs")
	assert.Equal(t, "editor", got.Config.EditorDir)
	assert.Equal(t, "e1", got.Config.EngineSHA256)
	assert.False(t, got.Config.Prepare)
}

// The engine process gets the few variables a Go program needs, never the
// server's own: its secrets stay with filex.
func TestRunner_TheEngineProcessGetsNoneOfTheServersSecrets(t *testing.T) {
	t.Setenv("FILEX_SECRET_KEY", "a-secret-the-engine-must-not-see")
	t.Setenv("FILEX_DB_DSN", "postgres://alice:secret@db.example.com/filex")
	r := readyRunner(t, Options{})
	out, err := job(t, context.Background(), r, "env", 0)
	require.NoError(t, err)
	assert.NotContains(t, out, "FILEX_SECRET_KEY")
	assert.NotContains(t, out, "FILEX_DB_DSN")
	assert.Contains(t, out, ConfigEnv+"=")
}

// One process per slot: with one slot two jobs take turns in the same
// process; with two they run side by side in two.
func TestRunner_OneProcessPerSlot(t *testing.T) {
	run2 := func(r *Runner) (time.Duration, []string) {
		var wg sync.WaitGroup
		pids := make([]string, 2)
		started := time.Now()
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out, err := job(t, context.Background(), r, "sleep 600ms", 0)
				assert.NoError(t, err)
				pids[i] = out
			}(i)
		}
		wg.Wait()
		return time.Since(started), pids
	}
	one := readyRunner(t, Options{Slots: func() int { return 1 }})
	took, pids := run2(one)
	assert.GreaterOrEqual(t, took, 1200*time.Millisecond, "the second waited for the slot")
	assert.Equal(t, pids[0], pids[1], "the same process took both")

	two := readyRunner(t, Options{Slots: func() int { return 2 }})
	_, pids = run2(two)
	assert.NotEqual(t, pids[0], pids[1], "two processes")
}

// Thumbnails have their own processes: a long conversion does not hold one
// up.
func TestRunner_AThumbnailDoesNotWaitForAConversion(t *testing.T) {
	r := readyRunner(t, Options{Slots: func() int { return 1 }})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = job(t, context.Background(), r, "sleep 2s", 0)
	}()
	time.Sleep(200 * time.Millisecond)
	src := filepath.Join(t.TempDir(), "page.xlsx")
	require.NoError(t, os.WriteFile(src, []byte("pid"), 0o600))
	started := time.Now()
	png, err := r.DrawThumbnail(context.Background(), Thumbnail{Src: src, From: "xlsx", Size: 320, Params: json.RawMessage(`{"spreadsheetLayout":{}}`)})
	require.NoError(t, err)
	assert.NotEmpty(t, png)
	assert.Less(t, time.Since(started), 1500*time.Millisecond, "the thumbnail did not wait for the conversion's slot")
	<-done
}

func TestRunner_AnIdleProcessEnds(t *testing.T) {
	r := readyRunner(t, Options{IdleTimeout: 300 * time.Millisecond})
	first, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	time.Sleep(1500 * time.Millisecond)
	second, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "the idle process had ended")
}

// Release (before an upgrade moves the app's files) ends the processes and
// forgets the package; giving it back finds it prepared.
func TestRunner_ReleaseEndsTheProcessesAndTheEngineComesBack(t *testing.T) {
	r := readyRunner(t, Options{})
	first, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	r.Release("another-app")
	assert.Equal(t, StateReady, r.Status().State, "another app's release changes nothing")

	r.Release("office-editor")
	assert.Equal(t, StateAbsent, r.Status().State)
	_, err = job(t, context.Background(), r, "pid", 0)
	assert.ErrorIs(t, err, ErrNoEngine)

	r.SetPackage(testPackage())
	assert.Equal(t, StateReady, r.Status().State, "prepared before: ready at once")
	second, err := job(t, context.Background(), r, "pid", 0)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

// A process that never says it is ready is ended at the start's limit.
func TestRunner_AProcessThatNeverStartsIsAFailure(t *testing.T) {
	cache := t.TempDir()
	prep := testRunner(t, "ok", Options{CacheDir: cache})
	prep.SetPackage(testPackage())
	waitState(t, prep, StateReady, 20*time.Second)

	r := testRunner(t, "no-hello", Options{CacheDir: cache, StartTimeout: 500 * time.Millisecond})
	r.SetPackage(testPackage())
	require.Equal(t, StateReady, r.Status().State)
	_, err := job(t, context.Background(), r, "pid", 0)
	e, ok := AsError(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, "start", e.What)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline"), "%v", err)
}

func TestClampMaxMB(t *testing.T) {
	for in, want := range map[int]int{0: 2048, -1: 2048, 100: 512, 512: 512, 2048: 2048, 3000: 3000, 4096: 4096, 9000: 4096} {
		assert.Equal(t, want, ClampMaxMB(in), "%d", in)
	}
}
