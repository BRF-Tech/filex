package tenantdomain_test

// A tenant's own domain (docs/TENANT-ADMIN.md): proven by a CNAME to the
// tenant's platform subdomain, one tenant at a time, suspended when the CNAME
// goes and back when it returns, a failure to ask the DNS changing nothing.

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// fakeDNS answers LookupCNAME from a table; an entry that is an error is
// returned as one.
type fakeDNS struct {
	mu sync.Mutex
	m  map[string]any
}

func (f *fakeDNS) set(host string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[host] = v
}

func (f *fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch v := f.m[host].(type) {
	case string:
		return v + ".", nil
	case error:
		return "", v
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func notFound(host string) error {
	return &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func setup(t *testing.T) (db.Store, *tenantdomain.Service, *fakeDNS, *model.Provider, *model.Provider, *[]string) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	db.SetTenantDomain("tenants.files.example")
	t.Cleanup(func() { db.SetTenantDomain("") })
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true, Host: "files.beta.example"})
	require.NoError(t, err)
	dns := &fakeDNS{m: map[string]any{
		// The platform's wildcard: address records, so a subdomain is its own
		// canonical name.
		"acme.tenants.files.example": "acme.tenants.files.example",
		"beta.tenants.files.example": "beta.tenants.files.example",
	}}
	var told []string
	svc := &tenantdomain.Service{Store: store, Resolver: dns, PlatformHosts: []string{"files.example"},
		Notify: func(_ context.Context, p *model.Provider, d *model.ProviderDomain, status string) {
			told = append(told, p.Slug+":"+d.Domain+":"+status)
		}}
	return store, svc, dns, acme, beta, &told
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Files.Acme.Example.": "files.acme.example",
		" files.acme.example": "files.acme.example",
		"dosya.örnek.com.tr":  "dosya.xn--rnek-4qa.com.tr",
	} {
		got, err := tenantdomain.Normalize(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "localhost", "10.0.0.1", "::1", "a_b.example", "-a.example", "a..example", "acme.example/x", "user@acme.example"} {
		_, err := tenantdomain.Normalize(bad)
		assert.ErrorIs(t, err, tenantdomain.ErrInvalid, bad)
	}
}

func TestAdd_OneTenantAtATimeAndNeverThePlatformsNames(t *testing.T) {
	_, svc, _, acme, beta, _ := setup(t)
	ctx := context.Background()

	d, err := svc.Add(ctx, acme, "Files.Acme.Example", nil)
	require.NoError(t, err)
	assert.Equal(t, "files.acme.example", d.Domain)
	assert.Equal(t, model.DomainPending, d.Status)

	_, err = svc.Add(ctx, beta, "files.acme.example", nil)
	assert.ErrorIs(t, err, tenantdomain.ErrTaken, "another tenant cannot take a domain a tenant has, whatever its state")
	_, err = svc.Add(ctx, acme, "files.acme.example", nil)
	assert.ErrorIs(t, err, tenantdomain.ErrTaken)

	for _, reserved := range []string{"files.example", "beta.tenants.files.example", "x.tenants.files.example", "files.beta.example"} {
		_, err = svc.Add(ctx, acme, reserved, nil)
		assert.ErrorIs(t, err, tenantdomain.ErrReserved, reserved)
	}

	db.SetTenantDomain("")
	_, err = svc.Add(ctx, acme, "other.acme.example", nil)
	assert.ErrorIs(t, err, tenantdomain.ErrNoTenantDomain, "without platform subdomains there is nothing to point at")
}

