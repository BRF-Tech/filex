package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Sentinel error kinds the handler maps to HTTP statuses.
var (
	// ErrInvalid — malformed signup input (→ 400).
	ErrInvalid = errors.New("cloud: invalid input")
	// ErrConflict — tenant slug already taken (→ 409).
	ErrConflict = errors.New("cloud: conflict")
	// ErrTokenUnknown — verification token unknown or expired (→ 404).
	ErrTokenUnknown = errors.New("cloud: unknown or expired verification token")
)

// verifyTTL bounds how long a pending e-mail verification token stays valid.
const verifyTTL = 24 * time.Hour

// slugRe validates tenant slugs: lowercase alphanumeric + dashes, 2–63 chars,
// must start with a letter or digit (DNS-label-safe so <slug>.<BaseHost> is a
// valid hostname).
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// reservedSlugs can never be self-claimed (routing / platform collisions).
// model.DefaultProviderSlug ("default") is reserved too — checked separately
// below so gofmt keeps this literal compact.
var reservedSlugs = map[string]bool{
	"admin": true, "api": true, "www": true, "mail": true,
	"cloud": true, "billing": true, "status": true,
}

// pendingVerify is an in-memory pending e-mail verification. SKELETON: state
// does not survive a restart — a real launch persists this (see docs/CLOUD.md
// runbook TODOs).
type pendingVerify struct {
	ProviderID int64
	Email      string
	ExpiresAt  time.Time
}

// Service implements the self-signup skeleton. Constructed ONLY when
// FILEX_CLOUD=1 (api.BuildRouter); nothing else references it.
type Service struct {
	store  db.Store
	mailer *mailer.Service // optional; nil or unverified → token logged + returned
	plans  []Plan
	// plansErr remembers a FILEX_CLOUD_PLANS parse failure (service keeps
	// booting on DefaultPlans; /api/cloud/status surfaces the error).
	plansErr string
	baseHost string

	mu      sync.Mutex
	pending map[string]pendingVerify // verify token → pending signup
}

// New builds the Service from the runtime config pieces. An invalid
// FILEX_CLOUD_PLANS does not abort boot: the service falls back to
// DefaultPlans() and reports the parse error through Status().
func New(store db.Store, m *mailer.Service, plansJSON, baseHost string) *Service {
	s := &Service{
		store:    store,
		mailer:   m,
		baseHost: strings.TrimSpace(strings.TrimPrefix(baseHost, ".")),
		pending:  map[string]pendingVerify{},
	}
	plans, err := ParsePlans(plansJSON)
	if err != nil {
		slog.Warn("cloud: falling back to default plan catalog", slog.String("error", err.Error()))
		s.plansErr = err.Error()
		plans = DefaultPlans()
	}
	s.plans = plans
	return s
}

// Plans returns the active catalog.
func (s *Service) Plans() []Plan { return s.plans }

// PlansErr returns the remembered FILEX_CLOUD_PLANS parse error ("" when the
// catalog loaded cleanly).
func (s *Service) PlansErr() string { return s.plansErr }

// PlanByID resolves a plan id; nil when unknown.
func (s *Service) PlanByID(id string) *Plan {
	for i := range s.plans {
		if s.plans[i].ID == id {
			return &s.plans[i]
		}
	}
	return nil
}

// SignupRequest is the POST /api/cloud/signup payload.
type SignupRequest struct {
	// Email of the prospective tenant owner (receives the verification mail).
	Email string `json:"email"`
	// Slug is the requested tenant identifier (DNS-label rules).
	Slug string `json:"slug"`
	// Name is the display name; defaults to the slug.
	Name string `json:"name,omitempty"`
	// Plan is a plan id from the catalog; defaults to the first plan.
	Plan string `json:"plan,omitempty"`
}

// SignupResult reports a provisioned (but not yet verified) tenant.
type SignupResult struct {
	ProviderID int64  `json:"tenant_id"`
	Slug       string `json:"slug"`
	Host       string `json:"host,omitempty"`
	Plan       string `json:"plan"`
	// MailSent — whether the verification mail went out through SMTP.
	MailSent bool `json:"mail_sent"`
	// VerifyToken is returned ONLY when the mail could not be sent (no/unverified
	// SMTP) so a local/dev flow can complete verification — the same on-screen
	// fallback pattern the share/invite mailer uses. Empty when mailed.
	VerifyToken string `json:"verify_token,omitempty"`
}

