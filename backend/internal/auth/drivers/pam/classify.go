package pam

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"regexp"
	"strings"
)

var (
	pamtesterMsg = regexp.MustCompile(`(?m)(?:^|\s)pamtester: `)
	// sudoersDiag is the first line of a sudoers parse diagnostic,
	// `<file>:<line>:<column>: <message>` (sudo-rs, and classic sudo 1.9),
	// sometimes behind the program's own `sudo: `.
	sudoersDiag = regexp.MustCompile(`^(?:sudo(?:-rs)?: )?/\S*:\d+:\d+: `)
	// caretLine is its last line: a caret under the echoed sudoers line.
	caretLine = regexp.MustCompile(`^\s*\^~*\s*$`)
)

// verdict is what one authentication run comes to.
type verdict int

const (
	// verdictOK: PAM said yes (authenticate AND acct_mgmt).
	verdictOK verdict = iota
	// verdictDenied: PAM said no — a wrong password, a user it does not know,
	// an account that is locked or has expired. Answered as ErrUnauthorized.
	verdictDenied
	// verdictUndecided: nothing was decided — the setup is incomplete (no
	// pamtester, sudo wants a password), PAM itself is broken, or the command
	// timed out. NEVER answered as a wrong password: the operator has to see
	// it, and the person has to be told to try again, not that they mistyped.
	verdictUndecided
)

// Reasons an undecided run gives. Stable strings: they are probe params and
// log fields, and the hint catalogue is keyed by them.
const (
	reasonMissing          = "missing"           // the command (sudo / pamtester) is not there
	reasonPasswordRequired = "password_required" // sudo -n wants a password: no NOPASSWD rule
	reasonRequireTTY       = "requiretty"        // sudo insists on a terminal
	reasonNotAllowed       = "not_allowed"       // sudoers does not allow this command
	reasonSudo             = "sudo"              // some other sudo failure
	reasonTimeout          = "timeout"
	reasonPAM              = "pam_error" // pamtester ran and PAM reported a system fault
	reasonExec             = "exec"      // the command could not be started / was killed
)

type outcome struct {
	V      verdict
	Reason string
	// Detail is the tool's own message — for the operator's log and the test's
	// `detail` param. It never holds a password (none is ever in a command's
	// output).
	Detail string
}

// pamFaults are pamtester's messages for a PAM failure that says nothing about
// the person: PAM_SYSTEM_ERR, PAM_ABORT, PAM_CONV_ERR, PAM_AUTHINFO_UNAVAIL
// (sssd down, a directory unreachable)… Anything else pamtester prints on a
// failed run is a "no" (PAM_AUTH_ERR, PAM_USER_UNKNOWN, PAM_ACCT_EXPIRED,
// PAM_PERM_DENIED, PAM_MAXTRIES, PAM_NEW_AUTHTOK_REQD …).
var pamFaults = []string{
	"system error", "critical error", "service error", "conversation error",
	"buffer error", "abort", "module is unknown", "symbol not found",
	"cannot retrieve authentication info", "token manipulation error",
	"failure setting",
}

