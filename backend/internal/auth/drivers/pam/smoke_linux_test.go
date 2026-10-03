//go:build linux

package pam

// The real PAM, on a real Linux machine: real sudo, real pamtester, a real
// /etc/pam.d/filex and a real account. It runs only when the operator names a
// THROWAWAY account:
//
//	FILEX_PAM_SMOKE_USER, FILEX_PAM_SMOKE_PASSWORD
//	    the account (a throwaway: never root, never a person's own)
//	FILEX_PAM_SMOKE_SETUP
//	    the state of the machine's setup, docs/OS-LOGIN.md "Set it up":
//	    complete (default)  steps 1-3 done: everything below runs
//	    no_pamtester        step 1 not done
//	    no_pam_service      step 2 not done
//	    no_sudoers          step 3 not done
//	    no_nopasswd         step 3 done without NOPASSWD
//	    permit_all          /etc/pam.d/filex is `pam_permit` alone (accepts
//	                        anybody)
//	    The provider test must stop at that step, for that reason, and the
//	    running driver must refuse to start.
//	FILEX_PAM_SMOKE_GROUPS  optional: the account's groups, comma-separated,
//	                        as `id -Gn` must read them
//	FILEX_PAM_SMOKE_SUDO    optional: sudo_path (e.g. /usr/bin/sudo.ws where
//	                        /usr/bin/sudo is sudo-rs)
//
// Build it elsewhere, copy the binary, and run it AS the account filex runs as
// (the one the sudoers line names):
//
//	GOOS=linux CGO_ENABLED=0 go test -c -o pam.test ./internal/auth/drivers/pam
//	sudo -u filex --preserve-env=FILEX_PAM_SMOKE_USER,FILEX_PAM_SMOKE_PASSWORD \
//	    ./pam.test -test.run PAMSmoke -test.v
//
// ⚠ The password is never printed: every test checks, without echoing it, that
// it is in no argv, no probe answer and no log line.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func smokeAccount(t *testing.T) (user, pw string) {
	t.Helper()
	user, pw = os.Getenv("FILEX_PAM_SMOKE_USER"), os.Getenv("FILEX_PAM_SMOKE_PASSWORD")
	if user == "" || pw == "" {
		t.Skip("FILEX_PAM_SMOKE_USER / FILEX_PAM_SMOKE_PASSWORD not set")
	}
	require.False(t, identity.ForbiddenAccount(user), "the smoke account must be a throwaway, not %s", user)
	return user, pw
}

func smokeSetup() string {
	if s := os.Getenv("FILEX_PAM_SMOKE_SETUP"); s != "" {
		return s
	}
	return "complete"
}

func needComplete(t *testing.T) {
	t.Helper()
	if s := smokeSetup(); s != "complete" {
		t.Skipf("FILEX_PAM_SMOKE_SETUP=%s: the setup is not complete", s)
	}
}

func smokeSudo() string {
	if s := os.Getenv("FILEX_PAM_SMOKE_SUDO"); s != "" {
		return s
	}
	return DefaultSudo
}

func smokeCfg(extra map[string]any) map[string]any {
	c := map[string]any{}
	if s := os.Getenv("FILEX_PAM_SMOKE_SUDO"); s != "" {
		c["sudo_path"] = s
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func smokeStore(t *testing.T) db.Store {
	t.Helper()
	_, raw := dbtest.NewTestDB(t)
	return identitystore.New(raw)
}

// recRunner is the real runner, recording every argv (never stdin).
type recRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recRunner) Run(ctx context.Context, argv []string, stdin string) (result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), argv...))
	r.mu.Unlock()
	return execRunner{}.Run(ctx, argv, stdin)
}

// pamUsers lists the logins pamtester was really run for (sudo -l only asks
// sudo, it runs nothing).
func (r *recRunner) pamUsers(pamtester string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if slices.Contains(c, "-l") {
			continue
		}
		for i, a := range c {
			if a == pamtester && i+2 < len(c) {
				out = append(out, c[i+2])
			}
		}
	}
	return out
}

func smokeDriver(t *testing.T, cfg map[string]any) (*Driver, *recRunner, db.Store) {
	t.Helper()
	store := smokeStore(t)
	rec := &recRunner{}
	d := New(store)
	d.run = rec
	require.NoError(t, d.Init(context.Background(), smokeCfg(cfg)), "the setup of docs/OS-LOGIN.md is complete, so the driver starts")
	return d, rec, store
}

