// Native OS notifications for the desktop app.
//
// The web bell raises a browser `Notification` while its tab is open. The
// desktop window has no bell in it at all — it is the explorer and nothing
// else — so without this the one client that is ALWAYS running was the one
// client that never told you anything.
//
// ⚠⚠ Where a click goes is NOT decided here. It comes from
// `web/src/lib/notificationTarget.ts`, the same module the browser bell
// imports, which is what makes "one resolver, three surfaces" a fact rather
// than a promise. Nothing in this file may decide a destination on its own.
//
// ⚠ Same cadence as the bell (15 s) and the same endpoint. This is not a
// second notification system: it is the existing feed, read by a process that
// can put something in front of the user.

// ⚠ No `electron` import. Everything that needs the runtime — the OS
// notification and the authenticated fetch — is injected by main.ts, which
// keeps this module (the part with the rules in it) runnable under
// `node --test`. A top-level `import … from 'electron'` outside Electron is a
// SyntaxError, and a rule that cannot be tested is a rule nobody checks.
import {
  resolveNotificationTarget,
  type NotificationDestination,
  type NotificationTarget,
} from '../../web/src/lib/notificationTarget.ts';
// ⚠⚠ Same reason, same boundary: the SENTENCE also comes from the web package,
// because a row's stored title is written once on the server in one language
// and for most file events is not written at all (Send substitutes the event
// id, which is how a native toast once read `file.uploaded`). One catalogue for
// the bell, the browser toast and this one — see web/src/lib/notificationText.ts.
import {
  renderNotification,
  type NotificationText,
  type NotifyLocale,
} from '../../web/src/lib/notificationText.ts';

/** The bell's own cadence. Do not lower it — see the note above. */
export const NOTIFY_POLL_MS = 15_000;

/** One row of `GET /api/notifications`, only the fields we use.
 *
 *  ⚠ `meta` is not decoration: it carries the nested `node`, `actor` and
 *  `share` the phrasing interpolates. Dropping it here would leave the shared
 *  renderer with nothing to say but the path. */
export interface NotificationRow {
  id: number;
  event: string;
  title?: string;
  body?: string;
  meta?: unknown;
  target?: NotificationTarget;
}

/**
 * One page of `GET /api/notifications` — the rows, and how many there are.
 *
 * ⚠ `total` is the count for the FILTER that was asked for. The poll asks
 * `unread=true`, so its total is the unread count: the same number the web
 * bell's badge shows, from the same server, without a second request.
 */
export interface NotificationPage {
  items: NotificationRow[];
  total: number;
}

export interface NotifyAccount {
  id: string;
  serverUrl: string;
  token: string;
}

/**
 * Which rows are NEW relative to a baseline, oldest first.
 *
 * ⚠ Split out as a pure function so the rule can be tested without an Electron
 * runtime: "> baseline" and the ordering are the two things that decide
 * whether a person gets told once, twice, or not at all.
 */
export function newRows(rows: NotificationRow[], since: number): NotificationRow[] {
  return rows.filter((r) => typeof r.id === 'number' && r.id > since).sort((a, b) => a.id - b.id);
}

export interface DesktopNotifierOptions {
  /** The account to watch — null while signed out / switching. */
  account: () => NotifyAccount | null;
  /** The user's switch in app settings. */
  enabled: () => boolean;
  /** Where a click goes. Handed the resolved destination, never a raw target. */
  onOpen: (accountId: string, dest: NotificationDestination) => void;
  /** Reads the bell. main.ts passes Electron's `net.fetch`; tests pass rows. */
  fetchRows: (acc: NotifyAccount, limit: number) => Promise<NotificationPage>;
  /**
   * The unread count, whenever a poll learned it — the desktop's half of
   * rule 3 (docs/NOTIFICATIONS.md: *the count lives ON the icon*). main.ts
   * puts it on the dock / taskbar badge and in the tray tooltip.
   *
   * ⚠⚠ It costs NOTHING extra. `GET /api/notifications?unread=true`
   * already answers `{items, total}` and this poll already makes that call;
   * the count is the `total` it was throwing away. A second request every
   * 15 s for a number we were already being handed would be a new cost for
   * an old fact.
   */
  onUnread?: (accountId: string, count: number) => void;
  /**
   * The language THIS reader is using — main.ts passes `effectiveLocale()`.
   * Read per row rather than captured, so switching the app's language changes
   * what the next notification says without a restart.
   */
  locale?: () => NotifyLocale;
  /**
   * Puts one row on screen. main.ts passes a real OS notification.
   *
   * ⚠ `text` is what to show; `row` is kept only for logging and for the click.
   * The composition happens HERE rather than in main.ts so the rule — never
   * show a raw event id — is covered by `node --test`, where a rule that
   * cannot be tested is a rule nobody checks.
   */
  show: (row: NotificationRow, text: NotificationText, onClick: () => void) => void;
  log?: (msg: string, extra?: Record<string, unknown>) => void;
  /**
   * The server refused this account's token — twice in a row, so one odd
   * answer from a proxy does not sign anybody out. main.ts marks the account
   * and stops everything that uses the token; `account()` then returns null
   * for it and polling stops. Called once per run of refusals.
   *
   * ⚠ This poll is often the only thing talking to the server at all: an
   * account with no synced folders has no watcher to notice a revoked token.
   */
  onUnauthorized?: (accountId: string) => void;
}

