// Realtime (WebSocket) client for filex live collaboration.
//
// Connects to the same-origin GET /api/ws endpoint — the browser's session
// cookie authenticates the upgrade, so no token plumbing is needed for the
// native panel. The client relays two folder-scoped streams from the backend
// hub: `change` (a file was added/removed/renamed/uploaded in the folder →
// refresh the list) and `presence` (who else is in this folder and which file
// each is focused on).
//
// Design goals:
//   - Degrade gracefully: if WebSocket is unavailable or the upgrade fails, the
//     page keeps working with zero live updates and no thrown errors. Every
//     send is guarded; connect() is wrapped; reconnect attempts are capped.
//   - Reconnect with exponential backoff, re-subscribing to the last folder and
//     re-sending the last focus on reconnect.
//   - Single connection reused across folder navigations (subscribe swaps room).

export interface PresenceUser {
  id: number;
  name: string;
  file?: string;
}

export interface ChangeMessage {
  type: 'change';
  path: string;
  action: string; // create | delete | rename | move | upload | modify
  name?: string;
  new_name?: string;
}

export interface PresenceMessage {
  type: 'presence';
  path: string;
  users: PresenceUser[];
}

export interface RealtimeHandlers {
  onChange?: (msg: ChangeMessage) => void;
  onPresence?: (msg: PresenceMessage) => void;
  onStatus?: (connected: boolean) => void;
}

const PING_INTERVAL_MS = 25_000;
const MAX_BACKOFF_MS = 15_000;
const MAX_RETRIES = 8;

function wsUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/api/ws`;
}

export class RealtimeClient {
  private ws: WebSocket | null = null;
  private handlers: RealtimeHandlers;
  private closed = false;
  private retries = 0;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  // Last-known intent, replayed on (re)connect.
  private currentPath: string | null = null;
  private currentFocus: string | null = null;

  constructor(handlers: RealtimeHandlers) {
    this.handlers = handlers;
    this.connect();
  }

  /** Subscribe to a folder ("<adapter>://<dir>"). Swaps the active room and
   *  resets focus. Passing null unsubscribes (no folder, e.g. the drive list). */
  subscribe(path: string | null): void {
    this.currentPath = path;
    this.currentFocus = null;
    if (path) this.send({ type: 'subscribe', path });
  }

  /** Report the file the user is focused on (or null to clear). */
  setFocus(file: string | null): void {
    this.currentFocus = file;
    this.send({ type: 'focus', file });
  }

  /** Tear down permanently — call on unmount. */
  close(): void {
    this.closed = true;
    this.clearTimers();
    if (this.ws) {
      try {
        this.ws.onclose = null; // suppress reconnect
        this.ws.close();
      } catch {
        /* ignore */
      }
      this.ws = null;
    }
  }

  private connect(): void {
    if (this.closed) return;
    let ws: WebSocket;
    try {
      ws = new WebSocket(wsUrl());
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.retries = 0;
      this.handlers.onStatus?.(true);
      // Replay intent so a reconnect lands the user back in their folder.
      if (this.currentPath) this.send({ type: 'subscribe', path: this.currentPath });
      if (this.currentFocus !== null) this.send({ type: 'focus', file: this.currentFocus });
      this.startPing();
    };

    ws.onmessage = (ev: MessageEvent) => {
      let msg: unknown;
      try {
        msg = JSON.parse(typeof ev.data === 'string' ? ev.data : '');
      } catch {
        return;
      }
      const m = msg as { type?: string };
      if (m?.type === 'change') this.handlers.onChange?.(msg as ChangeMessage);
      else if (m?.type === 'presence') this.handlers.onPresence?.(msg as PresenceMessage);
      // pong / error frames are intentionally ignored by the UI.
    };

    ws.onerror = () => {
      // onclose fires next; reconnect is handled there.
    };

    ws.onclose = () => {
      this.stopPing();
      this.ws = null;
      this.handlers.onStatus?.(false);
      this.scheduleReconnect();
    };
  }

  private send(obj: unknown): void {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try {
      ws.send(JSON.stringify(obj));
    } catch {
      /* ignore — a failed send just means a missed live update */
    }
  }

  private startPing(): void {
    this.stopPing();
    this.pingTimer = setInterval(() => this.send({ type: 'ping' }), PING_INTERVAL_MS);
  }

  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }

  private scheduleReconnect(): void {
    if (this.closed || this.reconnectTimer) return;
    if (this.retries >= MAX_RETRIES) return; // give up silently (unsupported env)
    const delay = Math.min(1000 * 2 ** this.retries, MAX_BACKOFF_MS);
    this.retries += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private clearTimers(): void {
    this.stopPing();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}
