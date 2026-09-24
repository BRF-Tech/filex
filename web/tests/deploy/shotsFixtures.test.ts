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

import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

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
  // a row-attrs object, and the `testid-prefix` a component hands to a child
  // that appends to it.
  const attr =
    /['"]?(?::?data-testid|:?testid-prefix|testidPrefix)['"]?\s*[=:]\s*(?:"([^"]*)"|'([^']*)'|`([^`]*)`)/g;
  const decls = new Set<string>();
  for (const root of UI_ROOTS) {
    for (const file of sourceFiles(root)) {
      const src = readFileSync(file, 'utf8');
      for (const m of src.matchAll(attr)) {
        let v = (m[1] ?? m[2] ?? m[3] ?? '').trim();
        // A Vue dynamic attribute quotes an expression, which is usually a
        // template literal: `:data-testid="`a-${b}`"` captures the backticks.
        if (v.length > 1 && v.startsWith('`') && v.endsWith('`')) v = v.slice(1, -1);
        if (v) decls.add(v);
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
