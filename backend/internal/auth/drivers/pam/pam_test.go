//go:build linux

package pam

// The PAM sign-in, driven by a fake machine: a runner that plays sudo,
// pamtester, getent and id. What is tested is what filex does with what those
// tools say — the argv it builds, where the password goes, which answers stay
// apart (no / could not decide / busy), which accounts are never let in.
// pamtester_exec_test.go runs the same driver against real scripts through
// real exec, for the parts a fake cannot show (stdin, environment, timeouts).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const (
	realPW = "s3cret-pw"
	tokPW  = "another-pw"
)

// world is the fake machine.
type world struct {
	mu    sync.Mutex
	calls [][]string
	pw    map[string]string // login → password PAM accepts
	pass  map[string]string // getent passwd lines
	group map[string]string // id -Gn output

	// sudoMode: "" (allowed), "pwreq", "tty", "denied", "raw" (sudoErr).
	sudoMode string
	// sudoErr is what sudo writes to stderr in sudoMode "raw" (exit 1): the
	// text a real sudo wrote, verbatim.
	sudoErr string
	// pamMode: "" (normal), "sysdown", "permit" (a stack that accepts anybody), "hang".
	pamMode string
	// installed: pamtester/sudo exist (checked by stat, so the tests make files).
	stdins []string
	gate   chan struct{} // when set, pamtester blocks until it is closed
	inside chan struct{} // receives one value when a pamtester call is inside the gate
}

func newWorld() *world {
	return &world{
		pw: map[string]string{"alice": realPW, "carol": tokPW, "svcacct": "svc-pw", "nologin1": "nl-pw", "root": "root-pw", "daemonx": "d-pw"},
		pass: map[string]string{
			"alice":    "alice:x:1001:1001:Alice:/home/alice:/bin/bash",
			"carol":    "carol:x:1002:1002:Carol:/home/carol:/bin/zsh",
			"svcacct":  "svcacct:x:500:500::/var/lib/svc:/bin/bash",
			"nologin1": "nologin1:x:1500:1500::/home/n:/usr/sbin/nologin",
			"root":     "root:x:0:0:root:/root:/bin/bash",
			"daemonx":  "daemonx:x:1:1::/:/usr/sbin/nologin",
		},
		group: map[string]string{"alice": "alice staff docs", "carol": "carol guests", "svcacct": "svcacct", "nologin1": "nologin1", "root": "root"},
	}
}

func (w *world) run(argv []string, stdin string) (int, string, string, error) {
	w.mu.Lock()
	w.calls = append(w.calls, append([]string(nil), argv...))
	w.stdins = append(w.stdins, stdin)
	gate, inside := w.gate, w.inside
	w.mu.Unlock()
	switch filepath.Base(argv[0]) {
	case "getent":
		if l, ok := w.pass[argv[2]]; ok {
			return 0, l + "\n", "", nil
		}
		return 2, "", "", nil
	case "id":
		if g, ok := w.group[argv[3]]; ok {
			return 0, g + "\n", "", nil
		}
		return 1, "", "id: no such user\n", nil
	case "sudo":
		if argv[1] != "-n" {
			return 1, "", "sudo: unexpected\n", nil
		}
		switch w.sudoMode {
		case "pwreq":
			return 1, "", "sudo: a password is required\n", nil
		case "tty":
			return 1, "", "sudo: you must have a tty to run sudo\n", nil
		case "denied":
			return 1, "", "Sorry, user filex is not allowed to execute '" + strings.Join(argv[3:], " ") + "' as root on host.\n", nil
		case "raw":
			return 1, "", w.sudoErr, nil
		}
		if argv[2] == "-l" {
			return 0, argv[3] + "\n", "", nil
		}
		return w.run(argv[2:], stdin)
	case "pamtester":
		if gate != nil {
			if inside != nil {
				inside <- struct{}{}
			}
			<-gate
		}
		switch w.pamMode {
		case "sysdown":
			return 1, "", "Password: pamtester: System error\n", nil
		case "permit":
			return 0, "", "", nil
		case "hang":
			time.Sleep(5 * time.Second)
		}
		user, pw := argv[2], strings.TrimSuffix(stdin, "\n")
		if want, ok := w.pw[user]; ok && want == pw {
			return 0, "pamtester: successfully authenticated\n", "", nil
		}
		return 1, "", "Password: pamtester: Authentication failure\n", nil
	}
	return 127, "", "not found\n", nil
}

