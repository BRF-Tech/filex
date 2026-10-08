/**
 * A notification's words, as the SERVER said them.
 *
 * ⚠⚠ The sentence is composed on the server, in Go, and nowhere else
 * (backend internal/notify say.go; the maintainers' decision, 2026-10-08:
 * "the browser sends a key and its values; the server builds the sentence and
 * sends it where it goes"). `GET /api/notifications` answers every row with
 * its `title` and `body` already said in the reader's language - a language
 * pack's included, right to left isolated - and the same code path says the
 * same row to a phone (Web Push), in an email and to a webhook. This module
 * composes NOTHING: there is no phrase table, no template and no plural rule
 * on this side any more (web/tests/quality/noClientNotificationText.test.ts
 * holds that).
 *
 * The one thing it does is the one thing the server cannot: the name of an
 * item inside an end-to-end encrypted folder, which the server only has
 * scrambled. Such a row says "🔒 Encrypted item" there, and carries `e2e` -
 * the same sentence cut at the names, and what each name is. A reader whose
 * explorer has that folder unlocked puts the real name in its place; every
 * other reader (the desktop app's toast, a push, an email) shows the server's
 * words as they are.
 *
 * ⚠ Plain TypeScript with no import and no framework: the bell (NotificationRow,
 * in the web and in the desktop app's window), the page's pop-up
 * (web useNotificationWatcher) and the admin history all read through it.
 */

/** One notification as a screen shows it. */
export interface NotificationText {
  title: string;
  body: string;
}

/** One piece of a sentence: words, or the name at `names[name]`. */
export interface NotificationTextPart {
  text?: string;
  name?: number;
}

/** One name inside an encrypted folder (backend model.NotificationE2EName). */
export interface NotificationE2EName {
  /** The item as listings address it, `<storage>://<path>`, as stored. */
  wire: string;
  /** The encrypted folder it is in, `<storage>://<root>`. */
  root: string;
  /** `name` - the item's own name; `path` - where it is. */
  part: string;
  /** What it reads as without the key: what `title`/`body` already say. */
  locked: string;
}

/** Where the encrypted names of a sentence stand (backend model.NotificationE2E). */
export interface NotificationE2E {
  title: NotificationTextPart[];
  body: NotificationTextPart[];
  names: NotificationE2EName[];
}

/** The shape this module reads - structural, so any row type passes. */
export interface NotificationLike {
  event: string;
  title?: string;
  body?: string;
  e2e?: NotificationE2E | null;
}

/** The plaintext of an item inside an encrypted folder, from a reader's
 *  explorer that has the folder unlocked; null when it does not. */
export type E2eNameResolver = (wire: string, root: string) => { name: string; path: string } | null;

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/**
 * The words a screen shows for one row: the server's, with an encrypted
 * item's real name where this reader can name it.
 */
export function notificationText(row: NotificationLike, e2eName?: E2eNameResolver): NotificationText {
  const said: NotificationText = { title: str(row.title), body: str(row.body) };
  const e2e = row.e2e;
  if (!e2e || typeof e2eName !== 'function' || !Array.isArray(e2e.names)) return said;
  const names = e2e.names.map((n) => {
    let known: { name: string; path: string } | null = null;
    try {
      known = e2eName(str(n?.wire), str(n?.root));
    } catch {
      known = null;
    }
    if (!known) return str(n?.locked);
    return n.part === 'path' ? str(known.path) : str(known.name);
  });
  const join = (parts: unknown, fallback: string): string => {
    if (!Array.isArray(parts)) return fallback;
    return parts
      .map((p: NotificationTextPart) => (typeof p?.name === 'number' ? (names[p.name] ?? '') : str(p?.text)))
      .join('');
  };
  return { title: join(e2e.title, said.title), body: join(e2e.body, said.body) };
}