// noPassword fails, WITHOUT printing the password, when it is anywhere the
// test can see: every argv the driver ran, and the texts given.
func noPassword(t *testing.T, rec *recRunner, pw string, texts ...string) {
	t.Helper()
	if rec != nil {
		rec.mu.Lock()
		for i, c := range rec.calls {
			for j, a := range c {
				if strings.Contains(a, pw) {
					t.Errorf("the password is in the argv of command %d (element %d)", i, j)
				}
			}
		}
		rec.mu.Unlock()
	}
	for i, s := range texts {
		if strings.Contains(s, pw) {
			t.Errorf("the password is in text %d", i)
		}
	}
}

// logChecks prints a provider test step by step, once it is known not to hold
// the password.
func logChecks(t *testing.T, pw string, cs []auth.ProbeCheck) {
	t.Helper()
	for _, c := range cs {
		for k, v := range c.Params {
			if strings.Contains(v, pw) {
				t.Errorf("the password is in the provider test answer (%s.%s)", c.ID, k)
				return
			}
		}
	}
	for _, c := range cs {
		t.Logf("  %-18s %-4s %v", c.ID, c.Status, c.Params)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureLog sends every log line (debug included) to a buffer for the test.
func captureLog(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func printLog(t *testing.T, pw string, logs *syncBuf) {
	t.Helper()
	s := logs.String()
	if strings.Contains(s, pw) {
		t.Error("the password is in the log")
		return
	}
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "pam:") {
			lines = append(lines, l)
		}
	}
	t.Logf("the provider's log lines:\n%s", strings.Join(lines, "\n"))
}

// sysAccounts names one account of this machine with a UID below 1000 that is
// NOT on the name list: only the operating system can tell it is a service
// account (getent). Empty when the machine has none.
func sysAccounts(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("getent", "passwd").Output()
	if err != nil {
		t.Logf("getent passwd: %v", err)
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil || uid == 0 || uid >= 1000 {
			continue
		}
		n := f[0]
		if identity.ForbiddenAccount(n) || identity.Check(n) != nil || !userRe.MatchString(n) {
			continue
		}
		t.Logf("service account not on the name list: %s (uid %d, shell %s)", n, uid, f[6])
		return []string{n}
	}
	t.Log("no service account outside the name list on this machine")
	return nil
}

// The provider test, against the machine as it is: every step when the setup is
// complete; the step that is missing, with its reason, when it is not.
func TestPAMSmoke_ProviderTest(t *testing.T) {
	user, pw := smokeAccount(t)
	rec := &recRunner{}
	ta := &auth.TestAccount{Username: user, Password: pw}
	start := time.Now()
	cs := (&Driver{run: rec}).Probe(auth.WithTestAccount(context.Background(), ta), smokeCfg(nil), nil)
	t.Logf("setup %q, sudo %s: the provider test took %s", smokeSetup(), smokeSudo(), time.Since(start).Round(time.Millisecond))
	logChecks(t, pw, cs)
	noPassword(t, rec, pw)

	type stop struct {
		step    string
		reasons []string
	}
	// With no rule at all classic sudo says "a password is required", sudo-rs
	// (the default sudo from Ubuntu 25.10 on) that the user "may not run sudo".
	// Both reasons carry the same fix: the sudoers line. A rule without
	// NOPASSWD is "a password is required" / "interactive authentication is
	// required": password_required on both.
	want := map[string]stop{
		"no_pamtester":   {"pamtester", []string{reasonMissing}},
		"no_pam_service": {"pam_service", []string{reasonMissing}},
		"no_sudoers":     {"sudo", []string{reasonPasswordRequired, reasonNotAllowed}},
		"no_nopasswd":    {"sudo", []string{reasonPasswordRequired}},
		"permit_all":     {"unknown_user", []string{"accepts_anyone"}},
	}
	s := smokeSetup()
	if s == "complete" {
		require.True(t, auth.ProbeOKAll(cs), "every step passes on a complete setup")
		var ids []string
		for _, c := range cs {
			ids = append(ids, c.ID)
		}
		assert.Equal(t, []string{"config", "first_login_closed", "pamtester", "pam_service", "sudo", "unknown_user", "test_account"}, ids)
		assert.Equal(t, user, byID(cs)["test_account"].Params["account"])
		assert.Equal(t, user+"@local", ta.Email)
		return
	}
	w, ok := want[s]
	require.True(t, ok, "unknown FILEX_PAM_SMOKE_SETUP %q", s)
	last := lastFail(t, cs)
	assert.Equal(t, w.step, last.ID, "the test stops at the step that is not done")
	assert.Contains(t, w.reasons, last.Params["reason"])
	if w.step == "sudo" {
		assert.Contains(t, last.Params["hint"], "NOPASSWD: "+DefaultPamtester+" "+DefaultService+" *", "the fix is the sudoers line")
	}

	// The running driver refuses to start, and says which step in the audit.
	store := smokeStore(t)
	d := New(store)
	d.run = rec
	err := d.Init(context.Background(), smokeCfg(nil))
	require.Error(t, err, "a driver whose setup is incomplete does not start")
	t.Logf("Init: %v", err)
	assert.Contains(t, err.Error(), `"`+w.step+`"`)
	rows, lerr := store.ListAuditRecent(context.Background(), 10)
	require.NoError(t, lerr)
	var steps []string
	for _, r := range rows {
		if r.Action == AuditProviderUnavailable {
			steps = append(steps, fmt.Sprint(r.Metadata["step"]))
		}
	}
	assert.Equal(t, []string{w.step}, steps)
	noPassword(t, rec, pw, err.Error())
}

// The provider test's last step on the real machine: a wrong password is a
// wrong password; root, daemon and a service account are refused before PAM is
// ever asked about them.
func TestPAMSmoke_ProviderTestRefuses(t *testing.T) {
	user, pw := smokeAccount(t)
	needComplete(t)
	rec := &recRunner{}
	probe := func(u, p string) auth.ProbeCheck {
		t.Helper()
		cs := (&Driver{run: rec}).Probe(withAccount(u, p), smokeCfg(nil), nil)
		logChecks(t, pw, cs)
		return lastFail(t, cs)
	}

	last := probe(user, pw+"-nope")
	assert.Equal(t, "test_account", last.ID)
	assert.Equal(t, "wrong_password", last.Params["reason"])

	forbidden := append([]string{"root", "daemon"}, sysAccounts(t)...)
	for _, name := range forbidden {
		last = probe(name, randHex(12))
		assert.Equal(t, "test_account", last.ID, name)
		assert.Equal(t, identity.RefuseForbidden, last.Params["reason"], name)
	}
	asked := rec.pamUsers(DefaultPamtester)
	for _, name := range forbidden {
		assert.NotContains(t, asked, name, "PAM is never asked about a system account")
	}
	noPassword(t, rec, pw)
}

// The whole driver on the real machine: the right password signs in (by name
// and by address), a wrong one and an unknown login are the ordinary "no" — not
// "could not decide" — and the system accounts never reach PAM.
func TestPAMSmoke_SignIn(t *testing.T) {
	user, pw := smokeAccount(t)
	needComplete(t)
	logs := captureLog(t)
	ctx := context.Background()
	d, rec, store := smokeDriver(t, map[string]any{"auto_create": true})

	start := time.Now()
	u, tok, err := d.Login(ctx, user, pw)
	require.NoError(t, err, "the right password signs in")
	t.Logf("right password: signed in after %s", time.Since(start).Round(time.Millisecond))
	assert.NotEmpty(t, tok)
	assert.Equal(t, user+"@local", u.Email)

	u2, err := d.VerifyPassword(ctx, user+"@local", pw)
	require.NoError(t, err, "the address form reaches the same account")
	assert.Equal(t, u.ID, u2.ID)

	start = time.Now()
	_, _, err = d.Login(ctx, user, pw+"-nope")
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "a wrong password is a no")
	assert.NotErrorIs(t, err, auth.ErrUndecided)
	t.Logf("wrong password: %v after %s", err, time.Since(start).Round(time.Millisecond))

	ghost := "filexnosuch" + randHex(3)
	start = time.Now()
	_, _, err = d.Login(ctx, ghost, pw)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "a login the machine does not know is a no")
	assert.NotErrorIs(t, err, auth.ErrUndecided)
	t.Logf("unknown login: %v after %s", err, time.Since(start).Round(time.Millisecond))
	_, gerr := store.GetUserByEmail(ctx, ghost+"@local")
	assert.Error(t, gerr, "no account for a login the machine does not know")

	forbidden := append([]string{"root", "daemon"}, sysAccounts(t)...)
	for _, name := range forbidden {
		_, _, err = d.Login(ctx, name, randHex(12))
		assert.ErrorIs(t, err, auth.ErrUnauthorized, name)
		assert.NotErrorIs(t, err, auth.ErrUndecided, name)
	}
	asked := rec.pamUsers(d.set.pamtester)
	t.Logf("pamtester was run for: %v", asked)
	for _, name := range forbidden {
		assert.NotContains(t, asked, name, "PAM is never asked about a system account")
	}
	var wantRefusals []string
	for range forbidden {
		wantRefusals = append(wantRefusals, identity.RefuseForbidden)
	}
	assert.Equal(t, wantRefusals, refusals(t, store), "each system account is refused, with the reason in the audit")

	noPassword(t, rec, pw)
	printLog(t, pw, logs)
}

