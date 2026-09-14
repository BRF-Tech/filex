/**
 * timezone.ts — whose clock an instant is read on.
 *
 * A timestamp is an instant. `last_modified` is the same number for everybody
 * on the planet; what differs is the clock it is read against. The owner's
 * rule, verbatim: *"ben 03'te video yükledim, GMT 0 eleman o videonun yüklenme
 * saatini kendi zaman diliminde görecek."* So the instant stays untouched on
 * the wire and the READER picks the clock.
 *
 * ⚠⚠ WHICH reader is a question with several answers, and this module is the
 * ONE place they are ranked. Before it, the admin web app followed the account
 * and an embedded `<filex-explorer>` on another origin followed the browser —
 * a person who chose Tokyo saw Tokyo in one and their laptop's zone in the
 * other, for the same file. That disagreement is the bug this exists to end,
 * so every surface resolves through `resolveTimeZone()` and nothing else
 * decides.
 *
 * The owner's ruling (2026-09-14): three tiers — the embedder, then the
 * account behind the key when it IS a person's key, then the browser — plus a
 * setting inside the embed, kept in the browser, because an embed has no
 * user-settings dialog. Where that setting ranks is `TIME_ZONE_TIERS` below,
 * and the reasoning is on it.
 *
 * Scope: page-level, deliberately. The clock belongs to the person looking at
 * the page, and there is one of them. Host and account zones are REGISTERED by
 * whoever knows them (an explorer instance, the web app's auth store) under an
 * owner key, so an explorer that unmounts takes its zones with it instead of
 * leaving them behind for the next one. Two explorers on one page that
 * disagree about a host zone, or hold different people's tokens, resolve to
 * the most recently written value for both — one viewer, one clock.
 *
 * The same-tab `filex:timezone` event survives from the previous design and for
 * the same reason: the admin app and a `<filex-explorer>` web component on one
 * page can be two different BUNDLES, i.e. two copies of this module, and a ref
 * in one says nothing to the other. The event crosses that gap for the one tier
 * that is shared state by nature — the browser's.
 */

import { computed, ref, type InjectionKey, type Ref } from 'vue';

/* ── the order ────────────────────────────────────────────────────────── */

/**
 * The places a zone can come from.
 *
 *   viewer  — the person looking at this page picked one in THIS browser
 *             (the explorer's "⋯" → Time zone). localStorage, per origin.
 *   host    — the embedder set `config.timeZone` on the explorer.
 *   account — the account behind the credential, from `GET /api/auth/me`, and
 *             ONLY when that credential is a person's (a cookie session or
 *             their own API key). An `app` token is shared by every visitor of
 *             the embed that holds it, so its owner's zone is nobody's clock
 *             and the tier is skipped.
 *   device  — the browser's own zone, resolved live by `Intl`.
 */
export type TimeZoneTier = 'viewer' | 'host' | 'account' | 'device';

/**
 * ⚠⚠ THE order. First tier holding a valid zone wins; `device` always holds
 * one. Flipping a ranking is a change to this array and to nothing else — the
 * resolver, the explorer, the web app and the embed's own dialog all read it
 * (pinned by web/tests/lib/timeZoneTiers.test.ts).
 *
 * `viewer` is on top, above the embedder the owner numbered "1", and that is a
 * judgement rather than a transcription:
 *
 *   - A host's `config.timeZone` is a DEFAULT it chooses for everybody who
 *     visits, written before any of them arrived. A viewer's pick is a
 *     decision one person made on purpose, afterwards, in a control that
 *     exists for nothing else. If the host outranked it, every host that sets
 *     a zone would ship an embed whose Time zone control visibly does nothing
 *     — and a control that saves, reads back and changes nothing is worse than
 *     no control.
 *   - The explorer already ranks the same two things this way for the theme
 *     mode: `config.theme` is what `'host'` (never chosen) resolves to, and
 *     the first click in the gallery pins the viewer's own choice above it
 *     (FileExplorer.vue, `themeMode`). One product, one answer to "does the
 *     host or the person decide how this looks".
 *   - The pick is cheap to undo: the picker's first row is "use the default",
 *     and that row names the zone and the tier it falls back to.
 *
 * Below the viewer, the owner's own numbering stands as given.
 */
export const TIME_ZONE_TIERS: readonly TimeZoneTier[] = Object.freeze([
  'viewer',
  'host',
  'account',
  'device',
] as TimeZoneTier[]);

/** A value per tier. Empty, missing or engine-rejected means "no opinion". */
export type TimeZoneSources = Partial<Record<Exclude<TimeZoneTier, 'device'>, string | null>>;

export interface ResolvedTimeZone {
  /** The zone to hand `Intl`; `undefined` = the device, resolved live. */
  zone: string | undefined;
  /** Which tier it came from. */
  tier: TimeZoneTier;
}

