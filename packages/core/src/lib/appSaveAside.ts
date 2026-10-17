/**
 * appSaveAside - what an app's `file.save` asks beyond its bytes (filex
 * 0.57; docs/APP-PLUGINS-API.md → Saving beside the file, conflict copies):
 *
 *  - `beside: 'docx'` - the save is in a format the opened file cannot hold
 *    (an office editor's OOXML over a .doc): the server writes it beside the
 *    file under a name it chooses;
 *  - `conflict: 'copy'` - a file that changed since it was opened gets the
 *    save as a conflict copy instead of a refusal.
 *
 * Only their shape is checked here. Which formats may go beside a file, who
 * may create it there and what it is called are the server's rules
 * (backend internal/officesave, handlers app_ui_beside.go), the same for the
 * Document Server's saves - a second list here would drift from them.
 */

/** The read's answer header naming the version handed over as a save's
 *  `expect` names it, "<size>:<ms>" (backend app_ui_beside.go). */
export const VERSION_HEADER = 'X-Filex-Version';

/** The version a read's answer names, or undefined when it names none. */
export function versionOf(header: string | null | undefined): { size: number; modified: number } | undefined {
  const m = /^(\d{1,15}):(\d{1,15})$/.exec((header ?? '').trim());
  if (!m) return undefined;
  return { size: Number(m[1]), modified: Number(m[2]) };
}

/** A file extension as `beside` names it: lower case, no dot. */
const EXT = /^[a-z0-9]{2,8}$/;

/** The save's query values for `beside` and `conflict`; null when either is
 *  not one (the bridge answers `invalid`). */
export function saveAsideQuery(p: { beside?: unknown; conflict?: unknown } | null | undefined): { beside?: string; conflict?: 'copy' } | null {
  const out: { beside?: string; conflict?: 'copy' } = {};
  const beside = p?.beside;
  if (beside !== undefined && beside !== null && beside !== '') {
    if (typeof beside !== 'string') return null;
    const ext = beside.trim().replace(/^\./, '').toLowerCase();
    if (!EXT.test(ext)) return null;
    out.beside = ext;
  }
  const conflict = p?.conflict;
  if (conflict !== undefined && conflict !== null) {
    if (conflict !== 'copy') return null;
    out.conflict = 'copy';
  }
  return out;
}
