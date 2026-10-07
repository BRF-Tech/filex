// The one clock every screenshot is taken at (task #176, the owner's decision
// of 2026-10-06): the same scene taken tonight and in a month shows the same
// dates, the same times and the same "x minutes ago", so a picture changes
// only when the screen did. Without it every picture that shows a date moved
// every night - the upload time of each file, the day in a date picker, the
// time of each notification - and the pixel comparison of `pnpm shots` put
// them all in front of a person again.
//
// Four parts, each where nothing else can do it:
//
//   1. THE BROWSER'S CLOCK starts at SCENE_NOW (Playwright's
//      `context.clock.setSystemTime`) and runs from there, in SCENE_TZ.
//      ⚠ Not `setFixedTime`: the explorer measures time between two clicks
//      (FilePane's open guard swallows a click within 500 ms of an open,
//      DataTable's a click within a moment of a drag); on a clock that never
//      moves every click after the first open is swallowed.
//   2. THE SERVER'S TIMES - an upload's modification time, a notification,
//      a share's expiry, a licence's renewal - are written with the real clock
//      (the server has no other: Go reads it in ~400 places). Every API answer
//      the page reads is moved into scene time on the way (context.route,
//      shiftJson): an instant near the real now moves by SCENE_NOW - (the real
//      time the context started), and a time the page sends back (a share's
//      expiry, a date filter) moves the other way.
//   3. WHAT A PERSON READS is snapped to the hour around SCENE_NOW
//      (installSceneDisplay, before any page script): Intl.DateTimeFormat and
//      Date#toLocale* print an instant rounded to the hour from SCENE_NOW, and
//      Intl.RelativeTimeFormat prints anything under half an hour as "now".
//      Everything a scene creates lands within minutes of the context's start,
//      and the minutes differ between two runs; at the hour they do not.
//   4. THE FIXTURES' OWN TIMES are fixed instants before SCENE_NOW
//      (fixtureTime, pinTimes in fixtures.mjs), so a file written to disk reads
//      "3 days ago" in every run.
//
// SHOTS_REAL_CLOCK=1 turns 1-3 off, to tell a scene that breaks on the clock
// from one that breaks on its own.
//
// What it cannot reach: a time inside a WebSocket message (the realtime feed
// carries changes, not times a picture shows), and a time the page computes
// and prints without Intl (none known). Pure functions are exported for
// web/tests/deploy/shotsFixtures.test.ts.

