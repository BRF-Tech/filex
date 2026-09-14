// The viewer's own clock — the admin app's half of it.
//
// ⚠⚠ This module does NOT decide which zone is in force. `@brftech/filex-core`'s
// `lib/timezone` does, through ONE ordered list of tiers (`TIME_ZONE_TIERS`:
// the viewer's own pick in this browser, a host's `config.timeZone`, the
// account behind a person's credential, the device). The admin app and every
// embedded explorer resolve through that same function, so they cannot
// disagree about one file's time again — which is precisely how they came to
// disagree before: this app followed the account and an embed on another
// origin followed the browser.
//
// What this file adds is the one thing only the admin app knows: the ACCOUNT
// of the person signed in, from `/api/auth/me`, written into the `account`
// tier under this app's own owner key — plus the settings modal's write path
// (`users.timezone` through `PATCH /api/auth/profile`) and the two helpers
// `lib/format.ts` calls.
//
// The account is mirrored to localStorage (`TIMEZONE_ACCOUNT_LS_KEY`, still
// `filex.timezone`) so the FIRST PAINT after a reload is already right instead
// of flashing the device's zone until `/api/auth/me` lands. The account wins
// over the mirror: there is one control for it and it writes both halves, so
// a difference is a stale cache (a zone saved in another browser), never a
// newer decision.

import {
  activeTimeZone as coreActiveTimeZone,
  accountTimeZoneOf,
  deviceTimeZone,
  isValidTimeZone,
  rememberedAccountTimeZone,
  setAccountTimeZone,
  setViewerTimeZone,
  supportedTimeZones,
  TIMEZONE_ACCOUNT_LS_KEY,
} from '@brftech/filex-core';

export { deviceTimeZone, isValidTimeZone, supportedTimeZones, TIMEZONE_ACCOUNT_LS_KEY };

/** This app's key in the resolver's `account` tier. */
const WEB_APP = Symbol('filex-web-app');

// First paint: the account as the last session in this browser saw it. Only a
// remembered zone is registered — with nothing remembered, "no entry" and
// "no zone" resolve the same way (the device) until `/api/auth/me` answers.
{
  const remembered = rememberedAccountTimeZone();
  if (remembered) setAccountTimeZone(WEB_APP, remembered, { remember: true });
}

/** The zone to hand `Intl`; `undefined` = this device, resolved live. */
export function activeTimeZone(): string | undefined {
  return coreActiveTimeZone();
}

/** The ACCOUNT's zone as this app knows it — `''` means "use the device". */
export function getStoredTimeZone(): string {
  return accountTimeZoneOf(WEB_APP) ?? '';
}

/**
 * The settings modal's pick: the account's zone, locally first. The caller
 * PATCHes the profile in the same handler — kept separate because a failed
 * network call must not undo a choice the user can already see took effect.
 *
 * ⚠ It also clears THIS browser's viewer pick. The admin app has no other door
 * to it — the explorer's "⋯ → Time zone" row is dropped from this app's menu
 * because this modal is the door (views/Explore.vue, ROWS_WITH_ANOTHER_DOOR) —
 * and the viewer tier outranks the account. Without this line, a pick left
 * behind by an embed on this origin would make the modal's control visibly do
 * nothing, with no way here to undo it. The person just chose, on this device,
 * in the control for exactly this; that is the decision in force.
 */
export function setStoredTimeZone(tz: string): void {
  setAccountTimeZone(WEB_APP, tz && isValidTimeZone(tz) ? tz : '', { remember: true });
  setViewerTimeZone('');
}

/**
 * Adopt the zone stored on the ACCOUNT, from `/api/auth/me`.
 *
 * `undefined`/`null` means the response carried no opinion (an older server,
 * a leaner payload) and nothing is touched. An empty string is an opinion —
 * "this person has not chosen a zone", i.e. the device's — and is applied like
 * any other.
 */
export function applyAccountTimeZone(tz?: string | null): void {
  if (tz === undefined || tz === null) return;
  setAccountTimeZone(WEB_APP, tz && isValidTimeZone(tz) ? tz : '', { remember: true });
}
