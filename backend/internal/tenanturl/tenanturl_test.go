package tenanturl_test

// Unit coverage for the origin resolver every absolute-URL builder shares.
// The handler-level proof (share links, e-mails, the wss:// endpoint) lives in
// internal/api/handlers/tenant_urls_test.go; this file pins the rule itself,
// especially the cases a forged Host header can reach.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

const (
	operator = "https://operator.test"
	host     = "files.tenant-a.test"
	origin   = "https://files.tenant-a.test"
)

// fakeStore is the provider slice of db.Store, backed by maps.
type fakeStore struct {
	byHost      map[string]*model.Provider
	byID        map[int64]*model.Provider
	storage     map[int64]int64
	err         error
	hostLookups int
}

func (f *fakeStore) GetProviderByHost(_ context.Context, h string) (*model.Provider, error) {
	f.hostLookups++
	if f.err != nil {
		return nil, f.err
	}
	return f.byHost[h], nil // nil, nil = no such tenant (the store's contract)
}

func (f *fakeStore) GetProvider(_ context.Context, id int64) (*model.Provider, error) {
	if f.err != nil {
		return nil, f.err
	}
	p, ok := f.byID[id]
	if !ok {
		return nil, errors.New("no such provider")
	}
	return p, nil
}

func (f *fakeStore) GetProviderIDForStorage(_ context.Context, id int64) (int64, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	pid, ok := f.storage[id]
	return pid, ok, nil
}

func newStore() *fakeStore {
	p := &model.Provider{ID: 7, Slug: "tenant-a", Host: host, Enabled: true}
	return &fakeStore{
		byHost:  map[string]*model.Provider{host: p},
		byID:    map[int64]*model.Provider{7: p},
		storage: map[int64]int64{42: 7},
	}
}

