//go:build linux

package pam

// The provider test is the gate the provider cannot be switched on without.
// Each step is shown failing with its reason and the fix (the `hint`), and the
// step after a failure is shown NOT to run.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

func (r *rig) cfg(extra map[string]any) map[string]any {
	c := map[string]any{"pamtester_path": filepath.Join(r.dir, "pamtester"), "sudo_path": filepath.Join(r.dir, "sudo")}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func (r *rig) probe(ctx context.Context, extra map[string]any) []auth.ProbeCheck {
	return (&Driver{}).Probe(ctx, r.cfg(extra), nil)
}

func withAccount(u, p string) context.Context {
	return auth.WithTestAccount(context.Background(), &auth.TestAccount{Username: u, Password: p})
}

func byID(cs []auth.ProbeCheck) map[string]auth.ProbeCheck {
	m := map[string]auth.ProbeCheck{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return m
}

func lastFail(t *testing.T, cs []auth.ProbeCheck) auth.ProbeCheck {
	t.Helper()
	require.NotEmpty(t, cs)
	last := cs[len(cs)-1]
	require.Equal(t, auth.ProbeFail, last.Status, "the probe stops at the failing step: %+v", cs)
	assert.NotEmpty(t, last.Params["reason"])
	assert.NotEmpty(t, last.Params["hint"], "every failure names the fix")
	assert.False(t, auth.ProbeOKAll(cs))
	return last
}

func TestProbe_HappyPathEndsInARealSignIn(t *testing.T) {
	r := newRig(t, nil)
	cs := r.probe(withAccount("alice", realPW), nil)
	assert.True(t, auth.ProbeOKAll(cs), "%+v", cs)
	ids := []string{}
	for _, c := range cs {
		assert.Equal(t, auth.ProbeOK, c.Status, c.ID)
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []string{"config", "first_login_closed", "pamtester", "pam_service", "sudo", "unknown_user", "test_account"}, ids)
	assert.Equal(t, "alice", byID(cs)["test_account"].Params["account"])

	// Testing creates nothing: the account is made when the provider is saved.
	_, err := r.store.GetUserByEmail(context.Background(), "alice@local")
	assert.Error(t, err)

	// The only pamtester calls are the probe's own: a login that does not exist,
	// and the test account.
	var users []string
	for _, c := range r.w.pamtesterCalls() {
		users = append(users, c[2])
	}
	require.Len(t, users, 2) // the probe's unknown user, the test account
	assert.Contains(t, users[0], "filex-probe-")
	assert.Equal(t, "alice", users[1])
}

func TestProbe_PasswordNeverAppearsInTheAnswer(t *testing.T) {
	r := newRig(t, nil)
	for _, pw := range []string{realPW, "wrong-pw-xyz"} {
		cs := r.probe(withAccount("alice", pw), nil)
		b, _ := json.Marshal(cs)
		assert.NotContains(t, string(b), pw)
	}
	ta := auth.TestAccount{Username: "alice", Password: "hunter2-secret"}
	assert.NotContains(t, ta.String(), "hunter2")
	assert.NotContains(t, (&ta).String(), "hunter2")
}

func TestProbe_TestAccountStep(t *testing.T) {
	r := newRig(t, nil)
	cases := []struct {
		name   string
		ctx    context.Context
		reason string
	}{
		{"no account given", context.Background(), "missing"},
		{"empty password", withAccount("alice", ""), "missing"},
		{"wrong password", withAccount("alice", "nope"), "wrong_password"},
		{"unknown login", withAccount("nobody-here", "x"), "wrong_password"},
		{"root", withAccount("root", "root-pw"), "forbidden_account"},
		{"a service account", withAccount("svcacct", "svc-pw"), "forbidden_account"},
		{"a nologin shell", withAccount("nologin1", "nl-pw"), "forbidden_account"},
		{"not a name", withAccount("-alice", "x"), "invalid_name"},
		{"an address of another domain", withAccount("alice@corp.example", "x"), "invalid_name"},
		{"newline in the password", withAccount("alice", realPW+"\nx"), "wrong_password"},
	}
	for _, tc := range cases {
		cs := r.probe(tc.ctx, nil)
		f := lastFail(t, cs)
		assert.Equal(t, "test_account", f.ID, tc.name)
		assert.Equal(t, tc.reason, f.Params["reason"], tc.name)
	}
	// None of the failing ones may have reached pamtester with a forbidden account.
	for _, c := range r.w.pamtesterCalls() {
		assert.NotContains(t, []string{"root", "svcacct", "nologin1", "-alice"}, c[2])
	}
}

func TestProbe_EachSetupStepFailsWithItsReasonAndStops(t *testing.T) {
	type tc struct {
		name   string
		mut    func(r *rig)
		extra  func(r *rig) map[string]any
		id     string
		reason string
		hint   string
		after  []string // steps that must NOT have run
	}
	cases := []tc{
		{"no pamtester", nil, func(r *rig) map[string]any { return map[string]any{"pamtester_path": filepath.Join(r.dir, "absent")} },
			"pamtester", "missing", "apt install pamtester", []string{"pam_service", "sudo", "unknown_user", "test_account"}},
		{"pamtester not executable", func(r *rig) { require.NoError(t, os.Chmod(filepath.Join(r.dir, "pamtester"), 0o644)) }, nil,
			"pamtester", "not_executable", "chmod 755", []string{"pam_service"}},
		{"no PAM service file", func(r *rig) { require.NoError(t, os.Remove(filepath.Join(r.dir, "pam.d", "filex"))) }, nil,
			"pam_service", "missing", "pam_unix.so", []string{"sudo", "unknown_user", "test_account"}},
		{"another service without a file", nil, func(r *rig) map[string]any { return map[string]any{"service": "filex-2fa"} },
			"pam_service", "missing", "filex-2fa", []string{"sudo"}},
		{"no sudo", nil, func(r *rig) map[string]any { return map[string]any{"sudo_path": filepath.Join(r.dir, "nosudo")} },
			"sudo", "missing", "apt install sudo", []string{"unknown_user", "test_account"}},
		{"sudo wants a password", func(r *rig) { r.w.sudoMode = "pwreq" }, nil,
			"sudo", "password_required", "NOPASSWD", []string{"unknown_user", "test_account"}},
		{"sudo insists on a tty", func(r *rig) { r.w.sudoMode = "tty" }, nil,
			"sudo", "requiretty", "!requiretty", []string{"unknown_user", "test_account"}},
		{"sudoers does not name the command", func(r *rig) { r.w.sudoMode = "denied" }, nil,
			"sudo", "not_allowed", "NOPASSWD", []string{"unknown_user", "test_account"}},
		{"PAM is failing", func(r *rig) { r.w.pamMode = "sysdown" }, nil,
			"unknown_user", "pam_error", "/etc/pam.d/filex", []string{"test_account"}},
		{"a stack that lets everybody in", func(r *rig) { r.w.pamMode = "permit" }, nil,
			"unknown_user", "accepts_anyone", "pam_permit", []string{"test_account"}},
	}
	for _, c := range cases {
		r := newRig(t, nil)
		if c.mut != nil {
			c.mut(r)
		}
		var extra map[string]any
		if c.extra != nil {
			extra = c.extra(r)
		}
		cs := r.probe(withAccount("alice", realPW), extra)
		f := lastFail(t, cs)
		assert.Equal(t, c.id, f.ID, c.name)
		assert.Equal(t, c.reason, f.Params["reason"], c.name)
		assert.Contains(t, f.Params["hint"], c.hint, c.name)
		got := byID(cs)
		for _, later := range c.after {
			_, ran := got[later]
			assert.False(t, ran, "%s: step %s must not run after a failure", c.name, later)
		}
	}
}

func TestProbe_SudoHintNamesTheExactSudoersLine(t *testing.T) {
	r := newRig(t, nil)
	r.w.sudoMode = "pwreq"
	f := lastFail(t, r.probe(withAccount("alice", realPW), nil))
	pt := filepath.Join(r.dir, "pamtester")
	assert.Contains(t, f.Params["hint"], "ALL=(root) NOPASSWD: "+pt+" filex *")
}

func TestProbe_HintsAreInTheAdministratorsLanguage(t *testing.T) {
	r := newRig(t, nil)
	r.w.sudoMode = "pwreq"
	en := lastFail(t, r.probe(auth.WithProbeLang(withAccount("alice", realPW), "en"), nil))
	tr := lastFail(t, r.probe(auth.WithProbeLang(withAccount("alice", realPW), "tr"), nil))
	assert.Contains(t, en.Params["hint"], "asks for a password")
	assert.Contains(t, tr.Params["hint"], "parola istiyor")
	assert.Contains(t, tr.Params["hint"], "yeniden test edin")
	assert.NotEqual(t, en.Params["hint"], tr.Params["hint"])
}

func TestProbe_WithoutSudo(t *testing.T) {
	r := newRig(t, nil)
	// use_sudo off: no sudo step, and pamtester runs directly.
	cs := r.probe(withAccount("alice", realPW), map[string]any{"use_sudo": false})
	assert.True(t, auth.ProbeOKAll(cs), "%+v", cs)
	_, has := byID(cs)["sudo"]
	assert.False(t, has)
}

func TestProbe_BadConfigIsOneClearFailure(t *testing.T) {
	r := newRig(t, nil)
	cs := r.probe(withAccount("alice", realPW), map[string]any{"pamtester_path": "pamtester"})
	require.Len(t, cs, 1)
	assert.Equal(t, "config", cs[0].ID)
	assert.Equal(t, auth.ProbeFail, cs[0].Status)
}

func TestProbe_AllowedGroupsAreReadFromId(t *testing.T) {
	r := newRig(t, nil)
	cs := r.probe(withAccount("alice", realPW), map[string]any{"auto_create": true, "allowed_groups": "staff"})
	assert.True(t, auth.ProbeOKAll(cs))
	c := byID(cs)["first_login_groups"]
	assert.Equal(t, "staff", c.Params["groups"])
}

func TestProbe_NotLinux(t *testing.T) {
	r := newRig(t, nil)
	platformOK = false
	cs := r.probe(withAccount("alice", realPW), nil)
	f := lastFail(t, cs)
	assert.Equal(t, "platform", f.ID)
}

func TestStrictProbeAndPassword(t *testing.T) {
	var d auth.Driver = &Driver{}
	sp, ok := d.(auth.StrictProber)
	require.True(t, ok)
	assert.True(t, sp.StrictProbe(), "confirm_failed_test is not honoured for this provider")
}
