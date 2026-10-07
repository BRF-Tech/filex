// The screenshot scripts have to be able to seed the instance they photograph.
//
// ⚠⚠ They could not, for five releases. `sync_mode: 'manual'` was a valid value
// once; `ValidateSyncMode` arrived on 2026-09-07 and started refusing it, and
// from that day `node e2e/shots/capture.mjs` died on its first API call with
// `invalid sync_mode "manual"`. The release process has a numbered step that
// says to retake the screenshots, and v0.35.0, v0.36.0, v0.37.0, v0.38.0 and
// v0.38.1 all shipped over a script that could not take one.
//
// A screenshot script is not covered by any suite — it needs a browser and a
// running server — so nothing said a word. This test is the cheap half: it
// does not run the scripts, it checks that the values they hardcode are still
// values the server accepts, which is the only way they have ever broken.

import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import { afterAll, describe, expect, it } from 'vitest';

import {
  SCENE_CONTEXT,
  SCENE_NOW,
  SCENE_TZ,
  TIME_KEY,
  UNTOUCHED,
  WINDOW,
  fixtureTime,
  installSceneDisplay,
  pinTimes,
  sceneAnswer,
  sceneRequest,
  sceneShift,
  shiftJson,
  shiftQuery,
  shiftValue,
  snapForDisplay,
} from '../../../e2e/shots/clock.mjs';
import { seedFixtures, writeOfficeFile } from '../../../e2e/shots/fixtures.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');

/** The modes the sync worker implements, read from the Go source that decides. */
function implementedSyncModes(): string[] {
  const src = readFileSync(
    path.join(REPO, 'backend', 'internal', 'model', 'storage.go'),
    'utf8',
  );
  // func SyncModes() []SyncMode { return []SyncMode{SyncModePoll, …} }
  const body = /func SyncModes\(\)[^{]*\{[^}]*\{([^}]*)\}/.exec(src);
  expect(body, 'SyncModes() no longer has the shape this test reads').not.toBeNull();
  const names = body![1].split(',').map((s) => s.trim()).filter(Boolean);
  return names.map((name) => {
    const lit = new RegExp(`${name}\\s+SyncMode\\s*=\\s*"([^"]+)"`).exec(src);
    expect(lit, `no string literal for ${name}`).not.toBeNull();
    return lit![1];
  });
}

/** Every `sync_mode: '…'` a fixture script hardcodes, with the file it is in. */
function hardcodedSyncModes(): Array<{ file: string; value: string }> {
  // e2e/tests too: 70-multi-storage.spec.ts still seeded 'manual' in 0.41.0's
  // cycle, a week after the scripts here were fixed. It only runs with S3_*
  // set, so it rotted where no run would ever show it.
  const dirs = ['shots', 'helpers', 'tests'].map((d) => path.join(REPO, 'e2e', d));
  const out: Array<{ file: string; value: string }> = [];
  for (const dir of dirs) {
    for (const name of readdirSync(dir)) {
      if (!/\.(mjs|ts)$/.test(name)) continue;
      const src = readFileSync(path.join(dir, name), 'utf8');
      for (const m of src.matchAll(/sync_?[Mm]ode:\s*['"]([^'"]+)['"]/g)) {
        out.push({ file: path.relative(REPO, path.join(dir, name)), value: m[1] });
      }
    }
  }
  return out;
}

describe('the screenshot + e2e fixtures can still seed a storage', () => {
  it('every hardcoded sync_mode is one the server implements', () => {
    const valid = implementedSyncModes();
    expect(valid.length).toBeGreaterThan(0);

    const used = hardcodedSyncModes();
    expect(used.length, 'no fixture sets sync_mode any more — has the seed moved?')
      .toBeGreaterThan(0);

    for (const { file, value } of used) {
      expect(
        valid,
        `${file} seeds a storage with sync_mode "${value}", which the server refuses — ` +
          `the script dies on its first call and the release step that runs it reports nothing`,
      ).toContain(value);
    }
  });
});

