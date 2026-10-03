// The webhook-v2 event catalogue the admin UI offers for subscription.
//
// ⚠ This list is a deliberate hand-maintained mirror of the dotted EventType
// constants in `backend/internal/notify/event.go`, and
// `web/tests/webhooks/eventCatalog.test.ts` fails the build the moment the two
// diverge or an entry loses its en/tr label.
//
// Why a mirror rather than an endpoint that lists them: the admin UI is
// compiled INTO the server binary (`backend/embed`, `//go:embed all:admin
// all:web`), so the two ship as one artifact and can never disagree at
// runtime. An endpoint would add a request and a real failure mode — a
// checkbox list that renders empty when the call fails, i.e. an operator who
// cannot subscribe to anything — to buy freshness that is impossible to lose.
// The drift is a build-time problem, so it gets a build-time gate.
//
// The order here is the order of the checkboxes: the write path first, then
// the things that go wrong, then the sharing surfaces.
export const WEBHOOK_EVENTS = [
  'file.uploaded',
  'file.updated',
  'file.upload_failed',
  'file.infected',
  'file.deleted',
  'file.trashed',
  'file.moved',
  'archive.created',
  'archive.extracted',
  'share.created',
  'drop.received',
  'comment.added',
  'e2e.escrow_used',
  'e2e.password_changed',
  'e2e.request_created',
  'e2e.request_decided',
  'plugin.notice',
] as const;

export type WebhookEvent = (typeof WEBHOOK_EVENTS)[number];

/**
 * The event name as an i18n key SEGMENT.
 *
 * vue-i18n reads `.` as a path separator, so the event name cannot be a key on
 * its own: `webhooks.events.file.uploaded` would look for a nested `file`
 * object. The dots become underscores instead.
 */
export function eventSlug(event: string): string {
  return event.replace(/\./g, '_');
}

/**
 * i18n key for an event's OPERATOR label — the admin webhook screen.
 *
 * These read like the log line they describe ("File updated — a write replaced
 * the bytes of an existing file"), because the person ticking the box is
 * wiring a delivery and needs to know exactly which write fires it.
 */
export function webhookEventKey(event: string): string {
  return `webhooks.events.${eventSlug(event)}`;
}

/**
 * i18n key for an event's END-USER label — the per-event switches in the user
 * settings dialog.
 *
 * ⚠ A separate catalogue on purpose, not a second spelling of the same one.
 * The switches borrowed the operator sentences for a release, and the result
 * was a list of rows explaining write semantics to somebody who had opened
 * "What to tell me about" to stop being pinged about comments. The two
 * audiences want different sentences about the same event, and one string
 * cannot be both — so the difference is stored rather than negotiated, and
 * `web/tests/webhooks/eventCatalog.test.ts` fails when an event arrives
 * without either of them.
 */
export function userEventKey(event: string): string {
  return `userSettings.notifications.events.${eventSlug(event)}`;
}

/** The facts that decide whether an event can happen here at all, and who could
 *  make it so. */
export interface EventPossibility {
  antivirus?: boolean;
  e2e_escrow?: { enabled?: boolean } | null;
  app_plugins?: { enabled?: boolean } | null;
  /** The server's verdict on the CALLER (`capabilities.caller_admin`): may this
   *  caller set the INSTANCE up — a single-tenant install's administrators, or
   *  the supertenant's. Decides who could switch a service on (`eventFixableBy`). */
  caller_admin?: boolean;
  /** The signed-in ACCOUNT is an administrator — of its tenant, of the
   *  supertenant, or of a single-tenant install (`role === 'admin'`): the
   *  accounts a new encryption request is sent to (backend notify/bell.go
   *  `bellFor`). ⚠ NOT published by the server, so not in the capabilities:
   *  whoever calls fills it in from the account's own role — the settings
   *  dialog's `host.isAdmin`, the admin app's `auth.isAdmin`. And not
   *  `caller_admin`, which on a multi-tenant install is the supertenant's alone. */
  account_admin?: boolean;
  /** `capabilities.e2e_policy`, the caller's own tenant's row: `available` is
   *  the platform operator's switch, `policy` the tenant's choice (`off` |
   *  `admins` | `permitted` | `approval`). */
  e2e_policy?: { available?: boolean; policy?: string } | null;
}