// `id -Gn` on the real machine: the groups are read right, recorded at the
// sign-in, and allowed_groups is the door.
func TestPAMSmoke_Groups(t *testing.T) {
	user, pw := smokeAccount(t)
	needComplete(t)
	ctx := context.Background()
	d, rec, store := smokeDriver(t, map[string]any{"auto_create": true})

	groups, err := d.groupsOf(ctx, user)
	require.NoError(t, err)
	t.Logf("id -Gn %s: %v", user, groups)
	require.NotEmpty(t, groups)
	if want := os.Getenv("FILEX_PAM_SMOKE_GROUPS"); want != "" {
		assert.ElementsMatch(t, strings.Split(want, ","), groups)
	}

	u, err := d.VerifyPassword(ctx, user, pw)
	require.NoError(t, err)
	recorded, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, groups, recorded, "the sign-in records the machine's groups")

	member := groups[len(groups)-1]
	d2, rec2, _ := smokeDriver(t, map[string]any{"auto_create": true, "allowed_groups": strings.ToUpper(member)})
	_, err = d2.VerifyPassword(ctx, user, pw)
	assert.NoError(t, err, "a member of an allowed group (%s) gets an account", member)

	d3, rec3, store3 := smokeDriver(t, map[string]any{"auto_create": true, "allowed_groups": "filex-smoke-no-such-group"})
	_, err = d3.VerifyPassword(ctx, user, pw)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "outside every allowed group: no account")
	assert.Equal(t, []string{auth.ReasonGroupNotAllowed}, refusals(t, store3))

	for _, r := range []*recRunner{rec, rec2, rec3} {
		noPassword(t, r, pw)
	}
}