// ───────────────────────────────────────────────────────────────────────────
// …and can still FIND what they photograph
//
// ⚠⚠ The second way a shot script rots, and the one that stopped the v0.43.0
// run: a control MOVED. `notifications.mjs` waited for `notif-browser-ask` on
// the admin Notifications page; a person's own preferences had been gathered
// into the user settings dialog (`user-settings-browser-ask`) and the old test
// id was emitted nowhere at all. The script sat on a 20-second timeout, failed,
// and `pnpm shots` stops at the first failure — so four scenes after it were
// never taken either, a fortnight of UI work away from the release that needed
// their pictures.
//
// ⚠ This does not run a browser. It asks the cheap half of the question: is
// every test id these scripts WAIT FOR still written somewhere in the
// interface? A test id that appears in no `.vue` and no `.ts` cannot be on any
// screen, whatever the layout does.
//
// ⚠ Composed ids are matched by their SHAPE, not by string equality: the
// interface writes `` `${testidPrefix}-action-${a.id}` `` and a script asks for
// `plugin-view-action-submit`, so every `${…}` in a declaration becomes `.+`.
// That is deliberately loose — it proves the id is still CONSTRUCTIBLE, which
// is exactly what a rename breaks and what a re-render does not.
//
// ⚠⚠ What it therefore cannot tell you: whether the variable half is the right
// word. An APP supplies its own action ids, so `plugin-view-action-next` is a
// perfectly well-shaped id for a button labelled "Next" that filex-convert
// calls `submit` — this gate passed it and the run met it as a 20-second
// timeout (2026-09-23). Find a footer button by its role in the footer, not by
// guessing its id from its label.

const UI_ROOTS = [
  path.join(REPO, 'web', 'src'),
  path.join(REPO, 'packages', 'core', 'src'),
  path.join(REPO, 'packages', 'webcomponent', 'src'),
  path.join(REPO, 'packages', 'react', 'src'),
];

function sourceFiles(dir: string): string[] {
  if (!existsSync(dir)) return [];
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) out.push(...sourceFiles(full));
    else if (/\.(vue|ts|tsx|js|mjs)$/.test(name)) out.push(full);
  }
  return out;
}

/** Every test id the interface can write, as a pattern. */
function emittedTestIds(): Array<{ decl: string; re: RegExp }> {
  // `data-testid="x"`, `:data-testid="`a-${b}`"`, the `'data-testid':` key of
  // a row-attrs object, the `testid` a page hands to one of core's controls,
  // and the `testid-prefix` a component hands to a child that appends to it.
  //
  // ⚠ Since #160 every list and every row of choice buttons is core's
  // (ChoiceSelect, ChoiceButtons), and a page names one by a PROP: `testid="x"`
  // is the combobox's `data-testid="x"` (its list `x-list`, an option
  // `x-option-<value>`), `testid-prefix="p"` is each button's `p-<value>`.
  // Read as plain attributes they were invisible, and three scenes that were
  // right (guide-protocol, share-max-downloads, share-add-level-owner) read as
  // waiting for an id nothing writes.
  const attr =
    /['"]?(:?data-testid|:?testid-prefix|testidPrefix|(?<![\w-]):?testid)['"]?\s*[=:]\s*(?:"([^"]*)"|'([^']*)'|`([^`]*)`)/g;
  const decls = new Set<string>();
  for (const root of UI_ROOTS) {
    for (const file of sourceFiles(root)) {
      const src = readFileSync(file, 'utf8');
      for (const m of src.matchAll(attr)) {
        const name = m[1]!.replace(/^:/, '');
        let v = (m[2] ?? m[3] ?? m[4] ?? '').trim();
        // A Vue dynamic attribute quotes an expression, which is usually a
        // template literal: `:data-testid="`a-${b}`"` captures the backticks.
        if (v.length > 1 && v.startsWith('`') && v.endsWith('`')) v = v.slice(1, -1);
        if (!v) continue;
        decls.add(v);
        if (name === 'testid') {
          decls.add(`${v}-list`);
          decls.add(`${v}-option-\${value}`);
        } else if (name !== 'data-testid') {
          decls.add(`${v}-\${value}`);
        }
      }
      // A DataTable row's Actions control: `:row-actions-test-id="(row) =>
      // `x-${row.id}`"` names the control, and core RowActions names each
      // entry of its menu `<that id>-<action key>`. Every admin table row is
      // reached this way (e2e 178, 179 and the app-permission scene open the
      // Roles table's row menus), so a scene that does must not read as
      // waiting for an id nothing writes.
      for (const m of src.matchAll(/:row-actions-test-id="[^"]*?`([^`]+)`/g)) {
        decls.add(m[1]);
        decls.add(`${m[1]}-\${key}`);
      }
    }
  }
  return [...decls].map((decl) => ({
    decl,
    re: new RegExp(
      `^${decl
        .split(/\$\{[^}]*\}/)
        .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
        .join('.+')}$`,
    ),
  }));
}

/** Every test id a shot script waits for, clicks or reads — literals only. */
function testIdsShotScriptsWaitFor(): Array<{ file: string; id: string }> {
  const dir = path.join(REPO, 'e2e', 'shots');
  const asked = /(?:data-testid=\\?["']([^"'\]]+)\\?["']|getByTestId\(\s*[`'"]([^`'"]+)[`'"])/g;
  const out: Array<{ file: string; id: string }> = [];
  for (const name of readdirSync(dir)) {
    if (!name.endsWith('.mjs')) continue;
    for (const line of readFileSync(path.join(dir, name), 'utf8').split('\n')) {
      // ⚠ A `.count()` line is usually the opposite claim — driveshell.mjs
      // asserts that `sidenav-upload` is NOT on an embed that hid it. An id
      // nothing writes is the POINT there, so those lines are left alone.
      if (line.includes('.count()')) continue;
      for (const m of line.matchAll(asked)) {
        const id = m[1] ?? m[2] ?? '';
        // `${…}` here is the SCRIPT's own interpolation; what it resolves to
        // is not knowable without running it.
        if (!id || id.includes('${')) continue;
        out.push({ file: `e2e/shots/${name}`, id });
      }
    }
  }
  return out;
}