/**
 * Walk `TIME_ZONE_TIERS` and return the first tier with a usable zone.
 *
 * Pure — the reactive state below feeds it, and so can a test, or a surface
 * that needs to say what a default WOULD be (the embed's dialog asks it with
 * the viewer tier blanked, to label its "use the default" row).
 */
export function resolveTimeZone(sources: TimeZoneSources): ResolvedTimeZone {
  for (const tier of TIME_ZONE_TIERS) {
    if (tier === 'device') break;
    const v = sources[tier];
    if (typeof v === 'string' && v && isValidTimeZone(v)) return { zone: v, tier };
  }
  return { zone: undefined, tier: 'device' };
}

/* ── storage keys ─────────────────────────────────────────────────────── */

/**
 * localStorage key of the VIEWER tier — the embed's own setting.
 *
 * ⚠ Not `filex.timezone`. That key already holds the web app's copy of the
 * ACCOUNT's zone in every browser that has opened the admin app, and reading
 * it as a viewer choice would rank a stale cache above the account forever: a
 * zone changed in another browser would never arrive in this one.
 */
export const TIMEZONE_VIEWER_LS_KEY = 'filex.timezone.viewer';

/**
 * localStorage key of the web app's copy of the ACCOUNT tier, so the first
 * paint after a reload is already right instead of flashing the device's zone
 * until `/api/auth/me` lands. Written only by a host that opts in
 * (`setAccountTimeZone(…, { remember: true })`) — an embed on a shared origin
 * must not inherit the zone of whoever last signed in to something else there.
 */
export const TIMEZONE_ACCOUNT_LS_KEY = 'filex.timezone';

/* ── the engine ───────────────────────────────────────────────────────── */

/**
 * A short, boring fallback list for engines without
 * `Intl.supportedValuesOf` (Safari < 15.4, older embedded WebViews). Not a
 * world atlas: enough to cover the zones this product's users are actually
 * in, plus UTC as the neutral reference every ops conversation falls back to.
 */
const FALLBACK_ZONES = [
  'UTC',
  'Europe/Istanbul',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Paris',
  'Europe/Madrid',
  'Europe/Moscow',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Africa/Cairo',
  'Africa/Lagos',
  'Asia/Dubai',
  'Asia/Karachi',
  'Asia/Kolkata',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Australia/Sydney',
  'Pacific/Auckland',
];

/** The zone this browser is in right now, asked fresh every time. */
export function deviceTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/**
 * True when the engine accepts `tz` as an IANA zone.
 *
 * ⚠ The check is a real formatter construction, not a lookup in the list
 * below: `supportedValuesOf` is the engine's *canonical* set and does not
 * contain the aliases it nonetheless accepts (`Asia/Istanbul`,
 * `US/Pacific`, …). The value may have come from another device, another
 * browser, a host page or a hand-edited account row.
 */
export function isValidTimeZone(tz: string): boolean {
  if (!tz) return false;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/** Every zone the engine knows, sorted; the fallback list when it knows none. */
export function supportedTimeZones(): string[] {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] })
      .supportedValuesOf;
    if (typeof fn === 'function') {
      const list = fn('timeZone');
      if (Array.isArray(list) && list.length) {
        // UTC is missing from the canonical list on some engines (it is
        // spelled Etc/UTC there) and it is the one zone an operator asks for
        // by name, so it is guaranteed a row rather than left to chance.
        return list.includes('UTC') ? [...list] : ['UTC', ...list];
      }
    }
  } catch {
    /* fall through */
  }
  return [...FALLBACK_ZONES];
}

/**
 * Which calendar day an instant falls on, in `zone`, as a day count since the
 * epoch. Comparable and subtractable — `a - b === 1` means "the day before".
 *
 * Why not `new Date(ms).getDate()`: that is the DEVICE's calendar, and the
 * date a listing groups by has to be the same date it prints in the cell four
 * pixels away. A file touched at 01:00 in Istanbul belongs under "Yesterday"
 * for a viewer reading UTC and under "Today" for one reading Istanbul.
 *
 * `en-CA` is not a locale choice, it is the shortest route to ISO `Y-M-D` out
 * of `Intl`; the parts are re-assembled through `Date.UTC` so the arithmetic
 * happens on a calendar with no offsets in it at all.
 */
export function zonedDayNumber(d: Date, zone?: string): number {
  const tz = zone ?? activeTimeZone();
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: tz,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(d);
    const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
    const y = get('year');
    const m = get('month');
    const day = get('day');
    if (!Number.isFinite(y) || !Number.isFinite(m) || !Number.isFinite(day)) {
      throw new Error('unparsable');
    }
    return Math.floor(Date.UTC(y, m - 1, day) / 86_400_000);
  } catch {
    // Engine refused the zone (or formatToParts) — the device's calendar is a
    // worse answer than the right one but a much better answer than none.
    return Math.floor(
      Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000,
    );
  }
}

