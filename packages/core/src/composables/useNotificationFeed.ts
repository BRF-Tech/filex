/**
 * The signed-in person's notifications — the rows the bell lists, the count on
 * it, and the full list "see all" opens.
 *
 * ⚠⚠ ONE implementation for every surface that draws the bell. It lived in the
 * web app's pinia store (web/src/stores/notifications.ts) until 2026-09-27,
 * when the desktop app — which mounts `<filex-explorer>` and cannot reach that
 * store — was given the same bell (owner: *"masaüstünde bildirimler geliyor ama
 * uygulamanın içinde onlara bakılacak bir panel yok"*). Writing the desktop a
 * second feed would have been a second answer to "is this row read yet", so
 * the logic moved here and the web store now COMPOSES it (and keeps only the
 * admin half: the instance-wide audit list and the webhook settings).
 *
 * ⚠ Plain refs and functions, no framework store. The web wraps the result in
 * pinia (which unwraps the refs); the explorer wraps it in `reactive()` (which
 * does the same) — so the components read one shape, `NotificationFeed`, from
 * either host.
 */
import { computed, ref, type Ref } from 'vue';
import type { NotificationTarget } from '../lib/notificationTarget';

/** How many of the person's own rows the bell holds. It shows 15; the rest is
 *  headroom for the browser-notification diff, which reads the same list. */
export const FEED_LIMIT = 50;

/** The bell's cadence, on every surface that polls (the web's watcher, the
 *  explorer's own loop). Do not lower it: a quiet instance costs one small
 *  request per tick, per open window. */
export const NOTIFY_POLL_MS = 15_000;

/**
 * One notification, as far as the bell is concerned. The web's `NotificationItem`
 * carries more (the admin columns); every field here is one it has too.
 */
export interface NotificationRowData {
  id: number;
  event: string;
  severity: string;
  title?: string;
  body?: string;
  meta?: unknown;
  target?: NotificationTarget | null;
  read_at?: string | null;
  created_at: string;
}

/** The four user-scoped endpoints the feed reads (docs/NOTIFICATIONS.md →
 *  "In-app bell (endpoints)"). None of them needs administrator rights. */
export interface NotificationsTransport<T extends NotificationRowData = NotificationRowData> {
  list(params: { unread?: boolean; limit?: number; offset?: number }): Promise<{ items?: T[] | null; total?: number }>;
  unreadCount(): Promise<number>;
  markRead(id: number): Promise<void>;
  markAllRead(): Promise<void>;
}

export interface NotificationFeedOptions<T extends NotificationRowData> {
  transport: NotificationsTransport<T>;
  /** What a failed page load says (the full-list screen prints it). */
  errorText?: (err: unknown) => string;
  /**
   * Other lists on screen that hold the same rows and must stop looking unread
   * the moment one is read here — the web's admin audit table.
   */
  alsoStamp?: () => Ref<T[]>[];
  /**
   * Told after a row (or every row) was marked read here — so a badge the host
   * draws itself (the desktop app's dock and tray) moves at once instead of on
   * its next poll.
   */
  onRead?: () => void;
}

/**
 * The feed's state and verbs. See the file note for why this is not a store.
 */