/** How many refusals in a row mean "this token is dead". */
export const UNAUTHORIZED_STREAK = 2;

/** True for a failed fetch the server answered 401. Reads the `status` the
 *  caller attaches, never the wording of the message. */
export function isUnauthorized(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { status?: unknown }).status === 401;
}

/**
 * Watches one account's bell and raises a native notification per new row.
 *
 * The baseline (`since`) is set on the FIRST poll and never notified for: an
 * app that has just started must not replay every unread row the user already
 * had. It is reset when the account changes, so switching servers on the rail
 * does not announce the new server's backlog either.
 */
/**
 * Does the app WINDOW carry this destination out? A folder (with the row to
 * select and the app screen to open) and the Trash view do; a share goes to the
 * system browser and `none` only brings the window forward.
 *
 * ⚠ This is not a second resolver: the destination still comes from the shared
 * one. It is the desktop's half of the answer only — which of the resolved
 * kinds this window knows how to show — kept here, beside the rules, so a new
 * kind is a test away rather than a lambda in main.ts nobody runs. The Trash
 * view arrived as a kind of its own (a `file.trashed` notice) and the window
 * forwarded folders only, so clicking that toast raised the window and left it
 * wherever it was.
 */
export function opensInWindow(dest: NotificationDestination): boolean {
  return dest.kind === 'folder' || dest.kind === 'trash';
}

export class DesktopNotifier {
  private timer: ReturnType<typeof setInterval> | null = null;
  private since: number | null = null;
  private watching: string | null = null;
  private inFlight = false;
  /** Consecutive 401s for the watched account. */
  private refusals = 0;
  private readonly opts: DesktopNotifierOptions;

  constructor(opts: DesktopNotifierOptions) {
    this.opts = opts;
  }

  start(): void {
    if (this.timer) return;
    void this.poll();
    this.timer = setInterval(() => void this.poll(), NOTIFY_POLL_MS);
    // Never hold the process open on its own account: the app quits when the
    // user quits it, not when a poll timer says so.
    this.timer.unref?.();
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    this.since = null;
    this.watching = null;
  }

  /** Called when the active account changes — forget the old baseline. */
  reset(): void {
    this.since = null;
    this.watching = null;
    this.refusals = 0;
  }

  async poll(): Promise<void> {
    if (this.inFlight) return;
    const acc = this.opts.account();
    if (!acc) {
      this.reset();
      return;
    }
    if (this.watching !== acc.id) {
      this.watching = acc.id;
      this.since = null;
      this.refusals = 0;
    }
    this.inFlight = true;
    try {
      const page = await this.opts.fetchRows(acc, 10);
      this.refusals = 0;
      const rows = page.items ?? [];
      // ⚠ Reported BEFORE the baseline check below returns: the first poll
      // announces nothing (it is establishing what "already seen" means) but
      // the badge must still be right on the first tick, or the app sits
      // there with no badge until something new arrives.
      this.opts.onUnread?.(acc.id, Math.max(0, page.total ?? rows.length));
      const top = rows.reduce((m, r) => (r.id > m ? r.id : m), 0);
      if (this.since === null) {
        this.since = top;
        return; // first look: establish the baseline, announce nothing
      }
      // ⚠ The switch is read HERE, not at start(): a user who turns
      // notifications off mid-session must stop being interrupted, and one who
      // turns them back on must not then be told about everything they missed
      // while it was off — so the baseline keeps advancing either way.
      const fresh = newRows(rows, this.since);
      this.since = Math.max(this.since, top);
      if (!this.opts.enabled()) return;
      const lang: NotifyLocale = this.opts.locale?.() ?? 'en';
      for (const row of fresh) {
        const text = renderNotification(row, lang);
        this.opts.show(row, text, () =>
          this.opts.onOpen(acc.id, resolveNotificationTarget(row.target)),
        );
      }
    } catch (err) {
      // A server that is asleep, no network: the app keeps working and says
      // nothing. This is a courtesy channel.
      //
      // ⚠ The badge is NOT cleared here. A number that vanishes on a dropped
      // request reads as "you have read everything", which is a claim about
      // the person's mail made by a failed network call. The last known count
      // stands until a poll succeeds.
      this.opts.log?.('notifications: poll failed', { err: String(err) });
      // …except for a token the server no longer accepts, which is not going
      // to start working by being asked again every 15 seconds.
      if (isUnauthorized(err)) {
        this.refusals++;
        if (this.refusals === UNAUTHORIZED_STREAK) this.opts.onUnauthorized?.(acc.id);
      } else {
        this.refusals = 0;
      }
    } finally {
      this.inFlight = false;
    }
  }
}
