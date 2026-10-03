//go:build linux

package pam

// The same driver against real scripts through real exec — the parts a fake
// runner cannot show: what the process is actually given (argv, stdin,
// environment, session) and what a hung command costs.
//
// ⚠ These use scripts that PLAY pamtester; they were checked against the real
// pamtester's messages ("Password: pamtester: Authentication failure", exit 1,
// password read from a pipe when there is no terminal) but no real PAM is
// involved.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const fakePamtester = `#!/bin/sh
D="$(dirname "$0")"
printf '%s\n' "$@" > "$D/argv.last"
env > "$D/env.last"
awk '{print $6}' /proc/$$/stat > "$D/session.last"
echo $$ > "$D/pid.last"
IFS= read -r pw
printf '%s' "$pw" > "$D/stdin.last"
mode="$(cat "$D/mode" 2>/dev/null)"
case "$mode" in
  hang) exec sleep 30 ;;
  pwreq) echo "sudo: a password is required" >&2; exit 1 ;;
esac
if [ "$2" = alice ] && [ "$pw" = "s3cret-pw" ]; then
  echo "pamtester: successfully authenticated"
  exit 0
fi
printf 'Password: ' >&2
echo "pamtester: Authentication failure" >&2
exit 1
`

const fakeGetent = `#!/bin/sh
case "$2" in
  alice) echo "alice:x:1001:1001:Alice:/home/alice:/bin/bash" ;;
  *) exit 2 ;;
esac
`

const fakeID = `#!/bin/sh
echo "alice staff"
`

func execRig(t *testing.T, cfg map[string]any) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
		return p
	}
	pt := write("pamtester", fakePamtester)
	ge := write("getent", fakeGetent)
	id := write("id", fakeID)
	pamd := filepath.Join(dir, "pam.d")
	require.NoError(t, os.MkdirAll(pamd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pamd, "filex"), []byte("x\n"), 0o644))
	prev := pamDir
	pamDir = pamd
	t.Cleanup(func() { pamDir = prev })

	_, raw := dbtest.NewTestDB(t)
	d := New(identitystore.New(raw))
	d.getent, d.idTool = ge, id
	c := map[string]any{"pamtester_path": pt, "use_sudo": false, "auto_create": true}
	for k, v := range cfg {
		c[k] = v
	}
	require.NoError(t, d.Init(context.Background(), c))
	return d, dir
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err, name)
	return string(b)
}

func TestExec_PasswordOnlyOnStdin_CleanEnvironment_NoTerminal(t *testing.T) {
	t.Setenv("FILEX_SECRET_KEY", "topsecret-value")
	d, dir := execRig(t, nil)

	u, err := d.VerifyPassword(context.Background(), "alice", "s3cret-pw")
	require.NoError(t, err)
	assert.Equal(t, "alice@local", u.Email)

	assert.Equal(t, "s3cret-pw", readFile(t, dir, "stdin.last"), "the password arrives on stdin, one line")
	argv := readFile(t, dir, "argv.last")
	assert.Equal(t, "filex\nalice\nauthenticate\nacct_mgmt\n", argv)
	assert.NotContains(t, argv, "s3cret-pw")
	env := readFile(t, dir, "env.last")
	assert.NotContains(t, env, "s3cret-pw", "not in the environment either")
	assert.NotContains(t, env, "topsecret-value", "filex's own environment does not reach the command")
	assert.NotContains(t, env, "FILEX_")
	assert.Contains(t, env, "LC_ALL=C")

	// The command is a session leader with no controlling terminal, so it
	// reads the pipe and not an operator's terminal.
	pid := strings.TrimSpace(readFile(t, dir, "pid.last"))
	sess := strings.TrimSpace(readFile(t, dir, "session.last"))
	assert.Equal(t, pid, sess, "the command runs in a session of its own")
	_, perr := strconv.Atoi(pid)
	assert.NoError(t, perr)
}

func TestExec_RealMessagesAreClassified(t *testing.T) {
	d, dir := execRig(t, nil)
	ctx := context.Background()

	_, err := d.VerifyPassword(ctx, "alice", "wrong-password")
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "pamtester's real refusal is a no")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "mode"), []byte("pwreq"), 0o644))
	_, err = d.VerifyPassword(ctx, "alice", "s3cret-pw")
	assert.ErrorIs(t, err, auth.ErrUndecided)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
}

func TestExec_AHungCommandCostsTheTimeoutAndIsCouldNotDecide(t *testing.T) {
	d, dir := execRig(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mode"), []byte("hang"), 0o644))
	d.set.timeout = 400 * time.Millisecond

	start := time.Now()
	_, err := d.VerifyPassword(context.Background(), "alice", "s3cret-pw")
	took := time.Since(start)
	assert.ErrorIs(t, err, auth.ErrUndecided)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	assert.Less(t, took, 4*time.Second, "the attempt is cut off, not left waiting for 30 s")
}

func TestExec_ProbeAgainstScripts(t *testing.T) {
	d, dir := execRig(t, nil)
	cfg := map[string]any{"pamtester_path": filepath.Join(dir, "pamtester"), "use_sudo": false}
	cs := d.Probe(withAccount("alice", "s3cret-pw"), cfg, nil)
	assert.True(t, auth.ProbeOKAll(cs), "%+v", cs)
}
