package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// syncEnv points every piece of state the sync commands touch at a temp dir,
// so a test never reads or writes the developer's own ~/.filex.
func syncEnv(t *testing.T) (dir string, st *filesync.Store) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("FILEX_SYNC_DIR", filepath.Join(dir, "state"))
	t.Setenv("FILEX_CLI_CONFIG", filepath.Join(dir, "cli.yaml"))
	t.Setenv("FILEX_UPLOAD_STATE", filepath.Join(dir, "uploads"))
	t.Setenv("FILEX_URL", "")
	t.Setenv("FILEX_TOKEN", "")
	return dir, &filesync.Store{Dir: filepath.Join(dir, "state")}
}

// runSync executes `filex sync <args…>` in-process and returns its error and
// what it printed.
func runSync(t *testing.T, ctx context.Context, args ...string) (error, string, string) {
	t.Helper()
	cmd := syncCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return err, out.String(), errOut.String()
}

// A token revoked on the server used to leave the watcher retrying every
// 30 s forever — and the desktop app, which only saw stderr lines, showed an
// error screen whose only button retried the same dead token. The watcher now
// stops, and says why with an exit status a supervisor can act on.
func TestSyncRun_ARevokedTokenStopsTheWatcherWithStatus3(t *testing.T) {
	dir, st := syncEnv(t)
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
	err, _, _ = runSync(t, ctx, "run", "--url", srv.URL, "--token", "revoked", "--watch", "20ms")

	require.Error(t, err, "a watcher must not keep going on a token the server rejects")
	require.NoError(t, ctx.Err(), "it must stop at once, not retry until the test gives up")
	var ee *exitError
	require.True(t, errors.As(err, &ee), "want an exit status, got %T: %v", err, err)
	require.Equal(t, exitSignedOut, ee.code)
	require.Equal(t, "signed out: the server no longer accepts this token (HTTP 401)", err.Error())
}

func TestExitCodeFollowsTheErrorChain(t *testing.T) {
	require.Equal(t, 0, exitCode(nil))
	require.Equal(t, 1, exitCode(errors.New("anything")))
	require.Equal(t, exitSignedOut, exitCode(&exitError{code: exitSignedOut, err: errSignedOut}))
	wrapped := fmt.Errorf("pair-1: %w", &exitError{code: exitSignedOut, err: errSignedOut})
	require.Equal(t, exitSignedOut, exitCode(wrapped))
}

// holdingPair stores a pair that a first run left holding one local file.
func holdingPair(t *testing.T) (st *filesync.Store, p filesync.Pair, file string) {
	t.Helper()
	dir, st := syncEnv(t)
	local := filepath.Join(dir, "mirror")
	file = filepath.Join(local, "stale.txt")
	require.NoError(t, os.MkdirAll(local, 0o755))
	require.NoError(t, os.WriteFile(file, []byte("cleaned up on the server"), 0o644))
	info, err := os.Stat(file)
	require.NoError(t, err)
	p = filesync.Pair{ID: "pair-1", Local: local, Remote: "docs://work", HoldNew: true, Held: 1}
	require.NoError(t, st.SavePairs([]filesync.Pair{p}))
	sig := (filesync.Node{Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}).Signature()
	require.NoError(t, os.MkdirAll(filepath.Join(st.Dir, "held"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(st.Dir, "held", "pair-1.json"), []byte(`{"stale.txt":"`+sig+`"}`), 0o600))
	return st, p, file
}

func TestSyncConfirm_EndsTheHold(t *testing.T) {
	st, _, file := holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "confirm", "pair-1")
	require.NoError(t, err)
	require.Equal(t, "pair-1: 1 held item(s) go to the server on the next run.\n", out)
	pairs, err := st.LoadPairs()
	require.NoError(t, err)
	require.False(t, pairs[0].HoldNew)
	require.Zero(t, pairs[0].Held)
	require.FileExists(t, file, "confirm touches no file")
}

func TestSyncDiscard_MovesTheHeldFilesToTheLocalTrash(t *testing.T) {
	st, _, file := holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "discard", "pair-1")
	require.NoError(t, err)
	require.Equal(t, "pair-1: moved 1 held file(s) to the local sync trash.\n", out)
	require.NoFileExists(t, file)
	items, err := st.ListTrash("pair-1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	pairs, _ := st.LoadPairs()
	require.False(t, pairs[0].HoldNew)
}

func TestSyncList_JSONCarriesTheHold(t *testing.T) {
	holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "list", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"hold_new": true`)
	require.Contains(t, out, `"held": 1`)
}
