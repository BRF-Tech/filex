package ldap

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
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
//	people, groups / groups_search, sync_groups — what sign-in and directory
//	           sync will find there (probeDirectory)
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
	out = append(out, auth.FirstLoginCheck(cfg, p.groupAttr))

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
	out = append(out, auth.Check("base", auth.ProbeOK, "dn", p.baseDN))
	return append(out, p.probeDirectory(c)...)
}

// probeCount is how many entries a provider test counts at most: enough to
// tell "none" from "some" from "many" without reading a whole directory
// while an administrator waits.
const probeCount = 1000

// countOf is a count as a test says it: "1000+" past probeCount.
func countOf(n int, more bool) string {
	if more {
		return strconv.Itoa(n) + "+"
	}
	return strconv.Itoa(n)
}

// limited runs a search capped at probeCount, reading a size-limit answer
// as "at least that many". A directory that ignores the cap (lldap) answers
// everything: that count is exact, and said without the "+".
func limited(c conn, req *ldap.SearchRequest) ([]*ldap.Entry, bool, error) {
	req.SizeLimit = probeCount
	res, err := c.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) && res != nil {
			return res.Entries, true, nil
		}
		return nil, false, err
	}
	return res.Entries, false, nil
}

// probeDirectory is the rest of a test, after the base DN: what sign-in and
// directory sync will find there —
//
//	people     the people the user filter lists (sync_filter), and how many
//	           have an e-mail (email_attr) — nobody is a failure
//	groups     whether their groups can be read: listed in group_attr on the
//	           people found, or found by group_filter for the first of them
//	sync_groups how many groups directory sync would bring in
//	           (sync_groups, sync_group_filter)
//
// A directory with no groups is said ("unchecked"), not failed: groups are
// optional.
func (p *Driver) probeDirectory(c conn) []auth.ProbeCheck {
	var out []auth.ProbeCheck
	attrs := []string{p.emailAttr}
	if p.groupFilter == "" {
		attrs = append(attrs, p.groupAttr)
	}
	filter := p.syncFilter()
	people, more, err := limited(c, ldap.NewSearchRequest(p.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0,
		int(probeTimeout.Seconds()), false, filter, attrs, nil))
	if err != nil {
		return append(out, auth.Check("people", auth.ProbeFail, "filter", filter, "reason", "other", "detail", err.Error()))
	}
	withMail, withGroups := 0, 0
	var first *ldap.Entry
	for _, e := range people {
		if e.GetAttributeValue(p.emailAttr) != "" {
			withMail++
			if first == nil {
				first = e
			}
		}
		if p.groupFilter == "" && len(e.GetAttributeValues(p.groupAttr)) > 0 {
			withGroups++
		}
	}
	if withMail == 0 {
		return append(out, auth.Check("people", auth.ProbeFail, "filter", filter, "attr", p.emailAttr,
			"n", countOf(len(people), more), "reason", "no_people"))
	}
	out = append(out, auth.Check("people", auth.ProbeOK, "filter", filter, "attr", p.emailAttr,
		"n", countOf(len(people), more), "mail", countOf(withMail, more)))

	if p.groupFilter == "" {
		if withGroups == 0 {
			out = append(out, auth.Check("groups", auth.ProbeUnchecked, "attr", p.groupAttr))
		} else {
			out = append(out, auth.Check("groups", auth.ProbeOK, "attr", p.groupAttr,
				"n", countOf(withGroups, more), "of", countOf(len(people), more)))
		}
	} else {
		dns, err := p.linkGroupsOf(c, first, first.GetAttributeValue(p.emailAttr))
		switch {
		case err != nil:
			out = append(out, auth.Check("groups_search", auth.ProbeFail, "filter", p.groupFilter, "reason", "other", "detail", err.Error()))
		case len(dns) == 0:
			out = append(out, auth.Check("groups_search", auth.ProbeUnchecked, "filter", p.groupFilter, "who", first.GetAttributeValue(p.emailAttr)))
		default:
			// groupsOf gives each group as its DN and its name: half are groups.
			out = append(out, auth.Check("groups_search", auth.ProbeOK, "filter", p.groupFilter,
				"who", first.GetAttributeValue(p.emailAttr), "n", strconv.Itoa((len(dns)+1)/2)))
		}
	}

	if p.importGroupsOn {
		base := p.groupBaseDN
		if base == "" {
			base = p.baseDN
		}
		groups, more, err := limited(c, ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0,
			int(probeTimeout.Seconds()), false, p.syncGroupFilter(), []string{"1.1"}, nil))
		switch {
		case err != nil:
			out = append(out, auth.Check("sync_groups", auth.ProbeFail, "filter", p.syncGroupFilter(), "reason", "other", "detail", err.Error()))
		case len(groups) == 0:
			out = append(out, auth.Check("sync_groups", auth.ProbeUnchecked, "filter", p.syncGroupFilter()))
		default:
			out = append(out, auth.Check("sync_groups", auth.ProbeOK, "n", countOf(len(groups), more)))
		}
	}
	return out
}

// probeTimeout bounds each step of a provider test: an administrator is
// waiting on the button.
const probeTimeout = 10 * time.Second
