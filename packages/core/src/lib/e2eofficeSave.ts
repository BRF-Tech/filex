/**
 * e2eofficeSave - when an encrypted co-editing session is saved, and by whom
 * (task #189; design: docs/E2E-OFFICE.md, "Saving").
 *
 * The server cannot save: it has no key. So a browser does, and every browser
 * in the session has to agree which one without asking anybody: each folds
 * the same log (the relay's join, leave and saved entries, the changes'
 * places and times) into the same state and reaches the same answer.
 *
 * The rules the maintainers settled:
 *   - while there are unsaved changes, a version every 10 minutes, counted
 *     from the first change after the last save, written by the saver: the
 *     writer online who joined first;
 *   - Save (the editor's button, Ctrl+S) saves at once, by whoever pressed it;
 *   - the last writer to leave saves what is not saved yet;
 *   - unsaved changes nobody saved stay in the session for 30 days
 *     (backend/internal/e2eoffice DefaultUnsavedRetention);
 *   - in a vault (level 3) the editor runs alone: no relay, nothing reaches
 *     the server until the save, so a vault does not tell the server that
 *     somebody is editing which document.
 *
 * ⚠ Prototype: no surface uses this yet.
 */

/** A version every 10 minutes while there are unsaved changes. */
export const OFFICE_E2E_AUTOSAVE_MS = 10 * 60 * 1000;

/** Unsaved changes wait this many days for somebody to save them. */
export const OFFICE_E2E_UNSAVED_KEEP_DAYS = 30;

/** One member, as the log says it joined. */
export interface OfficeMember {
  client: string;
  /** The seq of its join entry: the earlier, the older. */
  joinSeq: number;
  canEdit: boolean;
}

/** What every browser derives from the log, the same everywhere. */
export interface OfficeSaveState {
  members: OfficeMember[];
  /** The seq of the last changes entry. */
  changesHead: number;
  /** The last entry the file holds (the last saved entry's `through`). */
  savedThrough: number;
  /** When (relay time, ms) the first change after the last save landed; null when saved. */
  dirtySince: number | null;
}

/** A log entry, as much of it as saving needs. */
export interface OfficeSaveEntry {
  seq: number;
  kind: string;
  client: string;
  /** Relay time, Unix ms. */
  at: number;
  /** join: whether the member may write. */
  canEdit?: boolean;
  /** saved: the last entry the saved file holds. */
  through?: number;
}

export function emptySaveState(): OfficeSaveState {
  return { members: [], changesHead: 0, savedThrough: 0, dirtySince: null };
}

/** The state after one more entry. Pure: the same log gives the same state. */
export function foldSaveState(s: OfficeSaveState, e: OfficeSaveEntry): OfficeSaveState {
  switch (e.kind) {
    case 'join':
      return {
        ...s,
        members: [
          ...s.members.filter((m) => m.client !== e.client),
          { client: e.client, joinSeq: e.seq, canEdit: e.canEdit === true },
        ],
      };
    case 'leave':
      return { ...s, members: s.members.filter((m) => m.client !== e.client) };
    case 'changes':
      return { ...s, changesHead: e.seq, dirtySince: s.dirtySince ?? e.at };
    case 'saved': {
      const through = Math.max(s.savedThrough, e.through ?? 0);
      // Changes the save did not hold keep the session dirty, from the time
      // the save was recorded: the clock starts again for them.
      const dirtySince = s.changesHead > through ? e.at : null;
      return { ...s, savedThrough: through, dirtySince };
    }
    default:
      return s;
  }
}

/** True when the log holds changes no save has. */
export function isDirty(s: OfficeSaveState): boolean {
  return s.changesHead > s.savedThrough;
}

/**
 * The saver: of the members who may write, the one who joined first. null
 * when nobody online may write.
 */
export function electSaver(members: readonly OfficeMember[]): string | null {
  let best: OfficeMember | null = null;
  for (const m of members) {
    if (!m.canEdit) continue;
    if (!best || m.joinSeq < best.joinSeq) best = m;
  }
  return best ? best.client : null;
}

/** Why a browser would save now. */
export type OfficeSaveReason = 'tick' | 'leave' | 'button';

/**
 * Should `me` save now?
 *   - 'button': whoever pressed Save saves, if it may write and there is
 *     something to save;
 *   - 'tick' (a timer): the saver saves once the oldest unsaved change is 10
 *     minutes old;
 *   - 'leave': a writer leaving saves when no other writer stays to do it.
 */
export function shouldSave(s: OfficeSaveState, me: string, now: number, why: OfficeSaveReason): boolean {
  if (!isDirty(s)) return false;
  const mine = s.members.find((m) => m.client === me);
  if (!mine || !mine.canEdit) return false;
  switch (why) {
    case 'button':
      return true;
    case 'tick':
      return electSaver(s.members) === me && s.dirtySince !== null && now - s.dirtySince >= OFFICE_E2E_AUTOSAVE_MS;
    case 'leave':
      return !s.members.some((m) => m.client !== me && m.canEdit);
  }
  return false;
}

/** How the editor runs: with others through the relay, or alone in the browser. */
export type OfficeE2eMode = 'shared' | 'solo';

/** A vault edits alone (the maintainers' decision): the server learns nothing until the save. */
export function officeE2eMode(where: { vault: boolean }): OfficeE2eMode {
  return where.vault ? 'solo' : 'shared';
}