// pamtesterCalls are the calls that reached pamtester (not sudo -l, getent, id).
func (w *world) pamtesterCalls() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out [][]string
	for _, c := range w.calls {
		if len(c) == 0 {
			continue
		}
		if filepath.Base(c[0]) == "pamtester" {
			out = append(out, c)
		}
	}
	return out
}

// rig is a driver over the fake machine and a real test database.
type rig struct {
	d     *Driver
	store db.Store
	w     *world
	dir   string
}

func newRig(t *testing.T, cfg map[string]any, mutate ...func(*world)) *rig {
	t.Helper()
	dir := t.TempDir()
	pamd := filepath.Join(dir, "pam.d")
	require.NoError(t, os.MkdirAll(pamd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pamd, "filex"), []byte("auth required pam_unix.so\n"), 0o644))
	pt := filepath.Join(dir, "pamtester")
	require.NoError(t, os.WriteFile(pt, []byte("#!/bin/sh\n"), 0o755))
	sd := filepath.Join(dir, "sudo")
	require.NoError(t, os.WriteFile(sd, []byte("#!/bin/sh\n"), 0o755))

	w := newWorld()
	for _, m := range mutate {
		m(w)
	}
	restore := SwapForTest(w.run, pamd)
	t.Cleanup(restore)

	_, raw := dbtest.NewTestDB(t)
	store := identitystore.New(raw)
	c := map[string]any{"pamtester_path": pt, "sudo_path": sd}
	for k, v := range cfg {
		c[k] = v
	}
	d := New(store)
	require.NoError(t, d.Init(context.Background(), c))
	// What start-up ran is not what the tests are about.
	w.mu.Lock()
	w.calls, w.stdins = nil, nil
	w.mu.Unlock()
	return &rig{d: d, store: store, w: w, dir: dir}
}

func refusals(t *testing.T, store db.Store) []string {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 100)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			out = append(out, r.Metadata["reason"].(string))
		}
	}
	return out
}

