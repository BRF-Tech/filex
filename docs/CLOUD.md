# Cloud preparation (`FILEX_CLOUD`)

> Status: **preparation skeleton only - NOT a live service.** Built in the
> v0.7 "Kimlik" wave (E3). The explicit product decision (the project owner, md.18) is:
> *prepare the scaffolding, do NOT launch a hosted cloud offering yet.* The
> master flag is therefore **off by default**, and while it is off the
> feature is guaranteed to be invisible (see §5).

## 1. What this is

The groundwork for a future self-serve hosted filex ("filex cloud"): plan
catalog, tenant self-signup, email verification, and a Stripe billing
skeleton. It deliberately builds on the **native multi-tenancy foundation
(v0.1.61, `docs/MULTI-TENANCY.md`)** instead of inventing anything new:

- a signed-up **tenant IS a provider row** - signup calls the exact same
  provisioning primitive (`db.Store.CreateProvider`) as the operator's
  `/api/admin/providers` lifecycle API. There is **no second provisioning
  path** to keep in sync.
- plan metadata rides on the existing `providers` table (migration
  `00021_provider_cloud_plan.sql`, all three DB dialects): three **nullable,
  passive** columns - `plan`, `limits_json`, `billing_ref`.
- email delivery reuses the existing settings-table SMTP mailer
  (`internal/mailer`), with the same "not verified → show the
  token/link on-screen" fallback the share/invite mail uses.

Code map:

| Piece | Path |
|---|---|
| Plan catalog + parsing | `backend/internal/cloud/plans.go` |
| Signup / verify service | `backend/internal/cloud/service.go` |
| Stripe skeleton (stdlib http, **no SDK dep**) | `backend/internal/cloud/stripe.go` |
| HTTP surface | `backend/internal/api/handlers/cloud.go` |
| Route gate (single `if d.Cfg.Cloud.Enabled` block) | `backend/internal/api/routes.go` |
| Migration 00021 (×3 dialects) | `backend/db/migrations/*/00021_provider_cloud_plan.sql` |
| Store accessors | `SetProviderPlan` / `GetProviderPlan` in `internal/db` drivers |

## 2. Flags / environment

| Env | Default | Meaning |
|---|---|---|
| `FILEX_CLOUD` | **off** | Master flag. `1`/`true` mounts `/api/cloud` and adds the `cloud` capabilities field. Anything else (including unset) = feature does not exist. |
| `FILEX_CLOUD_PLANS` | *(empty)* | JSON plan catalog: `[{"id","name","price_monthly","stripe_price_id","limits":{"storage_bytes","max_users"}}]`. Empty → one built-in `free` plan (1 GiB / 3 users). The catalog needs **at least one plan without a `stripe_price_id`**: the first such plan is the one every signup starts on. A parse error (this one included) does **not** abort boot: defaults stay active and `/api/cloud/status` reports `plans_error`. |
| `STRIPE_SECRET` (alias `FILEX_STRIPE_SECRET`) | *(empty)* | Stripe API secret. Empty → both `/api/cloud/billing/*` endpoints answer **503 "stripe not configured"**. |
| `STRIPE_WEBHOOK_SECRET` (alias `FILEX_STRIPE_WEBHOOK_SECRET`) | *(empty)* | The webhook endpoint's signing secret (`whsec_…`). A plan changes only on an event signed with it. Empty → the webhook answers **503**. |
| `FILEX_CLOUD_BASE_HOST` | *(empty)* | e.g. `filex.cloud` → a signed-up tenant gets `host = <slug>.<base>`. Empty → tenant provisioned without a host. |
| `FILEX_MULTI_TENANT` | off | Not owned by this feature, but a real cloud launch **requires** it - tenants are provider rows and only resolve/isolate in multi-tenant mode. `/api/cloud/status` echoes its state. |

## 3. HTTP surface (only exists when `FILEX_CLOUD=1`)

All endpoints are **public** (a signup surface has no session yet).

| Method + path | Purpose | Answers |
|---|---|---|
| `GET /api/cloud/status` | State snapshot | `{enabled, multi_tenant, plans, stripe_configured, signup_url[, plans_error]}` |
| `GET /api/cloud/plans` | Plan catalog | `{plans:[…]}` |
| `POST /api/cloud/signup` | `{email, slug, name?}` → provisions a **disabled** tenant on the **signup plan** + mints a 24 h verification token. A `plan` field is ignored (since 0.54.0). | `202 {tenant_id, slug, host, plan, mail_sent[, verify_token]}` · `400` invalid input · `409` slug taken |
| `POST /api/cloud/verify` | `{token}` → enables the tenant (single-use) | `200 {ok, slug, enabled}` · `404` unknown/expired/replayed |
| `POST /api/cloud/billing/checkout` | `{tenant, plan}` → a Stripe checkout session for the tenant (its slug) to move to a paid plan. The return addresses are the server's, built from the tenant's own origin (`/?billing=done…`, `/?billing=cancelled`); a `success_url` / `cancel_url` in the body is ignored (since 0.54.0). Opening a session changes nothing. | `200 {checkout_url}` · `503` without secret · `400` unknown tenant, or plan has no `stripe_price_id` · `502` Stripe error |
| `POST /api/cloud/billing/webhook` | Stripe events, **the only way a tenant's plan changes**. `Stripe-Signature` is verified (HMAC-SHA256 over `<t>.<body>`, 5-minute tolerance) before the event is parsed; `checkout.session.completed` with `payment_status: paid` moves the tenant in `client_reference_id` to the paid plan in `metadata.plan` and stamps `billing_ref` (subscription, else customer). Other events are acknowledged and ignored. | `200 {status: applied\|ignored}` · `503` without `STRIPE_SECRET` or `STRIPE_WEBHOOK_SECRET` · `400` signature does not verify, or the event names no tenant / paid plan |

