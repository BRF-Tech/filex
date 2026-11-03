# Bot protection (CAPTCHA)

filex can put a bot check on the forms anybody can post without an account:
the **sign-in**, a demo's **one-click sign-in**, the **PIN** of a protected
link and the **upload into a file request**. The check is
[Cloudflare Turnstile](https://developers.cloudflare.com/turnstile/) (free,
cookieless, invisible to most people) or
[Google reCAPTCHA v2](https://developers.google.com/recaptcha/docs/display)
(the checkbox). It is **off** until you turn it on, so an upgrade changes
nothing for an installation that does not.

When the provider cannot be reached, the form is neither left open nor shut:
the browser does a short **proof of work** instead (one to three seconds on a
desktop), and the administrators are told. See
[When the provider is down](#when-the-provider-is-down).

- [Turning it on](#turning-it-on)
- [The settings](#the-settings)
- [The forms](#the-forms)
- [What a visitor sees](#what-a-visitor-sees)
- [How a form is checked](#how-a-form-is-checked)
- [When the provider is down](#when-the-provider-is-down)
- [The local proof of work](#the-local-proof-of-work)
- [File requests: the upload pass](#file-requests-the-upload-pass)
- [Alarms and metrics](#alarms-and-metrics)
- [Several instances, tenants and the page's security headers](#several-instances-tenants-and-the-pages-security-headers)
- [API](#api)
- [Testing](#testing)

---

## Turning it on

1. Create a widget at the provider. For Turnstile: the Cloudflare dashboard →
   **Turnstile** → **Add widget**, mode **Managed**, and the host name filex is
   served on (a host name covers its subdomains). The site does **not** have to
   be proxied by Cloudflare. For reCAPTCHA: a **v2 "I'm not a robot"** key pair
   at the [admin console](https://www.google.com/recaptcha/admin).
2. In filex: **Admin → Bot protection**. Pick the provider, paste
   the **site key** and the **secret key**, choose the forms, **Test the keys** (filex asks
   the provider with the keys on the page: is it reachable, does it take the
   secret), **Save**.

Or from the environment, for an installation provisioned from compose:

```yaml
environment:
  CAPTCHA_PROVIDER: turnstile
  CAPTCHA_SITE_KEY: 0x4AAAAAAA...
  CAPTCHA_SECRET: ${CAPTCHA_SECRET}      # from your secret store
  CAPTCHA_FORMS: login,share_password,file_request   # optional; default: all
```

The secret typed on the page is sealed with the installation's encryption key
(`FILEX_SECRET_KEY`, [CONFIGURATION.md → The secret key](CONFIGURATION.md#the-secret-key)) and never shown
again: the page says only that one is stored. An installation without
`FILEX_SECRET_KEY` cannot store it there; give it through `CAPTCHA_SECRET`.

## The settings

| Setting | Panel | Environment | Default | Values |
|---|---|---|---|---|
| `captcha.provider` | Provider | `CAPTCHA_PROVIDER` | `none` (off) | `none`, `turnstile`, `recaptcha` |
| `captcha.site_key` | Site key | `CAPTCHA_SITE_KEY` | - | the provider's public key; it goes to the browser |
| `captcha.secret` | Secret key | `CAPTCHA_SECRET` | - | the provider's secret; sealed in the database, never answered |
| `captcha.forms` | Forms | `CAPTCHA_FORMS` | every form | `login`, `demo_login`, `share_password`, `file_request` (comma-separated in the environment) |
| `captcha.fallback` | When the provider cannot be reached: Local check / Refuse | `CAPTCHA_FALLBACK` | `local` | `local`; `off` (Refuse) answers 503 instead of the proof of work (for tests only) |
| `captcha.verify_timeout_ms` | Provider timeout (ms) | `CAPTCHA_VERIFY_TIMEOUT_MS` | `4000` | 1000-15000; each call to the provider, tried twice at most |

**Which value is in force: the panel, then the environment, then off** - field
by field. A field you saved on the page wins over its variable; a field you
cleared there is the variable's again.

> ⚠ This is **not** how filex's other database settings use the environment.
> Those read their variable once, at the first boot, into the database
> ([CONFIGURATION.md](CONFIGURATION.md)), and the variable is inert after that.
> The bot check's variables are read **live** whenever the panel's field is
> empty: the order is the one every product that shares this bot check uses.
> Changing `CAPTCHA_SITE_KEY` in compose and restarting therefore takes effect,
> unless the page holds a site key of its own.

A provider is chosen and a key is missing - no site key, no secret, a stored
secret that does not open (did `FILEX_SECRET_KEY` change?), or a secret the
provider refuses - is a **misconfiguration**, not "off": the protected forms
are checked with the local proof of work and the administrators are told
until the keys are fixed. The protection never switches itself off.

## The forms

| Form | What it protects |
|---|---|
| `login` | The password sign-in on the sign-in page, including the recovery sign-in an SSO installation offers ([SSO.md](SSO.md)), on the platform's address and on every tenant's |
| `demo_login` | A demo's one-click sign-in with its documented account ([DEMO.md](DEMO.md)); any other account on a demo is `login` |
| `share_password` | The PIN a protected link asks for: a download share, an app's page and a file request ([SHARING.md](SHARING.md)) |
| `file_request` | The upload into a file request ([SHARING.md → File requests](SHARING.md#file-requests-upload--file-drop)) |

filex has no self-registration and no "forgot password" form (accounts come
from an administrator, a directory or SSO, and only an administrator resets a
password), so it has no `register` or `password_reset` form.

## What a visitor sees

On the sign-in page and on a public link's page, the widget sits above the
form's button. Turnstile in managed mode usually decides by itself in a
second; sometimes it asks for a click. The form waits for its answer and
sends it with the request; after every attempt - a wrong password too - the
widget gives a new one.

- **JavaScript is needed.** The check runs in the browser. While a link's PIN
  or a file request's upload is protected, the pages filex draws for a browser
  without JavaScript (`/s/<token>?nojs=1`, the classic `/d/<token>` page) and a
  PIN in the address (`?pin=`, `X-Filex-Pin`) are refused with
  **422 - JavaScript needed**: none of them can carry the check, and a bot would
  otherwise use them to go around it. A link without a PIN still downloads with
  `curl` as before; the form that is not protected is as it was.
- **The CLI** cannot answer a bot check either. While `login` is protected,
  `filex client login` with a password is refused and says so: create an API
  key in the browser (**Connections → API keys**) and use it with `FILEX_TOKEN`
  ([CLI.md](CLI.md#connecting)). Nothing goes around the check.
- **The desktop app** signs in through the browser ([DESKTOP.md](DESKTOP.md)),
  on the same sign-in page, so it sees the same widget.
- **An ad blocker** that keeps the provider's script from loading is not an
  outage (filex can still reach the provider): the widget says it could not be
  loaded and asks to allow it.

## How a form is checked

The form sends the provider's token with the request (`captcha_token`; the
providers' own names `cf-turnstile-response` and `g-recaptcha-response` are
read too). Before anything else - before a password is compared or a PIN
attempt counted - filex asks the provider's `siteverify`:

| What happened | Answer |
|---|---|
| The form is not protected, or the check is off | the request goes on as before |
| No token, a token of the wrong shape (longer than the provider's tokens, characters outside `A-Z a-z 0-9 . _ -`; refused without asking the provider), a token already used, or the provider said no (`success: false`, or an HTTP 4xx - **429 included**) | **422** `{error, message, captcha: <reason>}`: the widget is reset, a new attempt needs a new answer |
| The provider could not be reached: no connection, no answer in time, an HTTP 5xx, an answer that is not its JSON, `internal-error` (each asked twice) | **409** with a local challenge ([below](#when-the-provider-is-down)) |
| The keys cannot work ([misconfiguration](#the-settings): `invalid-input-secret`, `missing-input-secret`, `sitekey-secret-mismatch`, `invalid-keys`, or a key missing on this side) | **409** with a local challenge, and the administrators are told |
| The provider is down and `captcha.fallback` is `off` | **503** |

> ⚠ A 4xx from the provider is never an outage. If it were, anybody could
> make the provider rate-limit a form (429) and earn the cheap proof of work
> instead of the widget. `bad-request` is a refused token too: the token comes
> from the client.

A token is accepted once: filex keeps the ones it accepted for five minutes and
refuses them again (the provider refuses a reused one as well).

## When the provider is down

The 409 carries a **challenge**; the browser solves it in a Web Worker (a
progress bar and the words *"The verification service cannot be reached; your
browser is doing a short check instead"*) and sends the same form again by
itself with the solution (`captcha_local`). The visitor waits a few seconds
and the form goes through.

- The 409 raises an **outage flag** for 120 seconds. A solution is accepted
  only while it is up: once the provider answers again, a solution is refused
  with **422 `fallback_inactive`** and the widget comes back - so a bot cannot
  skip the widget with the cheap proof of work while the provider is fine.
- A solution is accepted once.
- The script not loading in the browser is told apart on the server: the page
  asks `POST /api/captcha/local/challenge`, and filex hands a challenge out only
  when it cannot reach the provider either (or the keys cannot work);
  otherwise it answers 409 `provider` and the page asks to allow the widget.

## The local proof of work

The browser finds a number: the challenge gives a salt, a SHA-256 hash and a
signature, and the browser tries `0, 1, 2…` until `sha256(salt + number)` is
the hash (at most `maxnumber`, 200000 by default - one to three seconds on a
desktop, under eight on a phone). The server keeps no state for a challenge:
its HMAC signature over the hash says the server issued it, the salt carries
its end (10 minutes), and the solution's signature is spent once.

The format is a contract shared with the other products that run this bot
check (ALTCHA-like): base64 of
`{"algorithm":"SHA-256","salt":"<32 hex>?expires=<unix>","number":N,"hash":"…","signature":"…"}`,
checked in a fixed order (`bad_payload`, `bad_algorithm`, `bad_salt`,
`expired`, `bad_number`, `bad_signature`, `bad_hash`, `replayed`). Its twenty
test vectors are in the repository
(`backend/internal/captcha/testdata/captcha-local-vectors.json`, pinned by
hash); the server and the browser's solver are both tested against them.

## File requests: the upload pass

A file request's upload is one multipart request carrying every file. A refusal
sent while the browser is still sending the body cannot be read by it - the
connection closes under the upload - so the check runs first, on a small
request, and its yes is a **pass**:

1. `POST /api/public/d/<token>/captcha` with `{captcha_token}` (or
   `{captcha_local}` during an outage) → `{pass, expires_at}`.
2. `POST /api/public/d/<token>/upload` with the header `X-Captcha-Pass: <pass>`.

The pass is signed, names this one link, lives ten minutes and is spent by the
upload. Without one the upload is refused (422 `missing_pass` / `bad_pass`)
before a byte of its body is read.

## Alarms and metrics

The administrators are told on the operator notifications (the bell, and the
e-mail, push and webhook channels set up in
[NOTIFICATIONS.md](NOTIFICATIONS.md)), in each reader's language:

| Event | When |
|---|---|
| `captcha_fallback` | More than one form in ten minutes was checked with the local proof of work. Once per ten minutes; the last reason in it (no answer in time, no connection, a server error…). |
| `captcha_misconfigured` | The keys cannot work (above). Once an hour per reason. |

`/metrics` counts the local challenges handed out:
`filex_captcha_fallback_total{cause="unreachable"|"misconfigured"}`
([METRICS.md](METRICS.md)).

## Several instances, tenants and the page's security headers

- **Several instances behind one address** share the outage flag and the
  once-only marks through the database (table `captcha_marks`): the instance
  that answered 409 and the one that receives the solution are not always the
  same. Set the same `FILEX_SECRET_KEY` on all of them - the challenges are
  signed with a key derived from it, and without it each instance signs with a
  key of its own and refuses the others' challenges.
- **Tenants.** The bot check is one setting for the whole installation, the
  platform operator's ([MULTI-TENANCY.md](MULTI-TENANCY.md)). A tenant's own
  address and a realm typed on the sign-in form are checked with it: nobody
  gets around it by naming a tenant.
- **Content-Security-Policy.** While the check is on, filex's pages allow the
  provider's widget frame (`frame-src https://challenges.cloudflare.com`, or
  `https://www.google.com` for reCAPTCHA). A reverse proxy that adds a policy
  of its own must allow the provider's script and frame too, and `blob:` for
  the proof of work's worker.

## API

| Method & path | Who | What |
|---|---|---|
| `GET /api/captcha/config` | anybody | `{provider, site_key, script, fallback, forms, misconfigured, fallback_active, local_field, token_field}` - what a form draws. No secret. |
| `POST /api/captcha/local/challenge` | anybody | 200 `{captcha: "fallback", challenge}` when the server cannot reach the provider either; 409 `{captcha: "provider"}` when it can; 404 when off. |
| `POST /api/public/d/{token}/captcha` | anybody | A file request's [upload pass](#file-requests-the-upload-pass). |
| `GET /api/admin/captcha` | platform operator | The settings: what the panel holds (`stored`), what is in force (`effective`) and where each field came from (`sources`: `db`, `env`, `default`); `misconfigured`, `fallback_active`, `fallback_total`. `has_secret`, never the secret. |
| `PATCH /api/admin/captcha` | platform operator | `{provider?, site_key?, secret?, clear_secret?, forms?, clear_forms?, fallback?, verify_timeout_ms?}`; an empty string clears a field (the environment applies again). 400 `captcha_bad_value` / `captcha_out_of_bounds`, 409 `captcha_no_secret_key`. The audit row names the fields, never a value. |
| `POST /api/admin/captcha/test` | platform operator | `{provider?, site_key?, secret?}` (the page's values, saved or not) → `{ok, reachable, secret_ok, codes, misconfigured, cause, message}`. |

The protected requests carry the fields in their JSON body:
`POST /api/auth/login` (`login` / `demo_login`),
`POST /api/public/s/{token}/pin` and `POST /api/public/d/{token}/pin`
(`share_password`). The refusals are filex's error envelope
([API-ERRORS.md](API-ERRORS.md)) with the bot check's own field:

```json
{"error": "captcha_required", "message": "Please confirm that you are not a robot, then try again.", "captcha": "missing_token"}
{"error": "captcha_fallback", "message": "…", "captcha": "fallback", "challenge": {"algorithm": "SHA-256", "salt": "…", "hash": "…", "signature": "…", "maxnumber": 200000}}
```

## Testing

Cloudflare publishes test keys that work on any host, `localhost` included:
site key `1x00000000000000000000AA` passes and `2x00000000000000000000AB`
blocks; secret `1x0000000000000000000000000000000AA` passes and
`2x0000000000000000000000000000000AA` fails. The passing test key hands out the
same dummy token every time, so on one installation a second success within
five minutes is refused as `replayed` - a real key never does that. To see the
fallback, point the server's call at an address that does not answer:
`CAPTCHA_TURNSTILE_SITEVERIFY=http://127.0.0.1:1/` (and
`CAPTCHA_RECAPTCHA_SITEVERIFY`) - for tests only, never in production.