func addUser(t *testing.T, store db.Store, email string) *model.User {
	t.Helper()
	u, err := store.CreateUser(context.Background(), email, "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	return u
}

// ── the argument vector ─────────────────────────────────────────────────────

func TestAuthArgv_FixedShape(t *testing.T) {
	s := defaultSettings()
	got, err := s.authArgv("alice")
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/bin/sudo", "-n", "/usr/bin/pamtester", "filex", "alice", "authenticate", "acct_mgmt"}, got)

	s.useSudo = false
	s.service = "filex-2fa"
	got, err = s.authArgv("alice")
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/bin/pamtester", "filex-2fa", "alice", "authenticate", "acct_mgmt"}, got)
}

func TestAuthArgv_NameCannotBeAnything(t *testing.T) {
	s := defaultSettings()
	for _, bad := range []string{
		"", "-alice", "--help", "-o", "al ice", "alice;id", "alice$(id)", "alice`id`", "alice\nroot",
		"alice|x", "../alice", "Alice", "9alice", ".alice", "_alice", "al\x00ice", "alice&", "alice'", "alice\"", "ali*", "alice@local",
	} {
		_, err := s.authArgv(bad)
		assert.Error(t, err, "%q must never reach a command line", bad)
	}
	for _, good := range []string{"alice", "a.b-c_d", "alex99"} {
		_, err := s.authArgv(good)
		assert.NoError(t, err, good)
	}
}

func TestParseSettings_RefusesAnythingButPlainPaths(t *testing.T) {
	for k, v := range map[string]string{
		"pamtester_path": "pamtester", // relative
	} {
		_, err := parseSettings(map[string]any{k: v})
		assert.Error(t, err, k+"="+v)
	}
	for _, v := range []string{"/usr/bin/pam tester", "/usr/bin/pamtester;id", "/usr/bin/$(id)", "/usr/bin/../bin/x", "/usr/bin/pam'tester", "/usr/bin/*", "/usr/bin/p\nx", "-x"} {
		_, err := parseSettings(map[string]any{"pamtester_path": v})
		assert.Error(t, err, "pamtester_path %q", v)
		_, err = parseSettings(map[string]any{"sudo_path": v})
		assert.Error(t, err, "sudo_path %q", v)
	}
	for _, v := range []string{"-filex", "a/b", "a b", "a;b", "$x", "", "x" + strings.Repeat("y", 70)} {
		if v == "" {
			continue
		}
		_, err := parseSettings(map[string]any{"service": v})
		assert.Error(t, err, "service %q", v)
	}
	for k, v := range map[string]string{"timeout_seconds": "1", "max_concurrent": "0"} {
		_, err := parseSettings(map[string]any{k: v})
		assert.Error(t, err, k)
	}
	s, err := parseSettings(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, defaultSettings(), s)
	// use_sudo off drops sudo_path from the checks.
	_, err = parseSettings(map[string]any{"use_sudo": false, "sudo_path": "relative"})
	assert.NoError(t, err)
}

// ── the three answers ───────────────────────────────────────────────────────

func TestClassify(t *testing.T) {
	type c struct {
		name string
		res  result
		err  error
		v    verdict
		why  string
	}
	for _, tc := range []c{
		{"yes", result{ExitCode: 0}, nil, verdictOK, ""},
		{"real wrong password", result{ExitCode: 1, Stderr: "Password: pamtester: Authentication failure\n"}, nil, verdictDenied, ""},
		{"user unknown", result{ExitCode: 1, Stderr: "pamtester: User not known to the underlying authentication module\n"}, nil, verdictDenied, ""},
		{"expired", result{ExitCode: 1, Stderr: "pamtester: User account has expired\n"}, nil, verdictDenied, ""},
		{"permission denied", result{ExitCode: 1, Stderr: "pamtester: Permission denied\n"}, nil, verdictDenied, ""},
		{"password required", result{ExitCode: 1, Stderr: "sudo: a password is required\n"}, nil, verdictUndecided, reasonPasswordRequired},
		{"terminal required", result{ExitCode: 1, Stderr: "sudo: a terminal is required to read the password\n"}, nil, verdictUndecided, reasonPasswordRequired},
		{"requiretty", result{ExitCode: 1, Stderr: "sudo: you must have a tty to run sudo\n"}, nil, verdictUndecided, reasonRequireTTY},
		{"not allowed", result{ExitCode: 1, Stderr: "Sorry, user filex is not allowed to execute '/usr/bin/pamtester' as root\n"}, nil, verdictUndecided, reasonNotAllowed},
		{"sudo command not found", result{ExitCode: 1, Stderr: "sudo: /usr/bin/pamtester: command not found\n"}, nil, verdictUndecided, reasonMissing},
		{"pam system error", result{ExitCode: 1, Stderr: "Password: pamtester: System error\n"}, nil, verdictUndecided, reasonPAM},
		{"sssd down", result{ExitCode: 1, Stderr: "pamtester: Authentication service cannot retrieve authentication info\n"}, nil, verdictUndecided, reasonPAM},
		{"silent failure", result{ExitCode: 3}, nil, verdictUndecided, reasonExec},
		{"timeout", result{}, context.DeadlineExceeded, verdictUndecided, reasonTimeout},
		{"not found", result{}, os.ErrNotExist, verdictUndecided, reasonMissing},
	} {
		got := classify(tc.res, tc.err)
		assert.Equal(t, tc.v, got.V, tc.name)
		assert.Equal(t, tc.why, got.Reason, tc.name)
	}
}

func TestVerify_YesNoAndCouldNotDecide(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	ctx := context.Background()

	u, err := r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, "alice@local", u.Email, "the address a login name gets: <name>@<token>")
	assert.Equal(t, model.RoleUser, u.Role, "a machine login never opens an administrator account by itself")

	// No: wrong password, and a login PAM does not know.
	for _, ident := range []string{"alice", "nobody-here"} {
		_, err = r.d.VerifyPassword(ctx, ident, "wrong")
		assert.ErrorIs(t, err, auth.ErrUnauthorized, ident)
		assert.NotErrorIs(t, err, auth.ErrUndecided)
	}

	// Could not decide: never a wrong-password answer.
	for name, mode := range map[string]func(*world){
		"sudo wants a password": func(w *world) { w.sudoMode = "pwreq" },
		"sudo wants a tty":      func(w *world) { w.sudoMode = "tty" },
		"sudo refuses":          func(w *world) { w.sudoMode = "denied" },
		"pam is failing":        func(w *world) { w.pamMode = "sysdown" },
	} {
		r2 := newRig(t, map[string]any{"auto_create": true})
		mode(r2.w)
		_, err := r2.d.VerifyPassword(ctx, "alice", realPW)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, auth.ErrUndecided, name)
		assert.NotErrorIs(t, err, auth.ErrUnauthorized, name+": a broken setup must not read as a wrong password")
	}
}

