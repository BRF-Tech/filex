package external_test

// The live external-services resolver (issue #17).
//
// The bug it replaces: the admin UI wrote `external_services` rows, the
// capability probe read them, and the code that actually USED the configuration
// read a boot-time snapshot of env/YAML. So the operator got a green Test
// button and a 503 "onlyoffice not configured" from the same instance, at the
// same moment. These tests pin the property that closes it — an answer that
// changes when the row changes, without a restart.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestGet_ReflectsARowWrittenAfterTheResolverWasBuilt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	r := external.New(store)

	// Nothing configured — and asking is not what configures it.
	require.False(t, r.Get(ctx, external.OnlyOffice).Enabled)

	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice,
		true, "https://docs.example/", "s3cr3t", "{}", time.Time{}, "ok"))
	r.Invalidate()

	got := r.Get(ctx, external.OnlyOffice)
	require.True(t, got.Enabled)
	// The trailing slash is normalised here so every consumer joins paths the
	// same way the health probe does.
	require.Equal(t, "https://docs.example", got.URL)
	require.Equal(t, "s3cr3t", got.Secret)
}

func TestGet_DisabledOrURLlessRowsReadAsNotConfigured(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	r := external.New(store)

	require.NoError(t, store.UpsertExternalService(ctx, external.Drawio,
		false, "https://draw.example", "", "{}", time.Time{}, "disabled"))
	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice,
		true, "   ", "", "{}", time.Time{}, "unconfigured"))
	r.Invalidate()

	require.False(t, r.Get(ctx, external.Drawio).Enabled, "an operator turned it off")
	require.False(t, r.Get(ctx, external.OnlyOffice).Enabled, "a blank URL is not a URL")
	require.Empty(t, r.URL(ctx, external.OnlyOffice))
}

// ⚠ A database blip must not report a configured editor as unconfigured: that
// would take a working install down for as long as the blip lasts, which is
// worse than answering with the last thing we knew.
func TestGet_AReadErrorKeepsTheLastGoodAnswer(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	flaky := &flakyStore{Store: store}
	r := external.New(flaky)

	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice,
		true, "https://docs.example", "s3cr3t", "{}", time.Time{}, "ok"))
	require.True(t, r.Get(ctx, external.OnlyOffice).Enabled)

	flaky.fail = true
	r.Invalidate()
	require.True(t, r.Get(ctx, external.OnlyOffice).Enabled,
		"the row is still there; we simply could not read it")
}

func TestNew_NilStoreIsSafeAndReportsNothingConfigured(t *testing.T) {
	r := external.New(nil)
	require.False(t, r.Get(context.Background(), external.OnlyOffice).Enabled)
	require.Empty(t, r.URL(context.Background(), external.Drawio))
	var nilResolver *external.Resolver
	require.False(t, nilResolver.Get(context.Background(), external.OnlyOffice).Enabled)
	nilResolver.Invalidate()
}

type flakyStore struct {
	db.Store
	fail bool
}

func (f *flakyStore) ListExternalServices(ctx context.Context) ([]*db.ExternalService, error) {
	if f.fail {
		return nil, errors.New("database is locked")
	}
	return f.Store.ListExternalServices(ctx)
}

// TestCallbackURL_RoundTrip: the callback address lives in options_json, and
// writing it must not cost the operator their other options.
func TestCallbackURL_RoundTrip(t *testing.T) {
	got, err := external.WithCallbackURL(`{"keep":"me"}`, "http://filex:5212/")
	require.NoError(t, err)
	require.Contains(t, got, `"keep":"me"`)
	require.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(got))

	cleared, err := external.WithCallbackURL(got, "")
	require.NoError(t, err)
	require.Equal(t, "", external.CallbackURLFromOptions(cleared))
	require.Contains(t, cleared, `"keep":"me"`)

	// A blob that is not an object is the operator's typo, not something to
	// overwrite in silence.
	_, err = external.WithCallbackURL(`["nope"]`, "http://filex:5212")
	require.Error(t, err)

	// An unreadable blob still yields a working server, just no callback URL.
	require.Equal(t, "", external.CallbackURLFromOptions(`{oops`))
}

// GitHub #143: the address filex reaches a service at, when it is not the
// row's URL (FILEX_ONLYOFFICE_INTERNAL_URL, FILEX_DRAWIO_INTERNAL_URL). URL
// stays the browser's; ServerURL is where filex's own requests go.
func TestInternalURL_ServerURLIsTheInternalAddressAndURLStaysTheBrowsers(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, external.Drawio,
		true, "https://draw.example.com/", "", "{}", time.Time{}, "ok"))
	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice,
		true, "https://office.example.com", "s3cr3t", "{}", time.Time{}, "ok"))
	r := external.New(store)

	got := r.Get(ctx, external.Drawio)
	require.Empty(t, got.InternalURL, "none given: none")
	require.Equal(t, "https://draw.example.com", got.ServerURL(), "and filex's requests go to the URL, as before")

	r.SetInternalURLs(map[string]string{external.Drawio: " http://drawio:8080/ ", external.OnlyOffice: ""})
	got = r.Get(ctx, external.Drawio)
	require.Equal(t, "https://draw.example.com", got.URL, "the browser's address")
	require.Equal(t, "http://drawio:8080", got.InternalURL)
	require.Equal(t, "http://drawio:8080", got.ServerURL())
	require.Equal(t, "https://draw.example.com", r.URL(ctx, external.Drawio), "URL is what the browser is handed")

	oo := r.Get(ctx, external.OnlyOffice)
	require.Empty(t, oo.InternalURL, "a blank value is no address")
	require.Equal(t, "https://office.example.com", oo.ServerURL())

	// Switched off is off, whatever the variable says.
	require.NoError(t, store.UpsertExternalService(ctx, external.Drawio,
		false, "https://draw.example.com", "", "{}", time.Time{}, "disabled"))
	r.Invalidate()
	require.False(t, r.Get(ctx, external.Drawio).Enabled)
}
