// useRealtime wires the folder-scoped live-collaboration layer (RealtimeClient)
// into the explorer: it turns change events into a debounced soft reload, tracks
// per-folder presence, and — when no live socket is available — falls back to
// plain API polling so embedded/offline consumers still get near-live updates.
//
// The explorer calls start()/stop() on mount/unmount, subscribe() on folder
// navigation, and setFocus() on selection; it binds presenceUsers into the UI.

import { ref, type Ref } from 'vue';
import { RealtimeClient, type PresenceUser, type PresenceMessage, type AppUpdatedMessage, type VaultMessage } from '../lib/realtime';
import { burstDebounce } from '../lib/burstDebounce';
import { announceAppUpdated } from '../lib/appUpdates';
import { invalidatePluginActions } from './usePluginActions';

const RELOAD_DEBOUNCE_MS = 200;
// Ceiling on how long a run of change frames may postpone the reload it is
// waiting for — see burstDebounce for the starvation this exists to stop.
// Measured in a real browser on 2026-09-06, watching a folder while a
// 5 000-file zip was extracted into it: the first re-listing came 114 s into
// the job, and there was a 40 s gap in the middle.
const RELOAD_MAX_WAIT_MS = 2_000;
const POLL_INTERVAL_MS = 12_000;

export interface RealtimeApi {
  wsTicket: () => Promise<{ ticket: string; ws_url: string } | null>;
}

/**
 * #196 - the answers a right-click menu depends on may be stale: the server
 * said so (`access.changed`; `all` when everybody connected heard it), or the
 * socket was down for a while and could have missed it (`resync`).
 */
export interface AccessNews {
  all: boolean;
  resync: boolean;
}

export function useRealtime(
  api: RealtimeApi,
  opts: { reload: () => void; onVault?: (msg: VaultMessage) => void; onAccess?: (news: AccessNews) => void },
) {
  const presenceUsers: Ref<PresenceUser[]> = ref([]);
  const connected = ref(false);
  // True while the live socket is unavailable and the polling fallback is
  // active — the explorer surfaces a small "no live connection" badge from it.
  // Stays false during ordinary reconnect attempts, so brief blips don't flash
  // the badge; only a genuinely given-up socket (RealtimeClient onFallback)
  // flips it.
  const degraded = ref(false);

  let client: RealtimeClient | null = null;
  let pendingSubscribe: string | null = null;
  // #196 - a socket that comes back after a drop may have missed an
  // access.changed frame: the explorer is told to ask again.
  let everConnected = false;
  const debouncedReload = burstDebounce(() => opts.reload(), {
    wait: RELOAD_DEBOUNCE_MS,
    maxWait: RELOAD_MAX_WAIT_MS,
  });
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  function onPresence(msg: PresenceMessage): void {
    // Ignore late frames for a folder we've already navigated away from.
    if (pendingSubscribe && msg.path && msg.path !== pendingSubscribe) return;
    presenceUsers.value = Array.isArray(msg.users) ? msg.users : [];
  }

  /** An app's approved version changed: the cached menu rows carry the old
   *  interface's address, and an open frame on it offers to reload. */
  function onAppUpdated(msg: AppUpdatedMessage): void {
    invalidatePluginActions();
    announceAppUpdated(msg.app, msg.version);
  }

  function onFallback(active: boolean): void {
    degraded.value = active;
    if (active) {
      // No live socket → poll the listing; presence isn't available here.
      presenceUsers.value = [];
      if (!pollTimer) pollTimer = setInterval(() => opts.reload(), POLL_INTERVAL_MS);
    } else if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function start(): void {
    if (client) return;
    client = new RealtimeClient({
      getTicket: () => api.wsTicket(),
      handlers: {
        onChange: debouncedReload,
        onPresence,
        onAppUpdated,
        onVault: (msg) => opts.onVault?.(msg) /* wiring:e2 vault */,
        onAccessChanged: (ev) => opts.onAccess?.({ all: ev.all, resync: false }) /* #196 */,
        onFallback,
        onStatus: (c) => {
          connected.value = c;
          if (c && everConnected) opts.onAccess?.({ all: false, resync: true });
          if (c) everConnected = true;
        },
      },
    });
  }

  function subscribe(wire: string | null): void {
    presenceUsers.value = []; // drop the previous folder's roster immediately
    pendingSubscribe = wire;
    client?.subscribe(wire);
  }

  function setFocus(file: string | null): void {
    client?.setFocus(file);
  }

  function stop(): void {
    debouncedReload.cancel();
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    client?.close();
    client = null;
    everConnected = false;
    degraded.value = false;
  }

  return { presenceUsers, connected, degraded, start, subscribe, setFocus, stop };
}
