/**
 * viewPrefsHttp — THE transport `lib/viewPrefs` reads and writes through, in
 * one place for every host.
 *
 * ⚠ Written once because there are two callers now and a copy each would
 * drift (filex lesson #119): the explorer attaches it when it mounts, and the
 * admin app attaches it the moment a person has signed in — the admin panel's
 * tables keep their columns in the same per-person document
 * (`lib/tablePrefs`), and a person can open their settings and change their
 * default folder view without ever having opened the explorer. Whichever
 * attaches first wins; the second call is a no-op (`attachViewPrefsStore`).
 *
 * ⚠ The answer carries TWO things: the person's document (`prefs`) and the
 * operator's default folder view (`instance_default`). The second is handed to
 * `setInstanceFolderDefault` and deliberately NOT returned as part of the
 * document — it is the operator's answer, and folding it into the person's
 * document would save it back as their choice (the palette made exactly that
 * mistake this release; see `setInstanceFolderDefault`).
 */
import { attachViewPrefsStore, setInstanceFolderDefault } from './viewPrefs';

export interface ViewPrefsHttpOptions {
  /** The server's base URL, '' for same-origin. */
  apiBase?: string;
  /** Authorization and friends. May be async (the desktop app fetches its
   *  credential from the main process per call). ⚠ Awaited — a Promise spread
   *  into a headers object sends the request with no Authorization at all
   *  (filex lesson #10). */
  headers?: () => Record<string, string> | Promise<Record<string, string>>;
  credentials?: RequestCredentials;
}

export function viewPrefsHttpTransport(opts: ViewPrefsHttpOptions) {
  const url = `${(opts.apiBase ?? '').replace(/\/+$/, '')}/api/files/manager/view-prefs`;
  const credentials = opts.credentials ?? 'same-origin';
  return {
    async load(): Promise<unknown> {
      const res = await fetch(url, {
        headers: { ...((await opts.headers?.()) ?? {}) },
        credentials,
      });
      /* ⚠ A 401 is not an error here, it is an ANSWER: an app token or a
       * public share link has no person to remember anything for. Null
       * degrades to "remember nothing, write nothing". */
      if (!res.ok) return null;
      const body = (await res.json()) as { prefs?: unknown; instance_default?: unknown };
      setInstanceFolderDefault(body?.instance_default ?? null);
      return body?.prefs ?? null;
    },
    save(doc: unknown): void {
      void (async () => {
        try {
          const payload = JSON.stringify({ prefs: doc });
          await fetch(url, {
            method: 'PUT',
            headers: {
              ...((await opts.headers?.()) ?? {}),
              'Content-Type': 'application/json',
            },
            credentials,
            body: payload,
            /* ⚠ `keepalive` is what lets the save fired on `pagehide` outlive
             * the page — but browsers reject a keepalive body over 64 KB
             * outright, so the flag goes only on documents that fit. */
            keepalive: payload.length < 60000,
          });
        } catch {
          /* Fire and forget: the next save carries the whole document. */
        }
      })();
    },
  };
}

/** Attach the HTTP transport (a no-op when one is already attached). */
export function attachViewPrefsHttp(opts: ViewPrefsHttpOptions): void {
  attachViewPrefsStore(viewPrefsHttpTransport(opts));
}
