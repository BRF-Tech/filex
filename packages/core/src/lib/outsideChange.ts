// A document open in the office editor that changed OUTSIDE it (issue #184).
//
// The same rule on every surface — the desktop's "Open with filex" window, the
// web explorer, the standalone editor tab, the embeds — because the editor is
// the same component on all of them (PreviewModal). What differs is only who
// NOTICES the change: the desktop app watches the document on the computer,
// the web hears it from the server's realtime feed. Either hands the viewer
// an OutsideChange; the viewer decides, says, and answers.
//
//   · nothing of this editor's is unsaved → the new version is loaded, and a
//     short note says so ("updated outside filex, the new version is loaded");
//   · there IS something of this editor's (an edit made here, or a save the
//     host holds) → the person is asked which one stays:
//       Keep theirs  — this editor's edits are dropped, the new version loads;
//       Keep mine    — this editor's version replaces it when saved;
//       Keep both    — the outside version stays, this editor's is saved
//                      beside it as a conflict copy.
//     Until they answer nothing is written over anything.

/** One change, handed to the viewer by its host. */
export interface OutsideChange {
  /** Rises with every change the host sees: the same change delivered twice
   *  is one change, and an answer names the change it answers. */
  seq: number;
  /**
   * Where the new version is, when it is not where the open document is. The
   * desktop app puts each outside version on the server as a NEW working copy
   * (so a late save of the old editor session can never land on it); the web
   * reloads the same path and leaves this out.
   */
  path?: string | null;
  /** The host holds a save of this editor's that is not written yet: the
   *  question is asked even when the editor has no unsaved edit of its own. */
  pending?: boolean;
}

/** What became of a change. 'reloaded' = it was taken in without a question. */
export type OutsideChoice = 'reloaded' | 'theirs' | 'mine' | 'both';

/** The viewer's answer to its host. */
export interface OutsideAnswer {
  seq: number;
  choice: OutsideChoice;
  /** The path the document is open at now (the new one after a reload). */
  path: string;
  /** The office session the answer is about (ONLYOFFICE `document.key`), when
   *  there was one: the server needs it to know which save to keep where. */
  key?: string | null;
  /**
   * Who noticed the change: the host (its `outsideChange` prop - the desktop
   * app) or the viewer itself, from the server's realtime feed. Only a host's
   * change is the host's to act on; the viewer tells the server about its
   * own (officeSession).
   */
  origin?: 'host' | 'server';
}

/** What the server says about an editing session (POST
 *  /api/files/onlyoffice/session): `stale` - a save of it would be kept beside
 *  the document, not written over it. */
export interface OfficeSessionState {
  stale: boolean;
  known: boolean;
}

/** The diagnosis endpoint's sibling: `/…/onlyoffice/diagnose` (lib/
 *  officeDiagnosis officeDiagnoseEndpoint, from the config endpoint) →
 *  `/…/onlyoffice/session`. */
export function officeSessionEndpoint(diagnoseEndpoint: string | null): string | null {
  return diagnoseEndpoint ? diagnoseEndpoint.replace(/diagnose$/, 'session') : null;
}

/**
 * Asks the server about the session (`state`), or gives it the person's answer
 * (`mine`: write my version over the outside one; `theirs`: drop my session's
 * save). null when the server could not say (an older server, no permission):
 * the caller then does nothing, and the server keeps a stale save beside the
 * document, which loses nothing.
 *
 * An answer carries `token`, the signed editor configuration's own token: the
 * server takes an answer only from somebody it handed an editing session of
 * that key, and the token is how any of its instances can tell (0.54).
 */
export async function officeSession(
  request: (url: string, init: RequestInit) => Promise<Response>,
  endpoint: string,
  body: { path: string; key: string; action?: 'state' | 'mine' | 'theirs'; token?: string },
): Promise<OfficeSessionState | null> {
  try {
    const res = await request(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'state', ...body }),
    });
    if (!res.ok) return null;
    const j = (await res.json()) as Partial<OfficeSessionState>;
    return { stale: j.stale === true, known: j.known === true };
  } catch {
    return null;
  }
}

/**
 * Reload without asking only when nothing of this editor's would be lost.
 *
 * ⚠ "Edited" is sticky for the life of one editor: ONLYOFFICE's
 * onDocumentStateChange says `true` while the person types and `false` once
 * the edits have reached the DOCUMENT SERVER — not the file. The file gets
 * them only when the session ends (or on a forced save), so `false` must not
 * be read as "saved".
 */
export function outsideAction(edited: boolean, change: Pick<OutsideChange, 'pending'>): 'reload' | 'ask' {
  return edited || change.pending ? 'ask' : 'reload';
}

/**
 * The document's name as its owner knows it, for the question. The desktop
 * app's working copies are `<12 hex>-<name>` inside `.filex-open`
 * (desktop/src/openwith.ts scratchBasename); the prefix is machinery.
 */
export function outsideDocumentName(path: string, basename: string): string {
  if (/(^|\/|:\/\/)\.filex-open\//.test(path)) return basename.replace(/^[0-9a-f]{12}-/, '');
  return basename;
}