/* ── the state ────────────────────────────────────────────────────────── */

function readValid(key: string): string {
  try {
    const v = localStorage.getItem(key);
    return v && isValidTimeZone(v) ? v : '';
  } catch {
    return '';
  }
}

function writeOrClear(key: string, value: string): void {
  try {
    if (value) localStorage.setItem(key, value);
    else localStorage.removeItem(key);
  } catch {
    /* private mode / blocked site data — the in-memory state still moved, so
       this page is correct; only the next reload forgets. */
  }
}

const hasWindow = typeof window !== 'undefined';

/** Tier `viewer`. `''` = no choice made in this browser. Read at module load
 *  so the value is right on the FIRST paint after a reload. */
const viewerZone: Ref<string> = ref(hasWindow ? readValid(TIMEZONE_VIEWER_LS_KEY) : '');

interface OwnedZone {
  owner: symbol;
  zone: string;
}

/**
 * A tier fed by several owners. The newest WRITE wins; re-stating an unchanged
 * value keeps its place, so a watcher that fires again cannot jump the queue
 * over a genuinely newer answer.
 */
function ownedTier() {
  const entries = ref<OwnedZone[]>([]);
  return {
    set(owner: symbol, zone: string | null): void {
      const cur = entries.value.find((e) => e.owner === owner);
      if (zone === null) {
        if (cur) entries.value = entries.value.filter((e) => e !== cur);
        return;
      }
      if (cur && cur.zone === zone) return;
      entries.value = [...entries.value.filter((e) => e.owner !== owner), { owner, zone }];
    },
    of(owner: symbol): string | null {
      return entries.value.find((e) => e.owner === owner)?.zone ?? null;
    },
    current(): string {
      const list = entries.value;
      return list.length ? list[list.length - 1].zone : '';
    },
    /** The newest answer among the owners `pick` accepts. */
    currentWhere(pick: (owner: symbol) => boolean): string {
      const list = entries.value.filter((e) => pick(e.owner));
      return list.length ? list[list.length - 1].zone : '';
    },
  };
}

const hostTier = ownedTier();
const accountTier = ownedTier();
/** Owners whose account entry is the localStorage copy, and follows it. */
const rememberingOwners = new Set<symbol>();

const resolved = computed<ResolvedTimeZone>(() =>
  resolveTimeZone({
    viewer: viewerZone.value,
    host: hostTier.current(),
    account: accountTier.current(),
  }),
);

/**
 * The zone to hand `Intl` — the resolved one. Reactive: reading it inside a
 * render or a computed subscribes that surface to every tier.
 *
 * `undefined` is returned for the device rather than the resolved name, so the
 * formatter takes `Intl`'s own live answer (a laptop carried across an ocean
 * keeps telling the truth without anybody re-saving anything).
 */
export function activeTimeZone(): string | undefined {
  return resolved.value.zone;
}

/** The resolved zone AND the tier it came from. Reactive. */
export function resolvedTimeZone(): ResolvedTimeZone {
  return resolved.value;
}

/**
 * The owner key an explorer PROVIDES to its components, so the dates they
 * print resolve against that explorer's own tiers (useLocale injects it).
 */
export const EXPLORER_CLOCK: InjectionKey<symbol> = Symbol('filex-explorer-clock');

/**
 * What the tiers hold for ONE explorer.
 *
 * ⚠⚠ The page-wide tiers answer with the NEWEST write, which is right for a
 * surface outside every explorer and wrong inside one: two explorers on one
 * page with different `config.timeZone` both printed the zone of whichever
 * mounted last. The host tier is by definition one explorer's setting, so an
 * explorer reads only its own. Its account tier is its own too; the only other
 * account it may inherit is the one a HOST APP remembered for the person
 * signed in (the admin app's session, `remember: true`) — never a sibling
 * explorer's, which may belong to a different key altogether. The viewer tier
 * is this browser's, shared by everything on the page.
 *
 * `owner` undefined is the page-wide answer, unchanged.
 */
export function timeZoneSourcesFor(owner: symbol | undefined): Required<TimeZoneSources> {
  if (!owner) return timeZoneSources();
  return {
    viewer: viewerZone.value,
    host: hostTier.of(owner) ?? '',
    account: accountTier.of(owner) ?? accountTier.currentWhere((o) => rememberingOwners.has(o)),
  };
}