export function createNotificationFeed<T extends NotificationRowData>(opts: NotificationFeedOptions<T>) {
  const api = opts.transport;
  const errorText = opts.errorText ?? ((e: unknown) => (e instanceof Error ? e.message : String(e)));
  const others = () => opts.alsoStamp?.() ?? [];

  /**
   * The signed-in person's OWN rows, newest first — what the bell lists and
   * what the watcher diffs to raise browser notifications.
   *
   * ⚠⚠ ONE list, filled from ONE place (`refreshFeed`), for both readers. The
   * bell used to fetch its rows once — on a vnode hook that fires when the
   * popover is first rendered, not when it opens — while the watcher polled
   * the count and fetched its own copy of the unread head. So the badge kept
   * climbing and the list never moved: measured 2026-09-14, badge 9 over a
   * list whose newest row predated the arrival, still so after closing and
   * reopening, and still so with the panel left open while a tenth arrived.
   */
  const feed = ref([]) as Ref<T[]>;
  const feedLoading = ref(false);
  /**
   * The person's OWN full list — what "see all" opens, paginated.
   *
   * ⚠⚠ A separate list, deliberately. `feed` is the bell's newest 50; this is
   * "page 4 of MY history", which is a different question with a different
   * answer. Writing it into `feed` would make the bell show page 4.
   *
   * ⚠ It reads `GET /api/notifications`, which is USER-scoped, and that is the
   * whole point: "see all" used to point at the admin page, so somebody
   * without admin rights could not read their own notifications at all.
   */
  const mine = ref([]) as Ref<T[]>;
  const mineTotal = ref(0);
  const mineLimit = ref(25);
  const mineOffset = ref(0);
  const mineUnreadOnly = ref(false);
  const mineLoading = ref(false);
  const mineError = ref<string | null>(null);
  /** Only the newest fetch may write — paging fast must not let page 2 land after page 3. */
  let mineSeq = 0;
  /**
   * Is the full-list screen open? Held HERE, not in the bell, because the web
   * draws the bell in TWO headers (the admin nav and the explorer's own) while
   * the screen is rendered once. A flag per bell would be two screens.
   */
  const panelOpen = ref(false);
  /**
   * The unread count the feed was fetched alongside. When the polled count
   * stops matching it, something arrived (or was read elsewhere) and the list
   * is behind the badge. `null` = never fetched.
   */
  let feedCount: number | null = null;
  /** Only the newest refresh may write — an open racing a poll must not let the
   *  slower, older answer land last. */
  let feedSeq = 0;
  const unreadCount = ref(0);
  const hasUnread = computed(() => unreadCount.value > 0);

  /**
   * Fetch the person's rows AND the unread count together, so the badge and
   * the list are read from the same moment. Resolves `false` when the fetch
   * failed (the previous list is kept — a failed request is not "no rows").
   */
  async function refreshFeed(): Promise<boolean> {
    const seq = ++feedSeq;
    feedLoading.value = true;
    try {
      const [r, count] = await Promise.all([
        api.list({ unread: false, limit: FEED_LIMIT, offset: 0 }),
        api.unreadCount(),
      ]);
      if (seq !== feedSeq) return true;
      feed.value = r.items ?? [];
      unreadCount.value = count;
      feedCount = count;
      return true;
    } catch {
      return false;
    } finally {
      if (seq === feedSeq) feedLoading.value = false;
    }
  }

  async function fetchUnread(): Promise<void> {
    try {
      unreadCount.value = await api.unreadCount();
    } catch {
      /* swallow — bell badge defaults to 0 */
    }
  }

  /**
   * The poll's one call: read the count, and bring the list up to date when
   * the count no longer matches the one the list was fetched with. A quiet
   * instance therefore costs one small request per tick.
   */
  async function syncUnread(): Promise<void> {
    await fetchUnread();
    if (feedCount !== unreadCount.value) await refreshFeed();
  }

  /**
   * A count somebody ELSE already read from the server — the desktop app's main
   * process polls the bell for its OS notifications and dock badge, and hands
   * the number over instead of this window asking a second time every 15 s.
   * Same rule as `syncUnread`: a count that disagrees with the list's means the
   * list is behind.
   */
  async function acceptCount(count: number): Promise<void> {
    const n = Math.max(0, Math.floor(Number(count) || 0));
    unreadCount.value = n;
    if (feedCount !== n) await refreshFeed();
  }

  /**
   * One page of the person's OWN history — the full-list screen's only fetch.
   *
   * ⚠ A failure keeps the previous page on screen and SAYS so. Emptying the
   * list on a dropped request would tell somebody with 133 notifications that
   * they have none, which is the one answer the screen must never invent.
   */
  async function fetchMine(): Promise<void> {
    const seq = ++mineSeq;
    mineLoading.value = true;
    mineError.value = null;
    try {
      const r = await api.list({
        unread: mineUnreadOnly.value,
        limit: mineLimit.value,
        offset: mineOffset.value,
      });
      if (seq !== mineSeq) return;
      mine.value = r.items ?? [];
      mineTotal.value = r.total ?? 0;
    } catch (e: unknown) {
      if (seq !== mineSeq) return;
      mineError.value = errorText(e);
    } finally {
      if (seq === mineSeq) mineLoading.value = false;
    }
  }

  function setMinePage(p: number): void {
    mineOffset.value = Math.max(0, (p - 1) * mineLimit.value);
  }

  function setMineUnreadFilter(u: boolean): void {
    mineUnreadOnly.value = u;
    mineOffset.value = 0;
  }

  /** Open the full-list screen and load its first page. */
  function openPanel(): void {
    panelOpen.value = true;
    mineOffset.value = 0;
    void fetchMine();
  }

  function closePanel(): void {
    panelOpen.value = false;
  }

  /** Every list, and the count the feed agrees with, move together — a local
   *  read must not look like news to the next poll. */
  async function markRead(id: number): Promise<void> {
    await api.markRead(id);
    const at = new Date().toISOString();
    const stamp = (n: T): T => (n.id === id && !n.read_at ? { ...n, read_at: at } : n);
    // ⚠ Every list that can be on screen at once. The full-list screen sits
    // OVER the explorer whose bell is still drawn behind it, so a row marked
    // read in one has to stop looking unread in the other — a list left
    // unstamped is the same row in two states, two inches apart.
    const lists = [feed, mine, ...others()];
    const wasUnread = lists.some((l) => l.value.some((n) => n.id === id && !n.read_at));
    for (const l of lists) l.value = l.value.map(stamp);
    if (wasUnread && unreadCount.value > 0) {
      unreadCount.value -= 1;
      if (feedCount !== null && feedCount > 0) feedCount -= 1;
    }
    opts.onRead?.();
  }

  async function markAllRead(): Promise<void> {
    await api.markAllRead();
    const at = new Date().toISOString();
    const stamp = (n: T): T => ({ ...n, read_at: n.read_at ?? at });
    for (const l of [feed, mine, ...others()]) l.value = l.value.map(stamp);
    unreadCount.value = 0;
    if (feedCount !== null) feedCount = 0;
    opts.onRead?.();
    // The unread-only view of a list where nothing is unread any more is an
    // empty list, and that is the honest answer — but it has to be FETCHED,
    // not guessed, because page 2 of it no longer exists either.
    if (panelOpen.value && mineUnreadOnly.value) {
      mineOffset.value = 0;
      void fetchMine();
    }
  }

  return {
    feed,
    feedLoading,
    unreadCount,
    hasUnread,
    mine,
    mineTotal,
    mineLimit,
    mineOffset,
    mineUnreadOnly,
    mineLoading,
    mineError,
    panelOpen,
    refreshFeed,
    fetchUnread,
    syncUnread,
    acceptCount,
    fetchMine,
    setMinePage,
    setMineUnreadFilter,
    openPanel,
    closePanel,
    markRead,
    markAllRead,
  };
}

