// The unread count, as it is drawn — one rule, every surface.
//
// The count lives ON the bell icon (docs/NOTIFICATIONS.md → "The bell, and who
// can reach it", rule 3). It reads the exact number up to 99 and `99+` above
// that, never a raw three-digit number and never a bare dot.
//
// ⚠⚠ Why a module and not three `count > 99 ? '99+' : count` expressions. The
// same number is drawn in the explorer's header, in the admin panel's top nav,
// in the full list's own header, and on the desktop app's dock/taskbar badge.
// A counter written four times is a counter that disagrees with itself — and
// the disagreement is invisible until somebody has exactly 100 unread rows,
// which is the one moment nobody tests by hand.
//
// ⚠ Keep this file free of imports, of Vue and of `window`: it is imported by
// the DESKTOP MAIN PROCESS (desktop/src/main.ts), which has no DOM — the same
// discipline `notificationTarget.ts` keeps, and for the same reason.

/** Above this the badge stops counting and starts saying "more than this". */
export const UNREAD_BADGE_MAX = 99;

/**
 * What the badge SAYS. `''` when there is nothing to show — a zero badge is
 * not a small badge, it is no badge, and the caller renders nothing.
 *
 * ⚠ Non-finite and negative counts read as nothing rather than as `NaN`: the
 * count arrives from a network response, and a badge is not the place to
 * discover that a server answered oddly.
 */
export function unreadBadgeLabel(count: number | null | undefined): string {
  const n = Math.floor(Number(count ?? 0));
  if (!Number.isFinite(n) || n <= 0) return '';
  return n > UNREAD_BADGE_MAX ? `${UNREAD_BADGE_MAX}+` : String(n);
}

/**
 * The number an OS-level badge (dock, taskbar, tray) should carry.
 *
 * ⚠ NOT clamped to 99. macOS and Unity draw the number themselves and are
 * perfectly happy with 137; clamping it there would be this module inventing a
 * limit the platform does not have. The 99 ceiling is about how much room a
 * 16px circle has, which is a fact about the DOM badge and nothing else.
 */
export function unreadBadgeCount(count: number | null | undefined): number {
  const n = Math.floor(Number(count ?? 0));
  return Number.isFinite(n) && n > 0 ? n : 0;
}
