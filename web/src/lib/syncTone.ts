// One mapping from a storage's last sync status to a badge tone.
//
// ⚠ It lives here because there were two copies — Storages.vue and
// Dashboard.vue — and they drifted from the producer in the same way: both
// matched `'error'`, and the backend writes `'failed'`
// (backend/internal/sync/poll.go). A failed sync therefore fell to the neutral
// default and looked exactly like a storage that had never run, which is the
// one distinction an operator scans a storage list for.
//
// `'error'` stays accepted: it is what api/sync.ts translates `'failed'` into
// for the sync-RUNS list, so both spellings reach a UI surface.
//
// `'aborted'` is a run the server stopped in the middle of (closed when it next
// started): not a failure of the storage, not nothing either — the catalogue is
// behind the backend until the next sync. Amber, as on the sync-runs list.
export type SyncTone = 'emerald' | 'rose' | 'sky' | 'amber' | 'zinc';

export function syncTone(state: string | undefined | null): SyncTone {
  switch (state) {
    case 'ok':
      return 'emerald';
    case 'failed':
    case 'error':
      return 'rose';
    case 'running':
      return 'sky';
    case 'pending':
    case 'aborted':
      return 'amber';
    default:
      return 'zinc';
  }
}

type T = (key: string) => string;

/** The sync states the catalogue has words for (`sync.state.*`). */
export const SYNC_STATES = ['ok', 'error', 'running', 'aborted', 'pending'] as const;

/**
 * A sync state in the reader's words — for a storage's last sync and for a
 * run alike.
 *
 * ⚠ One function, because the raw wire value leaked in four places at once:
 * the Panel's storage cards and its Recent syncs chips, the Storages cards
 * and the Sync page's table all printed `ok` in the Turkish panel
 * (release-candidate sweep, 2026-09-21) — the Sync page's FILTER had been
 * translated and the cells right under it had not. `failed` (a storage's
 * last sync) is the same word as `error` (a run). An unknown state is shown
 * as sent rather than hidden.
 */
export function syncStateLabel(state: string | null | undefined, t: T): string {
  if (!state) return '';
  const s = state === 'failed' ? 'error' : state;
  return (SYNC_STATES as readonly string[]).includes(s) ? t(`sync.state.${s}`) : state;
}
