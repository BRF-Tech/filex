// Realtime (WebSocket) client for filex live collaboration, bundled into the
// core explorer so EVERY consumer — the native panel AND the vendored
// webcomponent embedded in host apps — gets live folder updates + presence.
//
// Auth: the browser's native WebSocket can't set an Authorization header and,
// when embedded, connects cross-origin to fm.brf.sh. So instead of a header we
// fetch a short-lived, single-use TICKET through the host's normal API (which
// injects the real token server-side) and open `wss://…/api/ws?ticket=<t>`.
// The durable token never reaches the browser.
//
// Degradation: if the ticket or the socket is unavailable (old backend, blocked
// upgrade, unsupported env), `onFallback(true)` fires so the consumer can fall
// back to plain API polling. The page always keeps working — every send is
// guarded, connect() is wrapped, reconnects are capped.

export interface PresenceUser {
  id: number;
  /** Stable identity key from the server (user id + optional per-end-user
   *  presence key). Distinguishes end users sharing one proxy token — use it
   *  for list keys and colours instead of `id`. */
  uid?: string;
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

export interface WsTicket {
  ticket: string;
  ws_url: string;
}

export interface RealtimeHandlers {
  onChange?: (msg: ChangeMessage) => void;
  onPresence?: (msg: PresenceMessage) => void;
  onStatus?: (connected: boolean) => void;
  /** Fires true when the live socket is unavailable (consumer should poll),
   *  false when a live socket is (re)established. */
  onFallback?: (active: boolean) => void;
}

export interface RealtimeOptions {
  /** Fetch a fresh ticket (single-use) via the host API. null → no live socket. */
  getTicket: () => Promise<WsTicket | null>;
  handlers: RealtimeHandlers;
}

const PING_INTERVAL_MS = 25_000;
const MAX_BACKOFF_MS = 15_000;
const MAX_RETRIES = 6;

export class RealtimeClient {
  private ws: WebSocket | null = null;
  private opts: RealtimeOptions;
  private closed = false;
  private connecting = false;
  private retries = 0;
  private fallback = false;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  // Last-known intent, replayed on (re)connect.
  private currentPath: string | null = null;
  private currentFocus: string | null = null;

  constructor(opts: RealtimeOptions) {
    this.opts = opts;
    void this.connect();
  }

  /** Subscribe to a folder ("<adapter>://<dir>"). Swaps the active room and
   *  resets focus. Passing null unsubscribes (drive list / trash have no room). */
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
    this.dropSocket();
  }

  private async connect(): Promise<void> {
    if (this.closed || this.connecting || this.ws) return;
    this.connecting = true;
    let ticket: WsTicket | null = null;
    try {
      ticket = await this.opts.getTicket();
    } catch {
      ticket = null;
    }
    this.connecting = false;
    if (this.closed) return;
    if (!ticket || !ticket.ticket || !ticket.ws_url) {
      this.fail();
      return;
    }

    const sep = ticket.ws_url.includes('?') ? '&' : '?';
    let ws: WebSocket;
    try {
      ws = new WebSocket(`${ticket.ws_url}${sep}ticket=${encodeURIComponent(ticket.ticket)}`);
    } catch {
      this.fail();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.retries = 0;
      this.setFallback(false);
      this.opts.handlers.onStatus?.(true);
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
      if (m?.type === 'change') this.opts.handlers.onChange?.(msg as ChangeMessage);
      else if (m?.type === 'presence') this.opts.handlers.onPresence?.(msg as PresenceMessage);
      // pong / error frames are intentionally ignored by the UI.
    };

    ws.onerror = () => {
      // onclose fires next; reconnect is handled there.
    };

    ws.onclose = () => {
      this.stopPing();
      this.ws = null;
      this.opts.handlers.onStatus?.(false);
      this.fail();
    };
  }

  // fail schedules a capped reconnect; after MAX_RETRIES it gives up on the live
  // socket and flips to fallback (polling) mode.
  private fail(): void {
    if (this.closed || this.reconnectTimer) return;
    if (this.retries >= MAX_RETRIES) {
      this.setFallback(true);
      return;
    }
    const delay = Math.min(1000 * 2 ** this.retries, MAX_BACKOFF_MS);
    this.retries += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.connect();
    }, delay);
  }

  private setFallback(active: boolean): void {
    if (this.fallback === active) return;
    this.fallback = active;
    this.opts.handlers.onFallback?.(active);
  }

  private send(obj: unknown): void {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try {
      ws.send(JSON.stringify(obj));
    } catch {
      /* a failed send just means a missed live update */
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

  private dropSocket(): void {
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

  private clearTimers(): void {
    this.stopPing();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}
