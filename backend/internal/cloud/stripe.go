package cloud

// Stripe SKELETON — deliberately NO Stripe SDK dependency (Burak md.18: raw
// stdlib http drafts + TODOs only; no live billing until the cloud offering
// actually launches). Everything below compiles and shapes the integration,
// but nothing is production-hardened: see the TODO markers + docs/CLOUD.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// CreateCheckoutSession drafts POST /v1/checkout/sessions and returns the
// hosted checkout URL. DRAFT — the request wiring is real but the parameter
// set is the bare minimum:
//
// TODO(cloud-launch): pass customer_email + client_reference_id (tenant slug)
// TODO(cloud-launch): set subscription mode metadata for the webhook to map
// TODO(cloud-launch): idempotency key header (Idempotency-Key)
// TODO(cloud-launch): typed error decoding (Stripe error JSON envelope)
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, priceID, successURL, cancelURL string) (string, error) {
	if !c.Configured() {
		return "", ErrStripeNotConfigured
	}
	if priceID == "" {
		return "", fmt.Errorf("cloud: stripe: price id required")
	}
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("line_items[0][price]", priceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)

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
	// TODO(cloud-launch): decode the session JSON properly; for the skeleton we
	// only need the hosted "url" field.
	return extractJSONString(body, "url"), nil
}

// VerifyWebhookSignature drafts the Stripe-Signature check. SKELETON — always
// rejects until implemented so an accidentally exposed webhook can never be
// spoofed into doing something.
//
// TODO(cloud-launch): parse `t=…,v1=…`, HMAC-SHA256 over "<t>.<payload>" with
// the endpoint's signing secret (whsec_…), constant-time compare + 5-minute
// tolerance window.
func VerifyWebhookSignature(payload []byte, sigHeader, signingSecret string) error {
	_ = payload
	_ = sigHeader
	_ = signingSecret
	return errors.New("cloud: stripe webhook signature verification not implemented (skeleton)")
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