func TestVerify_TimeoutIsCouldNotDecide(t *testing.T) {
	r := newRig(t, nil)
	r.w.pamMode = "hang"
	r.d.set.timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := r.d.VerifyPassword(context.Background(), "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUndecided)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	_ = start
}

// ── the password ────────────────────────────────────────────────────────────

func TestVerify_PasswordGoesOnStdinOnly(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	_, err := r.d.VerifyPassword(context.Background(), "alice", realPW)
	require.NoError(t, err)
	pt := r.w.pamtesterCalls()
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	for i, argv := range r.w.calls {
		for _, a := range argv {
			assert.NotContains(t, a, realPW, "the password is in an argv element of call %d: %v", i, argv)
		}
	}
	require.Len(t, pt, 1)
	assert.Equal(t, []string{filepath.Join(r.dir, "pamtester"), "filex", "alice", "authenticate", "acct_mgmt"}, pt[0])
	seen := false
	for i, in := range r.w.stdins {
		if in == realPW+"\n" {
			seen = true
			assert.Contains(t, []string{"sudo", "pamtester"}, filepath.Base(r.w.calls[i][0]), "only sudo (relaying it) and pamtester receive it")
		} else {
			assert.Empty(t, in, "no other command is given anything on stdin")
		}
	}
	assert.True(t, seen)
}

func TestVerify_RefusesWhatCouldInjectOrBypass(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	ctx := context.Background()
	cases := map[string][2]string{
		"empty password":            {"alice", ""},
		"empty login":               {"  ", realPW},
		"second line in a password": {"alice", realPW + "\nextra"},
		"carriage return":           {"alice", realPW + "\r"},
		"NUL":                       {"alice", realPW + "\x00"},
		"option as a name":          {"-alice", realPW},
		"double dash":               {"--help", realPW},
		"space in name":             {"al ice", realPW},
		"shell in name":             {"alice;id", realPW},
		"substitution":              {"$(id)", realPW},
		"newline in name":           {"alice\nroot", realPW},
		"someone else's address":    {"alice@corp.example", realPW},
		"leading @":                 {"@local", realPW},
		"too short":                 {"al", realPW},
	}
	for name, c := range cases {
		_, err := r.d.VerifyPassword(ctx, c[0], c[1])
		assert.ErrorIs(t, err, auth.ErrUnauthorized, name)
	}
	assert.Empty(t, r.w.pamtesterCalls(), "none of these may reach pamtester")
}

// ── accounts that never sign in ─────────────────────────────────────────────

func TestVerify_SystemAndServiceAccountsAreNeverLetIn(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	ctx := context.Background()
	// Every one of them has the right password on this fake machine.
	for _, ident := range []string{"root", "ROOT", "root@local", "svcacct", "nologin1", "daemonx"} {
		_, err := r.d.VerifyPassword(ctx, ident, map[string]string{
			"root": "root-pw", "ROOT": "root-pw", "root@local": "root-pw", "svcacct": "svc-pw", "nologin1": "nl-pw", "daemonx": "d-pw",
		}[ident])
		assert.ErrorIs(t, err, auth.ErrUnauthorized, ident)
	}
	assert.Empty(t, r.w.pamtesterCalls(), "PAM is not even asked about a forbidden account")
	got := refusals(t, r.store)
	assert.Len(t, got, 6)
	for _, reason := range got {
		assert.Equal(t, auth.ReasonForbiddenAccount, reason)
	}
	for _, e := range []string{"root@local", "svcacct@local", "nologin1@local", "daemonx@local"} {
		_, err := r.store.GetUserByEmail(ctx, e)
		assert.Error(t, err, e+" must not have been created")
	}
}

func TestVerify_AnExistingAccountOfAServiceLoginStillCannotSignIn(t *testing.T) {
	r := newRig(t, nil)
	addUser(t, r.store, "svcacct@local")
	_, err := r.d.VerifyPassword(context.Background(), "svcacct", "svc-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

func TestVerify_CannotAskGetentIsASetupProblemNotAForbiddenAccount(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	r.d.getent = "/nonexistent/getentx"
	_, err := r.d.VerifyPassword(context.Background(), "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUndecided)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	assert.Empty(t, r.w.pamtesterCalls(), "an account nobody could vouch for is not let in")
}

func TestIsSystemEntry(t *testing.T) {
	for line, want := range map[string]bool{
		"alice:x:1000:1000::/home/alice:/bin/bash":   false,
		"alice:x:999:999::/home/alice:/bin/bash":     true,
		"a:x:1000:1000::/h:/usr/sbin/nologin":        true,
		"a:x:1000:1000::/h:/bin/false":               true,
		"a:x:1000:1000::/h:/sbin/nologin":            true,
		"a:x:1000:1000::/h:":                         false,
		"a:x:0:0:root:/root:/bin/bash":               true,
		"a:x:65534:65534:nobody:/nonexistent:/bin/x": false,
	} {
		got, err := isSystemEntry(line)
		require.NoError(t, err, line)
		assert.Equal(t, want, got, line)
	}
	for _, bad := range []string{"", "junk", "a:x:notanumber:1:::/bin/sh"} {
		_, err := isSystemEntry(bad)
		assert.Error(t, err, bad)
	}
}

// ── the first-login rule ────────────────────────────────────────────────────

func TestFirstLogin_AutoCreateIsOffByDefault(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_, err := r.d.VerifyPassword(ctx, "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "a machine account is not a filex account until somebody says so")
	_, gerr := r.store.GetUserByEmail(ctx, "alice@local")
	assert.Error(t, gerr)
	assert.Equal(t, []string{auth.ReasonAutoCreateOff}, refusals(t, r.store))

	// The account exists: it signs in.
	existing := addUser(t, r.store, "alice@local")
	u, err := r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, existing.ID, u.ID)
	// Typing the address works too (the protocol resolver hands over the e-mail).
	u, err = r.d.VerifyPassword(ctx, "alice@local", realPW)
	require.NoError(t, err)
	assert.Equal(t, existing.ID, u.ID)
}

func TestFirstLogin_AllowedGroupsAreTheDoorAndGroupsAreRecorded(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, map[string]any{"auto_create": true, "allowed_groups": "STAFF"})
	u, err := r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	groups, err := r.store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"alice", "staff", "docs"}, groups)

	// carol is in no allowed group.
	_, err = r.d.VerifyPassword(ctx, "carol", tokPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, gerr := r.store.GetUserByEmail(ctx, "carol@local")
	assert.Error(t, gerr)
	assert.Equal(t, []string{auth.ReasonGroupNotAllowed}, refusals(t, r.store))

	// Every sign-in replaces the recorded groups.
	r.w.group["alice"] = "alice"
	_, err = r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	groups, _ = r.store.ListUserSSOGroups(ctx, u.ID)
	assert.Equal(t, []string{"alice"}, groups)
}

func TestFirstLogin_GroupsThatCannotBeReadAreNotGuessed(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	delete(r.w.group, "alice")
	_, err := r.d.VerifyPassword(context.Background(), "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUndecided)
	_, gerr := r.store.GetUserByEmail(context.Background(), "alice@local")
	assert.Error(t, gerr, "no account on a guess")
}

func TestEmailTokenAndDomain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, map[string]any{"auto_create": true, "email_token": "evim"})
	u, err := r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, "alice@evim", u.Email)
	_, err = r.d.VerifyPassword(ctx, "alice@local", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "the address of another token is not this machine's")

	r = newRig(t, map[string]any{"auto_create": true, "email_domain": "@Corp.Example", "email_token": "evim"})
	u, err = r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, "alice@corp.example", u.Email, "a domain wins over the token")
	_, err = r.d.VerifyPassword(ctx, "alice@corp.example", realPW)
	assert.NoError(t, err)

	d := New(r.store)
	assert.Error(t, d.load(map[string]any{"email_token": "bad token!"}))
}