import { readdirSync, statSync, utimesSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** The instant every scene's browser starts at: a Tuesday morning, UTC. */
export const SCENE_NOW = Date.parse('2026-09-15T10:30:00Z');

/** The time zone every scene's browser is in (newContext's `timezoneId`). */
export const SCENE_TZ = 'UTC';

/** What every browser context of a scene is created with, on top of its own options. */
export const SCENE_CONTEXT = Object.freeze({ timezoneId: SCENE_TZ });

/**
 * Which instants are moved. A server time is this run's when it lies from two
 * days before the context started to three years after (an expiry, a
 * licence); a time the page sends is scene time when it lies from 400 days
 * before SCENE_NOW (a "last year" filter) to three years after. Anything else
 * - a fixture's fixed time, Go's zero time, a byte count - is left as it is.
 */
export const WINDOW = Object.freeze({ realBefore: 2 * DAY, sceneBefore: 400 * DAY, after: 3 * 365 * DAY });

/**
 * The paths whose answers are passed on untouched: signed or session-bound.
 * ⚠ Of E2E only what is signed or bound to a session (the escrow challenge
 * and its use, a password change, the cleanup after an in-place
 * encryption): `/e2e/` as a whole also held the who-may-encrypt requests
 * and policy, and Admin → Encryption printed the day the scene ran beside
 * pictures dated September 15 (0.53.0).
 */
export const UNTOUCHED = [/\/api\/auth\//, /\/onlyoffice\//, /\/e2e\/(escrow|password-changed|cleanup)(\/|$)/];

/**
 * The hosts whose requests are moved: the scenes' own instances, on loopback.
 * ⚠ A request is moved by sending it again from this process, which resolves
 * names its own way: a host only the browser knows (realm.mjs maps one with
 * --host-resolver-rules) would not be found, so it is passed on untouched.
 */
export const SCENE_HOSTS = new Set(['127.0.0.1', 'localhost', '[::1]']);

const dayOf = (ms) => Math.floor(ms / DAY) * DAY;

/**
 * The two directions of one context, started (really) at `startMs`:
 * `toScene` for what the server answers, `toReal` for what the page sends.
 * Each takes an instant or a calendar day (UTC midnight, `day: true`) in
 * milliseconds and returns the moved one, or null when it is not to be moved.
 */
export function sceneShift(startMs, now = SCENE_NOW) {
  const shift = now - startMs;
  const days = dayOf(now) - dayOf(startMs);
  return {
    shift,
    toScene(ms, { day = false } = {}) {
      if (ms < startMs - WINDOW.realBefore || ms > startMs + WINDOW.after) return null;
      return ms + (day ? days : shift);
    },
    toReal(ms, { day = false } = {}) {
      if (ms < now - WINDOW.sceneBefore || ms > now + WINDOW.after) return null;
      return ms - (day ? days : shift);
    },
  };
}

/** An instant as a person reads it in a picture: rounded to the hour around `now`. */
export function snapForDisplay(ms, now = SCENE_NOW) {
  return now + Math.round((ms - now) / HOUR) * HOUR;
}

// ── values ──────────────────────────────────────────────────────────────────

const ISO = /^(\d{4})-(\d{2})-(\d{2})([T ])(\d{2}):(\d{2})(?::(\d{2})(\.\d+)?)?(Z|z|[+-]\d{2}(?::?\d{2})?)?$/;
const DATE_ONLY = /^(\d{4})-(\d{2})-(\d{2})$/;

/**
 * The keys whose numbers (epoch seconds or milliseconds) and calendar days are
 * times. A date-time string is one under any key; a bare number or a
 * `YYYY-MM-DD` only under such a key - a byte count is not a time, and a
 * folder named after a day is not one either.
 */
export const TIME_KEY =
  /(^|[_-])(at|on|ts|time|date|day|when|mtime|ctime|atime|modified|expires?|expiry|until|since|from|to|before|after|start|end|last|first|seen|checked|opened|created|updated|deleted|started|finished|issued|used|by)$|(At|On|Time|Date|Day|Ms|Mtime|Modified|Until|Since|Expires|Expiry|Seen|Used)$/;

const pad = (n, w = 2) => String(n).padStart(w, '0');

function isoParts(s) {
  const m = ISO.exec(s);
  if (!m) return null;
  const [, y, mo, d, sepChar, h, mi, se = '0', frac = '', zone] = m;
  // Milliseconds from the first three digits: Go writes nine, and 0.57 * 1000
  // is 569.99... in floating point.
  const msPart = frac ? Number(frac.slice(1, 4).padEnd(3, '0')) : 0;
  let ms = Date.UTC(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(se), msPart);
  if (zone && zone !== 'Z' && zone !== 'z') {
    const z = /^([+-])(\d{2}):?(\d{2})?$/.exec(zone);
    const minutes = Number(z[2]) * 60 + Number(z[3] ?? 0);
    ms -= (z[1] === '+' ? 1 : -1) * minutes * MINUTE;
  }
  return Number.isNaN(ms) ? null : { ms, zoned: !!zone, sep: sepChar };
}

function formatLike(ms, parts) {
  if (parts.zoned) return new Date(ms).toISOString();
  const d = new Date(ms);
  const date = `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
  return `${date}${parts.sep}${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`;
}

/**
 * One value, moved by `move` (sceneShift's toScene or toReal) when it is a
 * time: a date-time string anywhere, a calendar day or an epoch number under
 * a TIME_KEY. Anything else comes back as it was.
 */
export function shiftValue(value, move, key = '') {
  const timeKey = TIME_KEY.test(String(key));
  if (typeof value === 'string') {
    const parts = isoParts(value);
    if (parts) {
      const moved = move(parts.ms);
      return moved === null ? value : formatLike(moved, parts);
    }
    const day = timeKey ? DATE_ONLY.exec(value) : null;
    if (day) {
      const moved = move(Date.UTC(Number(day[1]), Number(day[2]) - 1, Number(day[3])), { day: true });
      return moved === null ? value : new Date(moved).toISOString().slice(0, 10);
    }
    return value;
  }
  if (typeof value === 'number' && timeKey && Number.isFinite(value)) {
    if (value >= 1e11 && value < 1e14) {
      const moved = move(value);
      return moved === null ? value : Math.round(moved);
    }
    if (value >= 1e9 && value < 1e11 && Number.isInteger(value)) {
      const moved = move(value * 1000);
      return moved === null ? value : Math.round(moved / 1000);
    }
  }
  return value;
}

/** shiftValue over a parsed JSON document; an array's items read their parent's key. */
export function shiftJson(value, move, key = '') {
  if (Array.isArray(value)) return value.map((v) => shiftJson(v, move, key));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = shiftJson(v, move, k);
    return out;
  }
  return shiftValue(value, move, key);
}

/** A URL's query moved by `move`; the same string when nothing in it is a time. */
export function shiftQuery(url, move) {
  const u = new URL(url);
  let changed = false;
  const next = new URLSearchParams();
  for (const [k, v] of u.searchParams) {
    // A number only where a number can be a time: "007" stays "007".
    const asNumber = TIME_KEY.test(k) && /^\d{9,14}$/.test(v);
    const moved = shiftValue(asNumber ? Number(v) : v, move, k);
    const text = asNumber ? String(moved) : moved;
    if (text !== v) changed = true;
    next.append(k, text);
  }
  if (!changed) return url;
  u.search = next.toString();
  return u.toString();
}

// ── the fixtures' own times ─────────────────────────────────────────────────

/**
 * A fixed instant for a fixture file, before SCENE_NOW: 1 to 40 days and 1 to
 * 9 whole hours earlier, chosen by the file's path, so the same file has the
 * same date in every run and two files rarely share one. Whole hours away
 * from SCENE_NOW: the display's snap leaves them as they are, and "3 days
 * ago" stays 3 days whether a page rounds or truncates.
 */
export function fixtureTime(rel, now = SCENE_NOW) {
  let h = 2166136261;
  for (const c of String(rel).replace(/\\/g, '/')) h = Math.imul(h ^ c.codePointAt(0), 16777619) >>> 0;
  const days = 1 + (h % 40);
  const hours = 1 + ((h >>> 8) % 9);
  return now - days * DAY - hours * HOUR;
}

/**
 * Sets the modification (and access) time of every file and folder under
 * `root` to its fixtureTime - folders after their contents, since writing a
 * file into a folder moves the folder's own time. Call it after the last
 * write and before the storage is synced.
 */
export function pinTimes(root, now = SCENE_NOW) {
  const relOf = (full) => relative(root, full).split(sep).join('/');
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, e.name);
      if (e.isDirectory()) walk(full);
      else if (e.isFile()) {
        const t = new Date(fixtureTime(relOf(full), now));
        utimesSync(full, t, t);
      }
    }
    const t = new Date(fixtureTime(relOf(dir) || '.', now));
    utimesSync(dir, t, t);
  };
  if (statSync(root).isDirectory()) walk(root);
}

// ── the browser ─────────────────────────────────────────────────────────────

/**
 * Runs in the page before any script of its own (addInitScript): what a
 * person reads is snapped to the hour around `now`. Self-contained -
 * Playwright sends it to the page as source.
 *
 * ⚠ Intl.DateTimeFormat is wrapped as a CONSTRUCTOR, not patched on its
 * prototype: Playwright's clock replaces `Intl` with one whose DateTimeFormat
 * hands out plain objects (they never reach the prototype), and whichever of
 * the two runs first, the wrapper sits in front of the formatter the page
 * gets. Date and Intl.RelativeTimeFormat keep their native prototypes under
 * the clock, so those are patched in place.
 */
export function installSceneDisplay(now) {
  const HOUR_MS = 3_600_000;
  const NEAR_MS = 1_800_000;
  const snap = (ms) => now + Math.round((ms - now) / HOUR_MS) * HOUR_MS;
  const snapArg = (d) => {
    if (d === undefined) return new Date(snap(Date.now()));
    const ms = d instanceof Date ? d.getTime() : typeof d === 'number' ? d : NaN;
    return Number.isFinite(ms) ? new Date(snap(ms)) : d;
  };
  const Base = Intl.DateTimeFormat;
  function SceneDateTimeFormat(...args) {
    const inner = new Base(...args);
    return {
      format: (d) => inner.format(snapArg(d)),
      formatToParts: (d) => inner.formatToParts(snapArg(d)),
      formatRange: (a, b) => inner.formatRange(snapArg(a), snapArg(b)),
      formatRangeToParts: (a, b) => inner.formatRangeToParts(snapArg(a), snapArg(b)),
      resolvedOptions: () => inner.resolvedOptions(),
    };
  }
  SceneDateTimeFormat.supportedLocalesOf = Base.supportedLocalesOf;
  SceneDateTimeFormat.prototype = Base.prototype;
  Intl.DateTimeFormat = SceneDateTimeFormat;
  for (const name of ['toLocaleString', 'toLocaleDateString', 'toLocaleTimeString']) {
    const original = Date.prototype[name];
    Date.prototype[name] = function (...args) {
      const ms = this.getTime();
      return original.apply(Number.isFinite(ms) ? new Date(snap(ms)) : this, args);
    };
  }
  const UNIT_MS = { second: 1000, minute: 60_000, hour: HOUR_MS, day: 86_400_000 };
  const RTF = Intl.RelativeTimeFormat && Intl.RelativeTimeFormat.prototype;
  if (RTF) {
    for (const name of ['format', 'formatToParts']) {
      const original = RTF[name];
      if (!original) continue;
      RTF[name] = function (value, unit) {
        const each = UNIT_MS[String(unit).replace(/s$/, '')];
        if (each && Math.abs(Number(value) * each) < NEAR_MS) return original.call(this, 0, 'second');
        return original.call(this, value, unit);
      };
    }
  }
}

/** Removed from an answer that is passed on with a body of its own. */
const DROP_HEADERS = new Set(['content-length', 'content-encoding', 'transfer-encoding', 'set-cookie', 'connection', 'keep-alive', 'etag', 'last-modified']);
/** Removed from a request: its length is the new body's, and the answer must come back whole, with a body to move. */
const DROP_REQUEST = new Set(['content-length', 'if-none-match', 'if-modified-since']);

/**
 * What a request of the page becomes on the real clock: the options of
 * route.fetch, or null for a request that is passed on untouched - one that
 * is not the page's own fetch (a picture, a video, a page), goes to a host
 * not in SCENE_HOSTS or to a path in UNTOUCHED.
 */
export function sceneRequest(req, move) {
  const kind = req.resourceType();
  if (kind !== 'fetch' && kind !== 'xhr') return null;
  const where = new URL(req.url());
  if (!SCENE_HOSTS.has(where.hostname) || UNTOUCHED.some((re) => re.test(where.pathname))) return null;
  const method = req.method();
  // ⚠ A body that is not JSON - a multipart upload above all - is passed on
  // untouched. Chromium does not hand the route a body that holds a File: its
  // postData comes without the file's bytes, so route.fetch sent the form
  // with an empty part and the server read nothing ("manifest: EOF", the
  // install review of defaultapps.mjs; 0.53.0). Such a request carries no
  // time of the page's to move, and its answer stays on the real clock.
  const type = req.headers()['content-type'] ?? '';
  if (method !== 'GET' && method !== 'HEAD' && type && !/json/i.test(type)) return null;
  const headers = {};
  for (const [k, v] of Object.entries(req.headers())) if (!DROP_REQUEST.has(k.toLowerCase())) headers[k] = v;
  const options = { url: shiftQuery(req.url(), move.toReal), headers, timeout: 0 };
  if (method !== 'GET' && method !== 'HEAD' && /json/i.test(headers['content-type'] ?? '')) {
    const raw = req.postData();
    if (raw) {
      try {
        options.postData = JSON.stringify(shiftJson(JSON.parse(raw), move.toReal));
      } catch {
        /* not JSON after all: sent as it is */
      }
    }
  }
  return { options, idempotent: method === 'GET' || method === 'HEAD' };
}

/** The answer the page gets: the server's, a JSON body moved to scene time. */
export async function sceneAnswer(res, move) {
  const headers = {};
  for (const [k, v] of Object.entries(res.headers())) if (!DROP_HEADERS.has(k.toLowerCase())) headers[k] = v;
  const raw = await res.body();
  let body = raw;
  if (/json/i.test(res.headers()['content-type'] ?? '') && raw.length) {
    try {
      body = JSON.stringify(shiftJson(JSON.parse(raw.toString('utf8')), move.toScene));
    } catch {
      body = raw;
    }
  }
  return { status: res.status(), headers, body };
}

/**
 * One request of the page through scene time. ⚠ It never throws: an error
 * in a route handler is an unhandled rejection, and that ends the scene's
 * process. Whatever cannot be moved is passed on as it is, and nothing that
 * may have changed something on the server is sent twice.
 */
export async function sceneRoute(route, move) {
  let plan = null;
  try {
    plan = sceneRequest(route.request(), move);
  } catch {
    plan = null;
  }
  if (!plan) return route.fallback().catch(() => {});
  let res;
  try {
    res = await route.fetch(plan.options);
  } catch {
    return (plan.idempotent ? route.fallback() : route.abort('failed')).catch(() => {});
  }
  try {
    return await route.fulfill(await sceneAnswer(res, move));
  } catch {
    return route.fulfill({ response: res }).catch(() => {});
  }
}

/**
 * Puts a browser context on the scene's clock: its Date starts at SCENE_NOW,
 * what it reads from the API is in scene time, and what a person reads is
 * snapped to the hour. Call it right after `browser.newContext({
 * ...SCENE_CONTEXT, ... })`, before the first page. SHOTS_REAL_CLOCK=1 leaves
 * the context on the real clock (its time zone stays SCENE_TZ).
 */
export async function stageClock(context, { now = SCENE_NOW } = {}) {
  if (process.env.SHOTS_REAL_CLOCK === '1') return null;
  const move = sceneShift(Date.now(), now);
  await context.clock.setSystemTime(now);
  await context.addInitScript(installSceneDisplay, now);
  await context.route('**/api/**', (route) => sceneRoute(route, move));
  return move;
}
