// #184, the web half: an office document open in the viewer hears that it
// changed outside the editor (another person's save over WebDAV, a sync
// client, an agent on a mounted folder, a second editing session).
//
// The viewer joins its document's FOLDER on the realtime feed, the way the
// explorer joins the folder it shows (lib/realtime RealtimeClient, a folder
// room - not a recursive watch, which would catalogue a whole lazy subtree),
// and says which file it is on (presence, like the explorer's focus). A
// change frame naming the document is only "something happened to it": the
// caller asks the server whether its editing session is still on the current
// version (POST /api/files/onlyoffice/session) before it tells anybody,
// because the editor's OWN save announces itself the same way.
//
// The desktop app's working copies are not watched: nothing but the desktop
// app writes them, and it watches the person's file itself (desktop/src
// openwith.ts LocalDocMonitor).

import { RealtimeClient, type ChangeMessage, type WsTicket } from '../lib/realtime';
import { isInternalPath } from '../lib/internalPaths';

/** `<storage>://a/b.docx` → { dir: `<storage>://a`, name: `b.docx` }. */
export function documentFolderOf(path: string): { dir: string; name: string } | null {
  const idx = path.indexOf('://');
  if (idx < 0) return null;
  const storage = path.slice(0, idx + 3);
  const rel = path.slice(idx + 3).replace(/\/+$/, '');
  const cut = rel.lastIndexOf('/');
  const name = cut >= 0 ? rel.slice(cut + 1) : rel;
  if (!name) return null;
  return { dir: storage + (cut >= 0 ? rel.slice(0, cut) : ''), name };
}

/** Does this change frame (maybe) touch the file called `name`? A frame that
 *  names nothing (a merged burst) might. */
export function changeTouches(m: Pick<ChangeMessage, 'name' | 'new_name' | 'action'>, name: string): boolean {
  if (!m.name && !m.new_name) return true;
  return m.name === name || m.new_name === name;
}

export interface DocumentWatch {
  /** Watch the document at `path` (null, or an internal path: stop). */
  watch(path: string | null): void;
  stop(): void;
}

/**
 * `onMaybeChanged` is called, debounced, after a change frame that may be
 * about the document. No ticket (an old server, an embed without the realtime
 * endpoint) means no watch: the editor simply keeps the version it opened.
 */
export function useDocumentWatch(opts: {
  ticket: () => Promise<WsTicket | null>;
  onMaybeChanged: () => void;
  debounceMs?: number;
}): DocumentWatch {
  let client: RealtimeClient | null = null;
  let current: { dir: string; name: string } | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  function onChange(m: ChangeMessage): void {
    if (!current) return;
    if (m.path && m.path.replace(/\/+$/, '') !== current.dir.replace(/\/+$/, '')) return;
    if (!changeTouches(m, current.name)) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = undefined;
      opts.onMaybeChanged();
    }, opts.debounceMs ?? 400);
  }

  function stop(): void {
    if (timer) clearTimeout(timer);
    timer = undefined;
    current = null;
    client?.close();
    client = null;
  }

  function watch(path: string | null): void {
    const where = path && !isInternalPath(path) ? documentFolderOf(path) : null;
    if (!where) {
      stop();
      return;
    }
    current = where;
    if (!client) {
      client = new RealtimeClient({ getTicket: opts.ticket, handlers: { onChange } });
    }
    client.subscribe(where.dir);
    client.setFocus(where.name);
  }

  return { watch, stop };
}
