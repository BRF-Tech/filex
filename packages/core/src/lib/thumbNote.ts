/**
 * thumbNote - why a file has no thumbnail, said ON the file (filex 0.50).
 *
 * A listing carries `thumb_note` on a file whose thumbnail will not come
 * because of the file itself (backend thumb/notes.go, one table):
 *
 *   corrupt    it could not be read: damaged or incomplete
 *   encrypted  a password on an office document, a zip's encrypted entries,
 *              or filex's own end-to-end encryption
 *   too_large  over a preview size limit (office, SVG, archive, an app's)
 *
 * The file keeps its type icon, and a small marker with an icon sits in its
 * corner; resting the pointer on it, or tapping it, says the sentence below.
 * A failure that may pass (the network, a busy document server) has no note:
 * it is drawn again later.
 *
 * ⚠ ONE reader, for the reason lib/unavailable is one: the list, the grid
 * and the gallery all take the note, its words and its icon from here, and
 * ThumbTile draws it, so the three views cannot disagree. The marker belongs
 * to the type icon, never to a picture: a file with a thumbnail has no note,
 * and the list's type badge (lib/filePreview thumbTypeBadge) is only drawn
 * on a picture, so the two never meet.
 */
import type { FileNode, ThumbNote } from '../types/FileNode';

/** What `thumbNoteWords` needs from the caller's locale. */
export interface ThumbNoteHost {
  t: (key: string, vars?: Record<string, string | number>) => string;
}

/** The words and the icon of one note. */
export interface ThumbNoteWords {
  note: ThumbNote;
  /** One or two words: "Damaged". */
  label: string;
  /** Why there is no thumbnail, in a sentence. */
  why: string;
  /** `label` and `why` together: the tooltip, the accessible name. */
  full: string;
  /** The icon, as the `d` of 24x24 stroked paths (the icon set's style). */
  paths: readonly string[];
}

/** The notes this client knows; any other value is ignored. */
export const THUMB_NOTES: readonly ThumbNote[] = ['corrupt', 'encrypted', 'too_large'];

/** The icon of each note: a page with a cross, a padlock, arrows apart. */
const ICONS: Record<ThumbNote, readonly string[]> = {
  corrupt: [
    'M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z',
    'M14 2v4a2 2 0 0 0 2 2h4',
    'm14.5 12.5-5 5',
    'm9.5 12.5 5 5',
  ],
  encrypted: ['M5 11h14a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2Z', 'M7 11V7a5 5 0 0 1 10 0v4'],
  too_large: ['M15 3h6v6', 'M9 21H3v-6', 'M21 3l-7 7', 'M3 21l7-7'],
};

/** Every wording handed out, by note and words (see thumbNoteWords). */
const WORDS = new Map<string, ThumbNoteWords>();

/** The note of a file that has no thumbnail, or null. */
export function thumbNoteOf(
  node: Pick<FileNode, 'type' | 'thumb_note' | 'thumb_url'> | null | undefined,
): ThumbNote | null {
  if (!node || node.type !== 'file' || node.thumb_url) return null;
  const n = node.thumb_note;
  return n && (THUMB_NOTES as readonly string[]).includes(n) ? n : null;
}

/**
 * The words for a file's note, or `null` when it has none.
 *
 * ⚠ Literal `t('…')` keys on purpose: `web/tests/i18n/coreKeysUsed.test.ts`
 * scans the source for them and skips anything computed.
 */
export function thumbNoteWords(
  node: Pick<FileNode, 'type' | 'thumb_note' | 'thumb_url'> | null | undefined,
  host: ThumbNoteHost,
): ThumbNoteWords | null {
  const note = thumbNoteOf(node);
  if (!note) return null;
  let label: string;
  let why: string;
  switch (note) {
    case 'corrupt':
      label = host.t('thumbNote.corrupt');
      why = host.t('thumbNote.corrupt_why');
      break;
    case 'encrypted':
      label = host.t('thumbNote.encrypted');
      why = host.t('thumbNote.encrypted_why');
      break;
    default:
      label = host.t('thumbNote.too_large');
      why = host.t('thumbNote.too_large_why');
  }
  const full = host.t('thumbNote.full', { label, why });
  // One object per note and wording, so a re-render hands ThumbTile the same
  // value and the tile has nothing to compare (as thumbTypeBadge does).
  const key = `${note}|${full}`;
  let words = WORDS.get(key);
  if (!words) {
    words = { note, label, why, full, paths: ICONS[note] };
    WORDS.set(key, words);
  }
  return words;
}
