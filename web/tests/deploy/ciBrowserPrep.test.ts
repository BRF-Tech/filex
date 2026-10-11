// scripts/ci/browser-prep.mjs: what every CI browser part needs before the
// suite runs - the apps it installs and the build host's font map - in the
// one script GitHub's ci.yml and CircleCI's config both call (task #181).
//
// ⚠⚠ Why it is held here (CircleCI's first run, 2026-10-10): the config ran
// the Playwright suite with FILEX_REQUIRE_WASM_FIXTURE set and nothing had
// fetched the sign and convert apps, so every app spec failed to load ("the
// sign app's plugin.wasm is not built") and all four copies were red in
// seconds. GitHub fetched them in a script of its own, with a list of its
// own. What must hold:
//   - every app of e2e/helpers/app-locations.mjs is fetched, and nothing else:
//     the table is the list (the specs and the screenshots read it too);
//   - each lands where the specs will look, under the variable they read;
//   - the font map is the chain's FONTS_CONF, byte for byte;
//   - the variables reach the later steps of whichever CI runs it.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { APP_LOCATIONS, locateApp } from '../../../e2e/helpers/app-locations.mjs';
import { FONTS_CONF } from '../../../scripts/chain/run.mjs';
import { appPlan, envAssignments, fetchApps, writeFontsConf } from '../../../scripts/ci/browser-prep.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const SCRIPT = path.join(REPO, 'scripts', 'ci', 'browser-prep.mjs');

const temps: string[] = [];
const tmp = () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-prep-'));
  temps.push(d);
  return d;
};
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

type Spot = { env: string; repo: string; dataOnly?: boolean };
const SPOTS = Object.entries(APP_LOCATIONS) as Array<[string, Spot]>;

/** A fetch that answers every app file from memory, and records the URLs. */
function fakeFetch(answer: (url: string) => { status: number; body: string | Buffer }) {
  const urls: string[] = [];
  const impl = async (url: string) => {
    urls.push(url);
    const { status, body } = answer(url);
    const bytes = Buffer.from(body);
    return {
      ok: status >= 200 && status < 300,
      status,
      arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    };
  };
  return { urls, impl };
}

const manifestFor = (url: string) => JSON.stringify({ name: url.split('/')[4], version: '1.2.3', permissions: [] });
const good = (url: string) => ({ status: 200, body: url.endsWith('.json') ? manifestFor(url) : Buffer.from([0, 97, 115, 109]) });

describe('browser-prep apps: the table is the list', () => {
  it('plans one row per entry of app-locations.mjs, in its order, under its variable', () => {
    const plan = appPlan('/work/apps');
    expect(SPOTS.length).toBeGreaterThan(0);
    expect(plan.map((a: { name: string }) => a.name)).toEqual(SPOTS.map(([n]) => n));
    for (const [i, [name, spot]] of SPOTS.entries()) {
      const row = plan[i];
      expect(row.env).toBe(spot.env);
      expect(row.repo).toBe(spot.repo);
      expect(row.dir).toBe(path.resolve('/work/apps', name));
      if (spot.dataOnly) {
        // A language pack is its manifest: no build, no release asset.
        expect(row.files).toEqual([{ file: 'filex-app.json', url: `https://raw.githubusercontent.com/${spot.repo}/HEAD/filex-app.json` }]);
      } else {
        expect(row.files).toEqual([
          { file: 'plugin.wasm', url: `https://github.com/${spot.repo}/releases/latest/download/plugin.wasm` },
          { file: 'filex-app.json', url: `https://github.com/${spot.repo}/releases/latest/download/filex-app.json` },
        ]);
      }
    }
    // Both kinds are in the table: the apps with a module, and the packs.
    expect(SPOTS.some(([, s]) => s.dataOnly)).toBe(true);
    expect(SPOTS.some(([, s]) => !s.dataOnly)).toBe(true);
  });

  it('refuses an entry it could not fetch or hand on', () => {
    expect(() => appPlan('/w', { x: { env: 'X', dirs: [] } })).toThrow('x names no repository');
    expect(() => appPlan('/w', { x: { repo: 'o/x', dirs: [] } })).toThrow('x names no variable');
  });

  it('fetches every app where the specs will find it, and returns the variables they read', async () => {
    const dir = tmp();
    const { urls, impl } = fakeFetch(good);
    const lines: string[] = [];
    const vars = await fetchApps(dir, { fetchImpl: impl, log: (s: string) => lines.push(s) });
    expect(Object.keys(vars).sort()).toEqual(SPOTS.map(([, s]) => s.env).sort());
    expect(urls).toEqual(appPlan(dir).flatMap((a: { files: Array<{ url: string }> }) => a.files.map((f) => f.url)));
    for (const [name, spot] of SPOTS) {
      expect(vars[spot.env]).toBe(path.resolve(dir, name));
      const before = process.env[spot.env];
      process.env[spot.env] = vars[spot.env];
      try {
        expect(locateApp(name).present, `${name} is found by the specs' lookup`).toBe(true);
      } finally {
        if (before === undefined) delete process.env[spot.env];
        else process.env[spot.env] = before;
      }
      expect(lines.join('\n')).toContain(`${spot.repo} 1.2.3`);
    }
    // It leaves the variables as it found them: the CI hands them on.
    for (const [, spot] of SPOTS) expect(process.env[spot.env] ?? '').not.toBe(vars[spot.env]);
  });

  it('a missing release file, an empty one and a manifest that is not JSON are each red, naming the repository', async () => {
    const [name, spot] = SPOTS.find(([, s]) => !s.dataOnly)!;
    const cases: Array<[(url: string) => { status: number; body: string | Buffer }, string]> = [
      [(u) => (u.includes(spot.repo) && u.endsWith('plugin.wasm') ? { status: 404, body: 'x' } : good(u)), `${spot.repo}: no plugin.wasm (`],
      [(u) => (u.includes(spot.repo) && u.endsWith('plugin.wasm') ? { status: 200, body: '' } : good(u)), 'an empty file'],
      [(u) => (u.includes(spot.repo) && u.endsWith('.json') ? { status: 200, body: '{nope' } : good(u)), `${spot.repo}: its filex-app.json is not JSON`],
    ];
    for (const [answer, said] of cases) {
      const { impl } = fakeFetch(answer);
      await expect(fetchApps(tmp(), { fetchImpl: impl, log: () => undefined, tries: 1 }), name).rejects.toThrow(said);
    }
  }, 30_000);
});

