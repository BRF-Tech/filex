package handlers

/* kimlik:e3 cloud */

// Cloud — the /api/cloud self-signup PREPARATION surface (v0.7 "Kimlik" E3,
// docs/CLOUD.md). This handler is ONLY constructed when FILEX_CLOUD=1: with
// the flag off (default) api.BuildRouter never mounts /api/cloud, so every
// path here answers chi's stock 404 and nothing below can run.
//
// Endpoints (all public — a signup surface has no session yet; rate limiting
// is a launch TODO, see docs/CLOUD.md):
//
//	GET  /api/cloud/status            → flag/plan/stripe state snapshot
//	GET  /api/cloud/plans             → the configured plan catalog
//	POST /api/cloud/signup            → provision a DISABLED tenant + verify token
//	POST /api/cloud/verify            → consume the token, enable the tenant
//	POST /api/cloud/billing/checkout  → Stripe checkout-session draft (503 w/o STRIPE_SECRET)
//	POST /api/cloud/billing/webhook   → Stripe webhook draft (503 w/o STRIPE_SECRET)

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/cloud"
)

// Cloud handles /api/cloud.
type Cloud struct {
	Svc    *cloud.Service
	Stripe *cloud.StripeClient
	// MultiTenant is echoed in /status — a real cloud launch requires
	// FILEX_MULTI_TENANT=1 (a signed-up tenant is a provider row).
	MultiTenant bool
}

// NewCloud constructs the handler.
func NewCloud(svc *cloud.Service, stripe *cloud.StripeClient, multiTenant bool) *Cloud {
	return &Cloud{Svc: svc, Stripe: stripe, MultiTenant: multiTenant}
}

// Register mounts the cloud routes (called only when FILEX_CLOUD=1).
func (h *Cloud) Register(r chi.Router) {
	r.Get("/status", h.Status)
	r.Get("/plans", h.Plans)
	r.Post("/signup", h.Signup)
	r.Post("/verify", h.Verify)
	r.Post("/billing/checkout", h.Checkout)
	r.Post("/billing/webhook", h.Webhook)
}

// Status is the simple state snapshot (the brief's "durum endpoint'i").
func (h *Cloud) Status(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"enabled":           true, // route only exists when the flag is on
		"multi_tenant":      h.MultiTenant,
		"plans":             len(h.Svc.Plans()),
		"stripe_configured": h.Stripe.Configured(),
		"signup_url":        "/api/cloud/signup",
	}
	if e := h.Svc.PlansErr(); e != "" {
		out["plans_error"] = e
	}
	writeJSON(w, http.StatusOK, out)
}

// Plans returns the configured catalog.
func (h *Cloud) Plans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"plans": h.Svc.Plans()})
}

// Signup provisions a disabled tenant + mints the verification token.
func (h *Cloud) Signup(w http.ResponseWriter, r *http.Request) {
	var req cloud.SignupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	res, err := h.Svc.Signup(r.Context(), req)
	if err != nil {
		writeJSON(w, cloudErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

// Verify consumes a verification token ({"token": "…"}).
func (h *Cloud) Verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token required"})
		return
	}
	p, err := h.Svc.Verify(r.Context(), req.Token)
	if err != nil {
		writeJSON(w, cloudErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slug": p.Slug, "enabled": p.Enabled})
}

// Checkout drafts a Stripe checkout session. Without STRIPE_SECRET → 503.
func (h *Cloud) Checkout(w http.ResponseWriter, r *http.Request) {
	if !h.Stripe.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stripe not configured"})
		return
	}
	var req struct {
		Plan       string `json:"plan"`
		SuccessURL string `json:"success_url"`
		CancelURL  string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	plan := h.Svc.PlanByID(req.Plan)
	if plan == nil || plan.StripePriceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown plan or plan has no stripe_price_id"})
		return
	}
	url, err := h.Stripe.CreateCheckoutSession(r.Context(), plan.StripePriceID, req.SuccessURL, req.CancelURL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"checkout_url": url})
}

// Webhook drafts the Stripe webhook receiver. Without STRIPE_SECRET → 503;
// with it, the (skeleton) signature check rejects everything until a launch
// implements it — the endpoint can never be spoofed into acting.
func (h *Cloud) Webhook(w http.ResponseWriter, r *http.Request) {
	if !h.Stripe.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stripe not configured"})
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := cloud.VerifyWebhookSignature(payload, r.Header.Get("Stripe-Signature"), ""); err != nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": err.Error()})
		return
	}
	// TODO(cloud-launch): dispatch on event type — checkout.session.completed →
	// stamp providers.billing_ref; customer.subscription.deleted → downgrade.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// cloudErrStatus maps service sentinel errors to HTTP statuses.
func cloudErrStatus(err error) int {
	switch {
	case errors.Is(err, cloud.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, cloud.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, cloud.ErrTokenUnknown):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
