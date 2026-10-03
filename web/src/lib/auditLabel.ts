/**
 * What an audit row says, in the reader's language.
 *
 * The server stores an action as `<resource>.<verb>` (`user.password_reset`,
 * `storage.sync_trigger`, `ai.file.move`) and a target as a type plus an id.
 * Those are wire names: the dashboard printed them as they are, so the
 * "Recent activity" card read `user.update` over `admin@… · user:12` — a log
 * line, not a sentence, and in English on a Turkish panel.
 *
 * ⚠ The set of actions is OPEN. `auth/audit_middleware.go` falls back to
 * `<admin segment>.<method verb>` for every mutating /api/admin/* route it has
 * no case for, and `ai.file.<verb>` for every AI verb, so a lookup table of
 * whole action names would go stale the day a route is added. The label is
 * therefore COMPOSED: the resource and the verb are translated separately and
 * joined by one phrase. A resource or verb the catalogue does not know is
 * still printed readably (`replication targets`), never dropped.
 *
 * `web/tests/lib/auditLabel.test.ts` reads the Go source and fails when a
 * fixed action name has no translation in either language.
 */

import { DEMO_MASKED_ADDRESS } from '@/lib/format';

type T = (key: string, values?: Record<string, unknown>) => string;
type TE = (key: string) => boolean;

const keyOf = (s: string) => s.replace(/[.-]/g, '_');

/** A name the catalogue lacks, made readable: `replication-targets` → `replication targets`. */
const readable = (s: string) => s.replace(/[._-]+/g, ' ').trim();

/** Split `ai.file.move` into resource `ai.file` and verb `move`. */
export function splitAction(action: string): { resource: string; verb: string } {
  const i = action.lastIndexOf('.');
  if (i <= 0) return { resource: action, verb: '' };
  return { resource: action.slice(0, i), verb: action.slice(i + 1) };
}

/** A resource's name; `ai.<resource>` (a write through the AI admin surface,
 *  auth.AIAdminAction) is the same resource, marked as taken through AI. */
function resourceLabel(resource: string, t: T, te: TE): string {
  const rk = `audit.resource.${keyOf(resource)}`;
  if (te(rk)) return t(rk);
  if (resource.startsWith('ai.') && resource.length > 3) {
    return t('audit.viaAi', { resource: resourceLabel(resource.slice(3), t, te) });
  }
  return readable(resource);
}

export function auditActionLabel(action: string, t: T, te: TE): string {
  if (!action) return '-';
  const { resource, verb } = splitAction(action);
  const vk = `audit.verb.${keyOf(verb)}`;
  const r = resourceLabel(resource, t, te);
  if (!verb) return r;
  const v = te(vk) ? t(vk) : readable(verb);
  return t('audit.phrase', { resource: r, verb: v });
}

/**
 * The thing a row is about: its kind, and WHICH one.
 *
 * `name` is the server's answer (`target_name`: a user's e-mail, a storage's
 * name, a file's path — handlers/audit_targets.go). ⚠ Without it the Panel
 * read "Kullanıcı: oluşturuldu — Kullanıcı" and "Depo #1" (release-candidate
 * sweep, 2026-09-21): the kind, and an id nobody can read. An id is shown
 * only when there is no name, and a numeric one as `#12` rather than as if it
 * were a name.
 */
export function auditTargetLabel(
  type: string | null | undefined,
  id: string | number | null | undefined,
  t: T,
  te: TE,
  name?: string | null,
): string {
  // A public demo masks an address a row is about (an address lock): the
  // server's "hidden on the demo" is said in the reader's language.
  if (id === DEMO_MASKED_ADDRESS) id = t('demo.hiddenAddress');
  if (name === DEMO_MASKED_ADDRESS) name = t('demo.hiddenAddress');
  const tk = type ? `audit.target.${keyOf(type)}` : '';
  /* ⚠ The middleware's generic fallback (any other /api/admin/* write) names
     its target by the route segment: `app-plugins`, `replication-targets`.
     Those have no `audit.target.*` entry, and the Panel read "Uygulama:
     oluşturuldu — app plugins" on a Turkish screen (0.50 measurement). The
     resource's own word, the one the action label already uses, comes
     before the raw text. */
  const rk = type ? `audit.resource.${keyOf(type)}` : '';
  const kind = type ? (te(tk) ? t(tk) : te(rk) ? t(rk) : readable(type)) : '';
  if (name) return kind ? `${kind} “${name}”` : name;
  if (!type) return id ? String(id) : '';
  if (id === null || id === undefined || id === '') return kind;
  return /^\d+$/.test(String(id)) ? `${kind} #${id}` : `${kind} “${id}”`;
}

/**
 * The resources the Audit page offers to filter by: every `audit.resource.*`
 * the catalogue names, labelled in the reader's language. The value is the
 * `<resource>.` prefix the server understands (db.AuditActionPrefixes) — or
 * several, comma-separated, when two spellings share one name (`user.` and
 * `users.`: the fixed case and the admin route's generic one).
 *
 * ⚠ The filter was a free-text box matched EXACTLY against wire names
 * ("user.create") — typing what the page shows ("Kullanıcı") found nothing.
 */
export function auditResourceOptions(
  resources: Record<string, unknown> | undefined,
  t: T,
): Array<{ value: string; label: string }> {
  const byLabel = new Map<string, string[]>();
  for (const key of Object.keys(resources ?? {})) {
    const label = t(`audit.resource.${key}`);
    const list = byLabel.get(label) ?? [];
    for (const wire of [WIRE_RESOURCE[key] ?? key].flat()) if (!list.includes(`${wire}.`)) list.push(`${wire}.`);
    byLabel.set(label, list);
  }
  return [...byLabel.entries()]
    .map(([label, prefixes]) => ({ value: prefixes.join(','), label }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

/** Catalogue keys whose wire resource is not the key itself (`keyOf` folds
 *  both `.` and `-` to `_`, so the way back has to be spelled out). */
const WIRE_RESOURCE: Record<string, string | string[]> = {
  // The fixed action is `login_security.update`, the admin route's own segment `login-security`.
  login_security: ['login_security', 'login-security'],
  ai_file: 'ai.file',
  ai_share: 'ai.share',
  ai_tokens: 'ai-tokens',
  replication_targets: 'replication-targets',
  auth_providers: 'auth-providers',
  app_plugins: 'app-plugins',
  plugin_requests: 'plugin-requests',
  webhook_config: 'webhook-config',
  smtp_test: 'smtp-test',
  file_types: 'file-types',
};