/**
 * Can this event happen on THIS instance? A person choosing what to be told
 * about is offered only what can reach them.
 *
 * ⚠ Measured in the release-candidate sweep (2026-09-21): the settings dialog
 * offered "A virus is found in a file" on an instance with scanning off, and
 * "An encrypted folder is opened with the escrow key" on one with no escrow
 * key — two switches that can never fire, read by somebody deciding what
 * matters to them. Each rule names what the event depends on; every other
 * event can happen anywhere. The two encryption request events depend on the
 * tenant's policy, and the first also on who is asking.
 *
 * ⚠ For the person's own switches only. An operator wiring a webhook target
 * may subscribe ahead of turning a service on, so the Webhooks screen keeps
 * the whole catalogue.
 */
export function eventPossible(event: string, caps: EventPossibility): boolean {
  return eventOffReason(event, caps) === null;
}

/**
 * WHY an event cannot happen on this instance — the i18n key of the sentence
 * that says which service is off and where it is switched on — or null when
 * it can.
 *
 * ⚠ The same split as every other "needs a service" entry (packages/core
 * lib/serviceGate `gateOnService`, the owner's rule of 2026-09-21): an
 * administrator, who can switch the service on (`eventFixableBy`), sees the
 * switch greyed with this sentence; everybody else is not offered it at all.
 * The Webhooks screen keeps every event subscribable (an operator may
 * subscribe ahead of turning the service on) and prints the sentence beside
 * the box instead.
 */
export function eventOffReason(event: string, caps: EventPossibility): string | null {
  switch (event) {
    case 'file.infected':
      return caps.antivirus === true ? null : 'webhooks.offReason.antivirus';
    case 'e2e.escrow_used':
      return caps.e2e_escrow?.enabled === true ? null : 'webhooks.offReason.escrow';
    case 'plugin.notice':
      return caps.app_plugins?.enabled === true ? null : 'webhooks.offReason.appPlugins';
    /* wiring:e2 policy — both events exist only under the `approval` policy
       (backend internal/e2epolicy requests.go announce). A NEW request is sent
       to administrator ACCOUNTS alone — a tenant's own, the supertenant's, a
       single-tenant install's (notify/bell.go `bellFor`: a member's bell never
       reads it); the answer goes to the person who asked.
       ⚠ The account's role, not `caller_admin`: that is "may set the instance
       up", the supertenant's alone on a multi-tenant install, and it left a
       tenant's own administrator — the person these requests are for —
       without the switch. */
    case 'e2e.request_created':
      return caps.account_admin === true && asksForApproval(caps) ? null : 'webhooks.offReason.e2eApproval';
    case 'e2e.request_decided':
      return asksForApproval(caps) ? null : 'webhooks.offReason.e2eApproval';
    default:
      return null;
  }
}

/** Does the caller's tenant want an administrator's approval before anything
 *  new is encrypted, with encryption available to it at all? */
function asksForApproval(caps: EventPossibility): boolean {
  const p = caps.e2e_policy;
  return p?.available === true && p.policy === 'approval';
}

/**
 * Could THIS person switch on what the event is waiting for? — the
 * `callerAdmin` of lib/serviceGate `gateOnService`, which greys the switch
 * (with the reason) for whoever can and leaves everybody else without it.
 *
 * A service — scanning, the escrow key, apps — is the INSTANCE's to set up, and
 * "may this caller set it up" is the server's answer (`caller_admin`; docs/
 * CONTRIBUTING.md, "A service that is not there"). The two encryption request
 * events wait for the TENANT's policy, which the tenant's own administrators
 * set (Admin → Encryption): an administrator account (`account_admin`).
 *
 * ⚠ The one place a role is read on purpose. `caller_admin` is the
 * supertenant's alone on a multi-tenant install and would leave a tenant's own
 * administrators out; the server publishes nothing finer, and it sends the
 * request by the same role (notify/bell.go `bellFor`).
 */
export function eventFixableBy(event: string, who: EventPossibility): boolean {
  switch (event) {
    case 'e2e.request_created':
    case 'e2e.request_decided':
      return who.account_admin === true;
    default:
      return who.caller_admin === true;
  }
}