/** resolvedTimeZone for ONE explorer (see timeZoneSourcesFor). Reactive. */
export function resolvedTimeZoneFor(owner: symbol | undefined): ResolvedTimeZone {
  return owner ? resolveTimeZone(timeZoneSourcesFor(owner)) : resolved.value;
}

/** activeTimeZone for ONE explorer (see timeZoneSourcesFor). Reactive. */
export function activeTimeZoneFor(owner: symbol | undefined): string | undefined {
  return resolvedTimeZoneFor(owner).zone;
}

/** What each tier currently holds (`''` = nothing). Reactive. */
export function timeZoneSources(): Required<TimeZoneSources> {
  return {
    viewer: viewerZone.value,
    host: hostTier.current(),
    account: accountTier.current(),
  };
}

/** Tier `viewer`, as stored in this browser. `''` = no choice. Reactive. */
export function viewerTimeZone(): string {
  return viewerZone.value;
}

/** Set (or, with `''`, clear) the viewer's own choice in this browser. */
export function setViewerTimeZone(tz: string): void {
  const valid = tz && isValidTimeZone(tz) ? tz : '';
  viewerZone.value = valid;
  writeOrClear(TIMEZONE_VIEWER_LS_KEY, valid);
  // A window never receives its own `storage` event, and a second bundle on
  // this page holds a different copy of this module. Say it out loud.
  try {
    window.dispatchEvent(new CustomEvent('filex:timezone', { detail: valid }));
  } catch {
    /* non-DOM environment, or an engine without the CustomEvent constructor */
  }
}

/**
 * Tier `host`: the zone `owner` (an explorer instance) was configured with.
 * `''`/`null`/an engine-rejected id withdraws it — an explorer with no
 * `config.timeZone` has no opinion, and must not hide another one's.
 */
export function setHostTimeZone(owner: symbol, tz: string | null | undefined): void {
  hostTier.set(owner, tz && isValidTimeZone(tz) ? tz : null);
}

/**
 * Tier `account`: what `owner` learned about the account behind its
 * credential.
 *
 * `''` is an answer — "this person has not chosen a zone" — and is recorded,
 * so it overrides an older answer from the same page. `null`/`undefined` means
 * "I have no account to speak for" (an `app` token, a failed lookup) and
 * withdraws the entry. A zone the engine rejects is recorded as `''`: the
 * account did say something, and it was not usable.
 *
 * `remember` mirrors the value into `TIMEZONE_ACCOUNT_LS_KEY` for the next
 * first paint, and makes this owner follow that key when another tab changes
 * it. For the admin web app, whose session is always a person.
 */
export function setAccountTimeZone(
  owner: symbol,
  tz: string | null | undefined,
  opts: { remember?: boolean } = {},
): void {
  if (tz === null || tz === undefined) {
    accountTier.set(owner, null);
    return;
  }
  const valid = tz && isValidTimeZone(tz) ? tz : '';
  accountTier.set(owner, valid);
  if (opts.remember) {
    rememberingOwners.add(owner);
    writeOrClear(TIMEZONE_ACCOUNT_LS_KEY, valid);
  }
}

/** The account zone `owner` last recorded (`null` = none recorded). Reactive. */
export function accountTimeZoneOf(owner: symbol): string | null {
  return accountTier.of(owner);
}

/** The account zone remembered in this browser by the last session. */
export function rememberedAccountTimeZone(): string {
  return hasWindow ? readValid(TIMEZONE_ACCOUNT_LS_KEY) : '';
}

/** Withdraw everything `owner` registered — call it on unmount. */
export function releaseTimeZoneOwner(owner: symbol): void {
  hostTier.set(owner, null);
  accountTier.set(owner, null);
  rememberingOwners.delete(owner);
}

if (hasWindow) {
  try {
    window.addEventListener('storage', (e) => {
      // `key === null` is a whole-storage clear, which is also news.
      if (e.key === null || e.key === TIMEZONE_VIEWER_LS_KEY) {
        viewerZone.value = readValid(TIMEZONE_VIEWER_LS_KEY);
      }
      if (e.key === null || e.key === TIMEZONE_ACCOUNT_LS_KEY) {
        const v = readValid(TIMEZONE_ACCOUNT_LS_KEY);
        for (const owner of rememberingOwners) accountTier.set(owner, v);
      }
    });
    window.addEventListener('filex:timezone', (e) => {
      // ⚠ From the event's own detail, not re-read from storage: in a private
      // window storage refuses the write, and re-reading would undo — in this
      // very bundle — the choice that was just made.
      const d = (e as CustomEvent).detail;
      viewerZone.value =
        typeof d === 'string' ? (d && isValidTimeZone(d) ? d : '') : readValid(TIMEZONE_VIEWER_LS_KEY);
    });
  } catch {
    /* non-DOM environment */
  }
}