describe('the screenshot scripts can still find what they photograph', () => {
  const emitted = emittedTestIds();
  const asked = testIdsShotScriptsWaitFor();

  it('there is something to compare on both sides', () => {
    // Without this, a regex that stopped matching would report zero problems.
    expect(emitted.length, 'no data-testid found in web/src or packages/*/src').toBeGreaterThan(100);
    expect(asked.length, 'no shot script waits for a test id any more — has the scan broken?').toBeGreaterThan(20);
  });

  it.each(asked.map((a) => [`${a.file} → ${a.id}`, a] as const))(
    '%s is still written somewhere in the interface',
    (_label, { file, id }) => {
      expect(
        emitted.some((e) => e.re.test(id)),
        `${file} waits for [data-testid="${id}"] and nothing in web/src or packages/*/src writes that id. ` +
          'The control was renamed, moved or removed: the script will sit on its timeout, fail, and take ' +
          'every scene after it down with it (pnpm shots stops at the first failure). Point the script at ' +
          'the control as it is now.',
      ).toBe(true);
    },
  );
});

// ───────────────────────────────────────────────────────────────────────────
// …and take every picture at one clock (task #176, the owner's decision of
// 2026-10-06)
//
// ⚠⚠ Why. `pnpm shots` compares every picture with the published one pixel
// by pixel and shows a person only the ones that moved. A picture with a date
// on it moved every night: each upload's time, the day in a date picker, a
// notification's "2 minutes ago". e2e/shots/clock.mjs puts every scene on one
// clock - the browser starts at SCENE_NOW in UTC, the API's times are moved
// into it, what a person reads is snapped to the hour, the fixtures' files
// carry fixed dates. These tests hold each part without a browser; the first
// one fails on a script that makes a browser context off the clock.

const DAY_MS = 86_400_000;
const HOUR_MS = 3_600_000;
const SHOTS = path.join(REPO, 'e2e', 'shots');

