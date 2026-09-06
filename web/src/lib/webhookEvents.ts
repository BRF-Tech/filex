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
 * i18n key for an event's operator-readable label.
 *
 * vue-i18n reads `.` as a path separator, so the event name cannot be a key on
 * its own: `webhooks.events.file.uploaded` would look for a nested `file`
 * object. The dots become underscores instead.
 */
export function webhookEventKey(event: string): string {
  return `webhooks.events.${event.replace(/\./g, '_')}`;
}
