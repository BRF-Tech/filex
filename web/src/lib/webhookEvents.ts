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
  'share.created',
  'drop.received',
  'comment.added',
  'e2e.escrow_used',
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
