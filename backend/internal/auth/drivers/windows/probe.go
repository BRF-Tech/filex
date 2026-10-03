package windows

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/identity"
)

// StrictProbe implements auth.StrictProber: an operating-system provider is
// never switched on over a failing test (the panel's "switch on anyway" does
// not exist for it), because the only proof it works is a real sign-in.
func (d *Driver) StrictProbe() bool { return true }

// Probe tests the provider the only way that proves anything: by signing a real
// account in with the operating system, exactly as a login would.
//
//	config     the configuration reads (a valid default domain)
//	platform   this machine can ask Windows — on any other OS the test stops
//	           here with unsupported_os
//	test_account   an account was given on the test request
//	logon      that account signed in (the reason is named when it did not)
//	account    the account is one filex would accept: not a system, service or
//	           built-in account
//
// The account and its password come from the request that asked for the test
// (auth.TestAccountFrom): used for this one call, never stored, never logged,
// audited or returned. On success the prober records the account's filex
// address on it (TestAccount.Email) — the handler makes that account the
// instance's super administrator, so whoever sets this provider up cannot lock
// themselves out (auth.GrantSuperAdmin).
//
// ⚠ Nothing is written and no session is created.
func (d *Driver) Probe(ctx context.Context, cfg map[string]any, _ *http.Request) []auth.ProbeCheck {
	p := &Driver{logon: d.logon}
	if err := p.load(cfg); err != nil {
		return []auth.ProbeCheck{auth.Check("config", auth.ProbeFail, "detail", err.Error())}
	}
	if err := p.ready(); err != nil {
		return []auth.ProbeCheck{auth.Check("unsupported_os", auth.ProbeFail)}
	}
	out := []auth.ProbeCheck{auth.Check("platform", auth.ProbeOK)}

	// The first-login step, judged on the configuration as this driver reads it
	// (auto_create is OFF here unless said otherwise). The token always carries
	// groups, so allowed_groups always has something to read.
	fl := map[string]any{"allowed_groups": auth.CfgString(cfg, "allowed_groups")}
	fl["auto_create"] = p.firstLogin.AutoCreate
	out = append(out, auth.FirstLoginCheck(fl, "token"))

	ta := auth.TestAccountFrom(ctx)
	if ta == nil {
		return append(out, auth.Check("test_account", auth.ProbeFail, "reason", "missing"))
	}
	if ta.Password == "" {
		return append(out, auth.Check("test_account", auth.ProbeFail, "reason", "no_password"))
	}
	// The test account is the platform's own (it becomes its administrator).
	a, ok := parseAccount(ta.Username, p.domain, "", p.emailToken)
	if !ok {
		return append(out, auth.Check("test_account", auth.ProbeFail, "reason", identity.RefuseInvalidName))
	}
	out = append(out, auth.Check("test_account", auth.ProbeOK))
	if _, refusal := p.checkName(ctx, a); refusal != "" {
		return append(out, auth.Check("account", auth.ProbeFail, "reason", refusal))
	}

	res, err := p.signIn(ctx, a, ta.Password)
	if err != nil {
		reason, code := "other", ""
		var le *LogonError
		if errors.As(err, &le) {
			code = strconv.FormatUint(uint64(le.Code), 10)
			switch v, why := judge(le.Code); v {
			case verdictWrong:
				reason = "credentials"
			case verdictRefused:
				reason = why
			}
		}
		// ⚠ The reason and the Win32 number only: never the account's password,
		// and not the error text of a call that was given one.
		return append(out, auth.Check("logon", auth.ProbeFail, "reason", reason, "code", code))
	}
	if sidIsSystem(res.SID) {
		return append(out, auth.Check("account", auth.ProbeFail, "reason", identity.RefuseForbidden))
	}
	out = append(out, auth.Check("logon", auth.ProbeOK, "account", a.Name))
	out = append(out, auth.Check("account", auth.ProbeOK))
	ta.Email = a.Email
	return out
}
