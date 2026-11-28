/**
 * accountRules — what an account's e-mail address and username may be,
 * checked WHILE the person types, by the SERVER.
 *
 * ⚠⚠ The server is the only authority and the only author (0.54 audit B15 +
 * A12). This file used to be a mirror of its rules (backend identity.Check,
 * handlers/account_rules.go) with a copy of their sentences in two
 * catalogues beside the server's own `server.account.*`: three copies of
 * every sentence, and the e-mail rule had already drifted - the browser let
 * `a,b@x` and `ada.@x` through and the save refused them. Now a form asks
 * `POST /api/auth/account/check` while it is typed and shows what the server
 * says, in the reader's language; the save refuses the same things in the
 * same words.
 *
 * Why the check exists at all (release-candidate sweep, 2026-09-21): the
 * profile saved "bu-bir-eposta-degil" as an address and answered "Profil
 * kaydedildi", and a username with "ş" came back, after Save, as the server's
 * raw English. A form says the problem under the box while it is typed.
 */

/** identity.Normalize: trimmed, lower-case — what the server will store. */
export function normalizeUsername(raw: string): string {
  return raw.trim().toLowerCase();
}

/** One field the server would refuse: its code and its sentence. */
export interface AccountFieldRefusal {
  error: string;
  message: string;
}

/** The check's answer: a field is present only when it would be refused. */
export interface AccountCheckAnswer {
  email?: AccountFieldRefusal;
  username?: AccountFieldRefusal;
}

/** What a form asks about: only the fields sent are checked. `for` is whose
 *  account the values are meant for - the caller's own (the default), or an
 *  account an administrator is about to create. */
export interface AccountCheckQuery {
  email?: string;
  username?: string;
  for?: 'self' | 'new';
}

/** How long typing must pause before the server is asked. */
export const ACCOUNT_CHECK_DELAY_MS = 250;

/**
 * A checker for one form: call it with the values in the boxes after every
 * change; it waits until the typing pauses, asks the server once
 * (`ask` - POST /api/auth/account/check through the form's own client) and
 * resolves with the answer. A call a newer one replaced resolves with null,
 * and so does a request that failed: the save still judges, so a check that
 * could not be made says nothing rather than something wrong.
 */
export function accountChecker(
  ask: (q: AccountCheckQuery) => Promise<AccountCheckAnswer>,
  delayMs = ACCOUNT_CHECK_DELAY_MS,
): (q: AccountCheckQuery) => Promise<AccountCheckAnswer | null> {
  let seq = 0;
  let pending: { timer: ReturnType<typeof setTimeout>; resolve: (a: AccountCheckAnswer | null) => void } | null = null;
  return (q) => {
    const mine = ++seq;
    if (pending) {
      clearTimeout(pending.timer);
      pending.resolve(null);
      pending = null;
    }
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        pending = null;
        ask(q).then(
          (ans) => resolve(mine === seq ? (ans ?? {}) : null),
          () => resolve(null),
        );
      }, delayMs);
      pending = { timer, resolve };
    });
  };
}

/** A check's answer together with the values it was asked about. */
export interface AccountCheckResult {
  asked: AccountCheckQuery;
  answer: AccountCheckAnswer;
}

/**
 * The server's sentence about `field`, said only while the box still holds
 * the value the server judged (compared as the server stores it: trimmed,
 * lower-case); a box that has changed since waits for its own answer.
 *
 * ⚠⚠ An answer is about the values it was ASKED about, not about whatever
 * the box holds when it arrives (task #311). The Add user form asked about
 * the address with the username still empty - refused, "Enter a username." -
 * then the address's suggestion filled the username in, and the old refusal
 * stood under the new name until the next check came back (the typing pause
 * plus a round trip). A form sends nothing while a box says what is wrong
 * with it, so an Enter pressed in that window did nothing, and the refusal
 * was gone before anyone could read it: the dialog just stayed open (Cypress
 * spec 42 went red on and off from 0.59.1). The save still judges every
 * value, so a box with no answer yet holds nothing back.
 */
export function refusalFor(
  result: AccountCheckResult | null | undefined,
  field: 'email' | 'username',
  value: string,
): string {
  if (!result) return '';
  const asked = result.asked[field];
  if (asked === undefined || normalizeUsername(asked) !== normalizeUsername(value)) return '';
  return result.answer[field]?.message ?? '';
}

/**
 * The field a server refusal is about (`{error, message, field}` from
 * handlers/account_rules.go), so a form can put the server's sentence — it
 * is already in the reader's language — under the right box.
 */
export function refusalField(err: unknown): { field: string; message: string } | null {
  // Two carriers of the same body: an axios error (the admin app's client:
  // `response.data`) and core's own `jsonFetch` refusal (lib/errorWords
  // `requestFailure`: the raw body, clipped, in `detail`) — the desktop app's
  // settings dialog talks through the second.
  let data = (err as { response?: { data?: { field?: unknown; message?: unknown } } })?.response?.data;
  if (!data) {
    const detail = (err as { detail?: unknown })?.detail;
    if (typeof detail === 'string' && detail.trim().startsWith('{')) {
      try {
        data = JSON.parse(detail) as { field?: unknown; message?: unknown };
      } catch {
        data = undefined;
      }
    }
  }
  if (!data || typeof data.field !== 'string' || typeof data.message !== 'string') return null;
  return { field: data.field, message: data.message };
}