// classify reads the outcome of the sudo-wrapped pamtester run.
//
// The two tools speak on stderr and are told apart by their own prefix: a
// `pamtester:` message means PAM was reached and judged; anything else that
// fails is sudo (or the shell-less exec) speaking, which is a setup problem.
// Exit 0 with nothing to say is a yes.
//
// Two sudos answer, in different words: classic sudo ("a password is
// required", "is not allowed to execute") and sudo-rs, the default sudo from
// Ubuntu 25.10 on ("interactive authentication is required", "may not run
// sudo", "I'm afraid I can't do that"). sudo's answer is the LAST thing it
// says: a warning before it ("unable to resolve host …") is not the answer, and
// neither is a sudoers parse diagnostic (sudoSpeech) — its words are the file's.
func classify(res result, runErr error) outcome {
	if runErr != nil {
		switch {
		case errors.Is(runErr, context.DeadlineExceeded), errors.Is(runErr, context.Canceled):
			return outcome{V: verdictUndecided, Reason: reasonTimeout}
		case errors.Is(runErr, exec.ErrNotFound), errors.Is(runErr, fs.ErrNotExist):
			return outcome{V: verdictUndecided, Reason: reasonMissing, Detail: runErr.Error()}
		}
		return outcome{V: verdictUndecided, Reason: reasonExec, Detail: runErr.Error()}
	}
	if res.ExitCode == 0 {
		return outcome{V: verdictOK}
	}
	stderr := res.Stderr
	// pamtester's own message: `pamtester: <reason>`, at the start of a line or
	// right after the "Password: " prompt it printed. NOT sudo's
	// "/usr/bin/pamtester: command not found", where a slash comes first.
	if ms := pamtesterMsg.FindAllStringIndex(stderr, -1); len(ms) > 0 {
		i := ms[len(ms)-1][1]
		msg := strings.TrimSpace(firstLine(stderr[i:]))
		low := strings.ToLower(msg)
		for _, f := range pamFaults {
			if strings.Contains(low, f) {
				return outcome{V: verdictUndecided, Reason: reasonPAM, Detail: msg}
			}
		}
		return outcome{V: verdictDenied, Detail: msg}
	}
	said, diag := sudoSpeech(stderr)
	if len(said) == 0 {
		if len(diag) > 0 {
			// Nothing but a sudoers diagnostic: sudo's own configuration.
			return outcome{V: verdictUndecided, Reason: reasonSudo, Detail: clip(diag[0])}
		}
		return outcome{V: verdictUndecided, Reason: reasonExec}
	}
	answer := said[len(said)-1]
	detail := clip(answer)
	if len(diag) > 0 {
		// The operator still sees the complaint about the file, after the
		// answer (sudo-rs and a `Defaults:filex !requiretty` line).
		detail += " [" + clip(diag[0]) + "]"
	}
	low := strings.ToLower(answer)
	switch {
	case strings.Contains(low, "a password is required"), strings.Contains(low, "terminal is required"),
		strings.Contains(low, "interactive authentication is required"):
		return outcome{V: verdictUndecided, Reason: reasonPasswordRequired, Detail: detail}
	case strings.Contains(low, "tty"):
		return outcome{V: verdictUndecided, Reason: reasonRequireTTY, Detail: detail}
	case strings.Contains(low, "not allowed"), strings.Contains(low, "not in the sudoers"), strings.Contains(low, "may not run sudo"),
		strings.Contains(low, "afraid i can"):
		return outcome{V: verdictUndecided, Reason: reasonNotAllowed, Detail: detail}
	case strings.Contains(low, "command not found"), strings.Contains(low, "no such file"):
		return outcome{V: verdictUndecided, Reason: reasonMissing, Detail: detail}
	case strings.Contains(low, "sudo"):
		return outcome{V: verdictUndecided, Reason: reasonSudo, Detail: detail}
	}
	return outcome{V: verdictUndecided, Reason: reasonExec, Detail: detail}
}

// sudoSpeech splits what sudo wrote to stderr into what it SAID (its own lines,
// in order) and the sudoers parse diagnostics it printed before them. A
// diagnostic is three lines — `<file>:<line>:<column>: <message>`, the sudoers
// line it is about, a caret under it — and sudo prints it on every call while
// the file is there. Its words are the file's, not sudo's answer: sudo-rs's
// "unknown setting: 'requiretty'" must not read as sudo asking for a terminal.
func sudoSpeech(stderr string) (said, diag []string) {
	lines := strings.Split(stderr, "\n")
	drop := make([]bool, len(lines))
	for i, l := range lines {
		switch {
		case sudoersDiag.MatchString(l):
			drop[i] = true
			diag = append(diag, strings.TrimSpace(l))
		case caretLine.MatchString(l):
			drop[i] = true
			if i > 0 {
				drop[i-1] = true // the sudoers line the caret points into
			}
		}
	}
	for i, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !drop[i] {
			said = append(said, l)
		}
	}
	return said, diag
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// clip cuts a line to a length a log line can carry.
func clip(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
