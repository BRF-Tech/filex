package ldap

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// Probe tests an LDAP configuration the way a sign-in would use it, stopping
// at the first step that fails:
//
//	required   the address and the base DN are filled in
//	address    the address is ldap:// or ldaps://
//	connect    the directory answers at that address (TLS verified for ldaps)
//	starttls   StartTLS succeeds, when it is switched on for ldap://
//	bind       the service account binds with its password — or, with no
//	           service account, the bind is anonymous and said to be
//	base       the base DN exists and is readable by that bind
//
// ⚠ Nothing is written and nobody signs in: this is the service bind and one
// base-scope read, which is what every login starts with. What it cannot
// check — that a particular person's filter matches — is left to a login.
func (d *Driver) Probe(ctx context.Context, cfg map[string]any, _ *http.Request) []auth.ProbeCheck {
	var missing []string
	for _, k := range []string{"url", "base_dn"} {
		if auth.CfgString(cfg, k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return []auth.ProbeCheck{auth.Check("required", auth.ProbeFail, "fields", strings.Join(missing, ","))}
	}
	out := []auth.ProbeCheck{auth.Check("required", auth.ProbeOK)}

	raw := auth.CfgString(cfg, "url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" {
		return append(out, auth.Check("address", auth.ProbeFail, "url", raw))
	}
	host := u.Host

	p := &Driver{dial: d.dial}
	if err := p.load(cfg); err != nil {
		return append(out, auth.Check("address", auth.ProbeFail, "url", raw, "detail", err.Error()))
	}

	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c, err := p.connect(cctx)
	if err != nil {
		return append(out, auth.Check("connect", auth.ProbeFail, "host", host, "reason", auth.NetReason(err), "detail", err.Error()))
	}
	defer c.Close()
	out = append(out, auth.Check("connect", auth.ProbeOK, "host", host))

	if p.startTLS {
		tc, err := p.startTLSConfig()
		if err == nil {
			err = c.StartTLS(tc)
		}
		if err != nil {
			return append(out, auth.Check("starttls", auth.ProbeFail, "host", host, "reason", auth.NetReason(err), "detail", err.Error()))
		}
		out = append(out, auth.Check("starttls", auth.ProbeOK, "host", host))
	}

	if p.bindDN != "" {
		if err := c.Bind(p.bindDN, p.bindPass); err != nil {
			reason := "other"
			if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
				reason = "credentials"
			}
			return append(out, auth.Check("bind", auth.ProbeFail, "dn", p.bindDN, "reason", reason, "detail", err.Error()))
		}
		out = append(out, auth.Check("bind", auth.ProbeOK, "dn", p.bindDN))
	} else {
		out = append(out, auth.Check("bind_anonymous", auth.ProbeOK))
	}

	req := ldap.NewSearchRequest(p.baseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, int(probeTimeout.Seconds()), false,
		"(objectClass=*)", []string{"1.1"}, nil)
	if _, err := c.Search(req); err != nil {
		reason := "other"
		switch {
		case ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject):
			reason = "no_such_object"
		case ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights):
			reason = "access"
		}
		return append(out, auth.Check("base", auth.ProbeFail, "dn", p.baseDN, "reason", reason, "detail", err.Error()))
	}
	return append(out, auth.Check("base", auth.ProbeOK, "dn", p.baseDN))
}

// probeTimeout bounds each step of a provider test: an administrator is
// waiting on the button.
const probeTimeout = 10 * time.Second
