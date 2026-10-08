package cloud_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cloud"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A tenant's plan changes in one place: a Stripe event whose signature
// verifies. A signup starts on the catalogue's first plan nobody pays for,
// whatever it asks for, and a checkout only opens a payment, with return
// addresses the server builds.

const billingPlans = `[
	{"id":"free","name":"Free","limits":{"storage_bytes":1024,"max_users":3}},
	{"id":"pro","name":"Pro","price_monthly":"9.90 EUR","stripe_price_id":"price_pro","limits":{"storage_bytes":1048576,"max_users":25}}
]`

func billingService(t *testing.T) (*cloud.Service, db.Store, int64) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	svc := cloud.New(store, nil, billingPlans, "filex.test")
	require.Empty(t, svc.PlansErr())
	res, err := svc.Signup(context.Background(), cloud.SignupRequest{Email: "owner@acme.test", Slug: "acme"})
	require.NoError(t, err)
	return svc, store, res.ProviderID
}

func TestSignup_StartsOnTheSignupPlanWhateverItAsks(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	svc := cloud.New(store, nil, billingPlans, "")

	// A body that names the paid plan: the JSON a client sends still decodes,
	// and the plan in it is not the one the tenant gets.
	var req cloud.SignupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"email":"a@acme.test","slug":"paid-ask","plan":"pro"}`), &req))
	res, err := svc.Signup(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "free", res.Plan)
	plan, limits, ref, err := store.GetProviderPlan(context.Background(), res.ProviderID)
	require.NoError(t, err)
	assert.Equal(t, "free", plan, "a signup was given the plan it asked for")
	assert.Contains(t, limits, `"storage_bytes":1024`)
	assert.Empty(t, ref)
}

func TestParsePlans_ACatalogueNeedsAPlanASignupCanStartOn(t *testing.T) {
	_, err := cloud.ParsePlans(`[{"id":"pro","name":"Pro","stripe_price_id":"price_pro"}]`)
	assert.Error(t, err, "a catalogue of paid plans only has nothing for a signup to start on")
}

// stub answers Stripe's checkout endpoint and keeps the form it was sent.
type stub struct{ form url.Values }

func (s *stub) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	s.form, _ = url.ParseQuery(string(b))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"url":"https://checkout.stripe.test/c/1"}`))),
		Request:    r,
	}, nil
}

func TestCheckout_TheReturnAddressesAreTheServers(t *testing.T) {
	svc, _, id := billingService(t)
	st := &stub{}
	sc := &cloud.StripeClient{Secret: "sk_test_x", HTTP: &http.Client{Transport: st}}
	origin := func(_ context.Context, pid int64) string {
		if pid == id {
			return "https://acme.filex.test"
		}
		return "https://wrong.example"
	}

	u, err := svc.Checkout(context.Background(), sc, "acme", "pro", origin)
	require.NoError(t, err)
	assert.Equal(t, "https://checkout.stripe.test/c/1", u)
	assert.Equal(t, "https://acme.filex.test/?billing=done&session_id={CHECKOUT_SESSION_ID}", st.form.Get("success_url"))
	assert.Equal(t, "https://acme.filex.test/?billing=cancelled", st.form.Get("cancel_url"))
	assert.Equal(t, strconv.FormatInt(id, 10), st.form.Get("client_reference_id"))
	assert.Equal(t, "pro", st.form.Get("metadata[plan]"))
	assert.Equal(t, "price_pro", st.form.Get("line_items[0][price]"))

	_, err = svc.Checkout(context.Background(), sc, "acme", "free", origin)
	assert.ErrorIs(t, err, cloud.ErrInvalid, "a plan nobody pays for cannot be bought")
	_, err = svc.Checkout(context.Background(), sc, "nobody", "pro", origin)
	assert.ErrorIs(t, err, cloud.ErrInvalid, "an unknown tenant")
}

func sign(secret string, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	const secret = "whsec_test"
	now := time.Now()
	body := []byte(`{"type":"checkout.session.completed"}`)

	assert.NoError(t, cloud.VerifyWebhookSignature(body, sign(secret, now, body), secret, now))
	assert.ErrorIs(t, cloud.VerifyWebhookSignature([]byte(`{"type":"other"}`), sign(secret, now, body), secret, now),
		cloud.ErrWebhookSignature, "a body other than the one signed")
	assert.ErrorIs(t, cloud.VerifyWebhookSignature(body, sign("whsec_other", now, body), secret, now),
		cloud.ErrWebhookSignature, "another secret")
	assert.ErrorIs(t, cloud.VerifyWebhookSignature(body, sign(secret, now.Add(-time.Hour), body), secret, now),
		cloud.ErrWebhookSignature, "an hour-old delivery")
	assert.ErrorIs(t, cloud.VerifyWebhookSignature(body, "", secret, now), cloud.ErrWebhookSignature, "no header")
	assert.ErrorIs(t, cloud.VerifyWebhookSignature(body, sign(secret, now, body), "", now),
		cloud.ErrWebhookNotConfigured, "no signing secret")
}

func TestApplyStripeEvent_APaidCheckoutMovesThePlan(t *testing.T) {
	svc, store, id := billingService(t)
	ctx := context.Background()
	event := func(typ, status, plan string) []byte {
		return []byte(fmt.Sprintf(`{"id":"evt_1","type":%q,"data":{"object":{"client_reference_id":"%d","subscription":"sub_1","customer":"cus_1","payment_status":%q,"metadata":{"plan":%q}}}}`,
			typ, id, status, plan))
	}

	changed, err := svc.ApplyStripeEvent(ctx, event("checkout.session.completed", "unpaid", "pro"))
	require.NoError(t, err)
	assert.False(t, changed, "an unpaid session moved the plan")
	changed, err = svc.ApplyStripeEvent(ctx, event("invoice.created", "paid", "pro"))
	require.NoError(t, err)
	assert.False(t, changed)
	_, err = svc.ApplyStripeEvent(ctx, event("checkout.session.completed", "paid", "free"))
	assert.True(t, errors.Is(err, cloud.ErrInvalid), "a session for a plan nobody pays for: %v", err)
	plan, _, _, err := store.GetProviderPlan(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "free", plan)

	changed, err = svc.ApplyStripeEvent(ctx, event("checkout.session.completed", "paid", "pro"))
	require.NoError(t, err)
	assert.True(t, changed)
	plan, limits, ref, err := store.GetProviderPlan(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "pro", plan)
	assert.Contains(t, limits, `"max_users":25`)
	assert.Equal(t, "sub_1", ref)
}