func TestLoginMintsASessionAndVerifyDoesNot(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true})
	u, tok, err := r.d.Login(context.Background(), "alice", realPW)
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	assert.NotZero(t, u.ID)
	require.NoError(t, r.d.Logout(context.Background(), tok))
	_, tok, err = r.d.Login(context.Background(), "alice", "wrong")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Empty(t, tok)
}

// ── simultaneous attempts ───────────────────────────────────────────────────

func TestConcurrentAttemptsAreLimitedAndBusyIsNotAWrongPassword(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true, "max_concurrent": "1"})
	r.w.gate = make(chan struct{})
	r.w.inside = make(chan struct{}, 4)

	done := make(chan error, 1)
	go func() {
		_, err := r.d.VerifyPassword(context.Background(), "alice", realPW)
		done <- err
	}()
	select {
	case <-r.w.inside:
	case <-time.After(3 * time.Second):
		t.Fatal("the first attempt never reached pamtester")
	}

	start := time.Now()
	_, err := r.d.VerifyPassword(context.Background(), "carol", tokPW)
	assert.ErrorIs(t, err, auth.ErrBusy)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized, "busy is not a judgement of the password")
	assert.Less(t, time.Since(start), time.Second, "the second attempt does not wait")

	close(r.w.gate)
	require.NoError(t, <-done)
	// Room again.
	_, err = r.d.VerifyPassword(context.Background(), "carol", tokPW)
	assert.NotErrorIs(t, err, auth.ErrBusy)
}