Signup flow details:

- slug: DNS-label rules (`^[a-z0-9][a-z0-9-]{1,62}$`), reserved names
  (`default`, `admin`, `api`, `www`, `mail`, `cloud`, `billing`, `status`)
  rejected. The slug is also the tenant's **realm** (its sign-in name, set once
  and never changed - [MULTI-TENANCY.md → Realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)),
  so a slug that is a reserved realm (`local`, `root`, `filex`, a protocol name…)
  is `400`, and one that is another tenant's realm `409`.
- the tenant is created `enabled=false` (`auth_type=local`) on the **signup
  plan** - the catalog's first plan without a `stripe_price_id` - whatever
  the request says, and that plan's limits are stamped as a JSON **snapshot**
  into `limits_json` - later catalog edits never silently change an existing
  tenant's entitlement. A paid plan is reached only through checkout and the
  verified webhook.
- verification token: 32 random bytes hex, 24 h TTL, single-use, held
  **in memory** (skeleton - see §6). Always logged (`slog`); mailed through
  the SMTP mailer when configured **and** verified, otherwise returned in
  the response (`verify_token`) - the same on-screen fallback pattern the
  invite mail uses.
- when the flag is on, `GET /api/capabilities` additionally carries
  `"cloud": {"enabled": true, "signup_url": "/api/cloud/signup"}`.

No admin-SPA page exists for any of this (intentional - brief E3): the status
endpoint is the whole operator surface for now.

## 4. Stripe skeleton - what is real, what is TODO

Real: config gating (503 without secret), the form-encoded
`POST /v1/checkout/sessions` request wiring via stdlib `net/http` (no Stripe
SDK dependency, per the wave's no-new-deps rule), plan → `stripe_price_id`
mapping, the tenant (`client_reference_id`) and plan (`metadata[plan]`) in
the session, server-built return addresses (`cloud.ReturnURLs`), webhook
signature verification (`VerifyWebhookSignature`) and the
`checkout.session.completed` → plan + `billing_ref` transition
(`cloud.Service.ApplyStripeEvent`, `internal/cloud/billing.go`).

Deliberately NOT implemented (marked `TODO(cloud-launch)`):

- `customer.subscription.deleted` → back to the signup plan (needs a lookup
  of the tenant by `billing_ref`);
- idempotency keys, typed error decoding.

## 5. Guarantees while the flag is OFF (the binding contract)

`FILEX_CLOUD` unset/false - the default everywhere - means:

1. **No routes.** The `/api/cloud` block in `BuildRouter` is skipped
   entirely; every `/api/cloud/*` path answers chi's stock 404, exactly as
   on a build without the feature.
2. **No capabilities field.** The `cloud` key is absent (not `false`) from
   `/api/capabilities` - the wire format is byte-identical.
3. **Passive schema.** Migration 00021's columns are nullable and are
   read/written **only** by the cloud service (`SetProviderPlan` /
   `GetProviderPlan`, called nowhere else). Existing provider CRUD SQL was
   not widened - flag-off installs keep the columns `NULL` forever.
4. **No construction.** `cloud.Service` / `StripeClient` are only built
   inside the gated block; no goroutines, no state, no logging.

These are locked by tests (`internal/api/handlers/cloud_test.go`
`TestCloud_FlagOff_ZeroBehaviorChange` + the untouched full suite).

## 6. Launch runbook (what a REAL "filex cloud" still needs)

Prep in order; nothing below is required today.

1. **Product**: domain (e.g. `filex.cloud`), plan/pricing decision →
   `FILEX_CLOUD_PLANS`.
2. **Infra**: Postgres (not sqlite) for the shared control plane; per-tenant
   S3 bucket/prefix provisioning + `LinkProviderStorage` at signup (the
   skeleton deliberately provisions **no storage**); `FILEX_MULTI_TENANT=1`;
   wildcard DNS + TLS for `*.<base_host>`.
3. **Durability**: move pending email verifications from the in-memory map
   to a table (they currently die on restart - acceptable for a skeleton,
   not for production).
4. **Mail**: production SMTP (settings table) so `verify_token` stops
   falling back into the API response; localized mail templates.
5. **Stripe**: account + products/prices → `stripe_price_id` per plan,
   `STRIPE_SECRET`, a webhook endpoint and its `STRIPE_WEBHOOK_SECRET`, the
   §4 TODOs (the downgrade first).
6. **Abuse controls**: rate-limit `/api/cloud/signup`, CAPTCHA or
   equivalent, disposable-email policy.
7. **Enforcement**: actually enforce `limits_json` (storage via the existing
   quota service, user count at user-create) - the skeleton only *records*
   entitlements.
8. **Ops**: signup/verify metrics + alerts; admin SPA page if operating it
   becomes routine.

## 7. Testing

- `internal/cloud/plans_test.go` - catalog parsing (defaults, valid,
  malformed, duplicate ids, no signup plan) + boot-on-broken-catalog fallback.
- `internal/cloud/billing_test.go` - the signup plan whatever a signup asks,
  the checkout's server-built return addresses and session fields, the webhook
  signature (good, tampered, stale, wrong secret) and the plan transition.
- `internal/api/handlers/cloud_test.go` - flag-off 404s + capabilities
  absence; flag-on signup→verify e2e on the sqlite rig (provider row
  disabled→enabled, plan snapshot stamped, token single-use); Stripe-less
  503s; `plans_error` surfacing.

Run: `cd backend && go build ./... && go test ./...`.
