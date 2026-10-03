package pam

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// pamDir is where PAM keeps its service files. A variable so a test can point
// it at a temporary directory.
var pamDir = "/etc/pam.d"

// platformOK: the provider needs Linux-PAM.
var platformOK = runtime.GOOS == "linux"

// Probe tests a configuration the way a sign-in would use it, stopping at the
// first step that fails. Every failing step carries a `reason` and a `hint`:
// the sentence, in the administrator's language, that names the fix — with the
// command to run.
//
//	config        the settings are well-formed
//	first_login…  what happens to a person with no account (auth.FirstLoginCheck)
//	pamtester     the tool is installed and runnable
//	pam_service   /etc/pam.d/<service> exists
//	sudo          sudo may run that one command without a password (no
//	              requiretty); skipped when use_sudo is off
//	unknown_user  a login that does not exist is a clean "no" — not an error,
//	              and not a "yes"
//	test_account  the account the administrator named signs in for real
//
// ⚠ This is the gate the provider cannot be switched on without (StrictProbe):
// no `confirm_failed_test`. The last step needs `test_account` on the request
// (auth.TestAccountFrom) — its password is used for this one call and goes
// nowhere else.
func (d *Driver) Probe(ctx context.Context, cfg map[string]any, _ *http.Request) []auth.ProbeCheck {
	p := &Driver{run: d.run, getent: d.getent, idTool: d.idTool}
	if err := p.load(cfg); err != nil {
		return []auth.ProbeCheck{auth.Check("config", auth.ProbeFail, "reason", "invalid", "detail", err.Error())}
	}
	out := []auth.ProbeCheck{auth.Check("config", auth.ProbeOK)}

	// The policy this provider really runs (auto_create defaults to OFF here).
	pc := map[string]any{"allowed_groups": auth.CfgString(cfg, "allowed_groups"), "auto_create": p.firstLogin.AutoCreate}
	out = append(out, auth.FirstLoginCheck(pc, "id -Gn"))

	setup := p.setupChecks(ctx)
	out = append(out, setup...)
	if !auth.ProbeOKAll(setup) || hasFail(setup) {
		return out
	}
	return append(out, p.testAccountCheck(ctx))
}

func hasFail(cs []auth.ProbeCheck) bool {
	for _, c := range cs {
		if c.Status == auth.ProbeFail {
			return true
		}
	}
	return false
}

// hint is the sentence that says what to do, in the reader's language.
func (d *Driver) hint(ctx context.Context, key string, extra map[string]string) string {
	vars := srvtext.Vars{
		"pamtester": d.set.pamtester, "sudo": d.set.sudo, "service": d.set.service,
		"pamfile": filepath.Join(pamDir, d.set.service), "runas": runAs(),
	}
	for k, v := range extra {
		vars[k] = v
	}
	return srvtext.Text(auth.ProbeLangFrom(ctx), "server.auth_provider.pam_hint_"+key, vars)
}

// runAs is the operating-system account filex runs as — the one sudoers must
// name.
func runAs() string {
	if u, err := user.Current(); err == nil && u.Username != "" && userRe.MatchString(u.Username) {
		return u.Username
	}
	return "filex"
}

func (d *Driver) fail(ctx context.Context, id, reason, hintKey, detail string, kv ...string) auth.ProbeCheck {
	params := []string{"reason", reason, "hint", d.hint(ctx, hintKey, nil)}
	if detail != "" {
		params = append(params, "detail", detail)
	}
	return auth.Check(id, auth.ProbeFail, append(params, kv...)...)
}