/**
 * The feed as the components read it: refs unwrapped, the way a pinia store
 * and a `reactive()` both hand it over. The bell and the full-list screen take
 * this, never the raw refs, so one component works under either host.
 */
export interface NotificationFeed<T extends NotificationRowData = NotificationRowData> {
  readonly feed: T[];
  readonly feedLoading: boolean;
  readonly unreadCount: number;
  readonly hasUnread: boolean;
  readonly mine: T[];
  readonly mineTotal: number;
  readonly mineLimit: number;
  readonly mineOffset: number;
  readonly mineUnreadOnly: boolean;
  readonly mineLoading: boolean;
  readonly mineError: string | null;
  readonly panelOpen: boolean;
  refreshFeed(): Promise<boolean>;
  fetchUnread(): Promise<void>;
  syncUnread(): Promise<void>;
  acceptCount(count: number): Promise<void>;
  fetchMine(): Promise<void>;
  setMinePage(p: number): void;
  setMineUnreadFilter(u: boolean): void;
  openPanel(): void;
  closePanel(): void;
  markRead(id: number): Promise<void>;
  markAllRead(): Promise<void>;
}

/**
 * The four endpoints over the explorer's own client — for a host that hands
 * the explorer a credential rather than a store (the desktop app, an embed).
 *
 * `jsonFetch` is `useFileApi(config).jsonFetch`, so the request carries the
 * explorer's auth, its language and its credentials mode, exactly like every
 * listing it makes. `base` is the server root (`connectionsBase(config)`).
 */
export function notificationsTransport(
  jsonFetch: <R>(url: string, init?: RequestInit) => Promise<R>,
  base: string,
): NotificationsTransport {
  const root = `${base.replace(/\/+$/, '')}/api/notifications`;
  return {
    async list(p) {
      const q: string[] = [];
      if (p.unread) q.push('unread=true');
      if (p.limit != null) q.push(`limit=${encodeURIComponent(String(p.limit))}`);
      if (p.offset != null) q.push(`offset=${encodeURIComponent(String(p.offset))}`);
      const body = await jsonFetch<{ items?: NotificationRowData[] | null; total?: number }>(
        q.length ? `${root}?${q.join('&')}` : root,
      );
      return body ?? { items: [], total: 0 };
    },
    async unreadCount() {
      const body = await jsonFetch<{ count?: number }>(`${root}/unread-count`);
      return Math.max(0, Number(body?.count ?? 0) || 0);
    },
    async markRead(id) {
      await jsonFetch(`${root}/${encodeURIComponent(String(id))}/read`, { method: 'POST' });
    },
    async markAllRead() {
      await jsonFetch(`${root}/read-all`, { method: 'POST' });
    },
  };
}