func TestCheck_TheCNAMEProvesAndKeepsProving(t *testing.T) {
	store, svc, dns, acme, _, told := setup(t)
	ctx := context.Background()
	d, err := svc.Add(ctx, acme, "files.acme.example", nil)
	require.NoError(t, err)

	// No record yet: pending, and it does not route.
	d, changed, err := svc.Check(ctx, d)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, model.DomainPending, d.Status)
	assert.Contains(t, d.LastError, "does not resolve")
	assert.Equal(t, model.DomainWhyNoRecord, d.LastErrorCode, "a screen says it in its own words")
	assert.Equal(t, map[string]string{"domain": "files.acme.example"}, d.LastErrorParams)

	// Pointed at another tenant's subdomain: still not acme's.
	dns.set("files.acme.example", "beta.tenants.files.example")
	d, _, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, model.DomainPending, d.Status)
	assert.Contains(t, d.LastError, "points at beta.tenants.files.example")
	assert.Equal(t, model.DomainWhyPointsElsewhere, d.LastErrorCode)
	assert.Equal(t, map[string]string{"found": "beta.tenants.files.example", "target": "acme.tenants.files.example"}, d.LastErrorParams)

	// Pointed at acme's: active, and it routes to acme.
	dns.set("files.acme.example", "acme.tenants.files.example")
	d, changed, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, model.DomainActive, d.Status)
	p, err := store.GetProviderByHost(ctx, "files.acme.example")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, acme.ID, p.ID)
	assert.Empty(t, *told, "becoming active the first time is the tenant's own doing: nobody is told")

	// A resolver's bad minute changes nothing.
	dns.set("files.acme.example", errors.New("i/o timeout"))
	d, changed, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, model.DomainActive, d.Status, "a failure to ask must not take a tenant's address away")

	// The record gone: suspended, the row kept, the tenant told.
	dns.set("files.acme.example", notFound("files.acme.example"))
	d, changed, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, model.DomainSuspended, d.Status)
	p, err = store.GetProviderByHost(ctx, "files.acme.example")
	require.NoError(t, err)
	assert.Nil(t, p, "a suspended domain stops routing")
	assert.Equal(t, []string{"acme:files.acme.example:suspended"}, *told)

	// Back: active again, and told.
	dns.set("files.acme.example", "acme.tenants.files.example")
	_, changed, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"acme:files.acme.example:suspended", "acme:files.acme.example:active"}, *told)

	checked, changedN, err := svc.Sweep(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, checked)
	assert.Equal(t, 0, changedN)
}

// The platform's wildcard must be address records: a canonical name that runs
// on to the platform's host names no tenant, and the screen says why.
func TestCheck_AWildcardCNAMEIsExplained(t *testing.T) {
	_, svc, dns, acme, _, _ := setup(t)
	ctx := context.Background()
	dns.set("acme.tenants.files.example", "files.example")
	dns.set("files.acme.example", "files.example")
	d, err := svc.Add(ctx, acme, "files.acme.example", nil)
	require.NoError(t, err)
	d, _, err = svc.Check(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, model.DomainPending, d.Status)
	assert.Contains(t, d.LastError, "must be address records")
	assert.Equal(t, model.DomainWhyWildcardCNAME, d.LastErrorCode)
	assert.Equal(t, "tenants.files.example", d.LastErrorParams["tenant_domain"])
}

func TestCheck_ASuspendedTenantsDomainDoesNotRoute(t *testing.T) {
	store, svc, dns, acme, _, _ := setup(t)
	ctx := context.Background()
	dns.set("files.acme.example", "acme.tenants.files.example")
	d, err := svc.Add(ctx, acme, "files.acme.example", nil)
	require.NoError(t, err)
	_, _, err = svc.Check(ctx, d)
	require.NoError(t, err)
	acme.Enabled = false
	require.NoError(t, store.UpdateProvider(ctx, acme))
	p, err := store.GetProviderByHost(ctx, "files.acme.example")
	require.NoError(t, err)
	assert.Nil(t, p)
	assert.False(t, tenantdomain.Serves(ctx, store, "files.acme.example"), "nor is it certified")
}

// TestCheck_AFailureToAskIsSaidAsSuch: an answer that is not one (a timeout,
// a "lame referral" - what pebble-challtestsrv's empty answer is to Go's
// resolver, measured in e2e/realenv) leaves a pending domain pending and says
// the DNS could not be asked, with what the resolver said; it is not "no
// record".
func TestCheck_AFailureToAskIsSaidAsSuch(t *testing.T) {
	_, svc, dns, acme, _, _ := setup(t)
	ctx := context.Background()
	d, err := svc.Add(ctx, acme, "files.acme.example", nil)
	require.NoError(t, err)
	dns.set("files.acme.example", &net.DNSError{Err: "lame referral", Name: "files.acme.example"})
	d, changed, err := svc.Check(ctx, d)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, model.DomainPending, d.Status)
	assert.Equal(t, model.DomainWhyDNSFailed, d.LastErrorCode)
	assert.Contains(t, d.LastErrorParams["detail"], "lame referral")
}