// setupChecks are steps 1-4: everything about the machine that does not need a
// person's password. It runs when the provider is tested AND every time filex
// starts (Init).
func (d *Driver) setupChecks(ctx context.Context) []auth.ProbeCheck {
	var out []auth.ProbeCheck
	if !platformOK {
		return append(out, d.fail(ctx, "platform", "unsupported", "platform", ""))
	}

	// 1. pamtester is installed and runnable.
	fi, err := os.Stat(d.set.pamtester)
	switch {
	case err != nil:
		return append(out, d.fail(ctx, "pamtester", reasonMissing, "pamtester_missing", err.Error(), "path", d.set.pamtester))
	case fi.IsDir() || fi.Mode()&0o111 == 0:
		return append(out, d.fail(ctx, "pamtester", "not_executable", "pamtester_not_executable", "", "path", d.set.pamtester))
	}
	out = append(out, auth.Check("pamtester", auth.ProbeOK, "path", d.set.pamtester))

	// 2. The PAM service file exists. Without it PAM falls back to its `other`
	// service, which usually refuses everybody — a setup that "works" by
	// answering no to all.
	file := filepath.Join(pamDir, d.set.service)
	if _, err := os.Stat(file); err != nil {
		return append(out, d.fail(ctx, "pam_service", reasonMissing, "pam_service_missing", err.Error(), "file", file))
	}
	out = append(out, auth.Check("pam_service", auth.ProbeOK, "file", file))

	probeUser := "filex-probe-" + randHex(4)

	// 3. sudo may run exactly this command without a password.
	if d.set.useSudo {
		if _, err := os.Stat(d.set.sudo); err != nil {
			return append(out, d.fail(ctx, "sudo", reasonMissing, "sudo_missing", err.Error(), "path", d.set.sudo))
		}
		cctx, cancel := context.WithTimeout(ctx, d.set.timeout)
		res, rerr := d.run.Run(cctx, []string{d.set.sudo, "-n", "-l", d.set.pamtester, d.set.service, probeUser, "authenticate", "acct_mgmt"}, "")
		cancel()
		o := classify(res, rerr)
		if o.V != verdictOK {
			reason := o.Reason
			switch reason {
			case reasonPasswordRequired, reasonRequireTTY, reasonNotAllowed, reasonTimeout:
			default:
				// `sudo -l` of a command sudoers does not name exits 1, and
				// older sudo says nothing at all.
				reason = reasonNotAllowed
			}
			key := "sudo_" + reason
			if reason == reasonTimeout {
				key = "pam_broken"
			}
			return append(out, d.fail(ctx, "sudo", reason, key, o.Detail))
		}
		out = append(out, auth.Check("sudo", auth.ProbeOK))
	}

	// 4. A login that does not exist is a clean "no".
	argv, err := d.set.authArgv(probeUser)
	if err != nil {
		return append(out, d.fail(ctx, "unknown_user", reasonExec, "pam_broken", err.Error()))
	}
	cctx, cancel := context.WithTimeout(ctx, d.set.timeout)
	res, rerr := d.run.Run(cctx, argv, randHex(12)+"\n")
	cancel()
	o := classify(res, rerr)
	switch o.V {
	case verdictDenied:
		return append(out, auth.Check("unknown_user", auth.ProbeOK))
	case verdictOK:
		return append(out, d.fail(ctx, "unknown_user", "accepts_anyone", "accepts_anyone", ""))
	}
	key := "sudo_" + o.Reason
	switch o.Reason {
	case reasonPasswordRequired, reasonRequireTTY, reasonNotAllowed:
	case reasonMissing:
		key = "pamtester_missing"
	default:
		key = "pam_broken"
	}
	return append(out, d.fail(ctx, "unknown_user", o.Reason, key, o.Detail))
}

// testAccountCheck is step 5: a real sign-in with the account the
// administrator named.
func (d *Driver) testAccountCheck(ctx context.Context) auth.ProbeCheck {
	ta := auth.TestAccountFrom(ctx)
	if ta == nil || ta.Password == "" {
		return d.fail(ctx, "test_account", "missing", "test_account_missing", "")
	}
	if strings.ContainsAny(ta.Password, "\r\n\x00") {
		return d.fail(ctx, "test_account", "wrong_password", "test_account_wrong", "")
	}
	name, ok := d.osName(ta.Username, "")
	if !ok || !userRe.MatchString(name) {
		return d.fail(ctx, "test_account", identity.RefuseInvalidName, "test_account_invalid", "")
	}
	cctx, cancel := context.WithTimeout(ctx, d.set.timeout)
	defer cancel()
	chk := &systemChecker{d: d}
	name, refusal := identity.CheckOSLogin(cctx, name, chk)
	switch {
	case refusal != "" && chk.err != nil:
		return d.fail(ctx, "test_account", "getent", "getent_failed", chk.err.Error())
	case refusal == identity.RefuseForbidden:
		return d.fail(ctx, "test_account", identity.RefuseForbidden, "test_account_forbidden", "")
	case refusal != "":
		return d.fail(ctx, "test_account", identity.RefuseInvalidName, "test_account_invalid", "")
	}
	argv, err := d.set.authArgv(name)
	if err != nil {
		return d.fail(ctx, "test_account", identity.RefuseInvalidName, "test_account_invalid", "")
	}
	res, rerr := d.run.Run(cctx, argv, ta.Password+"\n")
	o := classify(res, rerr)
	switch o.V {
	case verdictOK:
		ta.Email = d.email(name, "")
		return auth.Check("test_account", auth.ProbeOK, "account", name)
	case verdictDenied:
		return d.fail(ctx, "test_account", "wrong_password", "test_account_wrong", o.Detail, "account", name)
	}
	key := "pam_broken"
	switch o.Reason {
	case reasonPasswordRequired, reasonRequireTTY, reasonNotAllowed:
		key = "sudo_" + o.Reason
	}
	return d.fail(ctx, "test_account", o.Reason, key, o.Detail)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ auth.Prober = (*Driver)(nil)