// Signup validates the request and provisions the tenant DISABLED, pending
// e-mail verification. Provisioning reuses the exact primitive the
// /api/admin/providers lifecycle API uses (db.Store.CreateProvider) — no
// parallel provisioning path — then stamps the plan snapshot into the
// migration-00021 columns. Storage linking is deliberately NOT part of the
// skeleton (see docs/CLOUD.md runbook).
func (s *Service) Signup(ctx context.Context, req SignupRequest) (*SignupResult, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(email); err != nil || email == "" {
		return nil, fmt.Errorf("%w: valid email required", ErrInvalid)
	}
	slug := strings.TrimSpace(strings.ToLower(req.Slug))
	if !slugRe.MatchString(slug) {
		return nil, fmt.Errorf("%w: slug must match %s", ErrInvalid, slugRe.String())
	}
	if reservedSlugs[slug] || slug == model.DefaultProviderSlug {
		return nil, fmt.Errorf("%w: slug %q is reserved", ErrInvalid, slug)
	}
	planID := strings.TrimSpace(req.Plan)
	if planID == "" {
		planID = s.plans[0].ID
	}
	plan := s.PlanByID(planID)
	if plan == nil {
		return nil, fmt.Errorf("%w: unknown plan %q", ErrInvalid, planID)
	}
	if existing, err := s.store.GetProviderBySlug(ctx, slug); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, fmt.Errorf("%w: slug %q is taken", ErrConflict, slug)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	host := ""
	if s.baseHost != "" {
		host = slug + "." + s.baseHost
	}
	// Same provisioning primitive as handlers.Providers.Create — the tenant is
	// a provider row; Enabled=false parks it until the e-mail verifies.
	p, err := s.store.CreateProvider(ctx, &model.Provider{
		Slug:     slug,
		Name:     name,
		Host:     host,
		AuthType: model.AuthTypeLocal,
		Enabled:  false,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	limitsJSON, _ := json.Marshal(plan.Limits)
	if err := s.store.SetProviderPlan(ctx, p.ID, plan.ID, string(limitsJSON), ""); err != nil {
		return nil, err
	}

	token, err := s.mintVerifyToken(p.ID, email)
	if err != nil {
		return nil, err
	}
	res := &SignupResult{ProviderID: p.ID, Slug: p.Slug, Host: p.Host, Plan: plan.ID}
	// E-mail verification skeleton: always log the token; send it through the
	// existing mailer when SMTP is configured + verified, otherwise return it
	// in the response (dev/local fallback).
	slog.Info("cloud: signup verification token minted",
		slog.String("slug", p.Slug), slog.String("email", email), slog.String("token", token))
	if s.mailer != nil && s.mailer.Verified() {
		body := fmt.Sprintf(
			"Your filex cloud tenant %q is almost ready.\n\nVerification token: %s\n\nPOST it to /api/cloud/verify to activate the tenant. The token expires in %s.\n",
			p.Slug, token, verifyTTL)
		if err := s.mailer.Send(ctx, email, "filex cloud: verify your tenant", body); err == nil {
			res.MailSent = true
		}
	}
	if !res.MailSent {
		res.VerifyToken = token
	}
	return res, nil
}

// Verify consumes a verification token: the tenant flips Enabled=true through
// the same UpdateProvider path the admin API uses.
func (s *Service) Verify(ctx context.Context, token string) (*model.Provider, error) {
	token = strings.TrimSpace(token)
	s.mu.Lock()
	pv, ok := s.pending[token]
	if ok && time.Now().After(pv.ExpiresAt) {
		delete(s.pending, token)
		ok = false
	}
	if ok {
		delete(s.pending, token)
	}
	s.mu.Unlock()
	if !ok || token == "" {
		return nil, ErrTokenUnknown
	}
	p, err := s.store.GetProvider(ctx, pv.ProviderID)
	if err != nil || p == nil {
		return nil, ErrTokenUnknown
	}
	p.Enabled = true
	if err := s.store.UpdateProvider(ctx, p); err != nil {
		return nil, err
	}
	slog.Info("cloud: tenant verified + enabled", slog.String("slug", p.Slug))
	return p, nil
}

func (s *Service) mintVerifyToken(providerID int64, email string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	s.pending[token] = pendingVerify{ProviderID: providerID, Email: email, ExpiresAt: time.Now().Add(verifyTTL)}
	s.mu.Unlock()
	return token, nil
}
