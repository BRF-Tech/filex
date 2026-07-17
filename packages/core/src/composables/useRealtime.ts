// useRealtime wires the folder-scoped live-collaboration layer (RealtimeClient)
// into the explorer: it turns change events into a debounced soft reload, tracks
// per-folder presence, and — when no live socket is available — falls back to
// plain API polling so embedded/offline consumers still get near-live updates.
//
// The explorer calls start()/stop() on mount/unmount, subscribe() on folder
// navigation, and setFocus() on selection; it binds presenceUsers into the UI.

import { ref, type Ref } from 'vue';
import { RealtimeClient, type PresenceUser, type PresenceMessage } from '../lib/realtime';

const RELOAD_DEBOUNCE_MS = 200;
const POLL_INTERVAL_MS = 12_000;

export interface RealtimeApi {
  wsTicket: () => Promise<{ ticket: string; ws_url: string } | null>;
}

export function useRealtime(api: RealtimeApi, opts: { reload: () => void }) {
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
  let reloadTimer: ReturnType<typeof setTimeout> | null = null;
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  function debouncedReload(): void {
    // A burst of change frames (e.g. a multi-file upload) collapses into one
    // soft reload of the current folder.
    if (reloadTimer) clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => {
      reloadTimer = null;
      opts.reload();
    }, RELOAD_DEBOUNCE_MS);
  }

  function onPresence(msg: PresenceMessage): void {
    // Ignore late frames for a folder we've already navigated away from.
    if (pendingSubscribe && msg.path && msg.path !== pendingSubscribe) return;
    presenceUsers.value = Array.isArray(msg.users) ? msg.users : [];
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
        onFallback,
        onStatus: (c) => {
          connected.value = c;
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
    if (reloadTimer) {
      clearTimeout(reloadTimer);
      reloadTimer = null;
    }
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    client?.close();
    client = null;
    degraded.value = false;
  }

  return { presenceUsers, connected, degraded, start, subscribe, setFocus, stop };
}