// ── the test account becomes a super administrator ──────────────────────────

func TestGrantTestAccount(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, nil)
	cfg := map[string]any{}

	require.NoError(t, r.d.GrantTestAccount(ctx, r.store, cfg, "Alice"))
	u, err := r.store.GetUserByEmail(ctx, "alice@local")
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role)
	super, err := r.store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, super.ID, *u.ProviderID, "a SUPER administrator: homed in the platform tenant")

	// An existing ordinary account is raised, not duplicated.
	bob := addUser(t, r.store, "carol@local")
	require.Equal(t, model.RoleUser, bob.Role)
	require.NoError(t, r.d.GrantTestAccount(ctx, r.store, cfg, "carol"))
	c2, err := r.store.GetUserByEmail(ctx, "carol@local")
	require.NoError(t, err)
	assert.Equal(t, bob.ID, c2.ID)
	assert.Equal(t, model.RoleAdmin, c2.Role)

	// Nobody else is touched.
	other := addUser(t, r.store, "dave@local")
	require.NoError(t, r.d.GrantTestAccount(ctx, r.store, cfg, "alice"))
	o, _ := r.store.GetUserByEmail(ctx, "dave@local")
	assert.Equal(t, other.Role, o.Role)

	// It is in the audit trail.
	rows, _ := r.store.ListAuditRecent(ctx, 50)
	n := 0
	for _, a := range rows {
		if a.Action == AuditOSAdminGranted {
			n++
			assert.NotContains(t, strings.ToLower(toJSON(a.Metadata)), "pass")
		}
	}
	assert.Equal(t, 3, n)
	assert.Error(t, r.d.GrantTestAccount(ctx, "not a store", cfg, "alice"))
}

func toJSON(m map[string]any) string {
	var sb strings.Builder
	for k, v := range m {
		sb.WriteString(k)
		sb.WriteString("=")
		if s, ok := v.(string); ok {
			sb.WriteString(s)
		}
		sb.WriteString(";")
	}
	return sb.String()
}

// ── start-up ────────────────────────────────────────────────────────────────

func TestInit_BrokenSetupKeepsTheProviderOutAndSaysWhy(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		break_ func(r *rig, cfg map[string]any)
		step   string
		reason string
	}{
		"pamtester removed": {func(r *rig, cfg map[string]any) { cfg["pamtester_path"] = filepath.Join(r.dir, "gone") }, "pamtester", "missing"},
		"pam file removed": {func(r *rig, cfg map[string]any) {
			require.NoError(t, os.Remove(filepath.Join(r.dir, "pam.d", "filex")))
		}, "pam_service", "missing"},
		"sudo wants a password": {func(r *rig, cfg map[string]any) { r.w.sudoMode = "pwreq" }, "sudo", "password_required"},
		"a permissive stack":    {func(r *rig, cfg map[string]any) { r.w.pamMode = "permit" }, "unknown_user", "accepts_anyone"},
	} {
		r := newRig(t, nil)
		cfg := map[string]any{"pamtester_path": filepath.Join(r.dir, "pamtester"), "sudo_path": filepath.Join(r.dir, "sudo")}
		tc.break_(r, cfg)
		d := New(r.store)
		err := d.Init(ctx, cfg)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.step, name)
		assert.Contains(t, err.Error(), tc.reason, name)

		rows, _ := r.store.ListAuditRecent(ctx, 50)
		found := false
		for _, a := range rows {
			if a.Action == AuditProviderUnavailable {
				found = true
				assert.Equal(t, tc.step, a.Metadata["step"], name)
				assert.Equal(t, tc.reason, a.Metadata["reason"], name)
			}
		}
		assert.True(t, found, name+": the administrator's trail says the provider was left out")
	}
}

func TestInit_NilStoreAndBadConfig(t *testing.T) {
	assert.Error(t, (&Driver{}).Init(context.Background(), nil))
	r := newRig(t, nil)
	assert.Error(t, New(r.store).Init(context.Background(), map[string]any{"pamtester_path": "rel"}))
}

func TestVerify_UninitialisedDriverDoesNotPanic(t *testing.T) {
	d := New(nil)
	_, err := d.VerifyPassword(context.Background(), "alice", realPW)
	assert.True(t, errors.Is(err, auth.ErrUndecided))
}
