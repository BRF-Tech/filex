/**
 * openWith - "always open this kind with this app", kept on the person's
 * ACCOUNT (0.50, docs/APP-PLUGINS.md → Default apps).
 *
 * ONE record per ACCOUNT (the maintainer, 2026-10-01): the browser, the desktop app
 * and every embed read and write the same choices. They arrive with the
 * surface's preference document (lib/prefs `openWith`, a JSON object
 * `{ext: handlerId}` the server puts in) and change one kind at a time
 * through `/api/me/open-with` (backend handlers/openwith.go) - never with the
 * surface document, whose whole-document PUT would let a copy read at boot
 * undo a choice made since on another surface. The handler is `builtin`
 * (filex's own viewer), `app:<plugin>/<view>`, or - for a kind ONLYOFFICE
 * opens as a choice (lib/appViewer OFFICE_OPEN_KINDS: `.csv`, filex 0.51) -
 * `onlyoffice`. A choice of ONLYOFFICE is kept while ONLYOFFICE is switched
 * off, and the kind opens in filex's own viewer meanwhile.
 *
 * ⚠ Where the chosen app cannot open the file - this surface does not offer
 * it, or the administrator switched it off for the kind - the next handler
 * that is on opens it (lib/appViewer), and the choice is kept for where it
 * can.
 *
 * ⚠ A choice is only ever USED among the handlers the administrator left on
 * for the kind (lib/appViewer `pickAppViewer`): one switched off since is
 * kept, not followed, and Settings → Preferences → Default apps says it is no longer
 * available. Nothing here decides what is on - the server's rules do.
 */
import { ref } from 'vue';
import { accountFetch, currentPrefs, onPrefs, setAccountPref } from './prefs';
import { ONLYOFFICE_VIEWER, officeOpensKind } from './appViewer';

/** Bumped on every change this page makes, so computed readers re-run. */
const version = ref(0);

function readAll(): Record<string, string> {
  const raw = currentPrefs().openWith;
  if (!raw) return {};
  try {
    const v = JSON.parse(raw);
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {};
    const out: Record<string, string> = {};
    for (const [k, id] of Object.entries(v as Record<string, unknown>)) {
      if (typeof id === 'string' && validKind(k) && validHandler(id, k)) out[k] = id;
    }
    return out;
  } catch {
    return {};
  }
}

/** A kind: a file name's extension, lower-case, no dot (backend assoc.ValidExt). */
export function validKind(ext: string): boolean {
  return /^[a-z0-9][a-z0-9_+-]{0,31}$/.test(ext);
}

/**
 * A handler id the open capability knows: `builtin`, `app:<plugin>/<view>`,
 * or `onlyoffice` for a kind ONLYOFFICE opens as a choice (with `kind`
 * given, only for those; backend handlers/openwith.go validOpenChoice).
 */
export function validHandler(id: string, kind?: string): boolean {
  if (id === ONLYOFFICE_VIEWER) return kind === undefined || officeOpensKind(kind);
  return id === 'builtin' || /^app:[a-z0-9][a-z0-9_-]{0,31}\/[a-z0-9][a-z0-9_.-]{0,63}$/.test(id);
}

/** Every choice this person made, by kind. Reactive. */
export function openWithChoices(): Record<string, string> {
  void version.value;
  return readAll();
}

/** The person's choice for a kind, null for none. Reactive. */
export function openWithChoice(ext: string | null | undefined): string | null {
  if (!ext) return null;
  return openWithChoices()[ext.toLowerCase()] ?? null;
}

/** The account's answer becomes this page's copy (what the server kept). */
async function send(path: string, init: { method: string; body?: string }): Promise<void> {
  const res = await accountFetch(path, init);
  if (!res || !res.ok) return;
  try {
    const body = (await res.json()) as { choices?: unknown };
    if (!body || !body.choices || typeof body.choices !== 'object' || Array.isArray(body.choices)) return;
    const out: Record<string, string> = {};
    for (const [k, id] of Object.entries(body.choices as Record<string, unknown>)) {
      if (typeof id === 'string' && validKind(k) && validHandler(id, k)) out[k] = id;
    }
    setAccountPref('openWith', JSON.stringify(out));
    version.value++;
  } catch {
    /* the change is on screen already; the next boot reads the account */
  }
}

/** Keep (or, with null, forget) the person's choice for a kind. */
export function setOpenWithChoice(ext: string, id: string | null): void {
  const kind = ext.toLowerCase();
  if (!validKind(kind) || (id !== null && !validHandler(id, kind))) return;
  const all = readAll();
  if (id === null) delete all[kind];
  else all[kind] = id;
  setAccountPref('openWith', JSON.stringify(all));
  version.value++;
  const path = `/api/me/open-with/${encodeURIComponent(kind)}`;
  void send(path, id === null ? { method: 'DELETE' } : { method: 'PUT', body: JSON.stringify({ handler: id }) });
}

/** Forget every choice. */
export function clearOpenWithChoices(): void {
  setAccountPref('openWith', '{}');
  version.value++;
  void send('/api/me/open-with', { method: 'DELETE' });
}

/**
 * Re-read the choices when the account's answer lands (another device changed
 * them). Call once from a component's setup; returns the unsubscribe.
 */
export function followOpenWithChoices(): () => void {
  return onPrefs(() => {
    version.value++;
  });
}