func req(h string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	r.Host = h
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestFromRequest_SingleTenantNeverReadsTheRequest(t *testing.T) {
	st := newStore()
	rv := tenanturl.New(st, operator, false)

	assert.Equal(t, operator, rv.FromRequest(req(host, nil)))
	assert.Equal(t, operator, rv.FromRequest(req("anything.example", nil)))
	assert.Zero(t, st.hostLookups,
		"single-tenant must not even ask the store — that is what keeps the majority of installs unchanged")
}

func TestFromRequest_MultiTenantKnownHost(t *testing.T) {
	rv := tenanturl.New(newStore(), operator, true)
	assert.Equal(t, origin, rv.FromRequest(req(host, nil)))
	assert.Equal(t, origin, rv.FromRequest(req(host+":8443", nil)),
		"a dialled port is not part of the tenant's public origin")
	assert.Equal(t, origin, rv.FromRequest(req("FILES.Tenant-A.TEST", nil)),
		"Host is case-insensitive")
}

// The host-header injection case: everything an attacker can put in Host must
// come back as the operator's own URL, never as their domain.
func TestFromRequest_ForgedHostFallsBackToPublicURL(t *testing.T) {
	st := newStore()
	rv := tenanturl.New(st, operator, true)

	for _, h := range []string{
		"evil.example",
		"files.tenant-a.test.evil.example", // suffix trick
		"evil.example:443",
		"", // no Host at all
	} {
		got := rv.FromRequest(req(h, nil))
		assert.Equal(t, operator, got, "Host %q", h)
		assert.NotContains(t, got, "evil.example")
	}

	// A provider row that exists but is switched off is not a tenant either —
	// GetProviderByHost filters on enabled, so it simply does not resolve.
	st.byHost["off.example"] = nil
	assert.Equal(t, operator, rv.FromRequest(req("off.example", nil)))

	// A store that errors must fail closed to PublicURL, not to the request.
	broken := newStore()
	broken.err = errors.New("db down")
	assert.Equal(t, operator, tenanturl.New(broken, operator, true).FromRequest(req(host, nil)))
}

// The origin is assembled from the provider ROW's host column, so even if the
// lookup were ever loosened the minted URL still names a provisioned host.
func TestFromRequest_OriginComesFromTheProviderRow(t *testing.T) {
	st := newStore()
	// A store that matches loosely and answers with the canonical row.
	st.byHost["whatever.example"] = st.byID[7]
	rv := tenanturl.New(st, operator, true)
	assert.Equal(t, origin, rv.FromRequest(req("whatever.example", nil)),
		"the row's host wins over the string the client sent")
}

func TestScheme(t *testing.T) {
	rv := tenanturl.New(newStore(), operator, true)
	assert.Equal(t, "https://"+host, rv.FromRequest(req(host, nil)))
	assert.Equal(t, "http://"+host, rv.FromRequest(req(host, map[string]string{"X-Forwarded-Proto": "http"})),
		"the trusted proxy may declare a TLS-less setup")
	assert.Equal(t, "https://"+host, rv.FromRequest(req(host, map[string]string{"X-Forwarded-Proto": "https"})))

	// A TLS-less install (dev / compose without a proxy) keeps http.
	dev := tenanturl.New(newStore(), "http://localhost:5212", true)
	assert.Equal(t, "http://"+host, dev.FromRequest(req(host, nil)))
	assert.Equal(t, "http://localhost:5212", dev.FromRequest(req("evil.example", nil)))
}

// ForStorage / ForProvider: the request-less path, used where an e-mail or an
// MCP call is built from a context.
func TestForStorageAndProvider(t *testing.T) {
	st := newStore()
	rv := tenanturl.New(st, operator, true)
	ctx := context.Background()

	assert.Equal(t, origin, rv.ForStorage(ctx, 42))
	assert.Equal(t, origin, rv.ForProvider(ctx, 7))

	assert.Equal(t, operator, rv.ForStorage(ctx, 99), "a storage linked to no tenant is the operator's")
	assert.Equal(t, operator, rv.ForStorage(ctx, 0))
	assert.Equal(t, operator, rv.ForProvider(ctx, 0))
	assert.Equal(t, operator, rv.ForProvider(ctx, 1234), "unknown provider id")

	// Disabled tenant → PublicURL. A link into a switched-off tenant's host is
	// worse than a link into the operator's: nothing is listening there.
	st.byID[7].Enabled = false
	assert.Equal(t, operator, rv.ForStorage(ctx, 42))
	st.byID[7].Enabled = true

	// Single-tenant ignores the data too.
	single := tenanturl.New(st, operator, false)
	assert.Equal(t, operator, single.ForStorage(ctx, 42))
	assert.Equal(t, operator, single.ForProvider(ctx, 7))
}

func TestZeroValueAndNilStore(t *testing.T) {
	var zero tenanturl.Resolver
	// The zero value yields "", so a caller concatenating gets the relative
	// "/s/<token>" filex produced before this package existed.
	assert.Equal(t, "", zero.FromRequest(req(host, nil)))
	assert.Equal(t, "", zero.ForStorage(context.Background(), 42))
	assert.Equal(t, "", zero.FromRequest(nil))

	nilStore := tenanturl.Resolver{PublicURL: operator, MultiTenant: true}
	assert.Equal(t, operator, nilStore.FromRequest(req(host, nil)))
	assert.Equal(t, operator, nilStore.ForStorage(context.Background(), 42))
}

func TestNewTrimsPublicURL(t *testing.T) {
	rv := tenanturl.New(nil, "  https://operator.test/  ", false)
	assert.Equal(t, operator, rv.FromRequest(req(host, nil)))
	assert.Equal(t, operator, rv.Fallback())
}

func TestRequestHost(t *testing.T) {
	require.Equal(t, "example.test", tenanturl.RequestHost(req("Example.Test:8080", nil)))
	require.Equal(t, "example.test", tenanturl.RequestHost(req("example.test", nil)))
	require.Equal(t, "", tenanturl.RequestHost(nil))
}
