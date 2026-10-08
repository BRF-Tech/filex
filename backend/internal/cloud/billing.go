package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Moving a tenant to a paid plan.
//
// ⚠ A tenant's plan changes in exactly one place: ApplyStripeEvent, after
// VerifyWebhookSignature has said the event is Stripe's. A signup starts on
// SignupPlan whatever it asks for, and Checkout only opens a payment: nothing
// a client sends sets a plan.

// Checkout opens a Stripe checkout session for the tenant named by slug to
// move to planID, and returns the hosted page's address. The price is the
// catalogue plan's, the return addresses are the server's (ReturnURLs over
// origin(the tenant)), and the tenant and plan travel in the session for the
// webhook. A plan with no stripe_price_id cannot be bought (ErrInvalid).
func (s *Service) Checkout(ctx context.Context, sc *StripeClient, slug, planID string, origin func(ctx context.Context, providerID int64) string) (string, error) {
	if !sc.Configured() {
		return "", ErrStripeNotConfigured
	}
	plan := s.PlanByID(strings.TrimSpace(planID))
	if plan == nil || strings.TrimSpace(plan.StripePriceID) == "" {
		return "", fmt.Errorf("%w: unknown plan or plan has no stripe_price_id", ErrInvalid)
	}
	p, err := s.store.GetProviderBySlug(ctx, strings.TrimSpace(strings.ToLower(slug)))
	if err != nil {
		return "", err
	}
	if p == nil {
		return "", fmt.Errorf("%w: unknown tenant %q", ErrInvalid, slug)
	}
	base := ""
	if origin != nil {
		base = origin(ctx, p.ID)
	}
	success, cancel := ReturnURLs(base)
	return sc.CreateCheckoutSession(ctx, CheckoutSession{
		PriceID:           plan.StripePriceID,
		SuccessURL:        success,
		CancelURL:         cancel,
		ClientReferenceID: strconv.FormatInt(p.ID, 10),
		Plan:              plan.ID,
	})
}

// stripeEvent is the part of a Stripe event the cloud reads.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object struct {
			ClientReferenceID string            `json:"client_reference_id"`
			Customer          string            `json:"customer"`
			Subscription      string            `json:"subscription"`
			PaymentStatus     string            `json:"payment_status"`
			Metadata          map[string]string `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

// ApplyStripeEvent acts on an event VerifyWebhookSignature has accepted, and
// reports whether a tenant's plan changed.
//
// checkout.session.completed with payment_status "paid" moves the tenant the
// session names (client_reference_id, set by Checkout) to the paid plan in
// metadata.plan, stamping the plan snapshot and the subscription (else the
// customer) as billing_ref. Every other event is acknowledged and ignored.
//
// TODO(cloud-launch): customer.subscription.deleted → back to SignupPlan
// (needs a lookup of the tenant by billing_ref).
func (s *Service) ApplyStripeEvent(ctx context.Context, payload []byte) (bool, error) {
	var ev stripeEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return false, fmt.Errorf("%w: event is not JSON", ErrInvalid)
	}
	if ev.Type != "checkout.session.completed" {
		return false, nil
	}
	obj := ev.Data.Object
	if obj.PaymentStatus != "paid" {
		return false, nil
	}
	providerID, err := strconv.ParseInt(strings.TrimSpace(obj.ClientReferenceID), 10, 64)
	if err != nil || providerID <= 0 {
		return false, fmt.Errorf("%w: checkout session names no tenant", ErrInvalid)
	}
	plan := s.PlanByID(strings.TrimSpace(obj.Metadata["plan"]))
	if plan == nil || strings.TrimSpace(plan.StripePriceID) == "" {
		return false, fmt.Errorf("%w: checkout session names no paid plan", ErrInvalid)
	}
	p, err := s.store.GetProvider(ctx, providerID)
	if err != nil || p == nil {
		return false, fmt.Errorf("%w: unknown tenant %d", ErrInvalid, providerID)
	}
	ref := obj.Subscription
	if ref == "" {
		ref = obj.Customer
	}
	limitsJSON, _ := json.Marshal(plan.Limits)
	if err := s.store.SetProviderPlan(ctx, p.ID, plan.ID, string(limitsJSON), ref); err != nil {
		return false, err
	}
	slog.Info("cloud: plan changed by a verified stripe event",
		slog.String("slug", p.Slug), slog.String("plan", plan.ID), slog.String("event", ev.ID))
	return true, nil
}