describe('browser-prep fonts: the build host\'s map', () => {
  it('writes the chain\'s FONTS_CONF where fontconfig looks with XDG_CONFIG_HOME', () => {
    const dir = tmp();
    const vars = writeFontsConf(dir);
    expect(vars).toEqual({ XDG_CONFIG_HOME: path.resolve(dir) });
    expect(fs.readFileSync(path.join(dir, 'fontconfig', 'fonts.conf'), 'utf8')).toBe(FONTS_CONF);
    // Every alias prefers the face the script makes sure is installed.
    const prefer = [...FONTS_CONF.matchAll(/<prefer><family>([^<]+)<\/family><\/prefer>/g)].map((m) => m[1]);
    expect(prefer.length).toBe(4);
    expect(new Set(prefer)).toEqual(new Set(['Liberation Sans']));
  });
});

describe('browser-prep: the variables reach the steps after it', () => {
  const vars = { FILEX_SIGN_APP_DIR: '/tmp/apps/sign', XDG_CONFIG_HOME: "/tmp/it's" };

  it('GitHub: NAME=value lines in $GITHUB_ENV', () => {
    expect(envAssignments(vars, { GITHUB_ENV: '/r/env', BASH_ENV: '/b' })).toEqual({
      file: '/r/env',
      text: "FILEX_SIGN_APP_DIR=/tmp/apps/sign\nXDG_CONFIG_HOME=/tmp/it's\n",
    });
  });

  it('CircleCI: export lines in $BASH_ENV, which every later step sources', () => {
    expect(envAssignments(vars, { BASH_ENV: '/b' })).toEqual({
      file: '/b',
      text: "export FILEX_SIGN_APP_DIR='/tmp/apps/sign'\nexport XDG_CONFIG_HOME='/tmp/it'\\''s'\n",
    });
  });

  it('elsewhere: the export lines, to print', () => {
    expect(envAssignments({ A: '/x' }, {})).toEqual({ file: '', text: "export A='/x'\n" });
  });

  it('refuses a value with a line break (it would end the assignment early)', () => {
    expect(() => envAssignments({ A: 'x\ny' }, { BASH_ENV: '/b' })).toThrow('A: a value with a line break');
  });

  it('the CLI says how to call it, and exits 2 on a wrong call', () => {
    for (const args of [[], ['apps'], ['nope', '/x'], ['apps', '/x', 'extra']]) {
      const r = spawnSync(process.execPath, [SCRIPT, ...args], { encoding: 'utf8' });
      expect(r.status, args.join(' ')).toBe(2);
      expect(r.stderr).toContain('usage: node scripts/ci/browser-prep.mjs apps <dir> | fonts <dir>');
    }
  });
});
