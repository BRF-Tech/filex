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
      return 'amber';
    default:
      return 'zinc';
  }
}