// auto_create is off unless switched on: the right password of an account filex
// has never seen is refused, and the audit says why.
func TestPAMSmoke_FirstSignInIsClosedByDefault(t *testing.T) {
	user, pw := smokeAccount(t)
	needComplete(t)
	d, rec, store := smokeDriver(t, nil)
	_, err := d.VerifyPassword(context.Background(), user, pw)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, []string{auth.ReasonAutoCreateOff}, refusals(t, store))
	noPassword(t, rec, pw)
}

// While PAM holds a failed attempt (its deliberate delay), a second one over
// max_concurrent is "busy", not a wrong password.
func TestPAMSmoke_BusyIsNotAWrongPassword(t *testing.T) {
	user, pw := smokeAccount(t)
	needComplete(t)
	ctx := context.Background()
	d, rec, _ := smokeDriver(t, map[string]any{"auto_create": true, "max_concurrent": "1"})

	first := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := d.VerifyPassword(ctx, user, pw+"-nope")
		first <- err
	}()
	time.Sleep(500 * time.Millisecond)
	_, err := d.VerifyPassword(ctx, user, pw)
	assert.ErrorIs(t, err, auth.ErrBusy)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	ferr := <-first
	assert.ErrorIs(t, ferr, auth.ErrUnauthorized)
	t.Logf("the failed attempt held its slot for %s", time.Since(start).Round(time.Millisecond))

	_, err = d.VerifyPassword(ctx, user, pw)
	assert.NoError(t, err, "free again once the first one is over")
	noPassword(t, rec, pw)
}
