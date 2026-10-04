package ldap

import (
	"context"
	"errors"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// The provider test used to be a stub that answered OK for anything — an
// LDAP entry with empty required fields got "Provider OK" (release-candidate
// sweep, 2026-09-21). Each case below is one way a configuration is wrong,
// and the test must stop at it and name it.

func probeWith(fc *fakeConn, dialErr error, cfg map[string]any) []auth.ProbeCheck {
	d := &Driver{dial: func(context.Context) (conn, error) {
		if dialErr != nil {
			return nil, dialErr
		}
		return fc, nil
	}}
	return d.Probe(context.Background(), cfg, nil)
}

func last(checks []auth.ProbeCheck) auth.ProbeCheck { return checks[len(checks)-1] }

func TestProbe_EmptyRequiredFieldsFailAndAreNamed(t *testing.T) {
	checks := probeWith(&fakeConn{}, nil, map[string]any{"url": "", "base_dn": "  "})
	assert.Len(t, checks, 1, "nothing is dialled when the address is missing")
	assert.Equal(t, "required", checks[0].ID)
	assert.Equal(t, auth.ProbeFail, checks[0].Status)
	assert.Equal(t, "url,base_dn", checks[0].Params["fields"])
	assert.False(t, auth.ProbeOKAll(checks))
}

func TestProbe_AnAddressThatIsNotLDAPFails(t *testing.T) {
	c := last(probeWith(&fakeConn{}, nil, map[string]any{"url": "https://dc.example.com", "base_dn": "dc=x"}))
	assert.Equal(t, "address", c.ID)
	assert.Equal(t, auth.ProbeFail, c.Status)
}

func TestProbe_ADirectoryThatDoesNotAnswerFails(t *testing.T) {
	c := last(probeWith(nil, errors.New("dial tcp 10.0.0.9:636: connect: connection refused"),
		map[string]any{"url": "ldaps://10.0.0.9:636", "base_dn": "dc=x"}))
	assert.Equal(t, "connect", c.ID)
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "refused", c.Params["reason"])
	assert.Equal(t, "10.0.0.9:636", c.Params["host"])
}

func TestProbe_AWrongServicePasswordFailsAsCredentials(t *testing.T) {
	fc := &fakeConn{servicePassword: "right"}
	checks := probeWith(fc, nil, map[string]any{
		"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com",
		"bind_dn": "cn=svc,dc=example,dc=com", "bind_password": "wrong",
	})
	c := last(checks)
	assert.Equal(t, "bind", c.ID)
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "credentials", c.Params["reason"])
	assert.True(t, fc.closed, "the connection is closed on the way out")
}

