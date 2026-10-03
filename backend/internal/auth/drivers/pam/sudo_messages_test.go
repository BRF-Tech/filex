//go:build linux

package pam

// What real sudos say, and what filex makes of it. The texts are verbatim from a
// real Ubuntu 26.04 machine (filex #128, 2026-09-30), where /usr/bin/sudo is
// sudo-rs 0.2.13 and classic sudo is /usr/bin/sudo.ws; only the host name is
// replaced. sudo-rs is the default sudo from Ubuntu 25.10 on, and it words every
// answer differently from classic sudo.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

const (
	// sudo-rs: the rule is there but has no NOPASSWD.
	rsInteractive = "sudo: interactive authentication is required\n"
	// sudo-rs: sudoers has no rule for the account - `sudo -n <command>` ...
	rsAfraid = "sudo: I'm sorry filex. I'm afraid I can't do that\n"
	// ... and `sudo -n -l <command>`.
	rsMayNotRun = "sudo: Sorry, user filex may not run sudo on files-host.\n"
	// sudo-rs reading a sudoers file that holds `Defaults:filex !requiretty`, a
	// setting sudo-rs does not have. It prints this on EVERY call while the file
	// is there, before whatever it says next.
	rsRequirettyWarning = "/etc/sudoers.d/filex:1:17: unknown setting: 'requiretty'\n" +
		"Defaults:filex !requiretty\n" +
		"                ^~~~~~~~~~\n"
	rsRequirettyNote = " [/etc/sudoers.d/filex:1:17: unknown setting: 'requiretty']"
	// classic sudo: no NOPASSWD, or no rule at all.
	wsPasswordRequired = "sudo: a password is required\n"
	// classic sudo's warning when the machine cannot resolve its own name,
	// printed before the answer.
	wsResolveWarning = "sudo: unable to resolve host files-host: Name or service not known\n"
	wsNotAllowed     = "Sorry, user filex is not allowed to execute '/usr/bin/pamtester filex alice authenticate acct_mgmt' as root on files-host.\n"
)

func TestClassify_WhatRealSudosSay(t *testing.T) {
	for _, c := range []struct {
		name   string
		stderr string
		v      verdict
		why    string
		detail string
	}{
		{"sudo-rs: a rule without NOPASSWD", rsInteractive,
			verdictUndecided, reasonPasswordRequired, "sudo: interactive authentication is required"},
		{"sudo-rs: no rule (sudo -n)", rsAfraid,
			verdictUndecided, reasonNotAllowed, "sudo: I'm sorry filex. I'm afraid I can't do that"},
		{"sudo-rs: no rule (sudo -n -l)", rsMayNotRun,
			verdictUndecided, reasonNotAllowed, "sudo: Sorry, user filex may not run sudo on files-host."},

		// The sudoers diagnostic is the FILE speaking, not sudo's answer: its
		// words ("requiretty") must not decide the reason. The operator still
		// sees it, after the answer.
		{"sudo-rs: the requiretty line, then a rule without NOPASSWD", rsRequirettyWarning + rsInteractive,
			verdictUndecided, reasonPasswordRequired, "sudo: interactive authentication is required" + rsRequirettyNote},
		{"sudo-rs: the requiretty line, then no rule", rsRequirettyWarning + rsAfraid,
			verdictUndecided, reasonNotAllowed, "sudo: I'm sorry filex. I'm afraid I can't do that" + rsRequirettyNote},
		{"sudo-rs: the requiretty line twice", rsRequirettyWarning + rsRequirettyWarning + rsInteractive,
			verdictUndecided, reasonPasswordRequired, "sudo: interactive authentication is required" + rsRequirettyNote},
		{"sudo-rs: the requiretty line, then pamtester's no", rsRequirettyWarning + "Password: pamtester: Authentication failure\n",
			verdictDenied, "", "Authentication failure"},
		{"sudo-rs: the requiretty line and nothing after it", rsRequirettyWarning,
			verdictUndecided, reasonSudo, "/etc/sudoers.d/filex:1:17: unknown setting: 'requiretty'"},

		{"classic sudo: no NOPASSWD", wsPasswordRequired,
			verdictUndecided, reasonPasswordRequired, "sudo: a password is required"},
		// sudo's answer is the last thing it says; a warning before it is not.
		{"classic sudo: a warning, then no NOPASSWD", wsResolveWarning + wsPasswordRequired,
			verdictUndecided, reasonPasswordRequired, "sudo: a password is required"},
		{"classic sudo: a warning, then no rule", wsResolveWarning + wsNotAllowed,
			verdictUndecided, reasonNotAllowed, strings.TrimSpace(wsNotAllowed)},
		{"classic sudo: requiretty", "sudo: sorry, you must have a tty to run sudo\n",
			verdictUndecided, reasonRequireTTY, "sudo: sorry, you must have a tty to run sudo"},
	} {
		got := classify(result{ExitCode: 1, Stderr: c.stderr}, nil)
		assert.Equal(t, c.v, got.V, c.name)
		assert.Equal(t, c.why, got.Reason, c.name)
		assert.Equal(t, c.detail, got.Detail, c.name)
	}
}

// The loop the real machine showed: sudo-rs, the documented
// `Defaults:filex !requiretty` line and a rule without NOPASSWD. The test must
// ask for the NOPASSWD line - never for the requiretty line that sudo-rs
// complains about.
func TestProbe_SudoRsWithTheRequirettyLineAsksForNOPASSWD(t *testing.T) {
	for _, lang := range []string{"en", "tr"} {
		r := newRig(t, nil)
		r.w.sudoMode, r.w.sudoErr = "raw", rsRequirettyWarning+rsInteractive
		f := lastFail(t, r.probe(auth.WithProbeLang(withAccount("alice", realPW), lang), nil))
		assert.Equal(t, "sudo", f.ID, lang)
		assert.Equal(t, reasonPasswordRequired, f.Params["reason"], lang)
		assert.Contains(t, f.Params["hint"], "NOPASSWD: "+filepath.Join(r.dir, "pamtester")+" filex *", lang)
		assert.NotContains(t, f.Params["hint"], "requiretty", "%s: the hint must not ask for the line sudo-rs rejects", lang)
		assert.Contains(t, f.Params["detail"], "unknown setting: 'requiretty'", "%s: the operator still sees the warning", lang)
	}
}

// The same answers at a sign-in: "could not decide", never a wrong password, with
// the reason the log and the troubleshooting table name.
func TestVerify_SudoRsAnswersGiveTheirReason(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ stderr, reason string }{
		{rsInteractive, reasonPasswordRequired},
		{rsAfraid, reasonNotAllowed},
		{rsRequirettyWarning + rsInteractive, reasonPasswordRequired},
		{rsRequirettyWarning + rsAfraid, reasonNotAllowed},
	} {
		r := newRig(t, map[string]any{"auto_create": true})
		r.w.sudoMode, r.w.sudoErr = "raw", c.stderr
		_, err := r.d.VerifyPassword(ctx, "alice", realPW)
		require.Error(t, err, c.stderr)
		assert.ErrorIs(t, err, auth.ErrUndecided, c.stderr)
		assert.NotErrorIs(t, err, auth.ErrUnauthorized, c.stderr)
		assert.True(t, strings.HasSuffix(err.Error(), "pam: "+c.reason), "%q: %v", c.stderr, err)
	}
}