describe('the scenes run on one clock', () => {
  it('is Tuesday, September 15, 2026, 10:30 UTC, and every browser context of every shot script is on it', () => {
    expect(SCENE_NOW).toBe(Date.parse('2026-09-15T10:30:00Z'));
    expect(SCENE_TZ).toBe('UTC');
    expect(SCENE_CONTEXT).toEqual({ timezoneId: 'UTC' });
    let contexts = 0;
    for (const name of readdirSync(SHOTS).filter((f) => f.endsWith('.mjs') && f !== 'clock.mjs')) {
      const src = readFileSync(path.join(SHOTS, name), 'utf8');
      const made = (src.match(/\bbrowser\.newContext\(/g) ?? []).length;
      const staged = (src.match(/\bawait stageClock\(/g) ?? []).length;
      const zoned = (src.match(/\bbrowser\.newContext\(\{\s*\.\.\.SCENE_CONTEXT\b/g) ?? []).length;
      expect(staged, `e2e/shots/${name} makes ${made} browser context(s) and puts ${staged} on the scene clock (stageClock)`).toBe(made);
      expect(zoned, `e2e/shots/${name}: a browser context without ...SCENE_CONTEXT is in the machine's time zone`).toBe(made);
      contexts += made;
    }
    expect(contexts, 'no shot script makes a browser context any more - has the scan broken?').toBeGreaterThan(8);
  });

  it('starts the browser at SCENE_NOW and lets it run - a frozen clock swallows every click after the first open', () => {
    // FilePane's open guard ignores a click within 500 ms of an open, measured
    // with Date.now(): on a clock that never moves that is every later click.
    const clock = readFileSync(path.join(SHOTS, 'clock.mjs'), 'utf8');
    expect(clock).toContain('await context.clock.setSystemTime(now);');
    expect(clock).not.toMatch(/clock\.setFixedTime\(/);
    expect(clock).toContain("await context.route('**/api/**', (route) => sceneRoute(route, move));");
    expect(clock).toContain('await context.addInitScript(installSceneDisplay, now);');
    const pane = readFileSync(path.join(REPO, 'packages', 'core', 'src', 'components', 'FilePane.vue'), 'utf8');
    expect(pane).toMatch(/return Date\.now\(\) - openedAt < OPEN_GUARD_MS;/);
  });

  describe('the server times move into scene time, and the page time back', () => {
    const start = Date.parse('2026-10-07T01:30:00Z');
    const move = sceneShift(start);

    it('by the distance between SCENE_NOW and the moment the context started', () => {
      expect(move.toScene(start)).toBe(SCENE_NOW);
      expect(move.toScene(start - 5 * 60_000)).toBe(SCENE_NOW - 5 * 60_000);
      expect(move.toScene(start + 365 * DAY_MS)).toBe(SCENE_NOW + 365 * DAY_MS);
      expect(move.toReal(SCENE_NOW + 7 * DAY_MS)).toBe(start + 7 * DAY_MS);
      expect(move.toReal(SCENE_NOW - 30 * DAY_MS)).toBe(start - 30 * DAY_MS);
      // A calendar day moves by whole days.
      expect(move.toScene(Date.UTC(2026, 9, 7), { day: true })).toBe(Date.UTC(2026, 8, 15));
      expect(move.toReal(Date.UTC(2026, 8, 15), { day: true })).toBe(Date.UTC(2026, 9, 7));
    });

    it('and not a time that is not this run: a fixture date, the zero time, years away', () => {
      expect(move.toScene(fixtureTime('Photos/aurora.png'))).toBeNull();
      expect(move.toScene(Date.parse('0001-01-01T00:00:00Z'))).toBeNull();
      expect(move.toScene(start - WINDOW.realBefore - 1)).toBeNull();
      expect(move.toScene(start + WINDOW.after + 1)).toBeNull();
      expect(move.toReal(Date.parse('2020-01-01T00:00:00Z'))).toBeNull();
    });

    it('in an API answer: date-time strings anywhere, numbers and days only under a time key', () => {
      const answer = {
        files: [
          {
            name: 'report.pdf',
            size: 1_790_000_000_000,
            last_modified: start - 60_000,
            created_at: '2026-10-07T01:29:00.123456789Z',
            indexed: '2026-10-07 01:29:00',
            folder: '2026-10-07',
          },
        ],
        expires_at: Math.floor((start + 7 * DAY_MS) / 1000),
        day: '2026-10-07',
        count: Math.floor(start / 1000),
        taken: '2026-09-12T08:30:00Z',
      };
      const moved = shiftJson(answer, move.toScene);
      expect(moved.files[0]).toEqual({
        name: 'report.pdf',
        size: 1_790_000_000_000,
        last_modified: SCENE_NOW - 60_000,
        created_at: new Date(SCENE_NOW - 60_000 + 123).toISOString(),
        indexed: '2026-09-15 10:29:00',
        folder: '2026-10-07',
      });
      expect(moved.expires_at).toBe(Math.floor((SCENE_NOW + 7 * DAY_MS) / 1000));
      expect(moved.day).toBe('2026-09-15');
      expect(moved.count).toBe(answer.count);
      expect(moved.taken).toBe('2026-09-12T08:30:00Z');
      expect(TIME_KEY.test('size')).toBe(false);
      expect(TIME_KEY.test('updatedAt')).toBe(true);
      expect(TIME_KEY.test('format')).toBe(false);
      expect(shiftValue('2026-10-07T04:29:00+03:00', move.toScene)).toBe(new Date(SCENE_NOW - 60_000).toISOString());
    });

    it('in a query: only what is a time, and the same URL when nothing is', () => {
      const url = 'http://127.0.0.1:5212/api/admin/audit?since=2026-09-08T10:30:00.000Z&path=demo%3A%2F%2FDocuments&page=007';
      const real = new URL(shiftQuery(url, move.toReal));
      expect(real.searchParams.get('since')).toBe(new Date(start - 7 * DAY_MS).toISOString());
      expect(real.searchParams.get('path')).toBe('demo://Documents');
      expect(real.searchParams.get('page')).toBe('007');
      const plain = 'http://127.0.0.1:5212/api/files/manager?action=index&path=demo%3A%2F%2F&limit=500';
      expect(shiftQuery(plain, move.toReal)).toBe(plain);
    });

    it("moves the page's request out of scene time, and passes the signed and session doors untouched", () => {
      const req = (url: string, over: Record<string, unknown> = {}) => ({
        resourceType: () => 'fetch',
        url: () => url,
        method: () => 'POST',
        headers: () => ({ 'content-type': 'application/json', 'content-length': '99', 'if-none-match': '"x"', authorization: 'Bearer t' }),
        postData: () => JSON.stringify({ expires_at: new Date(SCENE_NOW + 7 * DAY_MS).toISOString(), name: 'Q3' }),
        ...over,
      });
      const plan = sceneRequest(req('http://127.0.0.1:5212/api/shares'), move);
      expect(plan.idempotent).toBe(false);
      expect(plan.options.timeout).toBe(0);
      expect(plan.options.headers).toEqual({ 'content-type': 'application/json', authorization: 'Bearer t' });
      expect(JSON.parse(plan.options.postData)).toEqual({ expires_at: new Date(start + 7 * DAY_MS).toISOString(), name: 'Q3' });
      expect(sceneRequest(req('http://127.0.0.1:5212/api/auth/login'), move)).toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/onlyoffice/config?path=a'), move)).toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/thumb?path=a', { resourceType: () => 'image' }), move)).toBeNull();
      // A host only the browser resolves (realm.mjs): sending it again from Node would not find it.
      expect(sceneRequest(req('http://files.acme.test:5212/api/shares'), move)).toBeNull();
      expect(sceneRequest(req('http://localhost:5212/api/shares'), move)).not.toBeNull();
      // A body that is not JSON goes on untouched: Chromium gives the route a
      // multipart body without its files' bytes, and route.fetch would send
      // an empty part (defaultapps.mjs's install review, "manifest: EOF", 0.53).
      const multipart = { 'content-type': 'multipart/form-data; boundary=x' };
      expect(sceneRequest(req('http://127.0.0.1:5212/api/admin/app-plugins?dry_run=1', { headers: () => multipart }), move)).toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/upload', { headers: () => ({ 'content-type': 'application/octet-stream' }) }), move)).toBeNull();
      // A JSON body, and a request with none, are still moved.
      expect(sceneRequest(req('http://127.0.0.1:5212/api/shares', { headers: () => ({}), postData: () => null }), move)).not.toBeNull();
      // Of E2E, only the signed and session-bound doors: the requests and the
      // policy carry times a picture prints (Admin → Encryption, 0.53).
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/e2e/escrow/challenge'), move)).toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/e2e/cleanup'), move)).toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/admin/e2e/requests', { method: () => 'GET', postData: () => null }), move)).not.toBeNull();
      expect(sceneRequest(req('http://127.0.0.1:5212/api/files/e2e/requests'), move)).not.toBeNull();
      expect(UNTOUCHED.length).toBeGreaterThan(2);
    });

    it("moves the server's answer into scene time, with headers that fit the new body", async () => {
      const body = Buffer.from(JSON.stringify({ created_at: new Date(start - 120_000).toISOString(), name: 'x' }));
      const res = {
        status: () => 200,
        headers: () => ({
          'content-type': 'application/json; charset=utf-8',
          'content-length': String(body.length),
          'content-encoding': 'gzip',
          'set-cookie': 'a=b',
          etag: '"1"',
          'x-request-id': 'r1',
        }),
        body: async () => body,
      };
      const out = await sceneAnswer(res, move);
      expect(out.status).toBe(200);
      expect(out.headers).toEqual({ 'content-type': 'application/json; charset=utf-8', 'x-request-id': 'r1' });
      expect(JSON.parse(String(out.body))).toEqual({ created_at: new Date(SCENE_NOW - 120_000).toISOString(), name: 'x' });
      const png = Buffer.from([0x89, 0x50, 0x4e, 0x47]);
      const image = await sceneAnswer({ status: () => 200, headers: () => ({ 'content-type': 'image/png' }), body: async () => png }, move);
      expect(image.body).toBe(png);
    });
  });

  describe('what a person reads is snapped to the hour around SCENE_NOW', () => {
    const DATE_TIME = "{ timeZone: 'UTC', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }";
    const native = (ms: number) =>
      new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(ms));
    // ⚠ formatToParts is held to formatToParts, not to format: V8 turns the
    // narrow no-break space ICU 72+ puts before "AM" into a plain space in
    // format() only, so the two differ by that one character on the same Node
    // (the chain's Node, 2026-10-06: format() had the plain space, formatToParts the other).
    const nativeParts = (ms: number) =>
      new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })
        .formatToParts(new Date(ms))
        .map((p) => p.value)
        .join('');
    const page = (before = '') => {
      const ctx = vm.createContext({});
      vm.runInContext(`${before};(${installSceneDisplay.toString()})(${SCENE_NOW});`, ctx);
      return (code: string) => vm.runInContext(code, ctx);
    };
    // How Playwright's clock replaces Intl (playwright-core 1.59.1, clockSource
    // createIntl): a DateTimeFormat that hands out plain objects.
    const PLAYWRIGHT_CLOCK = `(() => {
      const N = Intl; const C = {};
      for (const k of Object.getOwnPropertyNames(N)) C[k] = N[k];
      C.DateTimeFormat = function (...a) {
        const r = new N.DateTimeFormat(...a);
        return { format: (d) => r.format(d || Date.now()), formatToParts: (d) => r.formatToParts(d || Date.now()), resolvedOptions: () => r.resolvedOptions() };
      };
      C.DateTimeFormat.prototype = Object.create(N.DateTimeFormat.prototype);
      globalThis.Intl = C;
    })()`;

    for (const [label, before] of [['on its own', ''], ["under Playwright's clock", PLAYWRIGHT_CLOCK]] as const) {
      it(`prints a date within half an hour of SCENE_NOW as SCENE_NOW (${label})`, () => {
        const run = page(before);
        for (const minutes of [-25, -4, 0, 9, 29]) {
          expect(run(`new Intl.DateTimeFormat('en-US', ${DATE_TIME}).format(new Date(${SCENE_NOW + minutes * 60_000}))`), `${minutes} min`).toBe(native(SCENE_NOW));
        }
        expect(run(`new Intl.DateTimeFormat('en-US', ${DATE_TIME}).format(new Date(${SCENE_NOW + 7 * DAY_MS + 3 * 60_000}))`)).toBe(native(SCENE_NOW + 7 * DAY_MS));
        const parts = run(`new Intl.DateTimeFormat('en-US', ${DATE_TIME}).formatToParts(new Date(${SCENE_NOW + 11 * 60_000})).map((p) => p.value).join('')`);
        expect(parts).toBe(nativeParts(SCENE_NOW));
      });
    }

    it('in Date#toLocaleString too, and "x seconds/minutes ago" under half an hour reads "now"', () => {
      const run = page();
      expect(run(`new Date(${SCENE_NOW + 9 * 60_000}).toLocaleString('en-US', { timeZone: 'UTC' })`)).toBe(new Date(SCENE_NOW).toLocaleString('en-US', { timeZone: 'UTC' }));
      const now = new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(0, 'second');
      expect(run(`new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(-12, 'second')`)).toBe(now);
      expect(run(`new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(-25, 'minutes')`)).toBe(now);
      expect(run(`new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(-3, 'day')`)).toBe(new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(-3, 'day'));
      expect(run(`new Date('nope').toLocaleString('en-US')`)).toBe('Invalid Date');
    });

    it("leaves the fixtures' dates as they are: whole hours from SCENE_NOW", () => {
      for (const rel of ['README.md', 'Photos/aurora.png', 'Documents/Q3 budget.xlsx', '.']) {
        const t = fixtureTime(rel);
        expect(snapForDisplay(t)).toBe(t);
        expect((SCENE_NOW - t) % HOUR_MS).toBe(0);
      }
    });
  });

  describe("the fixtures' files are dated the same in every run", () => {
    const root = mkdtempSync(path.join(tmpdir(), 'filex-shots-clock-'));
    afterAll(() => rmSync(root, { recursive: true, force: true }));

    it('by their path, 1 to 40 days and 1 to 9 hours before SCENE_NOW', () => {
      const t = fixtureTime('Documents/budget.csv');
      expect(fixtureTime('Documents/budget.csv')).toBe(t);
      expect(fixtureTime('Documents\\budget.csv')).toBe(t);
      for (const rel of ['a', 'b/c.txt', 'Photos/ocean.png', 'notes.txt']) {
        const before = SCENE_NOW - fixtureTime(rel);
        expect(before).toBeGreaterThanOrEqual(DAY_MS + HOUR_MS);
        expect(before).toBeLessThanOrEqual(40 * DAY_MS + 9 * HOUR_MS);
      }
    });

    it('pinTimes dates every file and folder; seedFixtures and writeOfficeFile date their own', () => {
      const own = path.join(root, 'own');
      mkdirSync(path.join(own, 'Docs'), { recursive: true });
      writeFileSync(path.join(own, 'Docs', 'a.txt'), 'a');
      writeFileSync(path.join(own, 'b.txt'), 'b');
      pinTimes(own);
      expect(statSync(path.join(own, 'Docs', 'a.txt')).mtimeMs).toBe(fixtureTime('Docs/a.txt'));
      expect(statSync(path.join(own, 'b.txt')).mtimeMs).toBe(fixtureTime('b.txt'));
      expect(statSync(path.join(own, 'Docs')).mtimeMs).toBe(fixtureTime('Docs'));
      expect(statSync(own).mtimeMs).toBe(fixtureTime('.'));

      const seeded = path.join(root, 'seeded');
      seedFixtures(seeded);
      expect(statSync(path.join(seeded, 'README.md')).mtimeMs).toBe(fixtureTime('README.md'));
      expect(statSync(path.join(seeded, 'Photos', 'aurora.png')).mtimeMs).toBe(fixtureTime('Photos/aurora.png'));
      const docx = path.join(seeded, 'Documents', 'Proposal.docx');
      writeOfficeFile(docx);
      expect(statSync(docx).mtimeMs).toBe(fixtureTime('Proposal.docx'));
    });
  });
});