func TestProbe_AMissingBaseDNFails(t *testing.T) {
	fc := &fakeConn{searchErr: goldap.NewError(goldap.LDAPResultNoSuchObject, errors.New("no such object"))}
	c := last(probeWith(fc, nil, map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=nope"}))
	assert.Equal(t, "base", c.ID)
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "no_such_object", c.Params["reason"])
}

func TestProbe_AWorkingConfigurationPassesStepByStep(t *testing.T) {
	fc := &fakeConn{
		servicePassword: "right",
		entries: []*goldap.Entry{
			goldap.NewEntry("cn=ayse,dc=example,dc=com", map[string][]string{"mail": {"ayse@example.com"}, "memberOf": {"cn=staff,dc=example,dc=com"}}),
			goldap.NewEntry("cn=printer,dc=example,dc=com", nil),
		},
		groups: []*goldap.Entry{goldap.NewEntry("cn=staff,dc=example,dc=com", nil)},
	}
	checks := probeWith(fc, nil, map[string]any{
		"url": "ldap://dc.example.com", "base_dn": "dc=example,dc=com", "start_tls": "true",
		"bind_dn": "cn=svc,dc=example,dc=com", "bind_password": "right", "group_attr": "memberOf",
	})
	var ids []string
	for _, c := range checks {
		assert.Equal(t, auth.ProbeOK, c.Status, c.ID)
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []string{"required", "first_login_open", "connect", "starttls", "bind", "base", "people", "groups", "sync_groups"}, ids)
	people := checks[6]
	assert.Equal(t, "2", people.Params["n"])
	assert.Equal(t, "1", people.Params["mail"], "one of the two has an e-mail")
	assert.Equal(t, "(mail=*)", people.Params["filter"], "the sync filter: user_filter with *")
	assert.Equal(t, "1", checks[7].Params["n"], "one lists its groups in memberOf")
	assert.Equal(t, "1", checks[8].Params["n"], "one group would be brought in")
	assert.True(t, auth.ProbeOKAll(checks))
	// StartTLS must carry the server name, or tls.Client refuses the
	// handshake whenever no ca_file is set (the bug the test turned up).
	if assert.NotNil(t, fc.startTLSConfig) {
		assert.Equal(t, "dc.example.com", fc.startTLSConfig.ServerName)
	}
	// The base read is a base-scope read of the base DN; then the people and
	// the groups, each capped (a test does not read a whole directory).
	if assert.Len(t, fc.searches, 3) {
		assert.Equal(t, probeCount, fc.searches[1].SizeLimit)
		assert.Equal(t, "dc=example,dc=com", fc.searches[0].BaseDN)
		assert.Equal(t, goldap.ScopeBaseObject, fc.searches[0].Scope)
	}
}

// With no service account the bind is anonymous, and the result says so
// rather than claiming a bind that did not happen.
func TestProbe_NoServiceAccountIsAnonymousAndSaidToBe(t *testing.T) {
	fc := &fakeConn{}
	checks := probeWith(fc, nil, map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com"})
	assert.Equal(t, "bind_anonymous", checks[3].ID)
	assert.Empty(t, fc.binds)
}

// A login over ldap:// with StartTLS hands the TLS layer the server name to
// verify: without it (no ca_file → tlsConfig() is nil) tls.Client refused
// every handshake, so StartTLS never worked — found by the provider test.
func TestLogin_StartTLSCarriesTheServerName(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, map[string]any{"url": "ldap://dc.example.com", "start_tls": true})
	_, _, err := d.Login(context.Background(), "ayse@example.com", "pw")
	if !assert.NoError(t, err) {
		return
	}
	assert.True(t, fc.startTLSCalled)
	if assert.NotNil(t, fc.startTLSConfig) {
		assert.Equal(t, "dc.example.com", fc.startTLSConfig.ServerName)
	}
}

// Nobody with an e-mail under the base: a failure, said with the filter.
func TestProbe_NobodyFoundFails(t *testing.T) {
	fc := &fakeConn{entries: []*goldap.Entry{goldap.NewEntry("cn=printer,dc=example,dc=com", nil)}}
	c := last(probeWith(fc, nil, map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com", "user_filter": "(uid=%s)"}))
	assert.Equal(t, "people", c.ID)
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "(uid=*)", c.Params["filter"])
	assert.Equal(t, "no_people", c.Params["reason"])
}

// Nobody lists groups in memberOf, and no group to bring in: said, not
// failed — groups are optional.
func TestProbe_NoGroupsIsSaidNotFailed(t *testing.T) {
	fc := &fakeConn{entries: []*goldap.Entry{goldap.NewEntry("cn=ayse,dc=example,dc=com", map[string][]string{"mail": {"ayse@example.com"}})}}
	checks := probeWith(fc, nil, map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com"})
	byID := map[string]string{}
	for _, c := range checks {
		byID[c.ID] = c.Status
	}
	assert.Equal(t, auth.ProbeUnchecked, byID["groups"])
	assert.Equal(t, auth.ProbeUnchecked, byID["sync_groups"])
	assert.True(t, auth.ProbeOKAll(checks), "no failure: groups are optional")
}

// With group_filter, the groups of the first person found are searched for.
func TestProbe_GroupSearchIsTriedOnTheFirstPerson(t *testing.T) {
	fc := &fakeConn{
		entries: []*goldap.Entry{goldap.NewEntry("cn=ayse,dc=example,dc=com", map[string][]string{"mail": {"ayse@example.com"}})},
		groups:  []*goldap.Entry{goldap.NewEntry("cn=staff,dc=example,dc=com", nil), goldap.NewEntry("cn=ops,dc=example,dc=com", nil)},
	}
	checks := probeWith(fc, nil, map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com",
		"group_filter": "(&(objectClass=groupOfNames)(member=%s))", "sync_groups": "false"})
	c := last(checks)
	assert.Equal(t, "groups_search", c.ID)
	assert.Equal(t, auth.ProbeOK, c.Status)
	assert.Equal(t, "ayse@example.com", c.Params["who"])
	assert.Equal(t, "2", c.Params["n"])
}

// Past the cap: "1000+" when the directory says it stopped there; the exact
// count when it answered everything regardless (lldap does).
func TestProbe_CountsSayPlusOnlyWhenTheDirectoryStopped(t *testing.T) {
	many := make([]*goldap.Entry, 0, probeCount+6)
	for i := 0; i < probeCount+6; i++ {
		many = append(many, goldap.NewEntry("cn=p,dc=example,dc=com", map[string][]string{"mail": {"p@example.com"}}))
	}
	cfg := map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com", "sync_groups": "false"}

	people := func(checks []auth.ProbeCheck) auth.ProbeCheck {
		for _, c := range checks {
			if c.ID == "people" {
				return c
			}
		}
		return auth.ProbeCheck{}
	}
	assert.Equal(t, "1006", people(probeWith(&fakeConn{entries: many}, nil, cfg)).Params["n"])

	stopped := &capConn{fakeConn: &fakeConn{}, entries: many[:probeCount]}
	c := people(probeWithConn(stopped, cfg))
	assert.Equal(t, "1000+", c.Params["n"])
}

// capConn stops a subtree search at the cap, the way a directory that honours
// the size limit does: the entries so far, and "size limit exceeded".
type capConn struct {
	*fakeConn
	entries []*goldap.Entry
}

func (c *capConn) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	if req.Scope != goldap.ScopeWholeSubtree {
		return c.fakeConn.Search(req)
	}
	return &goldap.SearchResult{Entries: c.entries}, goldap.NewError(goldap.LDAPResultSizeLimitExceeded, errors.New("size limit"))
}

func probeWithConn(c conn, cfg map[string]any) []auth.ProbeCheck {
	d := &Driver{dial: func(context.Context) (conn, error) { return c, nil }}
	return d.Probe(context.Background(), cfg, nil)
}
