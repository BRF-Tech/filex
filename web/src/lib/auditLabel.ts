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

export function auditActionLabel(action: string, t: T, te: TE): string {
  if (!action) return '—';
  const { resource, verb } = splitAction(action);
  const rk = `audit.resource.${keyOf(resource)}`;
  const vk = `audit.verb.${keyOf(verb)}`;
  const r = te(rk) ? t(rk) : readable(resource);
  if (!verb) return r;
  const v = te(vk) ? t(vk) : readable(verb);
  return t('audit.phrase', { resource: r, verb: v });
}

export function auditTargetLabel(type: string | null | undefined, id: string | number | null | undefined, t: T, te: TE): string {
  if (!type) return id ? String(id) : '';
  const tk = `audit.target.${keyOf(type)}`;
  const name = te(tk) ? t(tk) : readable(type);
  if (id === null || id === undefined || id === '') return name;
  return /^\d+$/.test(String(id)) ? `${name} #${id}` : `${name} “${id}”`;
}
