package cloud

// Stripe SKELETON — deliberately NO Stripe SDK dependency (owner decision md.18: raw
// stdlib http drafts + TODOs only; no live billing until the cloud offering
// actually launches). Everything below compiles and shapes the integration,
// but nothing is production-hardened: see the TODO markers + docs/CLOUD.md.

import (
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
	"strings"
	"time"
)

// ErrStripeNotConfigured — no STRIPE_SECRET; the billing endpoints answer 503.
var ErrStripeNotConfigured = errors.New("cloud: stripe not configured")

// stripeAPIBase is Stripe's REST endpoint (form-encoded POST bodies).
const stripeAPIBase = "https://api.stripe.com/v1"

// StripeClient is a minimal stdlib-http Stripe caller.
type StripeClient struct {
	// Secret is the API secret key (sk_test_… / sk_live_…). Empty = not
	// configured; every call returns ErrStripeNotConfigured.
	Secret string
	// HTTP is the transport; nil → a 15s-timeout default client.
	HTTP *http.Client
}

// NewStripe constructs the client (secret may be empty = not configured).
func NewStripe(secret string) *StripeClient {
	return &StripeClient{Secret: strings.TrimSpace(secret)}
}

// Configured reports whether a secret key is present.
func (c *StripeClient) Configured() bool { return c != nil && c.Secret != "" }

// CheckoutSession is what a checkout session is opened with. Every field is
// the server's: the price is the catalogue plan's, the return addresses are
// built from the tenant's own origin (ReturnURLs), and the tenant and plan
// travel in the session so the webhook knows what was paid for.
type CheckoutSession struct {
	PriceID    string
	SuccessURL string
	CancelURL  string
	// ClientReferenceID is the tenant's provider id.
	ClientReferenceID string
	// Plan is the catalogue plan id, carried as metadata[plan].
	Plan string
}

// CreateCheckoutSession opens POST /v1/checkout/sessions and returns the
// hosted checkout URL.
//
// TODO(cloud-launch): idempotency key header (Idempotency-Key)
// TODO(cloud-launch): typed error decoding (Stripe error JSON envelope)
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, cs CheckoutSession) (string, error) {
	if !c.Configured() {
		return "", ErrStripeNotConfigured
	}
	if cs.PriceID == "" {
		return "", fmt.Errorf("cloud: stripe: price id required")
	}
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("line_items[0][price]", cs.PriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("success_url", cs.SuccessURL)
	form.Set("cancel_url", cs.CancelURL)
	if cs.ClientReferenceID != "" {
		form.Set("client_reference_id", cs.ClientReferenceID)
	}
	if cs.Plan != "" {
		form.Set("metadata[plan]", cs.Plan)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		stripeAPIBase+"/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cloud: stripe: checkout session: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return extractJSONString(body, "url"), nil
}

// ReturnURLs are the two addresses Stripe sends the payer back to, built by
// the server from the tenant's origin. A caller never names them: an address
// taken from the request would let anyone send a payer, through Stripe's own
// page, wherever they liked.
func ReturnURLs(origin string) (success, cancel string) {
	base := strings.TrimRight(strings.TrimSpace(origin), "/")
	return base + "/?billing=done&session_id={CHECKOUT_SESSION_ID}", base + "/?billing=cancelled"
}

// Webhook signature errors.
var (
	// ErrWebhookNotConfigured — no webhook signing secret
	// (STRIPE_WEBHOOK_SECRET); the webhook answers 503.
	ErrWebhookNotConfigured = errors.New("cloud: stripe webhook signing secret not configured")
	// ErrWebhookSignature — the Stripe-Signature header does not verify, or
	// its timestamp is outside the tolerance.
	ErrWebhookSignature = errors.New("cloud: stripe webhook signature does not verify")
)

// webhookTolerance is how far a signed event's timestamp may be from now
// (Stripe's own libraries use five minutes): an older, replayed delivery is
// refused.
const webhookTolerance = 5 * time.Minute

// VerifyWebhookSignature checks a Stripe-Signature header
// (`t=<unix>,v1=<hex>[,v1=…]`): HMAC-SHA256 over "<t>.<payload>" with the
// endpoint's signing secret (whsec_…), compared in constant time, with the
// timestamp within webhookTolerance of now. A plan changes only after this
// says yes.
func VerifyWebhookSignature(payload []byte, sigHeader, signingSecret string, now time.Time) error {
	if strings.TrimSpace(signingSecret) == "" {
		return ErrWebhookNotConfigured
	}
	var (
		ts   int64
		sigs []string
	)
	for _, part := range strings.Split(sigHeader, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts <= 0 || len(sigs) == 0 {
		return ErrWebhookSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > webhookTolerance || d < -webhookTolerance {
		return ErrWebhookSignature
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, s := range sigs {
		if hmac.Equal([]byte(want), []byte(s)) {
			return nil
		}
	}
	return ErrWebhookSignature
}

func (c *StripeClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// extractJSONString pulls a top-level string field out of a JSON object
// without committing to a full response schema (skeleton helper).
func extractJSONString(body []byte, key string) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
